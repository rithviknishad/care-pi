package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/backup"
	"github.com/rithviknishad/care-pi/agent/internal/buildinfo"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/s3admin"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
	"github.com/rithviknishad/care-pi/agent/internal/window"
)

// Defaults of the update machine.
const (
	// DefaultMigrateTimeout bounds the start of care-beat (it runs the migrations).
	DefaultMigrateTimeout = 20 * time.Minute
	// DefaultStartTimeout bounds the start of care-worker and care-api.
	DefaultStartTimeout = 15 * time.Minute
)

// Updater runs the update machine. The service and the CLI make one for each call.
type Updater struct {
	Paths        paths.Paths
	ConfigPath   string
	SolutionsDir string
	Host         *host.Host
	IPs          netinfo.Source
	S3           s3admin.Admin
	Chown        bool
	Now          func() time.Time
	// Location is the time zone of the host (time.Local when nil). Timezone is its IANA name.
	Location *time.Location
	Timezone string
	HTTP     *http.Client
	// NewCheckpointer returns the checkpoint of a job (label: the job id). nil uses BackupCheckpointer.
	NewCheckpointer func(label string) Checkpointer
	// Verifier checks Care after a cutover. nil uses HTTPVerifier.
	Verifier Verifier
	// ApplyHook changes the apply options (tests).
	ApplyHook      func(*apply.Options)
	MigrateTimeout time.Duration
	StartTimeout   time.Duration
	// AgentVersion is the version of this agent (buildinfo.Version when "").
	AgentVersion string
	// Ready reports whether the setup state is ready. nil: the apply phase of the host is full.
	Ready func() bool
}

func (u *Updater) ready() bool {
	if u.Ready != nil {
		return u.Ready()
	}
	return setup.Phase(u.Paths) == setup.PhaseFull
}

func (u *Updater) now() time.Time {
	if u.Now == nil {
		return time.Now().UTC()
	}
	return u.Now().UTC()
}

func (u *Updater) loc() *time.Location {
	if u.Location != nil {
		return u.Location
	}
	return time.Local
}

func (u *Updater) agent() string {
	if u.AgentVersion != "" {
		return u.AgentVersion
	}
	return buildinfo.Version
}

func (u *Updater) configPath() string {
	if u.ConfigPath != "" {
		return u.ConfigPath
	}
	return u.Paths.Abs(paths.ConfigFile)
}

func (u *Updater) client() Client { return Client{Paths: u.Paths, HTTP: u.HTTP, Now: u.Now} }

func (u *Updater) store() solution.LockStore {
	return solution.LockStore{Paths: u.Paths, Dir: u.SolutionsDir}
}

// Checkpointer returns the checkpoint of a job (label: the job id).
func (u *Updater) Checkpointer(label string) Checkpointer { return u.checkpointer(label) }

func (u *Updater) checkpointer(label string) Checkpointer {
	if u.NewCheckpointer != nil {
		return u.NewCheckpointer(label)
	}
	return BackupCheckpointer{Ops: backup.Ops{Env: backup.Env{Paths: u.Paths, Runner: u.Host.Runner, Systemd: u.Host.Systemd,
		Podman: u.Host.Podman, Now: u.Now}}, Label: label, Now: u.Now}
}

func (u *Updater) verifier() Verifier {
	if u.Verifier != nil {
		return u.Verifier
	}
	return HTTPVerifier{Paths: u.Paths}
}

// applyOptions returns the options of the apply helpers.
func (u *Updater) applyOptions(out io.Writer) apply.Options {
	o := apply.Options{Paths: u.Paths, ConfigPath: u.configPath(), SolutionsDir: u.SolutionsDir, Host: u.Host, IPs: u.IPs,
		S3: u.S3, Chown: u.Chown, Out: out, LockHeld: true, Phase: setup.PhaseFull}
	if u.Now != nil {
		o.Now = u.Now
	}
	if u.ApplyHook != nil {
		u.ApplyHook(&o)
	}
	return o
}

// Config loads config.yaml.
func (u *Updater) Config() (*config.Config, error) { return config.Load(u.configPath()) }

// Window returns updates.window.
func windowOf(cfg *config.Config) window.Window {
	return window.Window{Days: cfg.Updates.Window.Days, Start: cfg.Updates.Window.Start, End: cfg.Updates.Window.End}
}

