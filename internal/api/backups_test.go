package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vaktikos/host-managed/internal/agentapi"
	"github.com/vaktikos/host-managed/internal/agentclient"
	"github.com/vaktikos/host-managed/internal/backup"
)

// Backup-related fake agent state lives here to keep api_test.go readable.
type fakeBackups struct {
	list    []backup.Manifest
	pruned  []agentapi.PruneRequest
	failFor string
}

func (f *fakeAgent) Backups(ctx context.Context, store string) ([]backup.Manifest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]backup.Manifest(nil), f.bk.list...), nil
}

func (f *fakeAgent) Backup(ctx context.Context, kind, name string, b agentapi.BackupRequest, log func(string), progress func(done, total int64)) (*backup.Manifest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if kind+"/"+name == f.bk.failFor {
		return nil, &agentclient.Error{Status: 500, Code: "internal", Message: "disk full"}
	}
	log("backing up " + name)
	progress(1, 2)
	m := backup.Manifest{ID: backup.NewID(kind, name, time.Now().Add(time.Duration(len(f.bk.list))*time.Second)), Kind: kind, Name: name, Notes: b.Notes}
	f.bk.list = append(f.bk.list, m)
	return &m, nil
}

func (f *fakeAgent) Restore(ctx context.Context, r agentapi.Restore, log func(string), progress func(done, total int64)) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, "restore:"+r.ID+":"+r.Name)
	return json.RawMessage(`{}`), nil
}

func (f *fakeAgent) DeleteBackup(ctx context.Context, store, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, "delete:"+id)
	return nil
}

func (f *fakeAgent) ProtectBackup(context.Context, agentapi.BackupProtect) error { return nil }

func (f *fakeAgent) PruneBackups(ctx context.Context, p agentapi.PruneRequest) (*agentapi.PruneResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bk.pruned = append(f.bk.pruned, p)
	return &agentapi.PruneResult{Kept: []string{}, Removed: []string{}}, nil
}

