package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/fe"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/plan"
	"github.com/rithviknishad/care-pi/agent/internal/plugcfg"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
)

type planAction = plan.Action

// step runs fn as one job step with the action key. It logs the output of fn.
func step(rec *jobs.Recorder, action, name string, fn func() error) error {
	if name == "" {
		name = action
	} else {
		name = action + ": " + name
	}
	end := rec.StepAction(name, action)
	err := fn()
	end(err)
	return err
}

// ---- update-check ----

// CheckOptions controls Check.
type CheckOptions struct {
	// Network allows the download of the index and the locks (false: the cache only).
	Network bool
}

// Check runs the steps of an update-check job: fetch-index, fetch-lock, resolve-refs, and records the target.
func (u *Updater) Check(ctx context.Context, rec *jobs.Recorder) (*CheckResult, error) {
	cfg, err := u.Config()
	if err != nil {
		return nil, err
	}
	cur, err := lock.Load(u.Paths)
	if err != nil {
		return nil, err
	}
	res := &CheckResult{Kind: jobs.KindUpdateCheck, Track: cfg.Solution.Track, ChangedComponents: []string{}}
	if cur != nil {
		res.CurrentVersion = strp(cur.Solution.Version)
	}
	fail := func(err error) (*CheckResult, error) {
		p := errorsAs(err)
		if p == nil {
			p = problem.New(problem.Internal, err.Error())
		}
		_ = UpdateState(u.Paths, func(s *State) { s.LastCheckError = p })
		return nil, p
	}
	var ix *solution.Index
	url := IndexURL(cfg.Updates.IndexURL)
	if err := step(rec, "fetch-index", url, func() error {
		ix, err = u.client().FetchIndex(ctx, url)
		return err
	}); err != nil {
		// Offline: the cache and the embedded locks stay usable, but the check fails.
		return fail(err)
	}
	if s := ix.Find(cfg.Solution.Name); s != nil && s.Latest != nil {
		res.LatestVersion = strp(solution.NormalizeLockVersion(*s.Latest))
	}
	var t *Target
	var blocked *UpdateBlocker
	if err := step(rec, "fetch-lock", "", func() error {
		t, err = u.ResolveTarget(ctx, cfg, "", "", true)
		if p := errorsAs(err); p != nil && p.Slug == problem.AgentTooOld {
			blocked = &UpdateBlocker{Code: p.Slug, Detail: p.Detail, MinAgentVersion: strp(p.MinAgentVersion)}
			rec.Logf("%s", p.Detail)
			return nil
		}
		if p := errorsAs(err); p != nil && p.Slug == problem.NoUpdateAvailable {
			rec.Logf("%s", p.Detail)
			t, err = nil, nil
			return nil
		}
		if t != nil && t.Lock != nil {
			rec.Logf("target %s %s (sha256 %s)", t.Lock.Name, t.Lock.Version, t.Lock.SHA256[:12])
		}
		return err
	}); err != nil {
		return fail(err)
	}
	var pr *apply.Prepared
	if t != nil && blocked == nil {
		if cfg.Solution.Track == "custom" {
			if err := step(rec, "resolve-refs", "solution.custom", func() error {
				pr, err = u.Prepare(ctx, t, true, rec.Writer())
				return err
			}); err != nil {
				return fail(err)
			}
		} else if pr, err = u.Prepare(ctx, t, false, rec.Writer()); err != nil {
			return fail(err)
		}
	}
	now := u.now()
	res.CheckedAt = now.Truncate(time.Second)
	res.BlockedBy = blocked
	var rec2 *TargetRecord
	if pr != nil {
		id := TargetID(pr.Lock)
		path, err := pathOf(ctx, u.applyOptions(rec.Writer()), pr)
		if err != nil {
			return fail(err)
		}
		rec2 = &TargetRecord{Kind: t.Kind, Version: t.Version(), TargetID: id, Path: path,
			NeedsBuild: pr.CareBuild != nil, ChangedComponents: changedComponents(cur, pr.Lock), EstimatedDowntimeS: downtime(path)}
		if cur != nil {
			rec2.FromVersion, rec2.FromSHA256 = cur.Solution.Version, cur.Solution.LockSHA256
		}
		res.TargetVersion, res.TargetID = strp(rec2.Version), &rec2.TargetID
		res.ChangedComponents = rec2.ChangedComponents
		res.UpdateAvailable = updateAvailable(cfg, cur, rec2)
	}
	if err := UpdateState(u.Paths, func(s *State) {
		s.LastCheckAt, s.LastCheckError, s.Target = &res.CheckedAt, nil, rec2
	}); err != nil {
		return nil, err
	}
	return res, nil
}

