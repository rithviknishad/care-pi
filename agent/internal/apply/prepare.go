// Package apply runs "care-pi apply": validate, resolve, render, diff, write, reload, restart.
package apply

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/buildinfo"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/firewall"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/render"
	"github.com/rithviknishad/care-pi/agent/internal/secrets"
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
	// SolutionLock is the solution lock of the tree (Phase 4), or nil (the refs of solution.yaml).
	SolutionLock *solution.Lock
	// CareBuild is the local image build of care (track custom), or nil.
	CareBuild *render.CareBuild
	// CareSource is the local build of CARE from its git source (S71), or nil.
	CareSource *render.CareSource
	// DistBase resolves the artefact references of SolutionLock.
	DistBase string
	// ACMEDNS is the value of the podman secret care-pi-acme-dns (secrets/acme_dns.json), or "" (Phase 6).
	ACMEDNS string
	// EnsureCA is true when the root of the agent does not exist and a LAN address is known (Phase 6).
	EnsureCA bool
	// HostAddrs are the IPv4 addresses of the host (netinfo.HostIPv4), the Tailscale address too.
	HostAddrs []string
	// TelemetryPassword is the value of the podman secret care-pi-telemetry-password, or "" (Phase 7).
	TelemetryPassword string
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
	// SolutionLock is the target solution lock of an update (Phase 4). nil: the solution lock of the
	// running on-device lock, or of the track (docs/solutions.md, section 7).
	SolutionLock *solution.Lock
	// PinnedLock is an on-device lock to install as it is, for a rollback. Nothing resolves.
	PinnedLock *lock.Lock
	// PlugsFromLock turns each plug on or off as components.plugs of PinnedLock says, whatever
	// solution.plugs of config.yaml says (the rollback of a failed plug change).
	PlugsFromLock bool
}

