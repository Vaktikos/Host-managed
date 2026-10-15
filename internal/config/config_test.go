package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	p := filepath.Join(t.TempDir(), "hm.conf")
	os.WriteFile(p, []byte("# comment\nlisten = 127.0.0.1:9000\nallow_software_emulation = true\ntrusted_proxies = 10.0.0.0/8, 192.168.1.1/32\n"), 0o644)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:9000" || !c.AllowSoftwareEmulation || len(c.TrustedProxies) != 2 || c.DefaultLanguage != "de" {
		t.Fatalf("%+v", c)
	}
	os.WriteFile(p, []byte("lisen = x\n"), 0o644)
	if _, err := Load(p); err == nil {
		t.Fatal("unknown key accepted")
	}
	os.WriteFile(p, []byte("default_language = fr\n"), 0o644)
	if _, err := Load(p); err == nil {
		t.Fatal("invalid language accepted")
	}
}
