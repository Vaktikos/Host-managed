// Package hostinfo reads hardware and runtime information from /proc and /sys.
package hostinfo

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/vaktikos/host-managed/internal/agentapi"
)

// Root allows tests to point the readers at a fixture tree.
var Root = "/"

func p(path string) string { return filepath.Join(Root, path) }

func readFile(path string) string {
	b, err := os.ReadFile(p(path))
	if err != nil {
		return ""
	}
	return string(b)
}

// CPUSampler computes host CPU usage from /proc/stat deltas.
type CPUSampler struct {
	mu          sync.Mutex
	idle, total uint64
	usage       *float64
}

func (s *CPUSampler) Sample() {
	idle, total, ok := readCPUStat()
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.total != 0 && total > s.total {
		u := (1 - float64(idle-s.idle)/float64(total-s.total)) * 100
		if u < 0 {
			u = 0
		}
		s.usage = &u
	}
	s.idle, s.total = idle, total
}

func (s *CPUSampler) Usage() *float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.usage == nil {
		return nil
	}
	v := *s.usage
	return &v
}

// Run samples every interval until stop is closed.
func (s *CPUSampler) Run(interval time.Duration, stop <-chan struct{}) {
	s.Sample()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			s.Sample()
		}
	}
}

func readCPUStat() (idle, total uint64, ok bool) {
	for _, line := range strings.Split(readFile("proc/stat"), "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || f[0] != "cpu" {
			continue
		}
		for i, v := range f[1:] {
			n, _ := strconv.ParseUint(v, 10, 64)
			if i >= 8 { // guest time is already part of user time
				break
			}
			total += n
			if i == 3 || i == 4 { // idle + iowait
				idle += n
			}
		}
		return idle, total, true
	}
	return 0, 0, false
}

// Collect gathers host information. Versions are supplied by the caller.
func Collect(cpu *CPUSampler) agentapi.Host {
	h := agentapi.Host{Time: time.Now()}
	h.Hostname, _ = os.Hostname()
	var u unix.Utsname
	if unix.Uname(&u) == nil {
		h.Kernel = unix.ByteSliceToString(u.Release[:])
		h.Arch = unix.ByteSliceToString(u.Machine[:])
	}
	h.OS = osRelease()

	sockets := map[string]bool{}
	for _, line := range strings.Split(readFile("proc/cpuinfo"), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "processor":
			h.CPUThreads++
		case "model name":
			if h.CPUModel == "" {
				h.CPUModel = v
			}
		case "physical id":
			sockets[v] = true
		case "flags":
			if h.VirtExtensions == "" {
				for _, f := range strings.Fields(v) {
					if f == "vmx" || f == "svm" {
						h.VirtExtensions = f
					}
				}
			}
		}
	}
	h.CPUSockets = len(sockets)
	if h.CPUSockets == 0 && h.CPUThreads > 0 {
		h.CPUSockets = 1
	}

	mi := meminfo()
	h.MemTotal, h.MemAvailable = mi["MemTotal"], mi["MemAvailable"]
	h.SwapTotal, h.SwapFree = mi["SwapTotal"], mi["SwapFree"]

	if f := strings.Fields(readFile("proc/loadavg")); len(f) >= 3 {
		for i := 0; i < 3; i++ {
			h.Load[i], _ = strconv.ParseFloat(f[i], 64)
		}
	}
	if f := strings.Fields(readFile("proc/uptime")); len(f) >= 1 {
		up, _ := strconv.ParseFloat(f[0], 64)
		h.UptimeSeconds = uint64(up)
	}
	if _, err := os.Stat(p("dev/kvm")); err == nil {
		h.KVMAvailable = true
	}
	if ents, err := os.ReadDir(p("sys/kernel/iommu_groups")); err == nil && len(ents) > 0 {
		h.IOMMUEnabled = true
	}
	h.BootMode = "bios"
	if _, err := os.Stat(p("sys/firmware/efi")); err == nil {
		h.BootMode = "uefi"
	}
	if cpu != nil {
		h.CPUUsage = cpu.Usage()
	}
	h.Filesystems = Mounts()
	return h
}

func osRelease() string {
	for _, line := range strings.Split(readFile("etc/os-release"), "\n") {
		if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return ""
}

// meminfo returns /proc/meminfo values in bytes.
func meminfo() map[string]uint64 {
	out := map[string]uint64{}
	sc := bufio.NewScanner(strings.NewReader(readFile("proc/meminfo")))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		n, _ := strconv.ParseUint(f[0], 10, 64)
		if len(f) > 1 && f[1] == "kB" {
			n *= 1024
		}
		out[k] = n
	}
	return out
}

var realFS = map[string]bool{
	"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true, "zfs": true,
	"vfat": true, "nfs": true, "nfs4": true, "cifs": true, "f2fs": true, "ceph": true,
}

// Mounts lists mounted filesystems that hold data (no pseudo filesystems).
func Mounts() []agentapi.HostMount {
	var out []agentapi.HostMount
	seen := map[string]bool{}
	for _, line := range strings.Split(readFile("proc/self/mounts"), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || !realFS[f[2]] {
			continue
		}
		mp := unescapeMount(f[1])
		if seen[mp] || strings.HasPrefix(mp, "/snap/") {
			continue
		}
		seen[mp] = true
		m := agentapi.HostMount{Mountpoint: mp, Device: f[0], FSType: f[2]}
		var st unix.Statfs_t
		if unix.Statfs(mp, &st) == nil {
			m.Total = st.Blocks * uint64(st.Bsize)
			m.Free = st.Bavail * uint64(st.Bsize)
		}
		out = append(out, m)
	}
	if out == nil {
		out = []agentapi.HostMount{}
	}
	return out
}

func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b bytes.Buffer
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
