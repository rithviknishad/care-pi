package service

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/applock"
	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/secrets"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
	"github.com/rithviknishad/care-pi/agent/internal/update"
)

// Updater returns the update machine of the host (Phase 4, docs/solutions.md).
func (s *Service) Updater() *update.Updater {
	tz := timezone(s.Paths)
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.Local
	}
	u := &update.Updater{Paths: s.Paths, ConfigPath: s.configPath(), SolutionsDir: s.SolutionsDir, Host: s.Host, IPs: s.IPs,
		S3: s.S3, Chown: s.Chown, Now: s.Now, Location: loc, Timezone: tz, ApplyHook: s.ApplyHook, StopOnSignal: s.StopOnSignal,
		Ready:           func() bool { return s.SetupStateName() == setup.Ready },
		NewCheckpointer: s.checkpointer}
	if s.UpdateHook != nil {
		s.UpdateHook(u)
	}
	return u
}

// checkpointer returns the checkpoint of Phase 3 (docs/api/README.md section 13.11) for a job of the update
// machine or for the plan action cutover (label: the job id).
func (s *Service) checkpointer(label string) update.Checkpointer {
	return backupCheckpoint{s: s, cp: update.BackupCheckpointer{Ops: s.ops(nil, nil, ""), Label: label, Now: s.Now}}
}

// backupCheckpoint drops the cached state of the local repository after the checkpoint (GET /backups shows it).
type backupCheckpoint struct {
	s  *Service
	cp update.BackupCheckpointer
}

func (c backupCheckpoint) Checkpoint(ctx context.Context) (update.CheckpointRef, error) {
	defer c.s.InvalidateBackups()
	return c.cp.Checkpoint(ctx)
}

// Updates returns GET /updates.
func (s *Service) Updates(ctx context.Context) (*update.Overview, error) {
	o, err := s.Updater().Overview(ctx)
	return o, toProblem(err)
}

// UpdatesSummary returns the updates member of GET /status. Errors give nil.
func (s *Service) UpdatesSummary(ctx context.Context) *update.Summary {
	sum, err := s.Updater().Summary(ctx)
	if err != nil {
		return nil
	}
	return sum
}

// UpdateHistory returns GET /updates/history.
func (s *Service) UpdateHistory(limit int, before string) update.History {
	return update.HistoryPage(s.Paths, limit, before)
}

func toProblem(err error) error {
	if err == nil {
		return nil
	}
	var p *problem.Problem
	if errors.As(err, &p) {
		return p
	}
	var ve *config.ValidationError
	if errors.As(err, &ve) {
		return configProblem(err)
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	return applyProblem(err)
}

// CheckRequest holds the options of an update check.
type CheckRequest struct {
	// Scheduled is the run of care-update-check.timer: it also writes 52care-pi.conf.
	Scheduled bool
}

// StartUpdateCheck starts a job of kind update-check. While a check runs, it returns that job.
// With updates.auto, a check that finds a target without a local build starts a job update-stage.
func (s *Service) StartUpdateCheck(ctx context.Context, req CheckRequest, source string) (*jobs.Job, error) {
	if j := jobs.Latest(s.Paths, func(j *jobs.Job) bool { return j.Kind == jobs.KindUpdateCheck && j.State == jobs.StateRunning }); j != nil {
		return j, nil
	}
	cfg, err := s.Config()
	if err != nil {
		return nil, err
	}
	return s.Jobs.Start(ctx, jobs.Spec{Kind: jobs.KindUpdateCheck, Source: source},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			u := s.Updater()
			if req.Scheduled {
				if _, err := u.SyncSecurityConf(ctx, cfg); err != nil {
					rec.Logf("warning: %v", err)
				}
			}
			res, err := u.Check(ctx, rec)
			if err != nil {
				return nil, toProblem(err)
			}
			if cfg.Updates.Auto && res.UpdateAvailable && res.BlockedBy == nil && s.SetupStateName() == setup.Ready {
				st := update.ReadState(s.Paths)
				if st.Target != nil && !st.Target.NeedsBuild && (st.Staging == nil || st.Staging.TargetID != st.Target.TargetID || st.Staging.State != "staged") {
					if j, err := s.StartUpdateStage(context.WithoutCancel(ctx), StageRequest{NoBuild: true}, jobs.SourceTimer); err == nil {
						res.StageJobID = &j.ID
						rec.Logf("updates.auto: staging job %s", j.ID)
					} else {
						rec.Logf("updates.auto: no staging now: %v", err)
					}
				}
			}
			return res, nil
		})
}