// WindowOpen reports whether the update window is open now, and the next opening.
func (u *Updater) WindowOpen(cfg *config.Config) (open bool, closes, next time.Time) {
	w := windowOf(cfg)
	now := u.now()
	if s, ok := w.Current(now, u.loc()); ok {
		open, closes = true, s.Close
	}
	if s, ok := w.Next(now, u.loc()); ok {
		next = s.Open
	}
	return open, closes, next
}

// WindowClosed returns update-window-closed when the window is closed, else nil.
func (u *Updater) WindowClosed(cfg *config.Config) error {
	open, _, next := u.WindowOpen(cfg)
	if open {
		return nil
	}
	w := windowOf(cfg)
	p := problem.Newf(problem.UpdateWindowClosed, "The window opens on %s at %s (%s). Send now=true to update now.",
		strings.Join(w.Days, ", "), w.Start, u.tzName())
	if !next.IsZero() {
		t := next.UTC()
		p.NextWindowAt = &t
	}
	return p
}

func (u *Updater) tzName() string {
	if u.Timezone != "" {
		return u.Timezone
	}
	return u.loc().String()
}

// ---- targets ----

// Target is what an update installs.
type Target struct {
	Kind string
	// Lock is the target solution lock; nil for a custom target without a base.
	Lock  *solution.Lock
	Entry *solution.IndexLock
}

// Version returns the version of the target, or "".
func (t *Target) Version() string {
	if t == nil || t.Lock == nil {
		return ""
	}
	return solution.NormalizeLockVersion(t.Lock.Version)
}

// index returns the cached index, or, with network, the index of the URL.
func (u *Updater) index(ctx context.Context, cfg *config.Config, network bool) (*solution.Index, error) {
	ix, err := solution.CachedIndex(u.Paths)
	if err == nil && ix != nil {
		return ix, nil
	}
	if !network {
		return nil, errNoIndex
	}
	return u.client().FetchIndex(ctx, IndexURL(cfg.Updates.IndexURL))
}

// lockOf returns the solution lock of a version: the cache, the agent, or (with network) the index.
func (u *Updater) lockOf(ctx context.Context, ix *solution.Index, name, version string, network bool) (*solution.Lock, *solution.IndexLock, error) {
	var entry *solution.IndexLock
	if s := ix.Find(name); s != nil {
		entry = s.Lock(version)
	}
	if l, _, err := u.store().Get(name, version); err == nil {
		if entry == nil || entry.SHA256 == l.SHA256 {
			return l, entry, nil
		}
	}
	if entry == nil {
		return nil, nil, problem.Newf(problem.LockNotFound, "%s has no lock %s in the index, the cache, or the agent.", name, version)
	}
	if !network {
		return nil, entry, problem.Newf(problem.BuildRequiredOffline, "The lock %s %s is not in the cache. Run care-pi update check.", name, version)
	}
	l, err := u.client().FetchLock(ctx, ix, name, *entry)
	return l, entry, err
}

// newest returns the newest installable version of the index (or of the store without an index).
func (u *Updater) newest(ix *solution.Index, name string) string {
	if s := ix.Find(name); s != nil {
		if n := s.Newest(); n != nil {
			return n.Version
		}
		return ""
	}
	for _, v := range u.store().Versions(name) {
		return v
	}
	return ""
}

