package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/backup"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/disks"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/render"
	"github.com/rithviknishad/care-pi/agent/internal/restore"
	"github.com/rithviknishad/care-pi/agent/internal/secrets"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
)

// Scopes of a backup run and of a verify.
const (
	ScopeAll         = "all"
	ScopeLocalDB     = "local-db"
	ScopeDestination = "destination"
)

// BackupRunRequest is POST /backups/run.
type BackupRunRequest struct {
	Scope       string
	Type        string
	Destination string
	Reason      string
	// Snapshot limits a destination run to db or files (the timers). "" makes both.
	Snapshot  string
	FromTimer bool
}

func (s *Service) restoreRunning() error {
	if id := jobs.RunningOf(s.Paths, jobs.RestoreKinds); id != "" {
		p := problem.New(problem.JobRunning, "A restore or a key rotation runs now. Backups start again when it ends.")
		p.JobID, p.HasJobID = id, true
		return p
	}
	return nil
}

func (s *Service) ops(rec *jobs.Recorder, cfg *config.Config, source string) backup.Ops {
	o := backup.Ops{Env: s.backupEnv(nil), Config: cfg, Source: source}
	if rec != nil {
		o.JobID, o.Queued, o.Logf = rec.ID(), rec.SetQueued, rec.Logf
		o.Out = rec.Writer()
	}
	if s.PollInterval > 0 {
		o.Poll = s.PollInterval
	}
	if s.HealthTimeout > 0 {
		o.DBWait = s.HealthTimeout
	}
	return o
}

// normalize fills the defaults of a backup run request and checks it.
func (s *Service) normalize(cfg *config.Config, r *BackupRunRequest) error {
	if r.Scope == "" {
		r.Scope = ScopeAll
		if r.Destination != "" {
			r.Scope = ScopeDestination
		}
	}
	if r.Type == "" {
		r.Type = "incr"
	}
	switch r.Scope {
	case ScopeAll, ScopeLocalDB:
		if r.Destination != "" {
			return problem.Invalid("/destination", "the scope "+r.Scope+" takes no destination", "scope")
		}
	case ScopeDestination:
		if r.Destination == "" {
			return problem.Invalid("/destination", "the scope destination needs a destination", "required")
		}
		found := false
		for _, d := range cfg.Backups.Destinations {
			found = found || d.ID == r.Destination
		}
		if !found {
			return problem.Newf(problem.NotFound, "No destination of config.yaml has the id %s.", r.Destination)
		}
	}
	if r.Scope != ScopeLocalDB && len(cfg.Backups.Destinations) == 0 {
		return problem.New(problem.NoDestination, "Add a destination to backups.destinations first, or use the scope local-db.")
	}
	if r.Scope != ScopeLocalDB && !s.keys().Exists(backup.Active) {
		return problem.New(problem.NoRecoveryKey, "A snapshot needs the recovery key. Make it with POST /backups/recovery-key.")
	}
	return nil
}

// StartBackupRun starts a job of kind backup-run (POST /backups/run).
func (s *Service) StartBackupRun(ctx context.Context, r BackupRunRequest, source string) (*jobs.Job, error) {
	if err := s.requireReady(); err != nil {
		return nil, err
	}
	if err := s.restoreRunning(); err != nil {
		return nil, err
	}
	cfg, err := s.Config()
	if err != nil {
		return nil, err
	}
	if err := s.normalize(cfg, &r); err != nil {
		return nil, err
	}
	if r.Scope != ScopeDestination && r.Type == "full" {
		info := s.local(ctx).info
		need := backup.FullSpaceNeeded(info)
		if free, _, err := backup.Free(s.Paths.Abs(paths.BackupPG)); err == nil && need > 0 && free < need {
			return nil, problem.NeedSpace(paths.BackupPG, need, free, "A full backup needs free space of 1.2 times the last full backup.")
		}
	}
	return s.Jobs.Start(ctx, jobs.Spec{Kind: jobs.KindBackupRun, Source: source, Reason: r.Reason, Cancellable: true},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			defer s.InvalidateBackups()
			return s.runBackup(ctx, rec, cfg, r, source)
		})
}

func (s *Service) runBackup(ctx context.Context, rec *jobs.Recorder, cfg *config.Config, r BackupRunRequest, source string) (any, error) {
	o := s.ops(rec, cfg, source)
	if source == jobs.SourceTimer && r.Scope == ScopeLocalDB && r.Type == "incr" {
		for _, d := range restore.PruneOld(s.Paths, s.now()) {
			rec.Logf("deleted the old directory %s", d)
		}
	}
	res := &backup.RunResult{Kind: jobs.KindBackupRun, Scope: r.Scope, Destinations: []backup.DestResult{}}
	if r.Scope != ScopeDestination {
		action := "backup-" + r.Type
		var lr *backup.LocalResult
		end := rec.StepAction(action+": local repository", action)
		lr, err := o.LocalBackup(ctx, r.Type, r.Reason)
		end(err)
		if r.FromTimer {
			o.RecordScheduled(err)
		}
		if err != nil {
			return nil, err
		}
		res.Local = lr
		if r.Type == "incr" && r.Reason != "" && r.Scope == ScopeLocalDB {
			reason := r.Reason
			_ = o.State().AddCheckpoint(backup.Checkpoint{Label: lr.Label, UndoPoint: lr.UndoPoint, Timeline: lr.Timeline, Reason: &reason})
		}
	}
	if r.Scope == ScopeLocalDB {
		return res, nil
	}
	k, err := backup.ActiveKey(o.Env)
	if err != nil {
		return nil, err
	}
	var first error
	for _, d := range cfg.Backups.Destinations {
		if r.Scope == ScopeDestination && d.ID != r.Destination {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, jobs.ErrCancelled
		}
		dr := backup.DestResult{ID: d.ID}
		ok := true
		for _, tag := range []string{backup.TagDB, backup.TagFiles} {
			if r.Snapshot != "" && r.Snapshot != tag {
				continue
			}
			end := rec.StepAction(fmt.Sprintf("snapshot: %s (%s)", d.ID, tag), "snapshot")
			sum, err := o.Snapshot(ctx, d, tag, k, false)
			end(stepErr(err))
			if err != nil {
				ok = false
				if first == nil {
					first = err
				}
				break
			}
			if tag == backup.TagDB {
				dr.DBSnapshotID = sum.SnapshotID
			} else {
				dr.FilesSnapshotID = sum.SnapshotID
			}
			dr.AddedBytes += sum.DataAddedPacked
		}
		if !ok {
			continue
		}
		end := rec.StepAction("forget: "+d.ID, "forget")
		var ferr error
		for _, tag := range []string{backup.TagDB, backup.TagFiles} {
			if r.Snapshot != "" && r.Snapshot != tag {
				continue
			}
			if err := o.Forget(ctx, d, tag, k); err != nil && ferr == nil {
				ferr = err
			}
		}
		end(stepErr(ferr))
		if dr.DBSnapshotID != "" && dr.FilesSnapshotID != "" {
			res.Destinations = append(res.Destinations, dr)
		}
	}
	if first != nil {
		return nil, first
	}
	return res, nil
}

