package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/server/contracttest"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
)

// secretOf returns a secret value of the device: the redaction must remove it from each export.
func secretOf(t *testing.T, e *env) string {
	t.Helper()
	ents, err := os.ReadDir(e.p.Abs(paths.SecretsDir))
	must(t, err)
	for _, ent := range ents {
		if ent.IsDir() || ent.Name() == "agent.json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(e.p.Abs(paths.SecretsDir), ent.Name()))
		must(t, err)
		if v := strings.TrimSpace(strings.Split(string(data), "\n")[0]); len(v) >= 12 {
			return v
		}
	}
	t.Fatal("no secret value")
	return ""
}

// privateTexts are in the job of failingJob, and never in a public response.
var privateTexts = []string{"a private reason", "a private detail", "private step detail", "hunter2", "abc.def.ghi", "| line 1", "a progress message"}

// failingJob runs an apply job with two steps, log lines (one with a token, a password, and a secret value of the
// device), a progress event, a reported problem, and a failure with a detail.
func failingJob(t *testing.T, e *env, logs int) string {
	t.Helper()
	secret := secretOf(t, e)
	j, err := e.svc.Jobs.Start(t.Context(), jobs.Spec{Kind: jobs.KindApply, Source: jobs.SourceConsole, Reason: "a private reason"},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			end := rec.StepAction("write-files: 2 file(s)", "write-files")
			for i := range logs {
				rec.Logf("line %d", i+1)
			}
			rec.Logf("curl -H 'Authorization: Bearer abc.def.ghi' password=hunter2 key %s", secret)
			rec.Progress(1, nil, "items", "a progress message")
			end(nil)
			end = rec.StepAction("restart: care-api.service", "restart")
			rec.Report(problem.New(problem.Internal, "a private detail of the report"))
			end(errors.New("care-api.service did not become healthy: private step detail"))
			return nil, problem.New(problem.Internal, "a private detail of the job")
		})
	must(t, err)
	if j := e.waitJob(j.ID); j.State != jobs.StateFailed {
		t.Fatalf("job %s", j.State)
	}
	return j.ID
}

