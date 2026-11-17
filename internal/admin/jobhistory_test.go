package admin

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"bin/internal/storage"
)

// The history of import and export jobs outlives a restart: finished jobs are
// listed as they ended, and jobs cut short are marked interrupted with what
// they had copied so far.
func TestTransferJobHistorySurvivesRestart(t *testing.T) {
	old := jobSaveInterval
	jobSaveInterval = 20 * time.Millisecond
	t.Cleanup(func() { jobSaveInterval = old })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dir := t.TempDir()
	store, err := storage.Open(dir, log)
	if err != nil {
		t.Fatal(err)
	}
	exports := newExportJobs(store, log)
	imports := newImportJobs(store, log)
	start := time.Now().UTC().Add(-time.Minute)
	newJob := func(id string, at time.Time) *importJob {
		return &importJob{ID: id, Bucket: "photos", Endpoint: "https://s3.example.com", Source: "exp_1",
			State: "queued", StartedAt: at, cancel: func() {}}
	}

	// One export finishes, another is still running when bin "stops".
	exports.add(newJob("export-done", start))
	exports.update("export-done", func(j *importJob) {
		now := time.Now().UTC()
		j.State, j.Copied, j.Bytes, j.FinishedAt = "done", 3, 30, &now
		j.Notes = []string{"a note"}
	})
	exports.finish("export-done")
	exports.add(newJob("export-cut", start.Add(time.Second)))
	exports.update("export-cut", func(j *importJob) { j.State, j.Copied = "running", 7 })
	imports.add(&importJob{ID: "import-1", Bucket: "docs", Endpoint: "https://old", State: "queued", StartedAt: start, cancel: func() {}})

	deadline := time.Now().Add(5 * time.Second)
	for {
		saved, _ := store.ListTransferJobs(context.Background(), "export")
		if len(saved) == 2 && saved[0].Copied == 7 {
			break // the running job's progress reached the database
		}
		if time.Now().After(deadline) {
			t.Fatalf("progress not saved: %+v", saved)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if exports.snapshotOK("export-done") {
		t.Error("a finished job stayed in memory")
	}
	// bin stops: its autosaves end without saving anything more.
	for _, q := range []*importJobs{exports, imports} {
		for _, id := range []string{"export-cut", "import-1"} {
			q.update(id, func(j *importJob) { now := time.Now(); j.FinishedAt = &now })
		}
		q.saveMu.Lock() // waits out a save in flight
		q.saveMu.Unlock()
	}
	store.Close()

	// "Restart": a new store and handler state on the same data.
	store, err = storage.Open(dir, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	exports = newExportJobs(store, log)
	imports = newImportJobs(store, log)
	exports.interrupt()

	jobs := exports.list()
	if len(jobs) != 2 || jobs[0].ID != "export-cut" || jobs[1].ID != "export-done" {
		t.Fatalf("export history after restart: %+v", jobs)
	}
	cut, done := jobs[0], jobs[1]
	if cut.State != "failed" || cut.Error != storage.InterruptedJobError || cut.Copied != 7 || cut.FinishedAt == nil {
		t.Errorf("interrupted job: %+v", cut)
	}
	if done.State != "done" || done.Copied != 3 || done.Bytes != 30 || done.FinishedAt == nil ||
		len(done.Notes) != 1 || done.Source != "exp_1" {
		t.Errorf("finished job: %+v", done)
	}
	if got := bySource(jobs, "exp_1"); len(got) != 2 {
		t.Errorf("jobs of the saved export: %+v", got)
	}
	if ij := imports.list(); len(ij) != 1 || ij[0].ID != "import-1" || ij[0].State != "failed" {
		t.Errorf("import history after restart: %+v", ij)
	}

	// A finished job from before the restart is found, but cannot be canceled.
	if _, found, active := exports.cancel("export-done"); !found || active {
		t.Errorf("cancel finished job: found=%v active=%v", found, active)
	}
	if _, found, _ := exports.cancel("export-nope"); found {
		t.Error("an unknown job was found")
	}
}

func (j *importJobs) snapshotOK(id string) bool {
	_, ok := j.snapshot(id)
	return ok
}
