package api

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vaktikos/host-managed/internal/agentapi"
	"github.com/vaktikos/host-managed/internal/jobs"
	"github.com/vaktikos/host-managed/internal/store"
)

// catchUp is how late a scheduled backup may still start (e.g. after the
// control plane was restarted). Older slots are skipped and logged.
const catchUp = time.Hour

func slotOn(day time.Time, at string, loc *time.Location) time.Time {
	var h, m int
	fmt.Sscanf(at, "%d:%d", &h, &m)
	y, mo, d := day.In(loc).Date()
	return time.Date(y, mo, d, h, m, 0, 0, loc)
}

// lastSlot is the latest scheduled time at or before now (zero if none in
// the past week).
func lastSlot(now time.Time, days int, at string, loc *time.Location) time.Time {
	for i := 0; i <= 7; i++ {
		t := slotOn(now.AddDate(0, 0, -i), at, loc)
		if !t.After(now) && days&(1<<uint(t.Weekday())) != 0 {
			return t
		}
	}
	return time.Time{}
}

// nextSlot is the first scheduled time after now.
func nextSlot(now time.Time, days int, at string, loc *time.Location) time.Time {
	for i := 0; i <= 7; i++ {
		t := slotOn(now.AddDate(0, 0, i), at, loc)
		if t.After(now) && days&(1<<uint(t.Weekday())) != 0 {
			return t
		}
	}
	return time.Time{}
}

// RunScheduler starts due backup schedules until ctx ends.
func (s *Server) RunScheduler(ctx context.Context) {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		s.schedulerTick(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) schedulerTick(ctx context.Context, now time.Time) {
	list, err := s.Store.BackupSchedules(ctx)
	if err != nil {
		s.Log.Error("scheduler: list schedules", "err", err)
		return
	}
	for _, b := range list {
		if !b.Enabled {
			continue
		}
		slot := lastSlot(now, b.Days, b.At, time.Local)
		ref := b.CreatedAt
		if b.LastRun != nil && b.LastRun.After(ref) {
			ref = *b.LastRun
		}
		if slot.IsZero() || !slot.After(ref) {
			continue
		}
		if now.Sub(slot) > catchUp {
			s.Log.Warn("scheduler: missed backup slot skipped", "schedule", b.Name, "slot", slot)
			_ = s.Store.MarkScheduleRun(ctx, b.ID, slot, b.LastJob)
			s.auditSystem(ctx, "backup.schedule.missed", "schedule/"+b.Name, "slot="+slot.Format(time.RFC3339))
			continue
		}
		if b.LastJob != "" && s.Jobs.Running(b.LastJob) {
			s.Log.Warn("scheduler: previous run still active, slot skipped", "schedule", b.Name)
			_ = s.Store.MarkScheduleRun(ctx, b.ID, slot, b.LastJob)
			s.auditSystem(ctx, "backup.schedule.skipped", "schedule/"+b.Name, "previous run still active")
			continue
		}
		j, err := s.submitSchedule(ctx, b, "scheduler")
		if err != nil {
			s.Log.Error("scheduler: submit", "schedule", b.Name, "err", err)
			continue
		}
		_ = s.Store.MarkScheduleRun(ctx, b.ID, slot, j.ID)
	}
}

func (s *Server) auditSystem(ctx context.Context, action, resource, detail string) {
	_ = s.Store.Audit(ctx, store.AuditEntry{Username: "scheduler", IP: "-", Action: action, Resource: resource, Result: "ok", Detail: detail})
}

// resolveTargets expands a schedule's selection into "kind/name" entries.
func (s *Server) resolveTargets(ctx context.Context, b store.BackupSchedule) ([]string, error) {
	if b.Selection == "list" {
		return b.Targets, nil
	}
	var out []string
	if b.Selection == "all" || b.Selection == "vms" {
		vms, err := s.Agent.VMs(ctx)
		if err != nil {
			return nil, err
		}
		for _, v := range vms {
			out = append(out, "vm/"+v.Name)
		}
	}
	if b.Selection == "all" || b.Selection == "containers" {
		cts, err := s.Agent.Containers(ctx)
		if err != nil {
			return nil, err
		}
		for _, c := range cts {
			out = append(out, "ct/"+c.Name)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *Server) submitSchedule(ctx context.Context, b store.BackupSchedule, user string) (*store.Job, error) {
	return s.Jobs.Submit(ctx, "backup.schedule", "schedule/"+b.Name, user, map[string]any{"schedule": b.ID, "store": b.Store}, func(ctx context.Context, tk jobs.Task) error {
		return s.runBackupSet(ctx, tk, b)
	})
}

func (s *Server) runBackupSet(ctx context.Context, tk jobs.Task, b store.BackupSchedule) error {
	targets, err := s.resolveTargets(ctx, b)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		tk.Log("No guests selected; nothing to do")
		return nil
	}
	tk.Log(fmt.Sprintf("Backing up %d guest(s) to %s", len(targets), b.Store))
	const scale = 1000
	n := int64(len(targets))
	var failed []string
	for i, t := range targets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		kind, name, _ := strings.Cut(t, "/")
		tk.Log(fmt.Sprintf("[%d/%d] %s", i+1, len(targets), t))
		base := int64(i) * scale
		tk.Progress(base, n*scale)
		_, err := s.Agent.Backup(ctx, kind, name, agentapi.BackupRequest{Store: b.Store, Notes: b.Notes, Mode: b.Mode},
			func(line string) { tk.Log("  " + line) },
			func(done, total int64) {
				if total > 0 {
					tk.Progress(base+done*scale/total, n*scale)
				}
			})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			tk.Log("  FAILED: " + err.Error())
			failed = append(failed, t)
			continue
		}
		if !b.Keep.Empty() {
			res, err := s.Agent.PruneBackups(ctx, agentapi.PruneRequest{Store: b.Store, Kind: kind, Name: name, Keep: b.Keep})
			if err != nil {
				tk.Log("  Prune failed: " + err.Error())
			} else if len(res.Removed) > 0 {
				tk.Log(fmt.Sprintf("  Pruned %d old backup(s): %s", len(res.Removed), strings.Join(res.Removed, ", ")))
			}
		}
	}
	tk.Progress(n*scale, n*scale)
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d backups failed: %s", len(failed), len(targets), strings.Join(failed, ", "))
	}
	return nil
}
