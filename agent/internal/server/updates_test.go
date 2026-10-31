package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
	"github.com/rithviknishad/care-pi/agent/internal/update"
	"github.com/rithviknishad/care-pi/agent/internal/update/updatetest"
)

type okCP struct{}

func (okCP) Checkpoint(context.Context) (update.CheckpointRef, error) {
	t := time.Now().UTC().Truncate(time.Second)
	return update.CheckpointRef{RestorePoint: "care-pi-test", TakenAt: t, TargetTime: t.Add(time.Second), Duration: time.Second}, nil
}

// readyForUpdates claims the device, marks the setup ready, and points updates.index_url at a local
// dist base with the locks 2026.11.0 and 2026.11.1. The window opens in 3 days (closed now).
func readyForUpdates(t *testing.T) (*env, *updatetest.Fixture) {
	t.Helper()
	e := newEnv(t)
	e.claim()
	cfgData, err := config.DefaultYAML("Sunrise Clinic", "care", "small-clinic", "none")
	must(t, err)
	cfg, err := config.Parse(cfgData, "config.yaml")
	must(t, err)
	fx, err := updatetest.Build(filepath.Join(t.TempDir(), "dist"), "../../../solutions/care-clinic/solution.yaml", cfg)
	must(t, err)
	day := e.clock.now().Add(72 * time.Hour).Weekday().String()[:3]
	s := string(cfgData) + "\nupdates:\n  auto: false\n  window: {days: [" + day + "], start: \"02:00\", end: \"05:00\"}\n  os_security: true\n  index_url: " + fx.IndexURL + "\n"
	must(t, os.WriteFile(e.p.Abs(paths.ConfigFile), []byte(s), 0o600))
	now := e.clock.now()
	_, err = e.svc.Setup.Update(func(r *setup.Record) error { r.ApplyPhase, r.ReadyAt = setup.PhaseFull, &now; return nil })
	must(t, err)
	e.svc.UpdateHook = func(u *update.Updater) {
		u.NewCheckpointer = func(string) update.Checkpointer { return okCP{} }
		u.Verifier = update.FuncVerifier(func(context.Context, update.VerifyTarget) error { return nil })
		u.AgentVersion = "v0.4.0"
	}
	return e, fx
}

func (e *env) jobOK(r *resp) string {
	e.t.Helper()
	e.want(r, 202, "")
	id := r.json(e.t)["id"].(string)
	j := e.waitJob(id)
	if j.State != jobs.StateSucceeded {
		e.t.Fatalf("job %s %s: %+v", id, j.State, j.Error)
	}
	return id
}

