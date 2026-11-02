// Package plan compares the rendered tree with the files on disk and decides the
// smallest set of actions (plans/03-solutions-releases-updates.md section 5).
package plan

import (
	"bytes"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/render"
)

// Kind is the type of an action.
type Kind string

// Action kinds in execution order.
const (
	EnsureDirs   Kind = "ensure-dirs"
	SetSecrets   Kind = "set-secrets"
	WriteFiles   Kind = "write-files"
	DeleteFiles  Kind = "delete-files"
	DaemonReload Kind = "daemon-reload"
	// EnableTarget enables care-start.service, which starts care.target at boot.
	EnableTarget Kind = "enable-target"
	PullImages   Kind = "pull-images"
	FullCycle    Kind = "full-cycle"
	StartTarget  Kind = "start-target"
	Restart      Kind = "restart"
	ReloadCaddy  Kind = "reload-caddy"
	EnsureBucket Kind = "ensure-buckets"
	// EnsureStanza runs before EnsureFE: WAL archiving fails until the stanza exists,
	// and a local FE build takes minutes (Phase 1 exit test, finding 7).
	EnsureStanza Kind = "ensure-stanza"
	// EnsureMounts starts the automount units of the backup disks (docs/backups.md).
	EnsureMounts Kind = "ensure-mounts"
	// EnsureRepositories runs "restic init" of a new destination. A destination that does not
	// answer does not fail the apply: its timer tries again.
	EnsureRepositories Kind = "ensure-repositories"
	// EnsureTimers turns the backup timers on or off (backups.paused) and restarts a changed timer.
	EnsureTimers Kind = "ensure-timers"
	EnsureFE     Kind = "ensure-fe"
	WriteLock    Kind = "write-lock"
	// Cutover (Phase 4) replaces a restart or a full cycle when the care image or the set of backend
	// plugs that are on changes: maintenance page, stop, checkpoint, start (docs/solutions.md section 8).
	Cutover Kind = "cutover"
	// PlugConfig (Phase 4) writes the plug_config rows of Care (docs/solutions.md section 12.2).
	PlugConfig Kind = "plug-config"
	// EnsureCA (Phase 6) makes the name-constrained root of the agent when it does not exist (07 §4.1).
	// On a host with the root of Caddy, this is the one-time switch: care-caddy then restarts.
	EnsureCA Kind = "ensure-ca"
	// DisableUnits (Phase 6) stops and disables a timer before apply deletes its file (care-dns-update.timer).
	DisableUnits Kind = "disable-units"
	// ReloadFirewall (Phase 6) reloads care-pi-firewall.service after a change of its drop-in or of the table of the
	// agent (Phase 7). A change of the policy from accept to drop first starts the rollback timer.
	ReloadFirewall Kind = "reload-firewall"
	// UpdateDNS (Phase 6) sends a new LAN address to the DNS service (public mode). A failure is a warning.
	UpdateDNS Kind = "update-dns"
	// EnsureTelemetry (Phase 7) restarts the units of the opt-in telemetry after a change (Units: the image units
	// first) and stops them after telemetry.enabled false (Names). It never restarts the stack.
	EnsureTelemetry Kind = "ensure-telemetry"
)

// Action is one step of a plan.
type Action struct {
	Kind   Kind
	Units  []string
	Paths  []string
	Names  []string
	Reason string
}

func (a Action) String() string {
	var parts []string
	parts = append(parts, string(a.Kind))
	if len(a.Units) > 0 {
		parts = append(parts, strings.Join(a.Units, " "))
	}
	if len(a.Paths) > 0 {
		parts = append(parts, fmt.Sprintf("%d file(s)", len(a.Paths)))
	}
	if len(a.Names) > 0 {
		parts = append(parts, strings.Join(a.Names, " "))
	}
	s := strings.Join(parts, ": ")
	if a.Reason != "" {
		s += " (" + a.Reason + ")"
	}
	return s
}

// ChangeKind tells how a file changes.
type ChangeKind string

// File change kinds.
const (
	Add    ChangeKind = "add"
	Modify ChangeKind = "modify"
	Delete ChangeKind = "delete"
)

// Change is one file change.
type Change struct {
	Path string
	Kind ChangeKind
	Old  []byte
	New  []byte
}