// updateAvailable applies the rules of the tracks: stable never goes down; pinned changes only with the pin.
func updateAvailable(cfg *config.Config, cur *lock.Lock, t *TargetRecord) bool {
	if t == nil || t.Path == PathNone && cur != nil && cur.Solution.Version == t.Version {
		return false
	}
	curV := ""
	if cur != nil {
		curV = cur.Solution.Version
	}
	switch cfg.Solution.Track {
	case "stable":
		return curV == "" || solution.CompareLockVersions(t.Version, curV) > 0
	case "pinned":
		return solution.CompareLockVersions(t.Version, curV) != 0
	}
	return cur == nil || TargetID(cur) != t.TargetID
}

// ---- update-stage ----

// StageOptions controls Stage and Apply.
type StageOptions struct {
	Version  string
	LockFile string
	// NoBuild stops before a local build (the automatic staging after a check).
	NoBuild bool
}

// staged holds what the stage steps made.
type staged struct {
	target *Target
	pr     *apply.Prepared
	id     string
	path   string
	result StageResult
}

// Stage runs the steps fetch-lock, plan, stage-pull, stage-bundles, stage-build, and render.
// Care stays up. It changes nothing that Care uses.
func (u *Updater) Stage(ctx context.Context, rec *jobs.Recorder, o StageOptions) (*StageResult, error) {
	st, err := u.stage(ctx, rec, o)
	if err != nil {
		return nil, err
	}
	return &st.result, nil
}

func (u *Updater) stage(ctx context.Context, rec *jobs.Recorder, o StageOptions) (*staged, error) {
	start := u.now()
	cfg, err := u.Config()
	if err != nil {
		return nil, err
	}
	st := &staged{}
	out := rec.Writer()
	fail := func(err error) (*staged, error) {
		p := errorsAs(err)
		if p == nil {
			p = problem.New(problem.Internal, err.Error())
		}
		_ = UpdateState(u.Paths, func(s *State) {
			s.Staging = &StagingRecord{State: "failed", Version: st.target.Version(), TargetID: st.id, JobID: rec.ID(), Error: p}
		})
		return nil, err
	}
	if err := step(rec, "fetch-lock", o.Version, func() error {
		st.target, err = u.ResolveTarget(ctx, cfg, o.Version, o.LockFile, true)
		return err
	}); err != nil {
		return fail(err)
	}
	if err := step(rec, "plan", st.target.Version(), func() error {
		st.pr, err = u.Prepare(ctx, st.target, false, out)
		if err != nil {
			return err
		}
		st.id = TargetID(st.pr.Lock)
		st.path, err = pathOf(ctx, u.applyOptions(out), st.pr)
		rec.SetDetail(st.path)
		return err
	}); err != nil {
		return fail(err)
	}
	ao := u.applyOptions(out)
	st.result = StageResult{Kind: jobs.KindUpdateStage, Version: strp(st.target.Version()), TargetID: st.id,
		PulledImages: []string{}, Downloaded: []string{}, Built: []string{}}
	_ = UpdateState(u.Paths, func(s *State) {
		s.Staging = &StagingRecord{State: "staging", Version: st.target.Version(), TargetID: st.id, JobID: rec.ID(), NeedsBuild: st.pr.CareBuild != nil}
	})
	if err := step(rec, "stage-pull", "", func() error {
		pulled, _, err := apply.EnsureImages(ctx, ao, st.pr)
		st.result.PulledImages = append(st.result.PulledImages, pulled...)
		recordImages(u.Paths, pulled)
		return err
	}); err != nil {
		return fail(err)
	}
	if err := step(rec, "stage-bundles", "", func() error {
		got, n, err := u.stageBundles(ctx, st.pr, out)
		st.result.Downloaded = append(st.result.Downloaded, got...)
		st.result.DownloadBytes += n
		return err
	}); err != nil {
		return fail(err)
	}
	if err := step(rec, "stage-build", "", func() error {
		if o.NoBuild && st.pr.CareBuild != nil {
			return problem.New(problem.BuildRequiredOffline, "The target needs a local image build; the automatic staging does builds only inside the window.")
		}
		built, err := u.stageBuild(ctx, st.pr, out)
		st.result.Built = append(st.result.Built, built...)
		return err
	}); err != nil {
		return fail(err)
	}
	if err := step(rec, "render", st.id, func() error {
		pruneStaging(u.Paths, st.id, currentStagingID(u.Paths))
		return writeStaging(u.Paths, st.id, st.pr)
	}); err != nil {
		return fail(err)
	}
	now := u.now().Truncate(time.Second)
	st.result.DurationS = float64(now.Sub(start).Milliseconds()) / 1000
	_ = UpdateState(u.Paths, func(s *State) {
		s.Staging = &StagingRecord{State: "staged", Version: st.target.Version(), TargetID: st.id, JobID: rec.ID(), StagedAt: &now,
			DownloadBytes: st.result.DownloadBytes, NeedsBuild: st.pr.CareBuild != nil}
	})
	return st, nil
}

