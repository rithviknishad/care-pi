package server

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/clockgate"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

const ntpQuery = "timedatectl show --property NTPSynchronized --value"

func setClockCall(t time.Time) string {
	return "systemd-run --wait --collect --pipe --quiet --unit=" + host.SetClockUnit + " -- /bin/sh -c " +
		`set -eu; date -u -s "@$1" >/dev/null; if command -v fake-hwclock >/dev/null 2>&1; then fake-hwclock save >/dev/null 2>&1 || true; fi` +
		" sh " + strconv.FormatInt(t.Unix(), 10)
}

func clockOf(t *testing.T, m map[string]any) (string, bool) {
	t.Helper()
	c := m["clock"].(map[string]any)
	return c["source"].(string), c["trusted"].(bool)
}

// S52: the claim gives the time of the browser. Without NTP the agent sets the clock one time (date in a transient
// unit), writes the marker, and reloads Caddy. The status says "browser, not trusted" until NTP synchronizes the clock.
func TestClaimSetsTheClockFromTheBrowser(t *testing.T) {
	e := newEnv(t)
	e.svc.ClockReloadDelay = time.Millisecond
	e.fake.Set(ntpQuery, host.Response{Out: "no\n"})
	code, _, err := e.svc.Auth.EnsureClaimCode()
	must(t, err)
	browser := time.Now().UTC().Add(3 * time.Hour).Truncate(time.Millisecond)

	// The format must be RFC 3339 with an offset.
	e.want(e.do("POST", "/api/v1/session/claim", map[string]any{"method": "claim-code", "secret": code, "new_password": password,
		"client_time": "2026-11-01 10:00"}, invalid), 422, "invalid-request")
	// A wrong claim secret never sets the clock.
	e.want(e.do("POST", "/api/v1/session/claim", map[string]any{"method": "claim-code", "secret": "0000-0000-0000", "new_password": password,
		"client_time": browser.Format(time.RFC3339Nano)}), 401, "bad-credentials")
	if got := e.fake.Filter("systemd-run"); len(got) != 0 {
		t.Fatalf("a failed claim set the clock: %q", got)
	}

	r := e.do("POST", "/api/v1/session/claim", map[string]any{"method": "claim-code", "secret": code, "new_password": password,
		"client_time": browser.Format(time.RFC3339Nano)})
	e.want(r, 201, "")
	e.csrf = r.json(t)["session"].(map[string]any)["csrf_token"].(string)
	if got := e.fake.Filter("systemd-run"); len(got) != 1 || got[0] != setClockCall(browser) {
		t.Fatalf("set-time commands:\n got  %q\n want %q", got, setClockCall(browser))
	}
	m, ok := clockgate.Read(e.p)
	if !ok || m.Trusted || m.Source != clockgate.SourceBrowser || !m.SetAt.Equal(browser.Truncate(time.Second)) {
		t.Fatalf("marker %+v %v", m, ok)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(e.fake.Filter("podman exec care-caddy caddy reload")) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no reload of Caddy after the clock jump")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := e.fake.Filter("podman exec care-caddy caddy reload", "systemctl try-restart"); len(got) != 1 ||
		got[0] != "podman exec care-caddy "+strings.Join(clockgate.CaddyReload, " ") {
		t.Fatalf("Caddy: %q (a restart would cut the answer of the claim)", got)
	}

	r = e.do("GET", "/api/v1/status", nil)
	e.want(r, 200, "")
	if src, trusted := clockOf(t, r.json(t)); src != "browser" || trusted {
		t.Fatalf("status clock %s %v", src, trusted)
	}
	e.clock.add(3 * time.Second)
	r = e.do("GET", "/_care-pi/status", nil)
	if src, trusted := clockOf(t, r.json(t)); src != "browser" || trusted {
		t.Fatalf("public status clock %s %v", src, trusted)
	}

	// NTP synchronizes the clock: trusted, and the marker goes.
	e.fake.Set(ntpQuery, host.Response{Out: "yes\n"})
	e.clock.add(30 * time.Second)
	r = e.do("GET", "/api/v1/status", nil)
	if src, trusted := clockOf(t, r.json(t)); src != "ntp" || !trusted {
		t.Fatalf("status clock after NTP %s %v", src, trusted)
	}
	if _, err := os.Stat(e.p.Abs(paths.ClockTrusted)); !os.IsNotExist(err) {
		t.Fatal("NTP synchronized the clock: the marker must go")
	}
}

// S52: the agent ignores the time of the browser when NTP synchronized the clock, or when the time is not plausible.
func TestClaimKeepsTheClock(t *testing.T) {
	for name, c := range map[string]struct {
		ntp  string
		time string
	}{
		"ntp synchronized":     {"yes\n", time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)},
		"before the build":     {"no\n", "2001-01-01T00:00:00Z"},
		"10 years after build": {"no\n", "2050-01-01T00:00:00+05:30"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.fake.Set(ntpQuery, host.Response{Out: c.ntp})
			code, _, err := e.svc.Auth.EnsureClaimCode()
			must(t, err)
			e.want(e.do("POST", "/api/v1/session/claim", map[string]any{"method": "claim-code", "secret": code, "new_password": password,
				"client_time": c.time}), 201, "")
			if got := e.fake.Filter("systemd-run"); len(got) != 0 {
				t.Fatalf("the clock changed: %q", got)
			}
			if _, ok := clockgate.Read(e.p); ok {
				t.Fatal("a marker without a clock change")
			}
		})
	}
}
