package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/auth"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/server/contracttest"
)

// TestSetupFlow: claim, recovery codes, CSRF, each step, finish (the setup job runs apply on the
// host fake), and ready.
func TestSetupFlow(t *testing.T) {
	e := newEnv(t)
	writeRootCA(t, e.p)

	r := e.do("GET", "/_care-pi/status", nil)
	e.want(r, 200, "")
	pub := r.json(t)
	if pub["setup_state"] != "unclaimed" || pub["console_port"] != float64(8443) || pub["site"] != "setup" {
		t.Fatalf("public status %v", pub)
	}
	e.want(e.do("GET", "/api/v1/session", nil), 401, "not-authenticated")
	e.want(e.do("POST", "/api/v1/session/login", map[string]any{"password": password}), 409, "not-claimed")
	e.want(e.do("POST", "/api/v1/session/recover", map[string]any{"recovery_code": "7KQ2M-X9PDA", "new_password": password}), 409, "not-claimed")
	_, _, err := e.svc.Auth.EnsureClaimCode()
	must(t, err)
	e.want(e.do("POST", "/api/v1/session/claim", map[string]any{"method": "claim-code", "secret": "0000-0000-0000", "new_password": password}), 401, "bad-credentials")
	e.want(e.do("POST", "/api/v1/session/claim", map[string]any{"method": "os-password", "secret": "x", "new_password": password}), 422, "invalid-request")
	e.want(e.do("POST", "/api/v1/session/claim", map[string]any{"method": "claim-code", "secret": "0000-0000-0000", "new_password": "short"}, invalid), 422, "weak-password")
	codes := e.claim()
	if len(codes) != 8 {
		t.Fatalf("codes %v", codes)
	}
	e.want(e.do("GET", "/api/v1/session", nil), 200, "")
	e.want(e.do("POST", "/api/v1/session/claim", map[string]any{"method": "claim-code", "secret": "0000-0000-0000", "new_password": password}), 409, "already-claimed")

	st := e.do("GET", "/api/v1/setup", nil)
	e.want(st, 200, "")
	if m := st.json(t); m["state"] != "claimed" || m["current_step"] != "recovery-codes" || m["dev_build"] != true {
		t.Fatalf("setup %v", m)
	}
	// CSRF: the token is required on each unsafe request.
	saved := e.csrf
	e.csrf = ""
	e.want(e.do("PUT", "/api/v1/setup/steps/recovery-codes", map[string]any{"confirm_code": codes[2]}), 403, "csrf")
	e.csrf = "wrong-token-wrong-token-wrong-token-wrong-to"
	e.want(e.do("PUT", "/api/v1/setup/steps/recovery-codes", map[string]any{"confirm_code": codes[2]}), 403, "csrf")
	e.csrf = saved

	e.want(e.do("PUT", "/api/v1/setup/steps/recovery-codes", map[string]any{"confirm_code": "ZZZZZ-ZZZZZ"}), 422, "invalid-request")
	e.want(e.do("PUT", "/api/v1/setup/steps/recovery-codes", map[string]any{"confirm_code": strings.ToLower(codes[2])}), 200, "")
	e.want(e.do("PUT", "/api/v1/setup/steps/hardware", map[string]any{"acknowledged": true}), 200, "")
	e.want(e.do("PUT", "/api/v1/setup/steps/identity", map[string]any{"display_name": "Sunrise", "hostname": "BAD NAME"}, invalid), 422, "invalid-config")
	r = e.do("PUT", "/api/v1/setup/steps/identity", map[string]any{"display_name": "Sunrise Clinic", "hostname": "care"})
	e.want(r, 200, "")
	if r.json(t)["state"] != "setup" {
		t.Fatalf("the first step moves the state to setup: %s", r.body)
	}
	e.want(e.do("PUT", "/api/v1/setup/steps/tls", map[string]any{"local_ca": map[string]any{"enabled": true},
		"public": map[string]any{"enabled": true, "clinic_id": "sunrise"}}), 422, "unsupported-config")
	e.want(e.do("PUT", "/api/v1/setup/steps/tls", map[string]any{"local_ca": map[string]any{"enabled": true}}), 200, "")
	e.want(e.do("PUT", "/api/v1/setup/steps/size", map[string]any{"preset": "solo"}), 200, "")
	e.want(e.do("POST", "/api/v1/setup/finish", nil), 409, "setup-incomplete")
	e.want(e.do("POST", "/api/v1/setup/steps/hardware/skip", nil), 409, "step-not-skippable")
	e.want(e.do("POST", "/api/v1/setup/steps/env/skip", nil), 200, "")
	e.want(e.do("PUT", "/api/v1/setup/steps/env", map[string]any{"backend": map[string]any{}, "frontend": map[string]any{},
		"fe": map[string]any{"provider": "none"}}), 200, "")
	e.want(e.do("PUT", "/api/v1/setup/steps/backups", map[string]any{"destinations": []any{map[string]any{"id": "usb", "type": "posix", "path": "/mnt/b"}},
		"credentials": map[string]any{}, "recovery_key_words": []any{map[string]any{"index": 1, "word": "able"}, map[string]any{"index": 2, "word": "acid"}, map[string]any{"index": 3, "word": "also"}}}), 501, "not-implemented-in-this-version")
	e.want(e.do("POST", "/api/v1/setup/steps/backups/skip", nil), 200, "")
	e.want(e.do("PUT", "/api/v1/setup/steps/care-admin", map[string]any{"username": "admin", "email": "admin@sunrise.example", "password": password}), 422, "weak-password")
	e.want(e.do("PUT", "/api/v1/setup/steps/care-admin", map[string]any{"username": "ad..min", "email": "admin@sunrise.example", "password": "a care superuser passphrase"}), 422, "invalid-request")
	r = e.do("PUT", "/api/v1/setup/steps/care-admin", map[string]any{"username": "admin", "email": "admin@sunrise.example", "password": "a care superuser passphrase"})
	e.want(r, 200, "")
	if m := r.json(t); m["can_finish"] != true || m["current_step"] != nil {
		t.Fatalf("setup before finish %v", m)
	}
	e.want(e.do("POST", "/api/v1/apply", nil), 409, "setup-incomplete")
	e.want(e.do("POST", "/api/v1/care/superuser", map[string]any{"username": "second", "email": "s@sunrise.example", "password": "another care passphrase"}), 409, "setup-incomplete")

	r = e.do("POST", "/api/v1/setup/finish", nil)
	e.want(r, 202, "")
	job := r.json(t)
	id := job["id"].(string)
	if job["kind"] != "setup" || r.header.Get("Location") != "/api/v1/jobs/"+id {
		t.Fatalf("finish job %v %v", job, r.header)
	}
	j := e.waitJob(id)
	if j.State != jobs.StateSucceeded {
		t.Fatalf("setup job %s: %+v", j.State, j.Error)
	}

	// SSE: the whole stream from the first event, then a resume after event 3.
	r = e.do("GET", "/api/v1/jobs/"+id+"/events?after=0", nil, header("Accept", "text/event-stream"))
	e.want(r, 200, "")
	frames := contracttest.Frames(r.body)
	if len(frames) < 6 || frames[len(frames)-1].Event != "done" || frames[0].ID != "1" {
		t.Fatalf("frames %+v", frames)
	}
	r = e.do("GET", "/api/v1/jobs/"+id+"/events?after=0", nil, header("Accept", "text/event-stream"), header("Last-Event-ID", "3"))
	if f := contracttest.Frames(r.body); f[0].ID != "4" {
		t.Fatalf("Last-Event-ID wins over after: %+v", f[0])
	}
	r = e.do("GET", "/api/v1/jobs/"+id+"/events?after=3", nil)
	e.want(r, 200, "")
	page := r.json(t)
	evs := page["events"].([]any)
	if page["done"] != true || evs[0].(map[string]any)["id"] != float64(4) {
		t.Fatalf("page %v", page)
	}

	r = e.do("GET", "/api/v1/setup", nil)
	m := r.json(t)
	if m["state"] != "ready" || m["ready_at"] == nil || m["job_id"] != nil {
		t.Fatalf("setup after finish %v", m)
	}
	var superuser string
	for _, c := range e.fake.Calls {
		if strings.Contains(c, "createsuperuser") {
			superuser = c
		}
	}
	if superuser == "" || strings.Contains(superuser, "passphrase") {
		t.Fatalf("createsuperuser call %q must exist without the password", superuser)
	}
	e.want(e.do("PUT", "/api/v1/setup/steps/size", map[string]any{"preset": "solo"}), 409, "setup-finished")
	e.want(e.do("POST", "/api/v1/setup/finish", nil), 409, "setup-finished")

	r = e.do("GET", "/api/v1/status", nil)
	e.want(r, 200, "")
	if s := r.json(t); s["setup_state"] != "ready" || s["agent"].(map[string]any)["dev_build"] != true {
		t.Fatalf("status %v", s)
	}
	r = e.do("GET", "/api/v1/jobs?kind=setup&limit=5", nil)
	if items := r.json(t)["items"].([]any); len(items) != 1 {
		t.Fatalf("jobs %s", r.body)
	}
	e.want(e.do("GET", "/api/v1/jobs/"+id, nil), 200, "")
	e.want(e.do("GET", "/api/v1/jobs/20991231T000000.000Z-apply", nil), 404, "job-not-found")
	e.want(e.do("GET", "/api/v1/jobs?limit=500", nil, invalid), 422, "invalid-request")
	e.want(e.do("GET", "/api/v1/care", nil), 200, "")
	e.want(e.do("POST", "/api/v1/care/superuser", map[string]any{"username": "second", "email": "s@sunrise.example", "password": "another care passphrase"}), 201, "")
	e.fake.On("podman exec --env DJANGO_SUPERUSER_PASSWORD care-api python manage.py createsuperuser --noinput --username second --email s@sunrise.example",
		host.Response{Out: "CommandError: Error: That username is already taken.", Code: 1})
	e.want(e.do("POST", "/api/v1/care/superuser", map[string]any{"username": "second", "email": "s@sunrise.example", "password": "another care passphrase"}), 409, "care-user-exists")

	// A normal apply job after setup.
	r = e.do("POST", "/api/v1/apply", map[string]any{"reason": "contract test", "update": false})
	e.want(r, 202, "")
	aj := e.waitJob(r.json(t)["id"].(string))
	if aj.State != jobs.StateSucceeded || string(aj.Result) == "null" {
		t.Fatalf("apply job %+v", aj)
	}
	e.want(e.do("GET", "/api/v1/jobs/"+aj.ID+"/events", nil, header("Accept", "text/event-stream")), 200, "")
	e.want(e.do("GET", "/api/v1/apply/plan?diff=true", nil), 200, "")
	e.want(e.do("POST", "/api/v1/jobs/"+aj.ID+"/cancel", nil), 409, "job-not-cancellable")
	e.want(e.do("POST", "/api/v1/jobs/20991231T000000.000Z-apply/cancel", nil), 404, "job-not-found")
}