func currentStagingID(p paths.Paths) string {
	if l, err := lock.Load(p); err == nil && l != nil {
		return TargetID(l)
	}
	return ""
}

// stageBundles downloads the default FE bundle of the lock and the FE plug bundles of the plugs that are on.
func (u *Updater) stageBundles(ctx context.Context, pr *apply.Prepared, out io.Writer) ([]string, int64, error) {
	var got []string
	var total int64
	sl := pr.SolutionLock
	id := pr.Model.FEBuildID
	if pr.Config.FE.Provider != "none" && id != "" && !fe.Exists(u.Paths, id) && sl != nil && sl.FE.BuildID == id {
		url := solution.ResolveURL(pr.DistBase, sl.FE.URL)
		_, _ = fmt.Fprintln(out, "    download", url)
		_, n, err := fe.InstallBundle(ctx, url, sl.FE.SHA256, u.Paths.Abs(fe.Dir(id)), func(root string) error {
			if _, err := os.Stat(root + "/index.html"); err != nil {
				return fmt.Errorf("the bundle %s has no index.html", url)
			}
			return nil
		})
		if err != nil {
			return got, total, problem.New(problem.ArtefactInvalid, err.Error())
		}
		got, total = append(got, "fe/"+id), total+n
	}
	for _, pid := range sortedKeys(pr.Lock.Components.Plugs) {
		c := pr.Lock.Components.Plugs[pid]
		f := c.Frontend
		if !c.Enabled || f == nil || f.Source != solution.FrontendBuilt || f.SHA == "" {
			continue
		}
		dir := paths.PlugsDir + "/" + f.Slug + "/" + f.SHA[:12]
		if _, err := os.Stat(u.Paths.Abs(dir + "/" + f.Entry)); err == nil {
			continue
		}
		_, _ = fmt.Fprintln(out, "    download", f.URL)
		_, n, err := fe.InstallBundle(ctx, f.URL, f.SHA256, u.Paths.Abs(dir), func(root string) error {
			if _, err := os.Stat(root + "/" + f.Entry); err != nil {
				return fmt.Errorf("the plug bundle %s has no %s", f.URL, f.Entry)
			}
			return nil
		})
		if err != nil {
			return got, total, problem.New(problem.ArtefactInvalid, err.Error())
		}
		got, total = append(got, "plugs/"+f.Slug+"/"+f.SHA[:12]), total+n
	}
	return got, total, nil
}

