package update

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/backup"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// TestBackupCheckpointer: the checkpoint of an update is the checkpoint of Phase 3 (docs/api/README.md section 13.11):
// an incremental backup with an archive check under the local lock, recorded in RestorePoints.checkpoints with its job.
func TestBackupCheckpointer(t *testing.T) {
	p := paths.New(t.TempDir())
	f := host.NewFake()
	stop := time.Date(2026, 11, 22, 2, 5, 40, 0, time.UTC)
	f.On("podman exec --user postgres care-db pgbackrest --stanza=main info --output=json", host.Response{Out: fmt.Sprintf(
		`[{"name":"main","archive":[{"min":"000000010000000000000001","max":"000000010000000000000009"}],`+
			`"backup":[{"label":"20261115-010000F","type":"full","timestamp":{"start":%d,"stop":%d}},`+
			`{"label":"20261115-010000F_20261122-020512I","type":"incr","timestamp":{"start":%d,"stop":%d}}],"status":{"code":0,"message":"ok"}}]`,
		stop.Add(-7*24*time.Hour).Unix(), stop.Add(-7*24*time.Hour+time.Minute).Unix(), stop.Add(-28*time.Second).Unix(), stop.Unix())})
	h := host.New(f)
	clock := stop.Add(-30 * time.Second)
	now := func() time.Time { clock = clock.Add(time.Second); return clock }
	const id = "20261122T020300.000Z-update-apply"
	c := BackupCheckpointer{Ops: backup.Ops{Env: backup.Env{Paths: p, Runner: f, Systemd: h.Systemd, Podman: h.Podman, Now: now}},
		Label: id, Now: now}
	ref, err := c.Checkpoint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var backupCmd string
	for _, l := range f.Calls {
		if strings.Contains(l, "pgbackrest --stanza=main backup ") {
			backupCmd = l
		}
	}
	if !strings.Contains(backupCmd, "--type=incr --archive-check") || !strings.Contains(backupCmd, "before update-apply "+id) {
		t.Fatalf("backup command %q", backupCmd)
	}
	if ref.BackupLabel != "20261115-010000F_20261122-020512I" || ref.RestorePoint != "" || !ref.TargetTime.After(stop) {
		t.Fatalf("ref %+v", ref)
	}
	cp := ref.Checkpoint()
	if cp.RestorePoint != nil || *cp.BackupLabel != ref.BackupLabel {
		t.Fatalf("checkpoint %+v", cp)
	}
	if o := offerOf(cp); o.Restore.Kind != "pitr" || !o.Restore.Time.Equal(cp.TargetTime) {
		t.Fatalf("offer %+v", o)
	}
	got := (backup.State{Paths: p}).Checkpoints()
	if len(got) != 1 || got[0].Label != ref.BackupLabel || !got[0].UndoPoint.Equal(stop) || got[0].JobID == nil || *got[0].JobID != id ||
		got[0].JobKind == nil || *got[0].JobKind != "update-apply" {
		t.Fatalf("recorded checkpoints %+v", got)
	}

	// A label that is not a job id (the plan action cutover outside a job) records no job.
	if _, err := (BackupCheckpointer{Ops: c.Ops, Label: "apply-20261122T020300Z", Now: now}).Checkpoint(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := (backup.State{Paths: p}).Checkpoints(); len(got) != 2 || got[0].JobID != nil || got[0].JobKind != nil {
		t.Fatalf("recorded checkpoints %+v", got)
	}
}
