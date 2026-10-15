package virt

import (
	"strings"
	"sync"
	"time"

	libvirt "github.com/digitalocean/go-libvirt"

	"github.com/vaktikos/host-managed/internal/agentapi"
)

// historyLen points are kept per VM (at the default 5 s interval: 10 minutes).
const historyLen = 120

type rawSample struct {
	t       time.Time
	cpuNs   uint64
	vcpus   uint64
	rx, tx  uint64
	rd, wr  uint64
	balloon uint64 // KiB currently assigned
	unused  *uint64
	state   int32
	memMax  uint64
}

type vmSeries struct {
	last    rawSample
	history []agentapi.VMStats
}

// collector samples all running domains with one bulk RPC per interval and
// derives rates from consecutive samples.
type collector struct {
	m      *Manager
	mu     sync.Mutex
	series map[string]*vmSeries // keyed by UUID
	quit   chan struct{}
	once   sync.Once
}

func newCollector(m *Manager) *collector {
	return &collector{m: m, series: map[string]*vmSeries{}, quit: make(chan struct{})}
}

func (c *collector) start(interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		c.sample()
		for {
			select {
			case <-c.quit:
				return
			case <-t.C:
				c.sample()
			}
		}
	}()
}

func (c *collector) stop() { c.once.Do(func() { close(c.quit) }) }

func paramU(p libvirt.TypedParam) (uint64, bool) {
	switch v := p.Value.I.(type) {
	case uint64:
		return v, true
	case int64:
		return uint64(v), true
	case uint32:
		return uint64(v), true
	case int32:
		return uint64(v), true
	}
	return 0, false
}

func (c *collector) sample() {
	l, err := c.m.conn()
	if err != nil {
		return
	}
	types := uint32(libvirt.DomainStatsState | libvirt.DomainStatsCPUTotal | libvirt.DomainStatsBalloon |
		libvirt.DomainStatsVCPU | libvirt.DomainStatsInterface | libvirt.DomainStatsBlock)
	recs, err := l.ConnectGetAllDomainStats(nil, types, uint32(libvirt.ConnectGetAllDomainsStatsActive))
	if err != nil {
		return
	}
	now := time.Now()
	seen := map[string]bool{}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range recs {
		id := uuidString(r.Dom.UUID)
		seen[id] = true
		s := rawSample{t: now}
		for _, p := range r.Params {
			v, ok := paramU(p)
			if !ok {
				continue
			}
			switch {
			case p.Field == "state.state":
				s.state = int32(v)
			case p.Field == "cpu.time":
				s.cpuNs = v
			case p.Field == "vcpu.current":
				s.vcpus = v
			case p.Field == "balloon.current":
				s.balloon = v
			case p.Field == "balloon.maximum":
				s.memMax = v
			case p.Field == "balloon.unused":
				u := v
				s.unused = &u
			case strings.HasPrefix(p.Field, "net.") && strings.HasSuffix(p.Field, ".rx.bytes"):
				s.rx += v
			case strings.HasPrefix(p.Field, "net.") && strings.HasSuffix(p.Field, ".tx.bytes"):
				s.tx += v
			case strings.HasPrefix(p.Field, "block.") && strings.HasSuffix(p.Field, ".rd.bytes"):
				s.rd += v
			case strings.HasPrefix(p.Field, "block.") && strings.HasSuffix(p.Field, ".wr.bytes"):
				s.wr += v
			}
		}
		ser := c.series[id]
		if ser == nil {
			ser = &vmSeries{}
			c.series[id] = ser
		}
		point := agentapi.VMStats{State: stateName(s.state), MemoryMiB: s.balloon / 1024, Time: now}
		if s.unused != nil && s.balloon >= *s.unused {
			used := s.balloon - *s.unused
			point.MemoryUsedKiB = &used
		}
		if p := ser.last; !p.t.IsZero() {
			dt := now.Sub(p.t).Seconds()
			if dt > 0 {
				if s.vcpus > 0 && s.cpuNs >= p.cpuNs {
					v := float64(s.cpuNs-p.cpuNs) / (dt * 1e9 * float64(s.vcpus)) * 100
					if v > 100 {
						v = 100
					}
					point.CPUUsage = &v
				}
				point.NetRxBps = rate(s.rx, p.rx, dt)
				point.NetTxBps = rate(s.tx, p.tx, dt)
				point.DiskReadBps = rate(s.rd, p.rd, dt)
				point.DiskWriteBps = rate(s.wr, p.wr, dt)
			}
		}
		ser.last = s
		ser.history = append(ser.history, point)
		if len(ser.history) > historyLen {
			ser.history = ser.history[len(ser.history)-historyLen:]
		}
	}
	for id := range c.series {
		if !seen[id] {
			delete(c.series, id)
		}
	}
}

func rate(cur, prev uint64, dt float64) *float64 {
	if cur < prev {
		return nil // counter reset (device hot-unplugged or VM restarted)
	}
	v := float64(cur-prev) / dt
	return &v
}

func uuidString(u libvirt.UUID) string {
	const hex = "0123456789abcdef"
	b := make([]byte, 0, 36)
	for i, x := range u {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			b = append(b, '-')
		}
		b = append(b, hex[x>>4], hex[x&0xf])
	}
	return string(b)
}

// VMStats returns the history of samples for a VM (oldest first). An empty
// slice means the VM is not running or has not been sampled yet.
func (m *Manager) VMStats(name string) ([]agentapi.VMStats, error) {
	_, d, err := m.lookup(name)
	if err != nil {
		return nil, err
	}
	id := uuidString(d.UUID)
	m.stats.mu.Lock()
	defer m.stats.mu.Unlock()
	ser := m.stats.series[id]
	if ser == nil {
		return []agentapi.VMStats{}, nil
	}
	out := make([]agentapi.VMStats, len(ser.history))
	copy(out, ser.history)
	return out, nil
}

// LatestStats returns the newest sample of every running VM keyed by name.
func (m *Manager) LatestStats() map[string]agentapi.VMStats {
	l, err := m.conn()
	if err != nil {
		return nil
	}
	doms, _, err := l.ConnectListAllDomains(1, libvirt.ConnectListDomainsActive)
	if err != nil {
		return nil
	}
	m.stats.mu.Lock()
	defer m.stats.mu.Unlock()
	out := map[string]agentapi.VMStats{}
	for _, d := range doms {
		if ser := m.stats.series[uuidString(d.UUID)]; ser != nil && len(ser.history) > 0 {
			out[d.Name] = ser.history[len(ser.history)-1]
		}
	}
	return out
}