// stageBuild runs the local builds: the care image (track custom), the FE build of the local provider,
// and the FE plug bundles without a CI artefact (experimental).
func (u *Updater) stageBuild(ctx context.Context, pr *apply.Prepared, out io.Writer) ([]string, error) {
	var built []string
	ao := u.applyOptions(out)
	if _, err := apply.WriteTransient(ctx, ao, pr); err != nil {
		return built, err
	}
	if did, err := apply.BuildCare(ctx, ao, pr); err != nil {
		return built, err
	} else if did {
		built = append(built, pr.CareBuild.Image())
		recordImages(u.Paths, []string{pr.CareBuild.Image()})
	}
	id := pr.Model.FEBuildID
	if pr.Config.FE.Provider == "local" && id != "" && !fe.Exists(u.Paths, id) {
		prov, err := fe.New("local", fe.Env{Config: pr.Config, Paths: u.Paths, CareFERepo: pr.Lock.Git["care_fe"].Repo,
			CareFESHA: pr.Lock.Git["care_fe"].SHA, BuildEnv: pr.Desire.Frontend, Host: u.Host, Out: out})
		if err != nil {
			return built, err
		}
		if _, err := prov.Ensure(ctx, id); err != nil {
			return built, err
		}
		built = append(built, "fe/"+id)
	}
	plugs, err := (PlugBuilder{Paths: u.Paths, Host: u.Host, Config: pr.Config, Out: out}).EnsureAll(ctx, pr)
	built = append(built, plugs...)
	return built, err
}

// ---- update-apply ----

// ApplyOptions controls Apply.
type ApplyOptions struct {
	StageOptions
	// Now skips the window check.
	Now bool
	// Source and Reason of the job, for the history.
	Source string
	Reason string
}

// Apply runs an update-apply job: the stage steps when the target is not staged, then the cutover.
func (u *Updater) Apply(ctx context.Context, rec *jobs.Recorder, o ApplyOptions) (*ApplyResult, error) {
	started := u.now()
	cfg, err := u.Config()
	if err != nil {
		return nil, err
	}
	if !o.Now {
		if err := u.WindowClosed(cfg); err != nil {
			return nil, err
		}
	}
	cur, err := lock.Load(u.Paths)
	if err != nil {
		return nil, err
	}
	st, err := u.stage(ctx, rec, o.StageOptions)
	if err != nil {
		return nil, err
	}
	if st.target.Lock != nil && cfg.Solution.Track == "pinned" && o.Version != "" &&
		solution.CompareLockVersions(cfg.Solution.Pin, st.target.Version()) != 0 {
		if err := setPin(u.configPath(), st.target.Version()); err != nil {
			return nil, err
		}
	}
	hist := HistoryEntry{JobID: rec.ID(), Kind: HistUpdate, Source: o.Source, Reason: strp(o.Reason), StartedAt: started,
		ToVersion: strp(st.target.Version())}
	if cur != nil {
		hist.FromVersion = strp(cur.Solution.Version)
	}
	res, cerr := u.cutover(ctx, rec, cutoverSpec{kind: jobs.KindUpdateApply, target: st.pr, targetID: st.id, prev: cur, path: st.path})
	hist.FinishedAt = u.now()
	hist.Checkpoint = res.checkpoint
	if res.downtime > 0 {
		d := res.downtime.Seconds()
		hist.DowntimeS = &d
	}
	if cerr != nil {
		hist.Outcome, hist.Problem = res.outcome, errorsAs(cerr)
		hist.ToVersion = hist.FromVersion
		hist.PitrOffer = res.offer
		_ = AppendHistory(u.Paths, hist)
		return nil, cerr
	}
	hist.Outcome = OutcomeSucceeded
	hist.FromLock = strp(res.fromLock)
	out := &ApplyResult{Kind: jobs.KindUpdateApply, FromVersion: hist.FromVersion, ToVersion: hist.ToVersion, TargetID: st.id,
		FromLock: res.fromLock, Checkpoint: res.checkpoint, DowntimeS: res.downtime.Seconds(), Warnings: []string{}}
	_ = step(rec, "prune-images", "", func() error {
		n, err := u.pruneImages(ctx, rec.Writer())
		out.PrunedImages = n
		if err != nil {
			out.Warnings = append(out.Warnings, "prune-images: "+err.Error())
		}
		return nil
	})
	if out.Checkpoint == nil {
		out.Checkpoint = &Checkpoint{TakenAt: started, TargetTime: started}
	}
	_ = AppendHistory(u.Paths, hist)
	_ = UpdateState(u.Paths, func(s *State) { s.Staging, s.Target, s.Cutover = nil, nil, nil })
	return out, nil
}