// TestPublicJobEvents: GET /_care-pi/jobs/{job_id}/events needs no session and serves only the steps, the title of a
// problem, and the public job: no log or progress event, no detail, no reason, no result (S67).
func TestPublicJobEvents(t *testing.T) {
	e := newEnv(t)
	id := failingJob(t, e, 3)
	secret := secretOf(t, e)
	noPrivate := func(r *resp) {
		t.Helper()
		for _, p := range append(privateTexts, secret) {
			if strings.Contains(string(r.body), p) {
				t.Errorf("%s: the public response has %q:\n%s", r.op, p, r.body)
			}
		}
	}

	// The JSON form.
	r := e.do("GET", "/_care-pi/jobs/"+id+"/events", nil)
	e.want(r, 200, "")
	noPrivate(r)
	page := r.json(t)
	if page["done"] != true || page["job_id"] != id {
		t.Fatalf("page %v", page)
	}
	kinds := map[string]int{}
	var done map[string]any
	for _, ev := range page["events"].([]any) {
		m := ev.(map[string]any)
		kinds[m["event"].(string)]++
		if m["event"] == "done" {
			done = m["data"].(map[string]any)
		}
	}
	if kinds["log"] != 0 || kinds["progress"] != 0 || kinds["step"] != 4 || kinds["error"] != 1 || kinds["done"] != 1 {
		t.Fatalf("public events %v", kinds)
	}
	if done["state"] != "failed" || done["error"].(map[string]any)["title"] != "Internal error" || done["result"] != nil || done["reason"] != nil {
		t.Fatalf("public done %v", done)
	}
	full, err := jobs.Load(e.p, id)
	must(t, err)
	if page["last_event_id"] != float64(full.LastEventID) {
		t.Fatalf("last_event_id %v, the job has %d", page["last_event_id"], full.LastEventID)
	}
	// A page after the last event: nothing more.
	r = e.do("GET", "/_care-pi/jobs/"+id+"/events?after=999", nil)
	e.want(r, 200, "")
	if m := r.json(t); len(m["events"].([]any)) != 0 || m["done"] != false {
		t.Fatalf("page after the end %v", m)
	}

	// SSE, from the start and after a Last-Event-ID. Port 8443 has the same path.
	r = e.do("GET", "/_care-pi/jobs/"+id+"/events", nil, header("Accept", "text/event-stream"))
	e.want(r, 200, "")
	noPrivate(r)
	frames := contracttest.Frames(r.body)
	if len(frames) != 6 || frames[0].ID != "1" || frames[len(frames)-1].Event != "done" {
		t.Fatalf("frames %+v", frames)
	}
	r = e.do("GET", "/_care-pi/jobs/"+id+"/events", nil, header("Accept", "text/event-stream"), header("Last-Event-ID", frames[2].ID))
	e.want(r, 200, "")
	if f := contracttest.Frames(r.body); len(f) != 3 || f[0].ID != frames[3].ID {
		t.Fatalf("resume after %s: %+v", frames[2].ID, f)
	}

	// Problems: an unknown job, a bad id, a bad after.
	e.want(e.do("GET", "/_care-pi/jobs/20991231T000000.000Z-apply/events", nil), 404, "job-not-found")
	e.want(e.do("GET", "/_care-pi/jobs/../events", nil, invalid), 404, "")
	e.want(e.do("GET", "/_care-pi/jobs/not-a-job/events", nil, invalid), 404, "job-not-found")
	e.want(e.do("GET", "/_care-pi/jobs/"+id+"/events?after=-1", nil, invalid), 422, "invalid-request")
	e.want(e.do("GET", "/_care-pi/jobs/"+id+"/events", nil, header("Last-Event-ID", "x"), invalid), 422, "invalid-request")
	// The private form still needs a session, and the export is not public.
	e.want(e.do("GET", "/api/v1/jobs/"+id+"/events", nil), 401, "not-authenticated")
	for _, u := range []string{"/_care-pi/jobs/" + id + "/export", "/_care-pi/jobs/" + id, "/_care-pi/jobs", "/api/v1/public/jobs/" + id + "/events"} {
		res, err := http.Get(e.hs.URL + u)
		must(t, err)
		_ = res.Body.Close()
		if res.StatusCode != 404 && res.StatusCode != 401 {
			t.Fatalf("%s: %d", u, res.StatusCode)
		}
	}
}

// TestPublicJobEventsLimits: 60 requests each minute for each address, separate from the public status; 4 open
// streams for each address and 16 in total, separate from the streams of the sessions.
func TestPublicJobEventsLimits(t *testing.T) {
	e := newEnv(t)
	id := failingJob(t, e, 1)
	e.addr = "192.168.1.77"
	for range 60 {
		e.want(e.do("GET", "/_care-pi/jobs/"+id+"/events", nil), 200, "")
	}
	r := e.do("GET", "/_care-pi/jobs/"+id+"/events", nil)
	e.want(r, 429, "rate-limited")
	if r.header.Get("Retry-After") == "" || !strings.Contains(string(r.body), "public job events") {
		t.Fatalf("429 %v %s", r.header, r.body)
	}
	// The public status has its own bucket, and another address has its own limit.
	e.want(e.do("GET", "/_care-pi/status", nil), 200, "")
	e.addr = "192.168.1.78"
	e.want(e.do("GET", "/_care-pi/jobs/"+id+"/events", nil), 200, "")

	sse := header("Accept", "text/event-stream")
	e.srv.mu.Lock()
	e.srv.byAddr[e.addr] = 4
	e.srv.mu.Unlock()
	r = e.do("GET", "/_care-pi/jobs/"+id+"/events", nil, sse)
	e.want(r, 429, "rate-limited")
	if !strings.Contains(string(r.body), "4 open public job streams for each address and 16 in total") {
		t.Fatalf("429 %s", r.body)
	}
	e.addr = "192.168.1.79"
	e.want(e.do("GET", "/_care-pi/jobs/"+id+"/events", nil, sse), 200, "")
	e.srv.mu.Lock()
	e.srv.pubStreams = 16
	e.srv.mu.Unlock()
	e.want(e.do("GET", "/_care-pi/jobs/"+id+"/events", nil, sse), 429, "rate-limited")
	// The sessions keep their stream slots.
	e.claim()
	e.want(e.do("GET", "/api/v1/jobs/"+id+"/events", nil, sse), 200, "")
	e.srv.mu.Lock()
	e.srv.pubStreams = 0
	delete(e.srv.byAddr, "192.168.1.78")
	e.srv.mu.Unlock()
}