// TestOriginAndBodyRules: an Origin of port 443 gets 403, and the general body rules.
func TestOriginAndBodyRules(t *testing.T) {
	e := newEnv(t)
	e.claim()
	for _, o := range []string{"https://care.local", "https://evil.example:8443", "http://care.local:8443"} {
		r := e.do("POST", "/api/v1/session/reauth", map[string]any{"password": password}, header("Origin", o))
		e.want(r, 403, "csrf")
	}
	e.want(e.do("POST", "/api/v1/session/reauth", map[string]any{"password": password}, noHeader("Origin")), 403, "csrf")
	e.want(e.do("POST", "/api/v1/session/login", map[string]any{"password": password}, header("Origin", "https://care.local")), 403, "csrf")
	e.want(e.do("POST", "/api/v1/session/reauth", map[string]any{"password": password}, header("Content-Type", "text/plain"), invalid), 415, "unsupported-media-type")
	e.want(e.do("POST", "/api/v1/session/reauth", nil, rawBody([]byte(`{"password":`)), header("Content-Type", "application/json"), invalid), 400, "malformed-request")
	e.want(e.do("POST", "/api/v1/session/reauth", map[string]any{"password": password, "extra": 1}, invalid), 422, "invalid-request")
	big := []byte(`{"password":"` + strings.Repeat("a", MaxBody) + `"}`)
	e.want(e.do("POST", "/api/v1/session/reauth", nil, rawBody(big), header("Content-Type", "application/json"), invalid), 413, "payload-too-large")
	// The served IP address on port 8443 is a valid origin too.
	r := e.do("POST", "/api/v1/session/reauth", map[string]any{"password": password}, header("Origin", "https://192.168.1.10:8443"),
		func(rq *http.Request, _ *contracttest.Exchange) { rq.Host = "192.168.1.10:8443" })
	e.want(r, 200, "")
	nr, err := http.Get(e.hs.URL + "/api/v1/nothing")
	must(t, err)
	_ = nr.Body.Close()
	if nr.StatusCode != 404 || nr.Header.Get("Content-Type") != "application/problem+json" {
		t.Fatalf("unknown path: %d %s", nr.StatusCode, nr.Header.Get("Content-Type"))
	}
}