// cutoverSpec describes one cutover.
type cutoverSpec struct {
	kind     string
	target   *apply.Prepared
	targetID string
	// prev is the on-device lock that runs now: the target of the automatic rollback.
	prev *lock.Lock
	path string
	// optionalCheckpoint makes a failed checkpoint a warning (a manual rollback).
	optionalCheckpoint bool
	// offer is the PITR offer of a failure before the migrations of this cutover (a rollback).
	offer *PitrOffer
}

type cutoverResult struct {
	checkpoint *Checkpoint
	offer      *PitrOffer
	downtime   time.Duration
	outcome    string
	fromLock   string
}

// cutover runs the cutover steps of docs/solutions.md section 8.1 with the automatic rollback.
func (u *Updater) cutover(ctx context.Context, rec *jobs.Recorder, s cutoverSpec) (cutoverResult, error) {
	var res cutoverResult
	out := rec.Writer()
	ao := u.applyOptions(out)
	h := u.Host
	pageOn := u.now()
	mark := func(stepName string, f func(*CutoverRecord)) {
		_ = UpdateState(u.Paths, func(st *State) {
			if st.Cutover == nil {
				st.Cutover = &CutoverRecord{JobID: rec.ID(), Kind: s.kind, TargetID: s.targetID}
			}
			st.Cutover.Step = stepName
			if f != nil {
				f(st.Cutover)
			}
		})
	}
	// restoreOld starts the old units after a failure before activate: nothing changed.
	restoreOld := func(cause error, slug string) (cutoverResult, error) {
		_ = step(rec, "rollback", "start the running version again", func() error { return apply.StartTarget(ctx, ao) })
		_ = step(rec, "maintenance-off", "", func() error { return apply.MaintenanceOff(u.Paths) })
		_ = UpdateState(u.Paths, func(st *State) { st.Cutover = nil })
		res.outcome, res.offer = OutcomeRolledBack, s.offer
		res.downtime = u.now().Sub(pageOn)
		p := problem.Newf(slug, "%v. The running version starts again; the database did not change.", cause)
		if s.offer != nil {
			p.PitrOffer = s.offer
		}
		return res, p
	}
	if err := step(rec, "cutover-maintenance-on", "", func() error {
		mark("cutover-maintenance-on", nil)
		return apply.MaintenanceOn(u.Paths, apply.MaintenanceInfo{JobID: rec.ID(), Kind: s.kind, StartedAt: pageOn.Truncate(time.Second)})
	}); err != nil {
		_ = apply.MaintenanceOff(u.Paths)
		return res, err
	}
	if err := step(rec, "stop", strings.Join(apply.AppUnits, " "), func() error {
		mark("stop", nil)
		if err := h.Systemd.Stop(ctx, apply.AppUnits...); err != nil {
			return err
		}
		return apply.WaitInactive(ctx, ao, apply.AppUnits)
	}); err != nil {
		return restoreOld(err, problem.VerifyFailed)
	}
	if err := step(rec, "checkpoint", "", func() error {
		mark("checkpoint", nil)
		ref, err := u.checkpointer(rec.ID()).Checkpoint(ctx)
		if err != nil {
			if s.optionalCheckpoint {
				rec.Logf("warning: the checkpoint failed: %v; the rollback continues without it", err)
				return nil
			}
			return err
		}
		res.checkpoint = ref.Checkpoint()
		mark("checkpoint", func(c *CutoverRecord) { c.Checkpoint = res.checkpoint })
		rec.SetDetail(ref.BackupLabel)
		rec.Logf("checkpoint %s; the PITR target is %s", ref.BackupLabel, ref.TargetTime.UTC().Format(time.RFC3339))
		return nil
	}); err != nil {
		return restoreOld(fmt.Errorf("the checkpoint failed: %w", err), problem.CheckpointFailed)
	}
	offer := offerOf(res.checkpoint)
	if offer == nil {
		offer = s.offer
	}
	fail := func(slug string, cause error, migrated bool) (cutoverResult, error) {
		pitr := s.offer
		if migrated {
			pitr = offer
		}
		res.offer = pitr
		rerr := u.rollbackTo(ctx, rec, s.prev)
		res.downtime = u.now().Sub(pageOn)
		if rerr != nil {
			res.outcome = OutcomeRollbackFailed
			p := problem.Newf(problem.RollbackFailed, "%v. The rollback to the previous lock failed: %v. The maintenance page stays.", cause, rerr)
			p.PitrOffer = pitr
			mark("rollback", nil)
			return res, p
		}
		_ = UpdateState(u.Paths, func(st *State) { st.Cutover = nil })
		res.outcome = OutcomeRolledBack
		p := problem.Newf(slug, "%v. The previous lock runs again.", cause)
		p.PitrOffer = pitr
		return res, p
	}
	if err := step(rec, "activate", s.targetID, func() error {
		mark("activate", nil)
		if err := writePrevious(u.Paths, s.targetID); err != nil {
			return err
		}
		if _, err := apply.Activate(ctx, ao, s.target); err != nil {
			return err
		}
		if infraChanged(s.prev, s.target.Lock) {
			_, _ = fmt.Fprintln(out, "    an image of the stack changed: stop care.target")
			if err := h.Systemd.Stop(ctx, "care.target"); err != nil {
				return err
			}
			if err := apply.WaitInactive(ctx, ao, stackUnits()); err != nil {
				return err
			}
		}
		if id := s.target.Model.FEBuildID; id != "" && fe.Exists(u.Paths, id) && s.target.Config.FE.Provider != "none" {
			if err := fe.SetCurrent(u.Paths, id); err != nil {
				return err
			}
		}
		mark("activate", func(c *CutoverRecord) { c.Activated = true })
		return nil
	}); err != nil {
		return fail(problem.VerifyFailed, fmt.Errorf("the activation failed: %w", err), false)
	}
	if err := step(rec, "migrate", "care-beat.service", func() error {
		mark("migrate", nil)
		mctx, cancel := context.WithTimeout(ctx, u.migrateTimeout())
		defer cancel()
		if err := h.Systemd.ResetFailed(mctx, stackUnits()...); err != nil {
			return err
		}
		if err := h.Systemd.Start(mctx, "care-beat.service"); err != nil {
			return fmt.Errorf("care-beat.service did not become healthy: %w", err)
		}
		mark("migrate", func(c *CutoverRecord) { c.Migrated = true })
		return nil
	}); err != nil {
		return fail(problem.MigrationFailed, err, true)
	}
	if err := step(rec, "start", "care-worker.service care-api.service", func() error {
		mark("start", nil)
		sctx, cancel := context.WithTimeout(ctx, u.startTimeout())
		defer cancel()
		if err := h.Systemd.Start(sctx, "care-worker.service", "care-api.service"); err != nil {
			return err
		}
		return h.Systemd.Start(sctx, "care.target")
	}); err != nil {
		return fail(problem.VerifyFailed, fmt.Errorf("care-worker or care-api did not start: %w", err), true)
	}
	rows := plugcfg.Desired(s.target.Config, s.target.Desire.Plugs, s.target.Lock)
	if err := step(rec, "plug-config", fmt.Sprintf("%d row(s)", len(rows)), func() error {
		mark("plug-config", nil)
		_, err := plugcfg.Sync(ctx, h, u.Paths, rows, u.now())
		return err
	}); err != nil {
		return fail(problem.VerifyFailed, err, true)
	}
	if err := step(rec, "verify", "", func() error {
		mark("verify", nil)
		return u.verifier().Verify(ctx, u.verifyTarget(s.target, rows))
	}); err != nil {
		return fail(problem.VerifyFailed, fmt.Errorf("the check after the update failed: %w", err), true)
	}
	if err := step(rec, "write-lock", "", func() error {
		mark("write-lock", nil)
		s.target.Lock.GeneratedAt = u.now().Format(time.RFC3339)
		if err := lock.Write(u.Paths, s.target.Lock, u.now()); err != nil {
			return err
		}
		res.fromLock = s.target.Lock.History.PreviousID()
		return nil
	}); err != nil {
		return res, err
	}
	if err := step(rec, "maintenance-off", "", func() error { return apply.MaintenanceOff(u.Paths) }); err != nil {
		return res, err
	}
	res.downtime = u.now().Sub(pageOn)
	res.outcome = OutcomeSucceeded
	_ = UpdateState(u.Paths, func(st *State) { st.Cutover = nil })
	return res, nil
}

