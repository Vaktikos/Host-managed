package totp

import (
	"testing"
	"time"
)

// RFC 6238 appendix B test vector (SHA1, secret "12345678901234567890").
func TestRFCVector(t *testing.T) {
	secret := enc.EncodeToString([]byte("12345678901234567890"))
	for ts, want := range map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"} {
		got, _ := Code(secret, time.Unix(ts, 0))
		if got != want {
			t.Errorf("t=%d: %s, want %s", ts, got, want)
		}
	}
}

func TestVerifyWindowAndReplay(t *testing.T) {
	s := NewSecret()
	now := time.Unix(1_800_000_000, 0)
	c, _ := Code(s, now.Add(-30*time.Second))
	step, ok := Verify(s, c, now, 0)
	if !ok {
		t.Fatal("previous step rejected")
	}
	if _, ok := Verify(s, c, now, step); ok {
		t.Fatal("replay accepted")
	}
	old, _ := Code(s, now.Add(-90*time.Second))
	if _, ok := Verify(s, old, now, 0); ok {
		t.Fatal("code outside window accepted")
	}
	if _, ok := Verify(s, "12345", now, 0); ok {
		t.Fatal("short code accepted")
	}
	if u := URI("Host-managed", "admin", s); u[:15] != "otpauth://totp/" {
		t.Fatal(u)
	}
}