// ResolveTarget returns the target of the track (docs/solutions.md, section 7). version picks a lock
// (see POST /updates/apply). lockFile installs a lock file (CLI only).
func (u *Updater) ResolveTarget(ctx context.Context, cfg *config.Config, version, lockFile string, network bool) (*Target, error) {
	name := cfg.Solution.Name
	if lockFile != "" {
		data, err := os.ReadFile(lockFile)
		if err != nil {
			return nil, problem.Newf(problem.LockNotFound, "Cannot read %s: %v", lockFile, err)
		}
		l, err := u.client().Accept(data, name, "", "")
		if err != nil {
			return nil, err
		}
		return u.checkAgent(&Target{Kind: "lock", Lock: l})
	}
	ix, _ := u.index(ctx, cfg, network)
	track := cfg.Solution.Track
	switch {
	case version != "" && track == "stable":
		if n := u.newest(ix, name); n != "" && solution.CompareLockVersions(n, version) != 0 {
			p := problem.Newf(problem.InvalidRequest, "On the track stable only the newest lock %s is possible. Pin the track to install %s.", n, version)
			p.Errors = []problem.FieldError{{Pointer: "/version", Message: "use the newest lock or pin the track", Code: "stable"}}
			return nil, p
		}
	case version == "" && track == "pinned":
		version = cfg.Solution.Pin
	case version == "" && track == "custom":
		version = cfg.Solution.Pin
	}
	if version == "" {
		version = u.newest(ix, name)
	}
	if version == "" {
		if track == "custom" {
			return &Target{Kind: "custom"}, nil
		}
		return nil, problem.Newf(problem.NoUpdateAvailable, "The index and the agent have no lock of %s.", name)
	}
	l, entry, err := u.lockOf(ctx, ix, name, version, network)
	if err != nil {
		return nil, err
	}
	kind := "lock"
	if track == "custom" {
		kind = "custom"
	}
	return u.checkAgent(&Target{Kind: kind, Lock: l, Entry: entry})
}

func (u *Updater) checkAgent(t *Target) (*Target, error) {
	if t.Lock == nil {
		return t, nil
	}
	if old, _ := solution.AgentTooOld(u.agent(), t.Lock.MinAgentVersion); old {
		p := problem.Newf(problem.AgentTooOld, "%s %s needs agent %s or newer. This agent is %s. Run the installer again.",
			t.Lock.Name, t.Lock.Version, t.Lock.MinAgentVersion, u.agent())
		p.MinAgentVersion = t.Lock.MinAgentVersion
		return t, p
	}
	return t, nil
}

// Prepare renders the target. A custom target resolves its tracked overrides with refresh.
func (u *Updater) Prepare(ctx context.Context, t *Target, refresh bool, out io.Writer) (*apply.Prepared, error) {
	o := apply.PrepareOptions{Paths: u.Paths, ConfigPath: u.configPath(), SolutionsDir: u.SolutionsDir, Host: u.Host,
		IPs: u.IPs, Phase: setup.PhaseFull, Strict: true, Warn: out}
	if t != nil {
		o.SolutionLock = t.Lock
	}
	if t != nil && t.Kind == "custom" {
		o.Update = refresh
	} else {
		o.Offline = true
	}
	pr, err := apply.Prepare(ctx, o)
	if err != nil {
		return nil, err
	}
	return pr, nil
}

// TargetID returns the first 16 hex digits of the SHA-256 of the components and images of a lock.
func TargetID(l *lock.Lock) string {
	data, _ := yaml.Marshal(map[string]any{"components": l.Components, "images": l.Images, "fe": l.FE.BuildID})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16]
}

// changedComponents returns the ids of the components that differ between two locks.
func changedComponents(cur, tgt *lock.Lock) []string {
	var out []string
	if cur == nil {
		cur = &lock.Lock{}
	}
	if cur.Images["care"].Pull() != tgt.Images["care"].Pull() {
		out = append(out, "care")
	}
	if cur.Git["care_fe"].SHA != tgt.Git["care_fe"].SHA {
		out = append(out, "care_fe")
	}
	for _, n := range []string{"postgres", "caddy", "redis", "s3", "node"} {
		if cur.Images[n].Pull() != tgt.Images[n].Pull() && (cur.Images[n].Ref != "" || tgt.Images[n].Ref != "") {
			out = append(out, "images."+n)
		}
	}
	ids := map[string]bool{}
	for id := range cur.Components.Plugs {
		ids[id] = true
	}
	for id := range tgt.Components.Plugs {
		ids[id] = true
	}
	var sorted []string
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	for _, id := range sorted {
		a, b := cur.Components.Plugs[id], tgt.Components.Plugs[id]
		if (a.Backend == nil) != (b.Backend == nil) || (a.Backend != nil && (a.Backend.SHA != b.Backend.SHA || a.Enabled != b.Enabled)) {
			out = append(out, "plugs."+id+".backend")
		}
		if (a.Frontend == nil) != (b.Frontend == nil) || (a.Frontend != nil && (a.Frontend.SHA != b.Frontend.SHA || a.Frontend.URL != b.Frontend.URL || a.Enabled != b.Enabled)) {
			out = append(out, "plugs."+id+".frontend")
		}
	}
	return out
}

