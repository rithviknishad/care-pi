package netinfo

import (
	"context"
	"net/netip"
	"reflect"
	"testing"
	"time"
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

// ticks gives no LAN address for the first n reads, then eth0 192.168.1.10.
type ticks struct {
	n, reads int
}

func (t *ticks) Interfaces() ([]Iface, error) {
	t.reads++
	ifs := []Iface{
		{Name: "lo", Up: true, Loop: true, Addrs: []netip.Prefix{pfx("127.0.0.1/8")}},
		{Name: "podman0", Up: true, Addrs: []netip.Prefix{pfx("10.88.0.1/16")}},
	}
	eth := Iface{Name: "eth0", Up: true, Addrs: []netip.Prefix{pfx("fe80::1/64"), pfx("fd00::5/64")}}
	if t.reads > t.n {
		eth.Addrs = append(eth.Addrs, pfx("192.168.1.10/24"))
	}
	return append(ifs, eth), nil
}

func TestWaitLANIPv4AddressAfterTwoTicks(t *testing.T) {
	src := &ticks{n: 2}
	said := 0
	got, err := WaitLANIPv4(context.Background(), src, time.Minute, time.Millisecond, func() { said++ })
	if err != nil || !reflect.DeepEqual(got, []string{"192.168.1.10"}) {
		t.Fatalf("got %v %v", got, err)
	}
	if src.reads != 3 {
		t.Fatalf("reads = %d, want 3 (2 without the address)", src.reads)
	}
	if said != 1 {
		t.Fatalf("waiting called %d times, want 1", said)
	}
}

func TestWaitLANIPv4AddressAtOnce(t *testing.T) {
	said := 0
	got, err := WaitLANIPv4(context.Background(), &ticks{}, time.Minute, time.Millisecond, func() { said++ })
	if err != nil || len(got) != 1 || said != 0 {
		t.Fatalf("got %v %v, waiting called %d times", got, err, said)
	}
}

func TestWaitLANIPv4Timeout(t *testing.T) {
	src := &ticks{n: 1 << 30}
	said := 0
	start := time.Now()
	got, err := WaitLANIPv4(context.Background(), src, 30*time.Millisecond, 5*time.Millisecond, func() { said++ })
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v %v, want no address", got, err)
	}
	if d := time.Since(start); d < 30*time.Millisecond {
		t.Fatalf("returned after %s, before the timeout", d)
	}
	if said != 1 || src.reads < 2 {
		t.Fatalf("waiting called %d times, %d reads", said, src.reads)
	}
}
