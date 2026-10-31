package apply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/applock"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/fe"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/plan"
	"github.com/rithviknishad/care-pi/agent/internal/render"
	"github.com/rithviknishad/care-pi/agent/internal/s3admin"
	"github.com/rithviknishad/care-pi/agent/internal/secrets"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
)

// Options controls one apply run.
type Options struct {
	Paths        paths.Paths
	ConfigPath   string
	SolutionsDir string
	DryRun       bool
	Update       bool
	Reason       string
	Out          io.Writer
	Host         *host.Host
	IPs          netinfo.Source
	S3           s3admin.Admin
	// S3Endpoint is the loopback publish of care-s3.
	S3Endpoint string
	// Chown sets owners on created files and directories. It needs root.
	Chown bool
	Now   func() time.Time
	// StopTimeout bounds the wait for units to become inactive after "systemctl stop".
	StopTimeout  time.Duration
	PollInterval time.Duration
	// DBReadyTimeout bounds the wait for care-db before the stanza step.
	DBReadyTimeout time.Duration
	// LockTimeout bounds the wait for the apply lock (applock). 0 waits without a limit.
	LockTimeout time.Duration
	// LockHeld tells that the caller holds the apply lock, for example bootstrap.
	LockHeld bool
	// Phase is "" (the phase of the host), setup.PhaseBootstrap, or setup.PhaseFull.
	Phase string
	// Journal gets one step for each action. The agent passes the recorder of its job.
	// When it is nil, apply writes its own job journal (source cli) when the plan has actions.
	Journal Journal
	// AddressWait bounds the wait for a LAN IPv4 address before the render. 0 does not wait.
	// The CLI (apply, bootstrap) waits: at the boot, the NetworkManager hook can run apply
	// before the DHCP lease. Without an address after the wait, apply renders without it and warns.
	AddressWait time.Duration
}

// Journal records the steps of a run.
type Journal interface {
	// Step starts a step. The returned function ends it with the error of the step.
	Step(name string) func(error)
}

// Result summarises a run.
type Result struct {
	Plan        *plan.Plan
	Changed     bool
	LockChanged bool
	Phase       string
	Solution    string
	Track       string
	Preset      string
	Hostnames   []string
	Addresses   []string
	Warnings    []string
	// FEBuildID is the FE build of the plan. FEProvider and FEExists describe it.
	FEBuildID  string
	FEProvider string
	FEExists   bool
	// FECurrent is the FE build that fe/current points at after the run, when the run changed it.
	FECurrent string
	// JobID is the journal of the run ("" without a journal).
	JobID string
}

// JobResult returns the result of an apply job.
func (r *Result) JobResult() jobs.ApplyResult {
	out := jobs.ApplyResult{Kind: jobs.KindApply}
	if r != nil {
		out.Changed, out.LockChanged = r.Changed, r.LockChanged
		if r.FECurrent != "" {
			id := r.FECurrent
			out.FEBuildID = &id
		}
	}
	return out
}

