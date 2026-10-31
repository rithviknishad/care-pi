// Package netinfo finds the LAN IPv4 addresses and the mDNS names of the host.
//
// The agent never resolves its own .local name: on the host, nss-mdns can answer
// with the podman bridge address (SP5 finding 6). It reads the interfaces instead.
package netinfo

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"time"
)

// Iface is one network interface with its addresses.
type Iface struct {
	Name  string
	Up    bool
	Loop  bool
	Addrs []netip.Prefix
}

// Source lists the interfaces of the host.
type Source interface {
	Interfaces() ([]Iface, error)
}

// System reads the interfaces of this host.
type System struct{}

// Interfaces implements Source.
func (System) Interfaces() ([]Iface, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]Iface, 0, len(ifs))
	for _, i := range ifs {
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		it := Iface{Name: i.Name, Up: i.Flags&net.FlagUp != 0, Loop: i.Flags&net.FlagLoopback != 0}
		for _, a := range addrs {
			if p, err := netip.ParsePrefix(a.String()); err == nil {
				it.Addrs = append(it.Addrs, p)
			}
		}
		out = append(out, it)
	}
	return out, nil
}

// Static returns a fixed list of addresses in the given order. Tests and "render --ip" use it.
type Static []string

// Interfaces implements Source. Each address gets its own interface, so the order stays.
func (s Static) Interfaces() ([]Iface, error) {
	out := make([]Iface, 0, len(s))
	for i, a := range s {
		addr, err := netip.ParseAddr(a)
		if err != nil {
			return nil, err
		}
		out = append(out, Iface{
			Name:  fmt.Sprintf("eth%03d", i),
			Up:    true,
			Addrs: []netip.Prefix{netip.PrefixFrom(addr, 32)},
		})
	}
	return out, nil
}

var excludedPrefixes = []string{"lo", "podman", "veth", "tailscale", "docker", "cni", "br-", "virbr", "wg", "tun", "utun", "zt"}

// Excluded reports whether an interface name is never a LAN interface.
func Excluded(name string) bool {
	for _, p := range excludedPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func rank(name string) int {
	switch {
	case strings.HasPrefix(name, "eth"), strings.HasPrefix(name, "en"):
		return 0
	case strings.HasPrefix(name, "wl"):
		return 1
	default:
		return 2
	}
}

// LANIPv4 returns the IPv4 addresses of the up, non-excluded interfaces.
// Wired interfaces come first, then Wi-Fi, then the rest. The first address is the primary one.
func LANIPv4(src Source) ([]string, error) {
	ifs, err := src.Interfaces()
	if err != nil {
		return nil, err
	}
	type cand struct {
		rank int
		name string
		addr netip.Addr
	}
	var cs []cand
	seen := map[netip.Addr]bool{}
	for _, i := range ifs {
		if !i.Up || i.Loop || Excluded(i.Name) {
			continue
		}
		for _, p := range i.Addrs {
			a := p.Addr()
			if !a.Is4() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsUnspecified() || a.IsMulticast() {
				continue
			}
			if seen[a] {
				continue
			}
			seen[a] = true
			cs = append(cs, cand{rank(i.Name), i.Name, a})
		}
	}
	sort.SliceStable(cs, func(x, y int) bool {
		if cs[x].rank != cs[y].rank {
			return cs[x].rank < cs[y].rank
		}
		if cs[x].name != cs[y].name {
			return cs[x].name < cs[y].name
		}
		return cs[x].addr.Less(cs[y].addr)
	})
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.addr.String())
	}
	return out, nil
}

// WaitLANIPv4 reads src until it gives at least one LAN IPv4 address (LANIPv4), or until
// timeout ends. It reads again each poll. Before the first wait it calls waiting once.
// At the boot, NetworkManager can send an event before the DHCP lease (lumine, wave 4d), so
// "care-pi apply" waits for an address before it renders the pod and the Caddyfile.
// It returns the addresses of the last read: none after a timeout or a stop of ctx.
func WaitLANIPv4(ctx context.Context, src Source, timeout, poll time.Duration, waiting func()) ([]string, error) {
	deadline := time.Now().Add(timeout)
	said := false
	for {
		ips, err := LANIPv4(src)
		if err != nil || len(ips) > 0 || !time.Now().Before(deadline) {
			return ips, err
		}
		if !said && waiting != nil {
			waiting()
			said = true
		}
		wait := min(poll, time.Until(deadline))
		select {
		case <-ctx.Done():
			return ips, nil
		case <-time.After(wait):
		}
	}
}

// MDNSName returns the mDNS host name for a hostname label.
func MDNSName(hostname string) string {
	return hostname + ".local"
}