// TestLockoutAndRateLimit uses the fake clock of the agent.
func TestLockoutAndRateLimit(t *testing.T) {
	e := newEnv(t)
	e.claim()
	e.cookie, e.csrf = "", ""
	for i := range 5 {
		r := e.do("POST", "/api/v1/session/login", map[string]any{"password": "wrong password"})
		e.want(r, 401, "bad-credentials")
		_ = i
	}
	r := e.do("POST", "/api/v1/session/login", map[string]any{"password": password})
	e.want(r, 429, "locked-out")
	if r.header.Get("Retry-After") != "900" {
		t.Fatalf("Retry-After %q", r.header.Get("Retry-After"))
	}
	e.clock.add(15 * time.Minute)
	e.want(e.do("POST", "/api/v1/session/login", map[string]any{"password": password}), 200, "")
	for range 9 {
		e.want(e.do("POST", "/api/v1/session/login", map[string]any{"password": password}), 200, "")
	}
	r = e.do("POST", "/api/v1/session/login", map[string]any{"password": password})
	e.want(r, 429, "rate-limited")
	e.clock.add(time.Minute)

	// TOTP required, then a TOTP login.
	r = e.do("POST", "/api/v1/session/login", map[string]any{"password": password})
	e.want(r, 200, "")
	e.csrf = r.json(t)["csrf_token"].(string)
	en := e.do("POST", "/api/v1/security/totp", nil)
	e.want(en, 200, "")
	secret := en.json(t)["secret"].(string)
	e.want(e.do("POST", "/api/v1/security/totp/verify", map[string]any{"code": "000000"}), 422, "invalid-request")
	code := totp(t, secret, e.clock.now())
	e.want(e.do("POST", "/api/v1/security/totp/verify", map[string]any{"code": code}), 200, "")
	e.want(e.do("POST", "/api/v1/security/totp", nil), 409, "state-conflict")
	e.cookie, e.csrf = "", ""
	e.want(e.do("POST", "/api/v1/session/login", map[string]any{"password": password}), 401, "totp-required")
	e.clock.add(30 * time.Second)
	r = e.do("POST", "/api/v1/session/login", map[string]any{"password": password, "totp_code": totp(t, secret, e.clock.now())})
	e.want(r, 200, "")
	e.csrf = r.json(t)["csrf_token"].(string)
	e.want(e.do("DELETE", "/api/v1/security/totp", nil), 204, "")
	e.want(e.do("DELETE", "/api/v1/security/totp", nil), 409, "state-conflict")
	e.want(e.do("POST", "/api/v1/security/totp/verify", map[string]any{"code": "123456"}), 409, "state-conflict")
}

