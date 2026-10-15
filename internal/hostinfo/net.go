package hostinfo

import (
	"net"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/vaktikos/host-managed/internal/agentapi"
)

func sysNet(iface, file string) string {
	return strings.TrimSpace(readFile("sys/class/net/" + iface + "/" + file))
}

func sysNetU(iface, file string) uint64 {
	n, _ := strconv.ParseUint(sysNet(iface, file), 10, 64)
	return n
}

// Interfaces lists host network interfaces with type, state and counters.
func Interfaces() ([]agentapi.Interface, error) {
	ents, err := os.ReadDir(p("sys/class/net"))
	if err != nil {
		return nil, err
	}
	addrs := map[string][]string{}
	if ifs, err := net.Interfaces(); err == nil && Root == "/" {
		for _, i := range ifs {
			as, _ := i.Addrs()
			for _, a := range as {
				addrs[i.Name] = append(addrs[i.Name], a.String())
			}
		}
	}
	var out []agentapi.Interface
	for _, e := range ents {
		name := e.Name()
		if !agentapi.ValidIfName(name) {
			continue
		}
		it := agentapi.Interface{
			Name:      name,
			MAC:       sysNet(name, "address"),
			State:     sysNet(name, "operstate"),
			Addresses: addrs[name],
			RxBytes:   sysNetU(name, "statistics/rx_bytes"),
			TxBytes:   sysNetU(name, "statistics/tx_bytes"),
			RxErrors:  sysNetU(name, "statistics/rx_errors"),
			TxErrors:  sysNetU(name, "statistics/tx_errors"),
			RxDropped: sysNetU(name, "statistics/rx_dropped"),
			TxDropped: sysNetU(name, "statistics/tx_dropped"),
		}
		it.MTU, _ = strconv.Atoi(sysNet(name, "mtu"))
		if s, err := strconv.Atoi(sysNet(name, "speed")); err == nil && s > 0 {
			it.Speed = s
		}
		if it.Addresses == nil {
			it.Addresses = []string{}
		}
		if link, err := os.Readlink(p("sys/class/net/" + name + "/master")); err == nil {
			it.Master = lastElem(link)
		}
		it.Kind = kind(name)
		switch it.Kind {
		case "bridge":
			it.Members = dirNames("sys/class/net/" + name + "/brif")
		case "bond":
			it.Members = strings.Fields(sysNet(name, "bonding/slaves"))
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func kind(name string) string {
	base := "sys/class/net/" + name
	if name == "lo" {
		return "loopback"
	}
	if _, err := os.Stat(p(base + "/bridge")); err == nil {
		return "bridge"
	}
	if _, err := os.Stat(p(base + "/bonding")); err == nil {
		return "bond"
	}
	if strings.Contains(readFile(base+"/uevent"), "DEVTYPE=vlan") {
		return "vlan"
	}
	if _, err := os.Stat(p(base + "/device")); err == nil {
		return "physical"
	}
	return "virtual"
}

func dirNames(path string) []string {
	ents, err := os.ReadDir(p(path))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

func lastElem(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}
