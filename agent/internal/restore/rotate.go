package restore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/rithviknishad/care-pi/agent/internal/backup"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/secrets"
)

// RotateRequest is one rotation (docs/api/README.md sections 13.4 and 13.12).
type RotateRequest struct {
	// NewKey uses the confirmed pending key. false re-keys only the local repository.
	NewKey    bool
	RemoveOld bool
	Source    string
}

// SetCipherPass writes the cipher pass file and the podman secret of care-db.
type SetCipherPass func(ctx context.Context, pass string) error

// LegacyDir is the old local repository of a re-key.
const LegacyDir = paths.BackupPG + ".legacy"

// Rotate runs the steps of recovery-key-rotate. Each step checks its result first, so a failed
// rotation can run again and continues where it stopped.
func Rotate(ctx context.Context, e Env, req RotateRequest, setPass SetCipherPass) (*backup.RotateResult, error) {
	ks := e.Keys()
	old, _, err := ks.Get(backup.Active)
	if err != nil {
		return nil, err
	}
	target := old
	if req.NewKey {
		if target, _, err = ks.Get(backup.Pending); err != nil {
			return nil, err
		}
	}
	res := &backup.RotateResult{Kind: jobs.KindRecoveryKeyRotate, NewKey: req.NewKey, KeyID: target.ID(),
		Destinations: []backup.RotateDest{}, RemovedRepositories: []string{}}
	if req.NewKey {
		id := old.ID()
		res.PreviousKeyID = &id
	}
	ops := e.Ops(req.Source)
	dests := e.Config.Backups.Destinations
	if err := e.Step(fmt.Sprintf("check-destinations: %d destination(s) with the key %s", len(dests), target.ID()), "check-destinations", func() error {
		for _, d := range dests {
			s, err := backup.OpenDest(e.Env, e.Config, d, target)
			if err != nil {
				return err
			}
			_, err = s.Run(ctx, e.Runner, "cat", "config")
			s.Close()
			var rm *backup.RepoMissingError
			if err != nil && !errors.As(err, &rm) {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := e.PauseBackups(ctx); err != nil {
		return nil, err
	}
	defer func() { _ = e.ResumeBackups(context.WithoutCancel(ctx)) }()
	if err := e.Maintenance(true); err != nil {
		return nil, err
	}
	up := false
	restart := func(err error) (*backup.RotateResult, error) {
		bg := context.WithoutCancel(ctx)
		if !up {
			_ = e.Step("start-app: care.target", "start-app", func() error { return e.StartTarget(bg) })
		}
		_ = e.Maintenance(false)
		return nil, err
	}
	if err := e.Step("stop-app: care-api care-worker care-beat", "stop-app", func() error { return e.StopUnits(ctx, AppUnits...) }); err != nil {
		return restart(err)
	}
	if err := e.Step("stop-db: care-db.service", "stop-db", func() error { return e.StopUnits(ctx, "care-db.service") }); err != nil {
		return restart(err)
	}
	if err := e.Step("rekey-local: "+paths.BackupPG, "rekey-local", func() error {
		cur, _ := secrets.Store{Paths: e.Paths}.Read(secrets.PGBackRestCipher)
		_, legacyErr := os.Stat(e.Paths.Abs(LegacyDir))
		if cur == target.CipherPass() && legacyErr == nil {
			e.logf("the local repository already has the cipher pass of the key %s", target.ID())
			return nil
		}
		if legacyErr == nil {
			// An earlier run moved the old repository: the directory now is the new one.
			if err := os.RemoveAll(e.Paths.Abs(paths.BackupPG)); err != nil {
				return err
			}
		} else if err := os.Rename(e.Paths.Abs(paths.BackupPG), e.Paths.Abs(LegacyDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.MkdirAll(e.Paths.Abs(paths.BackupPG), 0o700); err != nil {
			return err
		}
		if err := os.Chown(e.Paths.Abs(paths.BackupPG), paths.PostgresUID, paths.PostgresGID); err != nil && !errors.Is(err, os.ErrPermission) {
			return err
		}
		return setPass(ctx, target.CipherPass())
	}); err != nil {
		return restart(err)
	}
	res.LocalRekeyed = true
	if err := e.Step("start-db: care-db.service", "start-db", func() error {
		if err := e.Systemd.ResetFailed(ctx, "care-db.service"); err != nil {
			return err
		}
		if err := e.systemctl(ctx, "start", "care-db.service"); err != nil {
			return err
		}
		return ops.WaitDB(ctx)
	}); err != nil {
		return restart(err)
	}
	if err := e.Step("ensure-stanza: pgbackrest stanza-create", "ensure-stanza", func() error {
		_, err := (backup.PG{Podman: e.Podman}).Exec(ctx, "stanza-create")
		return err
	}); err != nil {
		return restart(err)
	}
	if err := e.Step("start-app: care.target", "start-app", func() error { return e.StartTarget(ctx) }); err != nil {
		return restart(err)
	}
	up = true
	if err := e.Step("wait-healthy: care-api.service", "wait-healthy", func() error { return e.Healthy(ctx) }); err != nil {
		return restart(err)
	}
	if err := e.Maintenance(false); err != nil {
		return nil, err
	}
	var full *backup.LocalResult
	if err := e.Step("backup-full: the first backup of the new repository", "backup-full", func() error {
		var err error
		full, err = ops.LocalBackup(ctx, "full", "recovery-key-rotate "+e.Rec.ID())
		return err
	}); err != nil {
		return nil, err
	}
	res.FullBackupLabel = full.Label
	for _, d := range dests {
		if req.NewKey {
			if err := e.Step("init-destination: "+d.ID, "init-destination", func() error {
				return backup.EnsureRepository(ctx, e.Env, e.Config, d, target, req.Source)
			}); err != nil {
				return nil, err
			}
		}
		var sum *backup.BackupSummary
		if err := e.Step("snapshot: "+d.ID+" (db)", "snapshot", func() error {
			var err error
			sum, err = ops.Snapshot(ctx, d, backup.TagDB, target, false)
			return err
		}); err != nil {
			return nil, err
		}
		res.Destinations = append(res.Destinations, backup.RotateDest{ID: d.ID,
			Location: backup.Location(backup.Effective(d, e.Config.Backups), target.ID()), SnapshotID: sum.SnapshotID})
	}
	if req.NewKey {
		if err := e.Step("activate-key: "+target.ID(), "activate-key", ks.Activate); err != nil {
			return nil, err
		}
	}
	if req.RemoveOld {
		if err := e.Step("remove-old-repositories", "remove-old-repositories", func() error {
			if _, err := os.Stat(e.Paths.Abs(LegacyDir)); err == nil {
				if err := os.RemoveAll(e.Paths.Abs(LegacyDir)); err != nil {
					return err
				}
				res.RemovedRepositories = append(res.RemovedRepositories, LegacyDir)
			}
			if !req.NewKey {
				return nil
			}
			var errs []string
			for _, d := range dests {
				loc, err := ops.RemoveRepository(ctx, d, old)
				if err != nil {
					errs = append(errs, d.ID+": "+err.Error())
					continue
				}
				res.RemovedRepositories = append(res.RemovedRepositories, loc)
			}
			if len(errs) > 0 {
				return problem.New(problem.DestinationUnreachable, "cannot remove the old repository of "+strings.Join(errs, "; "))
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return res, nil
}
