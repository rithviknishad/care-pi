package apply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/fe"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/plan"
	"github.com/rithviknishad/care-pi/agent/internal/plugcfg"
	"github.com/rithviknishad/care-pi/agent/internal/render"
)

// AppUnits are the units that a cutover stops and starts (docs/solutions.md, section 8.1).
var AppUnits = []string{"care-beat.service", "care-worker.service", "care-api.service"}

// MaintenanceInfo is state/mode/maintenance.json.
type MaintenanceInfo struct {
	JobID     string    `json:"job_id"`
	Kind      string    `json:"kind"`
	StartedAt time.Time `json:"started_at"`
}

// MaintenanceOn creates the flag file of the maintenance page and maintenance.json. Caddy then serves
// status/maintenance.html at / without a reload; the API, bucket, and plug routes stay.
func MaintenanceOn(p paths.Paths, info MaintenanceInfo) error {
	if err := os.MkdirAll(p.Abs(paths.ModeDir), 0o755); err != nil {
		return err
	}
	data, _ := json.Marshal(info)
	if err := lock.WriteFileAtomic(p.Abs(paths.MaintenanceJSON), data, 0o644); err != nil {
		return err
	}
	return lock.WriteFileAtomic(p.Abs(paths.ModeDir+"/"+paths.ModeMaintenance), []byte(info.JobID+"\n"), 0o644)
}

// MaintenanceOff deletes the flag file and maintenance.json.
func MaintenanceOff(p paths.Paths) error {
	err := os.Remove(p.Abs(paths.ModeDir + "/" + paths.ModeMaintenance))
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	_ = os.Remove(p.Abs(paths.MaintenanceJSON))
	return err
}

// ReadMaintenance returns maintenance.json, and false when the page is off or the file is missing.
func ReadMaintenance(p paths.Paths) (MaintenanceInfo, bool) {
	var m MaintenanceInfo
	if _, err := os.Stat(p.Abs(paths.ModeDir + "/" + paths.ModeMaintenance)); err != nil {
		return m, false
	}
	data, err := os.ReadFile(p.Abs(paths.MaintenanceJSON))
	if err != nil || json.Unmarshal(data, &m) != nil {
		return MaintenanceInfo{}, false
	}
	return m, true
}

// cutover runs the plan action cutover of an apply (docs/solutions.md, section 7.5): the maintenance
// page, the stop of beat, worker, and api, the checkpoint, the start of care.target (beat runs the
// migrations first), and the page off. A failure leaves the page on: run care-pi update rollback.
func (x *executor) cutover(ctx context.Context, a plan.Action) error {
	h := x.o.Host
	id, kind := x.o.JobID, x.o.JobKind
	if id == "" {
		if r, ok := x.journal.(interface{ ID() string }); ok {
			id = r.ID()
		}
	}
	if kind == "" {
		kind = jobs.KindApply
	}
	if err := MaintenanceOn(x.o.Paths, MaintenanceInfo{JobID: id, Kind: kind, StartedAt: x.o.Now().UTC().Truncate(time.Second)}); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(x.o.Out, "    maintenance page on; stop care-api, care-worker, and care-beat")
	if err := h.Systemd.Stop(ctx, AppUnits...); err != nil {
		return err
	}
	if err := WaitInactive(ctx, x.o, AppUnits); err != nil {
		return err
	}
	if x.o.Checkpoint != nil {
		_, _ = fmt.Fprintln(x.o.Out, "    checkpoint: an incremental backup of the database before the migrations")
		if err := x.o.Checkpoint(ctx, id); err != nil {
			return fmt.Errorf("the checkpoint failed, so the update did not start: %w", err)
		}
	}
	if strings.Contains(a.Reason, "changed:") || strings.Contains(a.Reason, "pod changed") || strings.Contains(a.Reason, "unit changed") || strings.Contains(a.Reason, "secret") {
		if err := h.Systemd.Stop(ctx, "care.target"); err != nil {
			return err
		}
		if err := x.waitInactive(ctx); err != nil {
			return err
		}
	}
	if err := x.startTarget(ctx); err != nil {
		return fmt.Errorf("care.target did not start after the cutover; the maintenance page stays. Run care-pi update rollback: %w", err)
	}
	return MaintenanceOff(x.o.Paths)
}