// Run executes apply.
func Run(ctx context.Context, o Options) (*Result, error) {
	o.defaults()
	addressWarning := waitAddress(ctx, o)
	if !o.LockHeld {
		release, err := applock.Acquire(ctx, o.Paths, o.LockTimeout, o.Out)
		if err != nil {
			return nil, err
		}
		defer release()
	}
	// The lock makes sure that no FE build runs now, so a drop-in is a leftover.
	if err := healScaleDown(ctx, o); err != nil {
		return nil, err
	}
	pr, err := Prepare(ctx, PrepareOptions{
		Paths: o.Paths, ConfigPath: o.ConfigPath, SolutionsDir: o.SolutionsDir,
		Host: o.Host, IPs: o.IPs, Update: o.Update, Strict: true, Warn: o.Out, Progress: o.Out, Phase: o.Phase,
	})
	if err != nil {
		return nil, err
	}
	provider, err := fe.New(pr.Config.FE.Provider, fe.Env{
		Config:     pr.Config,
		Paths:      o.Paths,
		CareFERepo: pr.Lock.Git["care_fe"].Repo,
		CareFESHA:  pr.Lock.Git["care_fe"].SHA,
		BuildEnv:   pr.Desire.Frontend,
		Host:       o.Host,
		Out:        o.Out,
		// The build log goes to the stdout of care-pi, so follow it only when Out is stdout.
		Follow: o.Out == io.Writer(os.Stdout),
	})
	if err != nil {
		return nil, exitcode.With(exitcode.BadInput, err)
	}

	current, err := readOwned(o.Paths)
	if err != nil {
		return nil, err
	}
	secretValues, err := podmanSecretValues(o.Paths)
	if err != nil {
		return nil, exitcode.With(exitcode.Failed, err)
	}
	secretsChanged, err := changedSecrets(ctx, o, secretValues)
	if err != nil {
		return nil, err
	}
	active, err := targetActive(ctx, o.Host)
	if err != nil {
		return nil, err
	}
	ensure := ensureRecord(pr)
	stale := !readEnsure(o.Paths).equal(ensure)
	lockChanged := !lock.Equal(pr.PrevLock, pr.Lock)
	feWanted := pr.Config.FE.Provider != "none" && pr.Model.FEBuildID != "" && fe.Current(o.Paths) != pr.Model.FEBuildID &&
		pr.Phase != setup.PhaseBootstrap

	units := map[string][]string{}
	for _, s := range secrets.PodmanSecrets() {
		units[s.Name] = s.Units
	}
	var stackImages []string
	for _, n := range sortedImageNames(pr.Model.Images) {
		if n != "node" {
			stackImages = append(stackImages, pr.Model.Images[n])
		}
	}
	p := plan.Build(plan.Input{
		Desired:        pr.Tree,
		Current:        current,
		ImagesChanged:  lock.ImagesChanged(pr.PrevLock, pr.Lock),
		SecretsChanged: secretsChanged,
		SecretUnits:    units,
		TargetActive:   active,
		EnsureStale:    stale,
		LockChanged:    lockChanged,
		FEWanted:       feWanted,
		StackImages:    stackImages,
		Bootstrap:      pr.Phase == setup.PhaseBootstrap,
	})
	res := &Result{Plan: p, Changed: !p.Empty(), LockChanged: lockChanged, Phase: pr.Phase,
		Solution: pr.Config.Solution.Name, Track: pr.Config.Solution.Track, Preset: pr.Model.Sizing.Preset,
		Hostnames: pr.Model.Hostnames, Addresses: pr.IPs, FEBuildID: pr.Model.FEBuildID, FEProvider: provider.Name()}
	if addressWarning != "" && len(pr.IPs) == 0 {
		res.Warnings = append(res.Warnings, addressWarning)
	}
	if p.Has(plan.EnsureFE) {
		res.FEExists = fe.Exists(o.Paths, pr.Model.FEBuildID)
	}

	printSummary(o.Out, pr)
	if pr.Phase == setup.PhaseBootstrap {
		_, _ = fmt.Fprintln(o.Out, "setup mode: apply renders only care.pod, care-caddy, and the start units; finish the setup in the console (https://<host>:8443/) or with \"care-pi setup finish\"")
	}
	if o.Paths.Root == "/" {
		if h, err := os.Hostname(); err == nil && strings.SplitN(h, ".", 2)[0] != pr.Config.Identity.Hostname {
			w := fmt.Sprintf("identity.hostname is %q but the OS host name is %q; avahi announces the OS host name (only the setup finish and the installer change it)", pr.Config.Identity.Hostname, h)
			res.Warnings = append(res.Warnings, w)
			_, _ = fmt.Fprintln(o.Out, "warning:", w)
		}
	}
	if p.Empty() {
		_, _ = fmt.Fprintln(o.Out, "no changes")
		return res, nil
	}
	printPlan(o.Out, p)
	if p.Has(plan.EnsureFE) {
		if fe.Exists(o.Paths, pr.Model.FEBuildID) {
			_, _ = fmt.Fprintf(o.Out, "FE build %s exists; apply points fe/current at it\n", pr.Model.FEBuildID)
		} else {
			_, _ = fmt.Fprintf(o.Out, "FE build %s needed (provider %s)\n", pr.Model.FEBuildID, provider.Name())
		}
	}
	if o.DryRun {
		for _, c := range p.Changes {
			a, b := c.Old, c.New
			if c.Kind == plan.Add {
				a = nil
			}
			if c.Kind == plan.Delete {
				b = nil
			}
			_, _ = fmt.Fprint(o.Out, plan.UnifiedDiff(c.Path, a, b))
		}
		_, _ = fmt.Fprintln(o.Out, "dry run: nothing was written")
		return res, nil
	}

	ex := &executor{o: o, pr: pr, plan: p, secretValues: secretValues, ensure: ensure, provider: provider}
	journal := o.Journal
	var rec *jobs.Recorder
	if journal == nil {
		rec, err = jobs.Create(jobs.CreateOptions{Paths: o.Paths, Kind: jobs.KindApply, Source: jobs.SourceCLI,
			Reason: o.Reason, Now: o.Now})
		if err != nil {
			_, _ = fmt.Fprintln(o.Out, "warning: no job journal:", err)
			journal = noJournal{}
		} else {
			journal = rec
			res.JobID = rec.ID()
		}
	}
	finish := func(err error) error {
		if rec != nil {
			rec.Finish(err, res.JobResult())
		}
		return err
	}
	var feErr error
	for _, a := range p.Actions {
		_, _ = fmt.Fprintln(o.Out, "==>", a.String())
		end := journal.Step(a.String())
		err := ex.run(ctx, a)
		end(err)
		if err == nil && a.Kind == plan.EnsureFE && fe.Current(o.Paths) == pr.Model.FEBuildID {
			res.FECurrent = pr.Model.FEBuildID
		}
		// A failed FE build does not stop the stack steps. The next apply tries the build again.
		if err != nil && a.Kind == plan.EnsureFE {
			_, _ = fmt.Fprintln(o.Out, "    error:", err)
			feErr = fmt.Errorf("apply failed at %s: %w", a.Kind, err)
			continue
		}
		if err != nil {
			return res, finish(fmt.Errorf("apply failed at %s: %w", a.Kind, err))
		}
	}
	if feErr != nil {
		return res, finish(feErr)
	}
	_, _ = fmt.Fprintln(o.Out, "apply complete")
	return res, finish(nil)
}