// TestUpdatesContract: each operation of docs/api/README.md section 14, success and problems.
func TestUpdatesContract(t *testing.T) {
	e, _ := readyForUpdates(t)

	r := e.do("GET", "/api/v1/updates", nil)
	e.want(r, 200, "")
	if m := r.json(t); m["update_available"] != false || m["current"] != nil || m["state"] != "idle" {
		t.Fatalf("updates before the check %v", m)
	}
	e.want(e.do("GET", "/api/v1/updates/history", nil), 200, "")
	e.want(e.do("GET", "/api/v1/updates/history?limit=500", nil, invalid), 422, "invalid-request")
	e.want(e.do("POST", "/api/v1/updates/rollback", nil), 409, "rollback-unavailable")

	e.jobOK(e.do("POST", "/api/v1/updates/check", nil))
	r = e.do("GET", "/api/v1/updates", nil)
	e.want(r, 200, "")
	if m := r.json(t); m["update_available"] != true || m["target"].(map[string]any)["version"] != "2026.11.1" {
		t.Fatalf("updates after the check %v", m)
	}
	// The window is closed now; the track stable takes only the newest lock.
	r = e.do("POST", "/api/v1/updates/apply", map[string]any{"version": nil, "now": false, "reason": nil})
	e.want(r, 409, "update-window-closed")
	if r.json(t)["next_window_at"] == nil {
		t.Fatalf("no next_window_at: %s", r.body)
	}
	e.want(e.do("POST", "/api/v1/updates/apply", map[string]any{"version": "2026.11.0", "now": true}), 422, "invalid-request")
	e.want(e.do("POST", "/api/v1/updates/stage", map[string]any{"version": "2026.11.1"}), 202, "")
	e.waitJob(jobs.Latest(e.p, func(j *jobs.Job) bool { return j.Kind == jobs.KindUpdateStage }).ID)

	e.jobOK(e.do("POST", "/api/v1/updates/apply", map[string]any{"now": true, "reason": "contract test"}))
	if l, _ := lock.Load(e.p); l == nil || l.Solution.Version != "2026.11.1" {
		t.Fatalf("lock after the update: %+v", l)
	}
	e.want(e.do("POST", "/api/v1/updates/apply", map[string]any{"now": true}), 409, "no-update-available")
	r = e.do("GET", "/api/v1/status", nil)
	e.want(r, 200, "")
	if u := r.json(t)["updates"].(map[string]any); u["current_version"] != "2026.11.1" {
		t.Fatalf("status updates %v", u)
	}
	r = e.do("GET", "/api/v1/updates/history?limit=1", nil)
	e.want(r, 200, "")
	if items := r.json(t)["items"].([]any); len(items) != 1 || items[0].(map[string]any)["kind"] != "update" {
		t.Fatalf("history %s", r.body)
	}

	// The first update installed the lock on a device without one, so no previous lock exists yet:
	// a second check and a pin give a previous lock for the rollback.
	e.want(e.do("POST", "/api/v1/updates/rollback", map[string]any{"to_lock": "20990101T000000Z", "reason": nil}), 409, "rollback-unavailable")

	// OS updates.
	e.want(e.do("GET", "/api/v1/updates/os", nil), 200, "")
	e.want(e.do("POST", "/api/v1/updates/os/apply", map[string]any{"now": false}), 409, "update-window-closed")
	e.jobOK(e.do("POST", "/api/v1/updates/os/apply", map[string]any{"now": true, "reboot_if_required": false, "reason": "test"}))
	r = e.do("POST", "/api/v1/updates/os/reboot", map[string]any{"now": false})
	e.want(r, 202, "")
	if r.json(t)["scheduled"] != true {
		t.Fatalf("reboot %s", r.body)
	}
	e.want(e.do("POST", "/api/v1/updates/os/reboot", map[string]any{"now": true, "delay_s": 5}), 202, "")
	e.want(e.do("POST", "/api/v1/updates/os/reboot", map[string]any{"delay_s": 900}, invalid), 422, "invalid-request")

	// Solutions.
	e.want(e.do("GET", "/api/v1/solutions", nil), 200, "")
	r = e.do("GET", "/api/v1/solutions/care-clinic", nil)
	e.want(r, 200, "")
	if m := r.json(t); m["document_version"] != "2026.11.1" || len(m["locks"].([]any)) != 2 {
		t.Fatalf("solution %v", m)
	}
	e.want(e.do("GET", "/api/v1/solutions/care-pharmacy", nil), 404, "not-found")
	e.want(e.do("GET", "/api/v1/solutions/care-clinic/locks/2026.11.0", nil), 200, "")
	e.want(e.do("GET", "/api/v1/solutions/care-clinic/locks/2026.10.0", nil), 404, "lock-not-found")
}

