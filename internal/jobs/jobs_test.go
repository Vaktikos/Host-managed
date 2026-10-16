package jobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/vaktikos/host-managed/internal/store"
)

func setup(t *testing.T) (*Runner, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "hm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st, slog.New(slog.NewTextHandler(io.Discard, nil)), 2), st
}

func wait(t *testing.T, st *store.Store, id string) *store.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, _ := st.Job(context.Background(), id)
		if j != nil && j.FinishedAt != nil {
			return j
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return nil
}

func TestJobOutcomes(t *testing.T) {
	r, st := setup(t)
	ctx := context.Background()
	finished := make(chan string, 3)
	r.OnFinish = func(j *store.Job) { finished <- j.Status }

	ok, _ := r.Submit(ctx, "test", "x", "admin", nil, func(ctx context.Context, tk Task) error {
		tk.Log("working")
		tk.Progress(50, 100)
		return nil
	})
	bad, _ := r.Submit(ctx, "test", "x", "admin", nil, func(ctx context.Context, tk Task) error {
		return errors.New("disk full")
	})
	panicky, _ := r.Submit(ctx, "test", "x", "admin", nil, func(ctx context.Context, tk Task) error {
		panic("boom")
	})
	if j := wait(t, st, ok.ID); j.Status != "succeeded" {
		t.Fatalf("ok job: %+v", j)
	}
	if j := wait(t, st, bad.ID); j.Status != "failed" || j.Error != "disk full" {
		t.Fatalf("bad job: %+v", j)
	}
	if j := wait(t, st, panicky.ID); j.Status != "failed" {
		t.Fatalf("panicking job: %+v", j)
	}
	if j, _ := st.Job(ctx, ok.ID); j.Progress == nil || *j.Progress != 50 {
		t.Fatalf("progress not stored: %v", j.Progress)
	}
	if j, _ := st.Job(ctx, bad.ID); j.Progress != nil {
		t.Fatal("progress invented for a job that reported none")
	}
	logs, _ := st.JobLog(ctx, ok.ID, 0)
	if len(logs) != 3 || logs[1].Line != "working" {
		t.Fatalf("logs: %+v", logs)
	}
	for i := 0; i < 3; i++ {
		<-finished
	}
}

func TestCancel(t *testing.T) {
	r, st := setup(t)
	started := make(chan struct{})
	j, _ := r.Submit(context.Background(), "test", "x", "admin", nil, func(ctx context.Context, tk Task) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	<-started
	if !r.Cancel(j.ID) {
		t.Fatal("cancel returned false")
	}
	if got := wait(t, st, j.ID); got.Status != "cancelled" {
		t.Fatalf("status %s", got.Status)
	}
	if r.Cancel(j.ID) {
		t.Fatal("finished job reported as cancellable")
	}
}

func TestParallelLimit(t *testing.T) {
	r, st := setup(t)
	running := make(chan struct{}, 10)
	release := make(chan struct{})
	var ids []string
	for i := 0; i < 4; i++ {
		j, _ := r.Submit(context.Background(), "test", "x", "admin", nil, func(ctx context.Context, tk Task) error {
			running <- struct{}{}
			<-release
			return nil
		})
		ids = append(ids, j.ID)
	}
	time.Sleep(100 * time.Millisecond)
	if n := len(running); n != 2 {
		t.Fatalf("%d jobs running, limit is 2", n)
	}
	close(release)
	for _, id := range ids {
		wait(t, st, id)
	}
}