// Prepare loads the config, the solution, and the lock, resolves refs, and renders the tree.
func Prepare(ctx context.Context, o PrepareOptions) (*Prepared, error) {
	cfg, err := config.Load(o.ConfigPath)
	if err != nil {
		return nil, exitcode.With(exitcode.BadInput, err)
	}
	prev, err := lock.Load(o.Paths)
	if err != nil {
		return nil, err
	}
	if o.PinnedLock != nil && o.PlugsFromLock {
		for id, c := range o.PinnedLock.Components.Plugs {
			if cfg.Solution.Plugs == nil {
				cfg.Solution.Plugs = map[string]config.PlugChoice{}
			}
			ch, on := cfg.Solution.Plugs[id], c.Enabled
			ch.Enabled = &on
			cfg.Solution.Plugs[id] = ch
		}
	}
	if o.Phase == "" {
		o.Phase = setup.Phase(o.Paths)
	}
	bootstrap := o.Phase == setup.PhaseBootstrap
	store := solution.LockStore{Paths: o.Paths, Dir: o.SolutionsDir}
	sl := o.SolutionLock
	if sl == nil && !bootstrap {
		pinned := o.PinnedLock
		if pinned == nil {
			pinned = prev
		}
		if sl, err = trackLock(cfg, pinned, store, o.PinnedLock != nil); err != nil {
			return nil, exitcode.With(exitcode.BadInput, err)
		}
	}
	if sl != nil && sl.Name != cfg.Solution.Name {
		return nil, exitcode.With(exitcode.BadInput, fmt.Errorf("the solution lock is for %s, but solution.name is %s", sl.Name, cfg.Solution.Name))
	}
	var sol *solution.Solution
	if sl != nil {
		sol = sl.Solution
	} else if sol, err = solution.Load(cfg.Solution.Name, o.SolutionsDir); err != nil {
		return nil, exitcode.With(exitcode.BadInput, err)
	}
	plugSecrets, err := secrets.Store{Paths: o.Paths}.AllPlugs()
	if err != nil {
		return nil, exitcode.With(exitcode.Failed, err)
	}
	r := lock.Resolver{OS: "linux", Arch: runtime.GOARCH, Offline: o.Offline, Progress: o.Progress}
	if o.Host != nil {
		r.Skopeo, r.Git = o.Host.Skopeo, o.Host.Git
	}
	if o.Warn != nil {
		r.Warn = func(m string) { _, _ = fmt.Fprintln(o.Warn, "care-pi: warning:", m) }
	}
	var gc *gitCare
	if !bootstrap && o.PinnedLock == nil {
		if sol, gc, err = resolveGitCare(ctx, o, cfg, sol, sl, prev, r, o.Update); err != nil {
			if o.Resolved != nil {
				o.Resolved(err)
			}
			return nil, err
		}
	}
	var careSource *render.CareSource
	if gc != nil {
		careSource = gc.Source
	}
	var cb *render.CareBuild
	if !bootstrap && o.PinnedLock == nil {
		if cb, err = careBuild(ctx, cfg, sol, sl, prev, r, o.Update); err != nil {
			if o.Resolved != nil {
				o.Resolved(err)
			}
			return nil, err
		}
	} else if o.PinnedLock != nil {
		cb = pinnedBuild(o.PinnedLock, cfg)
	}
	des, err := render.DesireLock(cfg, sol, sl, cb)
	if err != nil {
		return nil, exitcode.With(exitcode.BadInput, err)
	}
	if bootstrap {
		// Setup mode runs only Caddy, so it resolves only the Caddy image.
		des.Wants.Images = map[string]string{"caddy": des.Wants.Images["caddy"]}
		des.Wants.Git = map[string]lock.GitWant{}
		des.Wants.Plugs = map[string]lock.GitWant{}
	}
	if o.Strict {
		if err := checkImplemented(o.Paths, cfg, sol, des); err != nil {
			return nil, exitcode.With(exitcode.BadInput, err)
		}
	}
	var lk *lock.Lock
	if o.PinnedLock != nil {
		cp := *o.PinnedLock
		lk = &cp
		if o.Resolved != nil {
			o.Resolved(nil)
		}
	} else {
		seedSL := sl
		if bootstrap && seedSL == nil {
			// Setup mode resolves only the Caddy image. The solution lock of the track gives its digest,
			// so a first boot without internet (the Raspberry Pi image) needs no registry.
			seedSL, _ = trackLock(cfg, prev, store, false)
		}
		seed := seedLock(seedSL, des.Wants, prev, cb, o.Update && cfg.Solution.Track == "custom")
		update := o.Update
		if sl != nil {
			// The locks of stable and pinned are pinned: apply --update resolves nothing (section 7.5).
			update = false
		}
		lk, err = r.Resolve(ctx, des.Wants, seed, update)
		if o.Resolved != nil {
			o.Resolved(err)
		}
		if err != nil {
			return nil, err
		}
	}
	lk.AgentVersion = buildinfo.Version
	lk.GeneratedAt = ""
	if prev != nil {
		lk.GeneratedAt = prev.GeneratedAt
		lk.History = prev.History
		lk.FileVersion = prev.FileVersion
	}
	ips, err := netinfo.LANIPv4(o.IPs)
	if err != nil {
		return nil, fmt.Errorf("cannot read the network interfaces: %w", err)
	}
	conn := readConnectivity(o.Paths, cfg, o.IPs, ips, time.Now())
	// Phase 7: the basic auth password of the telemetry (docs/telemetry.md), only with telemetry on.
	var telemetryPassword string
	if cfg.Telemetry.Enabled {
		if data, err := os.ReadFile(o.Paths.Abs(paths.TelemetryPasswordFile)); err == nil {
			telemetryPassword = strings.TrimSpace(string(data))
		}
	}
	m, err := render.NewModel(render.Input{Config: cfg, Solution: sol, Lock: lk, IPs: ips, Bootstrap: bootstrap,
		SolutionLock: sl, PlugSecrets: plugSecrets, CareBuild: cb, CareSource: careSource,
		CARoot: conn.caRoot, PublicIssued: conn.publicIssued, ACMEDNS: conn.acmeDNS != "", TailscaleIP: conn.tailscaleIP,
		TelemetryPassword: telemetryPassword != "", FirewallDropConfirmed: firewall.Read(o.Paths).DropConfirmed()})
	if err != nil {
		return nil, exitcode.With(exitcode.BadInput, err)
	}
	distBase := solution.DistBase(o.Paths)
	if o.PinnedLock == nil {
		fillComponents(lk, cfg, des, sl, cb, m, distBase)
		if c := lk.Components.Care; gc != nil && c != nil && (cb != nil || sl == nil) {
			// A git source (S71): the component names the ref and the commit, not the tag of the image.
			c.Ref, c.GitSHA, c.Tracked = gc.Ref, gc.SHA, !host.IsFullSHA(gc.Ref)
		}
		lk.FE = lock.FE{Provider: cfg.FE.Provider, BuildID: m.FEBuildID}
		// Keep the record of the build (url, sha256, built_at) while the build id is the same.
		if prev != nil && prev.FE.Provider == lk.FE.Provider && prev.FE.BuildID == lk.FE.BuildID {
			lk.FE = prev.FE
		}
	}
	tree, err := render.Render(m)
	if err != nil {
		return nil, err
	}
	return &Prepared{Config: cfg, Solution: sol, Desire: des, PrevLock: prev, Lock: lk, IPs: ips, Model: m, Tree: tree,
		Phase: o.Phase, SolutionLock: sl, CareBuild: cb, CareSource: careSource, DistBase: distBase,
		ACMEDNS: conn.acmeDNS, EnsureCA: conn.caRoot && !conn.caExists, HostAddrs: netinfo.HostIPv4(o.IPs),
		TelemetryPassword: telemetryPassword}, nil
}

// CheckImplemented runs the apply-time checks on a config before it is installed (the pre-seed of the image): the
// features that this version does not have, and the modes that need a registration first.
func CheckImplemented(p paths.Paths, cfg *config.Config) error {
	return checkImplemented(p, cfg, nil, nil)
}

// checkImplemented rejects valid settings that this version cannot apply, and settings that need a step
// of an enrol job first (Phase 6).
func checkImplemented(p paths.Paths, cfg *config.Config, _ *solution.Solution, _ *render.Plan) error {
	errs := []error{cfg.CheckImplemented()}
	if render.PublicHost(cfg) != "" {
		if _, err := os.Stat(p.Abs(paths.ACMEDNSFile)); err != nil {
			errs = append(errs, fmt.Errorf("tls.public.enabled is true, but this device has no registration with the DNS service (%s); run: care-pi tls public enable", paths.ACMEDNSFile))
		}
	}
	return errors.Join(errs...)
}