// TestPlugsContract: GET /plugs, the settings schema, and PUT /plugs/{id} (plan, settings, apply).
func TestPlugsContract(t *testing.T) {
	e, _ := readyForUpdates(t)
	e.jobOK(e.do("POST", "/api/v1/updates/check", nil))
	e.jobOK(e.do("POST", "/api/v1/updates/apply", map[string]any{"now": true}))

	r := e.do("GET", "/api/v1/plugs", nil)
	e.want(r, 200, "")
	items := r.json(t)["items"].([]any)
	if len(items) != 4 {
		t.Fatalf("plugs %s", r.body)
	}
	e.want(e.do("GET", "/api/v1/plugs/care_filly/settings-schema", nil), 200, "")
	e.want(e.do("GET", "/api/v1/plugs/care_scribe/settings-schema", nil), 404, "plug-unknown")
	e.want(e.do("PUT", "/api/v1/plugs/care_scribe", map[string]any{"enabled": false}), 404, "plug-unknown")

	// A plug that is on needs its required settings.
	r = e.do("PUT", "/api/v1/plugs/care_filly?dry_run=true", map[string]any{"enabled": true})
	e.want(r, 422, "plug-settings-invalid")
	if !strings.Contains(string(r.body), "/settings/backend/MEDISPEAK_API_KEY") {
		t.Fatalf("errors %s", r.body)
	}
	e.want(e.do("PUT", "/api/v1/plugs/care_filly?dry_run=true", map[string]any{"enabled": true,
		"settings": map[string]any{"backend": map[string]any{"MEDISPEAK_BASE_URL": "ftp://x"}}}), 422, "plug-settings-invalid")
	r = e.do("PUT", "/api/v1/plugs/care_filly?dry_run=true", map[string]any{"enabled": true,
		"settings": map[string]any{"backend": map[string]any{"MEDISPEAK_API_KEY": "msk_live_0123456789abcdef", "MEDISPEAK_BASE_URL": "https://medispeak.example.org/api/v2"}}})
	e.want(r, 200, "")
	m := r.json(t)
	if plan := m["plan"].(map[string]any); plan["path"] != "cutover" || plan["checkpoint"] != true || m["written"] != false {
		t.Fatalf("filly plan %v", m)
	}
	if strings.Contains(string(r.body), "msk_live_0123456789abcdef") {
		t.Fatalf("a secret value is in the response: %s", r.body)
	}
	e.want(e.do("PUT", "/api/v1/plugs/care_filly?dry_run=true&apply=true", map[string]any{"enabled": true}, invalid), 422, "invalid-request")
	e.want(e.do("PUT", "/api/v1/plugs/care_fasterappointments_fe", map[string]any{"enabled": true}, header("If-Match", `"0000000000000000"`)), 412, "config-changed")

	// An FE plug on: plug-config, no restart.
	r = e.do("PUT", "/api/v1/plugs/care_fasterappointments_fe?apply=true", map[string]any{"enabled": true,
		"settings": map[string]any{"frontend": map[string]any{"config.payment_methods": []any{"cash", "debc"}}}, "reason": "front desk"})
	e.want(r, 202, "")
	res := r.json(t)
	if res["plan"].(map[string]any)["path"] != "plug-config" || res["written"] != true {
		t.Fatalf("faster appointments %v", res)
	}
	j := e.waitJob(res["job"].(map[string]any)["id"].(string))
	if j.State != jobs.StateSucceeded {
		t.Fatalf("plug-change %+v", j.Error)
	}
	cfg, err := config.Load(e.p.Abs(paths.ConfigFile))
	must(t, err)
	if c := cfg.Solution.Plugs["care_fasterappointments_fe"]; c.Enabled == nil || !*c.Enabled {
		t.Fatalf("config %+v", cfg.Solution.Plugs)
	}
	if l, _ := lock.Load(e.p); !l.Components.Plugs["care_fasterappointments_fe"].Enabled {
		t.Fatal("the lock has the plug off")
	}
	r = e.do("GET", "/api/v1/plugs", nil)
	for _, it := range r.json(t)["items"].([]any) {
		p := it.(map[string]any)
		if p["id"] == "care_fasterappointments_fe" && (p["on"] != true || p["pending"] != false) {
			t.Fatalf("plug after the change %v", p)
		}
	}
	// A setting of a plug that is off: path none. The agent stores the value (and the secret) and starts no job, also
	// with apply=true; the value counts when the plug turns on.
	r = e.do("PUT", "/api/v1/plugs/care_filly?apply=true", map[string]any{
		"settings": map[string]any{"backend": map[string]any{"MEDISPEAK_API_KEY": "msk_live_0123456789abcdef", "MEDISPEAK_BASE_URL": "https://medispeak.example.org/api/v2"}}})
	e.want(r, 200, "")
	m = r.json(t)
	if plan := m["plan"].(map[string]any); plan["path"] != "none" || plan["changed"] != false || m["written"] != true || m["job"] != nil {
		t.Fatalf("setting of a plug that is off %v", m)
	}
	if p := m["plug"].(map[string]any); p["on"] != false || p["pending"] != false || p["settings_complete"] != true {
		t.Fatalf("plug after the stored setting %v", p)
	}
	r = e.do("PUT", "/api/v1/plugs/care_filly?dry_run=true", map[string]any{"enabled": true})
	e.want(r, 200, "")
	if plan := r.json(t)["plan"].(map[string]any); plan["path"] != "cutover" {
		t.Fatalf("turn on with the stored values %v", plan)
	}
	// Follow the solution again: the plug turns off.
	r = e.do("PUT", "/api/v1/plugs/care_fasterappointments_fe", map[string]any{"enabled": nil})
	e.want(r, 200, "")
	if p := r.json(t)["plug"].(map[string]any); p["enabled"] != false || p["pending"] != true {
		t.Fatalf("follow the solution %v", p)
	}
}