// stepErr gives the problem type in the detail of a failed destination step.
func stepErr(err error) error {
	var p *problem.Problem
	if errors.As(err, &p) {
		return fmt.Errorf("%s: %s", p.Slug, p.Detail)
	}
	return err
}

// TimerSnapshot is the run of a destination timer: one snapshot, the retention, and no job journal.
func (s *Service) TimerSnapshot(ctx context.Context, dest, tag string) error {
	if err := s.restoreRunning(); err != nil {
		s.log().Info("backups: a restore runs; the timer run of " + dest + " does nothing")
		return nil
	}
	cfg, err := s.Config()
	if err != nil {
		return err
	}
	o := s.ops(nil, cfg, jobs.SourceTimer)
	o.Out = os.Stdout
	d, err := o.Dest(dest)
	if err != nil {
		return err
	}
	k, err := backup.ActiveKey(o.Env)
	if err != nil {
		o.State().AddRun(dest, backup.DestinationRun{StartedAt: s.now().Truncate(time.Second), Kind: tag, Source: jobs.SourceTimer})(false, "", nil, err)
		return err
	}
	_, err = o.Snapshot(ctx, d, tag, k, true)
	return err
}

// VerifyRequest is POST /backups/verify.
type VerifyRequest struct {
	Scope       string
	Destination string
	SubsetPct   int
	Prune       bool
}

// StartVerify starts a job of kind backup-verify.
func (s *Service) StartVerify(ctx context.Context, r VerifyRequest, source string) (*jobs.Job, error) {
	if err := s.requireReady(); err != nil {
		return nil, err
	}
	if err := s.restoreRunning(); err != nil {
		return nil, err
	}
	cfg, err := s.Config()
	if err != nil {
		return nil, err
	}
	br := BackupRunRequest{Scope: r.Scope, Destination: r.Destination}
	if err := s.normalize(cfg, &br); err != nil {
		return nil, err
	}
	r.Scope = br.Scope
	if r.SubsetPct == 0 {
		r.SubsetPct = 5
	}
	return s.Jobs.Start(ctx, jobs.Spec{Kind: jobs.KindBackupVerify, Source: source, Cancellable: true},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			defer s.InvalidateBackups()
			o := s.ops(rec, cfg, source)
			res := &backup.VerifyResult{Kind: jobs.KindBackupVerify, Destinations: []backup.VerifyDest{}}
			var first error
			if r.Scope != ScopeDestination {
				end := rec.StepAction("verify-local: pgbackrest verify", "verify-local")
				vo, err := o.VerifyLocal(ctx)
				end(stepErr(err))
				res.Local = vo
				if err != nil {
					first = err
				}
			}
			if r.Scope == ScopeLocalDB {
				return res, first
			}
			k, err := backup.ActiveKey(o.Env)
			if err != nil {
				return nil, err
			}
			for _, d := range cfg.Backups.Destinations {
				if r.Scope == ScopeDestination && d.ID != r.Destination {
					continue
				}
				vd, err := o.VerifyDest(ctx, d, k, r.SubsetPct, r.Prune, func(name, action string) func(error) {
					end := rec.StepAction(name, action)
					return func(err error) { end(stepErr(err)) }
				})
				if err != nil {
					if first == nil || problem.Is(err, problem.VerifyFailed) {
						first = err
					}
					continue
				}
				res.Destinations = append(res.Destinations, *vd)
			}
			if first != nil {
				return nil, first
			}
			return res, nil
		})
}

// restoreEnv returns the environment of a restore job.
func (s *Service) restoreEnv(rec *jobs.Recorder, cfg *config.Config) restore.Env {
	e := restore.Env{Env: s.backupEnv(rec.Writer()), Rec: rec, Config: cfg, Poll: s.PollInterval, Timeout: s.HealthTimeout,
		Healthy: func(ctx context.Context) error { return s.waitCareHealthy(ctx, rec) }}
	if img, err := restore.LockImage(s.Paths); err == nil {
		e.Image = img
	}
	return e
}

