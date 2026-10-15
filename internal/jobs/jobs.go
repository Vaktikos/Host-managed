// Package jobs runs long operations in the background with persistent status
// and logs. Progress is only reported as log lines; no percentage is invented.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/vaktikos/host-managed/internal/auth"
	"github.com/vaktikos/host-managed/internal/store"
)

// Func performs the work. It reports progress through log and must honour ctx.
type Func func(ctx context.Context, log func(string)) error

type Runner struct {
	st  *store.Store
	log *slog.Logger
	sem chan struct{}

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
	wg      sync.WaitGroup
	ctx     context.Context
	stop    context.CancelFunc

	// OnFinish is called after a job ended (for audit and notifications).
	OnFinish func(j *store.Job)
}

func New(st *store.Store, log *slog.Logger, parallel int) *Runner {
	if parallel < 1 {
		parallel = 4
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Runner{st: st, log: log, sem: make(chan struct{}, parallel), cancels: map[string]context.CancelFunc{}, ctx: ctx, stop: cancel}
}

// Recover marks jobs from a previous run as interrupted.
func (r *Runner) Recover(ctx context.Context) {
	if n, err := r.st.FailInterrupted(ctx); err == nil && n > 0 {
		r.log.Warn("marked interrupted jobs as failed", "count", n)
	}
}

// Submit persists a job and starts it asynchronously.
func (r *Runner) Submit(ctx context.Context, typ, target, user string, params any, fn Func) (*store.Job, error) {
	p, _ := json.Marshal(params)
	j := &store.Job{
		ID:        "job-" + time.Now().UTC().Format("20060102-150405") + "-" + auth.RandomToken(4),
		Type:      typ,
		Target:    target,
		Status:    "queued",
		Username:  user,
		CreatedAt: time.Now().UTC(),
		Params:    string(p),
	}
	if err := r.st.CreateJob(ctx, j); err != nil {
		return nil, err
	}
	jctx, cancel := context.WithCancel(r.ctx)
	r.mu.Lock()
	r.cancels[j.ID] = cancel
	r.mu.Unlock()
	r.wg.Add(1)
	go r.run(jctx, j, fn)
	return j, nil
}

func (r *Runner) run(ctx context.Context, j *store.Job, fn Func) {
	defer r.wg.Done()
	defer func() {
		r.mu.Lock()
		if c := r.cancels[j.ID]; c != nil {
			c()
		}
		delete(r.cancels, j.ID)
		r.mu.Unlock()
	}()
	bg := context.Background()
	logf := func(line string) {
		if err := r.st.AppendJobLog(bg, j.ID, line); err != nil {
			r.log.Error("job log", "job", j.ID, "err", err)
		}
	}
	select {
	case r.sem <- struct{}{}:
	case <-ctx.Done():
		_ = r.st.FinishJob(bg, j.ID, "cancelled", "cancelled before start")
		r.finished(j.ID)
		return
	}
	defer func() { <-r.sem }()

	_ = r.st.SetJobRunning(bg, j.ID)
	logf(fmt.Sprintf("Job %s started by %s", j.Type, j.Username))
	err := func() (err error) {
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("internal error: %v", p)
			}
		}()
		return fn(ctx, logf)
	}()
	switch {
	case err == nil:
		logf("Finished successfully")
		_ = r.st.FinishJob(bg, j.ID, "succeeded", "")
	case ctx.Err() != nil:
		logf("Cancelled: " + err.Error())
		_ = r.st.FinishJob(bg, j.ID, "cancelled", err.Error())
	default:
		logf("Failed: " + err.Error())
		_ = r.st.FinishJob(bg, j.ID, "failed", err.Error())
	}
	r.finished(j.ID)
}

func (r *Runner) finished(id string) {
	if r.OnFinish == nil {
		return
	}
	if j, err := r.st.Job(context.Background(), id); err == nil {
		r.OnFinish(j)
	}
}

// Cancel requests cancellation. Whether the operation can actually stop
// depends on the step it is in; the final status tells the truth.
func (r *Runner) Cancel(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.cancels[id]
	if ok {
		c()
	}
	return ok
}

func (r *Runner) Running(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.cancels[id]
	return ok
}

// Shutdown cancels running jobs and waits up to timeout.
func (r *Runner) Shutdown(timeout time.Duration) {
	r.stop()
	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}
