package update

import (
	"context"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/backup"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
)

// CheckpointRef is the result of a checkpoint (docs/solutions.md, section 8.4).
type CheckpointRef struct {
	// BackupLabel is the pgBackRest label of the incremental backup ("" when unknown).
	BackupLabel string
	// RestorePoint is the name of a PostgreSQL restore point ("": the checkpoint makes none).
	RestorePoint string
	// TargetTime is the PITR target: after the end of the backup, before the migrations.
	TargetTime time.Time
	TakenAt    time.Time
	Duration   time.Duration
}

// Checkpoint returns the API form (schema UpdateCheckpoint).
func (r CheckpointRef) Checkpoint() *Checkpoint {
	return &Checkpoint{TakenAt: r.TakenAt.UTC().Truncate(time.Second), TargetTime: r.TargetTime.UTC().Truncate(time.Second),
		RestorePoint: strp(r.RestorePoint), BackupLabel: strp(r.BackupLabel),
		DurationS: float64(r.Duration.Milliseconds()) / 1000}
}

// Checkpointer makes the checkpoint before the migrations. Care (api, worker, beat) must be stopped.
// BackupCheckpointer is the implementation; the tests use fakes.
type Checkpointer interface {
	Checkpoint(ctx context.Context) (CheckpointRef, error)
}

// BackupCheckpointer is the checkpoint of Phase 3 (docs/api/README.md section 13.11, docs/solutions.md section 8.4):
// an incremental pgBackRest backup to the local repository under the local backup lock. The backup ends with
// pg_backup_stop, which switches the WAL segment, and pgBackRest waits until the archive has it (--archive-check),
// so the checkpoint is also the archive push. It records the checkpoint in RestorePoints.checkpoints, so the
// Restore page offers it (POST /restore with {kind: pitr, checkpoint: <label>}).
type BackupCheckpointer struct {
	Ops backup.Ops
	// Label is the job id of the change, or another label (a plan action cutover outside a job).
	Label string
	Now   func() time.Time
}

func (c BackupCheckpointer) now() time.Time {
	if c.Now == nil {
		return time.Now().UTC()
	}
	return c.Now().UTC()
}

// Checkpoint implements Checkpointer.
func (c BackupCheckpointer) Checkpoint(ctx context.Context) (CheckpointRef, error) {
	start := c.now()
	ref := CheckpointRef{TakenAt: start}
	id, kind := jobOf(c.Label)
	reason := "before " + c.Label
	if id != "" {
		reason = "before " + kind + " " + id
	}
	res, err := c.Ops.LocalBackup(ctx, "incr", reason)
	if err != nil {
		return ref, err
	}
	cp := backup.Checkpoint{Label: res.Label, UndoPoint: res.UndoPoint, Timeline: res.Timeline, Reason: &reason}
	if id != "" {
		cp.JobID, cp.JobKind = &id, &kind
	}
	_ = c.Ops.State().AddCheckpoint(cp)
	end := c.now()
	ref.BackupLabel = res.Label
	// The PITR target is after the end of the backup (pgbackrest info has whole seconds, and a target before the
	// consistent point of the backup fails), and before the migrations: Care is stopped until they start.
	ref.TargetTime = end.Truncate(time.Second)
	if min := res.UndoPoint.UTC().Truncate(time.Second).Add(time.Second); ref.TargetTime.Before(min) {
		ref.TargetTime = min
	}
	ref.Duration = end.Sub(start)
	return ref, nil
}

// jobOf returns the id and the kind of a label that is a job id, else "", "".
func jobOf(label string) (id, kind string) {
	if !jobs.IDPattern.MatchString(label) {
		return "", ""
	}
	_, kind, _ = strings.Cut(label, "Z-")
	return label, kind
}

// offerOf returns the PITR offer to a checkpoint (docs/solutions.md, section 8.5).
func offerOf(c *Checkpoint) *PitrOffer {
	if c == nil {
		return nil
	}
	return &PitrOffer{Available: true, CheckpointAt: c.TakenAt, TargetTime: c.TargetTime, RestorePoint: c.RestorePoint,
		Restore: RestoreRequest{Kind: "pitr", Time: c.TargetTime}}
}