// pathOf returns the change path of a target: cutover when the plan stops or restarts Care.
func pathOf(ctx context.Context, o apply.Options, pr *apply.Prepared) (string, error) {
	pl, err := apply.PlanFor(ctx, o, pr)
	if err != nil {
		return "", err
	}
	careChanged := apply.CareChanged(pr.PrevLock, pr.Lock)
	switch {
	case pr.CareBuild != nil && careChanged:
		return PathBuildAndCutover, nil
	case careChanged, len(lock.ImagesChanged(pr.PrevLock, pr.Lock)) > 0 && !onlyNode(lock.ImagesChanged(pr.PrevLock, pr.Lock)),
		hasAction(pl.Actions, "full-cycle"), hasAction(pl.Actions, "cutover"):
		return PathCutover, nil
	case hasAction(pl.Actions, "restart"):
		return PathRestart, nil
	case hasAction(pl.Actions, "plug-config"), hasAction(pl.Actions, "ensure-fe"), hasAction(pl.Actions, "write-lock"), hasAction(pl.Actions, "write-files"):
		return PathPlugConfig, nil
	}
	return PathNone, nil
}

func onlyNode(names []string) bool {
	for _, n := range names {
		if n != "node" {
			return false
		}
	}
	return true
}

func hasAction(as []planAction, kind string) bool {
	for _, a := range as {
		if string(a.Kind) == kind {
			return true
		}
	}
	return false
}

// downtime estimates the time with the maintenance page (01 section 8): stop 20 s, checkpoint 30 s,
// migrate 60 s, start 45 s.
func downtime(path string) int {
	switch path {
	case PathCutover, PathBuildAndCutover:
		return 160
	case PathRestart:
		return 45
	}
	return 0
}

// ---- helpers ----

// isDone reports whether a job of this kind runs.
func runningJob(p paths.Paths, kinds ...string) *jobs.Job {
	return jobs.Latest(p, func(j *jobs.Job) bool {
		if j.State != jobs.StateRunning {
			return false
		}
		for _, k := range kinds {
			if j.Kind == k {
				return true
			}
		}
		return false
	})
}

// UpdateKinds are the kinds of the Phase 4 jobs.
var UpdateKinds = []string{jobs.KindUpdateCheck, jobs.KindUpdateStage, jobs.KindUpdateApply, jobs.KindUpdateRollback,
	jobs.KindOSUpdate, jobs.KindPlugChange}

func stagingDir(p paths.Paths, id string) string { return p.Abs(paths.StagingDir + "/" + id) }

// writeStaging writes the target tree and its lock into state/staging/<target id>/.
func writeStaging(p paths.Paths, id string, pr *apply.Prepared) error {
	dir := stagingDir(p, id)
	if err := os.RemoveAll(filepath.Join(dir, "tree")); err != nil {
		return err
	}
	for _, hp := range pr.Tree.Paths() {
		f := pr.Tree[hp]
		dst := filepath.Join(dir, "tree", filepath.FromSlash(hp))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(dst, f.Content, 0o600); err != nil {
			return err
		}
	}
	data, err := pr.Lock.Marshal()
	if err != nil {
		return err
	}
	return lock.WriteFileAtomic(filepath.Join(dir, "lock.yaml"), data, 0o600)
}

// writePrevious copies the tree that runs now into state/staging/<target id>/previous/.
func writePrevious(p paths.Paths, id string) error {
	snap, err := apply.Snapshot(p)
	if err != nil {
		return err
	}
	dir := filepath.Join(stagingDir(p, id), "previous")
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	for hp, data := range snap {
		dst := filepath.Join(dir, filepath.FromSlash(hp))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// pruneStaging keeps the staging directories of the targets in keep.
func pruneStaging(p paths.Paths, keep ...string) {
	ents, err := os.ReadDir(p.Abs(paths.StagingDir))
	if err != nil {
		return
	}
	k := map[string]bool{}
	for _, id := range keep {
		k[id] = true
	}
	for _, e := range ents {
		if !k[e.Name()] {
			_ = os.RemoveAll(filepath.Join(p.Abs(paths.StagingDir), e.Name()))
		}
	}
}

func errorsAs(err error) *problem.Problem {
	var p *problem.Problem
	if errors.As(err, &p) {
		return p
	}
	return nil
}
