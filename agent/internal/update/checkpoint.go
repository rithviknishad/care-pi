package update

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/render"
)

// CheckpointRef is the result of a checkpoint (docs/solutions.md, section 8.4).
type CheckpointRef struct {
	// BackupLabel is the pgBackRest label of the incremental backup ("" when unknown).
	BackupLabel string
	// RestorePoint is the name of the PostgreSQL restore point, care-pi-<job id>.
	RestorePoint string
	// TargetTime is the PITR target: after the restore point, before the migrations.
	TargetTime time.Time
	TakenAt    time.Time
	Duration   time.Duration
}

// Checkpoint returns the API form (schema Checkpoint).
func (r CheckpointRef) Checkpoint() *Checkpoint {
	return &Checkpoint{TakenAt: r.TakenAt.UTC().Truncate(time.Second), TargetTime: r.TargetTime.UTC().Truncate(time.Second),
		RestorePoint: strp(r.RestorePoint), BackupLabel: strp(r.BackupLabel),
		DurationS: float64(r.Duration.Milliseconds()) / 1000}
}

// Checkpointer makes a restore point before the migrations. Care (api, worker, beat) must be stopped.
// Phase 3 owns the real implementation (backup.Checkpoint); PGBackRestStub is the stub until the merge.
type Checkpointer interface {
	Checkpoint(ctx context.Context) (CheckpointRef, error)
}

// PGBackRestStub runs the three commands of docs/solutions.md section 8.4 in care-db:
// an incremental pgBackRest backup, a restore point care-pi-<label>, and pgbackrest check
// (a WAL switch and a wait until the segment is in the archive). It is minimal on purpose:
// the Phase 3 merge replaces it with backup.Checkpoint.
type PGBackRestStub struct {
	Host  *host.Host
	Label string
	Now   func() time.Time
}

func (c PGBackRestStub) now() time.Time {
	if c.Now == nil {
		return time.Now().UTC()
	}
	return c.Now().UTC()
}

// Checkpoint implements Checkpointer.
func (c PGBackRestStub) Checkpoint(ctx context.Context) (CheckpointRef, error) {
	start := c.now()
	ref := CheckpointRef{TakenAt: start, RestorePoint: "care-pi-" + c.Label}
	stanza := "--stanza=" + render.StanzaName
	run := func(args ...string) ([]byte, error) {
		out, err := c.Host.Podman.Exec(ctx, "care-db", "postgres", args...)
		if err != nil {
			return out, fmt.Errorf("%s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return out, nil
	}
	if _, err := run("pgbackrest", stanza, "--type=incr", "backup"); err != nil {
		return ref, err
	}
	if out, err := run("pgbackrest", stanza, "--output=json", "info"); err == nil {
		ref.BackupLabel = lastBackupLabel(out)
	}
	if _, err := run("psql", "--no-psqlrc", "--quiet", "--tuples-only", "--command",
		"SELECT pg_create_restore_point('"+ref.RestorePoint+"')"); err != nil {
		return ref, err
	}
	if _, err := run("pgbackrest", stanza, "check"); err != nil {
		return ref, err
	}
	ref.TargetTime = c.now()
	ref.Duration = ref.TargetTime.Sub(start)
	return ref, nil
}

// lastBackupLabel reads the label of the newest backup from "pgbackrest info --output=json".
func lastBackupLabel(out []byte) string {
	var info []struct {
		Backup []struct {
			Label string `json:"label"`
		} `json:"backup"`
	}
	if json.Unmarshal(out, &info) != nil || len(info) == 0 || len(info[0].Backup) == 0 {
		return ""
	}
	return info[0].Backup[len(info[0].Backup)-1].Label
}

// offerOf returns the PITR offer to a checkpoint (docs/solutions.md, section 8.5).
func offerOf(c *Checkpoint) *PitrOffer {
	if c == nil {
		return nil
	}
	return &PitrOffer{Available: true, CheckpointAt: c.TakenAt, TargetTime: c.TargetTime, RestorePoint: c.RestorePoint,
		Restore: RestoreRequest{Kind: "pitr", TargetTime: c.TargetTime}}
}
