package service

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
)

// InterfaceAddress is one address of an interface (schema InterfaceAddress).
type InterfaceAddress struct {
	Address      string `json:"address"`
	PrefixLength int    `json:"prefix_length"`
	Family       string `json:"family"`
	Dynamic      bool   `json:"dynamic"`
}

// NetworkInterface is one interface (schema NetworkInterface).
type NetworkInterface struct {
	Name      string             `json:"name"`
	Kind      string             `json:"kind"`
	MAC       *string            `json:"mac"`
	Link      bool               `json:"link"`
	SpeedMbps *int               `json:"speed_mbps"`
	Addresses []InterfaceAddress `json:"addresses"`
}

// DHCPHint is the address to reserve in the router (schema DhcpHint).
type DHCPHint struct {
	Interface string `json:"interface"`
	MAC       string `json:"mac"`
	Address   string `json:"address"`
}

// Network is the network state (schema Network).
type Network struct {
	Hostname   string `json:"hostname"`
	OSHostname string `json:"os_hostname"`
	MDNS       struct {
		Configured string  `json:"configured"`
		Advertised *string `json:"advertised"`
		Collision  bool    `json:"collision"`
	} `json:"mdns"`
	Addresses      []string           `json:"addresses"`
	Published      []string           `json:"published"`
	AddressChanged bool               `json:"address_changed"`
	Interfaces     []NetworkInterface `json:"interfaces"`
	Gateway        *string            `json:"gateway"`
	DNSServers     []string           `json:"dns_servers"`
	Internet       struct {
		Reachable *bool      `json:"reachable"`
		CheckedAt *time.Time `json:"checked_at"`
	} `json:"internet"`
	DHCPHint *DHCPHint `json:"dhcp_hint"`
	URLs     []string  `json:"urls"`
	WiFi     any       `json:"wifi"`
}

type ipAddr struct {
	IfName    string   `json:"ifname"`
	Flags     []string `json:"flags"`
	Address   string   `json:"address"`
	LinkType  string   `json:"link_type"`
	OperState string   `json:"operstate"`
	AddrInfo  []struct {
		Family    string `json:"family"`
		Local     string `json:"local"`
		PrefixLen int    `json:"prefixlen"`
		Dynamic   bool   `json:"dynamic"`
		Scope     string `json:"scope"`
	} `json:"addr_info"`
}

func (s *Service) interfaces(ctx context.Context) []NetworkInterface {
	out := []NetworkInterface{}
	raw, err := s.Host.Runner.Run(ctx, hostCmd("ip", "-json", "address", "show"))
	var list []ipAddr
	if err == nil && json.Unmarshal(raw, &list) == nil {
		for _, i := range list {
			if netinfo.Excluded(i.IfName) {
				continue
			}
			ni := NetworkInterface{Name: i.IfName, Kind: ifaceKind(i.IfName), Addresses: []InterfaceAddress{}}
			if i.LinkType == "ether" && i.Address != "" {
				mac := strings.ToLower(i.Address)
				ni.MAC = &mac
			}
			for _, f := range i.Flags {
				ni.Link = ni.Link || f == "LOWER_UP"
			}
			if v, err := strconv.Atoi(readTrim(s.Paths.Abs("/sys/class/net/" + i.IfName + "/speed"))); err == nil && v > 0 {
				ni.SpeedMbps = &v
			}
			for _, a := range i.AddrInfo {
				fam := "ipv4"
				if a.Family == "inet6" {
					fam = "ipv6"
				}
				ni.Addresses = append(ni.Addresses, InterfaceAddress{Address: a.Local, PrefixLength: a.PrefixLen, Family: fam, Dynamic: a.Dynamic})
			}
			out = append(out, ni)
		}
		return out
	}
	ifs, err := s.IPs.Interfaces()
	if err != nil {
		return out
	}
	for _, i := range ifs {
		if netinfo.Excluded(i.Name) || i.Loop {
			continue
		}
		ni := NetworkInterface{Name: i.Name, Kind: ifaceKind(i.Name), Link: i.Up, Addresses: []InterfaceAddress{}}
		for _, p := range i.Addrs {
			fam := "ipv4"
			if !p.Addr().Is4() {
				fam = "ipv6"
			}
			ni.Addresses = append(ni.Addresses, InterfaceAddress{Address: p.Addr().String(), PrefixLength: p.Bits(), Family: fam})
		}
		out = append(out, ni)
	}
	return out
}

func (s *Service) gateway(ctx context.Context) *string {
	raw, err := s.Host.Runner.Run(ctx, hostCmd("ip", "-json", "route", "show", "default"))
	if err != nil {
		return nil
	}
	var routes []struct {
		Gateway string `json:"gateway"`
	}
	if json.Unmarshal(raw, &routes) != nil {
		return nil
	}
	for _, r := range routes {
		if a, err := netip.ParseAddr(r.Gateway); err == nil && a.Is4() {
			g := r.Gateway
			return &g
		}
	}
	return nil
}

func (s *Service) dnsServers() []string {
	out := []string{}
	data, err := os.ReadFile(s.Paths.Abs("/etc/resolv.conf"))
	if err != nil {
		return out
	}
	for _, l := range strings.Split(string(data), "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && f[0] == "nameserver" {
			out = append(out, f[1])
		}
	}
	return out
}

// Network returns the network state.
func (s *Service) Network(ctx context.Context) *Network {
	cfg, err := config.Load(s.configPath())
	if err != nil {
		cfg = config.Default()
	}
	n := &Network{Hostname: cfg.Identity.Hostname, Addresses: s.Addresses(), Published: s.Published(),
		Interfaces: s.interfaces(ctx), Gateway: s.gateway(ctx), DNSServers: s.dnsServers(), URLs: s.CareURLs()}
	if h, err := os.Hostname(); err == nil {
		n.OSHostname = strings.SplitN(h, ".", 2)[0]
	}
	if s.Paths.Root != "/" && s.Paths.Root != "" {
		if h := readTrim(s.Paths.Abs("/etc/hostname")); h != "" {
			n.OSHostname = h
		}
	}
	n.MDNS.Configured = netinfo.MDNSName(cfg.Identity.Hostname)
	n.MDNS.Advertised = s.mdnsAdvertised(ctx)
	n.MDNS.Collision = n.MDNS.Advertised != nil && *n.MDNS.Advertised != n.MDNS.Configured
	n.AddressChanged = strings.Join(n.Addresses, " ") != strings.Join(n.Published, " ")
	if s.Internet != nil {
		n.Internet.Reachable, n.Internet.CheckedAt = s.Internet.State()
	}
	for _, i := range n.Interfaces {
		if i.Kind != "ethernet" || i.MAC == nil {
			continue
		}
		for _, a := range i.Addresses {
			if a.Family == "ipv4" && a.Dynamic {
				n.DHCPHint = &DHCPHint{Interface: i.Name, MAC: *i.MAC, Address: a.Address}
				break
			}
		}
		if n.DHCPHint != nil {
			break
		}
	}
	return n
}
