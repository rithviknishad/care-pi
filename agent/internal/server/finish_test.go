package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/server/contracttest"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
)

// via sends the request to another served host of port 8443, with its origin.
func via(hostport string) reqOpt {
	return func(r *http.Request, _ *contracttest.Exchange) {
		r.Host = hostport
		r.Header.Set("Origin", "https://"+hostport)
	}
}

// steps completes each setup step of the easy path with the host name name.
func (e *env) steps(codes []string, name string) {
	e.t.Helper()
	e.want(e.do("PUT", "/api/v1/setup/steps/recovery-codes", map[string]any{"confirm_code": codes[0]}), 200, "")
	e.want(e.do("PUT", "/api/v1/setup/steps/hardware", map[string]any{"acknowledged": true}), 200, "")
	e.want(e.do("PUT", "/api/v1/setup/steps/identity", map[string]any{"display_name": "Sunrise Clinic", "hostname": name}), 200, "")
	e.want(e.do("PUT", "/api/v1/setup/steps/tls", map[string]any{"local_ca": map[string]any{"enabled": true}}), 200, "")
	e.want(e.do("PUT", "/api/v1/setup/steps/size", map[string]any{"preset": "solo"}), 200, "")
	e.want(e.do("POST", "/api/v1/setup/steps/env/skip", nil), 200, "")
	e.want(e.do("POST", "/api/v1/setup/steps/backups/skip", nil), 200, "")
	e.want(e.do("PUT", "/api/v1/setup/steps/care-admin", map[string]any{"username": "admin", "email": "admin@sunrise.example",
		"password": "a care superuser passphrase"}), 200, "")
}

