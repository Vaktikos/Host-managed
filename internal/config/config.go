// Package config reads /etc/hostmanaged/hostmanaged.conf, a simple
// "key = value" file. Unknown keys are rejected so typos do not go unnoticed.
package config

import (
	"bufio"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Listen                 string
	TLSCert                string
	TLSKey                 string
	DataDir                string
	AgentSocket            string
	AgentGroup             string
	AllowedUser            string
	AllowSoftwareEmulation bool
	AllowPrivateDownloads  bool // permit downloads from private networks (internal mirrors)
	ProductName            string
	DefaultLanguage        string
	TrustedProxies         []netip.Prefix
	SessionIdleMinutes     int
	SessionMaxHours        int
	MaxUploadGiB           int64
	JobParallelism         int
	WebDir                 string // serve UI from disk instead of the embedded copy (development)
	InsecureHTTP           bool   // development only: plain HTTP without Secure cookies
}

func Defaults() Config {
	return Config{
		Listen:             "0.0.0.0:8443",
		TLSCert:            "/etc/hostmanaged/tls/server.crt",
		TLSKey:             "/etc/hostmanaged/tls/server.key",
		DataDir:            "/var/lib/hostmanaged",
		AgentSocket:        "/run/hostmanaged/agent.sock",
		AgentGroup:         "hostmanaged",
		AllowedUser:        "hostmanaged",
		ProductName:        "Host-managed",
		DefaultLanguage:    "de",
		SessionIdleMinutes: 120,
		SessionMaxHours:    12,
		MaxUploadGiB:       64,
		JobParallelism:     4,
	}
}

func Load(path string) (Config, error) {
	c := Defaults()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return c, fmt.Errorf("%s:%d: expected key = value", path, n)
		}
		k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"`)
		if err := c.set(k, v); err != nil {
			return c, fmt.Errorf("%s:%d: %w", path, n, err)
		}
	}
	if err := sc.Err(); err != nil {
		return c, err
	}
	return c, c.Validate()
}

func (c *Config) set(k, v string) error {
	var err error
	switch k {
	case "listen":
		c.Listen = v
	case "tls_cert":
		c.TLSCert = v
	case "tls_key":
		c.TLSKey = v
	case "data_dir":
		c.DataDir = v
	case "agent_socket":
		c.AgentSocket = v
	case "agent_group":
		c.AgentGroup = v
	case "allowed_user":
		c.AllowedUser = v
	case "allow_software_emulation":
		c.AllowSoftwareEmulation, err = strconv.ParseBool(v)
	case "allow_private_downloads":
		c.AllowPrivateDownloads, err = strconv.ParseBool(v)
	case "product_name":
		c.ProductName = v
	case "default_language":
		c.DefaultLanguage = v
	case "trusted_proxies":
		c.TrustedProxies = nil
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p == "" {
				continue
			}
			pfx, perr := netip.ParsePrefix(p)
			if perr != nil {
				return fmt.Errorf("trusted_proxies: %w", perr)
			}
			c.TrustedProxies = append(c.TrustedProxies, pfx)
		}
	case "session_idle_minutes":
		c.SessionIdleMinutes, err = strconv.Atoi(v)
	case "session_max_hours":
		c.SessionMaxHours, err = strconv.Atoi(v)
	case "max_upload_gib":
		c.MaxUploadGiB, err = strconv.ParseInt(v, 10, 64)
	case "job_parallelism":
		c.JobParallelism, err = strconv.Atoi(v)
	case "web_dir":
		c.WebDir = v
	case "insecure_http":
		c.InsecureHTTP, err = strconv.ParseBool(v)
	default:
		return fmt.Errorf("unknown key %q", k)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", k, err)
	}
	return nil
}

func (c *Config) Validate() error {
	if c.DefaultLanguage != "de" && c.DefaultLanguage != "en" {
		return fmt.Errorf("default_language must be de or en")
	}
	if c.SessionIdleMinutes < 5 || c.SessionIdleMinutes > 24*60 {
		return fmt.Errorf("session_idle_minutes must be between 5 and 1440")
	}
	if c.SessionMaxHours < 1 || c.SessionMaxHours > 24*7 {
		return fmt.Errorf("session_max_hours must be between 1 and 168")
	}
	if c.JobParallelism < 1 || c.JobParallelism > 64 {
		return fmt.Errorf("job_parallelism must be between 1 and 64")
	}
	if c.MaxUploadGiB < 1 {
		return fmt.Errorf("max_upload_gib must be positive")
	}
	if c.ProductName == "" || len(c.ProductName) > 64 {
		return fmt.Errorf("product_name must be 1-64 characters")
	}
	return nil
}