// Input is everything that Build needs. Build does no I/O.
type Input struct {
	Desired render.Tree
	// Current holds the owned files on disk: host path to content.
	Current map[string][]byte
	// ImagesChanged lists the lock images whose digest changed.
	ImagesChanged []string
	// SecretsChanged lists the podman secrets that are missing or have a new value.
	// SecretUnits maps each podman secret to the units that read it.
	SecretsChanged []string
	SecretUnits    map[string][]string
	TargetActive   bool
	// PodActive is true when care-pod.service is active. FailedUnits lists the units of the stack in the state
	// failed: care.target stays active (it only wants its units) when the pod fails to start, for example
	// on an address that is not there.
	PodActive   bool
	FailedUnits []string
	// EnsureStale is true when the buckets or the stanza were not ensured for this config.
	EnsureStale bool
	LockChanged bool
	// FEWanted is true when the FE provider must produce a build that is not current.
	FEWanted bool
	// StackImages are the pull references of the stack, for PullImages.
	StackImages []string
	// Bootstrap is setup mode: no buckets, no stanza, and no FE build (no database and no S3 run).
	Bootstrap bool
	// MountsStale, ReposStale, and TimersStale tell that the last ensure of the backup mounts, the
	// restic repositories, or the backup timers was for another config (or never ran).
	MountsStale bool
	ReposStale  bool
	TimersStale bool
	// Cutover is true when the care image or the set of backend plugs that are on changes (Phase 4).
	// Then a running stack gets the action cutover in place of a restart or a full cycle.
	Cutover bool
	// PlugConfig is true when the plug_config rows of Care must change (Phase 4).
	PlugConfig bool
	// EnsureCA is true when the root of the agent does not exist yet (Phase 6).
	EnsureCA bool
	// PodLive is the care.pod of the running pod (state/pod-live.json), or nil. When the new care.pod only
	// removes PublishPort lines of the running pod (for example the address of eth0 after a Wi-Fi failover),
	// the running pod keeps its publishes and needs no full cycle (docs/connectivity.md).
	PodLive []byte
	// HostAddrs are the IPv4 addresses of the host now (netinfo.HostIPv4). A PublishPort line that the new care.pod
	// drops for an address that is still here (the Tailscale mode off with tls.tailscale.manage_daemon false) needs
	// a full cycle: else the running pod keeps the ports of that address.
	HostAddrs []string
	// DNSStale is true when the DNS service has another LAN address than the primary address (public mode).
	DNSStale bool
}

// Plan is the ordered list of actions and the file changes.
type Plan struct {
	Changes []Change
	Actions []Action
	First   bool
}

// Empty reports whether the plan does nothing.
func (p *Plan) Empty() bool { return len(p.Actions) == 0 }

// Has reports whether the plan has an action of kind k.
func (p *Plan) Has(k Kind) bool {
	for _, a := range p.Actions {
		if a.Kind == k {
			return true
		}
	}
	return false
}

// Get returns the first action of kind k.
func (p *Plan) Get(k Kind) (Action, bool) {
	for _, a := range p.Actions {
		if a.Kind == k {
			return a, true
		}
	}
	return Action{}, false
}

// restartOrder is the order of restarts: dependencies first, api last.
var restartOrder = []string{"care-s3", "care-caddy", "care-beat", "care-worker", "care-api"}

// Units whose change needs the whole stack to restart through care.target.
// care-db and care-redis: SP6 finding 6 (a single restart leaves dependents failed).
var cycleUnits = map[string]bool{"care-db": true, "care-redis": true}

// Units that a cutover of the backend plugs stops and starts (apply.AppUnits).
var cutoverUnits = map[string]bool{"care-beat": true, "care-worker": true, "care-api": true}

// Images that do not belong to a running unit.
var transientImages = map[string]bool{"node": true, "vmagent": true, "node-exporter": true}

// telemetryUnits are the units of the opt-in telemetry (Phase 7): a change restarts only them, never the stack.
var telemetryUnits = map[string]bool{"care-telemetry": true, "care-node-exporter": true}

// Diff compares the desired tree with the current files.
func Diff(desired render.Tree, current map[string][]byte) []Change {
	var out []Change
	for _, p := range desired.Paths() {
		f := desired[p]
		old, ok := current[p]
		switch {
		case !ok:
			out = append(out, Change{Path: p, Kind: Add, New: f.Content})
		case !bytes.Equal(old, f.Content):
			out = append(out, Change{Path: p, Kind: Modify, Old: old, New: f.Content})
		}
	}
	var stale []string
	for p := range current {
		if _, ok := desired[p]; !ok && render.Owned(p) {
			stale = append(stale, p)
		}
	}
	sort.Strings(stale)
	for _, p := range stale {
		out = append(out, Change{Path: p, Kind: Delete, Old: current[p]})
	}
	return out
}