// waitAddress waits up to o.AddressWait for a LAN IPv4 address. It prints one line before the
// wait. It returns a warning when no address came; the render then has no LAN address, as before.
func waitAddress(ctx context.Context, o Options) string {
	if o.AddressWait <= 0 || o.IPs == nil {
		return ""
	}
	start := time.Now()
	waited := false
	ips, err := netinfo.WaitLANIPv4(ctx, o.IPs, o.AddressWait, o.PollInterval, func() {
		waited = true
		_, _ = fmt.Fprintf(o.Out, "wait for a network address (at most %s)\n", seconds(o.AddressWait))
	})
	if err != nil {
		// Prepare reads the interfaces again and reports the error.
		return ""
	}
	if len(ips) > 0 {
		if waited {
			_, _ = fmt.Fprintf(o.Out, "network address %s after %s\n", ips[0], seconds(time.Since(start)))
		}
		return ""
	}
	w := fmt.Sprintf("no LAN IPv4 address after %s; apply renders without a LAN address. The next address change runs apply again", seconds(o.AddressWait))
	_, _ = fmt.Fprintln(o.Out, "warning:", w)
	return w
}

// seconds prints a duration as "60 s" or "2.5 s".
func seconds(d time.Duration) string {
	return strconv.FormatFloat(d.Round(100*time.Millisecond).Seconds(), 'f', -1, 64) + " s"
}

type noJournal struct{}

func (noJournal) Step(string) func(error) { return func(error) {} }

func (o *Options) defaults() {
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.StopTimeout == 0 {
		o.StopTimeout = 5 * time.Minute
	}
	if o.PollInterval == 0 {
		o.PollInterval = time.Second
	}
	if o.DBReadyTimeout == 0 {
		o.DBReadyTimeout = 5 * time.Minute
	}
	if o.S3Endpoint == "" {
		o.S3Endpoint = "127.0.0.1:17070"
	}
	if o.ConfigPath == "" {
		o.ConfigPath = o.Paths.Abs(paths.ConfigFile)
	}
}

// healScaleDown removes the scale-down drop-in of an FE build that stopped before its
// restore (for example after a power loss). Then care-api gets all its workers again,
// and care-worker starts.
func healScaleDown(ctx context.Context, o Options) error {
	if !fe.ScaleDownLeft(o.Paths) {
		return nil
	}
	if o.DryRun {
		_, _ = fmt.Fprintf(o.Out, "a leftover FE build drop-in exists (%s); apply removes it\n", fe.ScaleDownDropIn)
		return nil
	}
	active, err := targetActive(ctx, o.Host)
	if err != nil {
		return err
	}
	if err := fe.RemoveScaleDown(ctx, o.Paths, o.Host, active); err != nil {
		return fmt.Errorf("cannot remove the leftover FE build drop-in: %w", err)
	}
	_, _ = fmt.Fprintf(o.Out, "removed a leftover FE build drop-in (%s)\n", fe.ScaleDownDropIn)
	return nil
}