// StageRequest holds the options of POST /updates/stage.
type StageRequest struct {
	Version  string
	LockFile string
	NoBuild  bool
}

func (s *Service) precheckTarget(ctx context.Context, cfg *config.Config, version, lockFile string, apply bool) error {
	if lockFile != "" {
		return nil
	}
	u := s.Updater()
	t, err := u.ResolveTarget(ctx, cfg, version, "", false)
	if p := toProblem(err); p != nil {
		if problem.Is(p, problem.BuildRequiredOffline) {
			return nil // the job downloads the lock
		}
		return p
	}
	if apply && version == "" && cfg.Solution.Track != "custom" && t != nil && t.Lock != nil {
		cur, _ := lock.Load(s.Paths)
		if cur != nil && cur.Solution.Version != "" && cur.Solution.LockSHA256 == t.Lock.SHA256 {
			return problem.Newf(problem.NoUpdateAvailable, "The device runs %s %s, the newest lock.", cfg.Solution.Name, cur.Solution.Version)
		}
	}
	return nil
}

// StartUpdateStage starts a job of kind update-stage. It is cancellable before its step render.
func (s *Service) StartUpdateStage(ctx context.Context, req StageRequest, source string) (*jobs.Job, error) {
	if err := s.requireReady(); err != nil {
		return nil, err
	}
	cfg, err := s.Config()
	if err != nil {
		return nil, err
	}
	if err := s.precheckTarget(ctx, cfg, req.Version, req.LockFile, false); err != nil {
		return nil, err
	}
	return s.Jobs.Start(ctx, jobs.Spec{Kind: jobs.KindUpdateStage, Source: source, Lock: true, Cancellable: true},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			r, err := s.Updater().Stage(ctx, rec, update.StageOptions{Version: req.Version, LockFile: req.LockFile, NoBuild: req.NoBuild})
			if err != nil {
				return nil, toProblem(err)
			}
			return r, nil
		})
}

// UpdateRequest holds the options of POST /updates/apply.
type UpdateRequest struct {
	Version  string
	Now      bool
	Reason   string
	LockFile string
}

// StartUpdateApply starts a job of kind update-apply (docs/solutions.md, section 8).
func (s *Service) StartUpdateApply(ctx context.Context, req UpdateRequest, source string) (*jobs.Job, error) {
	if err := s.requireReady(); err != nil {
		return nil, err
	}
	cfg, err := s.Config()
	if err != nil {
		return nil, err
	}
	if !req.Now {
		if err := s.Updater().WindowClosed(cfg); err != nil {
			return nil, err
		}
	}
	if err := s.precheckTarget(ctx, cfg, req.Version, req.LockFile, true); err != nil {
		return nil, err
	}
	return s.Jobs.Start(ctx, jobs.Spec{Kind: jobs.KindUpdateApply, Source: source, Reason: req.Reason, Lock: true},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			r, err := s.Updater().Apply(ctx, rec, update.ApplyOptions{StageOptions: update.StageOptions{Version: req.Version, LockFile: req.LockFile},
				Now: req.Now, Source: source, Reason: req.Reason})
			if err != nil {
				return nil, toProblem(err)
			}
			return r, nil
		})
}

// RollbackRequest holds the options of POST /updates/rollback.
type RollbackRequest struct {
	ToLock string
	Reason string
}