// unitOf returns the service unit name for a quadlet path, for example care-api.
func unitOf(p string) string {
	base := path.Base(p)
	if base == "care.pod" {
		return "care-pod"
	}
	return strings.TrimSuffix(base, path.Ext(base))
}

// Build computes the plan.
func Build(in Input) *Plan {
	pl := &Plan{Changes: Diff(in.Desired, in.Current)}
	_, hadTarget := in.Current[paths.SystemdDir+"/care.target"]
	pl.First = !hadTarget

	var (
		writes, deletes []string
		unitFiles       bool
		podChanged      bool
		targetChanged   bool
		backendEnv      bool
		caddyConf       bool
		dbConf          bool
		mountsChanged   bool
		timers          []string
		backupConf      bool
		timersChanged   bool
		firewall        bool
		disable         []string
		dnsTimer        bool
		restart         = map[string]bool{}
		cycle           []string
		telRestart      = map[string]bool{}
		telImage        = map[string]bool{}
		telStop         = map[string]bool{}
	)
	if _, ok := in.Desired[paths.SystemdDir+"/"+render.DNSUpdateTimer]; ok {
		dnsTimer = true
	}
	for _, c := range pl.Changes {
		if c.Kind == Delete {
			deletes = append(deletes, c.Path)
		} else {
			writes = append(writes, c.Path)
		}
		dir := path.Dir(c.Path)
		switch {
		case dir == paths.QuadletDir:
			unitFiles = true
			u := unitOf(c.Path)
			switch {
			case telemetryUnits[u]:
				switch {
				case c.Kind == Delete:
					telStop[u] = true
				case path.Ext(c.Path) == ".image":
					telImage[u] = true
					telRestart[u] = true
				default:
					telRestart[u] = true
				}
			case u == "care-pod":
				podChanged = !podShrinkOnly(in.Desired[c.Path].Content, in.PodLive, in.PodActive, in.HostAddrs)
			case u == "care-fe-build", u == "care-plug-build", strings.HasPrefix(u, "care-build-"):
				// Transient unit: daemon-reload is enough.
			case cycleUnits[u]:
				cycle = append(cycle, u+" unit changed")
			default:
				restart[u] = true
			}
		case dir == paths.SystemdDir:
			unitFiles = true
			b := path.Base(c.Path)
			switch {
			case b == "care.target" || b == render.StartUnit:
				targetChanged = true
			case render.MountFile(c.Path):
				mountsChanged = true
			case render.BackupUnit(c.Path):
				if path.Ext(b) == ".timer" && c.Kind == Modify {
					timers = append(timers, b)
				} else if c.Kind != Modify || b == render.BackupTarget {
					backupConf = true
				}
			}
			if (strings.HasPrefix(b, "care-update-") || strings.HasPrefix(b, "care-dns-update.")) && c.Kind != Delete {
				timersChanged = true
			}
			if b == render.DNSUpdateTimer && c.Kind == Delete {
				disable = append(disable, b)
			}
		case c.Path == paths.FirewallDropIn, c.Path == paths.FirewallDropInLegacy:
			unitFiles = true
			firewall = true
		case strings.HasPrefix(c.Path, paths.RenderedFirewall+"/"):
			firewall = true
		case strings.HasPrefix(c.Path, paths.RenderedTelemetry+"/"):
			if c.Kind != Delete {
				telRestart["care-telemetry"] = true
			}
		case strings.HasPrefix(c.Path, paths.RenderedBackup+"/"):
			backupConf = true
		case c.Path == paths.RenderedCare+"/backend.env":
			backendEnv = true
		case strings.HasPrefix(c.Path, paths.RenderedCaddy+"/"):
			caddyConf = true
		case strings.HasPrefix(c.Path, paths.RenderedDB+"/"):
			// pgBackRest reads its config on each run: no restart, but check the stanza.
			dbConf = true
		}
	}
	if podChanged {
		cycle = append(cycle, "pod changed")
	}
	var stackImages []string
	for _, n := range in.ImagesChanged {
		if !transientImages[n] {
			stackImages = append(stackImages, n)
		}
	}
	if len(stackImages) > 0 {
		cycle = append(cycle, "image changed: "+strings.Join(stackImages, " "))
	}
	if backendEnv {
		restart["care-beat"] = true
	}
	for _, s := range in.SecretsChanged {
		for _, u := range in.SecretUnits[s] {
			if telemetryUnits[u] {
				telRestart[u] = true
				continue
			}
			if cycleUnits[u] {
				cycle = append(cycle, "secret "+s+" changed")
			} else {
				restart[u] = true
			}
		}
	}
	// A new root of the agent: Caddy must make a new intermediate, so it restarts (a reload keeps the old one).
	if in.EnsureCA && in.TargetActive && !pl.First {
		restart["care-caddy"] = true
	}
	// care-api and care-worker require care-beat, so a beat restart restarts them too.
	if restart["care-beat"] {
		restart["care-worker"] = true
		restart["care-api"] = true
	}

	add := func(a Action) { pl.Actions = append(pl.Actions, a) }
	anything := len(pl.Changes) > 0 || len(in.SecretsChanged) > 0
	failed := len(in.FailedUnits) > 0
	if anything || pl.First || !in.TargetActive || failed {
		add(Action{Kind: EnsureDirs})
	}
	if in.EnsureCA {
		add(Action{Kind: EnsureCA})
	}
	if len(in.SecretsChanged) > 0 {
		add(Action{Kind: SetSecrets, Names: in.SecretsChanged})
	}
	if len(writes) > 0 {
		add(Action{Kind: WriteFiles, Paths: writes})
	}
	if len(disable) > 0 {
		add(Action{Kind: DisableUnits, Units: disable})
	}
	if len(deletes) > 0 {
		add(Action{Kind: DeleteFiles, Paths: deletes})
	}
	if unitFiles {
		add(Action{Kind: DaemonReload})
	}
	if firewall {
		add(Action{Kind: ReloadFirewall, Units: []string{paths.FirewallUnit}})
	}
	if pl.First || targetChanged || timersChanged {
		units := []string{render.StartUnit}
		if timersChanged {
			units = append(units, render.TimerUnits...)
			if dnsTimer {
				units = append(units, render.DNSUpdateTimer)
			}
		}
		add(Action{Kind: EnableTarget, Units: units})
	}

	started := false
	switch {
	case pl.First:
		add(Action{Kind: PullImages, Names: in.StackImages, Reason: "first apply"})
		add(Action{Kind: StartTarget, Units: []string{"care.target"}, Reason: "first apply"})
		started = true
	case !in.TargetActive:
		if len(stackImages) > 0 {
			add(Action{Kind: PullImages, Names: in.StackImages, Reason: "image changed"})
		}
		add(Action{Kind: StartTarget, Units: []string{"care.target"}, Reason: "care.target is not active"})
		started = true
	case in.Cutover && (len(cycle) > 0 || len(restart) > 0):
		if len(stackImages) > 0 {
			add(Action{Kind: PullImages, Names: in.StackImages, Reason: "image changed"})
		}
		reasons := cycle
		if len(reasons) == 0 {
			reasons = []string{"backend plugs changed"}
		}
		add(Action{Kind: Cutover, Units: []string{"care.target"}, Reason: strings.Join(dedupe(reasons), "; ")})
		started = true
		if len(cycle) == 0 {
			// This cutover stops and starts only beat, worker, and api (apply.AppUnits); care-caddy serves the
			// maintenance page meanwhile. The other changed units restart after it, and a new Caddyfile loads:
			// a new name changes the backend plugs, the Caddyfile, and the unit of care-caddy together (Phase 6).
			var units []string
			for _, u := range restartOrder {
				if restart[u] && !cutoverUnits[u] {
					units = append(units, u+".service")
				}
			}
			if len(units) > 0 {
				add(Action{Kind: Restart, Units: units})
			}
			if caddyConf && !restart["care-caddy"] {
				add(Action{Kind: ReloadCaddy, Units: []string{"care-caddy.service"}})
			}
		}
	case len(cycle) > 0:
		if len(stackImages) > 0 {
			add(Action{Kind: PullImages, Names: in.StackImages, Reason: "image changed"})
		}
		add(Action{Kind: FullCycle, Units: []string{"care.target"}, Reason: strings.Join(dedupe(cycle), "; ")})
		started = true
	default:
		var units []string
		for _, u := range restartOrder {
			if restart[u] {
				units = append(units, u+".service")
			}
		}
		// A failed unit of the stack (the pod after a boot without one of its addresses): reset it and start
		// care.target again. A restart of the changed units follows, so the units that ran get the new files.
		if failed {
			add(Action{Kind: StartTarget, Units: []string{"care.target"}, Reason: "failed: " + strings.Join(in.FailedUnits, " ")})
			started = true
		}
		if len(units) > 0 {
			add(Action{Kind: Restart, Units: units})
			started = true
		}
		if caddyConf && !restart["care-caddy"] {
			add(Action{Kind: ReloadCaddy, Units: []string{"care-caddy.service"}})
		}
	}
	// The buckets and the stanza come right after the start: the start of care.target returns
	// when the API is healthy. The FE build comes last, because it takes minutes.
	if (started || in.EnsureStale) && !in.Bootstrap {
		add(Action{Kind: EnsureBucket})
	}
	if (started || in.EnsureStale || dbConf) && !in.Bootstrap {
		add(Action{Kind: EnsureStanza})
	}
	if !in.Bootstrap {
		if mountsChanged || in.MountsStale {
			add(Action{Kind: EnsureMounts})
		}
		if backupConf || in.ReposStale {
			add(Action{Kind: EnsureRepositories})
		}
		if len(timers) > 0 || backupConf || mountsChanged || in.TimersStale {
			add(Action{Kind: EnsureTimers, Units: timers})
		}
	}
	if in.FEWanted && !in.Bootstrap {
		add(Action{Kind: EnsureFE})
	}
	if in.PlugConfig && !in.Bootstrap {
		add(Action{Kind: PlugConfig})
	}
	if in.DNSStale && !in.Bootstrap {
		add(Action{Kind: UpdateDNS})
	}
	// Phase 7: the telemetry units restart alone (an image unit pulls first); after telemetry.enabled false they stop.
	if (len(telRestart) > 0 || len(telStop) > 0) && !in.Bootstrap {
		a := Action{Kind: EnsureTelemetry}
		for _, u := range []string{"care-node-exporter", "care-telemetry"} {
			if telImage[u] {
				a.Units = append(a.Units, u+"-image.service")
			}
		}
		for _, u := range []string{"care-node-exporter", "care-telemetry"} {
			if telRestart[u] {
				a.Units = append(a.Units, u+".service")
			} else if telStop[u] {
				a.Names = append(a.Names, u+".service")
			}
		}
		add(a)
	}
	if in.LockChanged {
		add(Action{Kind: WriteLock})
	}
	if len(pl.Actions) == 1 && pl.Actions[0].Kind == EnsureDirs {
		pl.Actions = nil
	}
	return pl
}