func (u *Updater) migrateTimeout() time.Duration {
	if u.MigrateTimeout > 0 {
		return u.MigrateTimeout
	}
	return DefaultMigrateTimeout
}

func (u *Updater) startTimeout() time.Duration {
	if u.StartTimeout > 0 {
		return u.StartTimeout
	}
	return DefaultStartTimeout
}

func stackUnits() []string {
	return []string{"care-pod.service", "care-caddy.service", "care-db.service", "care-redis.service", "care-s3.service",
		"care-beat.service", "care-worker.service", "care-api.service"}
}

// infraChanged reports whether an image of the stack other than care changed.
func infraChanged(a, b *lock.Lock) bool {
	if a == nil || b == nil {
		return false
	}
	for _, n := range lock.ImagesChanged(a, b) {
		if n != "care" && n != "node" {
			return true
		}
	}
	return false
}

func (u *Updater) verifyTarget(pr *apply.Prepared, rows []plugcfg.Row) VerifyTarget {
	t := VerifyTarget{Canonical: pr.Model.Canonical, Address: pr.Model.PrimaryIP, Rows: rows}
	if pr.Config.FE.Provider != "none" && fe.Exists(u.Paths, pr.Model.FEBuildID) {
		t.FEBuildID = pr.Model.FEBuildID
	}
	return t
}

