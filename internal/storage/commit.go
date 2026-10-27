package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"runtime/debug"
)

// Group commit
//
// With synchronous=FULL every commit waits for an fsync of the WAL, and
// SQLite has a single writer: concurrent writes used to queue for one fsync
// each. Instead, one goroutine owns the write path. While a commit is on its
// way to disk, new transactions wait in the queue; the next round runs all
// of them in one SQLite transaction and makes them durable with one fsync.
//
// Each transaction runs inside its own savepoint, so one that fails (a
// precondition, a quota) is undone alone and the others still commit. Nothing
// waits on purpose: a lone write commits at once, exactly as before.
//
// fn receives a context that cannot be cancelled. Cancelling a statement
// interrupts SQLite, which rolls back the whole transaction, other callers'
// work included; a caller that gave up is skipped before its turn instead.

// maxBatch bounds how many transactions share one commit.
const maxBatch = 256

// ErrClosed is returned for writes attempted after Close.
var ErrClosed = errors.New("storage is closed")

type txRequest struct {
	ctx  context.Context
	fn   func(context.Context, *sql.Tx) error
	done chan error
}

// tx runs fn in a write transaction, possibly sharing its commit with other
// concurrent ones. It returns once the outcome is durable (or failed).
func (s *Store) tx(ctx context.Context, fn func(context.Context, *sql.Tx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	req := &txRequest{ctx: ctx, fn: fn, done: make(chan error, 1)}
	select {
	case s.txq <- req:
	case <-ctx.Done():
		return ctx.Err()
	case <-s.stopTx:
		return ErrClosed
	}
	// Once handed over, the request may commit: wait for the outcome even if
	// ctx ends, so a caller never cleans up after a write that landed.
	err := <-req.done
	var p *txPanic
	if errors.As(err, &p) {
		panic(p.value) // where it would have surfaced without batching
	}
	return err
}

// txPanic carries a panic out of the committer to the caller of tx, after
// the panicking transaction was undone.
type txPanic struct {
	value any
	stack []byte
}

func (p *txPanic) Error() string {
	return fmt.Sprintf("panic in write transaction: %v\n%s", p.value, p.stack)
}

// run calls the request's fn, turning a panic into a *txPanic error so the
// committer survives it.
func (req *txRequest) run(ctx context.Context, tx *sql.Tx) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = &txPanic{value: v, stack: debug.Stack()}
		}
	}()
	return req.fn(ctx, tx)
}

// startCommitter starts the goroutine that runs every write transaction.
func (s *Store) startCommitter() {
	s.txq = make(chan *txRequest) // unbuffered: a sent request is always served
	s.stopTx = make(chan struct{})
	s.txDone = make(chan struct{})
	go s.committer()
}

// stopCommitter ends the committer once its current round is done.
func (s *Store) stopCommitter() {
	if s.stopTx == nil {
		return
	}
	close(s.stopTx)
	<-s.txDone
}

func (s *Store) committer() {
	defer close(s.txDone)
	batch := make([]*txRequest, 0, maxBatch)
	for {
		select {
		case req := <-s.txq:
			batch = append(batch[:0], req)
		case <-s.stopTx:
			return
		}
	collect:
		for len(batch) < maxBatch {
			select {
			case req := <-s.txq:
				batch = append(batch, req)
			default:
				break collect
			}
		}
		s.commitBatch(batch)
		clear(batch) // drop references to finished requests
	}
}

// commitBatch runs the requests in one transaction and reports each outcome.
func (s *Store) commitBatch(batch []*txRequest) {
	results := make([]error, len(batch))
	defer func() {
		for i, req := range batch {
			req.done <- results[i]
		}
	}()

	tx, err := s.write.BeginTx(context.Background(), nil)
	if err != nil {
		for i := range results {
			results[i] = err
		}
		return
	}
	// Savepoints cost two statements per request: only pay for them when a
	// failure could otherwise undo someone else's work.
	shared := len(batch) > 1
	applied := 0
	for i, req := range batch {
		if err := req.ctx.Err(); err != nil {
			results[i] = err
			continue
		}
		ctx := context.WithoutCancel(req.ctx)
		if shared {
			if _, err := tx.ExecContext(ctx, `SAVEPOINT member`); err != nil {
				s.abortBatch(tx, batch, results, i, err)
				return
			}
		}
		if results[i] = req.run(ctx, tx); results[i] != nil {
			if !shared {
				break
			}
			if _, err := tx.ExecContext(ctx, `ROLLBACK TO member`); err != nil {
				s.abortBatch(tx, batch, results, i, err)
				return
			}
		} else {
			applied++
		}
		if shared {
			if _, err := tx.ExecContext(ctx, `RELEASE member`); err != nil {
				s.abortBatch(tx, batch, results, i, err)
				return
			}
		}
	}
	if applied == 0 {
		tx.Rollback()
		return
	}
	err = tx.Commit()
	s.bump() // even after a failed commit: whether it landed is unknown
	if err != nil {
		for i := range results {
			if results[i] == nil {
				results[i] = err
			}
		}
	}
}

// abortBatch handles a transaction SQLite can no longer use (a savepoint
// could not be set or undone). Requests that already ran are lost with it
// and get the error; the rest never ran and are retried in a new round.
func (s *Store) abortBatch(tx *sql.Tx, batch []*txRequest, results []error, failed int, cause error) {
	tx.Rollback()
	s.bump()
	err := fmt.Errorf("write transaction aborted: %w", cause)
	for i := 0; i <= failed; i++ {
		if results[i] == nil || i == failed {
			results[i] = err
		}
	}
	if rest := batch[failed+1:]; len(rest) > 0 {
		retry := make([]error, len(rest))
		s.commitBatchInto(rest, retry)
		copy(results[failed+1:], retry)
	}
}

// commitBatchInto runs commitBatch for a sub-batch, collecting its results
// instead of delivering them.
func (s *Store) commitBatchInto(batch []*txRequest, results []error) {
	proxies := make([]*txRequest, len(batch))
	for i, req := range batch {
		proxies[i] = &txRequest{ctx: req.ctx, fn: req.fn, done: make(chan error, 1)}
	}
	s.commitBatch(proxies)
	for i, p := range proxies {
		results[i] = <-p.done
	}
}