// TestFinishFailsThenSucceeds: a failed setup job moves the state back to setup with its job_id.
// The second finish succeeds. The finish job applies a new host name (README section 5).
func TestFinishFailsThenSucceeds(t *testing.T) {
	e := newEnv(t)
	must(t, os.MkdirAll(e.p.Abs("/etc"), 0o755))
	must(t, os.WriteFile(e.p.Abs("/etc/hostname"), []byte("care\n"), 0o644))
	must(t, os.WriteFile(e.p.Abs("/etc/hosts"), []byte("127.0.0.1\tlocalhost\n127.0.1.1\tcare\n::1\tlocalhost\n"), 0o644))
	// Setup mode: bootstrap rendered Caddy for care.local. The console stays on care.local:8443 after
	// the identity step writes a new name, until the finish applies it.
	caddyfile, err := os.ReadFile("../render/testdata/golden/small-clinic-bootstrap/etc/care-pi/rendered/caddy/Caddyfile")
	must(t, err)
	must(t, os.MkdirAll(e.p.Abs(paths.RenderedCaddy), 0o700))
	must(t, os.WriteFile(e.p.Abs(paths.RenderedCaddy+"/Caddyfile"), caddyfile, 0o600))
	if got := e.svc.RenderedHosts(); !slices.Equal(got, []string{"care.local", "192.168.1.10", "10.0.0.5"}) {
		t.Fatalf("rendered hosts %v", got)
	}
	e.fake.On("podman exec --env DJANGO_SUPERUSER_PASSWORD care-api python manage.py createsuperuser --noinput --username admin --email admin@sunrise.example",
		host.Response{Out: "django.db.utils.OperationalError: the database is not ready", Code: 1},
		host.Response{Out: "Superuser created successfully.\n"})
	codes := e.claim()
	e.steps(codes, "sunrise")
	r := e.do("GET", "/api/v1/setup", nil)
	if steps := r.json(t)["steps"].([]any); len(steps[3].(map[string]any)["warnings"].([]any)) != 1 {
		t.Fatalf("the identity step warns about the new URL: %s", r.body)
	}

	r = e.do("POST", "/api/v1/setup/finish", nil)
	e.want(r, 202, "")
	id := r.json(t)["id"].(string)
	if j := e.waitJob(id); j.State != jobs.StateFailed || j.Error == nil {
		t.Fatalf("the first setup job must fail: %+v", j)
	}
	r = e.do("GET", "/api/v1/setup", nil)
	e.want(r, 200, "")
	m := r.json(t)
	if m["state"] != "setup" || m["job_id"] != id || m["can_finish"] != true {
		t.Fatalf("after a failed finish: %v", m)
	}
	if !slices.Contains(e.fake.Calls, "hostnamectl set-hostname sunrise") || !slices.Contains(e.fake.Calls, "systemctl try-restart avahi-daemon.service") {
		t.Fatalf("the finish job sets the host name: %q", e.fake.Filter("hostnamectl", "systemctl try-restart"))
	}
	hosts, err := os.ReadFile(e.p.Abs("/etc/hosts"))
	must(t, err)
	if string(hosts) != "127.0.0.1\tlocalhost\n127.0.1.1\tsunrise\n::1\tlocalhost\n" {
		t.Fatalf("/etc/hosts %q", hosts)
	}

	// hostnamectl wrote /etc/hostname: the second job skips the step.
	must(t, os.WriteFile(e.p.Abs("/etc/hostname"), []byte("sunrise\n"), 0o644))
	n := len(e.fake.Filter("hostnamectl"))
	r = e.do("POST", "/api/v1/setup/finish", nil)
	e.want(r, 202, "")
	j := e.waitJob(r.json(t)["id"].(string))
	if j.State != jobs.StateSucceeded {
		t.Fatalf("the second setup job: %s %+v", j.State, j.Error)
	}
	if len(e.fake.Filter("hostnamectl")) != n {
		t.Fatal("the second job must not set the host name again")
	}
	var res jobs.SetupResult
	must(t, json.Unmarshal(j.Result, &res))
	if res.CareURL != "https://sunrise.local/" || res.ConsoleURL != "https://sunrise.local:8443/" {
		t.Fatalf("result %+v", res)
	}
	r = e.do("GET", "/api/v1/setup", nil)
	if m := r.json(t); m["state"] != "ready" || m["job_id"] != nil {
		t.Fatalf("after the second finish: %v", m)
	}

	// Caddy serves only the new name now: the old origin gets 403, the address works.
	// The agent keeps the list of served hosts for 5 s.
	e.clock.add(6 * time.Second)
	e.want(e.do("POST", "/api/v1/fe/prune", map[string]any{"keep": 1}), 403, "csrf")
	ip := via("192.168.1.10:8443")

	// The FE operations after the setup.
	e.want(e.do("POST", "/api/v1/fe/build", map[string]any{"force": true}, ip), 422, "unsupported-config")
	r = e.do("POST", "/api/v1/fe/prune", map[string]any{"keep": 1}, ip)
	e.want(r, 202, "")
	if pj := e.waitJob(r.json(t)["id"].(string)); pj.State != jobs.StateSucceeded {
		t.Fatalf("fe prune %s %+v", pj.State, pj.Error)
	}
	cfg := e.do("GET", "/api/v1/config", nil)
	e.want(e.do("PATCH", "/api/v1/config", nil, rawBody([]byte(`{"fe":{"provider":"local"}}`)), ip,
		header("Content-Type", "application/merge-patch+json"), header("If-Match", cfg.header.Get("ETag"))), 200, "")
	r = e.do("POST", "/api/v1/fe/build", nil, ip)
	e.want(r, 202, "")
	fb := r.json(t)
	if fb["kind"] != "fe-build" || fb["cancellable"] != true {
		t.Fatalf("fe build job %v", fb)
	}
	// Under load the fake build can end before the cancel arrives: 409 is then correct.
	if cr := e.do("POST", "/api/v1/jobs/"+fb["id"].(string)+"/cancel", nil, ip); cr.status != 202 {
		e.want(cr, 409, "job-not-cancellable")
	}
	if bj := e.waitJob(fb["id"].(string)); bj.State == jobs.StateRunning || bj.State == jobs.StateQueued {
		t.Fatalf("fe build after cancel %s", bj.State)
	}
}