// rollbackTo installs a previous on-device lock as it is: the step rollback, then maintenance-off.
// The database keeps the migrations that ran; the PITR offer undoes them.
func (u *Updater) rollbackTo(ctx context.Context, rec *jobs.Recorder, prev *lock.Lock) error {
	if prev == nil {
		err := errors.New("no previous lock exists")
		_ = step(rec, "rollback", "", func() error { return err })
		return err
	}
	out := rec.Writer()
	ao := u.applyOptions(out)
	h := u.Host
	var pr *apply.Prepared
	err := step(rec, "rollback", "to "+versionText(prev), func() error {
		_ = h.Systemd.Stop(ctx, apply.AppUnits...)
		_ = apply.WaitInactive(ctx, ao, apply.AppUnits)
		var err error
		pr, err = apply.Prepare(ctx, apply.PrepareOptions{Paths: u.Paths, ConfigPath: u.configPath(), SolutionsDir: u.SolutionsDir,
			Host: u.Host, IPs: u.IPs, Phase: "full", Offline: true, PinnedLock: prev, Warn: out})
		if err != nil {
			return err
		}
		if _, err := apply.Activate(ctx, ao, pr); err != nil {
			return err
		}
		if id := prev.FE.BuildID; id != "" && fe.Exists(u.Paths, id) {
			if err := fe.SetCurrent(u.Paths, id); err != nil {
				return err
			}
		}
		if err := h.Systemd.ResetFailed(ctx, stackUnits()...); err != nil {
			return err
		}
		mctx, cancel := context.WithTimeout(ctx, u.migrateTimeout())
		defer cancel()
		if err := h.Systemd.Start(mctx, "care-beat.service"); err != nil {
			return err
		}
		if err := h.Systemd.Start(mctx, "care-worker.service", "care-api.service"); err != nil {
			return err
		}
		if err := h.Systemd.Start(mctx, "care.target"); err != nil {
			return err
		}
		_, err = plugcfg.Sync(ctx, h, u.Paths, plugcfg.Desired(pr.Config, pr.Desire.Plugs, pr.Lock), u.now())
		return err
	})
	if err != nil {
		return err
	}
	return step(rec, "maintenance-off", "", func() error { return apply.MaintenanceOff(u.Paths) })
}

func versionText(l *lock.Lock) string {
	if l.Solution.Version != "" {
		return l.Solution.Name + " " + l.Solution.Version
	}
	return l.Solution.Name + " (solution refs)"
}

// ---- update-rollback ----

// RollbackOptions controls Rollback.
type RollbackOptions struct {
	// ToLock is a lock id of state/locks ("" is history.previous).
	ToLock string
	Source string
	Reason string
}