// TestSecurityAndSessions: reauthentication, recovery codes, password change, sessions, logout.
func TestSecurityAndSessions(t *testing.T) {
	e := newEnv(t)
	codes := e.claim()
	e.want(e.do("GET", "/api/v1/security", nil), 200, "")
	e.want(e.do("POST", "/api/v1/security/recovery-codes", nil), 200, "")
	e.clock.add(6 * time.Minute)
	e.want(e.do("POST", "/api/v1/security/recovery-codes", nil), 403, "reauth-required")
	e.want(e.do("POST", "/api/v1/power/shutdown", nil), 403, "reauth-required")
	e.want(e.do("POST", "/api/v1/session/reauth", map[string]any{"password": "wrong password"}), 401, "bad-credentials")
	e.want(e.do("POST", "/api/v1/session/reauth", map[string]any{"password": password}), 200, "")
	r := e.do("POST", "/api/v1/security/recovery-codes", nil)
	e.want(r, 200, "")
	fresh := r.json(t)["codes"].([]any)
	e.want(e.do("POST", "/api/v1/power/reboot", map[string]any{"delay_s": 5}), 202, "")
	e.want(e.do("POST", "/api/v1/power/shutdown", map[string]any{"delay_s": 0}, invalid), 422, "invalid-request")
	e.want(e.do("POST", "/api/v1/power/shutdown", nil), 202, "")

	// The old codes do not work any more.
	cookie, csrf := e.cookie, e.csrf
	e.cookie, e.csrf = "", ""
	e.want(e.do("POST", "/api/v1/session/recover", map[string]any{"recovery_code": codes[0], "new_password": "a recovered passphrase"}), 401, "bad-credentials")
	r = e.do("POST", "/api/v1/session/recover", map[string]any{"recovery_code": fresh[0].(string), "new_password": "a recovered passphrase"})
	e.want(r, 200, "")
	if r.json(t)["recovery_codes_remaining"] != float64(7) {
		t.Fatalf("recover %s", r.body)
	}
	recovered, recoveredCSRF := e.cookie, r.json(t)["session"].(map[string]any)["csrf_token"].(string)
	e.cookie, e.csrf = cookie, csrf
	e.want(e.do("GET", "/api/v1/session", nil), 401, "not-authenticated")
	e.cookie, e.csrf = recovered, recoveredCSRF

	// A second session, then the list and a revoke.
	other := &env{t: t, p: e.p, svc: e.svc, srv: e.srv, hs: e.hs, clock: e.clock, addr: "192.168.1.24"}
	r = other.do("POST", "/api/v1/session/login", map[string]any{"password": "a recovered passphrase"})
	other.want(r, 200, "")
	r = e.do("GET", "/api/v1/security/sessions", nil)
	e.want(r, 200, "")
	items := r.json(t)["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("sessions %s", r.body)
	}
	var otherRef string
	for _, it := range items {
		if m := it.(map[string]any); m["current"] == false {
			otherRef = m["ref"].(string)
		}
	}
	e.want(e.do("DELETE", "/api/v1/security/sessions/"+otherRef, nil), 204, "")
	e.want(e.do("DELETE", "/api/v1/security/sessions/"+otherRef, nil), 404, "not-found")
	other.want(other.do("GET", "/api/v1/session", nil), 401, "not-authenticated")

	before := e.cookie
	e.want(e.do("PUT", "/api/v1/security/password", map[string]any{"current_password": "wrong password", "new_password": "the newest passphrase"}), 401, "bad-credentials")
	e.want(e.do("PUT", "/api/v1/security/password", map[string]any{"current_password": "a recovered passphrase", "new_password": "short"}, invalid), 422, "weak-password")
	r = e.do("PUT", "/api/v1/security/password", map[string]any{"current_password": "a recovered passphrase", "new_password": "the newest passphrase"})
	e.want(r, 200, "")
	if e.cookie == before {
		t.Fatal("a password change gives a new session id")
	}
	r = e.do("DELETE", "/api/v1/session", nil)
	e.want(r, 204, "")
	if sc := r.header.Get("Set-Cookie"); sc != CookieName+"=; Path=/; Max-Age=0; HttpOnly; Secure; SameSite=Strict" {
		t.Fatalf("Set-Cookie %q", sc)
	}
	e.want(e.do("GET", "/api/v1/session", nil), 401, "not-authenticated")
}