// TestPhase1HostIsReadyAfterClaim: a host of Phase 1 (lock.yaml, no setup record) needs no setup.
func TestPhase1HostIsReadyAfterClaim(t *testing.T) {
	e := newEnv(t)
	must(t, os.Remove(e.p.Abs(setup.File)))
	must(t, os.WriteFile(e.p.Abs(paths.LockFile), []byte("version: 1\n"), 0o600))
	r := e.do("GET", "/_care-pi/status", nil)
	if m := r.json(t); m["setup_state"] != "unclaimed" {
		t.Fatalf("before the claim: %v", m)
	}
	code, _, err := e.svc.Auth.EnsureClaimCode()
	must(t, err)
	r = e.do("POST", "/api/v1/session/claim", map[string]any{"method": "claim-code", "secret": strings.ToLower(code), "new_password": password})
	e.want(r, 201, "")
	m := r.json(t)
	e.csrf = m["session"].(map[string]any)["csrf_token"].(string)
	if m["session"].(map[string]any)["setup_state"] != "ready" {
		t.Fatalf("claim %v", m)
	}
	r = e.do("GET", "/api/v1/setup", nil)
	if m := r.json(t); m["state"] != "ready" || m["ready_at"] == nil || m["can_finish"] != false {
		t.Fatalf("setup %v", m)
	}
	e.want(e.do("PUT", "/api/v1/setup/steps/size", map[string]any{"preset": "solo"}), 409, "setup-finished")
	e.want(e.do("POST", "/api/v1/setup/steps/env/skip", nil), 409, "setup-finished")
	if setup.Phase(e.p) != setup.PhaseFull {
		t.Fatal("a Phase 1 host applies the full phase")
	}
}

// TestJobStreamHeartbeat: the event stream of a running job sends heartbeats without an id,
// then the events of the job, then done.
func TestJobStreamHeartbeat(t *testing.T) {
	e := newEnv(t)
	e.claim()
	release := make(chan struct{})
	j, err := e.svc.Jobs.Start(t.Context(), jobs.Spec{Kind: jobs.KindSupportBundle, Source: jobs.SourceConsole, Cancellable: true},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			end := rec.StepAction("collect: status", "collect")
			rec.Logf("collect the status")
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			end(nil)
			return nil, nil
		})
	must(t, err)
	req, _ := http.NewRequestWithContext(t.Context(), "GET", e.hs.URL+"/api/v1/jobs/"+j.ID+"/events?after=0", nil)
	req.Host = "care.local:8443"
	req.Header.Set("Accept", "text/event-stream")
	req.AddCookie(&http.Cookie{Name: CookieName, Value: e.cookie})
	res, err := http.DefaultClient.Do(req)
	must(t, err)
	defer func() { _ = res.Body.Close() }()
	var buf []byte
	deadline := time.Now().Add(10 * time.Second)
	released := false
	for !strings.Contains(string(buf), "event: done") && time.Now().Before(deadline) {
		chunk := make([]byte, 4096)
		n, err := res.Body.Read(chunk)
		buf = append(buf, chunk[:n]...)
		if !released && strings.Contains(string(buf), "event: heartbeat") {
			close(release)
			released = true
		}
		if err != nil {
			break
		}
	}
	if !released {
		close(release)
	}
	contracttest.Check(t, coverage, contracttest.Exchange{Method: "GET", URL: "/api/v1/jobs/" + j.ID + "/events?after=0",
		Header: req.Header, Status: res.StatusCode, RHeader: res.Header, RBody: buf})
	frames := contracttest.Frames(buf)
	var kinds []string
	for _, f := range frames {
		kinds = append(kinds, f.Event)
	}
	hb := slices.Index(kinds, "heartbeat")
	if hb < 0 || frames[hb].ID != "" || kinds[len(kinds)-1] != "done" || kinds[0] != "step" {
		t.Fatalf("frames %v\n%s", kinds, buf)
	}
}