// WaitInactive polls until each unit is inactive or failed.
func WaitInactive(ctx context.Context, o Options, units []string) error {
	o.defaults()
	deadline := time.Now().Add(o.StopTimeout)
	for {
		props, err := o.Host.Systemd.Show(ctx, units, "Id", "ActiveState")
		if err != nil {
			return err
		}
		var busy []string
		for i, p := range props {
			s := p["ActiveState"]
			if s != "inactive" && s != "failed" && s != "" {
				busy = append(busy, units[i]+"="+s)
			}
		}
		if len(busy) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("units did not stop within %s: %s", o.StopTimeout, strings.Join(busy, " "))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(o.PollInterval):
		}
	}
}

// buildCare builds the local care image when it does not exist (docs/solutions.md, section 13.1).
// It reports whether it built.
func (x *executor) buildCare(ctx context.Context) (bool, error) {
	cb := x.pr.CareBuild
	h := x.o.Host
	ok, err := h.Podman.ImageExists(ctx, cb.Image())
	if err != nil || ok {
		return false, err
	}
	base, err := h.Podman.ImageExists(ctx, cb.Base)
	if err != nil {
		return false, err
	}
	if !base {
		_, _ = fmt.Fprintln(x.o.Out, "    pull", cb.Base)
		if err := h.Podman.Pull(ctx, cb.Base); err != nil {
			return false, fmt.Errorf("cannot pull %s: %w", cb.Base, err)
		}
	}
	env := fe.Env{Config: x.pr.Config, Paths: x.o.Paths, Host: h, Out: x.o.Out}
	if err := fe.RunScaled(ctx, env, "", cb.Unit(x.pr.Config.Solution.Name), "the image build "+cb.ID, time.Hour); err != nil {
		return false, err
	}
	return true, nil
}

// Activate installs the tree of a prepared target (Phase 4): the directories, the podman secrets,
// the files, daemon-reload, and the start units. It starts or stops no unit of the stack.
// The update jobs call it in the step activate (docs/solutions.md, section 8.1).
func Activate(ctx context.Context, o Options, pr *Prepared) (*plan.Plan, error) {
	o.defaults()
	current, err := readOwned(o.Paths)
	if err != nil {
		return nil, err
	}
	values, err := podmanSecretValues(o.Paths)
	if err != nil {
		return nil, err
	}
	values[render.AdditionalPlugsSecret] = pr.Model.AdditionalPlugsJSON
	changed, err := changedSecrets(ctx, o, values)
	if err != nil {
		return nil, err
	}
	p := plan.Build(plan.Input{Desired: pr.Tree, Current: current, SecretsChanged: changed, SecretUnits: secretUnits(),
		TargetActive: true})
	ex := &executor{o: o, pr: pr, plan: p, secretValues: values, journal: noJournal{}}
	for _, a := range p.Actions {
		switch a.Kind {
		case plan.EnsureDirs, plan.SetSecrets, plan.WriteFiles, plan.DeleteFiles, plan.DaemonReload, plan.EnableTarget:
			_, _ = fmt.Fprintln(o.Out, "    "+a.String())
			if err := ex.run(ctx, a); err != nil {
				return p, fmt.Errorf("%s: %w", a.Kind, err)
			}
		}
	}
	return p, nil
}

// EnsureImages pulls each missing image of the target by digest and builds the local care image
// (the step stage-pull and the build part of stage-build). It returns the pulled and the built images.
func EnsureImages(ctx context.Context, o Options, pr *Prepared) (pulled, built []string, err error) {
	o.defaults()
	for _, n := range sortedImageNames(pr.Model.Images) {
		ref := pr.Model.Images[n]
		if n == "node" && pr.Config.FE.Provider != "local" {
			continue
		}
		if cb := pr.CareBuild; cb != nil && ref == cb.Image() {
			continue
		}
		if lock.IsLocalImage(ref) {
			continue
		}
		ok, err := o.Host.Podman.ImageExists(ctx, ref)
		if err != nil {
			return pulled, built, err
		}
		if ok {
			continue
		}
		_, _ = fmt.Fprintln(o.Out, "    pull", ref)
		if err := o.Host.Podman.Pull(ctx, ref); err != nil {
			return pulled, built, fmt.Errorf("cannot pull %s: %w", ref, err)
		}
		pulled = append(pulled, ref)
	}
	if pr.CareBuild != nil {
		ok, err := o.Host.Podman.ImageExists(ctx, pr.CareBuild.Base)
		if err != nil {
			return pulled, built, err
		}
		if !ok {
			_, _ = fmt.Fprintln(o.Out, "    pull", pr.CareBuild.Base)
			if err := o.Host.Podman.Pull(ctx, pr.CareBuild.Base); err != nil {
				return pulled, built, fmt.Errorf("cannot pull %s: %w", pr.CareBuild.Base, err)
			}
			pulled = append(pulled, pr.CareBuild.Base)
		}
	}
	return pulled, built, nil
}

