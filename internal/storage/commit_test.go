package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// setting returns a transaction that stores key=value, then fails with err
// (nil: succeeds).
func setting(key string, err error) func(context.Context, *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, 'v')`, key); e != nil {
			return e
		}
		return err
	}
}

func request(ctx context.Context, fn func(context.Context, *sql.Tx) error) *txRequest {
	return &txRequest{ctx: ctx, fn: fn, done: make(chan error, 1)}
}

func hasSetting(t *testing.T, s *Store, key string) bool {
	t.Helper()
	_, ok, err := s.Setting(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestBatchUndoesOnlyTheFailedMember(t *testing.T) {
	s := openTest(t, t.TempDir())
	boom := errors.New("boom")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	batch := []*txRequest{
		request(context.Background(), setting("a", nil)),
		request(context.Background(), setting("b", boom)),
		request(cancelled, setting("c", nil)),
		request(context.Background(), setting("d", nil)),
	}
	s.commitBatch(batch)

	want := []error{nil, boom, context.Canceled, nil}
	for i, req := range batch {
		if err := <-req.done; !errors.Is(err, want[i]) || (want[i] == nil && err != nil) {
			t.Errorf("member %d: %v, want %v", i, err, want[i])
		}
	}
	for key, want := range map[string]bool{"a": true, "b": false, "c": false, "d": true} {
		if got := hasSetting(t, s, key); got != want {
			t.Errorf("setting %s stored = %v, want %v", key, got, want)
		}
	}
}

func TestAbortedBatchRetriesTheRest(t *testing.T) {
	s := openTest(t, t.TempDir())
	// Releasing the savepoint itself leaves nothing to roll back to: the
	// batch cannot isolate this member's failure and must be abandoned.
	breaker := func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `RELEASE member`); err != nil {
			return err
		}
		return errors.New("failed after breaking the savepoint")
	}
	batch := []*txRequest{
		request(context.Background(), setting("ran-before", nil)),
		request(context.Background(), breaker),
		request(context.Background(), setting("after-1", nil)),
		request(context.Background(), setting("after-2", nil)),
	}
	s.commitBatch(batch)

	for i, req := range batch {
		err := <-req.done
		if i <= 1 && err == nil {
			t.Errorf("member %d succeeded though its transaction was abandoned", i)
		}
		if i > 1 && err != nil {
			t.Errorf("member %d: %v, want a successful retry", i, err)
		}
	}
	if hasSetting(t, s, "ran-before") {
		t.Error("write of the abandoned transaction was committed")
	}
	for _, key := range []string{"after-1", "after-2"} {
		if !hasSetting(t, s, key) {
			t.Errorf("%s was not retried", key)
		}
	}
}

func TestConcurrentWritesShareCommits(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	mustCreate(t, s, "bkt")
	// Every successful write adds a version, so a second winner would show.
	if err := s.SetBucketVersioning(ctx, "bkt", VersioningEnabled); err != nil {
		t.Fatal(err)
	}

	const writers = 64
	var wg sync.WaitGroup
	var winners atomic.Int32
	errs := make(chan error, 2*writers)
	for i := range writers {
		wg.Add(2)
		go func() { // independent keys: all succeed
			defer wg.Done()
			key := fmt.Sprintf("k%d", i)
			_, err := s.PutObject(ctx, "bkt", key, strings.NewReader(key), int64(len(key)), PutOptions{})
			errs <- err
		}()
		go func() { // create-only on one key: exactly one wins
			defer wg.Done()
			_, err := s.PutObject(ctx, "bkt", "once", strings.NewReader("x"), 1, PutOptions{IfNoneMatch: "*"})
			if err == nil {
				winners.Add(1)
			} else if errors.Is(err, ErrPrecondition) {
				err = nil
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := range writers {
		key := fmt.Sprintf("k%d", i)
		if _, got := readAll(t, s, "bkt", key); got != key {
			t.Errorf("%s = %q", key, got)
		}
	}
	page, err := s.ListVersions(ctx, "bkt", ListVersionsOptions{Prefix: "once", MaxKeys: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if winners.Load() != 1 || len(page.Versions) != 1 {
		t.Errorf("create-only key: %d writers won, %d versions; want 1 and 1", winners.Load(), len(page.Versions))
	}
}

func TestPanicReachesTheCallerAlone(t *testing.T) {
	s := openTest(t, t.TempDir())
	func() {
		defer func() {
			if v := recover(); v != "bug" {
				t.Errorf("recovered %v, want the transaction's panic", v)
			}
		}()
		s.tx(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
			setting("half-done", nil)(ctx, tx)
			panic("bug")
		})
	}()
	if hasSetting(t, s, "half-done") {
		t.Error("write of the panicking transaction was committed")
	}
	// The committer survived and keeps serving.
	if err := s.tx(context.Background(), setting("after", nil)); err != nil || !hasSetting(t, s, "after") {
		t.Fatalf("write after a panic: %v", err)
	}
}

func TestWritesAfterCloseFail(t *testing.T) {
	s, err := Open(t.TempDir(), testLog)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err := s.tx(context.Background(), setting("late", nil)); !errors.Is(err, ErrClosed) {
		t.Fatalf("write after Close: %v, want ErrClosed", err)
	}
}
