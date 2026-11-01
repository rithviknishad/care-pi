package restore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/rithviknishad/care-pi/agent/internal/backup"
	"github.com/rithviknishad/care-pi/agent/internal/ca"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/secrets"
)

// DRRequest is one restore of a new device from one destination.
type DRRequest struct {
	Key        backup.Key
	Dest       config.BackupDestination
	Creds      *backup.Credentials
	Time       *time.Time
	SnapshotID string
	Files      bool
	Pause      bool
	Source     string
}

// DRDeps are the steps of the service that the job calls.
type DRDeps struct {
	// Apply runs a full apply with the apply lock held. noStart: the restore apply (render, write,
	// pull; no start of the database or of Care).
	Apply func(ctx context.Context, noStart bool) error
	// WritePaused writes backups.paused: true into config.yaml.
	WritePaused    func() error
	SetHostname    func(ctx context.Context, hostname string) error
	RestoreAccount func(agentJSON []byte) error
	Finish         func() error
	URLs           func(cfg *config.Config) (care, console string, urls []string)
}

// DRResult is the result of a restore-dr job (schema RestoreDrResult).
type DRResult struct {
	Kind              string     `json:"kind"`
	KeyID             string     `json:"key_id"`
	Location          string     `json:"location"`
	DBSnapshotID      string     `json:"db_snapshot_id"`
	DBSnapshotTime    time.Time  `json:"db_snapshot_time"`
	RestoredTo        time.Time  `json:"restored_to"`
	Timeline          int        `json:"timeline"`
	FilesSnapshotID   *string    `json:"files_snapshot_id"`
	FilesSnapshotTime *time.Time `json:"files_snapshot_time"`
	SkewS             *int64     `json:"skew_s"`
	DisplayName       string     `json:"display_name"`
	Hostname          string     `json:"hostname"`
	CareURL           string     `json:"care_url"`
	ConsoleURL        string     `json:"console_url"`
	URLs              []string   `json:"urls"`
	BackupsPaused     bool       `json:"backups_paused"`
	ConsoleAccount    string     `json:"console_account"`
}

// Candidate returns the destination of a restore request with an id for the SSH config.
func Candidate(d config.BackupDestination) config.BackupDestination {
	d.ID = ""
	return d
}

// OpenCandidate opens the repository of the key at a destination that config.yaml does not have yet.
func OpenCandidate(e backup.Env, d config.BackupDestination, creds *backup.Credentials, k backup.Key) (*backup.Session, error) {
	r := backup.Repo{Dest: backup.Effective(d, config.Default().Backups), Key: k, S3: creds}
	return backup.Open(e.Paths, r)
}

// Pick returns the newest snapshot of the tag (or the snapshot with the id), or nil.
func Pick(list []backup.Snapshot, tag, id string) *backup.Snapshot {
	for i := range list {
		s := list[i]
		if id != "" && (s.ID == id || s.ShortID == id || strings.HasPrefix(s.ID, id)) {
			return &list[i]
		}
		if id == "" && s.HasTag(tag) {
			return &list[i]
		}
	}
	return nil
}

// Snapshots lists the snapshots of a session.
func Snapshots(ctx context.Context, r host.Runner, s *backup.Session) ([]backup.Snapshot, error) {
	out, err := s.Run(ctx, r, "snapshots", "--json")
	if err != nil {
		var rm *backup.RepoMissingError
		if errors.As(err, &rm) {
			return nil, problem.New(problem.RecoveryKeyMismatch, "The destination has no repository "+backup.RepoDir(s.Repo.KeyID())+": the recovery key does not match a backup there.")
		}
		return nil, err
	}
	return backup.ParseSnapshots(out)
}

// Dump returns one file of a snapshot.
func Dump(ctx context.Context, r host.Runner, s *backup.Session, snap, file string) ([]byte, error) {
	return s.Run(ctx, r, "dump", snap, file)
}

// RepoBackups reads the backups of the pgBackRest repository in a db snapshot: the backup.info and
// the cipher pass of the snapshot (no container).
func RepoBackups(ctx context.Context, r host.Runner, s *backup.Session, snap string) ([]backup.LocalBackup, error) {
	pass, err := Dump(ctx, r, s, snap, paths.Secret(secrets.PGBackRestCipher))
	if err != nil {
		return nil, err
	}
	enc, err := Dump(ctx, r, s, snap, paths.BackupPG+"/backup/"+backup.Stanza+"/backup.info")
	if err != nil {
		return nil, err
	}
	plain, err := backup.DecryptInfo(enc, strings.TrimSpace(string(pass)))
	if err != nil {
		return nil, err
	}
	return backup.ParseBackupInfo(plain), nil
}

