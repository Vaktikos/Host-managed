package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vaktikos/host-managed/internal/agentapi"
	"github.com/vaktikos/host-managed/internal/backup"
	"github.com/vaktikos/host-managed/internal/jobs"
	"github.com/vaktikos/host-managed/internal/store"
)

// BackupEntry is a backup with the store it lives in.
type BackupEntry struct {
	backup.Manifest
	Store string `json:"store"`
}

func (s *Server) backupStores(ctx context.Context) ([]agentapi.StoragePool, error) {
	pools, err := s.Agent.Pools(ctx)
	if err != nil {
		return nil, err
	}
	out := []agentapi.StoragePool{}
	for _, p := range pools {
		if p.Content == "backup" {
			out = append(out, p)
		}
	}
	return out, nil
}

func (s *Server) listBackupStores(w http.ResponseWriter, r *http.Request) {
	st, err := s.backupStores(r.Context())
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 200, st)
}

// listBackups returns the backups of one store, or of all active stores.
// Optional filters: kind and name.
func (s *Server) listBackups(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var stores []string
	if st := q.Get("store"); st != "" {
		stores = []string{st}
	} else {
		all, err := s.backupStores(r.Context())
		if err != nil {
			s.agentFail(w, r, err)
			return
		}
		for _, p := range all {
			if p.Active {
				stores = append(stores, p.Name)
			}
		}
	}
	out := []BackupEntry{}
	for _, st := range stores {
		list, err := s.Agent.Backups(r.Context(), st)
		if err != nil {
			s.agentFail(w, r, err)
			return
		}
		for _, m := range list {
			if (q.Get("kind") == "" || m.Kind == q.Get("kind")) && (q.Get("name") == "" || m.Name == q.Get("name")) {
				out = append(out, BackupEntry{Manifest: m, Store: st})
			}
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) startBackup(w http.ResponseWriter, r *http.Request, kind string) {
	var b agentapi.BackupRequest
	if err := decodeBody(w, r, &b); err != nil {
		s.agentFail(w, r, err)
		return
	}
	if err := b.Validate(); err != nil {
		s.agentFail(w, r, err)
		return
	}
	name := r.PathValue("name")
	var err error
	if kind == "vm" {
		_, err = s.Agent.VM(r.Context(), name)
	} else {
		_, err = s.Agent.Container(r.Context(), name)
	}
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	target := kind + "/" + name
	j, err := s.Jobs.Submit(r.Context(), kind+".backup", target, identity(r).User.Username, b, func(ctx context.Context, tk jobs.Task) error {
		_, err := s.Agent.Backup(ctx, kind, name, b, tk.Log, tk.Progress)
		return err
	})
	s.audit(r, kind+".backup", target, err, "store="+b.Store)
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	jobAccepted(w, j)
}

func (s *Server) vmBackup(w http.ResponseWriter, r *http.Request) { s.startBackup(w, r, "vm") }
func (s *Server) ctBackup(w http.ResponseWriter, r *http.Request) { s.startBackup(w, r, "ct") }

func (s *Server) restoreBackup(w http.ResponseWriter, r *http.Request) {
	var req agentapi.Restore
	if err := decodeBody(w, r, &req); err != nil {
		s.agentFail(w, r, err)
		return
	}
	if err := req.Validate(); err != nil {
		s.agentFail(w, r, err)
		return
	}
	kind, orig, _, _ := backup.ParseID(req.ID)
	if req.Name == "" {
		req.Name = orig
	}
	if kind == "vm" {
		if req.Pool == "" {
			valErr(w, r, "pool", "a target storage pool is required")
			return
		}
		if _, err := s.Agent.VM(r.Context(), req.Name); err == nil {
			valErr(w, r, "name", "a VM with this name already exists")
			return
		}
	} else {
		if !agentapi.ValidContainerName(req.Name) {
			valErr(w, r, "name", "1-63 characters: lowercase letters, digits, '-'")
			return
		}
		if _, err := s.Agent.Container(r.Context(), req.Name); err == nil {
			valErr(w, r, "name", "a container with this name already exists")
			return
		}
	}
	target := kind + "/" + req.Name
	j, err := s.Jobs.Submit(r.Context(), "backup.restore", target, identity(r).User.Username, req, func(ctx context.Context, tk jobs.Task) error {
		tk.Log("Restoring " + req.ID + " from " + req.Store)
		_, err := s.Agent.Restore(ctx, req, tk.Log, tk.Progress)
		return err
	})
	s.audit(r, "backup.restore", target, err, req.Store+":"+req.ID)
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	jobAccepted(w, j)
}

func backupRef(w http.ResponseWriter, r *http.Request) (store, id string, ok bool) {
	q := r.URL.Query()
	store, id = q.Get("store"), q.Get("id")
	if !agentapi.ValidBackupStore(store) {
		valErr(w, r, "store", "not a backup store")
		return "", "", false
	}
	if _, _, _, err := backup.ParseID(id); err != nil {
		valErr(w, r, "id", "invalid backup id")
		return "", "", false
	}
	return store, id, true
}

func (s *Server) deleteBackup(w http.ResponseWriter, r *http.Request) {
	st, id, ok := backupRef(w, r)
	if !ok {
		return
	}
	err := s.Agent.DeleteBackup(r.Context(), st, id)
	s.audit(r, "backup.delete", "backup/"+st+"/"+id, err, "")
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) protectBackup(w http.ResponseWriter, r *http.Request) {
	var p agentapi.BackupProtect
	if err := decodeBody(w, r, &p); err != nil {
		s.agentFail(w, r, err)
		return
	}
	if !agentapi.ValidBackupStore(p.Store) {
		valErr(w, r, "store", "not a backup store")
		return
	}
	if _, _, _, err := backup.ParseID(p.ID); err != nil {
		valErr(w, r, "id", "invalid backup id")
		return
	}
	err := s.Agent.ProtectBackup(r.Context(), p)
	s.audit(r, "backup.protect", "backup/"+p.Store+"/"+p.ID, err, strconv.FormatBool(p.Protected))
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) pruneBackups(w http.ResponseWriter, r *http.Request) {
	var p agentapi.PruneRequest
	if err := decodeBody(w, r, &p); err != nil {
		s.agentFail(w, r, err)
		return
	}
	if err := p.Validate(); err != nil {
		s.agentFail(w, r, err)
		return
	}
	res, err := s.Agent.PruneBackups(r.Context(), p)
	if !p.DryRun {
		detail := ""
		if res != nil {
			detail = fmt.Sprintf("removed=%d", len(res.Removed))
		}
		s.audit(r, "backup.prune", p.Kind+"/"+p.Name, err, detail)
	}
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 200, res)
}

// --- schedules ---

var (
	schedNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9 ._-]{0,62}$`)
	hhmmRe      = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
)

func validateSchedule(b *store.BackupSchedule) error {
	fe := func(f, m string) error { return &agentapi.FieldError{Field: f, Reason: m} }
	switch {
	case !schedNameRe.MatchString(b.Name):
		return fe("name", "1-63 characters: letters, digits, space, '.', '_', '-'")
	case !agentapi.ValidBackupStore(b.Store):
		return fe("store", "not a backup store")
	case b.Days < 1 || b.Days > 127:
		return fe("days", "select at least one weekday")
	case !hhmmRe.MatchString(b.At):
		return fe("at", "time as HH:MM")
	case len(b.Notes) > 1024:
		return fe("notes", "at most 1024 characters")
	}
	switch b.Selection {
	case "all", "vms", "containers":
		b.Targets = []string{}
	case "list":
		if len(b.Targets) == 0 || len(b.Targets) > 500 {
			return fe("targets", "select 1 to 500 guests")
		}
		seen := map[string]bool{}
		for _, t := range b.Targets {
			kind, name, _ := strings.Cut(t, "/")
			if !(kind == "vm" && agentapi.ValidName(name)) && !(kind == "ct" && agentapi.ValidContainerName(name)) {
				return fe("targets", "invalid guest "+strconv.Quote(t))
			}
			if seen[t] {
				return fe("targets", "duplicate guest "+strconv.Quote(t))
			}
			seen[t] = true
		}
	default:
		return fe("selection", "all, vms, containers or list")
	}
	if err := (agentapi.BackupRequest{Store: b.Store, Mode: b.Mode}).Validate(); err != nil {
		return err
	}
	if err := b.Keep.Validate(); err != nil {
		return fe("keep", err.Error())
	}
	return nil
}

// scheduleView adds the next planned run.
type scheduleView struct {
	store.BackupSchedule
	NextRun *time.Time `json:"next_run"`
	Running bool       `json:"running"`
}

func (s *Server) viewSchedule(b store.BackupSchedule) scheduleView {
	v := scheduleView{BackupSchedule: b, Running: b.LastJob != "" && s.Jobs.Running(b.LastJob)}
	if b.Enabled {
		if t := nextSlot(time.Now(), b.Days, b.At, time.Local); !t.IsZero() {
			v.NextRun = &t
		}
	}
	return v
}

func (s *Server) listSchedules(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.BackupSchedules(r.Context())
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	out := make([]scheduleView, 0, len(list))
	for _, b := range list {
		out = append(out, s.viewSchedule(b))
	}
	writeJSON(w, 200, out)
}

type scheduleBody struct {
	Name      string      `json:"name"`
	Enabled   bool        `json:"enabled"`
	Store     string      `json:"store"`
	Days      int         `json:"days"`
	At        string      `json:"at"`
	Selection string      `json:"selection"`
	Targets   []string    `json:"targets"`
	Mode      string      `json:"mode"`
	Keep      backup.Keep `json:"keep"`
	Notes     string      `json:"notes"`
}

func (b scheduleBody) apply(to *store.BackupSchedule) {
	to.Name, to.Enabled, to.Store, to.Days, to.At = strings.TrimSpace(b.Name), b.Enabled, b.Store, b.Days, b.At
	to.Selection, to.Targets, to.Mode, to.Keep, to.Notes = b.Selection, b.Targets, b.Mode, b.Keep, b.Notes
	if to.Targets == nil {
		to.Targets = []string{}
	}
}

func (s *Server) createSchedule(w http.ResponseWriter, r *http.Request) {
	var body scheduleBody
	if err := decodeBody(w, r, &body); err != nil {
		s.agentFail(w, r, err)
		return
	}
	var b store.BackupSchedule
	body.apply(&b)
	if err := validateSchedule(&b); err != nil {
		s.agentFail(w, r, err)
		return
	}
	err := s.Store.CreateBackupSchedule(r.Context(), &b)
	s.audit(r, "backup.schedule.create", "schedule/"+b.Name, err, "")
	if errors.Is(err, store.ErrExists) {
		valErr(w, r, "name", "a schedule with this name already exists")
		return
	}
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 201, s.viewSchedule(b))
}

func (s *Server) scheduleByPath(w http.ResponseWriter, r *http.Request) (*store.BackupSchedule, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.fail(w, r, 404, "not_found", "schedule not found")
		return nil, false
	}
	b, err := s.Store.BackupSchedule(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, 404, "not_found", "schedule not found")
		return nil, false
	}
	if err != nil {
		s.agentFail(w, r, err)
		return nil, false
	}
	return b, true
}

func (s *Server) updateSchedule(w http.ResponseWriter, r *http.Request) {
	b, ok := s.scheduleByPath(w, r)
	if !ok {
		return
	}
	var body scheduleBody
	if err := decodeBody(w, r, &body); err != nil {
		s.agentFail(w, r, err)
		return
	}
	body.apply(b)
	if err := validateSchedule(b); err != nil {
		s.agentFail(w, r, err)
		return
	}
	err := s.Store.UpdateBackupSchedule(r.Context(), b)
	s.audit(r, "backup.schedule.update", "schedule/"+b.Name, err, "")
	if errors.Is(err, store.ErrExists) {
		valErr(w, r, "name", "a schedule with this name already exists")
		return
	}
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 200, s.viewSchedule(*b))
}

func (s *Server) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	b, ok := s.scheduleByPath(w, r)
	if !ok {
		return
	}
	err := s.Store.DeleteBackupSchedule(r.Context(), b.ID)
	s.audit(r, "backup.schedule.delete", "schedule/"+b.Name, err, "")
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) runScheduleNow(w http.ResponseWriter, r *http.Request) {
	b, ok := s.scheduleByPath(w, r)
	if !ok {
		return
	}
	if b.LastJob != "" && s.Jobs.Running(b.LastJob) {
		s.fail(w, r, 409, "conflict", "this schedule is already running")
		return
	}
	j, err := s.submitSchedule(r.Context(), *b, identity(r).User.Username)
	s.audit(r, "backup.schedule.run", "schedule/"+b.Name, err, "")
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	_ = s.Store.MarkScheduleRun(r.Context(), b.ID, lastRunOr(b), j.ID)
	jobAccepted(w, j)
}

// lastRunOr keeps the scheduled-slot bookkeeping unchanged on manual runs.
func lastRunOr(b *store.BackupSchedule) time.Time {
	if b.LastRun != nil {
		return *b.LastRun
	}
	return b.CreatedAt
}
