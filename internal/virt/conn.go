// Package virt is the KVM/QEMU adapter. It talks to libvirtd over its local
// RPC socket (no cgo) and is only used inside the node agent.
package virt

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	libvirt "github.com/digitalocean/go-libvirt"
	"github.com/digitalocean/go-libvirt/socket/dialers"
)

// ErrNotFound is returned when a domain, pool or volume does not exist.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned when an operation is not valid in the current state.
var ErrConflict = errors.New("conflict")

// Manager owns the libvirt connection and reconnects on demand.
type Manager struct {
	socket  string
	allowTCG bool

	mu sync.Mutex
	l  *libvirt.Libvirt

	stats *collector
}

type Options struct {
	Socket   string // default /var/run/libvirt/libvirt-sock
	AllowTCG bool   // permit VMs without /dev/kvm (software emulation, slow)
}

func New(o Options) *Manager {
	if o.Socket == "" {
		o.Socket = "/var/run/libvirt/libvirt-sock"
	}
	m := &Manager{socket: o.Socket, allowTCG: o.AllowTCG}
	m.stats = newCollector(m)
	return m
}

// conn returns a live connection, reconnecting if the old one dropped
// (libvirtd restarts are routine during package upgrades).
func (m *Manager) conn() (*libvirt.Libvirt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.l != nil && m.l.IsConnected() {
		return m.l, nil
	}
	l := libvirt.NewWithDialer(dialers.NewLocal(dialers.WithSocket(m.socket), dialers.WithLocalTimeout(5*time.Second)))
	if err := l.ConnectToURI(libvirt.QEMUSystem); err != nil {
		return nil, fmt.Errorf("connect to libvirtd at %s: %w", m.socket, err)
	}
	m.l = l
	return l, nil
}

func (m *Manager) Close() {
	m.stats.stop()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.l != nil {
		_ = m.l.Disconnect()
		m.l = nil
	}
}

// StartCollector begins periodic sampling of running domains.
func (m *Manager) StartCollector(interval time.Duration) { m.stats.start(interval) }

func KVMAvailable() bool {
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// Versions returns libvirt and hypervisor versions as dotted strings.
func (m *Manager) Versions() (libvirtVer, qemuVer string) {
	l, err := m.conn()
	if err != nil {
		return "", ""
	}
	if v, err := l.ConnectGetLibVersion(); err == nil {
		libvirtVer = dotted(v)
	}
	if v, err := l.ConnectGetVersion(); err == nil {
		qemuVer = dotted(v)
	}
	return
}

func dotted(v uint64) string {
	return fmt.Sprintf("%d.%d.%d", v/1000000, (v/1000)%1000, v%1000)
}

func isNotFound(err error) bool {
	var le libvirt.Error
	if errors.As(err, &le) {
		switch le.Code {
		case uint32(libvirt.ErrNoDomain), uint32(libvirt.ErrNoStoragePool), uint32(libvirt.ErrNoStorageVol), uint32(libvirt.ErrNoNetwork):
			return true
		}
	}
	return false
}

func wrap(err error) error {
	if err == nil {
		return nil
	}
	if isNotFound(err) {
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	var le libvirt.Error
	if errors.As(err, &le) {
		switch le.Code {
		case uint32(libvirt.ErrOperationInvalid), uint32(libvirt.ErrDomExist), uint32(libvirt.ErrStorageVolExist):
			return fmt.Errorf("%w: %s", ErrConflict, le.Message)
		}
	}
	return err
}