// TestConfigResource: ETag, If-Match, 412, 428, a merge patch, and a dry run.
func TestConfigResource(t *testing.T) {
	e := newEnv(t)
	e.claim()
	r := e.do("GET", "/api/v1/config", nil)
	e.want(r, 200, "")
	etag := r.header.Get("ETag")
	if len(etag) != 18 {
		t.Fatalf("ETag %q", etag)
	}
	e.want(e.do("GET", "/api/v1/config?view=file", nil), 200, "")
	e.want(e.do("GET", "/api/v1/config?view=all", nil, invalid), 422, "invalid-request")
	e.want(e.do("GET", "/api/v1/config/schema", nil), 200, "")
	e.want(e.do("GET", "/api/v1/config/presets", nil), 200, "")

	patch := func(body any, opts ...reqOpt) *resp {
		data, _ := json.Marshal(body)
		return e.do("PATCH", "/api/v1/config"+"", nil, append([]reqOpt{rawBody(data), header("Content-Type", "application/merge-patch+json")}, opts...)...)
	}
	e.want(patch(map[string]any{"size": map[string]any{"preset": "solo"}}, invalid), 428, "precondition-required")
	e.want(patch(map[string]any{"size": map[string]any{"preset": "solo"}}, header("If-Match", `"0000000000000000"`)), 412, "config-changed")
	e.want(patch(map[string]any{"size": map[string]any{"preset": "giant"}}, header("If-Match", etag)), 422, "invalid-config")
	e.want(patch(map[string]any{"tls": map[string]any{"tailscale": map[string]any{"enabled": true}}}, header("If-Match", etag)), 422, "unsupported-config")
	r = e.do("PATCH", "/api/v1/config?dry_run=true", nil, rawBody([]byte(`{"size":{"preset":"solo"}}`)),
		header("Content-Type", "application/merge-patch+json"), header("If-Match", etag))
	e.want(r, 200, "")
	if m := r.json(t); m["written"] != false || r.header.Get("ETag") != etag {
		t.Fatalf("dry run %v", m)
	}
	r = e.do("PATCH", "/api/v1/config?apply=true", nil, rawBody([]byte(`{"size":{"preset":"solo"}}`)),
		header("Content-Type", "application/merge-patch+json"), header("If-Match", etag))
	e.want(r, 409, "setup-incomplete")
	r = e.do("PATCH", "/api/v1/config", nil, rawBody([]byte(`{"size":{"preset":"solo"},"env":{"backend":{"EMAIL_PORT":"587"}}}`)),
		header("Content-Type", "application/merge-patch+json"), header("If-Match", etag))
	e.want(r, 200, "")
	next := r.header.Get("ETag")
	if next == etag || r.json(t)["written"] != true {
		t.Fatalf("write %s", r.body)
	}
	e.want(e.do("PUT", "/api/v1/config", map[string]any{"version": 1}, header("If-Match", etag)), 412, "config-changed")
	r = e.do("GET", "/api/v1/config?view=file", nil)
	if m := r.json(t); m["size"].(map[string]any)["preset"] != "solo" {
		t.Fatalf("file %v", m)
	}
	e.want(e.do("PUT", "/api/v1/config", map[string]any{"version": 1, "identity": map[string]any{"display_name": "X", "hostname": "care"}},
		header("If-Match", next)), 200, "")
}