// TestPublicJobLive: the setup finish job on a second device without a session (S67). GET /_care-pi/status names the
// job (job, setup_job_id, jobs_recent) and the public stream follows it until done, without its log lines.
func TestPublicJobLive(t *testing.T) {
	e := newEnv(t)
	e.claim()
	for range 6 {
		failingJob(t, e, 0)
	}
	e.clock.add(time.Second)
	go1, atFE := make(chan struct{}), make(chan struct{})
	j, err := e.svc.Jobs.Start(t.Context(), jobs.Spec{Kind: jobs.KindSetup, Source: jobs.SourceConsole, Reason: "setup finish", Lock: true,
		OnStart: func(id string) {
			_, err := e.svc.Setup.Update(func(r *setup.Record) error { r.JobID = id; return nil })
			must(t, err)
		}}, func(ctx context.Context, rec *jobs.Recorder) (any, error) {
		end := rec.StepAction("validate: config.yaml", "validate")
		end(nil)
		end = rec.StepAction("ensure-fe: build 8d2f0c1a9b7e", "ensure-fe")
		close(atFE)
		<-go1
		for i := range 50 {
			rec.Log("stdout", "vite build "+strings.Repeat("x", i))
		}
		rec.Progress(50, nil, "percent", "vite")
		end(nil)
		end = rec.StepAction("wait-healthy: care-api.service", "wait-healthy")
		end(nil)
		return nil, nil
	})
	must(t, err)
	id := j.ID
	<-atFE
	cookie := e.cookie
	e.cookie = ""
	e.clock.add(3 * time.Second)
	r := e.do("GET", "/_care-pi/status", nil)
	e.want(r, 200, "")
	pub := r.json(t)
	job := pub["job"].(map[string]any)
	recent := pub["jobs_recent"].([]any)
	if pub["setup_state"] != "applying" || pub["setup_job_id"] != id || job["id"] != id || job["state"] != "running" ||
		job["title"] != "Set up the device" || job["kind"] != "setup" || job["step"] != "ensure-fe: build 8d2f0c1a9b7e" ||
		job["step_index"] != float64(1) || job["step_count"] != float64(2) {
		t.Fatalf("public status during the finish: %s", r.body)
	}
	if len(recent) != 5 || recent[0].(map[string]any)["id"] != id || recent[0].(map[string]any)["state"] != "running" ||
		recent[1].(map[string]any)["state"] != "failed" || recent[0].(map[string]any)["finished_at"] != nil {
		t.Fatalf("jobs_recent %v", recent)
	}

	// The second device follows the job: the stream gets the steps that ran, then each new step, then done.
	req, err := http.NewRequestWithContext(t.Context(), "GET", e.hs.URL+"/_care-pi/jobs/"+id+"/events", nil)
	must(t, err)
	req.Host = "care.local"
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-Forwarded-For", "192.168.1.90")
	var body []byte
	var res *http.Response
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		res, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = res.Body.Close() }()
		buf := make([]byte, 4096)
		for {
			n, err := res.Body.Read(buf)
			body = append(body, buf[:n]...)
			if err != nil {
				return
			}
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.srv.mu.Lock()
		open := e.srv.pubStreams
		e.srv.mu.Unlock()
		if open == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(go1)
	if j := e.waitJob(id); j.State != jobs.StateSucceeded {
		t.Fatalf("setup job %s", j.State)
	}
	wg.Wait()
	if res == nil {
		t.Fatal("no response")
	}
	contracttest.Check(t, coverage, contracttest.Exchange{Method: "GET", URL: "/_care-pi/jobs/" + id + "/events",
		Header: req.Header, Status: res.StatusCode, RHeader: res.Header, RBody: body})
	frames := contracttest.Frames(body)
	var steps []string
	for _, f := range frames {
		if f.Event == "step" {
			steps = append(steps, f.ID)
		}
		if strings.Contains(f.Data, "vite") {
			t.Fatalf("the public stream has a log or progress line: %+v", f)
		}
	}
	if len(steps) != 6 || frames[len(frames)-1].Event != "done" || !strings.Contains(frames[len(frames)-1].Data, `"state":"succeeded"`) {
		t.Fatalf("live frames %+v", frames)
	}
	e.srv.mu.Lock()
	open := e.srv.pubStreams
	e.srv.mu.Unlock()
	if open != 0 {
		t.Fatalf("%d public streams stay open", open)
	}

	// After the finish: no job and no setup job.
	e.clock.add(3 * time.Second)
	r = e.do("GET", "/_care-pi/status", nil)
	pub = r.json(t)
	if pub["job"] != nil || pub["setup_job_id"] != nil || pub["jobs_recent"].([]any)[0].(map[string]any)["state"] != "succeeded" {
		t.Fatalf("public status after the finish: %s", r.body)
	}
	e.cookie = cookie
}