// StartUpdateRollback starts a job of kind update-rollback (docs/solutions.md, section 9).
func (s *Service) StartUpdateRollback(ctx context.Context, req RollbackRequest, source string) (*jobs.Job, error) {
	if err := s.requireReady(); err != nil {
		return nil, err
	}
	if _, _, err := s.Updater().RollbackTarget(req.ToLock); err != nil {
		return nil, err
	}
	return s.Jobs.Start(ctx, jobs.Spec{Kind: jobs.KindUpdateRollback, Source: source, Reason: req.Reason, Lock: true},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			r, err := s.Updater().Rollback(ctx, rec, update.RollbackOptions{ToLock: req.ToLock, Source: source, Reason: req.Reason})
			if err != nil {
				return nil, toProblem(err)
			}
			return r, nil
		})
}

// OSUpdates returns GET /updates/os.
func (s *Service) OSUpdates(ctx context.Context) (*update.OSUpdates, error) {
	o, err := s.Updater().OS(ctx)
	return o, toProblem(err)
}

// OSRequest holds the options of POST /updates/os/apply.
type OSRequest struct {
	Now              bool
	RebootIfRequired bool
	Reason           string
}

// StartOSUpdate starts a job of kind os-update (docs/solutions.md, section 11).
func (s *Service) StartOSUpdate(ctx context.Context, req OSRequest, source string) (*jobs.Job, error) {
	if err := s.requireReady(); err != nil {
		return nil, err
	}
	cfg, err := s.Config()
	if err != nil {
		return nil, err
	}
	if !req.Now {
		if err := s.Updater().WindowClosed(cfg); err != nil {
			return nil, err
		}
	}
	return s.Jobs.Start(ctx, jobs.Spec{Kind: jobs.KindOSUpdate, Source: source, Reason: req.Reason, Lock: true},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			r, err := s.Updater().OSUpdate(ctx, rec, update.OSOptions{RebootIfRequired: req.RebootIfRequired, Source: source,
				Reason: req.Reason, Reboot: s.rebootLater})
			if err != nil {
				return nil, toProblem(err)
			}
			return r, nil
		})
}

// rebootLater reboots after the job released the apply lock.
func (s *Service) rebootLater(delay time.Duration) error {
	run := func() {
		for i := 0; i < 60 && applock.Held(s.Paths); i++ {
			time.Sleep(time.Second)
		}
		if _, err := s.Host.Runner.Run(context.Background(), hostCmd("systemctl", "reboot")); err != nil {
			s.log().Error("os-update: systemctl reboot failed", "error", err)
		}
	}
	if s.AfterPower != nil {
		s.AfterPower(delay, run)
	} else {
		time.AfterFunc(delay, run)
	}
	return nil
}

// OSReboot is the reboot of POST /updates/os/reboot (schema OsReboot).
type OSReboot struct {
	At        time.Time `json:"at"`
	Scheduled bool      `json:"scheduled"`
}

// RebootForOS reboots now (after delay) or schedules the reboot for the next window.
func (s *Service) RebootForOS(now bool, delay time.Duration) (*OSReboot, error) {
	if now {
		pa, err := s.Power("reboot", delay)
		if err != nil {
			return nil, err
		}
		return &OSReboot{At: pa.At, Scheduled: false}, nil
	}
	cfg, err := s.Config()
	if err != nil {
		return nil, err
	}
	at, err := s.Updater().ScheduleReboot(cfg)
	if err != nil {
		return nil, problem.New(problem.InvalidConfig, err.Error())
	}
	return &OSReboot{At: at, Scheduled: true}, nil
}

// ---- plugs ----

// Plugs returns GET /plugs.
func (s *Service) Plugs(ctx context.Context) (*update.PlugList, error) {
	l, err := s.Updater().Plugs(ctx)
	return l, toProblem(err)
}