// TestNotImplementedAndPublic: the operations of later phases give 501, and the public operations
// answer on both prefixes.
func TestNotImplementedAndPublic(t *testing.T) {
	e := newEnv(t)
	writeRootCA(t, e.p)
	for _, u := range []string{"/_care-pi/health", "/api/v1/health"} {
		e.want(e.do("GET", u, nil), 200, "")
	}
	for _, prefix := range []string{"/_care-pi", "/api/v1"} {
		r := e.do("GET", prefix+"/tls/root-ca", nil)
		e.want(r, 200, "")
		if len(r.header.Get("X-Care-Pi-Fingerprint-SHA256")) != 64 {
			t.Fatalf("fingerprint header %v", r.header)
		}
		e.want(e.do("GET", prefix+"/tls/root-ca?format=pem", nil), 200, "")
		e.want(e.do("GET", prefix+"/tls/root-ca?format=p12", nil, invalid), 422, "invalid-request")
		e.want(e.do("GET", prefix+"/tls/root-ca/mobileconfig", nil), 200, "")
		for _, os := range []string{"ios", "android", "windows", "macos", "linux", "chromeos"} {
			r := e.do("GET", prefix+"/tls/instructions/"+os, nil)
			e.want(r, 200, "")
			root := r.json(t)["root"].(map[string]any)
			if !strings.HasPrefix(root["download_path"].(string), prefix+"/tls/root-ca") {
				t.Fatalf("download path %v", root)
			}
		}
		e.want(e.do("GET", prefix+"/tls/instructions/beos", nil, invalid), 422, "invalid-request")
	}
	// Each other operation is not below /_care-pi/.
	for _, u := range []string{"/_care-pi/session", "/_care-pi/setup", "/_care-pi/api/v1/status"} {
		r, err := http.Get(e.hs.URL + u)
		must(t, err)
		_ = r.Body.Close()
		if r.StatusCode != 404 {
			t.Fatalf("%s: %d", u, r.StatusCode)
		}
	}
	e.want(e.do("GET", "/api/v1/tls", nil), 401, "not-authenticated")
	e.want(e.do("GET", "/api/v1/backups", nil), 401, "not-authenticated")
	e.claim()
	e.want(e.do("GET", "/api/v1/tls", nil), 200, "")
	e.want(e.do("GET", "/api/v1/network", nil), 200, "")
	e.want(e.do("GET", "/api/v1/host/check?probe=false", nil), 200, "")
	e.want(e.do("GET", "/api/v1/host/check?probe=maybe", nil, invalid), 422, "invalid-request")
	e.want(e.do("GET", "/api/v1/fe", nil), 200, "")
	e.want(e.do("POST", "/api/v1/fe/build", map[string]any{"force": false}), 409, "setup-incomplete")
	e.want(e.do("POST", "/api/v1/fe/prune", map[string]any{"keep": 2}), 409, "setup-incomplete")

	type later struct {
		method, url string
		body        any
	}
	dest := map[string]any{"id": "usb", "type": "posix", "path": "/mnt/b"}
	for _, l := range []later{
		{"PUT", "/api/v1/security/ssh", map[string]any{"enabled": false, "authorized_keys": []any{}}},
		{"PUT", "/api/v1/network/wifi", map[string]any{"ssid": "x", "psk": "a wifi passphrase", "failover": true}},
		{"PUT", "/api/v1/tls/public", map[string]any{"enabled": false, "clinic_id": nil}},
		{"PUT", "/api/v1/tls/tailscale", map[string]any{"enabled": false, "auth_key": nil}},
		{"POST", "/api/v1/tls/local-ca/rotate", nil},
		{"GET", "/api/v1/backups", nil},
		{"POST", "/api/v1/backups/run", map[string]any{"destination": nil}},
		{"POST", "/api/v1/backups/destinations/test", map[string]any{"destination": dest, "credentials": nil}},
		{"GET", "/api/v1/backups/recovery-key", nil},
		{"GET", "/api/v1/restore/points", nil},
		{"POST", "/api/v1/restore", map[string]any{"kind": "pitr"}},
		{"GET", "/api/v1/updates", nil},
		{"POST", "/api/v1/updates/check", nil},
		{"POST", "/api/v1/updates/apply", map[string]any{"version": nil}},
		{"POST", "/api/v1/updates/rollback", nil},
		{"GET", "/api/v1/plugs", nil},
		{"PUT", "/api/v1/plugs/care_scribe", map[string]any{"enabled": false}},
	} {
		r := e.do(l.method, l.url, l.body)
		e.want(r, 501, "not-implemented-in-this-version")
		if r.json(t)["phase"] == nil {
			t.Fatalf("%s: no phase", l.url)
		}
	}
}

