package update

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/secrets"
)

// TestIndexClientHTTPS: ETag and If-None-Match, a 404, and the size limit.
func TestIndexClientHTTPS(t *testing.T) {
	e := newTEnv(t, "stable", "")
	body, err := os.ReadFile(e.fx.Path(e.fx.IndexURL))
	if err != nil {
		t.Fatal(err)
	}
	var hits, notModified int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Path {
		case "/index.json":
			if r.Header.Get("User-Agent") == "" || !strings.HasPrefix(r.Header.Get("User-Agent"), "care-pi/") {
				t.Errorf("user agent %q", r.Header.Get("User-Agent"))
			}
			if r.Header.Get("If-None-Match") == `"v1"` {
				notModified++
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", `"v1"`)
			_, _ = w.Write(body)
		case "/big.json":
			_, _ = w.Write([]byte(strings.Repeat(" ", MaxIndexBytes+10)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := Client{Paths: e.p, HTTP: &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}} //nolint:gosec // a test server
	if _, err := c.FetchIndex(context.Background(), srv.URL+"/index.json"); err != nil {
		t.Fatal(err)
	}
	ix, err := c.FetchIndex(context.Background(), srv.URL+"/index.json")
	if err != nil || ix == nil || notModified != 1 {
		t.Fatalf("second fetch: %v, 304s %d", err, notModified)
	}
	if _, err := c.FetchIndex(context.Background(), srv.URL+"/missing.json"); !problem.Is(err, problem.IndexUnavailable) || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("404: %v", err)
	}
	if _, err := c.FetchIndex(context.Background(), srv.URL+"/big.json"); !problem.Is(err, problem.IndexUnavailable) {
		t.Fatalf("size limit: %v", err)
	}
	if url, at := IndexFetchedAt(e.p); url != srv.URL+"/index.json" || at.IsZero() {
		t.Fatalf("meta %s %s", url, at)
	}
}

// TestPlugPlans: the path of each kind of plug change (docs/solutions.md, section 12.1).
func TestPlugPlans(t *testing.T) {
	e := newTEnv(t, "pinned", "2026.11.0")
	e.install()
	cfg := mustConfig(t, e.cfg)
	on, off := true, false
	cases := []struct {
		id     string
		change PlugChange
		path   string
		err    string
	}{
		{"care_fasterappointments_fe", PlugChange{SetEnabled: true, Enabled: &on}, PathPlugConfig, ""},
		{"care_onboarding_fe", PlugChange{Frontend: map[string]any{"config.redirect_after_login": false}}, PathPlugConfig, ""},
		{"care_onboarding_fe", PlugChange{SetEnabled: true, Enabled: &off}, PathPlugConfig, ""},
		{"care_multihost_files_be", PlugChange{SetEnabled: true, Enabled: &on}, PathCutover, ""},
		{"care_filly", PlugChange{SetEnabled: true, Enabled: &on}, "", problem.PlugSettingsInvalid},
		{"care_filly", PlugChange{SetEnabled: true, Enabled: &on, Backend: map[string]any{"MEDISPEAK_API_KEY": "msk_live_x", "MEDISPEAK_BASE_URL": "https://m.example/api/v2"}}, PathCutover, ""},
		{"care_filly", PlugChange{Backend: map[string]any{"MEDISPEAK_BASE_URL": "https://m.example/api/v2"}}, PathNone, ""},
		{"care_filly", PlugChange{Backend: map[string]any{"NOPE": "x"}}, "", problem.PlugSettingsInvalid},
		{"care_scribe", PlugChange{SetEnabled: true, Enabled: &on}, "", problem.PlugUnknown},
		{"care_onboarding_fe", PlugChange{SetEnabled: true}, PathNone, ""},
		// A setting of a plug that is off: path none (no runtime effect; the value counts when the plug turns on).
		{"care_fasterappointments_fe", PlugChange{Frontend: map[string]any{"config.auto_create_invoice": true}}, PathNone, ""},
		{"care_filly", PlugChange{Backend: map[string]any{"MEDISPEAK_API_KEY": "msk_live_off"}}, PathNone, ""},
		{"care_multihost_files_be", PlugChange{Backend: map[string]any{"ALLOWED_HOSTS": []any{"*.lan"}}}, PathNone, ""},
	}
	for _, c := range cases {
		out, err := e.u.PlanChange(context.Background(), cfg, c.id, c.change)
		if c.err != "" {
			if !problem.Is(err, c.err) {
				t.Errorf("%s %+v: want %s, got %v", c.id, c.change, c.err, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.id, err)
			continue
		}
		if out.Plan.Path != c.path {
			t.Errorf("%s %+v: path %s, want %s", c.id, c.change, out.Plan.Path, c.path)
		}
		if c.path == PathNone && (out.Plan.Changed || out.Plan.Maintenance || out.Plan.Checkpoint || len(out.Plan.Restarts) > 0 || len(out.Plan.Steps) > 0) {
			t.Errorf("%s %+v: plan none has an effect: %+v", c.id, c.change, out.Plan)
		}
		if c.path == PathCutover && (!out.Plan.Checkpoint || !out.Plan.Maintenance || out.Plan.Steps[len(out.Plan.Steps)-1] != "maintenance-off") {
			t.Errorf("%s: cutover plan %+v", c.id, out.Plan)
		}
	}
	// A backend plug that is on and gets a new setting restarts api, worker, and beat.
	cfg.Solution.Plugs = map[string]config.PlugChoice{"care_multihost_files_be": {Enabled: &on}}
	out, err := e.u.PlanChange(context.Background(), cfg, "care_multihost_files_be", PlugChange{Backend: map[string]any{"ALLOWED_HOSTS": []any{"*.lan"}}})
	if err != nil || out.Plan.Path != PathRestart || len(out.Plan.Restarts) != 3 {
		t.Fatalf("restart plan %+v %v", out, err)
	}
	// Secrets go to the secret file, not to the choice.
	out, err = e.u.PlanChange(context.Background(), cfg, "care_filly", PlugChange{Backend: map[string]any{"MEDISPEAK_API_KEY": "msk_live_y"}})
	if err != nil || !out.SecretsSet || out.Secrets["MEDISPEAK_API_KEY"] != "msk_live_y" || out.Choice.Settings != nil {
		t.Fatalf("secret %+v %v", out, err)
	}
}

// TestPlugChangeRestart: a backend setting renders ADDITIONAL_PLUGS with the secret into the podman
// secret, keeps the secret out of the rendered files, and restarts beat, worker, and api.
func TestPlugChangeRestart(t *testing.T) {
	e := newTEnv(t, "pinned", "2026.11.0")
	e.install()
	on := true
	data, _ := os.ReadFile(e.cfg)
	cfgText := strings.Replace(string(data), "  pin: \"2026.11.0\"\n", "  pin: \"2026.11.0\"\n  plugs:\n    care_filly:\n      enabled: true\n      settings:\n        backend: {MEDISPEAK_BASE_URL: \"https://m.example/api/v2\"}\n", 1)
	if err := os.WriteFile(e.cfg, []byte(cfgText), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (secrets.Store{Paths: e.p}).WritePlug("care_filly", map[string]any{"MEDISPEAK_API_KEY": "msk_live_secret"}); err != nil {
		t.Fatal(err)
	}
	_ = on
	// care_filly on is a cutover (migrations of the plug); then a setting change is a restart.
	rec := e.job(jobs.KindPlugChange)
	if _, err := e.u.RunPlugChange(context.Background(), rec, PlugChangeOptions{ID: "care_filly", Path: PathCutover}); err != nil {
		t.Fatal(err)
	}
	if l := e.running(); !l.Components.Plugs["care_filly"].Enabled || l.Plugs["care_filly"].SHA == "" {
		t.Fatalf("lock after the cutover %+v", l.Components.Plugs["care_filly"])
	}
	rendered, _ := os.ReadFile(e.p.Abs(paths.RenderedCare + "/additional-plugs.json"))
	if strings.Contains(string(rendered), "msk_live_secret") || !strings.Contains(string(rendered), `"<secret>"`) {
		t.Fatalf("additional-plugs.json %s", rendered)
	}
	cfgText = strings.Replace(cfgText, "https://m.example/api/v2", "https://m2.example/api/v2", 1)
	if err := os.WriteFile(e.cfg, []byte(cfgText), 0o600); err != nil {
		t.Fatal(err)
	}
	e.fake.Calls = nil
	rec = e.job(jobs.KindPlugChange)
	if _, err := e.u.RunPlugChange(context.Background(), rec, PlugChangeOptions{ID: "care_filly", Path: PathRestart}); err != nil {
		t.Fatal(err)
	}
	if got := actions(rec); !reflect.DeepEqual(got, []string{"plan", "activate", "restart", "plug-config"}) {
		t.Fatalf("restart steps %v", got)
	}
	if got := e.fake.Filter("podman secret create --replace care-additional-plugs", "systemctl restart"); !reflect.DeepEqual(got,
		[]string{"podman secret create --replace care-additional-plugs -", "systemctl restart care-beat.service care-worker.service care-api.service"}) {
		t.Fatalf("restart commands %q", got)
	}
	for i, c := range e.fake.Calls {
		if strings.Contains(c, "msk_live") {
			t.Fatalf("a secret is in a command line: %s (stdin %d)", c, e.fake.StdinLen[i])
		}
	}
}

// TestOSUpdateCommands: the steps and the apt commands of an os-update job; os-hold runs also after a failure.
func TestOSUpdateCommands(t *testing.T) {
	e := newTEnv(t, "stable", "")
	if err := os.MkdirAll(e.p.Abs("/etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.p.Abs("/etc/os-release"), []byte("ID=debian\nVERSION_CODENAME=trixie\nPRETTY_NAME=\"Debian GNU/Linux 13 (trixie)\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := e.job(jobs.KindUpdateCheck)
	if _, err := e.u.Check(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	e.fake.On("dpkg-query -W -f=${Package}\t${Version}\n podman crun netavark aardvark-dns conmon skopeo",
		host.Response{Out: "podman\t5.4.2+ds1-1\ncrun\t1.21-1\n"})
	e.fake.On("apt-cache policy podman crun netavark aardvark-dns conmon skopeo", host.Response{Out: "podman:\n  Installed: 5.4.2+ds1-1\n  Candidate: 5.4.2+ds1-2\n  Version table:\n     5.4.2+ds1-2 500\n        500 http://deb.debian.org/debian trixie/main arm64 Packages\n *** 5.4.2+ds1-1 100\n        100 /var/lib/dpkg/status\ncrun:\n  Installed: 1.21-1\n  Candidate: 1.21-1\n  Version table:\n *** 1.21-1 500\n"})
	// Cmd.String trims the trailing newline of the format.
	e.fake.On("dpkg-query -W -f=${Package}\t${Version}", host.Response{Out: "libc6\t2.41-12\npodman\t5.4.2+ds1-1\n"}, host.Response{Out: "libc6\t2.41-12+deb13u1\npodman\t5.4.2+ds1-2\n"})
	e.fake.On("systemctl show -p ActiveState care.target", host.Response{Out: "ActiveState=active\n"})
	o, err := e.u.OS(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Held) != 2 || o.Held[0].Action != "upgrade" || *o.Held[0].Tested != "5.4.2+ds1-2" || o.Held[1].Action != "keep" || o.Distro.Codename != "trixie" {
		t.Fatalf("os %+v held %+v", o, o.Held)
	}
	e.fake.Calls = nil
	rec = e.job(jobs.KindOSUpdate)
	res, err := e.u.OSUpdate(context.Background(), rec, OSOptions{Source: jobs.SourceCLI})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"os-refresh", "os-unhold", "os-upgrade", "os-hold", "cutover-maintenance-on", "stop", "start", "verify", "maintenance-off"}
	if got := actions(rec); !reflect.DeepEqual(got, want) {
		t.Fatalf("steps %v", got)
	}
	apt := e.fake.Filter("systemd-run")
	if len(apt) != 6 || !strings.Contains(apt[0], "tee /etc/apt/apt.conf.d/52care-pi.conf") || !strings.HasSuffix(apt[1], "apt-get -o DPkg::Lock::Timeout=600 -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold update") ||
		!strings.HasSuffix(apt[2], "apt-mark unhold podman") || !strings.HasSuffix(apt[3], "-y full-upgrade") ||
		!strings.HasSuffix(apt[4], "-y install podman=5.4.2+ds1-2") || !strings.HasSuffix(apt[5], "apt-mark hold podman crun netavark aardvark-dns conmon skopeo") {
		t.Fatalf("apt commands:\n%s", strings.Join(apt, "\n"))
	}
	if !res.CareRestarted || len(res.HeldChanged) != 1 || len(res.Upgraded) != 2 {
		t.Fatalf("result %+v", res)
	}
	// A failed upgrade holds the packages again.
	e.fake.On("systemd-run --wait --collect --pipe --quiet --unit=care-pi-apt -p MemoryMax=512M --setenv=DEBIAN_FRONTEND=noninteractive -- apt-get -o DPkg::Lock::Timeout=600 -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold -y full-upgrade",
		host.Response{Code: 100})
	e.fake.Calls = nil
	rec = e.job(jobs.KindOSUpdate)
	if _, err := e.u.OSUpdate(context.Background(), rec, OSOptions{}); !problem.Is(err, problem.OSUpdateFailed) {
		t.Fatalf("want os-update-failed, got %v", err)
	}
	if got := e.fake.Filter("systemd-run"); !strings.HasSuffix(got[len(got)-1], "apt-mark hold podman crun netavark aardvark-dns conmon skopeo") {
		t.Fatalf("no hold after the failure: %q", got)
	}
}

// TestScheduled: the window run does nothing outside the window, and checks and updates inside it.
func TestScheduled(t *testing.T) {
	e := newTEnv(t, "stable", "")
	e.clock = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC) // Sat
	sp, err := e.u.Scheduled(context.Background(), false)
	if err != nil || sp.WindowOpen || sp.Update {
		t.Fatalf("closed window %+v %v", sp, err)
	}
	if err := e.u.WindowClosed(mustConfig(t, e.cfg)); !problem.Is(err, problem.UpdateWindowClosed) || err.(*problem.Problem).NextWindowAt == nil {
		t.Fatalf("window closed %v", err)
	}
	e.clock = time.Date(2026, 11, 22, 2, 10, 0, 0, time.UTC) // Sun 02:10, the window ends at 05:00
	sp, err = e.u.Scheduled(context.Background(), false)
	if err != nil || !sp.WindowOpen || !sp.Check || !sp.Update || sp.Reboot {
		t.Fatalf("open window %+v %v", sp, err)
	}
	if err := os.MkdirAll(e.p.Abs("/run"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.p.Abs(paths.RebootRequired), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	e.clock = time.Date(2026, 11, 22, 4, 45, 0, 0, time.UTC) // 15 minutes left: no update, but a reboot
	sp, _ = e.u.Scheduled(context.Background(), false)
	if sp.Update || !sp.Reboot {
		t.Fatalf("late in the window %+v", sp)
	}
	if sp, _ = e.u.Scheduled(context.Background(), true); sp.Reboot {
		t.Fatal("no reboot while a job holds the apply lock")
	}
}

// TestRecovery: an interrupted cutover after activate rolls back to the lock on disk with the offer.
func TestRecovery(t *testing.T) {
	e := newTEnv(t, "pinned", "2026.11.0")
	e.install()
	cp := &Checkpoint{TakenAt: e.clock, TargetTime: e.clock.Add(time.Second)}
	if err := UpdateState(e.p, func(s *State) {
		s.Cutover = &CutoverRecord{JobID: "20261122T020300.000Z-update-apply", Kind: jobs.KindUpdateApply, Step: "migrate", Activated: true, Migrated: true, Checkpoint: cp}
	}); err != nil {
		t.Fatal(err)
	}
	r := e.u.PendingRecovery()
	if r == nil || !r.Rollback || r.Offer == nil {
		t.Fatalf("recovery %+v", r)
	}
	rec := e.job(jobs.KindUpdateRollback)
	res, err := e.u.Recover(context.Background(), rec, r)
	if err != nil || res.PitrOffer == nil {
		t.Fatalf("recover %+v %v", res, err)
	}
	if e.u.PendingRecovery() != nil {
		t.Fatal("the recovery stays pending")
	}
	// A recovery undoes the interrupted update: outcome rolled-back, with the offer of that update.
	if h := ReadHistory(e.p); h[0].Kind != HistRollback || h[0].Outcome != OutcomeRolledBack || h[0].PitrOffer == nil || h[0].Problem != nil {
		t.Fatalf("recovery history entry %+v", h[0])
	}
	if l := e.running(); l.Solution.Version != "2026.11.0" {
		t.Fatalf("lock %+v", l.Solution)
	}
	_ = lock.FileVersion
}

// TestMakeLockDist runs N → N+1 → rollback against a dist of solutions/scripts/make-lock.sh (the local index of
// wave 6a). CARE_PI_E2E_INDEX is the file:// URL of its index.json, with the locks 2026.11.0 and 2026.11.1.
func TestMakeLockDist(t *testing.T) {
	url := os.Getenv("CARE_PI_E2E_INDEX")
	if url == "" {
		t.Skip("CARE_PI_E2E_INDEX is not set")
	}
	e := newTEnv(t, "pinned", "2026.11.0")
	e.fx.IndexURL = url
	e.writeConfig("pinned", "2026.11.0")
	e.install()
	e.writeConfig("stable", "")
	rec := e.job(jobs.KindUpdateCheck)
	cr, err := e.u.Check(context.Background(), rec)
	if err != nil || !cr.UpdateAvailable || *cr.TargetVersion != "2026.11.1" {
		t.Fatalf("check %+v %v", cr, err)
	}
	rec = e.job(jobs.KindUpdateApply)
	if _, err := e.u.Apply(context.Background(), rec, ApplyOptions{Now: true, Source: jobs.SourceTimer}); err != nil {
		t.Fatal(err)
	}
	l := e.running()
	if l.Solution.Version != "2026.11.1" || l.History.Previous == "" || !l.Components.Plugs["care_fasterappointments_fe"].Enabled {
		t.Fatalf("after apply %+v", l.Solution)
	}
	if _, err := os.Stat(e.p.Abs(paths.LockHistory + "/" + l.History.Previous)); err != nil {
		t.Fatal(err)
	}
	t.Logf("apply steps %v", actions(rec))
	rec = e.job(jobs.KindUpdateRollback)
	rr, err := e.u.Rollback(context.Background(), rec, RollbackOptions{Source: jobs.SourceConsole})
	if err != nil || rr.PitrOffer == nil {
		t.Fatalf("rollback %+v %v", rr, err)
	}
	if l := e.running(); l.Solution.Version != "2026.11.0" || l.Components.Plugs["care_fasterappointments_fe"].Enabled {
		t.Fatalf("after rollback %+v", l.Solution)
	}
	t.Logf("rollback steps %v; pitr_offer target_time %s restore_point %s", actions(rec), rr.PitrOffer.TargetTime, *rr.PitrOffer.RestorePoint)
}
