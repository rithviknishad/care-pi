package restore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/backup"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
)

// PITRRequest is one point-in-time restore on this device.
type PITRRequest struct {
	// Time or Checkpoint (a backup label) selects the target.
	Time       *time.Time
	Checkpoint string
	Reason     string
	Source     string
}

// PITRResult is the result of a restore-pitr job (schema RestorePitrResult).
type PITRResult struct {
	Kind            string            `json:"kind"`
	RestoredTo      time.Time         `json:"restored_to"`
	Timeline        int               `json:"timeline"`
	Checkpoint      backup.Checkpoint `json:"checkpoint"`
	FullBackupLabel *string           `json:"full_backup_label"`
	DowntimeS       float64           `json:"downtime_s"`
}

// PITR runs the steps of restore-pitr (docs/api/README.md section 13.9.1). When the restore or the
// start of the database fails, the job restores its checkpoint (the state before the job), starts
// the stack, and fails with the first error.
func PITR(ctx context.Context, e Env, req PITRRequest) (*PITRResult, error) {
	res := &PITRResult{Kind: jobs.KindRestorePITR}
	ops := e.Ops(req.Source)
	if err := e.PauseBackups(ctx); err != nil {
		return nil, err
	}
	defer func() { _ = e.ResumeBackups(context.WithoutCancel(ctx)) }()
	downStart := time.Now()
	if err := e.Maintenance(true); err != nil {
		return nil, err
	}
	appStopped := false
	cleanup := func(err error) (*PITRResult, error) {
		bg := context.WithoutCancel(ctx)
		if appStopped {
			_ = e.Step("start-app: care.target", "start-app", func() error { return e.StartTarget(bg) })
			_ = e.Step("wait-healthy: care-api.service", "wait-healthy", func() error { return e.Healthy(bg) })
		}
		_ = e.Maintenance(false)
		return nil, err
	}
	if err := e.Step("stop-app: care-api care-worker care-beat", "stop-app", func() error {
		appStopped = true
		return e.StopUnits(ctx, AppUnits...)
	}); err != nil {
		return cleanup(err)
	}
	reason := req.Reason
	if reason == "" {
		reason = "before restore-pitr " + e.Rec.ID()
	}
	var cp *backup.LocalResult
	if err := e.Step("checkpoint: incremental backup", "checkpoint", func() error {
		var err error
		cp, err = ops.LocalBackup(ctx, "incr", reason)
		if err == nil {
			e.Rec.SetDetail(cp.Label)
		}
		return err
	}); err != nil {
		return cleanup(fmt.Errorf("no restore without a checkpoint: %w", err))
	}
	id, kind := e.Rec.ID(), jobs.KindRestorePITR
	res.Checkpoint = backup.Checkpoint{Label: cp.Label, UndoPoint: cp.UndoPoint, Timeline: cp.Timeline, JobID: &id, JobKind: &kind}
	if req.Reason != "" {
		r := req.Reason
		res.Checkpoint.Reason = &r
	}
	_ = e.State().AddCheckpoint(res.Checkpoint)

	if err := e.Step("stop-db: care-db.service", "stop-db", func() error { return e.StopUnits(ctx, "care-db.service") }); err != nil {
		return cleanup(err)
	}
	target := "checkpoint " + req.Checkpoint
	if req.Time != nil {
		target = req.Time.UTC().Format(time.RFC3339)
	}
	restoreErr := e.Step("restore-db: "+target, "restore-db", func() error { return e.RunRestore(ctx, req.Time, req.Checkpoint) })
	if restoreErr == nil {
		restoreErr = e.startDB(ctx)
	}
	if restoreErr != nil {
		e.logf("the restore failed; restore the checkpoint %s (the state before the job)", cp.Label)
		bg := context.WithoutCancel(ctx)
		rb := e.Step("stop-db: care-db.service (rollback)", "stop-db", func() error { return e.StopUnits(bg, "care-db.service") })
		if rb == nil {
			rb = e.Step("restore-db: rollback to the checkpoint "+cp.Label, "restore-db", func() error { return e.RunRestore(bg, nil, cp.Label) })
		}
		if rb == nil {
			rb = e.startDB(bg)
		}
		if rb != nil {
			e.logf("the rollback failed too: %v", rb)
		}
		return cleanup(restoreErr)
	}
	if err := e.Step("start-app: care.target", "start-app", func() error { return e.StartTarget(ctx) }); err != nil {
		return cleanup(err)
	}
	appStopped = false
	if err := e.Step("wait-healthy: care-api.service", "wait-healthy", func() error { return e.Healthy(ctx) }); err != nil {
		_ = e.Maintenance(false)
		return nil, err
	}
	if err := e.Maintenance(false); err != nil {
		return nil, err
	}
	res.DowntimeS = time.Since(downStart).Seconds()
	res.Timeline = 1
	if st, err := (backup.PG{Podman: e.Podman}).State(ctx); err == nil && st.Timeline > 0 {
		res.Timeline = st.Timeline
	}
	res.RestoredTo = res.Checkpoint.UndoPoint
	if req.Time != nil {
		res.RestoredTo = req.Time.UTC()
	} else if ri, err := (backup.PG{Podman: e.Podman}).Info(ctx); err == nil {
		if b := ri.Find(req.Checkpoint); b != nil {
			res.RestoredTo = b.FinishedAt
		}
	}
	var full *backup.LocalResult
	ferr := e.Step("backup-full: a new full backup of the new timeline", "backup-full", func() error {
		var err error
		full, err = ops.LocalBackup(ctx, "full", "after restore-pitr "+e.Rec.ID())
		return err
	})
	if ferr == nil {
		res.FullBackupLabel = &full.Label
		_ = e.Step("check-stanza: pgbackrest check", "check-stanza", func() error {
			_, err := (backup.PG{Podman: e.Podman}).Exec(ctx, "check")
			return err
		})
	} else {
		e.logf("the full backup failed: %v; the timer tries again", ferr)
		_, _ = e.State().UpdateLocal(func(l *backup.LocalState) {
			ok, now := false, time.Now().UTC().Truncate(time.Second)
			l.LastScheduledOK, l.LastScheduledAt = &ok, &now
			l.FullBackupNeeded = true
		})
	}
	return res, nil
}

// startDB starts care-db and waits for the end of the recovery.
func (e Env) startDB(ctx context.Context) error {
	if err := e.Step("start-db: care-db.service", "start-db", func() error {
		if err := e.Systemd.ResetFailed(ctx, "care-db.service"); err != nil {
			return err
		}
		return e.systemctl(ctx, "start", "care-db.service")
	}); err != nil {
		return err
	}
	return e.Step("wait-promote: the end of the recovery", "wait-promote", func() error { return e.WaitPromote(ctx) })
}

// ErrNoBackup tells that the local repository has no backup.
var ErrNoBackup = errors.New("the local repository has no backup")
