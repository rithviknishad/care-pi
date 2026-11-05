package apply

import (
	"runtime"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/plan"
	"github.com/rithviknishad/care-pi/agent/internal/render"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
	"github.com/rithviknishad/care-pi/agent/internal/update/updatetest"
)

// TestSetupModeTakesCaddyFromTheSolutionLock: setup mode resolves the Caddy image from the cached solution
// lock and asks no registry. The Raspberry Pi image boots the first time without internet (docs/image.md).
func TestSetupModeTakesCaddyFromTheSolutionLock(t *testing.T) {
	e := newEnv(t, smallClinic)
	if _, err := (&setup.Store{Paths: e.p}).Update(func(r *setup.Record) error { r.ApplyPhase = setup.PhaseBootstrap; return nil }); err != nil {
		t.Fatal(err)
	}
	// No registry: each skopeo call fails.
	e.fake = host.NewFake()
	e.fake.On("skopeo inspect --override-os linux --override-arch "+runtime.GOARCH+" --format {{.Digest}} docker://"+render.ImageCaddy,
		host.Response{Code: 1, Out: "dial tcp: lookup ghcr.io: no such host"})
	if _, err := e.run(false); err == nil {
		t.Fatal("setup mode without a solution lock and without a registry must fail")
	}

	cfg, err := config.Load(e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	fx, err := updatetest.Build(t.TempDir(), solutionsDir+"/care-clinic/solution.yaml", cfg)
	if err != nil {
		t.Fatal(err)
	}
	l, err := solution.ParseLock(fx.Locks["2026.11.1"])
	if err != nil {
		t.Fatal(err)
	}
	if err := (solution.LockStore{Paths: e.p}).Put(l); err != nil {
		t.Fatal(err)
	}
	e.fake.Calls = nil
	if _, err := e.run(false); err != nil {
		t.Fatalf("setup mode with a cached solution lock: %v\n%s", err, e.out.String())
	}
	if got := e.fake.Filter("skopeo"); len(got) != 0 {
		t.Errorf("setup mode asked the registry: %v", got)
	}
	lk, err := lock.Load(e.p)
	if err != nil {
		t.Fatal(err)
	}
	if got := lk.Images["caddy"]; got.Ref != render.ImageCaddy || got.Digest != updatetest.Digest("caddy") {
		t.Errorf("caddy %+v, want the digest of the solution lock", got)
	}
}

// TestSetupFinishIsNotACutover: the finish of the setup starts Care for the first time. The lock of setup mode
// has only Caddy, and care.target runs (Caddy and the console), but no database exists yet: the plan must not
// take the cutover, whose checkpoint backs up care-db (the first image boot on a Pi 4, 2026-11-05).
func TestSetupFinishIsNotACutover(t *testing.T) {
	e := newEnv(t, smallClinic)
	store := &setup.Store{Paths: e.p}
	if _, err := store.Update(func(r *setup.Record) error { r.ApplyPhase = setup.PhaseBootstrap; return nil }); err != nil {
		t.Fatal(err)
	}
	e.targetState("inactive")
	if _, err := e.run(false); err != nil {
		t.Fatalf("setup mode: %v\n%s", err, e.out.String())
	}
	if lk, err := lock.Load(e.p); err != nil || lk.Images["care"].Pull() != "" {
		t.Fatalf("the lock of setup mode has a care image (%v): %+v", err, lk)
	}

	if _, err := store.Update(func(r *setup.Record) error { r.ApplyPhase = setup.PhaseFull; return nil }); err != nil {
		t.Fatal(err)
	}
	e.reset()
	e.targetState("active")
	e.secretsPresent()
	res, err := e.run(false)
	if err != nil {
		t.Fatalf("setup finish: %v\n%s", err, e.out.String())
	}
	if res.Plan.Has(plan.Cutover) {
		t.Fatalf("the setup finish took the cutover: %+v", res.Plan.Actions)
	}
	if !res.Plan.Has(plan.FullCycle) {
		t.Errorf("the setup finish must cycle the stack: %+v", res.Plan.Actions)
	}
}
