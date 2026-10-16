package agent

import (
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/vaktikos/host-managed/internal/agentapi"
	"github.com/vaktikos/host-managed/internal/backup"
)

func (s *Server) storePath(w http.ResponseWriter, store string) (string, bool) {
	p, err := s.Virt.BackupStorePath(store)
	if err != nil {
		fail(w, err)
		return "", false
	}
	return p, true
}

func backupFail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, backup.ErrBad):
		fail(w, &agentapi.FieldError{Field: "id", Reason: "invalid backup id"})
	case errors.Is(err, os.ErrNotExist):
		writeErr(w, http.StatusNotFound, "not_found", "backup not found")
	case errors.Is(err, backup.ErrProtected):
		writeErr(w, http.StatusConflict, "conflict", "the backup is protected")
	default:
		fail(w, err)
	}
}

func (s *Server) listBackups(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.storePath(w, r.URL.Query().Get("store"))
	if !ok {
		return
	}
	list, err := backup.List(dir)
	if err != nil {
		fail(w, err)
		return
	}
	if list == nil {
		list = []backup.Manifest{}
	}
	writeJSON(w, 200, list)
}

func (s *Server) software() string { return "Host-managed " + s.Version }

func (s *Server) backupVM(w http.ResponseWriter, r *http.Request) {
	var b agentapi.BackupRequest
	if err := decode(r, &b); err != nil {
		fail(w, err)
		return
	}
	if err := b.Validate(); err != nil {
		fail(w, err)
		return
	}
	dir, ok := s.storePath(w, b.Store)
	if !ok {
		return
	}
	name := r.PathValue("name")
	unlock, ok := s.tryLock("vm:" + name)
	if !ok {
		writeErr(w, http.StatusConflict, "locked", "another operation on this VM is in progress")
		return
	}
	defer unlock()
	st := newStreamer(w)
	man, err := s.Virt.BackupVM(r.Context(), name, dir, b.Notes, s.software(), backup.Reporter{Log: st.log, Progress: st.progress})
	st.finish(man, err)
}

func (s *Server) backupCT(w http.ResponseWriter, r *http.Request) {
	var b agentapi.BackupRequest
	if err := decode(r, &b); err != nil {
		fail(w, err)
		return
	}
	if err := b.Validate(); err != nil {
		fail(w, err)
		return
	}
	dir, ok := s.storePath(w, b.Store)
	if !ok {
		return
	}
	name := r.PathValue("name")
	unlock, ok := s.tryLock("ct:" + name)
	if !ok {
		writeErr(w, http.StatusConflict, "locked", "another operation on this container is in progress")
		return
	}
	defer unlock()
	st := newStreamer(w)
	man, err := s.LXC.Backup(r.Context(), name, dir, b.Mode, b.Notes, s.software(), backup.Reporter{Log: st.log, Progress: st.progress})
	st.finish(man, err)
}

func (s *Server) restoreBackup(w http.ResponseWriter, r *http.Request) {
	var req agentapi.Restore
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	if err := req.Validate(); err != nil {
		fail(w, err)
		return
	}
	dir, ok := s.storePath(w, req.Store)
	if !ok {
		return
	}
	kind, orig, _, _ := backup.ParseID(req.ID)
	if req.Name == "" {
		req.Name = orig
	}
	unlockB, ok := s.tryLock("backup:" + req.Store + "/" + req.ID)
	if !ok {
		writeErr(w, http.StatusConflict, "locked", "this backup is in use")
		return
	}
	defer unlockB()
	unlock, ok := s.tryLock(kind + ":" + req.Name)
	if !ok {
		writeErr(w, http.StatusConflict, "locked", "another operation on the target name is in progress")
		return
	}
	defer unlock()
	if kind == "vm" && req.Pool == "" {
		fail(w, &agentapi.FieldError{Field: "pool", Reason: "a target storage pool is required"})
		return
	}
	st := newStreamer(w)
	rep := backup.Reporter{Log: st.log, Progress: st.progress}
	if kind == "vm" {
		vm, err := s.Virt.RestoreVM(r.Context(), dir, req.ID, req, rep)
		st.finish(vm, err)
		return
	}
	ct, err := s.LXC.Restore(r.Context(), dir, req.ID, req, rep)
	st.finish(ct, err)
}

func (s *Server) deleteBackup(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dir, ok := s.storePath(w, q.Get("store"))
	if !ok {
		return
	}
	id := q.Get("id")
	unlock, ok := s.tryLock("backup:" + q.Get("store") + "/" + id)
	if !ok {
		writeErr(w, http.StatusConflict, "locked", "this backup is in use")
		return
	}
	defer unlock()
	if err := backup.Delete(dir, id); err != nil {
		backupFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) protectBackup(w http.ResponseWriter, r *http.Request) {
	var p agentapi.BackupProtect
	if err := decode(r, &p); err != nil {
		fail(w, err)
		return
	}
	dir, ok := s.storePath(w, p.Store)
	if !ok {
		return
	}
	defer s.lock("backup:" + p.Store + "/" + p.ID)()
	if err := backup.SetProtected(dir, p.ID, p.Protected); err != nil {
		backupFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) pruneBackups(w http.ResponseWriter, r *http.Request) {
	var p agentapi.PruneRequest
	if err := decode(r, &p); err != nil {
		fail(w, err)
		return
	}
	if err := p.Validate(); err != nil {
		fail(w, err)
		return
	}
	dir, ok := s.storePath(w, p.Store)
	if !ok {
		return
	}
	all, err := backup.List(dir)
	if err != nil {
		fail(w, err)
		return
	}
	var group []backup.Manifest
	for _, m := range all {
		if m.Kind == p.Kind && m.Name == p.Name {
			group = append(group, m)
		}
	}
	keep, remove := backup.Prune(group, p.Keep, time.Local)
	res := agentapi.PruneResult{Kept: []string{}, Removed: []string{}}
	for _, m := range keep {
		res.Kept = append(res.Kept, m.ID)
	}
	for _, m := range remove {
		if !p.DryRun {
			unlock, ok := s.tryLock("backup:" + p.Store + "/" + m.ID)
			if !ok {
				res.Kept = append(res.Kept, m.ID) // in use; next prune removes it
				continue
			}
			err := backup.Delete(dir, m.ID)
			unlock()
			if err != nil {
				s.Log.Warn("prune: delete failed", "id", m.ID, "err", err)
				res.Kept = append(res.Kept, m.ID)
				continue
			}
		}
		res.Removed = append(res.Removed, m.ID)
	}
	writeJSON(w, 200, res)
}

// CleanPartialBackups removes backups left half-written by a crash. It must
// run before the agent accepts requests.
func (s *Server) CleanPartialBackups() {
	pools, err := s.Virt.ListPools()
	if err != nil {
		return
	}
	for _, p := range pools {
		if p.Content != "backup" || !p.Active {
			continue
		}
		dir, err := s.Virt.BackupStorePath(p.Name)
		if err != nil {
			continue
		}
		for _, d := range backup.CleanPartial(dir) {
			s.Log.Warn("removed incomplete backup", "path", d)
		}
	}
}
