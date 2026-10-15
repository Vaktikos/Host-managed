package api

import (
	"bufio"
	"context"
	"io"
	"net"

	"github.com/vaktikos/host-managed/internal/agentapi"
	"github.com/vaktikos/host-managed/internal/agentclient"
)

// Agent is the subset of the node agent used by the API. agentclient.Client
// implements it; tests use a fake.
type Agent interface {
	Host(ctx context.Context) (*agentapi.Host, error)
	Interfaces(ctx context.Context) ([]agentapi.Interface, error)
	Networks(ctx context.Context) ([]agentapi.VirtNetwork, error)
	Pools(ctx context.Context) ([]agentapi.StoragePool, error)
	Volumes(ctx context.Context, pool string) ([]agentapi.StorageVolume, error)
	DeleteVolume(ctx context.Context, pool, vol string) error
	Upload(ctx context.Context, pool, name string, size int64, body io.Reader) error

	VMs(ctx context.Context) ([]agentapi.VM, error)
	VM(ctx context.Context, name string) (*agentapi.VMDetail, error)
	CreateVM(ctx context.Context, spec agentapi.VMCreate, log func(string)) (*agentapi.VMDetail, error)
	VMAction(ctx context.Context, name, action string) error
	VMMeta(ctx context.Context, name string, m agentapi.VMMeta) error
	VMResources(ctx context.Context, name string, r agentapi.VMResources) (bool, error)
	VMTemplate(ctx context.Context, name string, template bool) error
	DeleteVM(ctx context.Context, name string, disks bool) ([]string, string, error)
	VMStats(ctx context.Context, name string) ([]agentapi.VMStats, error)
	LatestStats(ctx context.Context) (map[string]agentapi.VMStats, error)
	VMGuest(ctx context.Context, name string) (*agentapi.GuestInfo, error)
	VNC(ctx context.Context, name string) (net.Conn, *bufio.Reader, error)

	Containers(ctx context.Context) ([]agentapi.Container, error)
	Container(ctx context.Context, name string) (*agentapi.Container, error)
	CreateContainer(ctx context.Context, spec agentapi.ContainerCreate, log func(string)) (*agentapi.Container, error)
	ContainerAction(ctx context.Context, name, action string) error
	ContainerConfig(ctx context.Context, name string, cfg agentclient.ContainerConfig) error
	DeleteContainer(ctx context.Context, name string) error
	ContainerConsole(ctx context.Context, name string) (net.Conn, *bufio.Reader, error)
	LXCImages(ctx context.Context, arch string) ([]agentclient.Image, error)
}

var _ Agent = (*agentclient.Client)(nil)