// TestJobsLogsAndBundle: a support bundle job with SSE, cancel, logs, and job-running.
func TestJobsLogsAndBundle(t *testing.T) {
	e := newEnv(t)
	e.claim()
	r := e.do("POST", "/api/v1/support-bundle", map[string]any{"log_lines": 100, "since_hours": 1})
	e.want(r, 202, "")
	id := r.json(t)["id"].(string)
	r = e.do("GET", "/api/v1/jobs/"+id+"/events", nil, header("Accept", "text/event-stream"))
	e.want(r, 200, "")
	j := e.waitJob(id)
	var res jobs.SupportBundleResult
	must(t, json.Unmarshal(j.Result, &res))
	r = e.do("GET", res.DownloadPath, nil)
	e.want(r, 200, "")
	if len(r.body) != int(res.SizeBytes) || !strings.Contains(r.header.Get("Content-Disposition"), res.Filename) {
		t.Fatalf("bundle %d bytes, %v", len(r.body), r.header)
	}
	e.want(e.do("GET", "/api/v1/support-bundle/20991231T000000.000Z-support-bundle", nil), 404, "not-found")
	e.want(e.do("POST", "/api/v1/support-bundle", map[string]any{"log_lines": 5}, invalid), 422, "invalid-request")

	// A cancellable job that waits: cancel it.
	e.svc.Jobs.CancelPoll = 10 * time.Millisecond
	started := make(chan struct{})
	jj, err := e.svc.Jobs.Start(t.Context(), jobs.Spec{Kind: jobs.KindSupportBundle, Source: jobs.SourceConsole, Cancellable: true},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		})
	must(t, err)
	<-started
	e.want(e.do("POST", "/api/v1/jobs/"+jj.ID+"/cancel", nil), 202, "")
	if j := e.waitJob(jj.ID); j.State != jobs.StateCancelled {
		t.Fatalf("cancelled job %s", j.State)
	}

	// Another process holds the apply lock: job-running with job_id null.
	release, err := e.svc.Jobs.AcquireLock(t.Context())
	must(t, err)
	r = e.do("GET", "/api/v1/apply/plan", nil)
	e.want(r, 409, "job-running")
	if m := r.json(t); m["job_id"] != nil {
		t.Fatalf("job_id %v", m["job_id"])
	}
	e.want(e.do("POST", "/api/v1/power/reboot", nil), 409, "job-running")
	release()

	// Logs.
	line := `{"__CURSOR":"s=8c1f;i=1a2b;b=77e0;m=11;t=62a1;x=4f","__REALTIME_TIMESTAMP":"1793355300000000","PRIORITY":"6","MESSAGE":"Booting worker with pid: 12"}`
	e.fake.On("journalctl --unit care-api.service --output json --no-pager --lines 50", host.Response{Out: line + "\n"})
	e.fake.On("journalctl --unit care-api.service --output json --no-pager --lines 100 --follow", host.Response{Out: line + "\n"})
	e.fake.On("systemctl show -p LoadState care-fe-build.service", host.Response{Out: "LoadState=not-found\n"})
	e.want(e.do("GET", "/api/v1/logs", nil), 200, "")
	r = e.do("GET", "/api/v1/logs/api?n=50", nil)
	e.want(r, 200, "")
	if m := r.json(t); m["next_cursor"] != "s=8c1f;i=1a2b;b=77e0;m=11;t=62a1;x=4f" {
		t.Fatalf("logs %v", m)
	}
	e.want(e.do("GET", "/api/v1/logs/fe-build", nil), 404, "unit-not-found")
	e.want(e.do("GET", "/api/v1/logs/api?n=0", nil, invalid), 422, "invalid-request")

	// Follow: read the first line and a heartbeat, then close.
	req, _ := http.NewRequestWithContext(t.Context(), "GET", e.hs.URL+"/api/v1/logs/api?follow=true", nil)
	req.Host = "care.local:8443"
	req.Header.Set("Accept", "text/event-stream")
	req.AddCookie(&http.Cookie{Name: CookieName, Value: e.cookie})
	res2, err := http.DefaultClient.Do(req)
	must(t, err)
	buf := make([]byte, 0, 4096)
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(string(buf), "event: heartbeat") && time.Now().Before(deadline) {
		chunk := make([]byte, 1024)
		n, err := res2.Body.Read(chunk)
		buf = append(buf, chunk[:n]...)
		if err != nil {
			break
		}
	}
	_ = res2.Body.Close()
	contracttest.Check(t, coverage, contracttest.Exchange{Method: "GET", URL: "/api/v1/logs/api?follow=true",
		Header: req.Header, Status: res2.StatusCode, RHeader: res2.Header, RBody: buf})
	if !strings.Contains(string(buf), "id: s=8c1f") || !strings.Contains(string(buf), "event: heartbeat") {
		t.Fatalf("follow stream %q", buf)
	}

	// Four streams for each session at most.
	e.srv.mu.Lock()
	e.srv.bySess[mustSession(t, e).Hash] = 4
	e.srv.mu.Unlock()
	e.want(e.do("GET", "/api/v1/jobs/"+id+"/events", nil, header("Accept", "text/event-stream")), 429, "rate-limited")
}