// Snapshot returns the owned files on disk (host path to content): the tree that runs now.
func Snapshot(p paths.Paths) (map[string][]byte, error) { return readOwned(p) }

// PlanFor returns the plan of a prepared target against the host now, as "care-pi apply --dry-run" does.
func PlanFor(ctx context.Context, o Options, pr *Prepared) (*plan.Plan, error) {
	o.defaults()
	current, err := readOwned(o.Paths)
	if err != nil {
		return nil, err
	}
	values, err := podmanSecretValues(o.Paths)
	if err != nil {
		return nil, err
	}
	values[render.AdditionalPlugsSecret] = pr.Model.AdditionalPlugsJSON
	changed, err := changedSecrets(ctx, o, values)
	if err != nil {
		return nil, err
	}
	stack, err := readStack(ctx, o.Host)
	if err != nil {
		return nil, err
	}
	return plan.Build(plan.Input{Desired: pr.Tree, Current: current, ImagesChanged: lock.ImagesChanged(pr.PrevLock, pr.Lock),
		SecretsChanged: changed, SecretUnits: secretUnits(), TargetActive: stack.target, PodActive: stack.pod, FailedUnits: stack.failed,
		LockChanged: !lock.Equal(pr.PrevLock, pr.Lock),
		Cutover:     careChanged(pr.PrevLock, pr.Lock), Bootstrap: pr.Phase != "" && pr.Phase != "full",
		PlugConfig: plugcfg.NeedsSync(o.Paths, plugcfg.Desired(pr.Config, pr.Desire.Plugs, pr.Lock))}), nil
}

// CareChanged reports whether the care image or the set of backend plugs that are on changes.
func CareChanged(a, b *lock.Lock) bool { return careChanged(a, b) }

// transient reports whether a host path is a file of a build: the build units and their inputs.
// Staging writes them before the cutover; they change nothing that Care uses.
func transient(p string) bool {
	switch {
	case p == fe.BuildUnitFile, p == paths.QuadletDir+"/care-plug-build.container":
		return true
	case strings.HasPrefix(p, paths.QuadletDir+"/care-build-") && strings.HasSuffix(p, ".build"):
		return true
	case strings.HasPrefix(p, paths.RenderedFE+"/"), strings.HasPrefix(p, paths.RenderedBuild+"/"), strings.HasPrefix(p, paths.RenderedPlugs+"/"):
		return true
	}
	return false
}

// WriteTransient writes the build units and their inputs of a target (the step stage-build), then
// daemon-reload. It reports whether it wrote a file.
func WriteTransient(ctx context.Context, o Options, pr *Prepared) (bool, error) {
	o.defaults()
	current, err := readOwned(o.Paths)
	if err != nil {
		return false, err
	}
	wrote := false
	for _, p := range pr.Tree.Paths() {
		f := pr.Tree[p]
		if !transient(p) || string(current[p]) == string(f.Content) {
			continue
		}
		if err := writeFile(o.Paths, f, o.Chown); err != nil {
			return wrote, err
		}
		wrote = true
	}
	if wrote {
		return true, o.Host.Systemd.DaemonReload(ctx)
	}
	return false, nil
}

// BuildCare builds the local care image of a target when it does not exist. It reports whether it built.
func BuildCare(ctx context.Context, o Options, pr *Prepared) (bool, error) {
	o.defaults()
	if pr.CareBuild == nil {
		return false, nil
	}
	ex := &executor{o: o, pr: pr, journal: noJournal{}}
	return ex.buildCare(ctx)
}

// StartTarget resets the failed state of the stack and starts care.target.
func StartTarget(ctx context.Context, o Options) error {
	o.defaults()
	ex := &executor{o: o, journal: noJournal{}}
	return ex.startTarget(ctx)
}