func TestBackupPermissionsAndJobs(t *testing.T) {
	e := setup(t)
	e.agent.vms["web"] = &agentapi.VMDetail{VM: agentapi.VM{Name: "web", State: "running"}}
	viewer := e.client(t)
	viewer.login("viewer", "password-viewer")
	if code, _ := viewer.do("GET", "/api/v1/backups?store=backup", nil); code != 200 {
		t.Fatalf("auditor cannot list backups: %d", code)
	}
	if code, _ := viewer.do("POST", "/api/v1/vms/web/backup", map[string]any{"store": "backup"}); code != 403 {
		t.Fatalf("auditor may back up: %d", code)
	}
	op := e.client(t)
	op.login("op", "password-op")
	if code, body := op.do("POST", "/api/v1/vms/web/backup", map[string]any{"store": "local"}); code != 400 {
		t.Fatalf("non-backup store accepted: %d %v", code, body)
	}
	code, body := op.do("POST", "/api/v1/vms/web/backup", map[string]any{"store": "backup", "notes": "before upgrade"})
	if code != 202 {
		t.Fatalf("backup: %d %v", code, body)
	}
	j := waitJob(t, op, body["job"].(map[string]any)["id"].(string))
	if j["status"] != "succeeded" {
		t.Fatalf("backup job: %v", j)
	}
	id := e.agent.bk.list[0].ID
	if code, _ := op.do("POST", "/api/v1/backups/restore", map[string]any{"store": "backup", "id": id, "name": "web2", "pool": "local"}); code != 403 {
		t.Fatalf("operator may restore: %d", code)
	}
	if code, _ := op.do("DELETE", "/api/v1/backups?store=backup&id="+id, nil); code != 403 {
		t.Fatalf("operator may delete backups: %d", code)
	}
	admin := e.client(t)
	admin.login("admin", "password-admin")
	if code, _ := admin.do("POST", "/api/v1/backups/restore", map[string]any{"store": "backup", "id": id, "pool": "local"}); code != 400 {
		t.Fatalf("restore over existing VM accepted: %d", code)
	}
	if code, _ := admin.do("POST", "/api/v1/backups/restore", map[string]any{"store": "backup", "id": id, "name": "web2"}); code != 400 {
		t.Fatalf("VM restore without pool accepted: %d", code)
	}
	if code, _ := admin.do("POST", "/api/v1/backups/restore", map[string]any{"store": "backup", "id": "../../etc", "name": "x", "pool": "local"}); code != 400 {
		t.Fatalf("bad id accepted: %d", code)
	}
	code, body = admin.do("POST", "/api/v1/backups/restore", map[string]any{"store": "backup", "id": id, "name": "web2", "pool": "local"})
	if code != 202 {
		t.Fatalf("restore: %d %v", code, body)
	}
	waitJob(t, admin, body["job"].(map[string]any)["id"].(string))
	if code, _ := admin.do("DELETE", "/api/v1/backups?store=backup&id="+id, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := admin.do("POST", "/api/v1/backups/prune", map[string]any{"store": "backup", "kind": "vm", "name": "web", "keep": map[string]int{}}); code != 400 {
		t.Fatalf("empty retention accepted: %d", code)
	}
	acts := strings.Join(e.agent.actions, ",")
	if !strings.Contains(acts, "restore:"+id+":web2") || !strings.Contains(acts, "delete:"+id) {
		t.Fatalf("agent calls: %s", acts)
	}
}

func TestSchedules(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	admin.login("admin", "password-admin")
	good := map[string]any{"name": "nightly", "enabled": true, "store": "backup", "days": 127, "at": "02:30",
		"selection": "list", "targets": []string{"vm/web", "vm/db"}, "keep": map[string]int{"keep_last": 3}}
	for field, v := range map[string]any{"days": 0, "at": "25:00", "store": "local", "selection": "some", "targets": []string{"vm/../x"}, "mode": "hot"} {
		bad := map[string]any{}
		for k, x := range good {
			bad[k] = x
		}
		bad[field] = v
		if code, body := admin.do("POST", "/api/v1/backup-schedules", bad); code != 400 {
			t.Fatalf("%s=%v accepted: %d %v", field, v, code, body)
		}
	}
	code, body := admin.do("POST", "/api/v1/backup-schedules", good)
	if code != 201 || body["next_run"] == nil {
		t.Fatalf("create: %d %v", code, body)
	}
	if code, _ := admin.do("POST", "/api/v1/backup-schedules", good); code != 400 {
		t.Fatalf("duplicate name accepted: %d", code)
	}
	op := e.client(t)
	op.login("op", "password-op")
	if code, _ := op.do("POST", "/api/v1/backup-schedules", good); code != 403 {
		t.Fatalf("operator may create schedules: %d", code)
	}

	// Run now: db fails, web succeeds and is pruned; the job reports the failure.
	e.agent.bk.failFor = "vm/db"
	id := fmt.Sprint(int(body["id"].(float64)))
	code, body = admin.do("POST", "/api/v1/backup-schedules/"+id+"/run", nil)
	if code != 202 {
		t.Fatalf("run: %d %v", code, body)
	}
	j := waitJob(t, admin, body["job"].(map[string]any)["id"].(string))
	if j["status"] != "failed" || !strings.Contains(j["error"].(string), "vm/db") {
		t.Fatalf("job: %v", j)
	}
	if len(e.agent.bk.list) != 1 || len(e.agent.bk.pruned) != 1 || e.agent.bk.pruned[0].Keep.Last != 3 {
		t.Fatalf("backups %v pruned %v", e.agent.bk.list, e.agent.bk.pruned)
	}
	if code, _ := admin.do("DELETE", "/api/v1/backup-schedules/"+id, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
}

func TestScheduleSlots(t *testing.T) {
	loc := time.UTC
	// 2026-10-16 is a Friday.
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, loc)
	mon := 1 << time.Monday
	fri := 1 << time.Friday
	if got := lastSlot(now, mon, "03:00", loc); !got.Equal(time.Date(2026, 10, 12, 3, 0, 0, 0, loc)) {
		t.Fatalf("last monday: %v", got)
	}
	if got := lastSlot(now, fri, "13:00", loc); !got.Equal(time.Date(2026, 10, 9, 13, 0, 0, 0, loc)) {
		t.Fatalf("last friday 13:00: %v", got)
	}
	if got := nextSlot(now, fri, "13:00", loc); !got.Equal(time.Date(2026, 10, 16, 13, 0, 0, 0, loc)) {
		t.Fatalf("next friday: %v", got)
	}
	if got := nextSlot(now, fri, "11:00", loc); !got.Equal(time.Date(2026, 10, 23, 11, 0, 0, 0, loc)) {
		t.Fatalf("next week: %v", got)
	}
}

func TestSchedulerTick(t *testing.T) {
	e := setup(t)
	e.agent.vms["web"] = &agentapi.VMDetail{VM: agentapi.VM{Name: "web"}}
	admin := e.client(t)
	admin.login("admin", "password-admin")
	at := time.Now().Add(-2 * time.Minute).In(time.Local).Format("15:04")
	code, body := admin.do("POST", "/api/v1/backup-schedules", map[string]any{"name": "s", "enabled": true, "store": "backup",
		"days": 127, "at": at, "selection": "vms"})
	if code != 201 {
		t.Fatalf("create: %d %v", code, body)
	}
	// Created after the slot: nothing runs now.
	e.srvAPI.schedulerTick(context.Background(), time.Now())
	if len(e.agent.bk.list) != 0 {
		t.Fatal("slot before creation was run")
	}
	// A day later the slot is due.
	tomorrow := time.Now().Add(24 * time.Hour)
	e.srvAPI.schedulerTick(context.Background(), tomorrow)
	sc, _ := e.st.BackupSchedules(context.Background())
	if sc[0].LastJob == "" {
		t.Fatal("due schedule not started")
	}
	waitJob(t, admin, sc[0].LastJob)
	if len(e.agent.bk.list) != 1 {
		t.Fatalf("backups: %v", e.agent.bk.list)
	}
	// The same slot is not run twice.
	e.srvAPI.schedulerTick(context.Background(), tomorrow.Add(time.Minute))
	sc2, _ := e.st.BackupSchedules(context.Background())
	if sc2[0].LastJob != sc[0].LastJob {
		t.Fatal("slot ran twice")
	}
}