// TestRollbackContract: an update from a lock to the next one, then POST /updates/rollback.
func TestRollbackContract(t *testing.T) {
	e, _ := readyForUpdates(t)
	cfgPath := e.p.Abs(paths.ConfigFile)
	data, err := os.ReadFile(cfgPath)
	must(t, err)
	pinned := strings.Replace(string(data), "track: stable", "track: pinned\n  pin: \"2026.11.0\"", 1)
	must(t, os.WriteFile(cfgPath, []byte(pinned), 0o600))
	e.jobOK(e.do("POST", "/api/v1/updates/check", nil))
	e.jobOK(e.do("POST", "/api/v1/updates/apply", map[string]any{"now": true}))
	e.clock.add(2 * time.Second)
	e.jobOK(e.do("POST", "/api/v1/updates/apply", map[string]any{"version": "2026.11.1", "now": true}))
	if l, _ := lock.Load(e.p); l.Solution.Version != "2026.11.1" {
		t.Fatalf("lock %+v", l.Solution)
	}
	if cfg, _ := config.Load(cfgPath); cfg.Solution.Pin != "2026.11.1" {
		t.Fatalf("pin %q", cfg.Solution.Pin)
	}
	r := e.do("GET", "/api/v1/updates", nil)
	e.want(r, 200, "")
	if rb := r.json(t)["rollback"].(map[string]any); rb["available"] != true || rb["to_version"] != "2026.11.0" {
		t.Fatalf("rollback state %v", rb)
	}
	id := e.jobOK(e.do("POST", "/api/v1/updates/rollback", map[string]any{"to_lock": nil, "reason": "slow"}))
	j, _ := jobs.Load(e.p, id)
	var rr update.RollbackResult
	must(t, json.Unmarshal(j.Result, &rr))
	if rr.PitrOffer == nil || !rr.PitrOffer.Available || rr.ToLock == "" {
		t.Fatalf("rollback result %s", j.Result)
	}
	if l, _ := lock.Load(e.p); l.Solution.Version != "2026.11.0" {
		t.Fatalf("lock after the rollback %+v", l.Solution)
	}
	// After a reload the console reads the offer from the job, the history entry, and last_result.
	r = e.do("GET", "/api/v1/jobs/"+id, nil)
	e.want(r, 200, "")
	jobOffer := r.json(t)["result"].(map[string]any)["pitr_offer"]
	if jobOffer == nil {
		t.Fatalf("job %s", r.body)
	}
	r = e.do("GET", "/api/v1/updates/history", nil)
	e.want(r, 200, "")
	h := r.json(t)["items"].([]any)[0].(map[string]any)
	if h["kind"] != "rollback" || h["outcome"] != "succeeded" || !reflect.DeepEqual(h["pitr_offer"], jobOffer) {
		t.Fatalf("history entry %v, job offer %v", h, jobOffer)
	}
	r = e.do("GET", "/api/v1/updates", nil)
	e.want(r, 200, "")
	if lr := r.json(t)["last_result"].(map[string]any); !reflect.DeepEqual(lr["pitr_offer"], jobOffer) {
		t.Fatalf("last_result %v", lr)
	}
}

// TestAutomaticRollbackContract: a migration failure in update-apply rolls back by itself. The job is failed with
// error.pitr_offer, and the history entry (outcome rolled-back) has the same offer.
func TestAutomaticRollbackContract(t *testing.T) {
	e, _ := readyForUpdates(t)
	cfgPath := e.p.Abs(paths.ConfigFile)
	data, err := os.ReadFile(cfgPath)
	must(t, err)
	must(t, os.WriteFile(cfgPath, []byte(strings.Replace(string(data), "track: stable", "track: pinned\n  pin: \"2026.11.0\"", 1)), 0o600))
	e.jobOK(e.do("POST", "/api/v1/updates/check", nil))
	e.jobOK(e.do("POST", "/api/v1/updates/apply", map[string]any{"now": true}))
	e.clock.add(2 * time.Second)
	e.fake.On("systemctl start care-beat.service", host.Response{Code: 1, Out: "Job for care-beat.service failed"}, host.Response{})
	r := e.do("POST", "/api/v1/updates/apply", map[string]any{"version": "2026.11.1", "now": true})
	e.want(r, 202, "")
	id := r.json(t)["id"].(string)
	if j := e.waitJob(id); j.State != jobs.StateFailed {
		t.Fatalf("job %s", j.State)
	}
	r = e.do("GET", "/api/v1/jobs/"+id, nil)
	e.want(r, 200, "")
	jm := r.json(t)
	jerr := jm["error"].(map[string]any)
	if !strings.HasSuffix(jerr["type"].(string), "/migration-failed") || jerr["pitr_offer"] == nil || jm["result"] != nil {
		t.Fatalf("job %s", r.body)
	}
	r = e.do("GET", "/api/v1/updates/history", nil)
	e.want(r, 200, "")
	h := r.json(t)["items"].([]any)[0].(map[string]any)
	if h["kind"] != "update" || h["outcome"] != "rolled-back" || !reflect.DeepEqual(h["pitr_offer"], jerr["pitr_offer"]) {
		t.Fatalf("history entry %v, job offer %v", h, jerr["pitr_offer"])
	}
	if l, _ := lock.Load(e.p); l.Solution.Version != "2026.11.0" {
		t.Fatalf("lock after the automatic rollback %+v", l.Solution)
	}
}