// TestJobsView: the Jobs page of the console (S67) and the plain-text exports (S68): the state groups of GET /jobs,
// log_tail of GET /jobs/{job_id} (200 lines at most, without secrets), and GET /jobs/{job_id}/export.
func TestJobsView(t *testing.T) {
	e := newEnv(t)
	e.claim()
	id := failingJob(t, e, 250)
	secret := secretOf(t, e)
	running := make(chan struct{})
	rj, err := e.svc.Jobs.Start(t.Context(), jobs.Spec{Kind: jobs.KindSupportBundle, Source: jobs.SourceConsole, Cancellable: true},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			<-running
			return nil, nil
		})
	must(t, err)

	ids := func(r *resp) []string {
		var out []string
		for _, it := range r.json(t)["items"].([]any) {
			out = append(out, it.(map[string]any)["id"].(string))
		}
		return out
	}
	r := e.do("GET", "/api/v1/jobs?state=active", nil)
	e.want(r, 200, "")
	if got := ids(r); len(got) != 1 || got[0] != rj.ID {
		t.Fatalf("active %v", got)
	}
	r = e.do("GET", "/api/v1/jobs?state=recent", nil)
	e.want(r, 200, "")
	if got := ids(r); len(got) != 1 || got[0] != id {
		t.Fatalf("recent %v", got)
	}
	e.want(e.do("GET", "/api/v1/jobs?state=running", nil), 200, "")
	e.want(e.do("GET", "/api/v1/jobs?state=done", nil, invalid), 422, "invalid-request")
	close(running)
	e.waitJob(rj.ID)

	// log_tail: the last 200 lines up to last_event_id, without secrets.
	r = e.do("GET", "/api/v1/jobs/"+id, nil)
	e.want(r, 200, "")
	m := r.json(t)
	tail := m["log_tail"].(map[string]any)
	lines := tail["lines"].([]any)
	if len(lines) != jobs.MaxLogTail || tail["total"] != float64(251) || lines[0].(map[string]any)["line"] != "line 52" {
		t.Fatalf("log_tail: %d lines, total %v, first %v", len(lines), tail["total"], lines[0])
	}
	last := lines[len(lines)-1].(map[string]any)["line"].(string)
	if strings.Contains(last, secret) || strings.Contains(last, "hunter2") || strings.Contains(last, "abc.def.ghi") ||
		!strings.Contains(last, "Bearer <redacted>") || !strings.Contains(last, "password=<redacted>") {
		t.Fatalf("the tail has a secret: %q", last)
	}
	// A job without log lines has an empty tail; a job event (done) has no log_tail.
	r = e.do("GET", "/api/v1/jobs/"+rj.ID, nil)
	e.want(r, 200, "")
	if tail := r.json(t)["log_tail"].(map[string]any); tail["total"] != float64(0) || len(tail["lines"].([]any)) != 0 {
		t.Fatalf("empty tail %v", tail)
	}
	r = e.do("GET", "/api/v1/jobs/"+id+"/events?after=0", nil)
	if strings.Contains(string(r.body), "log_tail") {
		t.Fatal("the done event has log_tail")
	}

	// The export: the header, the steps with their times, each log line, the problem, and no secret.
	r = e.do("GET", "/api/v1/jobs/"+id+"/export", nil)
	e.want(r, 200, "")
	text := string(r.body)
	if ct := r.header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Fatalf("content type %q", ct)
	}
	for _, want := range []string{
		"CARE support text: job " + id + "\n", "Device: Sunrise Clinic (care.local)\n", "Agent: ", "\nLock: none", "\nTime: ",
		"Kind: apply (Apply the settings)\n", "State: failed\n", "Reason: a private reason\n",
		"  1. [succeeded] Write the configuration files (write-files: 2 file(s))\n",
		"  2. [failed] Start the changed services again (restart: care-api.service)\n",
		"     care-api.service did not become healthy: private step detail\n",
		"Log (251 lines; time, stream, step | line):\n", " agent 1 | line 1\n", " agent 1 | line 250\n",
		"problem: Internal error: a private detail of the report\n",
		"Problem:\n  Type: https://care-pi.rithviknishad.dev/problems/internal\n  Title: Internal error\n  Status: 500\n  Detail: a private detail of the job\n",
		"Bearer <redacted>", "password=<redacted>",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the export has no %q", want)
		}
	}
	for _, bad := range []string{secret, "hunter2", "abc.def.ghi", "a progress message"} {
		if strings.Contains(text, bad) {
			t.Errorf("the export has %q", bad)
		}
	}
	e.want(e.do("GET", "/api/v1/jobs/20991231T000000.000Z-apply/export", nil), 404, "job-not-found")
	cookie := e.cookie
	e.cookie = ""
	e.want(e.do("GET", "/api/v1/jobs/"+id+"/export", nil), 401, "not-authenticated")
	e.cookie = cookie
}

