package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/auth"
)

// The pod bridge: tailscaled masquerades each forwarded packet of the tailnet to it (S61).
const bridge = "10.88.0.1"

var (
	viaTailscale = header(ListenerHeader, ListenerTailscale)
	viaLocal     = header(ListenerHeader, "local")
)

// TestTailscaleSharedAddress: on the Tailscale site, the clients of the tailnet have one address. Failed passwords do
// not lock it; the account lock stays. The same address on a LAN site keeps the address lock.
func TestTailscaleSharedAddress(t *testing.T) {
	e := newEnv(t)
	e.claim()
	e.cookie, e.csrf = "", ""
	e.addr = bridge
	// The address lock comes after 5 failures. The tenth failure in a row locks the account.
	for i := range 10 {
		r := e.do("POST", "/api/v1/session/login", map[string]any{"password": "wrong password"}, viaTailscale)
		if r.status != 401 || r.slug() != "bad-credentials" {
			t.Fatalf("failure %d: %d %s", i+1, r.status, r.body)
		}
		e.clock.add(5 * time.Second)
	}
	r := e.do("POST", "/api/v1/session/login", map[string]any{"password": password}, viaTailscale)
	e.want(r, 429, "locked-out")
	if !strings.Contains(string(r.body), "for the account") {
		t.Fatalf("want the account lock, got %s", r.body)
	}
	e.clock.add(time.Minute)
	r = e.do("POST", "/api/v1/session/login", map[string]any{"password": password}, viaTailscale)
	e.want(r, 200, "")
	e.csrf = r.json(t)["csrf_token"].(string)
	// care-pi auth sessions and the console show the shared address.
	list := e.do("GET", "/api/v1/security/sessions", nil, viaTailscale)
	e.want(list, 200, "")
	if !strings.Contains(string(list.body), `"address":"`+SharedTailscaleAddr+`"`) {
		t.Fatalf("sessions: %s", list.body)
	}

	// A LAN site with the same address: 5 failures lock the address.
	e.cookie, e.csrf = "", ""
	e.clock.add(time.Minute)
	for i := range 5 {
		e.want(e.do("POST", "/api/v1/session/login", map[string]any{"password": "wrong password"}, viaLocal), 401, "bad-credentials")
		_ = i
	}
	r = e.do("POST", "/api/v1/session/login", map[string]any{"password": password}, viaLocal)
	e.want(r, 429, "locked-out")
	if !strings.Contains(string(r.body), "from this address") {
		t.Fatalf("want the address lock, got %s", r.body)
	}
	// Without the header (not from Caddy), the request is a LAN request too.
	e.want(e.do("POST", "/api/v1/session/login", map[string]any{"password": password}), 429, "locked-out")
	// The Tailscale site does not share the lock of the bridge address.
	e.want(e.do("POST", "/api/v1/session/login", map[string]any{"password": password}, viaTailscale), 200, "")
}

// TestTailscaleTailnetAddresses: a tailscaled without the masquerade gives the tailnet address of each client. Each
// address then has its own lock.
func TestTailscaleTailnetAddresses(t *testing.T) {
	e := newEnv(t)
	e.claim()
	e.cookie, e.csrf = "", ""
	e.addr = "100.101.1.2"
	for range 5 {
		e.want(e.do("POST", "/api/v1/session/login", map[string]any{"password": "wrong password"}, viaTailscale), 401, "bad-credentials")
	}
	e.want(e.do("POST", "/api/v1/session/login", map[string]any{"password": password}, viaTailscale), 429, "locked-out")
	e.addr = "100.101.1.3"
	e.want(e.do("POST", "/api/v1/session/login", map[string]any{"password": password}, viaTailscale), 200, "")
}

// TestTailscalePublicReads: the shared address has 600 public reads each minute; a LAN address has 60.
func TestTailscalePublicReads(t *testing.T) {
	e := newEnv(t)
	e.addr = bridge
	for i := range auth.PublicPerMinute {
		if r := e.do("GET", "/api/v1/health", nil, viaLocal); r.status != 200 {
			t.Fatalf("LAN read %d: %d", i, r.status)
		}
	}
	e.want(e.do("GET", "/api/v1/health", nil, viaLocal), 429, "rate-limited")
	for i := range auth.PublicPerMinute + 1 {
		if r := e.do("GET", "/api/v1/health", nil, viaTailscale); r.status != 200 {
			t.Fatalf("Tailscale read %d: %d", i, r.status)
		}
	}
}

// TestClientAddress: the agent trusts the listener header only from Caddy (not on the dev listener).
func TestClientAddress(t *testing.T) {
	for _, c := range []struct {
		dev           bool
		xff, listener string
		wantAddr      string
		wantShared    bool
	}{
		{false, bridge, ListenerTailscale, SharedTailscaleAddr, true},
		{false, bridge, "local", bridge, false},
		{false, bridge, "public", bridge, false},
		{false, bridge, "", bridge, false},
		{false, "100.101.1.2", ListenerTailscale, "100.101.1.2", false},
		{false, "fd7a:115c:a1e0::1", ListenerTailscale, "fd7a:115c:a1e0::1", false},
		{false, "192.168.1.23", "local", "192.168.1.23", false},
		{true, bridge, ListenerTailscale, "127.0.0.1", false},
	} {
		s := &Server{o: Options{Dev: c.dev}}
		r, _ := http.NewRequest("GET", "/api/v1/health", nil)
		r.RemoteAddr = "127.0.0.1:5000"
		r.Header.Set("X-Forwarded-For", c.xff)
		if c.listener != "" {
			r.Header.Set(ListenerHeader, c.listener)
		}
		got := s.client(r)
		if got.Addr != c.wantAddr || got.Shared != c.wantShared {
			t.Errorf("dev=%v xff=%s listener=%q: %+v, want %s shared=%v", c.dev, c.xff, c.listener, got, c.wantAddr, c.wantShared)
		}
	}
}
