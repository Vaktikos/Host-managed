package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "hm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrationsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hm.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	v, _ := s.SchemaVersion(context.Background())
	if v != len(migrations) {
		t.Fatalf("version %d", v)
	}
}

func TestRefusesNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hm.db")
	s, _ := Open(path)
	s.db.Exec(`UPDATE schema_version SET version = 999`)
	s.Close()
	if _, err := Open(path); err == nil {
		t.Fatal("opened a database with a newer schema")
	}
}

func TestUsersAndSessions(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx, "admin", "hash", "admin", "de")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, "admin", "x", "admin", "de"); err != ErrExists {
		t.Fatalf("duplicate: %v", err)
	}
	if err := s.CreateSession(ctx, "tok", "csrf", u.ID, time.Hour, "127.0.0.1", "test"); err != nil {
		t.Fatal(err)
	}
	se, err := s.SessionByToken(ctx, "tok")
	if err != nil || se.UserID != u.ID || se.CSRF != "csrf" {
		t.Fatalf("session: %+v %v", se, err)
	}
	if se.ID == "tok" {
		t.Fatal("session token stored in clear text")
	}
	if err := s.CreateSession(ctx, "old", "c", u.ID, -time.Second, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionByToken(ctx, "old"); err != ErrNotFound {
		t.Fatal("expired session accepted")
	}
	if err := s.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionByToken(ctx, "tok"); err != ErrNotFound {
		t.Fatal("session survived user deletion")
	}
}

func TestAuditChain(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := s.Audit(ctx, AuditEntry{Username: "admin", Action: "vm.start", Resource: "vm/test", Result: "ok"}); err != nil {
			t.Fatal(err)
		}
	}
	n, broken, err := s.VerifyAudit(ctx)
	if err != nil || n != 5 || broken != 0 {
		t.Fatalf("verify: %d %d %v", n, broken, err)
	}
	if _, err := s.db.Exec(`UPDATE audit SET result = 'x' WHERE id = 2`); err == nil {
		t.Fatal("audit update allowed")
	}
	if _, err := s.db.Exec(`DELETE FROM audit WHERE id = 2`); err == nil {
		t.Fatal("audit delete allowed")
	}
	// Bypass the trigger as an attacker with file access would.
	if _, err := s.db.Exec(`DROP TRIGGER audit_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE audit SET result = 'forged' WHERE id = 3`); err != nil {
		t.Fatal(err)
	}
	_, broken, _ = s.VerifyAudit(ctx)
	if broken != 3 {
		t.Fatalf("tampering not detected, broken=%d", broken)
	}
}

func TestJobs(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	j := &Job{ID: "j1", Type: "vm.create", Target: "vm/a", Status: "queued", Username: "admin", CreatedAt: time.Now(), Params: "{}"}
	if err := s.CreateJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	s.SetJobRunning(ctx, "j1")
	s.AppendJobLog(ctx, "j1", "line 1")
	s.AppendJobLog(ctx, "j1", "line 2")
	logs, _ := s.JobLog(ctx, "j1", 0)
	if len(logs) != 2 {
		t.Fatalf("logs: %+v", logs)
	}
	more, _ := s.JobLog(ctx, "j1", logs[0].ID)
	if len(more) != 1 || more[0].Line != "line 2" {
		t.Fatalf("incremental logs: %+v", more)
	}
	n, _ := s.FailInterrupted(ctx)
	if n != 1 {
		t.Fatalf("interrupted: %d", n)
	}
	got, _ := s.Job(ctx, "j1")
	if got.Status != "failed" || got.FinishedAt == nil {
		t.Fatalf("job: %+v", got)
	}
	list, total, _ := s.Jobs(ctx, JobFilter{Target: "vm/a"})
	if total != 1 || len(list) != 1 {
		t.Fatalf("list: %d", total)
	}
}

func TestBackup(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	s.CreateUser(ctx, "admin", "h", "admin", "de")
	dst := filepath.Join(t.TempDir(), "backup.db")
	if err := s.Backup(ctx, dst); err != nil {
		t.Fatal(err)
	}
	db, _ := sql.Open("sqlite", dst)
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("backup content: %d %v", n, err)
	}
}
