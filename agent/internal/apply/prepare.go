// Package apply runs "care-pi apply": validate, resolve, render, diff, write, reload, restart.
package apply

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"

	"github.com/rithviknishad/care-pi/agent/internal/buildinfo"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/render"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
)

// Prepared is the result of the steps that apply and render share.
type Prepared struct {
	Config   *config.Config
	Solution *solution.Solution
	Desire   *render.Plan
	PrevLock *lock.Lock
	Lock     *lock.Lock
	IPs      []string
	Model    *render.Model
	Tree     render.Tree
	// Phase is the apply phase of the tree: setup.PhaseBootstrap or setup.PhaseFull.
	Phase string
}

// PrepareOptions controls Prepare.
type PrepareOptions struct {
	Paths      paths.Paths
	ConfigPath string
	// SolutionsDir is empty for the embedded solutions.
	SolutionsDir string
	Host         *host.Host
	IPs          netinfo.Source
	// Offline forbids network access. render uses it.
	Offline bool
	Update  bool
	// Strict runs the apply-time checks (features that this version does not have).
	Strict bool
	Warn   io.Writer
	// Progress gets one line for each image digest or git sha that Prepare resolves over the network.
	Progress io.Writer
	// Resolved, when it is set, gets the result of the lock resolution, before the render.
	Resolved func(error)
	// Phase is setup.PhaseBootstrap (setup mode: the pod and Caddy only) or setup.PhaseFull.
	// "" reads the phase of the host from the setup record.
	Phase string
}

// Prepare loads the config, the solution, and the lock, resolves refs, and renders the tree.
func Prepare(ctx context.Context, o PrepareOptions) (*Prepared, error) {
	cfg, err := config.Load(o.ConfigPath)
	if err != nil {
		return nil, exitcode.With(exitcode.BadInput, err)
	}
	sol, err := solution.Load(cfg.Solution.Name, o.SolutionsDir)
	if err != nil {
		return nil, exitcode.With(exitcode.BadInput, err)
	}
	des, err := render.Desire(cfg, sol)
	if err != nil {
		return nil, exitcode.With(exitcode.BadInput, err)
	}
	if o.Phase == "" {
		o.Phase = setup.Phase(o.Paths)
	}
	bootstrap := o.Phase == setup.PhaseBootstrap
	if bootstrap {
		// Setup mode runs only Caddy, so it resolves only the Caddy image.
		des.Wants.Images = map[string]string{"caddy": des.Wants.Images["caddy"]}
		des.Wants.Git = map[string]lock.GitWant{}
		des.Wants.Plugs = map[string]lock.GitWant{}
	}
	if o.Strict {
		if err := checkImplemented(cfg, sol, des); err != nil {
			return nil, exitcode.With(exitcode.BadInput, err)
		}
	}
	prev, err := lock.Read(o.Paths.Abs(paths.LockFile))
	if err != nil {
		return nil, err
	}
	r := lock.Resolver{OS: "linux", Arch: runtime.GOARCH, Offline: o.Offline, Progress: o.Progress}
	if o.Host != nil {
		r.Skopeo, r.Git = o.Host.Skopeo, o.Host.Git
	}
	if o.Warn != nil {
		r.Warn = func(m string) { _, _ = fmt.Fprintln(o.Warn, "care-pi: warning:", m) }
	}
	lk, err := r.Resolve(ctx, des.Wants, prev, o.Update)
	if o.Resolved != nil {
		o.Resolved(err)
	}
	if err != nil {
		return nil, err
	}
	lk.AgentVersion = buildinfo.Version
	lk.GeneratedAt = ""
	if prev != nil {
		lk.GeneratedAt = prev.GeneratedAt
	}
	ips, err := netinfo.LANIPv4(o.IPs)
	if err != nil {
		return nil, fmt.Errorf("cannot read the network interfaces: %w", err)
	}
	m, err := render.NewModel(render.Input{Config: cfg, Solution: sol, Lock: lk, IPs: ips, Bootstrap: bootstrap})
	if err != nil {
		return nil, exitcode.With(exitcode.BadInput, err)
	}
	lk.FE = lock.FE{Provider: cfg.FE.Provider, BuildID: m.FEBuildID}
	// Keep the record of the build (url, sha256, built_at) while the build id is the same.
	if prev != nil && prev.FE.Provider == lk.FE.Provider && prev.FE.BuildID == lk.FE.BuildID {
		lk.FE = prev.FE
	}
	tree, err := render.Render(m)
	if err != nil {
		return nil, err
	}
	return &Prepared{Config: cfg, Solution: sol, Desire: des, PrevLock: prev, Lock: lk, IPs: ips, Model: m, Tree: tree, Phase: o.Phase}, nil
}

// checkImplemented rejects valid settings that this version cannot apply.
func checkImplemented(cfg *config.Config, sol *solution.Solution, des *render.Plan) error {
	errs := []error{cfg.CheckImplemented()}
	if cfg.Clock.Policy == "rtc" {
		errs = append(errs, fmt.Errorf("clock.policy rtc is %w (SP9); use ntp", config.ErrNotImplemented))
	}
	// A backend plug set that differs from the solution image needs a local image build (Phase 4).
	want := sol.DefaultBackendPlugs()
	got := map[string]string{}
	for _, p := range des.Plugs {
		if p.On && p.HasBackend() {
			got[p.ID] = p.Backend.Ref
		}
	}
	if cfg.Images.Care == "" && !sameSet(want, got) {
		errs = append(errs, fmt.Errorf("the backend plugs differ from the %s solution image; a local image build is %w (Phase 4). Set images.care to an image that has these plugs", sol.Name, config.ErrNotImplemented))
	}
	if len(got) > 0 && cfg.Solution.Track == "custom" && cfg.Solution.Custom.CareRef != "" && cfg.Images.Care == "" {
		errs = append(errs, fmt.Errorf("solution.custom.care_ref with backend plugs needs a local image build, which is %w (Phase 4)", config.ErrNotImplemented))
	}
	return errors.Join(errs...)
}

func sameSet(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
