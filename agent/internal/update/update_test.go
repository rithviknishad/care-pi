package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/fe"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/s3admin"
	"github.com/rithviknishad/care-pi/agent/internal/secrets"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
	"github.com/rithviknishad/care-pi/agent/internal/update/updatetest"
)

const solutionFile = "../../../solutions/care-clinic/solution.yaml"

type fakeCP struct{ n *int }

func (f fakeCP) Checkpoint(context.Context) (CheckpointRef, error) {
	*f.n++
	t := time.Date(2026, 11, 22, 2, 5, 12, 0, time.UTC)
	return CheckpointRef{BackupLabel: "20261122-020512I", RestorePoint: "care-pi-test", TakenAt: t, TargetTime: t.Add(28 * time.Second), Duration: 28 * time.Second}, nil
}

type tenv struct {
	t     *testing.T
	p     paths.Paths
	cfg   string
	fake  *host.Fake
	u     *Updater
	fx    *updatetest.Fixture
	cps   int
	vrfy  []VerifyTarget
	clock time.Time
	last  *jobs.Recorder
}

// finish ends the last job, so that the update machine does not see it as running.
func (e *tenv) finish() {
	if e.last != nil {
		e.last.Finish(nil, nil)
		e.last = nil
	}
}

func (e *tenv) overview() *Overview {
	e.t.Helper()
	e.finish()
	o, err := e.u.Overview(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	return o
}

func newTEnv(t *testing.T, track, pin string) *tenv {
	t.Helper()
	root := t.TempDir()
	p := paths.New(root)
	e := &tenv{t: t, p: p, fake: host.NewFake(), clock: time.Date(2026, 11, 22, 2, 3, 0, 0, time.UTC)}
	e.fake.Hook = host.PlugConfigHook
	cfgData, err := config.DefaultYAML("Sunrise Clinic", "care", "small-clinic", "artifact")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(cfgData, "config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	e.fx, err = updatetest.Build(filepath.Join(root, "dist"), solutionFile, cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.cfg = p.Abs(paths.ConfigFile)
	e.writeConfig(track, pin)
	if _, err := (secrets.Store{Paths: p}).EnsureAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := (&setup.Store{Paths: p}).Update(func(r *setup.Record) error { r.ApplyPhase = setup.PhaseFull; return nil }); err != nil {
		t.Fatal(err)
	}
	h := host.New(e.fake)
	e.u = &Updater{Paths: p, ConfigPath: e.cfg, Host: h, IPs: netinfo.Static{"192.168.1.10"}, S3: &s3admin.Fake{},
		Now: func() time.Time { return e.clock }, Location: time.UTC, Timezone: "UTC", AgentVersion: "v0.4.0",
		NewCheckpointer: func(string) Checkpointer { return fakeCP{n: &e.cps} },
		Verifier: FuncVerifier(func(_ context.Context, vt VerifyTarget) error {
			e.vrfy = append(e.vrfy, vt)
			return nil
		}),
		ApplyHook: func(o *apply.Options) { o.PollInterval = time.Millisecond },
		Ready:     func() bool { return true }}
	return e
}

func (e *tenv) writeConfig(track, pin string) {
	e.t.Helper()
	data, err := config.DefaultYAML("Sunrise Clinic", "care", "small-clinic", "artifact")
	if err != nil {
		e.t.Fatal(err)
	}
	s := string(data)
	repl := "solution:\n  name: care-clinic\n  track: " + track + "\n"
	if pin != "" {
		repl += "  pin: \"" + pin + "\"\n"
	}
	s = strings.Replace(s, "solution:\n  name: care-clinic\n  track: stable\n", repl, 1)
	s += "\nupdates:\n  auto: true\n  window: {days: [Sun], start: \"02:00\", end: \"05:00\"}\n  os_security: true\n  index_url: " + e.fx.IndexURL + "\n"
	if err := os.MkdirAll(filepath.Dir(e.cfg), 0o700); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(e.cfg, []byte(s), 0o600); err != nil {
		e.t.Fatal(err)
	}
	if _, err := config.Load(e.cfg); err != nil {
		e.t.Fatalf("config: %v\n%s", err, s)
	}
}

func (e *tenv) job(kind string) *jobs.Recorder {
	e.t.Helper()
	e.finish()
	e.clock = e.clock.Add(time.Second)
	rec, err := jobs.Create(jobs.CreateOptions{Paths: e.p, Kind: kind, Source: jobs.SourceCLI, Now: func() time.Time { return e.clock }})
	if err != nil {
		e.t.Fatal(err)
	}
	e.last = rec
	return rec
}

func actions(rec *jobs.Recorder) []string {
	var out []string
	for _, s := range rec.Job().Steps {
		if s.Action != nil {
			out = append(out, *s.Action)
		}
	}
	return out
}

func (e *tenv) running() *lock.Lock {
	e.t.Helper()
	l, err := lock.Load(e.p)
	if err != nil {
		e.t.Fatal(err)
	}
	return l
}

// install puts the device on 2026.11.0 with the track pinned.
func (e *tenv) install() {
	e.t.Helper()
	rec := e.job(jobs.KindUpdateCheck)
	if _, err := e.u.Check(context.Background(), rec); err != nil {
		e.t.Fatalf("check: %v", err)
	}
	rec = e.job(jobs.KindUpdateApply)
	res, err := e.u.Apply(context.Background(), rec, ApplyOptions{Now: true, Source: jobs.SourceCLI})
	if err != nil {
		e.t.Fatalf("install 2026.11.0: %v", err)
	}
	if l := e.running(); l.Solution.Version != "2026.11.0" || l.Solution.Source != lock.SourceLock || *res.ToVersion != "2026.11.0" {
		e.t.Fatalf("installed %+v", l.Solution)
	}
}

var cutoverSteps = []string{"fetch-lock", "plan", "stage-pull", "stage-bundles", "stage-build", "render",
	"cutover-maintenance-on", "stop", "checkpoint", "activate", "migrate", "start", "plug-config", "verify", "write-lock",
	"maintenance-off", "prune-images"}

// TestUpdateCheckStageApplyRollback: a device on stable updates from lock N to N+1 and rolls back to N
// with the PITR offer of the update (docs/solutions.md, sections 7 to 9).
func TestUpdateCheckStageApplyRollback(t *testing.T) {
	e := newTEnv(t, "pinned", "2026.11.0")
	e.install()
	n := e.running()
	if n.Components.Care == nil || n.Components.Care.Digest != updatetest.Digest("care-2026.11.0") || n.Images["care"].Digest != updatetest.Digest("solution-image-2026.11.0") {
		t.Fatalf("components of N: %+v images %+v", n.Components.Care, n.Images["care"])
	}
	if fe.Current(e.p) != e.fx.FEBuildID["2026.11.0"] {
		t.Fatalf("fe/current %q", fe.Current(e.p))
	}
	onb := n.Components.Plugs["care_onboarding_fe"].Frontend
	if onb == nil || onb.Source != "built" || !strings.HasPrefix(onb.MetaURL, "https://care.local/plugs/care_onboarding_fe/") {
		t.Fatalf("onboarding frontend %+v", onb)
	}
	if _, err := os.Stat(e.p.Abs(paths.PlugsDir + "/care_onboarding_fe/" + onb.SHA[:12] + "/assets/remoteEntry.js")); err != nil {
		t.Fatalf("plug bundle: %v", err)
	}

	// Stable: the check finds N+1.
	e.writeConfig("stable", "")
	rec := e.job(jobs.KindUpdateCheck)
	cr, err := e.u.Check(context.Background(), rec)
	if err != nil {
		t.Fatal(err)
	}
	if !cr.UpdateAvailable || *cr.TargetVersion != "2026.11.1" || *cr.CurrentVersion != "2026.11.0" || !reflect.DeepEqual(actions(rec), []string{"fetch-index", "fetch-lock"}) {
		t.Fatalf("check %+v steps %v", cr, actions(rec))
	}
	ov := e.overview()
	if !ov.UpdateAvailable || ov.Target == nil || ov.Target.Path != PathCutover || ov.Latest.Version != "2026.11.1" || len(ov.Available) != 1 || ov.Rollback.Available {
		t.Fatalf("overview %+v target %+v", ov, ov.Target)
	}

	// Stage: Care stays up; nothing on the stack changes.
	e.fake.Calls = nil
	rec = e.job(jobs.KindUpdateStage)
	sr, err := e.u.Stage(context.Background(), rec, StageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if *sr.Version != "2026.11.1" || !reflect.DeepEqual(actions(rec), cutoverSteps[:6]) || len(sr.Downloaded) == 0 {
		t.Fatalf("stage %+v steps %v", sr, actions(rec))
	}
	for _, c := range e.fake.Calls {
		if strings.HasPrefix(c, "systemctl stop") || strings.HasPrefix(c, "systemctl start") || strings.HasPrefix(c, "systemctl restart") {
			t.Fatalf("staging ran %q", c)
		}
	}
	if ov := e.overview(); ov.State != StateStaged || !ov.Target.Staged || len(ov.Components) == 0 {
		t.Fatalf("after stage: state %s target %+v", ov.State, ov.Target)
	}
	if l := e.running(); l.Solution.Version != "2026.11.0" {
		t.Fatal("staging changed lock.yaml")
	}

	// Apply: the cutover with the maintenance page, the checkpoint, and the order beat, then worker and api.
	e.fake.Calls = nil
	rec = e.job(jobs.KindUpdateApply)
	ar, err := e.u.Apply(context.Background(), rec, ApplyOptions{Now: true, Source: jobs.SourceTimer, Reason: "window Sun 02:00"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actions(rec), cutoverSteps) {
		t.Fatalf("apply steps %v", actions(rec))
	}
	l := e.running()
	if l.Solution.Version != "2026.11.1" || l.History.Previous == "" || *ar.FromVersion != "2026.11.0" || ar.Checkpoint == nil || ar.FromLock == "" {
		t.Fatalf("after apply %+v result %+v", l.Solution, ar)
	}
	if _, err := os.Stat(e.p.Abs(paths.LockHistory + "/" + l.History.Previous)); err != nil {
		t.Fatalf("history file: %v", err)
	}
	if prev, _ := lock.ReadHistory(e.p, l.History.PreviousID()); prev == nil || prev.Solution.Version != "2026.11.0" {
		t.Fatalf("previous lock %+v", prev)
	}
	order := e.fake.Filter("systemctl stop", "systemctl start", "podman exec -i care-api")
	want := []string{"systemctl stop care-beat.service care-worker.service care-api.service", "systemctl start care-beat.service",
		"systemctl start care-worker.service care-api.service", "systemctl start care.target", "podman exec -i care-api python manage.py shell"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("command order:\n got  %q\n want %q", order, want)
	}
	if fe.Current(e.p) != e.fx.FEBuildID["2026.11.1"] || e.cps != 2 {
		t.Fatalf("fe/current %s checkpoints %d", fe.Current(e.p), e.cps)
	}
	if _, on := apply.ReadMaintenance(e.p); on {
		t.Fatal("the maintenance page stays on")
	}
	h := ReadHistory(e.p)
	if len(h) != 2 || h[0].Outcome != OutcomeSucceeded || *h[0].ToVersion != "2026.11.1" || h[0].Checkpoint == nil {
		t.Fatalf("history %+v", h)
	}
	if ov := e.overview(); ov.UpdateAvailable || !ov.Rollback.Available || ov.Rollback.PitrOffer == nil || ov.State != StateIdle {
		t.Fatalf("after apply: %+v rollback %+v", ov, ov.Rollback)
	}

	// Rollback: the previous lock runs again; the result offers the PITR to the checkpoint of the update.
	rec = e.job(jobs.KindUpdateRollback)
	rr, err := e.u.Rollback(context.Background(), rec, RollbackOptions{Source: jobs.SourceConsole})
	if err != nil {
		t.Fatal(err)
	}
	wantRB := []string{"fetch-lock", "stage-pull", "cutover-maintenance-on", "stop", "checkpoint", "activate", "migrate", "start",
		"plug-config", "verify", "write-lock", "maintenance-off"}
	if !reflect.DeepEqual(actions(rec), wantRB) {
		t.Fatalf("rollback steps %v", actions(rec))
	}
	if l := e.running(); l.Solution.Version != "2026.11.0" || rr.PitrOffer == nil || rr.PitrOffer.Restore.Kind != "pitr" || *rr.ToVersion != "2026.11.0" {
		t.Fatalf("after rollback %+v result %+v", l.Solution, rr)
	}
	if fe.Current(e.p) != e.fx.FEBuildID["2026.11.0"] {
		t.Fatalf("fe/current after rollback %s", fe.Current(e.p))
	}
	// The history entry of the rollback has the same offer as the job result: the console shows it after a reload.
	if h := ReadHistory(e.p); h[0].Kind != HistRollback || h[0].Outcome != OutcomeSucceeded || h[0].PitrOffer == nil ||
		!reflect.DeepEqual(h[0].PitrOffer, rr.PitrOffer) {
		t.Fatalf("rollback history entry %+v, result offer %+v", h[0], rr.PitrOffer)
	}
	// Stable installs N+1 again at the next window: the update is available.
	if ov := e.overview(); ov.LastResult == nil || ov.LastResult.Kind != HistRollback {
		t.Fatalf("last result %+v", ov.LastResult)
	}
}

// TestMigrationFailureRollsBack: a failure of care-beat (the migrations) rolls back to N by itself, and
// the job error offers the PITR to the checkpoint.
func TestMigrationFailureRollsBack(t *testing.T) {
	e := newTEnv(t, "pinned", "2026.11.0")
	e.install()
	e.writeConfig("stable", "")
	e.fake.On("systemctl start care-beat.service", host.Response{Code: 1, Out: "Job for care-beat.service failed"}, host.Response{})
	e.fake.Calls = nil
	rec := e.job(jobs.KindUpdateApply)
	_, err := e.u.Apply(context.Background(), rec, ApplyOptions{Now: true, Source: jobs.SourceTimer})
	var p *problem.Problem
	if !errors.As(err, &p) || p.Slug != problem.MigrationFailed || p.PitrOffer == nil {
		t.Fatalf("want migration-failed with a PITR offer, got %v", err)
	}
	if offer := p.PitrOffer.(*PitrOffer); !offer.Available || offer.Restore.Time.IsZero() {
		t.Fatalf("offer %+v", offer)
	}
	got := actions(rec)
	want := []string{"fetch-lock", "plan", "stage-pull", "stage-bundles", "stage-build", "render", "cutover-maintenance-on", "stop",
		"checkpoint", "activate", "migrate", "rollback", "maintenance-off"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("steps:\n got  %v\n want %v", got, want)
	}
	if l := e.running(); l.Solution.Version != "2026.11.0" {
		t.Fatalf("the lock changed after a failed update: %+v", l.Solution)
	}
	if fe.Current(e.p) != e.fx.FEBuildID["2026.11.0"] {
		t.Fatalf("fe/current after the rollback %s", fe.Current(e.p))
	}
	if _, on := apply.ReadMaintenance(e.p); on {
		t.Fatal("the maintenance page stays after a rollback that succeeded")
	}
	h := ReadHistory(e.p)
	if h[0].Outcome != OutcomeRolledBack || h[0].PitrOffer == nil || h[0].Problem.Slug != problem.MigrationFailed {
		t.Fatalf("history %+v", h[0])
	}
	if st := ReadState(e.p); st.Cutover != nil {
		t.Fatalf("cutover state stays: %+v", st.Cutover)
	}
}

// TestCheckpointFailureStartsTheOldUnits: a failed checkpoint stops the update before any change.
func TestCheckpointFailureStartsTheOldUnits(t *testing.T) {
	e := newTEnv(t, "pinned", "2026.11.0")
	e.install()
	e.writeConfig("stable", "")
	e.u.NewCheckpointer = nil // the checkpoint of Phase 3 (BackupCheckpointer) on the fake host
	e.fake.Hook = func(c host.Cmd, in []byte) (host.Response, bool) {
		if strings.HasPrefix(c.String(), "podman exec --user postgres care-db pgbackrest --stanza=main backup --type=incr") {
			return host.Response{Code: 1, Out: "ERROR: [056]"}, true
		}
		return host.PlugConfigHook(c, in)
	}
	rec := e.job(jobs.KindUpdateApply)
	_, err := e.u.Apply(context.Background(), rec, ApplyOptions{Now: true})
	var p *problem.Problem
	if !errors.As(err, &p) || p.Slug != problem.CheckpointFailed || p.PitrOffer != nil {
		t.Fatalf("want checkpoint-failed without an offer, got %v", err)
	}
	got := actions(rec)
	if got[len(got)-3] != "checkpoint" || got[len(got)-2] != "rollback" || got[len(got)-1] != "maintenance-off" {
		t.Fatalf("steps %v", got)
	}
	if l := e.running(); l.Solution.Version != "2026.11.0" {
		t.Fatal("the lock changed")
	}
}

// TestWithdrawnAndAgentTooOld: stable skips a withdrawn lock and refuses a lock for a newer agent.
func TestWithdrawnAndAgentTooOld(t *testing.T) {
	e := newTEnv(t, "pinned", "2026.11.0")
	e.install()
	e.writeConfig("stable", "")
	if err := e.fx.Withdraw("care-clinic", "2026.11.1", "a migration fails"); err != nil {
		t.Fatal(err)
	}
	rec := e.job(jobs.KindUpdateCheck)
	cr, err := e.u.Check(context.Background(), rec)
	if err != nil {
		t.Fatal(err)
	}
	if cr.UpdateAvailable {
		t.Fatalf("a withdrawn lock is not an update: %+v", cr)
	}
	e.u.AgentVersion = "v0.0.0"
	if _, err := e.u.ResolveTarget(context.Background(), mustConfig(t, e.cfg), "", "", false); !problem.Is(err, problem.AgentTooOld) {
		t.Fatalf("want agent-too-old, got %v", err)
	}
}

func mustConfig(t *testing.T, p string) *config.Config {
	t.Helper()
	c, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestIndexUnavailable: a failed download keeps the cache and records the problem.
func TestIndexUnavailable(t *testing.T) {
	e := newTEnv(t, "stable", "")
	rec := e.job(jobs.KindUpdateCheck)
	if _, err := e.u.Check(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(e.fx.Path(e.fx.IndexURL)); err != nil {
		t.Fatal(err)
	}
	rec = e.job(jobs.KindUpdateCheck)
	_, err := e.u.Check(context.Background(), rec)
	if !problem.Is(err, problem.IndexUnavailable) {
		t.Fatalf("want index-unavailable, got %v", err)
	}
	if ix, _ := solutionCached(e.p); ix == nil {
		t.Fatal("the cache was lost")
	}
	if st := ReadState(e.p); st.LastCheckError == nil || st.LastCheckError.Slug != problem.IndexUnavailable {
		t.Fatalf("state %+v", st)
	}
}

// TestBadLockHash: a lock with another SHA-256 than the index is artefact-invalid.
func TestBadLockHash(t *testing.T) {
	e := newTEnv(t, "stable", "")
	v := "2026.11.1"
	path := filepath.Join(e.fx.Dir, "lock-care-clinic-"+v, "care-clinic-"+v+".lock.yaml")
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(path, append(data, []byte("\n# changed\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := e.job(jobs.KindUpdateCheck)
	_, err := e.u.Check(context.Background(), rec)
	if !problem.Is(err, problem.ArtefactInvalid) {
		t.Fatalf("want artefact-invalid, got %v", err)
	}
}

func solutionCached(p paths.Paths) (any, error) {
	data, err := os.ReadFile(p.Abs(paths.UpdateCache + "/index.json"))
	if err != nil {
		return nil, err
	}
	return data, nil
}
