package server

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// jsonBody returns a JSON object of about n bytes.
func jsonBody(n int) []byte {
	return []byte(`{"password":"` + strings.Repeat("a", n) + `"}`)
}

// TestBodyLimits: a request without a session never makes the agent read more than 8 KiB,
// and each route has its limit (docs/console-server.md, section 4).
func TestBodyLimits(t *testing.T) {
	e := newEnv(t)
	e.claim()
	cookie, csrf := e.cookie, e.csrf
	e.cookie, e.csrf = "", ""
	ct := header("Content-Type", "application/json")

	// Credential operations: 8 KiB.
	e.want(e.do("POST", "/api/v1/session/login", nil, rawBody(jsonBody(9<<10)), ct, invalid), 413, "payload-too-large")
	e.want(e.do("POST", "/api/v1/session/claim", nil, rawBody(jsonBody(9<<10)), ct, invalid), 413, "payload-too-large")
	e.want(e.do("POST", "/api/v1/session/login", nil, rawBody(jsonBody(7<<10)), ct, invalid), 422, "invalid-request")
	// An operation with a session checks the session before it reads the body.
	e.want(e.do("POST", "/api/v1/apply", nil, rawBody(jsonBody(100<<10)), ct, invalid), 401, "not-authenticated")
	e.want(e.do("PUT", "/api/v1/config", nil, rawBody(jsonBody(900<<10)), ct, invalid), 401, "not-authenticated")

	e.cookie, e.csrf = cookie, csrf
	e.want(e.do("POST", "/api/v1/session/reauth", nil, rawBody(jsonBody(9<<10)), ct, invalid), 413, "payload-too-large")
	e.want(e.do("POST", "/api/v1/security/totp/verify", nil, rawBody(jsonBody(9<<10)), ct, invalid), 413, "payload-too-large")
	// Each other operation: 64 KiB.
	r := e.do("POST", "/api/v1/apply", nil, rawBody(jsonBody(65<<10)), ct, invalid)
	e.want(r, 413, "payload-too-large")
	if !strings.Contains(string(r.body), "64 KiB") {
		t.Fatalf("detail: %s", r.body)
	}
	// The config and the setup steps with a part of the config: 1 MiB.
	e.want(e.do("PUT", "/api/v1/config", nil, rawBody(jsonBody(900<<10)), ct, invalid), 428, "precondition-required")
	e.want(e.do("PUT", "/api/v1/setup/steps/env", nil, rawBody(jsonBody(900<<10)), ct, invalid), 422, "invalid-request")
	e.want(e.do("PUT", "/api/v1/config", nil, rawBody(jsonBody(MaxBody)), ct, invalid), 413, "payload-too-large")
}

// slowRequest sends the headers of a POST with a body of 100 bytes and only 12 bytes of it.
func slowRequest(t *testing.T, addr, path, extra string) (*http.Response, time.Duration) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	must(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	start := time.Now()
	_, err = conn.Write([]byte("POST " + path + " HTTP/1.1\r\nHost: care.local:8443\r\nOrigin: " + origin +
		"\r\nContent-Type: application/json\r\nContent-Length: 100\r\nX-Forwarded-For: 192.168.1.50\r\n" + extra +
		"\r\n{\"password\":"))
	must(t, err)
	must(t, conn.SetReadDeadline(time.Now().Add(10*time.Second)))
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	must(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })
	return res, time.Since(start)
}

// TestSlowBody: a body that does not arrive within the body timeout gives 400 malformed-request
// (the catalogue has no type for 408), and the agent closes the connection.
func TestSlowBody(t *testing.T) {
	e := newEnv(t)
	srv := New(Options{Service: e.svc, BodyTimeout: 300 * time.Millisecond})
	hs := httptest.NewServer(srv)
	t.Cleanup(func() {
		srv.Close()
		hs.Close()
	})
	res, took := slowRequest(t, hs.Listener.Addr().String(), "/api/v1/session/login", "")
	var p struct {
		Type   string `json:"type"`
		Detail string `json:"detail"`
	}
	must(t, json.NewDecoder(res.Body).Decode(&p))
	if res.StatusCode != 400 || !strings.HasSuffix(p.Type, "/malformed-request") || !strings.Contains(p.Detail, "did not arrive") {
		t.Fatalf("slow body: %d %+v", res.StatusCode, p)
	}
	if took < 300*time.Millisecond || took > 5*time.Second {
		t.Fatalf("the agent answered after %s", took)
	}
	if !res.Close {
		t.Fatal("the agent must close the connection after a slow body")
	}

	// A request with a session that the agent rejects before the body also has the deadline.
	res, took = slowRequest(t, hs.Listener.Addr().String(), "/api/v1/apply", "")
	if res.StatusCode != 401 || took > 5*time.Second {
		t.Fatalf("apply without a session: %d after %s", res.StatusCode, took)
	}
}

