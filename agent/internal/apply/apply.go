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
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/applock"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/fe"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/plan"
	"github.com/rithviknishad/care-pi/agent/internal/render"
	"github.com/rithviknishad/care-pi/agent/internal/s3admin"
	"github.com/rithviknishad/care-pi/agent/internal/secrets"
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
}

// Result summarises a run.
type Result struct {
	Plan    *plan.Plan
	Changed bool
}

// Run executes apply.
func Run(ctx context.Context, o Options) (*Result, error) {
	o.defaults()
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
		Host: o.Host, IPs: o.IPs, Update: o.Update, Strict: true, Warn: o.Out, Progress: o.Out,
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
	feWanted := pr.Config.FE.Provider != "none" && pr.Model.FEBuildID != "" && fe.Current(o.Paths) != pr.Model.FEBuildID

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
	})
	res := &Result{Plan: p, Changed: !p.Empty()}

	printSummary(o.Out, pr)
	if o.Paths.Root == "/" {
		if h, err := os.Hostname(); err == nil && strings.SplitN(h, ".", 2)[0] != pr.Config.Identity.Hostname {
			_, _ = fmt.Fprintf(o.Out, "warning: identity.hostname is %q but the OS host name is %q; avahi announces the OS host name (Phase 1 does not change it)\n", pr.Config.Identity.Hostname, h)
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
	j := newJournal(o, "apply", o.Reason)
	var feErr error
	for _, a := range p.Actions {
		_, _ = fmt.Fprintln(o.Out, "==>", a.String())
		j.start(a.String())
		err := ex.run(ctx, a)
		j.finish(err)
		// A failed FE build does not stop the stack steps. The next apply tries the build again.
		if err != nil && a.Kind == plan.EnsureFE {
			_, _ = fmt.Fprintln(o.Out, "    error:", err)
			feErr = fmt.Errorf("apply failed at %s: %w", a.Kind, err)
			continue
		}
		if err != nil {
			j.close(err)
			return res, fmt.Errorf("apply failed at %s: %w", a.Kind, err)
		}
	}
	j.close(feErr)
	if feErr != nil {
		return res, feErr
	}
	_, _ = fmt.Fprintln(o.Out, "apply complete")
	return res, nil
}

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
