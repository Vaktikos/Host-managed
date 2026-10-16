// Package fetch downloads files from user-supplied URLs with protection
// against server-side request forgery: only http/https, no credentials in the
// URL, and the target address must be public unless explicitly allowed.
package fetch

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"
)

var ErrBlocked = errors.New("destination not allowed")

// Options configure a download.
type Options struct {
	// AllowPrivate permits private, loopback and link-local destinations
	// (internal mirrors). Off by default.
	AllowPrivate bool
	MaxBytes     int64
	// Checksum is "sha256:<hex>", "sha512:<hex>" or a bare 64/128 hex digest.
	Checksum string
	// Progress is called at most every 500 ms; total is -1 if unknown.
	Progress func(done, total int64)
	Log      func(string)
}

// blockedPrefixes lists non-public ranges besides what netip classifies.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
}

// PublicAddr reports whether a is a globally routable unicast address.
func PublicAddr(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsMulticast() || a.IsUnspecified() || a.IsInterfaceLocalMulticast() || !a.IsGlobalUnicast() {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// ValidateURL checks scheme and form of a download URL.
func ValidateURL(raw string) (*url.URL, error) {
	if len(raw) > 2048 {
		return nil, fmt.Errorf("%w: URL too long", ErrBlocked)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("%w: only http and https are allowed", ErrBlocked)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: credentials in URLs are not allowed", ErrBlocked)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("invalid URL: missing host")
	}
	return u, nil
}

// checkHost resolves host and rejects it if any address is not public.
func checkHost(ctx context.Context, host string, allowPrivate bool) error {
	if allowPrivate {
		return nil
	}
	if a, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		if !PublicAddr(a) {
			return fmt.Errorf("%w: %s is not a public address", ErrBlocked, a)
		}
		return nil
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", host, err)
	}
	for _, a := range addrs {
		if !PublicAddr(a) {
			return fmt.Errorf("%w: %s resolves to non-public address %s", ErrBlocked, host, a)
		}
	}
	return nil
}

// Client builds an HTTP client that enforces the destination policy. Without a
// proxy the check runs on the actual connected address (no DNS rebinding);
// with a proxy the target host is resolved and checked before each request.
func Client(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyFromEnvironment
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		if !allowPrivate && !viaProxy(ctx) {
			ap, perr := netip.ParseAddrPort(conn.RemoteAddr().String())
			if perr != nil || !PublicAddr(ap.Addr()) {
				conn.Close()
				return nil, fmt.Errorf("%w: connection to %s", ErrBlocked, conn.RemoteAddr())
			}
		}
		return conn, nil
	}
	return &http.Client{
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if _, err := ValidateURL(req.URL.String()); err != nil {
				return err
			}
			return checkHost(req.Context(), req.URL.Hostname(), allowPrivate)
		},
	}
}

type proxyKey struct{}

func viaProxy(ctx context.Context) bool { v, _ := ctx.Value(proxyKey{}).(bool); return v }

func parseChecksum(s string) (hash.Hash, []byte, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return nil, nil, nil
	}
	algo := ""
	if a, d, ok := strings.Cut(s, ":"); ok {
		algo, s = a, d
	}
	want, err := hex.DecodeString(s)
	if err != nil {
		return nil, nil, fmt.Errorf("checksum must be hex encoded")
	}
	switch {
	case (algo == "" || algo == "sha256") && len(want) == 32:
		return sha256.New(), want, nil
	case (algo == "" || algo == "sha512") && len(want) == 64:
		return sha512.New(), want, nil
	}
	return nil, nil, fmt.Errorf("checksum must be a SHA-256 or SHA-512 digest")
}

// ValidateChecksum checks the checksum syntax without downloading.
func ValidateChecksum(s string) error { _, _, err := parseChecksum(s); return err }

// Download fetches rawURL into target. Data goes to target+".part" first and
// is renamed only after the size limit and checksum checks passed.
func Download(ctx context.Context, rawURL, target string, o Options) (int64, error) {
	u, err := ValidateURL(rawURL)
	if err != nil {
		return 0, err
	}
	h, want, err := parseChecksum(o.Checksum)
	if err != nil {
		return 0, err
	}
	if err := checkHost(ctx, u.Hostname(), o.AllowPrivate); err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, err
	}
	if p, _ := http.ProxyFromEnvironment(req); p != nil {
		req = req.WithContext(context.WithValue(ctx, proxyKey{}, true))
	}
	req.Header.Set("User-Agent", "host-managed")
	resp, err := Client(o.AllowPrivate).Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("server answered %s", resp.Status)
	}
	total := resp.ContentLength
	if o.MaxBytes > 0 && total > o.MaxBytes {
		return 0, fmt.Errorf("file is %d bytes, limit is %d", total, o.MaxBytes)
	}
	if o.Log != nil {
		if total > 0 {
			o.Log(fmt.Sprintf("Downloading %s (%d bytes)", u.Redacted(), total))
		} else {
			o.Log(fmt.Sprintf("Downloading %s (size unknown)", u.Redacted()))
		}
	}
	part := target + ".part"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, fmt.Errorf("a download to this file is already running or the pool is not writable")
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(part)
		}
	}()
	var w io.Writer = f
	if h != nil {
		w = io.MultiWriter(f, h)
	}
	limit := o.MaxBytes
	if limit <= 0 {
		limit = 1 << 50
	}
	var done int64
	buf := make([]byte, 1<<20)
	last := time.Now()
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if done+int64(n) > limit {
				return done, fmt.Errorf("file exceeds the limit of %d bytes", limit)
			}
			if _, werr := w.Write(buf[:n]); werr != nil {
				return done, werr
			}
			done += int64(n)
			if o.Progress != nil && time.Since(last) > 500*time.Millisecond {
				o.Progress(done, total)
				last = time.Now()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return done, rerr
		}
	}
	if total >= 0 && done != total {
		return done, fmt.Errorf("incomplete download: %d of %d bytes", done, total)
	}
	if o.Progress != nil {
		o.Progress(done, total)
	}
	if h != nil {
		got := h.Sum(nil)
		if hex.EncodeToString(got) != hex.EncodeToString(want) {
			return done, fmt.Errorf("checksum mismatch: got %x", got)
		}
		if o.Log != nil {
			o.Log("Checksum verified")
		}
	} else if o.Log != nil {
		o.Log("No checksum given; file integrity not verified")
	}
	if err := f.Sync(); err != nil {
		return done, err
	}
	if err := f.Close(); err != nil {
		return done, err
	}
	if _, err := os.Lstat(target); err == nil {
		return done, fmt.Errorf("%s already exists", target)
	}
	if err := os.Rename(part, target); err != nil {
		return done, err
	}
	ok = true
	return done, nil
}

// FileNameFromURL proposes a file name from the last path element.
func FileNameFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	p := u.Path
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		p = p[i+1:]
	}
	p, _ = url.PathUnescape(p)
	return p
}