func mustSession(t *testing.T, e *env) *auth.Session {
	t.Helper()
	s, ok := e.svc.Auth.Authenticate(e.cookie)
	if !ok {
		t.Fatal("no session")
	}
	return s
}

// TestSPA: the "not built" page, the SPA fallback, and the long cache of /assets/*.
func TestSPA(t *testing.T) {
	e := newEnv(t)
	r, err := http.Get(e.hs.URL + "/")
	must(t, err)
	_ = r.Body.Close()
	if r.StatusCode != 503 {
		t.Fatalf("not built: %d", r.StatusCode)
	}
	e.srv.o.Console = testFS
	for path, want := range map[string]string{"/": "no-store", "/setup/identity": "no-store", "/assets/app.js": "public, max-age=31536000, immutable"} {
		r, err := http.Get(e.hs.URL + path)
		must(t, err)
		_ = r.Body.Close()
		if r.StatusCode != 200 || r.Header.Get("Cache-Control") != want {
			t.Fatalf("%s: %d %q", path, r.StatusCode, r.Header.Get("Cache-Control"))
		}
	}
	r, err = http.Get(e.hs.URL + "/assets/missing.js")
	must(t, err)
	_ = r.Body.Close()
	if r.StatusCode != 404 {
		t.Fatalf("missing asset: %d", r.StatusCode)
	}
}

var testFS = fstest.MapFS{
	"index.html":    {Data: []byte("<!doctype html><title>console</title>")},
	"assets/app.js": {Data: []byte("console.log(1)")},
	"favicon.svg":   {Data: []byte("<svg/>")},
}

func totp(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	c, err := auth.TOTPCode(secret, at)
	must(t, err)
	return c
}

var _ = paths.ConsolePort
