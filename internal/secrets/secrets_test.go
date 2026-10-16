package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSealOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	b, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %v", st.Mode())
	}
	s, _ := b.Seal("JBSWY3DPEHPK3PXP", "totp:1")
	if got, err := b.Open(s, "totp:1"); err != nil || got != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("open: %q %v", got, err)
	}
	if _, err := b.Open(s, "totp:2"); err == nil {
		t.Fatal("ciphertext accepted for another purpose")
	}
	b2, _ := LoadOrCreate(path)
	if _, err := b2.Open(s, "totp:1"); err != nil {
		t.Fatal("reloaded key cannot open")
	}
	other, _ := LoadOrCreate(filepath.Join(t.TempDir(), "k2"))
	if _, err := other.Open(s, "totp:1"); err == nil {
		t.Fatal("wrong key opened secret")
	}
}