// StartDrill starts a job of kind restore-drill. fromTimer records a skip for too little space.
func (s *Service) StartDrill(ctx context.Context, reason, source string, fromTimer bool) (*jobs.Job, error) {
	if err := s.requireReady(); err != nil {
		return nil, err
	}
	if err := s.restoreRunning(); err != nil {
		return nil, err
	}
	cfg, err := s.Config()
	if err != nil {
		return nil, err
	}
	if !fromTimer {
		e := restore.Env{Env: s.backupEnv(nil)}
		if need, free, err := restore.DrillSpace(ctx, e); err == nil && free < need {
			return nil, problem.NeedSpace(paths.DataDir, need, free, "The restore drill needs free space of 2 times the database size.")
		}
	}
	return s.Jobs.Start(ctx, jobs.Spec{Kind: jobs.KindRestoreDrill, Source: source, Reason: reason, Cancellable: true},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			defer s.InvalidateBackups()
			return restore.Drill(ctx, s.restoreEnv(rec, cfg), fromTimer)
		})
}

// ---- disk prepare ----

// DiskPrepareResult is the result of a disk-prepare job (schema DiskPrepareResult).
type DiskPrepareResult struct {
	Kind      string `json:"kind"`
	DiskID    string `json:"disk_id"`
	Device    string `json:"device"`
	Label     string `json:"label"`
	MountPath string `json:"mount_path"`
	FSType    string `json:"fs_type"`
	SizeBytes int64  `json:"size_bytes"`
}

// StartDiskPrepare erases a disk and prepares it for backups (job disk-prepare).
func (s *Service) StartDiskPrepare(ctx context.Context, diskID, label, source string) (*jobs.Job, error) {
	if st := s.SetupStateName(); st == setup.Unclaimed {
		return nil, problem.New(problem.NotClaimed, "The device is not claimed.")
	}
	list, err := disks.List(ctx, s.Host.Runner, s.Paths)
	if err != nil {
		return nil, err
	}
	d := disks.Find(list, diskID)
	if d == nil {
		return nil, problem.Newf(problem.NotFound, "No disk has the id %s.", diskID)
	}
	cfg, _ := config.Load(s.configPath())
	used := map[string]bool{}
	for _, x := range list {
		if x.CareLabel != nil {
			used[*x.CareLabel] = true
		}
	}
	if cfg != nil {
		for _, dd := range cfg.Backups.Destinations {
			if dd.DiskLabel != "" {
				used[dd.DiskLabel] = true
			}
		}
	}
	if err := diskInUse(d, label, cfg); err != nil {
		return nil, err
	}
	for _, x := range list {
		if label != "" && x.ID != d.ID && x.CareLabel != nil && *x.CareLabel == label {
			return nil, problem.Newf(problem.DiskInUse, "The disk %s has the label %s. Choose another label.", x.ID, label)
		}
	}
	if label == "" {
		label = disks.NextLabel(used)
	}
	return s.Jobs.Start(ctx, jobs.Spec{Kind: jobs.KindDiskPrepare, Source: source, Lock: true, Reason: "prepare " + diskID},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			return s.prepareDisk(ctx, rec, diskID, label)
		})
}

func diskInUse(d *disks.Disk, label string, cfg *config.Config) error {
	switch {
	case d.Usage == disks.UsageSystem:
		return problem.Newf(problem.DiskInUse, "%s holds the system. Choose another disk.", d.Device)
	case d.Held != "":
		return problem.Newf(problem.DiskInUse, "%s: %s. care-pi never erases a disk that LVM, LUKS, or md RAID uses. Remove it from that layer, or choose another disk.", d.Device, d.Held)
	case d.Mounted:
		for _, p := range d.Partitions {
			if p.Mount != nil {
				return problem.Newf(problem.DiskInUse, "%s is mounted at %s. Unmount it, or choose another disk.", p.Device, *p.Mount)
			}
		}
		return problem.Newf(problem.DiskInUse, "%s is mounted. Unmount it, or choose another disk.", d.Device)
	}
	if cfg != nil {
		for _, dd := range cfg.Backups.Destinations {
			if dd.DiskLabel != "" && (dd.DiskLabel == label || (d.CareLabel != nil && *d.CareLabel == dd.DiskLabel)) {
				return problem.Newf(problem.DiskInUse, "The destination %s uses the label %s.", dd.ID, dd.DiskLabel)
			}
		}
	}
	return nil
}

