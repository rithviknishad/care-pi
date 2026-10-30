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
	EnsureFE     Kind = "ensure-fe"
	WriteLock    Kind = "write-lock"
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
	// EnsureStale is true when the buckets or the stanza were not ensured for this config.
	EnsureStale bool
	LockChanged bool
	// FEWanted is true when the FE provider must produce a build that is not current.
	FEWanted bool
	// StackImages are the pull references of the stack, for PullImages.
	StackImages []string
	// Bootstrap is setup mode: no buckets, no stanza, and no FE build (no database and no S3 run).
	Bootstrap bool
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

// Images that do not belong to a running unit.
var transientImages = map[string]bool{"node": true}

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
		restart         = map[string]bool{}
		cycle           []string
	)
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
			case u == "care-pod":
				podChanged = true
			case u == "care-fe-build":
				// Transient unit: daemon-reload is enough.
			case cycleUnits[u]:
				cycle = append(cycle, u+" unit changed")
			default:
				restart[u] = true
			}
		case dir == paths.SystemdDir:
			unitFiles = true
			if b := path.Base(c.Path); b == "care.target" || b == render.StartUnit {
				targetChanged = true
			}
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
			if cycleUnits[u] {
				cycle = append(cycle, "secret "+s+" changed")
			} else {
				restart[u] = true
			}
		}
	}
	// care-api and care-worker require care-beat, so a beat restart restarts them too.
	if restart["care-beat"] {
		restart["care-worker"] = true
		restart["care-api"] = true
	}

	add := func(a Action) { pl.Actions = append(pl.Actions, a) }
	anything := len(pl.Changes) > 0 || len(in.SecretsChanged) > 0
	if anything || pl.First || !in.TargetActive {
		add(Action{Kind: EnsureDirs})
	}
	if len(in.SecretsChanged) > 0 {
		add(Action{Kind: SetSecrets, Names: in.SecretsChanged})
	}
	if len(writes) > 0 {
		add(Action{Kind: WriteFiles, Paths: writes})
	}
	if len(deletes) > 0 {
		add(Action{Kind: DeleteFiles, Paths: deletes})
	}
	if unitFiles {
		add(Action{Kind: DaemonReload})
	}
	if pl.First || targetChanged {
		add(Action{Kind: EnableTarget, Units: []string{render.StartUnit}})
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
	if in.FEWanted && !in.Bootstrap {
		add(Action{Kind: EnsureFE})
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