func dedupe(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// podShrinkOnly reports whether the new care.pod is the care.pod of the running pod without some of its
// publishes: each line of the new file is in the running file in the same order, and each line that the new
// file does not have is a PublishPort line, a comment, an empty line, or a line of the Tailscale wait.
// The running pod then keeps the old publishes: an address that went away (eth0 after a Wi-Fi failover)
// does no harm, and the pod needs no full cycle when the address comes back. A dropped PublishPort line of an
// address that is still on the host (hostAddrs) is not a shrink: the running pod would keep serving it (the
// Tailscale mode off while tailscaled keeps its address). active is the ActiveState of care-pod.service, not of
// care.target: a pod that failed has no publishes to keep.
func podShrinkOnly(desired, live []byte, active bool, hostAddrs []string) bool {
	if !active || live == nil || desired == nil {
		return false
	}
	present := map[string]bool{}
	for _, a := range hostAddrs {
		present[a] = true
	}
	want := strings.Split(strings.TrimSpace(string(desired)), "\n")
	have := strings.Split(strings.TrimSpace(string(live)), "\n")
	i := 0
	for _, l := range have {
		if i < len(want) && l == want[i] {
			i++
			continue
		}
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(t, "PublishPort="):
			if addr, _, ok := strings.Cut(strings.TrimPrefix(t, "PublishPort="), ":"); ok && present[addr] {
				return false
			}
		case t == "", strings.HasPrefix(t, "#"),
			t == "[Service]", t == "After=tailscaled.service",
			strings.HasPrefix(t, "ExecStartPre=-") && strings.Contains(t, " network wait-address "):
		default:
			return false
		}
	}
	return i == len(want)
}