func (s *Service) prepareDisk(ctx context.Context, rec *jobs.Recorder, diskID, label string) (any, error) {
	step := func(name, action string, fn func() error) error {
		end := rec.StepAction(name, action)
		err := fn()
		end(err)
		return err
	}
	var d *disks.Disk
	cfg, _ := config.Load(s.configPath())
	if err := step("check-disk: "+diskID, "check-disk", func() error {
		list, err := disks.List(ctx, s.Host.Runner, s.Paths)
		if err != nil {
			return err
		}
		if d = disks.Find(list, diskID); d == nil {
			return problem.Newf(problem.NotFound, "No disk has the id %s.", diskID)
		}
		return diskInUse(d, label, cfg)
	}); err != nil {
		return nil, err
	}
	wipe, part, format := disks.Commands(*d, label)
	for _, g := range []struct {
		name, action string
		cmds         []host.Cmd
	}{
		{"wipe-disk: " + d.Device, "wipe-disk", wipe},
		{"partition: " + d.Device + " (GPT, one partition)", "partition", part},
		{"format: ext4 " + label, "format", format},
	} {
		if err := step(g.name, g.action, func() error {
			for _, c := range g.cmds {
				out, err := s.Host.Runner.Run(ctx, c)
				if len(out) > 0 {
					rec.Log("stdout", string(out))
				}
				if err != nil {
					return err
				}
			}
			if g.action == "partition" {
				return disks.WaitDevice(ctx, s.Paths, disks.PartitionDevice(d.Device), 30*time.Second)
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	where := backup.DiskMount(label)
	mount, automount := backup.MountUnit(where), backup.AutomountUnit(where)
	if err := step("write-mount-unit: "+mount, "write-mount-unit", func() error {
		files, err := render.MountUnits(label)
		if err != nil {
			return err
		}
		for name, data := range files {
			if err := os.WriteFile(s.Paths.Abs(paths.SystemdDir+"/"+name), data, 0o644); err != nil {
				return err
			}
		}
		return s.Host.Systemd.DaemonReload(ctx)
	}); err != nil {
		return nil, err
	}
	if err := step("mount: "+where, "mount", func() error {
		if err := s.Host.Systemd.Start(ctx, automount, mount); err != nil {
			return err
		}
		pp, err := s.tester().Probe(ctx, where)
		if err != nil {
			return err
		}
		if !pp.WriteOK {
			return fmt.Errorf("cannot write %s: %s", where, pp.Error)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	_ = step("check-destinations: "+label, "check-destinations", func() error {
		for _, dd := range cfg.Backups.Destinations {
			if dd.DiskLabel == label {
				rec.SetDetail("the destination " + dd.ID + " uses " + label)
				return nil
			}
		}
		rec.SetDetail("no destination uses " + label + " yet: add {type: posix, disk_label: " + label + "}")
		return nil
	})
	size := int64(0)
	if _, total, err := backup.Free(s.Paths.Abs(where)); err == nil {
		size = total
	}
	return &DiskPrepareResult{Kind: jobs.KindDiskPrepare, DiskID: diskID, Device: disks.PartitionDevice(d.Device), Label: label,
		MountPath: where, FSType: "ext4", SizeBytes: size}, nil
}

// ---- rotation ----

// RotateRequest is POST /backups/recovery-key/rotate.
type RotateRequest struct {
	NewKey    bool
	RemoveOld bool
	Reason    string
}

// StartRotate starts a job of kind recovery-key-rotate.
func (s *Service) StartRotate(ctx context.Context, r RotateRequest, source string) (*jobs.Job, error) {
	if err := s.requireReady(); err != nil {
		return nil, err
	}
	st := s.RecoveryKeyStatus()
	if st.State == backup.KeyNone {
		return nil, problem.New(problem.NoRecoveryKey, "Make the recovery key first with POST /backups/recovery-key.")
	}
	if r.NewKey && (st.Pending == nil || st.Pending.ConfirmedAt == nil) {
		return nil, problem.New(problem.StateConflict, "A rotation with a new key needs a confirmed pending key: make it, save it, and confirm it first.")
	}
	if !r.NewKey && st.LocalRepository != backup.RepoLegacy {
		return nil, problem.New(problem.StateConflict, "The local repository does not use a random cipher pass; there is nothing to re-key.")
	}
	cfg, err := s.Config()
	if err != nil {
		return nil, err
	}
	return s.Jobs.Start(ctx, jobs.Spec{Kind: jobs.KindRecoveryKeyRotate, Source: source, Reason: r.Reason, Lock: true,
		OnStart: func(id string) { _ = s.keys().SetRotationJob(id) }},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			defer s.InvalidateBackups()
			e := s.restoreEnv(rec, cfg)
			return restore.Rotate(ctx, e, restore.RotateRequest{NewKey: r.NewKey, RemoveOld: r.RemoveOld, Source: source},
				func(ctx context.Context, pass string) error {
					if err := s.writeCipherPass(pass); err != nil {
						return err
					}
					return apply.SetPodmanSecret(ctx, s.Paths, s.Host.Podman, secrets.PodmanPGBackRestCipher, pass)
				})
		})
}

// ---- restore on this device ----

// RestoreRequest is POST /restore.
type RestoreRequest struct {
	Kind        string
	Time        *time.Time
	Checkpoint  string
	Reason      string
	Destination string
	SnapshotID  string
	Paths       []string
	Target      string
}

// Window returns the restore window: the end of the oldest full backup, and now.
func (s *Service) Window(ctx context.Context) (*time.Time, *time.Time) {
	ls := s.local(ctx)
	if ls.info == nil {
		return nil, nil
	}
	from := ls.info.WindowFrom()
	now := s.now()
	return from, &now
}

// StartRestore starts restore-pitr or restore-files.
func (s *Service) StartRestore(ctx context.Context, r RestoreRequest, source string) (*jobs.Job, error) {
	if err := s.requireReady(); err != nil {
		return nil, err
	}
	cfg, err := s.Config()
	if err != nil {
		return nil, err
	}
	if r.Kind == "files" {
		return s.startFiles(ctx, cfg, r, source)
	}
	s.InvalidateBackups()
	ls := s.local(ctx)
	if r.Checkpoint != "" {
		if ls.info == nil || ls.info.Find(r.Checkpoint) == nil {
			return nil, problem.Newf(problem.NotFound, "The local repository has no backup %s.", r.Checkpoint)
		}
	} else {
		from, to := s.Window(ctx)
		if from == nil || r.Time.Before(*from) || r.Time.After(*to) {
			p := problem.New(problem.RestoreWindowExceeded, "The time is outside the restore window.")
			p.Window = &problem.Window{}
			if from != nil {
				f, t := from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339)
				p.Window.From, p.Window.To = &f, &t
				p.Detail = "The earliest point in time is " + f + "."
			}
			return nil, p
		}
	}
	return s.Jobs.Start(ctx, jobs.Spec{Kind: jobs.KindRestorePITR, Source: source, Reason: r.Reason, Lock: true},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			defer s.InvalidateBackups()
			return restore.PITR(ctx, s.restoreEnv(rec, cfg), restore.PITRRequest{Time: r.Time, Checkpoint: r.Checkpoint, Reason: r.Reason, Source: source})
		})
}

func (s *Service) startFiles(ctx context.Context, cfg *config.Config, r RestoreRequest, source string) (*jobs.Job, error) {
	o := s.ops(nil, cfg, source)
	d, err := o.Dest(r.Destination)
	if err != nil {
		return nil, err
	}
	k, err := backup.ActiveKey(o.Env)
	if err != nil {
		return nil, err
	}
	if r.Target == "" {
		r.Target = restore.InPlace
	}
	return s.Jobs.Start(ctx, jobs.Spec{Kind: jobs.KindRestoreFiles, Source: source, Reason: r.Reason, Lock: true},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			return restore.Files(ctx, s.restoreEnv(rec, cfg), restore.FilesRequest{Dest: d, SnapshotID: r.SnapshotID,
				Paths: r.Paths, Target: r.Target, Source: source}, k)
		})
}

// ---- restore points ----

// SnapshotInfo is one snapshot (schema SnapshotInfo).
type SnapshotInfo struct {
	Destination *string   `json:"destination"`
	SnapshotID  string    `json:"snapshot_id"`
	Time        time.Time `json:"time"`
	Contents    []string  `json:"contents"`
	SizeBytes   *int64    `json:"size_bytes"`
	Hostname    string    `json:"hostname"`
	KeyID       string    `json:"key_id"`
}

// SnapshotListing is schema SnapshotListing.
type SnapshotListing struct {
	ID            string     `json:"id"`
	ListedAt      *time.Time `json:"listed_at"`
	OK            *bool      `json:"ok"`
	Error         *string    `json:"error"`
	SnapshotCount *int       `json:"snapshot_count"`
}

// PITRWindow is RestorePoints.pitr.
type PITRWindow struct {
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Timeline int       `json:"timeline"`
}

// RestorePoints is GET /restore/points (schema RestorePoints).
type RestorePoints struct {
	PITR          *PITRWindow          `json:"pitr"`
	FileSnapshots []SnapshotInfo       `json:"file_snapshots"`
	GeneratedAt   time.Time            `json:"generated_at"`
	DBBackups     []backup.LocalBackup `json:"db_backups"`
	Checkpoints   []backup.Checkpoint  `json:"checkpoints"`
	Destinations  []SnapshotListing    `json:"destinations"`
}

// Points returns the restore window and the restore points. refresh lists each destination now.
func (s *Service) Points(ctx context.Context, refresh bool) (*RestorePoints, error) {
	cfg, err := config.Load(s.configPath())
	if err != nil {
		cfg = config.Default()
	}
	if refresh {
		s.InvalidateBackups()
		if k, err := backup.ActiveKey(s.backupEnv(nil)); err == nil {
			o := s.ops(nil, cfg, jobs.SourceConsole)
			for _, d := range cfg.Backups.Destinations {
				c, cancel := context.WithTimeout(ctx, 60*time.Second)
				_ = o.List(c, d, k)
				cancel()
			}
		}
	}
	ls := s.local(ctx)
	st := backup.State{Paths: s.Paths}
	out := &RestorePoints{GeneratedAt: s.now().Truncate(time.Second), FileSnapshots: []SnapshotInfo{}, DBBackups: []backup.LocalBackup{},
		Checkpoints: []backup.Checkpoint{}, Destinations: []SnapshotListing{}}
	if ls.info != nil {
		for i := len(ls.info.Backups) - 1; i >= 0; i-- {
			out.DBBackups = append(out.DBBackups, ls.info.Backups[i])
		}
		if from := ls.info.WindowFrom(); from != nil {
			w := &PITRWindow{From: *from, To: *from, Timeline: 1}
			if ls.pg != nil && ls.pg.LastArchivedAt != nil && ls.pg.LastArchivedAt.After(w.To) {
				w.To = *ls.pg.LastArchivedAt
			}
			if last := ls.info.Last(); last != nil && last.FinishedAt.After(w.To) {
				w.To = last.FinishedAt
			}
			if ls.pg != nil && ls.pg.Timeline > 0 {
				w.Timeline = ls.pg.Timeline
			}
			out.PITR = w
		}
		st.PruneCheckpoints(ls.info)
	}
	if cps := st.Checkpoints(); cps != nil {
		out.Checkpoints = cps
	}
	for _, d := range cfg.Backups.Destinations {
		ds := st.Dest(d.ID)
		out.Destinations = append(out.Destinations, SnapshotListing{ID: d.ID, ListedAt: ds.ListedAt, OK: ds.ListOK,
			Error: ds.ListError, SnapshotCount: ds.SnapshotCount})
		id := d.ID
		for _, sn := range ds.Snapshots {
			out.FileSnapshots = append(out.FileSnapshots, SnapshotInfo{Destination: &id, SnapshotID: sn.ID, Time: sn.Time.UTC().Truncate(time.Second),
				Contents: sn.Contents, SizeBytes: sn.Size, Hostname: sn.Hostname, KeyID: ds.KeyID})
		}
	}
	sortSnapshots(out.FileSnapshots)
	return out, nil
}

func sortSnapshots(s []SnapshotInfo) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].Time.After(s[j-1].Time); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// ---- the setup step backups ----

// BackupsStepRequest is PUT /setup/steps/backups.
type BackupsStepRequest struct {
	Destinations []config.BackupDestination
	Credentials  map[string]backup.Credentials
	Words        []backup.WordInput
}

// StepBackups writes backups.destinations, stores the secrets, and confirms the recovery key.
func (s *Service) StepBackups(req BackupsStepRequest) (*SetupState, error) {
	if _, err := s.editable(); err != nil {
		return nil, err
	}
	if !s.keys().Exists(backup.Active) {
		return nil, problem.New(problem.NoRecoveryKey, "Make the recovery key first with POST /backups/recovery-key.")
	}
	for i, d := range req.Destinations {
		if d.Type == backup.TypeSFTP && d.HostKey == "" {
			return nil, problem.Invalid(fmt.Sprintf("/destinations/%d/host_key", i), "an sftp destination needs host_key: test it and accept its host key first", "host-key-not-pinned")
		}
	}
	for id := range req.Credentials {
		found := false
		for _, d := range req.Destinations {
			found = found || (d.ID == id && d.Type == backup.TypeS3)
		}
		if !found {
			return nil, problem.Invalid("/credentials/"+id, "no s3 destination has this id", "no-secrets-for-type")
		}
	}
	for i, d := range req.Destinations {
		if d.Type == backup.TypeS3 {
			if _, ok := req.Credentials[d.ID]; !ok && backup.ReadCredentials(s.Paths, d.ID) == nil {
				return nil, problem.Invalid(fmt.Sprintf("/credentials/%s", d.ID), "the s3 destination "+d.ID+" needs its keys", "missing-credentials").
					WithErrors(problem.FieldError{Pointer: fmt.Sprintf("/destinations/%d", i), Message: "no S3 keys", Code: "missing-credentials"})
			}
		}
	}
	if req.Words != nil {
		if _, err := s.ConfirmRecoveryKey(backup.Active, req.Words, "/recovery_key_words"); err != nil {
			return nil, err
		}
	} else if s.keys().Status().State != backup.KeyConfirmed {
		return nil, problem.Invalid("/recovery_key_words", "the recovery key is not confirmed: type the 3 words", "recovery-key-unconfirmed")
	}
	list := make([]any, 0, len(req.Destinations))
	for _, d := range req.Destinations {
		list = append(list, d)
	}
	err := s.updateConfig(func(doc map[string]any) {
		section(doc, "backups")["destinations"] = toAny(list)
	})
	if err != nil {
		return nil, rebase(err, "/backups", "")
	}
	for id, c := range req.Credentials {
		c.UpdatedAt = s.now().Truncate(time.Second)
		if err := backup.WriteCredentials(s.Paths, id, c); err != nil {
			return nil, err
		}
	}
	if err := s.markStep(setup.StepBackups, setup.Done, nil); err != nil {
		return nil, err
	}
	s.InvalidateBackups()
	return s.SetupState()
}

// backupsStepIssues returns the errors of the step backups for GET /setup.
func (s *Service) backupsStepIssues(cfg *config.Config) []setup.Issue {
	var out []setup.Issue
	st := s.keys().Status()
	switch st.State {
	case backup.KeyNone:
		out = append(out, setup.Issue{Code: "no-recovery-key", Message: "make the recovery key"})
	case backup.KeyUnconfirmed:
		out = append(out, setup.Issue{Code: "recovery-key-unconfirmed", Message: "confirm the recovery key"})
	}
	if len(cfg.Backups.Destinations) == 0 {
		out = append(out, setup.Issue{Code: "no-destination", Message: "add 1 or more destinations"})
	}
	for i, d := range cfg.Backups.Destinations {
		ptr := fmt.Sprintf("/destinations/%d", i)
		if backup.CredentialsState(s.Paths, d) == backup.CredMissing {
			out = append(out, setup.Issue{Code: "missing-credentials", Message: d.ID + " needs its S3 keys", Pointer: &ptr})
		}
		if d.Type == backup.TypeSFTP && d.HostKey == "" {
			out = append(out, setup.Issue{Code: "host-key-not-pinned", Message: d.ID + " has no pinned host key", Pointer: &ptr})
		}
	}
	return out
}

// BackupsDraft is SetupDraft.backups (schema BackupsDraft).
type BackupsDraft struct {
	Destinations         []config.BackupDestination `json:"destinations"`
	RecoveryKeyConfirmed bool                       `json:"recovery_key_confirmed"`
	RecoveryKey          backup.KeyStatus           `json:"recovery_key"`
	Credentials          map[string]string          `json:"credentials"`
}

func (s *Service) backupsDraft(cfg *config.Config) *BackupsDraft {
	st := s.RecoveryKeyStatus()
	d := &BackupsDraft{Destinations: cfg.Backups.Destinations, RecoveryKeyConfirmed: st.State == backup.KeyConfirmed,
		RecoveryKey: st, Credentials: map[string]string{}}
	if d.Destinations == nil {
		d.Destinations = []config.BackupDestination{}
	}
	for _, dd := range cfg.Backups.Destinations {
		if dd.Type == backup.TypeS3 {
			d.Credentials[dd.ID] = backup.CredentialsState(s.Paths, dd)
		}
	}
	return d
}

// ---- restore from backup (a new device) ----

// DRRequest is POST /restore/dr and POST /restore/dr/inspect.
type DRRequest struct {
	Key          backup.Key
	Destination  config.BackupDestination
	Credentials  *backup.Credentials
	Time         *time.Time
	SnapshotID   string
	Files        bool
	PauseBackups bool
}

func (s *Service) drAllowed() error {
	in, err := s.setupInput()
	if err != nil {
		return err
	}
	switch setup.StateName(in) {
	case setup.Ready:
		return problem.New(problem.SetupFinished, "A restore from backup is possible only before the setup is finished.")
	case setup.Applying:
		return problem.Running(in.Record.JobID)
	case setup.Unclaimed:
		return problem.New(problem.NotClaimed, "The device is not claimed.")
	}
	return nil
}

// DrInspection is the response of POST /restore/dr/inspect (schema DrInspection).
type DrInspection struct {
	KeyID               string         `json:"key_id"`
	Location            string         `json:"location"`
	Source              DrSource       `json:"source"`
	Snapshots           []SnapshotInfo `json:"snapshots"`
	NewestDBSnapshot    *SnapshotInfo  `json:"newest_db_snapshot"`
	NewestFilesSnapshot *SnapshotInfo  `json:"newest_files_snapshot"`
	PITR                *DrWindow      `json:"pitr"`
	Sizes               DrSizes        `json:"sizes"`
	Space               DrSpace        `json:"space"`
	Warnings            []setup.Issue  `json:"warnings"`
}

// DrSource is DrInspection.source.
type DrSource struct {
	DisplayName  string    `json:"display_name"`
	Hostname     string    `json:"hostname"`
	AgentVersion *string   `json:"agent_version"`
	Solution     *string   `json:"solution"`
	ConfigTime   time.Time `json:"config_time"`
}

// DrWindow is DrInspection.pitr.
type DrWindow struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// DrSizes is DrInspection.sizes.
type DrSizes struct {
	DBRepositoryBytes int64 `json:"db_repository_bytes"`
	FilesBytes        int64 `json:"files_bytes"`
	DatabaseBytes     int64 `json:"database_bytes"`
}

// DrSpace is DrInspection.space.
type DrSpace struct {
	Path          string `json:"path"`
	RequiredBytes int64  `json:"required_bytes"`
	FreeBytes     int64  `json:"free_bytes"`
	OK            bool   `json:"ok"`
}

// checkCandidate checks the host key of an sftp candidate: without host_key the answer is the
// problem destination-untrusted-hostkey with the key that the server presents.
func (s *Service) checkCandidate(ctx context.Context, d config.BackupDestination) error {
	if d.Type != backup.TypeSFTP || d.HostKey != "" {
		return nil
	}
	ed := backup.Effective(d, config.Default().Backups)
	pk, err := s.tester().SSH.HostKey(ctx, net.JoinHostPort(ed.Host, strconv.Itoa(ed.Port)), nil)
	if err != nil {
		return problem.Unreachable("", fmt.Sprintf("%s:%d: %v", ed.Host, ed.Port, err))
	}
	p := problem.Newf(problem.DestinationUntrusted, "%s presents a host key that is not pinned. Compare the fingerprint with the server.", ed.Host)
	p.HostKey = backup.HostKeyOf(pk, false)
	return p
}

// InspectDR reads what a destination holds for a recovery key. It writes nothing.
func (s *Service) InspectDR(ctx context.Context, r DRRequest) (*DrInspection, error) {
	if err := s.drAllowed(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := s.checkCandidate(ctx, r.Destination); err != nil {
		return nil, err
	}
	sess, err := restore.OpenCandidate(s.backupEnv(nil), r.Destination, r.Credentials, r.Key)
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	list, err := restore.Snapshots(ctx, s.Host.Runner, sess)
	if err != nil {
		return nil, err
	}
	out := &DrInspection{KeyID: r.Key.ID(), Location: backup.Location(sess.Repo.Dest, r.Key.ID()), Snapshots: []SnapshotInfo{}, Warnings: []setup.Issue{}}
	view := func(sn backup.Snapshot) SnapshotInfo {
		return SnapshotInfo{SnapshotID: sn.ShortID, Time: sn.Time.UTC().Truncate(time.Second), Contents: sn.Contents(), SizeBytes: sn.Size(), Hostname: sn.Hostname, KeyID: r.Key.ID()}
	}
	for i, sn := range list {
		if i >= 50 {
			break
		}
		out.Snapshots = append(out.Snapshots, view(sn))
	}
	if len(list) == 0 {
		return nil, problem.New(problem.RecoveryKeyMismatch, "The repository of this recovery key has no snapshot.")
	}
	db := restore.Pick(list, backup.TagDB, r.SnapshotID)
	files := restore.Pick(list, backup.TagFiles, "")
	if db != nil {
		v := view(*db)
		out.NewestDBSnapshot = &v
		if sz := db.Size(); sz != nil {
			out.Sizes.DBRepositoryBytes = *sz
		}
		if bk, err := restore.RepoBackups(ctx, s.Host.Runner, sess, db.ID); err == nil && len(bk) > 0 {
			for _, b := range bk {
				if b.Type == "full" {
					out.PITR = &DrWindow{From: b.FinishedAt, To: db.Time.UTC().Truncate(time.Second)}
					break
				}
			}
			out.Sizes.DatabaseBytes = bk[len(bk)-1].SizeBytes
		}
	}
	if files != nil {
		v := view(*files)
		out.NewestFilesSnapshot = &v
		if sz := files.Size(); sz != nil {
			out.Sizes.FilesBytes = *sz
		}
	} else {
		out.Warnings = append(out.Warnings, setup.Issue{Code: "no-files-snapshot", Message: "the destination has no files snapshot"})
	}
	src := list[0]
	if cfg, lk, err := restore.SourceInfo(ctx, s.Host.Runner, sess, src.ID); err == nil {
		out.Source = DrSource{DisplayName: cfg.Identity.DisplayName, Hostname: cfg.Identity.Hostname, ConfigTime: src.Time.UTC().Truncate(time.Second),
			Solution: strPtr(cfg.Solution.Name), AgentVersion: strPtr(lk.AgentVersion)}
		if db != nil {
			interval := 15
			for _, d := range cfg.Backups.Destinations {
				if d.IntervalMin > 0 {
					interval = d.IntervalMin
				}
			}
			if s.now().Sub(db.Time) < 2*time.Duration(interval)*time.Minute {
				out.Warnings = append(out.Warnings, setup.Issue{Code: "source-may-be-active", Message: "the newest snapshot is younger than 2 intervals: the old device can still run"})
			}
		}
	} else {
		out.Source = DrSource{ConfigTime: src.Time.UTC().Truncate(time.Second), Hostname: src.Hostname, DisplayName: src.Hostname}
	}
	if db != nil && files != nil && db.Time.Sub(files.Time) > time.Hour {
		out.Warnings = append(out.Warnings, setup.Issue{Code: "files-older-than-db",
			Message: fmt.Sprintf("the newest files snapshot is %d hours older than the database", int(db.Time.Sub(files.Time).Hours()))})
	}
	out.Space.Path = paths.DataDir
	out.Space.RequiredBytes = out.Sizes.DBRepositoryBytes + out.Sizes.FilesBytes + out.Sizes.DatabaseBytes + 1<<30
	if free, _, err := backup.Free(s.Paths.Abs(paths.DataDir)); err == nil {
		out.Space.FreeBytes = free
	}
	out.Space.OK = out.Space.FreeBytes > out.Space.RequiredBytes
	return out, nil
}

// StartDR starts the job restore-dr. The setup state becomes applying.
func (s *Service) StartDR(ctx context.Context, r DRRequest, source string) (*jobs.Job, error) {
	if err := s.drAllowed(); err != nil {
		return nil, err
	}
	if err := s.checkCandidate(ctx, r.Destination); err != nil {
		return nil, err
	}
	var restored atomic.Bool
	return s.Jobs.Start(ctx, jobs.Spec{Kind: jobs.KindRestoreDR, Source: source, Reason: "restore from backup", Lock: true,
		OnStart: func(id string) {
			_, err := s.Setup.Update(func(rec *setup.Record) error {
				rec.JobID = id
				rec.RestoreJobID = id
				return nil
			})
			if err != nil {
				s.log().Error("cannot record the restore job", "error", err)
			}
		},
		AfterDone: func(j *jobs.Job) {
			if restored.Load() {
				s.EndSessionsAfterDone(j.ID, source)
			}
			if j.State != jobs.StateSucceeded {
				return
			}
			// The local CA of the old device, after the event done (docs/backups.md, section 8.3).
			switched, err := restore.SwitchCA(context.Background(), s.Paths, s.Host.Runner, j.ID)
			switch {
			case err != nil:
				s.log().Error("cannot use the local CA of the old device; the clinic devices must trust the new root CA", "error", err)
			case switched:
				s.log().Info("the local CA of the old device is back; care-caddy restarted", "job", j.ID)
			}
		}}, func(ctx context.Context, rec *jobs.Recorder) (any, error) {
		defer s.InvalidateBackups()
		cfg, _ := config.Load(s.configPath())
		if cfg == nil {
			cfg = config.Default()
		}
		e := s.restoreEnv(rec, cfg)
		return restore.DR(ctx, e, restore.DRRequest{Key: r.Key, Dest: r.Destination, Creds: r.Credentials, Time: r.Time,
			SnapshotID: r.SnapshotID, Files: r.Files, Pause: r.PauseBackups, Source: source}, s.drDeps(rec, &restored))
	})
}

// StreamGrace is the longest wait of the job restore-dr for its open event streams, after the event
// done and before the end of the sessions. A job of the CLI also waits 2 poll intervals of the
// streams of care-pi.service (another process reads the journal each 500 ms).
var StreamGrace = 5 * time.Second

// EndSessionsAfterDone ends each session after each event stream of the job sent the event done, so
// the console gets the result (with the clinic name) before its session ends.
func (s *Service) EndSessionsAfterDone(id, source string) {
	s.Jobs.WaitUnwatched(id, StreamGrace)
	if source == jobs.SourceCLI {
		time.Sleep(StreamGrace / 5)
	}
	if err := s.Auth.RevokeAll(); err != nil {
		s.log().Error("cannot end the sessions after the restore", "error", err)
		return
	}
	s.log().Info("auth: each session ended after the restore from backup", "job", id)
}

func (s *Service) drDeps(rec *jobs.Recorder, restored *atomic.Bool) restore.DRDeps {
	return restore.DRDeps{
		Apply: func(ctx context.Context, noStart bool) error {
			o := s.applyOptions(apply.Options{Reason: "restore from backup", Out: rec.Writer(), Journal: rec, LockHeld: true,
				Phase: setup.PhaseFull, NoStart: noStart, NoFirstBackup: true})
			_, err := apply.Run(ctx, o)
			return applyProblem(err)
		},
		WritePaused: func() error {
			return s.updateConfig(func(doc map[string]any) { section(doc, "backups")["paused"] = true })
		},
		SetHostname: func(ctx context.Context, h string) error {
			current := strings.SplitN(readTrim(s.Paths.Abs(etcHostname)), ".", 2)[0]
			if current == "" || h == "" || current == h {
				return nil
			}
			return s.applyHostname(ctx, h)
		},
		RestoreAccount: func(data []byte) error {
			if err := s.Auth.RestoreAccount(data); err != nil {
				return err
			}
			restored.Store(true)
			return nil
		},
		Finish: func() error {
			now := s.now()
			_, err := s.Setup.Update(func(r *setup.Record) error {
				r.ReadyAt, r.JobID, r.ApplyPhase = &now, "", setup.PhaseFull
				for _, d := range setup.Steps {
					if d.ID != setup.StepAccount {
						r.Steps[d.ID] = &setup.StepRecord{Status: setup.Done, UpdatedAt: now}
					}
				}
				return nil
			})
			return err
		},
		URLs: func(cfg *config.Config) (string, string, []string) {
			primary := s.PrimaryHost()
			if h := cfg.Identity.Hostname; cfg.TLS.LocalCA.Enabled && h != "" {
				primary = netinfo.MDNSName(h)
			}
			return "https://" + primary + "/", fmt.Sprintf("https://%s:%d/", primary, paths.ConsolePort), s.CareURLs()
		},
	}
}

func toAny(v []any) []any {
	out := make([]any, 0, len(v))
	for _, x := range v {
		m := map[string]any{}
		data, _ := json.Marshal(x)
		_ = json.Unmarshal(data, &m)
		out = append(out, m)
	}
	return out
}
