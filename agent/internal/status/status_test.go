package status

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

func TestCollect(t *testing.T) {
	root := t.TempDir()
	p := paths.New(root)
	pod := p.Abs(paths.QuadletDir + "/care.pod")
	_ = os.MkdirAll(filepath.Dir(pod), 0o755)
	_ = os.WriteFile(pod, []byte("[Pod]\nPublishPort=192.168.1.10:80:80\nPublishPort=192.168.1.10:443:443\nPublishPort=127.0.0.1:19000:9000\n"), 0o644)

	units := Units()
	var b strings.Builder
	for _, u := range units {
		state := "active"
		if u == "care-worker.service" {
			state = "failed"
		}
		fmt.Fprintf(&b, "Id=%s\nActiveState=%s\nSubState=running\nMemoryCurrent=104857600\nMemoryPeak=[not set]\nMemoryMax=infinity\nNRestarts=1\n\n", u, state)
	}
	f := host.NewFake()
	f.On("systemctl show -p Id -p ActiveState -p SubState -p MemoryCurrent -p MemoryPeak -p MemoryMax -p NRestarts "+strings.Join(units, " "),
		host.Response{Out: b.String()})
	f.On("podman ps --all --filter pod=care --format {{.Names}}\t{{.Status}}",
		host.Response{Out: "care-api\tUp 1 minute (healthy)\ncare-db\tUp 2 minutes (unhealthy)\n"})
	r, err := Collect(context.Background(), host.New(f), p, []string{"care.local"}, netinfo.Static{"192.168.1.10", "10.0.0.5"})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Degraded {
		t.Fatal("want degraded")
	}
	var out strings.Builder
	r.WriteText(&out)
	s := out.String()
	for _, want := range []string{"state: degraded", "care.local", "addresses published: 192.168.1.10", "run \"care-pi apply\"", "care-worker.service is failed", "care-db.service is unhealthy", "100M"} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
}

// TestAddressChanged: the Tailscale mode publishes on the address of tailscale0 too, and LANIPv4 never gives it
// (wave 8-ts on lumine: the dashboard showed "The IP address changed." while the mode was on).
func TestAddressChanged(t *testing.T) {
	lan := []string{"192.168.165.91"}
	cases := []struct {
		name      string
		addresses []string
		published []string
		ts        string
		want      bool
	}{
		{"same LAN addresses", lan, lan, "", false},
		{"the Tailscale mode", lan, []string{"192.168.165.91", "100.67.15.72"}, "100.67.15.72", false},
		{"a new LAN address with the Tailscale mode", []string{"192.168.165.92"}, []string{"192.168.165.91", "100.67.15.72"}, "100.67.15.72", true},
		{"a new Tailscale address", lan, []string{"192.168.165.91", "100.67.15.72"}, "100.67.15.99", true},
		{"tailscale0 has no address", lan, []string{"192.168.165.91", "100.67.15.72"}, "", true},
		{"nothing published", lan, nil, "", true},
	}
	for _, c := range cases {
		if got := AddressChanged(c.addresses, c.published, c.ts); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}

	// Collect reads the address of tailscale0, and the text has no note.
	root := t.TempDir()
	p := paths.New(root)
	pod := p.Abs(paths.QuadletDir + "/care.pod")
	_ = os.MkdirAll(filepath.Dir(pod), 0o755)
	_ = os.WriteFile(pod, []byte("[Pod]\nPublishPort=192.168.165.91:443:443\nPublishPort=100.67.15.72:443:443\n"), 0o644)
	f := host.NewFake()
	f.On("systemctl show -p Id -p ActiveState -p SubState -p MemoryCurrent -p MemoryPeak -p MemoryMax -p NRestarts "+strings.Join(Units(), " "),
		host.Response{Out: ""})
	f.On("podman ps --all --filter pod=care --format {{.Names}}\t{{.Status}}", host.Response{Out: ""})
	src := tsSource{lan: "192.168.165.91", ts: "100.67.15.72"}
	r, err := Collect(context.Background(), host.New(f), p, []string{"lumine.local", "lumine.orthrus-bass.ts.net"}, src)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	r.WriteText(&out)
	if r.TailscaleIP != "100.67.15.72" || strings.Contains(out.String(), "the addresses changed") {
		t.Fatalf("tailscale %q, output:\n%s", r.TailscaleIP, out.String())
	}
}

type tsSource struct{ lan, ts string }

func (s tsSource) Interfaces() ([]netinfo.Iface, error) {
	return []netinfo.Iface{
		{Name: "eth0", Up: true, Addrs: []netip.Prefix{netip.PrefixFrom(netip.MustParseAddr(s.lan), 24)}},
		{Name: "tailscale0", Up: true, Addrs: []netip.Prefix{netip.PrefixFrom(netip.MustParseAddr(s.ts), 32)}},
	}, nil
}