// TestSecFetchSite: a request with a session from another origin (port 443 is the same site)
// gets 403 csrf, and it does not move the idle timeout of the session.
func TestSecFetchSite(t *testing.T) {
	e := newEnv(t)
	e.claim()
	for _, site := range []string{"same-site", "cross-site"} {
		e.want(e.do("GET", "/api/v1/host/check?probe=true", nil, header("Sec-Fetch-Site", site)), 403, "csrf")
		e.want(e.do("GET", "/api/v1/apply/plan?update=true", nil, header("Sec-Fetch-Site", site)), 403, "csrf")
		e.want(e.do("POST", "/api/v1/apply", map[string]any{}, header("Sec-Fetch-Site", site)), 403, "csrf")
	}
	e.want(e.do("GET", "/api/v1/status", nil, header("Sec-Fetch-Site", "same-origin")), 200, "")
	e.want(e.do("GET", "/api/v1/session", nil, header("Sec-Fetch-Site", "none")), 200, "")
	// The public operations do not use the session.
	e.want(e.do("GET", "/api/v1/health", nil, header("Sec-Fetch-Site", "cross-site")), 200, "")

	e.clock.add(90 * time.Minute)
	e.want(e.do("GET", "/api/v1/status", nil, header("Sec-Fetch-Site", "same-origin")), 200, "")
	e.clock.add(90 * time.Minute)
	e.want(e.do("GET", "/api/v1/status", nil, header("Sec-Fetch-Site", "same-site")), 403, "csrf")
	// The last request with a session was 121 minutes ago: the idle timeout (2 h) ended the session.
	e.clock.add(31 * time.Minute)
	e.want(e.do("GET", "/api/v1/session", nil), 401, "not-authenticated")
}

func TestListenKeepsALiveSocket(t *testing.T) {
	t.Chdir(t.TempDir())
	live, err := net.Listen("unix", "api.sock")
	must(t, err)
	_, err = Listen("unix://api.sock", paths.New("."), false)
	if err == nil || !strings.Contains(err.Error(), "another care-pi serve answers on api.sock") {
		t.Fatalf("a live socket: %v", err)
	}
	conn, err := net.Dial("unix", "api.sock")
	if err != nil {
		t.Fatalf("Listen must not remove a live socket: %v", err)
	}
	_ = conn.Close()

	// A socket without a server (the agent stopped without a clean exit) is replaced.
	live.(*net.UnixListener).SetUnlinkOnClose(false)
	must(t, live.Close())
	l, err := Listen("unix://api.sock", paths.New("."), false)
	if err != nil {
		t.Fatalf("an old socket: %v", err)
	}
	must(t, l.Close())

	must(t, os.WriteFile("file.sock", nil, 0o600))
	if _, err := Listen("unix://file.sock", paths.New("."), false); err == nil || !strings.Contains(err.Error(), "is not a socket") {
		t.Fatalf("a file: %v", err)
	}
}

func TestCheckListen(t *testing.T) {
	for spec, want := range map[string]string{
		"":                      "",
		"unix:///run/x.sock":    "",
		"tcp://127.0.0.1:8443":  "",
		"tcp://localhost:8443":  "",
		"tcp://0.0.0.0:8443":    "loopback",
		"tcp://192.168.1.10:80": "loopback",
		"tcp://127.0.0.1":       "HOST:PORT",
		"http://127.0.0.1:8443": "must start with",
	} {
		err := CheckListen(spec, true)
		if (want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), want)) {
			t.Errorf("CheckListen(%q, dev): %v, want %q", spec, err, want)
		}
	}
	if err := CheckListen("tcp://127.0.0.1:8443", false); err == nil || !strings.Contains(err.Error(), "--insecure-dev") {
		t.Errorf("TCP without dev: %v", err)
	}
}
