package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/service"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
)

// switchNet answers the internet check: down (no DNS, but the address answers) or up.
type switchNet struct {
	mu   sync.Mutex
	up   bool
	hits int
}

func (s *switchNet) set(up bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.up = up
}

func (s *switchNet) RoundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hits++
	if !s.up && r.URL.Host == "github.com" {
		return nil, &net.DNSError{Err: "no such host", Name: "github.com", IsNotFound: true}
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
}

func warningCodes(t *testing.T, st map[string]any, step string) []string {
	t.Helper()
	var out []string
	for _, s := range st["steps"].([]any) {
		m := s.(map[string]any)
		if m["id"] != step {
			continue
		}
		for _, w := range m["warnings"].([]any) {
			out = append(out, w.(map[string]any)["code"].(string))
		}
	}
	return out
}

// TestHostCheckProbeOutlivesAnAbortedRequest: the internet check of GET /host/check?probe=true has its own time
// limit, not the one of the request. A browser that aborts the request does not cancel the check in flight, so the
// result of the check is a real result, not "no route".
func TestHostCheckProbeOutlivesAnAbortedRequest(t *testing.T) {
	e := newEnv(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	e.svc.Internet = &service.Internet{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		once.Do(func() { close(entered) })
		<-release
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})}
	e.claim()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, "GET", e.hs.URL+"/api/v1/host/check?probe=true", nil)
	must(t, err)
	req.Host = "care.local:8443"
	req.Header.Set("X-Forwarded-For", e.addr)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: e.cookie})
	done := make(chan error, 1)
	go func() {
		_, err := http.DefaultClient.Do(req)
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the host check did not check the internet")
	}
	// The browser goes away. The server sees the closed connection before the check ends.
	cancel()
	if err := <-done; err == nil {
		t.Fatal("the request did not abort")
	}
	time.Sleep(200 * time.Millisecond)
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := e.svc.Internet.Snapshot()
		if st.Reachable != nil {
			if !*st.Reachable {
				t.Fatalf("the aborted request made the check fail: %+v", st)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the check did not end")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestInternetCheckNotFrozen (S74): a failed internet check at the hardware step does not stay in the setup state
// after the internet works; the host check checks again; the finish keeps only the warnings that are still true.
func TestInternetCheckNotFrozen(t *testing.T) {
	e := newEnv(t)
	writeRootCA(t, e.p)
	sw := &switchNet{}
	e.svc.Internet = &service.Internet{Transport: sw}
	e.svc.Internet.Check(context.Background())

	pub := e.do("GET", "/_care-pi/status", nil)
	e.want(pub, 200, "")
	if in := pub.json(t)["internet"].(map[string]any); in["reachable"] != false || in["problem"] != "dns" {
		t.Fatalf("public internet %v", in)
	}
	codes := e.claim()
	e.want(e.do("PUT", "/api/v1/setup/steps/recovery-codes", map[string]any{"confirm_code": codes[0]}), 200, "")

	r := e.do("GET", "/api/v1/host/check", nil)
	e.want(r, 200, "")
	hw := r.json(t)["hardware"].(map[string]any)
	if hw["internet_reachable"] != false || hw["internet_problem"] != "dns" {
		t.Fatalf("hardware %v", hw)
	}
	r = e.do("PUT", "/api/v1/setup/steps/hardware", map[string]any{"acknowledged": true})
	e.want(r, 200, "")
	if w := warningCodes(t, r.json(t), setup.StepHardware); len(w) != 1 || w[0] != "no-internet" {
		t.Fatalf("warnings %v", w)
	}
	rec, _, err := e.svc.Setup.Load()
	must(t, err)
	if p := rec.Steps[setup.StepHardware].Internet; p == nil || p.Reachable == nil || *p.Reachable || p.Problem != "dns" || p.CheckedAt == nil {
		t.Fatalf("the step records the check and its time: %+v", p)
	}

	// The internet works now. A host check with the probe checks again at once.
	sw.set(true)
	before := sw.hits
	r = e.do("GET", "/api/v1/host/check?probe=true", nil)
	e.want(r, 200, "")
	if sw.hits == before {
		t.Fatal("probe=true did not check the internet")
	}
	hw = r.json(t)["hardware"].(map[string]any)
	if hw["internet_reachable"] != true || hw["internet_problem"] != nil {
		t.Fatalf("hardware %v", hw)
	}
	for _, c := range r.json(t)["checks"].([]any) {
		m := c.(map[string]any)
		if m["name"] == "internet" && (m["ok"] != true || m["detail"] != "the internet is reachable") {
			t.Fatalf("the advised check is live: %v", m)
		}
	}
	st := e.do("GET", "/api/v1/setup", nil)
	e.want(st, 200, "")
	if w := warningCodes(t, st.json(t), setup.StepHardware); len(w) != 0 {
		t.Fatalf("the step froze the failed check: %v", w)
	}
	st = e.do("GET", "/api/v1/status", nil)
	e.want(st, 200, "")
	if n := st.json(t)["network"].(map[string]any); n["internet_reachable"] != true || n["internet_problem"] != nil {
		t.Fatalf("status network %v", n)
	}

	// The finish keeps in the record only the warnings that are still true.
	_, err = e.svc.Setup.Update(func(r *setup.Record) error {
		r.Steps[setup.StepHardware].Warnings = append(r.Steps[setup.StepHardware].Warnings, setup.Issue{Code: "sd-card", Message: "x"})
		return nil
	})
	must(t, err)
	e.want(e.do("PUT", "/api/v1/setup/steps/identity", map[string]any{"display_name": "Sunrise Clinic", "hostname": "care"}), 200, "")
	e.want(e.do("PUT", "/api/v1/setup/steps/tls", map[string]any{"local_ca": map[string]any{"enabled": true}}), 200, "")
	e.want(e.do("PUT", "/api/v1/setup/steps/size", map[string]any{"preset": "solo"}), 200, "")
	e.want(e.do("POST", "/api/v1/setup/steps/env/skip", nil), 200, "")
	e.want(e.do("POST", "/api/v1/setup/steps/backups/skip", nil), 200, "")
	e.want(e.do("PUT", "/api/v1/setup/steps/care-admin", map[string]any{"username": "admin", "email": "admin@sunrise.example", "password": "a care superuser passphrase"}), 200, "")
	r = e.do("POST", "/api/v1/setup/finish", nil)
	e.want(r, 202, "")
	e.waitJob(r.json(t)["id"].(string))
	rec, _, err = e.svc.Setup.Load()
	must(t, err)
	if w := rec.Steps[setup.StepHardware].Warnings; len(w) != 0 {
		t.Fatalf("the record keeps warnings that are not true: %v", w)
	}
}
