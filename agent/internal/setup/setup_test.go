package setup

import (
	"slices"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

func rec(steps map[string]string) *Record {
	r := &Record{Version: 1, ApplyPhase: PhaseBootstrap, Steps: map[string]*StepRecord{}}
	for id, st := range steps {
		r.Steps[id] = &StepRecord{Status: st, UpdatedAt: time.Unix(0, 0)}
	}
	return r
}

func TestStateName(t *testing.T) {
	at := time.Unix(1, 0)
	for _, tc := range []struct {
		name string
		in   Input
		want string
	}{
		{"no account", Input{Record: rec(nil), Exists: true}, Unclaimed},
		{"no account on a Phase 1 host", Input{Record: rec(nil), LockExists: true}, Unclaimed},
		{"claimed", Input{Record: rec(nil), Exists: true, Claimed: true}, Claimed},
		{"one step", Input{Record: rec(map[string]string{StepIdentity: Done}), Exists: true, Claimed: true}, InSetup},
		{"one skip", Input{Record: rec(map[string]string{StepEnv: Skipped}), Exists: true, Claimed: true}, InSetup},
		{"Phase 1 host after the claim", Input{Record: rec(nil), LockExists: true, Claimed: true}, Ready},
		{"setup mode with a lock", Input{Record: rec(nil), Exists: true, LockExists: true, Claimed: true}, Claimed},
		{"setup job runs", Input{Record: &Record{JobID: "j", Steps: map[string]*StepRecord{}}, Exists: true, Claimed: true, JobState: "running"}, Applying},
		{"setup job waits for the lock", Input{Record: &Record{JobID: "j", Steps: map[string]*StepRecord{}}, Exists: true, Claimed: true, JobState: "queued"}, Applying},
		{"setup job failed", Input{Record: &Record{JobID: "j", Steps: map[string]*StepRecord{StepSize: {Status: Done}}}, Exists: true, Claimed: true, JobState: "failed"}, InSetup},
		{"setup job interrupted, no journal", Input{Record: &Record{JobID: "j", Steps: map[string]*StepRecord{StepSize: {Status: Done}}}, Exists: true, Claimed: true}, InSetup},
		{"ready", Input{Record: &Record{ReadyAt: &at, Steps: map[string]*StepRecord{}}, Exists: true, Claimed: true}, Ready},
	} {
		if got := StateName(tc.in); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}

func allDone(dev bool) map[string]string {
	m := map[string]string{}
	for _, d := range Steps {
		if d.ID != StepAccount {
			m[d.ID] = Done
		}
	}
	m[StepEnv] = Skipped
	if dev {
		m[StepBackups] = Skipped
	}
	return m
}

func TestComputeSteps(t *testing.T) {
	// After the claim: the account is done, recovery-codes is current, each other step is pending.
	v := Compute(Input{Record: rec(nil), Exists: true, Claimed: true, DevBuild: true})
	if v.Current != StepRecoveryCodes || v.CanFinish || v.Steps[0].Status != Done || v.Steps[1].Status != Current || v.Steps[2].Status != Pending {
		t.Fatalf("after the claim: %+v", v)
	}
	if !slices.Equal(v.Missing, []string{StepRecoveryCodes, StepHardware, StepIdentity, StepTLS, StepSize, StepBackups, StepCareAdmin}) {
		t.Fatalf("missing %v", v.Missing)
	}

	// The order is free: current is the first step that is not done.
	v = Compute(Input{Record: rec(map[string]string{StepSize: Done, StepRecoveryCodes: Done}), Exists: true, Claimed: true})
	if v.Current != StepHardware {
		t.Fatalf("current %s", v.Current)
	}

	// A dev build can skip backups and finish. A release build cannot.
	dev := Compute(Input{Record: rec(allDone(true)), Exists: true, Claimed: true, DevBuild: true, CarePassword: true})
	if !dev.CanFinish || dev.Current != "" || len(dev.Missing) != 0 {
		t.Fatalf("dev build: %+v", dev)
	}
	release := Compute(Input{Record: rec(allDone(true)), Exists: true, Claimed: true, DevBuild: false, CarePassword: true})
	if release.CanFinish || !slices.Equal(release.Missing, []string{StepBackups}) {
		t.Fatalf("release build: %+v", release)
	}
	for _, s := range release.Steps {
		if s.Skippable != (s.ID == StepEnv) {
			t.Fatalf("release build: step %s skippable %v", s.ID, s.Skippable)
		}
	}

	// The agent restarted: the Care superuser password is lost, so care-admin is current again.
	v = Compute(Input{Record: rec(allDone(true)), Exists: true, Claimed: true, DevBuild: true, CarePassword: false})
	last := v.Steps[len(v.Steps)-1]
	if v.CanFinish || v.Current != StepCareAdmin || last.Status != Current || len(last.Errors) != 1 || last.Errors[0].Code != "password-needed" {
		t.Fatalf("after a restart: %+v", v)
	}

	// Ready: each step is done, nothing is editable or missing.
	at := time.Unix(1, 0)
	r := rec(nil)
	r.ReadyAt = &at
	v = Compute(Input{Record: r, Exists: true, Claimed: true, DevBuild: true})
	if v.State != Ready || v.CanFinish || v.Current != "" || len(v.Missing) != 0 {
		t.Fatalf("ready: %+v", v)
	}
	for _, s := range v.Steps {
		if s.Status != Done || s.Skippable {
			t.Fatalf("ready: step %+v", s)
		}
	}
}

func TestSkippable(t *testing.T) {
	for _, d := range Steps {
		want := d.ID == StepEnv
		if Skippable(d.ID, false) != want {
			t.Errorf("release build: %s", d.ID)
		}
		if Skippable(d.ID, true) != (want || d.ID == StepBackups) {
			t.Errorf("dev build: %s", d.ID)
		}
	}
	if Known("solution") || !Known(StepCareAdmin) {
		t.Fatal("Known")
	}
}

func TestStoreAndPhase(t *testing.T) {
	p := paths.New(t.TempDir())
	if Phase(p) != PhaseFull {
		t.Fatal("no record: the full phase")
	}
	s := &Store{Paths: p}
	if _, exists, err := s.Load(); err != nil || exists {
		t.Fatalf("load: %v %v", exists, err)
	}
	if _, err := s.Update(func(r *Record) error { r.ApplyPhase = PhaseBootstrap; return nil }); err != nil {
		t.Fatal(err)
	}
	if Phase(p) != PhaseBootstrap {
		t.Fatal("setup mode: the bootstrap phase")
	}
	at := time.Unix(1, 0)
	if _, err := s.Update(func(r *Record) error { r.ReadyAt = &at; return nil }); err != nil {
		t.Fatal(err)
	}
	if Phase(p) != PhaseFull {
		t.Fatal("ready: the full phase")
	}
	r, exists, err := s.Load()
	if err != nil || !exists || r.ApplyPhase != PhaseBootstrap || r.ReadyAt == nil {
		t.Fatalf("record %+v %v %v", r, exists, err)
	}
}
