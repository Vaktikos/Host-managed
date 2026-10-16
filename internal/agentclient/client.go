// Package agentclient is the control plane's client for the node agent.
package agentclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/vaktikos/host-managed/internal/agentapi"
)

// Error is a structured error returned by the agent.
type Error struct {
	Status  int
	Code    string
	Message string
	Field   string
}

func (e *Error) Error() string { return e.Message }

// ErrUnavailable means the agent could not be reached at all.
var ErrUnavailable = errors.New("node agent unavailable")

type Client struct {
	socket string
	hc     *http.Client // short requests
	long   *http.Client // streamed operations without overall timeout
}

func New(socket string) *Client {
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socket)
	}
	return &Client{
		socket: socket,
		hc:     &http.Client{Timeout: 2 * time.Minute, Transport: &http.Transport{DialContext: dial, MaxIdleConns: 8}},
		long:   &http.Client{Transport: &http.Transport{DialContext: dial}},
	}
}

func (c *Client) do(ctx context.Context, hc *http.Client, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://agent"+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return decodeErr(resp)
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func decodeErr(resp *http.Response) error {
	var e struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Field   string `json:"field"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&e)
	if e.Message == "" {
		e.Message = resp.Status
	}
	return &Error{Status: resp.StatusCode, Code: e.Code, Message: e.Message, Field: e.Field}
}

func esc(s string) string { return url.PathEscape(s) }

func (c *Client) Host(ctx context.Context) (*agentapi.Host, error) {
	var h agentapi.Host
	return &h, c.do(ctx, c.hc, "GET", "/v1/host", nil, &h)
}

func (c *Client) Interfaces(ctx context.Context) ([]agentapi.Interface, error) {
	var v []agentapi.Interface
	return v, c.do(ctx, c.hc, "GET", "/v1/interfaces", nil, &v)
}

func (c *Client) Networks(ctx context.Context) ([]agentapi.VirtNetwork, error) {
	var v []agentapi.VirtNetwork
	return v, c.do(ctx, c.hc, "GET", "/v1/networks", nil, &v)
}

func (c *Client) Pools(ctx context.Context) ([]agentapi.StoragePool, error) {
	var v []agentapi.StoragePool
	return v, c.do(ctx, c.hc, "GET", "/v1/pools", nil, &v)
}

func (c *Client) Volumes(ctx context.Context, pool string) ([]agentapi.StorageVolume, error) {
	var v []agentapi.StorageVolume
	return v, c.do(ctx, c.hc, "GET", "/v1/pools/"+esc(pool)+"/volumes", nil, &v)
}

func (c *Client) DeleteVolume(ctx context.Context, pool, vol string) error {
	return c.do(ctx, c.hc, "DELETE", "/v1/pools/"+esc(pool)+"/volumes/"+esc(vol), nil, nil)
}

// Upload streams body into a directory pool.
func (c *Client) Upload(ctx context.Context, pool, name string, size int64, body io.Reader) error {
	req, err := http.NewRequestWithContext(ctx, "PUT", "http://agent/v1/pools/"+esc(pool)+"/upload/"+esc(name), body)
	if err != nil {
		return err
	}
	req.ContentLength = size
	resp, err := c.long.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return decodeErr(resp)
	}
	return nil
}

func (c *Client) VMs(ctx context.Context) ([]agentapi.VM, error) {
	var v []agentapi.VM
	return v, c.do(ctx, c.hc, "GET", "/v1/vms", nil, &v)
}

func (c *Client) VM(ctx context.Context, name string) (*agentapi.VMDetail, error) {
	var v agentapi.VMDetail
	return &v, c.do(ctx, c.hc, "GET", "/v1/vms/"+esc(name), nil, &v)
}

func (c *Client) VMAction(ctx context.Context, name, action string) error {
	return c.do(ctx, c.hc, "POST", "/v1/vms/"+esc(name)+"/action", agentapi.Action{Action: action}, nil)
}

func (c *Client) VMMeta(ctx context.Context, name string, m agentapi.VMMeta) error {
	return c.do(ctx, c.hc, "PUT", "/v1/vms/"+esc(name)+"/meta", m, nil)
}

func (c *Client) VMResources(ctx context.Context, name string, r agentapi.VMResources) (bool, error) {
	var out struct {
		PendingRestart bool `json:"pending_restart"`
	}
	err := c.do(ctx, c.hc, "PUT", "/v1/vms/"+esc(name)+"/resources", r, &out)
	return out.PendingRestart, err
}

func (c *Client) VMTemplate(ctx context.Context, name string, template bool) error {
	return c.do(ctx, c.hc, "PUT", "/v1/vms/"+esc(name)+"/template", map[string]bool{"template": template}, nil)
}

func (c *Client) DeleteVM(ctx context.Context, name string, disks bool) (removed []string, warning string, err error) {
	q := ""
	if disks {
		q = "?disks=1"
	}
	var out struct {
		Removed []string `json:"removed_disks"`
		Warning string   `json:"warning"`
	}
	err = c.do(ctx, c.hc, "DELETE", "/v1/vms/"+esc(name)+q, nil, &out)
	return out.Removed, out.Warning, err
}

func (c *Client) VMStats(ctx context.Context, name string) ([]agentapi.VMStats, error) {
	var v []agentapi.VMStats
	return v, c.do(ctx, c.hc, "GET", "/v1/vms/"+esc(name)+"/stats", nil, &v)
}

func (c *Client) LatestStats(ctx context.Context) (map[string]agentapi.VMStats, error) {
	var v map[string]agentapi.VMStats
	return v, c.do(ctx, c.hc, "GET", "/v1/stats/latest", nil, &v)
}

func (c *Client) VMGuest(ctx context.Context, name string) (*agentapi.GuestInfo, error) {
	var v agentapi.GuestInfo
	return &v, c.do(ctx, c.hc, "GET", "/v1/vms/"+esc(name)+"/guest", nil, &v)
}

func (c *Client) Containers(ctx context.Context) ([]agentapi.Container, error) {
	var v []agentapi.Container
	return v, c.do(ctx, c.hc, "GET", "/v1/containers", nil, &v)
}

func (c *Client) Container(ctx context.Context, name string) (*agentapi.Container, error) {
	var v agentapi.Container
	return &v, c.do(ctx, c.hc, "GET", "/v1/containers/"+esc(name), nil, &v)
}

func (c *Client) ContainerAction(ctx context.Context, name, action string) error {
	return c.do(ctx, c.hc, "POST", "/v1/containers/"+esc(name)+"/action", agentapi.Action{Action: action}, nil)
}

type ContainerConfig struct {
	MemoryMiB   uint64  `json:"memory_mib"`
	SwapMiB     uint64  `json:"swap_mib"`
	CPULimit    float64 `json:"cpu_limit"`
	Autostart   *bool   `json:"autostart,omitempty"`
	Description *string `json:"description,omitempty"`
}

func (c *Client) ContainerConfig(ctx context.Context, name string, cfg ContainerConfig) error {
	return c.do(ctx, c.hc, "PUT", "/v1/containers/"+esc(name)+"/config", cfg, nil)
}

func (c *Client) DeleteContainer(ctx context.Context, name string) error {
	return c.do(ctx, c.hc, "DELETE", "/v1/containers/"+esc(name), nil, nil)
}

type Image struct {
	Distribution string `json:"distribution"`
	Release      string `json:"release"`
	Arch         string `json:"arch"`
	Build        string `json:"build"`
}

func (c *Client) LXCImages(ctx context.Context, arch string) ([]Image, error) {
	var v []Image
	return v, c.do(ctx, c.hc, "GET", "/v1/lxc/images?arch="+url.QueryEscape(arch), nil, &v)
}

// stream runs a long operation and forwards progress lines to log.
func (c *Client) stream(ctx context.Context, path string, body any, log func(string), progress func(done, total int64), out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "http://agent"+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.long.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return decodeErr(resp)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var line struct {
			Log      *string `json:"log"`
			Progress *struct {
				Done  int64 `json:"done"`
				Total int64 `json:"total"`
			} `json:"progress"`
			Result json.RawMessage `json:"result"`
			Error  *agentapi.Error `json:"error"`
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			continue
		}
		switch {
		case line.Log != nil:
			log(*line.Log)
		case line.Progress != nil:
			if progress != nil {
				progress(line.Progress.Done, line.Progress.Total)
			}
		case line.Error != nil:
			status := 500
			switch line.Error.Code {
			case "validation":
				status = 400
			case "not_found":
				status = 404
			case "conflict":
				status = 409
			}
			return &Error{Status: status, Code: line.Error.Code, Message: line.Error.Message}
		case line.Result != nil:
			if out != nil {
				return json.Unmarshal(line.Result, out)
			}
			return nil
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("%w: stream interrupted: %v", ErrUnavailable, err)
	}
	return fmt.Errorf("%w: agent closed the stream without a result", ErrUnavailable)
}

func (c *Client) CreateVM(ctx context.Context, spec agentapi.VMCreate, log func(string)) (*agentapi.VMDetail, error) {
	var v agentapi.VMDetail
	return &v, c.stream(ctx, "/v1/vms", spec, log, nil, &v)
}

func (c *Client) CreateContainer(ctx context.Context, spec agentapi.ContainerCreate, log func(string)) (*agentapi.Container, error) {
	var v agentapi.Container
	return &v, c.stream(ctx, "/v1/containers", spec, log, nil, &v)
}

// Download fetches a URL into a pool on the node.
func (c *Client) Download(ctx context.Context, pool string, d agentapi.Download, log func(string), progress func(done, total int64)) error {
	return c.stream(ctx, "/v1/pools/"+esc(pool)+"/download", d, log, progress, nil)
}

// OpenStream upgrades a connection to a raw byte stream (VNC or terminal).
func (c *Client) OpenStream(ctx context.Context, path string) (net.Conn, *bufio.Reader, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socket)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	req, _ := http.NewRequest("GET", "http://agent"+path, nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "hm-stream")
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer conn.Close()
		return nil, nil, decodeErr(resp)
	}
	return conn, br, nil
}

func (c *Client) VNC(ctx context.Context, name string) (net.Conn, *bufio.Reader, error) {
	return c.OpenStream(ctx, "/v1/vms/"+esc(name)+"/vnc")
}

func (c *Client) ContainerConsole(ctx context.Context, name string) (net.Conn, *bufio.Reader, error) {
	return c.OpenStream(ctx, "/v1/containers/"+esc(name)+"/console")
}