// RollbackTarget returns the lock id and the lock of a manual rollback, or rollback-unavailable.
func (u *Updater) RollbackTarget(toLock string) (string, *lock.Lock, error) {
	cur, err := lock.Load(u.Paths)
	if err != nil {
		return "", nil, err
	}
	if toLock == "" && cur != nil {
		toLock = cur.History.PreviousID()
	}
	if toLock == "" {
		return "", nil, problem.New(problem.RollbackUnavailable, paths.LockHistory+" has no lock before the running one.")
	}
	l, err := lock.ReadHistory(u.Paths, toLock)
	if err != nil || l == nil {
		return "", nil, problem.Newf(problem.RollbackUnavailable, "The lock %s is not in %s.", toLock, paths.LockHistory)
	}
	return toLock, l, nil
}

// Rollback runs an update-rollback job (docs/solutions.md, section 9).
func (u *Updater) Rollback(ctx context.Context, rec *jobs.Recorder, o RollbackOptions) (*RollbackResult, error) {
	started := u.now()
	cur, err := lock.Load(u.Paths)
	if err != nil {
		return nil, err
	}
	var id string
	var to *lock.Lock
	if err := step(rec, "fetch-lock", o.ToLock, func() error {
		id, to, err = u.RollbackTarget(o.ToLock)
		return err
	}); err != nil {
		return nil, err
	}
	out := rec.Writer()
	ao := u.applyOptions(out)
	var pr *apply.Prepared
	if err := step(rec, "stage-pull", "only missing images", func() error {
		pr, err = apply.Prepare(ctx, apply.PrepareOptions{Paths: u.Paths, ConfigPath: u.configPath(), SolutionsDir: u.SolutionsDir,
			Host: u.Host, IPs: u.IPs, Phase: "full", Offline: true, PinnedLock: to, Warn: out})
		if err != nil {
			return err
		}
		pulled, _, err := apply.EnsureImages(ctx, ao, pr)
		recordImages(u.Paths, pulled)
		return err
	}); err != nil {
		return nil, err
	}
	offer := u.installOffer(cur)
	hist := HistoryEntry{JobID: rec.ID(), Kind: HistRollback, Source: o.Source, Reason: strp(o.Reason), StartedAt: started,
		ToLock: strp(id), ToVersion: strp(to.Solution.Version)}
	if cur != nil {
		hist.FromVersion = strp(cur.Solution.Version)
	}
	tid := TargetID(pr.Lock)
	res, cerr := u.cutover(ctx, rec, cutoverSpec{kind: jobs.KindUpdateRollback, target: pr, targetID: tid, prev: cur,
		path: PathCutover, optionalCheckpoint: true, offer: offer})
	hist.FinishedAt = u.now()
	hist.Checkpoint = res.checkpoint
	d := res.downtime.Seconds()
	hist.DowntimeS = &d
	hist.PitrOffer = offer
	if cerr != nil {
		hist.Outcome, hist.Problem = res.outcome, errorsAs(cerr)
		hist.ToVersion = hist.FromVersion
		_ = AppendHistory(u.Paths, hist)
		return nil, cerr
	}
	hist.Outcome = OutcomeSucceeded
	hist.FromLock = strp(res.fromLock)
	_ = AppendHistory(u.Paths, hist)
	return &RollbackResult{Kind: jobs.KindUpdateRollback, FromVersion: hist.FromVersion, ToVersion: hist.ToVersion, ToLock: id,
		Checkpoint: res.checkpoint, PitrOffer: offer, DowntimeS: d}, nil
}

// installOffer returns the PITR offer of the update that installed the running lock: its checkpoint.
func (u *Updater) installOffer(cur *lock.Lock) *PitrOffer {
	if cur == nil {
		return nil
	}
	for _, e := range ReadHistory(u.Paths) {
		if e.Outcome != OutcomeSucceeded || e.Checkpoint == nil {
			continue
		}
		if (e.Kind == HistUpdate || e.Kind == HistPlugChange || e.Kind == HistRollback) && ptrEq(e.ToVersion, cur.Solution.Version) {
			return offerOf(e.Checkpoint)
		}
	}
	return nil
}

func ptrEq(p *string, s string) bool {
	if p == nil {
		return s == ""
	}
	return *p == s
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
