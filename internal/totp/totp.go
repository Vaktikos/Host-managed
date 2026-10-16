// Package totp implements RFC 6238 time-based one-time passwords
// (HMAC-SHA1, 30 s, 6 digits), compatible with common authenticator apps.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	period = 30
	digits = 6
)

var enc = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret returns a random 160-bit secret, base32 encoded.
func NewSecret() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return enc.EncodeToString(b)
}

// URI builds the otpauth:// URI for QR codes.
func URI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{"secret": {secret}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

func code(secret string, step int64) (string, error) {
	key, err := enc.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", err
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", v%1000000), nil
}

// Code returns the code for time t (used by tests and enrollment checks).
func Code(secret string, t time.Time) (string, error) { return code(secret, t.Unix()/period) }

// Verify checks a code against the current step ±1 and returns the matched
// step. Codes for steps <= lastStep are rejected (no replay).
func Verify(secret, input string, now time.Time, lastStep int64) (int64, bool) {
	input = strings.ReplaceAll(strings.TrimSpace(input), " ", "")
	if len(input) != digits {
		return 0, false
	}
	cur := now.Unix() / period
	for _, s := range []int64{cur - 1, cur, cur + 1} {
		if s <= lastStep {
			continue
		}
		c, err := code(secret, s)
		if err == nil && subtle.ConstantTimeCompare([]byte(c), []byte(input)) == 1 {
			return s, true
		}
	}
	return 0, false
}