func sortedImageNames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func printSummary(w io.Writer, pr *Prepared) {
	_, _ = fmt.Fprintf(w, "solution: %s (track %s), preset: %s\n", pr.Config.Solution.Name, pr.Config.Solution.Track, pr.Model.Sizing.Preset)
	_, _ = fmt.Fprintf(w, "names: %s\n", strings.Join(pr.Model.Hostnames, " "))
	ips := strings.Join(pr.IPs, " ")
	if ips == "" {
		ips = "(no LAN IPv4 address)"
	}
	_, _ = fmt.Fprintf(w, "addresses: %s\n", ips)
}

func printPlan(w io.Writer, p *plan.Plan) {
	_, _ = fmt.Fprintln(w, "plan:")
	for _, c := range p.Changes {
		_, _ = fmt.Fprintf(w, "  %-6s %s\n", c.Kind, c.Path)
	}
	for _, a := range p.Actions {
		_, _ = fmt.Fprintf(w, "  - %s\n", a.String())
	}
}

// readOwned reads every owned file below the owned directories.
func readOwned(p paths.Paths) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, dir := range render.OwnedDirs {
		root := p.Abs(dir)
		err := filepath.WalkDir(root, func(abs string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			hostPath := p.Host(abs)
			if d.IsDir() {
				// Only the rendered tree has owned subdirectories.
				if abs != root && !strings.HasPrefix(hostPath, paths.RenderedDir) {
					return fs.SkipDir
				}
				return nil
			}
			if !render.Owned(hostPath) {
				return nil
			}
			data, err := os.ReadFile(abs)
			if err != nil {
				return err
			}
			out[hostPath] = data
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("cannot read the files in %s: %w", dir, err)
		}
	}
	return out, nil
}

// podmanSecretValues builds the value of each podman secret from the secret files.
func podmanSecretValues(p paths.Paths) (map[string]string, error) {
	files, err := secrets.Store{Paths: p}.ReadAll()
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, s := range secrets.PodmanSecrets() {
		out[s.Name] = s.Value(files)
	}
	return out, nil
}

type secretState map[string]string

func readSecretState(p paths.Paths) secretState {
	st := secretState{}
	data, err := os.ReadFile(p.Abs(paths.StateDir + "/secrets.json"))
	if err == nil {
		_ = json.Unmarshal(data, &st)
	}
	return st
}

func writeSecretState(p paths.Paths, st secretState) error {
	data, _ := json.MarshalIndent(st, "", "  ")
	return lock.WriteFileAtomic(p.Abs(paths.StateDir+"/secrets.json"), data, 0o600)
}

// changedSecrets returns the podman secrets that are missing or that have a new value.
func changedSecrets(ctx context.Context, o Options, values map[string]string) ([]string, error) {
	names, err := o.Host.Podman.SecretNames(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot list the podman secrets: %w", err)
	}
	st := readSecretState(o.Paths)
	var out []string
	for _, s := range secrets.PodmanSecrets() {
		if !names[s.Name] || st[s.Name] != secrets.Hash(values[s.Name]) {
			out = append(out, s.Name)
		}
	}
	return out, nil
}

func targetActive(ctx context.Context, h *host.Host) (bool, error) {
	props, err := h.Systemd.Show(ctx, []string{"care.target"}, "ActiveState")
	if err != nil {
		return false, fmt.Errorf("cannot read the state of care.target: %w", err)
	}
	return props[0]["ActiveState"] == "active", nil
}

// ensureState records what the last successful ensure steps created.
type ensureState struct {
	Buckets string `json:"buckets"`
	Stanza  string `json:"stanza"`
}

func (e ensureState) equal(o ensureState) bool { return e == o }

func ensureRecord(pr *Prepared) ensureState {
	b := pr.Model.Buckets
	return ensureState{Buckets: b.Patient + "," + b.Facility + "@" + b.Region, Stanza: pr.Model.Stanza}
}

func readEnsure(p paths.Paths) ensureState {
	var st ensureState
	data, err := os.ReadFile(p.Abs(paths.EnsureState))
	if err == nil {
		_ = json.Unmarshal(data, &st)
	}
	return st
}

func writeEnsure(p paths.Paths, st ensureState) error {
	data, _ := json.MarshalIndent(st, "", "  ")
	return lock.WriteFileAtomic(p.Abs(paths.EnsureState), data, 0o600)
}