// DR runs the steps of restore-dr (docs/api/README.md section 13.9.3).
func DR(ctx context.Context, e Env, req DRRequest, deps DRDeps) (*DRResult, error) {
	res := &DRResult{Kind: jobs.KindRestoreDR, KeyID: req.Key.ID(), ConsoleAccount: "restored", URLs: []string{}}
	var s *backup.Session
	var list []backup.Snapshot
	if err := e.Step("check-key: "+backup.RepoDir(req.Key.ID()), "check-key", func() error {
		var err error
		s, err = OpenCandidate(e.Env, req.Dest, req.Creds, req.Key)
		if err != nil {
			return err
		}
		list, err = Snapshots(ctx, e.Runner, s)
		return err
	}); err != nil {
		return nil, err
	}
	defer s.Close()
	res.Location = backup.Location(s.Repo.Dest, req.Key.ID())
	db := Pick(list, backup.TagDB, req.SnapshotID)
	if db == nil || !db.HasTag(backup.TagDB) {
		return nil, problem.New(problem.NotFound, "The destination has no db snapshot for this recovery key.")
	}
	files := Pick(list, backup.TagFiles, "")
	res.DBSnapshotID, res.DBSnapshotTime = db.ShortID, db.Time.UTC().Truncate(time.Second)
	if err := e.Step("check-space: "+paths.DataDir, "check-space", func() error {
		need := int64(1 << 30)
		if sz := db.Size(); sz != nil {
			need += *sz
		}
		if files != nil && req.Files {
			if sz := files.Size(); sz != nil {
				need += *sz
			}
		}
		if bk, err := RepoBackups(ctx, e.Runner, s, db.ID); err == nil && len(bk) > 0 {
			need += bk[len(bk)-1].SizeBytes
		}
		free, _, err := backup.Free(e.Paths.Abs(paths.DataDir))
		if err != nil {
			return err
		}
		if free < need {
			return problem.NeedSpace(paths.DataDir, need, free, "The restore needs the repository, the files, and the database.")
		}
		return nil
	}); err != nil {
		return nil, err
	}
	stage := e.Paths.Abs(paths.RestoreDir + "/" + e.Rec.ID())
	newest := list[0]
	var account []byte
	if err := e.Step("fetch-config: /etc/care-pi of "+newest.ShortID, "fetch-config", func() error {
		var err error
		account, err = fetchConfig(ctx, e, s, newest.ID, stage, req.Key)
		if err != nil {
			return err
		}
		return fetchCA(ctx, e, s, newest)
	}); err != nil {
		return nil, err
	}
	if req.Pause {
		if err := deps.WritePaused(); err != nil {
			return nil, err
		}
	}
	if err := deps.Apply(ctx, true); err != nil {
		return nil, err
	}
	cfg, err := config.Load(e.Paths.Abs(paths.ConfigFile))
	if err != nil {
		return nil, err
	}
	e.Config = cfg
	if img, err := lockImage(e.Paths); err == nil {
		e.Image = img
	} else {
		return nil, err
	}
	if err := e.Step("fetch-db-repo: "+db.ShortID+" to "+paths.BackupPG, "fetch-db-repo", func() error {
		if err := emptyDir(e.Paths.Abs(paths.BackupPG)); err != nil {
			return err
		}
		_, err := s.Run(ctx, e.Runner, backup.RestoreArgs(db.ID, paths.BackupPG, paths.BackupPG, nil, false)...)
		return err
	}); err != nil {
		return nil, err
	}
	target := "the end of the WAL"
	if req.Time != nil {
		target = req.Time.UTC().Format(time.RFC3339)
	}
	if err := e.Step("restore-db: "+target, "restore-db", func() error { return e.RunRestore(ctx, req.Time, "") }); err != nil {
		return nil, err
	}
	if req.Files && files != nil {
		if err := e.Step("fetch-files: "+files.ShortID+" to "+paths.S3Data, "fetch-files", func() error {
			_, err := s.Run(ctx, e.Runner, backup.RestoreArgs(files.ID, paths.S3Data, paths.S3Data, nil, false)...)
			return err
		}); err != nil {
			return nil, err
		}
		id, t := files.ShortID, files.Time.UTC().Truncate(time.Second)
		res.FilesSnapshotID, res.FilesSnapshotTime = &id, &t
	}
	if err := e.Step("start-target: care.target", "start-target", func() error {
		if err := e.StartTarget(ctx); err != nil {
			return err
		}
		_, err := e.Podman.Exec(ctx, "care-caddy", "", "caddy", "reload", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
		if err != nil {
			return e.systemctl(ctx, "restart", "care-caddy.service")
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := e.Step("wait-promote: the end of the recovery", "wait-promote", func() error { return e.WaitPromote(ctx) }); err != nil {
		return nil, err
	}
	if err := e.Step("wait-healthy: care-api.service", "wait-healthy", func() error { return e.Healthy(ctx) }); err != nil {
		return nil, err
	}
	pg := backup.PG{Podman: e.Podman}
	res.Timeline = 1
	if st, err := pg.State(ctx); err == nil && st.Timeline > 0 {
		res.Timeline = st.Timeline
	}
	res.RestoredTo = db.Time.UTC().Truncate(time.Second)
	if req.Time != nil {
		res.RestoredTo = req.Time.UTC()
	}
	if res.FilesSnapshotTime != nil {
		skew := int64(res.RestoredTo.Sub(*res.FilesSnapshotTime).Seconds())
		res.SkewS = &skew
	}
	ops := e.Ops(req.Source)
	if err := e.Step("backup-full: the first backup of the new timeline", "backup-full", func() error {
		_, err := ops.LocalBackup(ctx, "full", "after restore-dr "+e.Rec.ID())
		return err
	}); err != nil {
		return nil, err
	}
	// The full apply: ensure-repositories, ensure-timers (not with pause_backups), and the FE build.
	if err := deps.Apply(ctx, false); err != nil {
		return nil, err
	}
	res.DisplayName, res.Hostname = cfg.Identity.DisplayName, cfg.Identity.Hostname
	res.BackupsPaused = cfg.Backups.Paused || req.Pause
	if err := e.Step("set-hostname: "+cfg.Identity.Hostname, "set-hostname", func() error { return deps.SetHostname(ctx, cfg.Identity.Hostname) }); err != nil {
		return nil, err
	}
	if err := e.Step("restore-account: the console account of the old device", "restore-account", func() error {
		if len(account) == 0 {
			return errors.New("the config snapshot has no console account")
		}
		return deps.RestoreAccount(account)
	}); err != nil {
		return nil, err
	}
	if err := e.Step("finish: the setup state is ready", "finish", deps.Finish); err != nil {
		return nil, err
	}
	_ = os.RemoveAll(stage)
	res.CareURL, res.ConsoleURL, res.URLs = deps.URLs(cfg)
	return res, nil
}

// fetchConfig restores /etc/care-pi of a snapshot into the stage, and copies config.yaml, lock.yaml,
// and the secrets (not the console account and not the recovery key) into /etc/care-pi. It writes
// the recovery key of the request as the confirmed active key. It returns the console account of
// the old device (secrets/agent.json) for the step restore-account.
func fetchConfig(ctx context.Context, e Env, s *backup.Session, snap, stage string, k backup.Key) ([]byte, error) {
	if err := os.RemoveAll(stage); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return nil, err
	}
	hostStage := e.Paths.Host(stage)
	if _, err := s.Run(ctx, e.Runner, backup.RestoreArgs(snap, paths.EtcDir, hostStage, nil, false)...); err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(stage, "config.yaml")); err != nil {
		return nil, fmt.Errorf("the snapshot %s has no config.yaml: %w", snap, err)
	}
	copyFile := func(rel string, mode os.FileMode) error {
		data, err := os.ReadFile(filepath.Join(stage, rel))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		dst := e.Paths.Abs(paths.EtcDir + "/" + rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		return lock.WriteFileAtomic(dst, data, mode)
	}
	for _, f := range []string{"config.yaml", "lock.yaml"} {
		if err := copyFile(f, 0o600); err != nil {
			return nil, err
		}
	}
	var account []byte
	err := filepath.WalkDir(filepath.Join(stage, "secrets"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(stage, p)
		switch filepath.Base(p) {
		case "agent.json":
			account, err = os.ReadFile(p)
			return err
		case "recovery.key", "recovery.key.pending", "claim_code":
			return nil
		}
		return copyFile(rel, 0o600)
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err := e.Keys().SetActive(k); err != nil {
		return nil, err
	}
	e.logf("restored config.yaml, lock.yaml, and %s", paths.SecretsDir)
	return account, nil
}

// CAStage returns the directory that holds the local CA of Caddy of a restore-dr job until SwitchCA.
func CAStage(jobID string) string { return paths.RestoreDir + "/" + jobID + "-caddy-ca" }

// AgentCAStage returns the directory that holds the root of the agent (Phase 6) of a restore-dr job until SwitchCA.
func AgentCAStage(jobID string) string { return paths.RestoreDir + "/" + jobID + "-agent-ca" }

// fetchCA restores the root of the agent (Phase 6, docs/connectivity.md) and the local CA of Caddy of the snapshot
// into their stages. SwitchCA uses them after the event done of the job: a restart of Caddy before it would change
// the certificate of the open event stream. A snapshot of an older agent has no root of the agent; a snapshot of
// Phase 1 or 2 has no CA. The device then keeps its new CA.
func fetchCA(ctx context.Context, e Env, s *backup.Session, snap backup.Snapshot) error {
	restoreTo := func(sub, stage string) error {
		dir := e.Paths.Abs(stage)
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		_, err := s.Run(ctx, e.Runner, backup.RestoreArgs(snap.ID, sub, e.Paths.Host(dir), nil, false)...)
		return err
	}
	switch {
	case snap.HasPath(paths.CADir):
		if err := restoreTo(paths.CADir, AgentCAStage(e.Rec.ID())); err != nil {
			return err
		}
		e.logf("restored the root CA of the old device; the agent uses it after the end of the job")
	case snap.HasPath(paths.CaddyCA):
		if err := restoreTo(paths.CaddyCA, CAStage(e.Rec.ID())); err != nil {
			return err
		}
		e.logf("restored the local CA of Caddy; the agent uses it after the end of the job")
	default:
		e.logf("the snapshot %s has no local CA; the clinic devices must trust the new root CA", snap.ShortID)
	}
	return nil
}

// SwitchCA puts back the CA of the old device after a restore from backup, and restarts care-caddy. Caddy then
// issues new certificates from the restored CA, and the clinic devices that trust the old root need no change.
// With the root of the agent in the stage (a snapshot of Phase 6), it adopts that root. With only the local CA of
// Caddy (an older snapshot), it replaces the CA of Caddy, but only on a device without a root of the agent: the
// root of the agent wins over the root of Caddy (the Caddyfile names it), so the switch of 07 §4.1 happens anyway.
// Without a stage it does nothing.
func SwitchCA(ctx context.Context, p paths.Paths, r host.Runner, jobID string) (bool, error) {
	agentSrc := p.Abs(AgentCAStage(jobID))
	if _, err := os.Stat(filepath.Join(agentSrc, ca.CertFile)); err == nil {
		if _, err := ca.Adopt(p, agentSrc, time.Now()); err != nil {
			return false, err
		}
		_ = os.RemoveAll(agentSrc)
		if _, err := r.Run(ctx, host.Cmd{Name: "systemctl", Args: []string{"restart", "care-caddy.service"}}); err != nil {
			return false, err
		}
		return true, nil
	}
	src := p.Abs(CAStage(jobID))
	if ca.Exists(p) {
		_ = os.RemoveAll(src)
		return false, nil
	}
	for _, f := range []string{"root.crt", "root.key", "intermediate.crt", "intermediate.key"} {
		if _, err := os.Stat(filepath.Join(src, f)); err != nil {
			if errors.Is(err, fs.ErrNotExist) && f == "root.crt" {
				return false, nil
			}
			return false, fmt.Errorf("the restored CA is not complete: %w", err)
		}
	}
	dst := p.Abs(paths.CaddyCA)
	old := dst + ".setup"
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return false, err
	}
	_ = os.RemoveAll(old)
	if err := os.Rename(dst, old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if err := os.Rename(src, dst); err != nil {
		_ = os.Rename(old, dst)
		return false, err
	}
	if err := os.RemoveAll(p.Abs(paths.CaddyLocalCerts)); err != nil {
		return false, err
	}
	if _, err := r.Run(ctx, host.Cmd{Name: "systemctl", Args: []string{"restart", "care-caddy.service"}}); err != nil {
		return false, err
	}
	_ = os.RemoveAll(old)
	return true, nil
}

func emptyDir(dir string) error {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return os.MkdirAll(dir, 0o700)
	}
	if err != nil {
		return err
	}
	for _, en := range ents {
		if err := os.RemoveAll(filepath.Join(dir, en.Name())); err != nil {
			return err
		}
	}
	return nil
}

// lockImage returns the postgres image of lock.yaml.
func lockImage(p paths.Paths) (string, error) {
	lk, err := lock.Read(p.Abs(paths.LockFile))
	if err != nil {
		return "", err
	}
	if lk == nil {
		return "", errors.New("no lock.yaml")
	}
	img, ok := lk.Images["postgres"]
	if !ok {
		return "", errors.New("lock.yaml has no postgres image")
	}
	return img.Pull(), nil
}

// LockImage returns the postgres image of lock.yaml (podman run of a restore or a drill).
func LockImage(p paths.Paths) (string, error) { return lockImage(p) }

// SourceInfo reads config.yaml and lock.yaml of a snapshot (DrInspection.source).
func SourceInfo(ctx context.Context, r host.Runner, s *backup.Session, snap string) (*config.Config, *lock.Lock, error) {
	data, err := Dump(ctx, r, s, snap, paths.ConfigFile)
	if err != nil {
		return nil, nil, err
	}
	cfg, err := config.Parse(data, "config.yaml of the snapshot")
	if err != nil {
		return nil, nil, err
	}
	var lk lock.Lock
	if raw, err := Dump(ctx, r, s, snap, paths.LockFile); err == nil {
		_ = yaml.Unmarshal(raw, &lk)
	}
	return cfg, &lk, nil
}