// PlugSchema returns GET /plugs/{id}/settings-schema.
func (s *Service) PlugSchema(id string) (map[string]any, error) {
	m, err := s.Updater().SettingsSchema(id)
	return m, toProblem(err)
}

// PlugChangeResult is the result of PUT /plugs/{id} (schema PlugChangeResult).
type PlugChangeResult struct {
	Written bool              `json:"written"`
	ETag    string            `json:"etag"`
	Plug    update.Plug       `json:"plug"`
	Plan    update.ChangePlan `json:"plan"`
	Job     *jobs.Job         `json:"job"`
}

// PlugWrite holds a plug change.
type PlugWrite struct {
	ID      string
	Change  update.PlugChange
	IfMatch string
	DryRun  bool
	Apply   bool
	Source  string
}

// SetPlug writes solution.plugs.<id> (and the secret file), returns the plan, and can start a plug-change job.
func (s *Service) SetPlug(ctx context.Context, w PlugWrite) (*PlugChangeResult, error) {
	if w.DryRun && w.Apply {
		return nil, problem.InvalidParam("apply", "apply=true is not possible together with dry_run=true")
	}
	s.configMu.Lock()
	defer s.configMu.Unlock()
	doc, etag, err := s.readConfigDoc()
	if err != nil {
		return nil, err
	}
	if w.IfMatch != "" && w.IfMatch != etag && w.IfMatch != "*" {
		return nil, problem.Newf(problem.ConfigChanged, "The ETag is now %s. Read the config again.", etag)
	}
	cfg, err := s.Config()
	if err != nil {
		return nil, err
	}
	u := s.Updater()
	out, err := u.PlanChange(ctx, cfg, w.ID, w.Change)
	if err != nil {
		return nil, toProblem(err)
	}
	res := &PlugChangeResult{ETag: etag, Plug: out.After, Plan: out.Plan}
	if w.DryRun {
		return res, nil
	}
	if w.Apply && s.SetupStateName() != setup.Ready {
		return nil, problem.New(problem.SetupIncomplete, "Finish the setup first. Then apply=true is possible.")
	}
	sol, _ := doc["solution"].(map[string]any)
	if sol == nil {
		sol = map[string]any{}
		doc["solution"] = sol
	}
	plugs, _ := sol["plugs"].(map[string]any)
	if plugs == nil {
		plugs = map[string]any{}
	}
	// The values before the change: a cutover that fails writes them back before its rollback.
	prev := &update.PlugSnapshot{Choice: plugs[w.ID]}
	if data, err := os.ReadFile((secrets.Store{Paths: s.Paths}).Path(secrets.PlugFile(w.ID))); err == nil {
		prev.Secrets = data
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if c := out.Choice; c.Enabled == nil && c.Settings == nil {
		delete(plugs, w.ID)
	} else {
		m := map[string]any{}
		if c.Enabled != nil {
			m["enabled"] = *c.Enabled
		}
		if c.Settings != nil {
			st := map[string]any{}
			if len(c.Settings.Backend) > 0 {
				st["backend"] = c.Settings.Backend
			}
			if len(c.Settings.Frontend) > 0 {
				st["frontend"] = c.Settings.Frontend
			}
			m["settings"] = st
		}
		plugs[w.ID] = m
	}
	if len(plugs) == 0 {
		delete(sol, "plugs")
	} else {
		sol["plugs"] = plugs
	}
	data, err := marshalConfig(doc)
	if err != nil {
		return nil, err
	}
	if _, err := config.Parse(data, "config.yaml"); err != nil {
		return nil, configProblem(err)
	}
	if out.SecretsSet {
		if err := (secrets.Store{Paths: s.Paths}).WritePlug(w.ID, out.Secrets); err != nil {
			return nil, err
		}
	}
	if err := s.writeConfig(data); err != nil {
		return nil, err
	}
	res.Written, res.ETag = true, etagOf(data)
	// Path none (for example a setting of a plug that is off): the value is stored and counts when the plug turns on.
	// No job starts, also with apply=true (docs/solutions.md section 12.1).
	if w.Apply && out.Plan.Path != update.PathNone {
		path := out.Plan.Path
		j, err := s.Jobs.Start(ctx, jobs.Spec{Kind: jobs.KindPlugChange, Source: w.Source, Reason: w.Change.Reason, Lock: true},
			func(ctx context.Context, rec *jobs.Recorder) (any, error) {
				r, err := s.Updater().RunPlugChange(ctx, rec, update.PlugChangeOptions{ID: w.ID, Path: path, Source: w.Source, Reason: w.Change.Reason,
					Previous: prev})
				if err != nil {
					return nil, toProblem(err)
				}
				return r, nil
			})
		if err != nil {
			return res, err
		}
		res.Job = j
	}
	return res, nil
}

// ---- solutions ----

// Solutions returns GET /solutions.
func (s *Service) Solutions() (*update.SolutionList, error) {
	l, err := s.Updater().Solutions()
	return l, toProblem(err)
}

// Solution returns GET /solutions/{name}.
func (s *Service) Solution(name string) (*update.SolutionDetail, error) {
	d, err := s.Updater().Solution(name)
	return d, toProblem(err)
}

// SolutionLock returns GET /solutions/{name}/locks/{version}.
func (s *Service) SolutionLock(ctx context.Context, name, version string) (any, error) {
	l, err := s.Updater().SolutionLock(ctx, name, version)
	return l, toProblem(err)
}

// RecoverUpdates starts the recovery of an interrupted cutover (docs/solutions.md, section 8.6).
// The agent calls it at the start, after jobs.MarkInterrupted, and then each minute: a job of a CLI
// process (a timer) that stopped inside a cutover is recovered without a restart of the agent.
// While a process holds the apply lock, nothing is interrupted yet: it does nothing. It handles each
// cutover job one time in a process (a recovery that fails waits for the next start of the agent).
func (s *Service) RecoverUpdates(ctx context.Context) (*jobs.Job, error) {
	if s.recovering.Load() || applock.Held(s.Paths) {
		return nil, nil
	}
	u := s.Updater()
	r := u.PendingRecovery()
	if r == nil {
		return nil, nil
	}
	if _, done := s.recovered.LoadOrStore(r.JobID, true); done {
		return nil, nil
	}
	if r.Restart {
		// The interruption came before activate: the previous tree is on disk. Start it, then turn the page off.
		o := s.applyOptions(apply.Options{LockHeld: true})
		s.recovering.Store(true)
		go func() {
			defer s.recovering.Store(false)
			ctx := context.Background()
			release, err := applock.Acquire(ctx, s.Paths, time.Minute, nil)
			if err != nil {
				s.log().Error("recovery: no apply lock", "error", err)
				return
			}
			defer release()
			if err := apply.StartTarget(ctx, o); err != nil {
				s.log().Error("recovery: cannot start care.target; the maintenance page stays", "error", err)
				return
			}
			_ = apply.MaintenanceOff(s.Paths)
			_ = update.UpdateState(s.Paths, func(st *update.State) { st.Cutover = nil })
			s.log().Info("recovery: care.target runs again after the interrupted job " + r.JobID)
		}()
		return nil, nil
	}
	return s.Jobs.Start(ctx, jobs.Spec{Kind: jobs.KindUpdateRollback, Source: jobs.SourceTimer, Reason: "recovery " + r.JobID, Lock: true},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			res, err := u.Recover(ctx, rec, r)
			if err != nil {
				return nil, toProblem(err)
			}
			return res, nil
		})
}

// MaintenanceFlag reports whether the maintenance flag file exists.
func (s *Service) MaintenanceFlag() bool {
	_, err := os.Stat(s.Paths.Abs(paths.ModeDir + "/" + paths.ModeMaintenance))
	return err == nil
}
