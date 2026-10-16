package fetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicAddr(t *testing.T) {
	for s, want := range map[string]bool{
		"1.1.1.1": true, "2606:4700:4700::1111": true,
		"127.0.0.1": false, "10.1.2.3": false, "192.168.1.1": false, "172.16.0.1": false,
		"169.254.169.254": false, "100.64.0.1": false, "::1": false, "fe80::1": false,
		"fc00::1": false, "0.0.0.0": false, "224.0.0.1": false, "::ffff:127.0.0.1": false,
	} {
		if got := PublicAddr(netip.MustParseAddr(s)); got != want {
			t.Errorf("%s: %v, want %v", s, got, want)
		}
	}
}

func TestBlockedURLs(t *testing.T) {
	dir := t.TempDir()
	for _, u := range []string{
		"file:///etc/passwd", "ftp://example.com/x.iso", "http://127.0.0.1/x.iso",
		"http://169.254.169.254/latest/meta-data", "http://[::1]/x", "http://user:pw@example.com/x",
		"http://10.0.0.5/x.iso", "gopher://x",
	} {
		_, err := Download(context.Background(), u, filepath.Join(dir, "x"), Options{})
		if err == nil || !errors.Is(err, ErrBlocked) {
			t.Errorf("%s: expected ErrBlocked, got %v", u, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "x.part")); err == nil {
		t.Fatal("part file left behind")
	}
}

func serve(t *testing.T, body string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/file.iso":
			w.Write([]byte(body))
		case "/redirect":
			http.Redirect(w, r, "http://127.0.0.1:1/x", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDownload(t *testing.T) {
	body := strings.Repeat("iso-data", 100000)
	srv := serve(t, body)
	dir := t.TempDir()
	sum := sha256.Sum256([]byte(body))
	var lastDone int64
	n, err := Download(context.Background(), srv.URL+"/file.iso", filepath.Join(dir, "a.iso"), Options{
		AllowPrivate: true, Checksum: "sha256:" + hex.EncodeToString(sum[:]),
		Progress: func(d, tot int64) { lastDone = d },
	})
	if err != nil || n != int64(len(body)) || lastDone != n {
		t.Fatalf("download: n=%d err=%v progress=%d", n, err, lastDone)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "a.iso"))
	if string(got) != body {
		t.Fatal("content differs")
	}

	// Wrong checksum: nothing is kept.
	_, err = Download(context.Background(), srv.URL+"/file.iso", filepath.Join(dir, "b.iso"), Options{AllowPrivate: true, Checksum: strings.Repeat("0", 64)})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("checksum: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.iso")); err == nil {
		t.Fatal("file kept despite checksum mismatch")
	}
	// Size limit.
	if _, err := Download(context.Background(), srv.URL+"/file.iso", filepath.Join(dir, "c.iso"), Options{AllowPrivate: true, MaxBytes: 1000}); err == nil {
		t.Fatal("size limit ignored")
	}
	// HTTP errors.
	if _, err := Download(context.Background(), srv.URL+"/missing", filepath.Join(dir, "d.iso"), Options{AllowPrivate: true}); err == nil {
		t.Fatal("404 accepted")
	}
	// Existing file is not overwritten.
	if _, err := Download(context.Background(), srv.URL+"/file.iso", filepath.Join(dir, "a.iso"), Options{AllowPrivate: true}); err == nil {
		t.Fatal("overwrote existing file")
	}
	// Loopback blocked without AllowPrivate.
	if _, err := Download(context.Background(), srv.URL+"/file.iso", filepath.Join(dir, "e.iso"), Options{}); !errors.Is(err, ErrBlocked) {
		t.Fatalf("loopback not blocked: %v", err)
	}
	// Cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Download(ctx, srv.URL+"/file.iso", filepath.Join(dir, "f.iso"), Options{AllowPrivate: true}); err == nil {
		t.Fatal("cancelled download succeeded")
	}
}

func TestRedirectToPrivateBlocked(t *testing.T) {
	c := Client(false)
	req, _ := http.NewRequest("GET", "http://127.0.0.1:1/x", nil)
	if err := c.CheckRedirect(req, []*http.Request{req}); !errors.Is(err, ErrBlocked) {
		t.Fatalf("redirect to loopback: %v", err)
	}
	req2, _ := http.NewRequest("GET", "file:///etc/passwd", nil)
	if err := c.CheckRedirect(req2, nil); err == nil {
		t.Fatal("redirect to file: allowed")
	}
}

func TestChecksumAndName(t *testing.T) {
	if ValidateChecksum("sha256:zz") == nil || ValidateChecksum("abc") == nil {
		t.Fatal("bad checksum accepted")
	}
	if ValidateChecksum(strings.Repeat("a", 128)) != nil || ValidateChecksum("") != nil {
		t.Fatal("valid checksum rejected")
	}
	if n := FileNameFromURL("https://cdimage.debian.org/x/debian-13.1.0-amd64-netinst.iso?y=1"); n != "debian-13.1.0-amd64-netinst.iso" {
		t.Fatalf("name %q", n)
	}
}