// TestTextExports: GET /logs/{unit}/export and GET /doctor/export give plain text with the header and without
// secrets (S68).
func TestTextExports(t *testing.T) {
	e, _ := readyConnEnv(t)
	line := `{"__CURSOR":"s=8c1f;i=1a2b;b=77e0;m=11;t=62a1;x=4f","__REALTIME_TIMESTAMP":"1793355300000000","PRIORITY":"3","MESSAGE":"login with token=abcdef123456 failed"}`
	e.fake.On("journalctl --unit care-api.service --output json --no-pager --lines 100", host.Response{Out: line + "\n"})
	e.fake.On("journalctl --unit care-api.service --output json --no-pager --lines 20", host.Response{Out: line + "\n"})
	r := e.do("GET", "/api/v1/logs/api/export", nil)
	e.want(r, 200, "")
	text := string(r.body)
	if ct := r.header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Fatalf("content type %q", ct)
	}
	for _, want := range []string{"CARE support text: the journal of care-api.service\n", "Device: Sunrise Clinic (care.local)\n",
		"Lock: no solution lock (solution care-clinic, track stable, source solution-refs, generated 2026-10-30T00:00:00Z)\n",
		"Unit: care-api.service (api), 1 lines", " err care-api.service: login with token=<redacted> failed\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("the log text has no %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "abcdef123456") {
		t.Fatal("the log text has the token")
	}
	e.want(e.do("GET", "/api/v1/logs/api/export?n=20", nil), 200, "")
	e.want(e.do("GET", "/api/v1/logs/api/export?n=0", nil, invalid), 422, "invalid-request")
	e.want(e.do("GET", "/api/v1/logs/api/export?since=yesterday", nil, invalid), 422, "invalid-request")
	e.fake.On("systemctl show -p LoadState care-fe-build.service", host.Response{Out: "LoadState=not-found\n"})
	e.want(e.do("GET", "/api/v1/logs/fe-build/export", nil), 404, "unit-not-found")

	e.fake.On("ss -ltnpH", host.Response{Out: "LISTEN 0 4096 0.0.0.0:8443 0.0.0.0:* users:((\"nginx\",pid=2,fd=6))\n"})
	r = e.do("GET", "/api/v1/doctor/export", nil)
	e.want(r, 200, "")
	text = string(r.body)
	for _, want := range []string{"CARE support text: care-pi doctor\n", "\nChecked: ", "STATUS  CHECK", "FAIL", "nginx on 0.0.0.0:8443", "\nstatus: fail\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("the doctor text has no %q:\n%s", want, text)
		}
	}
	cookie := e.cookie
	e.cookie = ""
	e.want(e.do("GET", "/api/v1/doctor/export", nil), 401, "not-authenticated")
	e.want(e.do("GET", "/api/v1/logs/api/export", nil), 401, "not-authenticated")
	e.cookie = cookie
}
