package netinfo

import (
	"net/netip"
	"reflect"
	"testing"
)

type fakeSource []Iface

func (f fakeSource) Interfaces() ([]Iface, error) { return f, nil }

func pfx(s string) netip.Prefix { return netip.MustParsePrefix(s) }

func TestLANIPv4(t *testing.T) {
	src := fakeSource{
		{Name: "lo", Up: true, Loop: true, Addrs: []netip.Prefix{pfx("127.0.0.1/8")}},
		{Name: "wlan0", Up: true, Addrs: []netip.Prefix{pfx("10.0.0.5/24")}},
		{Name: "eth0", Up: true, Addrs: []netip.Prefix{pfx("192.168.1.10/24"), pfx("fd00::1/64"), pfx("169.254.3.4/16")}},
		{Name: "podman0", Up: true, Addrs: []netip.Prefix{pfx("10.88.0.1/16")}},
		{Name: "veth1234", Up: true, Addrs: []netip.Prefix{pfx("10.88.0.9/16")}},
		{Name: "tailscale0", Up: true, Addrs: []netip.Prefix{pfx("100.100.1.1/32")}},
		{Name: "docker0", Up: true, Addrs: []netip.Prefix{pfx("172.17.0.1/16")}},
		{Name: "eth1", Up: false, Addrs: []netip.Prefix{pfx("192.168.9.9/24")}},
	}
	got, err := LANIPv4(src)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"192.168.1.10", "10.0.0.5"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestStaticKeepsOrder(t *testing.T) {
	got, err := LANIPv4(Static{"192.168.1.10", "10.0.0.5"})
	if err != nil || !reflect.DeepEqual(got, []string{"192.168.1.10", "10.0.0.5"}) {
		t.Fatalf("got %v %v", got, err)
	}
	if MDNSName("care") != "care.local" {
		t.Fatal("MDNSName")
	}
}
