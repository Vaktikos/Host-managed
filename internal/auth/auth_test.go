package auth

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$") {
		t.Fatalf("hash format: %s", h)
	}
	if !VerifyPassword("correct horse battery", h) {
		t.Fatal("valid password rejected")
	}
	if VerifyPassword("wrong", h) {
		t.Fatal("wrong password accepted")
	}
	if VerifyPassword("x", "$argon2id$v=19$m=999999999,t=1,p=1$AA$AA") {
		t.Fatal("malformed hash accepted")
	}
	if CheckPasswordPolicy("short") == nil {
		t.Fatal("short password accepted")
	}
	if p := RandomPassword(); len(p) != 20 || CheckPasswordPolicy(p) != nil {
		t.Fatalf("random password %q", p)
	}
}

func TestRoles(t *testing.T) {
	if !Allowed("admin", PermSysUsers) {
		t.Fatal("admin lacks user management")
	}
	if Allowed("operator", PermVMDelete) || Allowed("operator", PermSysUsers) {
		t.Fatal("operator has too many rights")
	}
	if Allowed("auditor", PermVMPower) || !Allowed("auditor", PermSysAudit) {
		t.Fatal("auditor rights wrong")
	}
	if Allowed("nonexistent", PermVMView) || ValidRole("root") {
		t.Fatal("unknown role has rights")
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(3, 50*time.Millisecond)
	for i := 0; i < 3; i++ {
		if b, _ := l.Blocked("ip"); b {
			t.Fatal("blocked too early")
		}
		l.Fail("ip")
	}
	if b, _ := l.Blocked("ip"); !b {
		t.Fatal("not blocked after limit")
	}
	time.Sleep(60 * time.Millisecond)
	if b, _ := l.Blocked("ip"); b {
		t.Fatal("still blocked after window")
	}
}
