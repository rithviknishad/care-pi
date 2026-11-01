package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/rithviknishad/care-pi/agent/internal/backup"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/disks"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/render"
	"github.com/rithviknishad/care-pi/agent/internal/secrets"
)

// LocalView is the local repository (schema LocalBackupState).
type LocalView struct {
	LastFullAt        *time.Time            `json:"last_full_at"`
	LastIncrAt        *time.Time            `json:"last_incr_at"`
	LastWALArchivedAt *time.Time            `json:"last_wal_archived_at"`
	WindowFrom        *time.Time            `json:"window_from"`
	WindowTo          *time.Time            `json:"window_to"`
	SizeBytes         *int64                `json:"size_bytes"`
	State             string                `json:"state"`
	RepoPath          string                `json:"repo_path"`
	Key               string                `json:"key"`
	Timeline          *int                  `json:"timeline"`
	BackupCount       int                   `json:"backup_count"`
	LastBackup        *backup.LocalBackup   `json:"last_backup"`
	LastError         *string               `json:"last_error"`
	WALFailingSince   *time.Time            `json:"wal_archive_failing_since"`
	FreeBytes         *int64                `json:"free_bytes"`
	NextFullAt        *time.Time            `json:"next_full_at"`
	NextIncrAt        *time.Time            `json:"next_incr_at"`
	LastVerify        *backup.VerifyOutcome `json:"last_verify"`
}

// DestView is one destination (schema DestinationState).
type DestView struct {
	ID                  string                   `json:"id"`
	Type                string                   `json:"type"`
	State               string                   `json:"state"`
	LastSnapshotAt      *time.Time               `json:"last_snapshot_at"`
	SizeBytes           *int64                   `json:"size_bytes"`
	FreeBytes           *int64                   `json:"free_bytes"`
	LastError           *string                  `json:"last_error"`
	Location            string                   `json:"location"`
	Destination         config.BackupDestination `json:"destination"`
	Credentials         string                   `json:"credentials"`
	HostKeyFingerprint  *string                  `json:"host_key_fingerprint"`
	LastDBSnapshotAt    *time.Time               `json:"last_db_snapshot_at"`
	LastFilesSnapshotAt *time.Time               `json:"last_files_snapshot_at"`
	LastAttemptAt       *time.Time               `json:"last_attempt_at"`
	LastErrorCode       *string                  `json:"last_error_code"`
	FailingSince        *time.Time               `json:"failing_since"`
	NextDBRunAt         *time.Time               `json:"next_db_run_at"`
	NextFilesRunAt      *time.Time               `json:"next_files_run_at"`
	RPOMinutes          int                      `json:"rpo_minutes"`
	SnapshotCount       *int                     `json:"snapshot_count"`
	LastVerify          *backup.VerifyOutcome    `json:"last_verify"`
	Running             bool                     `json:"running"`
	RecentRuns          []backup.DestinationRun  `json:"recent_runs"`
}

// ScheduleEntry is one backup timer (schema ScheduleEntry).
type ScheduleEntry struct {
	ID            string     `json:"id"`
	Kind          string     `json:"kind"`
	DestinationID *string    `json:"destination_id"`
	Unit          string     `json:"unit"`
	Calendar      *string    `json:"calendar"`
	IntervalMin   *int       `json:"interval_min"`
	Enabled       bool       `json:"enabled"`
	NextRunAt     *time.Time `json:"next_run_at"`
	LastRunAt     *time.Time `json:"last_run_at"`
	LastOK        *bool      `json:"last_ok"`
}

// SSHPublicKey is the SSH key of the agent (schema SshPublicKey).
type SSHPublicKey struct {
	Key                string `json:"key"`
	KeyID              string `json:"key_id"`
	Type               string `json:"type"`
	PublicKey          string `json:"public_key"`
	AuthorizedKeysLine string `json:"authorized_keys_line"`
	FingerprintSHA256  string `json:"fingerprint_sha256"`
}

// BackupsOverview is GET /backups (schema BackupsOverview).
type BackupsOverview struct {
	Local                LocalView        `json:"local"`
	Destinations         []DestView       `json:"destinations"`
	RecoveryKeyConfirmed bool             `json:"recovery_key_confirmed"`
	GeneratedAt          time.Time        `json:"generated_at"`
	Configured           bool             `json:"configured"`
	Paused               bool             `json:"paused"`
	Health               backup.Health    `json:"health"`
	RecoveryKey          backup.KeyStatus `json:"recovery_key"`
	Schedule             []ScheduleEntry  `json:"schedule"`
	Retention            RetentionView    `json:"retention"`
	RPO                  RPOView          `json:"rpo"`
	Verify               VerifyView       `json:"verify"`
	Drill                DrillView        `json:"drill"`
	SSHKey               *SSHPublicKey    `json:"ssh_key"`
	RunningJob           *jobs.Summary    `json:"running_job"`
	health               backup.HealthInput
}

// RetentionView is BackupsOverview.retention.
type RetentionView struct {
	DBFull          int `json:"db_full"`
	FilesKeepDaily  int `json:"files_keep_daily"`
	FilesKeepWeekly int `json:"files_keep_weekly"`
}

// RPOView is BackupsOverview.rpo.
type RPOView struct {
	WALArchiveTimeoutMin int  `json:"wal_archive_timeout_min"`
	LocalMinutes         int  `json:"local_minutes"`
	OffsiteMinutes       *int `json:"offsite_minutes"`
}

// VerifyView is schema VerifyState.
type VerifyView struct {
	Schedule  string                `json:"schedule"`
	NextRunAt *time.Time            `json:"next_run_at"`
	Last      *backup.VerifyOutcome `json:"last"`
}

// DrillView is schema DrillState.
type DrillView struct {
	Enabled   bool                 `json:"enabled"`
	Schedule  string               `json:"schedule"`
	NextRunAt *time.Time           `json:"next_run_at"`
	Last      *backup.DrillOutcome `json:"last"`
}

// localSnap is the cached state of the local repository (two podman exec calls).
type localSnap struct {
	info *backup.RepoInfo
	pg   *backup.PGState
}

// backupEnv returns the environment of the backup operations.
func (s *Service) backupEnv(out io.Writer) backup.Env {
	return backup.Env{Paths: s.Paths, Runner: s.Host.Runner, Systemd: s.Host.Systemd, Podman: s.Host.Podman, Now: s.Now, Out: out}
}

func (s *Service) keys() *backup.KeyStore { return &backup.KeyStore{Paths: s.Paths, Now: s.Now} }

// InvalidateBackups drops the cached local state (after a backup or a restore).
func (s *Service) InvalidateBackups() {
	s.mu.Lock()
	s.localCache = cached[*localSnap]{}
	s.backupsCache = cached[*BackupsOverview]{}
	s.statusCache = cached[*Status]{}
	s.mu.Unlock()
}

func (s *Service) local(ctx context.Context) *localSnap {
	s.mu.Lock()
	if v, ok := s.localCache.get(s.now(), 30*time.Second); ok {
		s.mu.Unlock()
		return v
	}
	s.mu.Unlock()
	pg := backup.PG{Podman: s.Host.Podman}
	snap := &localSnap{}
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if info, err := pg.Info(c); err == nil {
		snap.info = info
	}
	if st, err := pg.State(c); err == nil {
		snap.pg = st
	}
	s.mu.Lock()
	s.localCache.set(s.now(), snap)
	s.mu.Unlock()
	return snap
}

// LocalRepository returns derived, legacy, or none (docs/api/README.md section 13.12).
func (s *Service) LocalRepository() string {
	repo := s.Paths.Abs(paths.BackupPG)
	_, e1 := os.Stat(repo + "/archive/" + backup.Stanza + "/archive.info")
	_, e2 := os.Stat(repo + "/backup/" + backup.Stanza + "/backup.info")
	if e1 != nil && e2 != nil {
		return backup.RepoNone
	}
	pass, err := secrets.Store{Paths: s.Paths}.Read(secrets.PGBackRestCipher)
	if err != nil {
		return backup.RepoLegacy
	}
	for _, which := range []string{backup.Active, backup.Pending} {
		if k, _, err := s.keys().Get(which); err == nil && k.CipherPass() == pass {
			return backup.RepoDerived
		}
	}
	return backup.RepoLegacy
}

// RecoveryKeyStatus returns the state of the recovery key (schema RecoveryKeyStatus).
func (s *Service) RecoveryKeyStatus() backup.KeyStatus {
	st := s.keys().Status()
	st.LocalRepository = s.LocalRepository()
	return st
}

type timerInfo struct {
	next, last *time.Time
}

// timers reads the next and the last run of each backup timer, and the result of its service.
func (s *Service) timers(ctx context.Context) map[string]timerInfo {
	out := map[string]timerInfo{}
	raw, err := s.Host.Runner.Run(ctx, host.Cmd{Name: "systemctl", Args: []string{"list-timers", "--all", "--output=json", "care-backup-*"}})
	if err == nil {
		var items []struct {
			Next any    `json:"next"`
			Last any    `json:"last"`
			Unit string `json:"unit"`
		}
		if json.Unmarshal(raw, &items) == nil {
			for _, it := range items {
				out[it.Unit] = timerInfo{next: usec(it.Next), last: usec(it.Last)}
			}
		}
	}
	return out
}

func usec(v any) *time.Time {
	var n int64
	switch x := v.(type) {
	case float64:
		n = int64(x)
	case string:
		n, _ = strconv.ParseInt(x, 10, 64)
	}
	if n <= 0 {
		return nil
	}
	t := time.UnixMicro(n).UTC().Truncate(time.Second)
	return &t
}

// SSHKeyOf returns the SSH key of a recovery key.
func (s *Service) SSHKeyOf(which string, k backup.Key, hostname string) *SSHPublicKey {
	line, fp := k.SSHPublic(backup.SSHComment(hostname, k.ID()))
	return &SSHPublicKey{Key: which, KeyID: k.ID(), Type: ssh.KeyAlgoED25519, PublicKey: line,
		AuthorizedKeysLine: "restrict " + line, FingerprintSHA256: fp}
}

// Backups returns GET /backups. It reads only local data and keeps the report for 2 s.
func (s *Service) Backups(ctx context.Context) (*BackupsOverview, error) {
	s.mu.Lock()
	if v, ok := s.backupsCache.get(s.now(), 2*time.Second); ok {
		s.mu.Unlock()
		return v, nil
	}
	s.mu.Unlock()
	cfg, err := config.Load(s.configPath())
	if err != nil {
		cfg = config.Default()
	}
	now := s.now()
	b := cfg.Backups
	st := backup.State{Paths: s.Paths, Now: s.Now}
	key := s.RecoveryKeyStatus()
	ls := s.local(ctx)
	localState := st.Local()
	// The agent records the start of a WAL archive failure that it sees.
	if failing := backup.WALFailing(ls.pg); failing != (localState.WALFailingSince != nil) {
		localState, _ = st.UpdateLocal(func(l *backup.LocalState) {
			if failing {
				t := now
				if ls.pg.LastFailedAt != nil {
					t = *ls.pg.LastFailedAt
				}
				l.WALFailingSince = &t
			} else {
				l.WALFailingSince = nil
			}
		})
	}
	timers := s.timers(ctx)
	ov := &BackupsOverview{GeneratedAt: now.Truncate(time.Second), Configured: len(b.Destinations) > 0, Paused: b.Paused,
		RecoveryKey: key, RecoveryKeyConfirmed: key.State == backup.KeyConfirmed, Destinations: []DestView{}, Schedule: []ScheduleEntry{},
		Retention: RetentionView{DBFull: b.Retention.DBFull, FilesKeepDaily: b.Retention.FilesKeepDaily, FilesKeepWeekly: b.Retention.FilesKeepWeekly},
		RPO:       RPOView{WALArchiveTimeoutMin: b.WALArchiveTimeoutMin, LocalMinutes: max(b.WALArchiveTimeoutMin, 1)}}
	ov.Local = s.localView(ls, localState, key.LocalRepository, timers, b)
	hin := backup.HealthInput{Now: now, Config: b, Key: key, Local: localState, Info: ls.info, PG: ls.pg}
	if ov.Local.FreeBytes != nil {
		hin.LocalFree = ov.Local.FreeBytes
	}
	if d := st.Drills(); len(d) > 0 {
		hin.Drill = &d[0]
	}
	var k *backup.Key
	if kk, _, err := s.keys().Get(backup.Active); err == nil {
		k = &kk
		ov.SSHKey = s.SSHKeyOf(backup.Active, kk, cfg.Identity.Hostname)
	}
	for _, d := range b.Destinations {
		ed := backup.Effective(d, b)
		ds := st.Dest(d.ID)
		in := backup.DestInput{Dest: ed, State: ds, Credentials: backup.CredentialsState(s.Paths, d)}
		hin.Dests = append(hin.Dests, in)
		ov.Destinations = append(ov.Destinations, s.destView(hin, in, k, timers, b))
		if rpo := ed.IntervalMin + b.WALArchiveTimeoutMin; ov.RPO.OffsiteMinutes == nil || rpo < *ov.RPO.OffsiteMinutes {
			ov.RPO.OffsiteMinutes = &rpo
		}
	}
	ov.health = hin
	ov.Health = backup.ComputeHealth(hin)
	ov.Schedule = s.schedule(cfg, timers)
	ov.Verify = VerifyView{Schedule: b.Schedule.Verify, NextRunAt: timers[render.TimerVerify].next, Last: lastVerify(localState, hin.Dests)}
	ov.Drill = DrillView{Enabled: b.DrillEnabled, Schedule: b.Schedule.Drill, Last: hin.Drill}
	if b.DrillEnabled {
		ov.Drill.NextRunAt = timers[render.TimerDrill].next
	}
	backupKinds := map[string]bool{jobs.KindBackupRun: true, jobs.KindBackupVerify: true, jobs.KindRestoreDrill: true,
		jobs.KindRestorePITR: true, jobs.KindRestoreFiles: true, jobs.KindRestoreDR: true, jobs.KindDiskPrepare: true, jobs.KindRecoveryKeyRotate: true}
	if id := jobs.RunningOf(s.Paths, backupKinds); id != "" {
		if j, err := jobs.Load(s.Paths, id); err == nil {
			sum := j.Summary()
			ov.RunningJob = &sum
		}
	}
	s.mu.Lock()
	s.backupsCache.set(s.now(), ov)
	s.mu.Unlock()
	return ov, nil
}

func lastVerify(l *backup.LocalState, dests []backup.DestInput) *backup.VerifyOutcome {
	var last *backup.VerifyOutcome
	pick := func(v *backup.VerifyOutcome) {
		if v != nil && (last == nil || v.FinishedAt.After(last.FinishedAt) || (!v.OK && last.OK)) {
			last = v
		}
	}
	pick(l.LastVerify)
	for _, d := range dests {
		if d.State != nil {
			pick(d.State.LastVerify)
		}
	}
	return last
}

func (s *Service) localView(ls *localSnap, l *backup.LocalState, key string, timers map[string]timerInfo, b config.Backups) LocalView {
	v := LocalView{RepoPath: paths.BackupPG, Key: key, State: "none", LastError: l.LastError, LastVerify: l.LastVerify,
		WALFailingSince: l.WALFailingSince}
	if !b.Paused {
		v.NextFullAt, v.NextIncrAt = timers[render.TimerDBFull].next, timers[render.TimerDBIncr].next
	}
	if free, _, err := backup.Free(s.Paths.Abs(paths.BackupPG)); err == nil {
		v.FreeBytes = &free
	}
	if ri := ls.info; ri != nil {
		v.BackupCount = len(ri.Backups)
		v.LastBackup = ri.Last()
		if f := ri.LastOf("full"); f != nil {
			t := f.FinishedAt
			v.LastFullAt = &t
		}
		if i := ri.LastOf("incr"); i != nil {
			t := i.FinishedAt
			v.LastIncrAt = &t
		}
		v.WindowFrom = ri.WindowFrom()
		var size int64
		for _, bk := range ri.Backups {
			size += bk.RepoSizeBytes
		}
		if len(ri.Backups) > 0 {
			v.SizeBytes = &size
		}
	}
	if pg := ls.pg; pg != nil {
		v.LastWALArchivedAt = pg.LastArchivedAt
		if pg.Timeline > 0 {
			tl := pg.Timeline
			v.Timeline = &tl
		}
	}
	if v.WindowFrom != nil {
		v.WindowTo = v.LastWALArchivedAt
		if v.WindowTo == nil || (v.LastBackup != nil && v.LastBackup.FinishedAt.After(*v.WindowTo)) {
			t := v.LastBackup.FinishedAt
			v.WindowTo = &t
		}
	}
	switch {
	case ls.info == nil && ls.pg == nil && key != backup.RepoNone:
		v.State = "unknown"
	case backup.WALFailing(ls.pg):
		v.State = "error"
	case l.LastScheduledOK != nil && !*l.LastScheduledOK:
		v.State = "degraded"
	case v.BackupCount > 0:
		v.State = "ok"
	}
	return v
}

func (s *Service) destView(hin backup.HealthInput, in backup.DestInput, k *backup.Key, timers map[string]timerInfo, b config.Backups) DestView {
	d, ds := in.Dest, in.State
	state, code := backup.DestStatus(hin, in)
	v := DestView{ID: d.ID, Type: d.Type, State: state, Destination: d, Credentials: in.Credentials,
		SizeBytes: ds.SizeBytes, FreeBytes: ds.FreeBytes, LastError: ds.LastError, LastErrorCode: ds.LastErrorCode,
		LastDBSnapshotAt: ds.LastDBSnapshotAt, LastFilesSnapshotAt: ds.LastFilesSnapshotAt, LastAttemptAt: ds.LastAttemptAt,
		FailingSince: ds.FailingSince, SnapshotCount: ds.SnapshotCount, LastVerify: ds.LastVerify,
		RPOMinutes: d.IntervalMin + b.WALArchiveTimeoutMin, Running: backup.Running(s.Paths, d.ID), RecentRuns: ds.Runs}
	if v.RecentRuns == nil {
		v.RecentRuns = []backup.DestinationRun{}
	}
	if code != nil && v.LastErrorCode == nil {
		v.LastErrorCode = code
	}
	v.LastSnapshotAt = ds.LastDBSnapshotAt
	if ds.LastFilesSnapshotAt != nil && (v.LastSnapshotAt == nil || ds.LastFilesSnapshotAt.After(*v.LastSnapshotAt)) {
		v.LastSnapshotAt = ds.LastFilesSnapshotAt
	}
	if k != nil {
		v.Location = backup.Location(d, k.ID())
	} else {
		v.Location = backup.Base(d)
	}
	if d.HostKey != "" {
		if pk, err := backup.ParseHostKey(d.HostKey); err == nil {
			fp := ssh.FingerprintSHA256(pk)
			v.HostKeyFingerprint = &fp
		}
	}
	if !b.Paused {
		v.NextDBRunAt = timers[render.DestTimer("db", d.ID)].next
		v.NextFilesRunAt = timers[render.DestTimer("files", d.ID)].next
	}
	return v
}

func (s *Service) schedule(cfg *config.Config, timers map[string]timerInfo) []ScheduleEntry {
	b := cfg.Backups
	st := backup.State{Paths: s.Paths}
	local := st.Local()
	var out []ScheduleEntry
	str := func(v string) *string { return &v }
	add := func(id, kind string, dest *string, unit string, cal *string, interval *int, enabled bool, lastOK *bool) {
		t := timers[unit]
		e := ScheduleEntry{ID: id, Kind: kind, DestinationID: dest, Unit: unit, Calendar: cal, IntervalMin: interval,
			Enabled: enabled, LastRunAt: t.last, LastOK: lastOK}
		if enabled {
			e.NextRunAt = t.next
		}
		out = append(out, e)
	}
	on := !b.Paused
	add("db-full", "db-full", nil, render.TimerDBFull, str(b.Schedule.DBFull), nil, on, local.LastScheduledOK)
	add("db-incr", "db-incr", nil, render.TimerDBIncr, str(b.Schedule.DBIncr), nil, on, local.LastScheduledOK)
	var verifyOK *bool
	if local.LastVerify != nil {
		ok := local.LastVerify.OK
		verifyOK = &ok
	}
	add("verify", "verify", nil, render.TimerVerify, str(b.Schedule.Verify), nil, on, verifyOK)
	var drillOK *bool
	if d := st.Drills(); len(d) > 0 {
		ok := d[0].Outcome != "failed"
		drillOK = &ok
	}
	add("drill", "drill", nil, render.TimerDrill, str(b.Schedule.Drill), nil, on && b.DrillEnabled, drillOK)
	for _, d := range b.Destinations {
		id := d.ID
		ed := backup.Effective(d, b)
		ds := st.Dest(id)
		interval := ed.IntervalMin
		add("db:"+id, "snapshot-db", &id, render.DestTimer("db", id), nil, &interval, on, lastRunOK(ds, backup.TagDB))
		add("files:"+id, "snapshot-files", &id, render.DestTimer("files", id), str(b.Schedule.Files), nil, on, lastRunOK(ds, backup.TagFiles))
	}
	return out
}

func lastRunOK(ds *backup.DestState, kind string) *bool {
	for _, r := range ds.Runs {
		if r.Kind == kind && r.OK != nil {
			ok := *r.OK
			return &ok
		}
	}
	return nil
}

// BackupsHealth returns GET /backups/health.
func (s *Service) BackupsHealth(ctx context.Context) (backup.Health, error) {
	ov, err := s.Backups(ctx)
	if err != nil {
		return backup.Health{}, err
	}
	return ov.Health, nil
}

// Destinations returns GET /backups/destinations.
func (s *Service) Destinations(ctx context.Context) ([]DestView, error) {
	ov, err := s.Backups(ctx)
	if err != nil {
		return nil, err
	}
	return ov.Destinations, nil
}

// DestinationSecretsState is the response of PUT .../secrets (schema DestinationSecretsState).
type DestinationSecretsState struct {
	ID        string     `json:"id"`
	Type      string     `json:"type"`
	Present   bool       `json:"present"`
	KeyIDHint *string    `json:"access_key_id_hint"`
	UpdatedAt *time.Time `json:"updated_at"`
}

func (s *Service) fileDest(id string) (*config.BackupDestination, error) {
	doc, _, err := s.readConfigDoc()
	if err != nil {
		return nil, err
	}
	data, err := marshalConfig(doc)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Parse(data, "config.yaml")
	if err != nil {
		return nil, configProblem(err)
	}
	for _, d := range cfg.Backups.Destinations {
		if d.ID == id {
			dd := d
			return &dd, nil
		}
	}
	return nil, problem.Newf(problem.NotFound, "No destination of config.yaml has the id %s.", id)
}

// SetDestinationSecrets stores the S3 keys of a destination.
func (s *Service) SetDestinationSecrets(id string, c backup.Credentials) (*DestinationSecretsState, error) {
	d, err := s.fileDest(id)
	if err != nil {
		return nil, err
	}
	if d.Type != backup.TypeS3 {
		return nil, problem.Invalid("", "only an s3 destination has a secret; "+d.Type+" uses none", "no-secrets-for-type")
	}
	now := s.now().Truncate(time.Second)
	c.UpdatedAt = now
	if err := backup.WriteCredentials(s.Paths, id, c); err != nil {
		return nil, err
	}
	s.InvalidateBackups()
	return &DestinationSecretsState{ID: id, Type: backup.TypeS3, Present: true, KeyIDHint: hint(c.AccessKeyID), UpdatedAt: &now}, nil
}

func hint(id string) *string {
	if len(id) <= 8 {
		h := "…"
		return &h
	}
	h := id[:4] + "…" + id[len(id)-4:]
	return &h
}

// SSHKey returns GET /backups/ssh-key.
func (s *Service) SSHKey(which string) (*SSHPublicKey, error) {
	k, err := s.keyOf(which)
	if err != nil {
		return nil, err
	}
	cfg, _ := config.Load(s.configPath())
	host := "care"
	if cfg != nil {
		host = cfg.Identity.Hostname
	}
	return s.SSHKeyOf(which, k, host), nil
}

func (s *Service) keyOf(which string) (backup.Key, error) {
	k, _, err := s.keys().Get(which)
	if errors.Is(err, backup.ErrNoKey) {
		if which == backup.Pending {
			if !s.keys().Exists(backup.Active) {
				return k, problem.New(problem.NoRecoveryKey, "No recovery key exists. Make it with POST /backups/recovery-key.")
			}
			return k, problem.New(problem.NotFound, "No pending recovery key exists.")
		}
		return k, problem.New(problem.NoRecoveryKey, "No recovery key exists. Make it with POST /backups/recovery-key.")
	}
	return k, err
}

// BackupDiskList is GET /backups/disks (schema BackupDiskList).
type BackupDiskList struct {
	Items       []disks.Disk `json:"items"`
	GeneratedAt time.Time    `json:"generated_at"`
}

// Disks lists the block devices of the host.
func (s *Service) Disks(ctx context.Context) (*BackupDiskList, error) {
	list, err := disks.List(ctx, s.Host.Runner, s.Paths)
	if err != nil {
		return nil, err
	}
	cfg, _ := config.Load(s.configPath())
	if cfg != nil {
		for i := range list {
			if list[i].CareLabel == nil {
				continue
			}
			for _, d := range cfg.Backups.Destinations {
				if d.DiskLabel == *list[i].CareLabel {
					id := d.ID
					list[i].DestinationID = &id
				}
			}
		}
	}
	return &BackupDiskList{Items: list, GeneratedAt: s.now().Truncate(time.Second)}, nil
}

// RecoveryKeyView is schema RecoveryKey.
type RecoveryKeyView struct {
	Words     []string  `json:"words"`
	CreatedAt time.Time `json:"created_at"`
	Key       string    `json:"key"`
	KeyID     string    `json:"key_id"`
	QRPayload string    `json:"qr_payload"`
	FileName  string    `json:"file_name"`
}

func (s *Service) displayName() string {
	if cfg, err := config.Load(s.configPath()); err == nil {
		return cfg.Identity.DisplayName
	}
	return ""
}

func (s *Service) keyView(which string, k backup.Key, m *backup.KeyMeta) *RecoveryKeyView {
	return &RecoveryKeyView{Words: k.Words(), CreatedAt: m.CreatedAt, Key: which, KeyID: k.ID(), QRPayload: k.QRPayload(),
		FileName: backup.FileName(s.displayName())}
}

// CreateRecoveryKey makes a key: the active key without one, else the pending key of a rotation.
// The first key of a host with an empty local repository also writes the derived cipher pass.
func (s *Service) CreateRecoveryKey(ctx context.Context) (*RecoveryKeyView, backup.KeyStatus, error) {
	if id := jobs.RunningLockJob(s.Paths); id != "" {
		return nil, backup.KeyStatus{}, problem.Running(id)
	}
	k, which, m, err := s.keys().Create()
	if err != nil {
		return nil, backup.KeyStatus{}, err
	}
	s.log().Info("backups: a recovery key was made", "key", which, "key_id", k.ID())
	if which == backup.Active && s.LocalRepository() == backup.RepoNone {
		if err := s.writeCipherPass(k.CipherPass()); err != nil {
			return nil, backup.KeyStatus{}, err
		}
	}
	s.InvalidateBackups()
	return s.keyView(which, k, m), s.RecoveryKeyStatus(), nil
}

// writeCipherPass replaces the cipher pass file. The next apply sets the podman secret.
func (s *Service) writeCipherPass(pass string) error {
	return lock.WriteFileAtomic(secrets.Store{Paths: s.Paths}.Path(secrets.PGBackRestCipher), []byte(pass+"\n"), 0o600)
}

// ShowRecoveryKey returns the words of a key. The journal records the show, without the words.
func (s *Service) ShowRecoveryKey(which string) (*RecoveryKeyView, error) {
	if _, err := s.keyOf(which); err != nil {
		return nil, err
	}
	k, m, err := s.keys().Get(which)
	if err != nil {
		return nil, err
	}
	s.log().Info("backups: the recovery key was shown", "key", which, "key_id", k.ID())
	return s.keyView(which, k, m), nil
}

// RecoveryKeyText returns the text file of a key and its name.
func (s *Service) RecoveryKeyText(which string) (string, string, error) {
	if _, err := s.keyOf(which); err != nil {
		return "", "", err
	}
	k, m, err := s.keys().Get(which)
	if err != nil {
		return "", "", err
	}
	name := s.displayName()
	s.log().Info("backups: the recovery key file was downloaded", "key", which, "key_id", k.ID())
	return backup.Text(k, name, m.CreatedAt), backup.FileName(name), nil
}

// ConfirmRecoveryKey checks the 3 words of a key.
func (s *Service) ConfirmRecoveryKey(which string, words []backup.WordInput, pointer string) (backup.KeyStatus, error) {
	if _, err := s.keyOf(which); err != nil {
		return backup.KeyStatus{}, err
	}
	_, err := s.keys().Confirm(which, words)
	var me *backup.MismatchError
	var pe *backup.PositionsError
	switch {
	case errors.As(err, &me):
		p := problem.New(problem.RecoveryKeyMismatch, fmt.Sprintf("%d word(s) are not the words of the recovery key.", len(me.Wrong)))
		for _, i := range me.Wrong {
			p.Errors = append(p.Errors, problem.FieldError{Pointer: fmt.Sprintf("%s/%d/word", pointer, i),
				Message: fmt.Sprintf("not the word at position %d", words[i].Index), Code: "wrong-word"})
		}
		return backup.KeyStatus{}, p
	case errors.As(err, &pe):
		return backup.KeyStatus{}, problem.Invalid(pointer, fmt.Sprintf("type the words at the positions %v", pe.Want), "positions")
	case err != nil:
		return backup.KeyStatus{}, err
	}
	s.InvalidateBackups()
	return s.RecoveryKeyStatus(), nil
}

// ---- destination test ----

// TestRequest is POST /backups/destinations/test.
type TestRequest struct {
	Destination       config.BackupDestination
	Credentials       *backup.Credentials
	AcceptFingerprint string
	CreateBucket      bool
}

// Tester returns the destination tester. Tests replace SSH and S3.
func (s *Service) tester() *backup.Tester {
	if s.BackupTester != nil {
		t := *s.BackupTester
		return &t
	}
	return &backup.Tester{Paths: s.Paths, Runner: s.Host.Runner, Systemd: s.Host.Systemd, Now: s.Now, SSH: backup.NetSSH{}}
}

// TestDestination tests a candidate destination (docs/api/README.md section 13.5).
func (s *Service) TestDestination(ctx context.Context, req TestRequest) (*backup.TestResult, error) {
	cfg, err := config.Load(s.configPath())
	if err != nil {
		cfg = config.Default()
	}
	if err := checkDest(req.Destination, "/destination"); err != nil {
		return nil, err
	}
	d := backup.Effective(req.Destination, cfg.Backups)
	tr := backup.TestRequest{Dest: d, Creds: req.Credentials, AcceptFingerprint: req.AcceptFingerprint,
		CreateBucket: req.CreateBucket, Hostname: cfg.Identity.Hostname}
	if tr.Creds == nil && d.ID != "" {
		tr.Creds = backup.ReadCredentials(s.Paths, d.ID)
	}
	if k, _, err := s.keys().Get(backup.Active); err == nil {
		tr.Key = &k
	}
	c, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return s.tester().Test(c, tr)
}

// CheckDestination validates one destination with the JSON Schema of config.yaml (invalid-request
// with pointers below pointer).
func CheckDestination(d config.BackupDestination, pointer string) error { return checkDest(d, pointer) }

// checkDest validates one destination with the JSON Schema of config.yaml.
func checkDest(d config.BackupDestination, pointer string) error {
	data, err := json.Marshal(map[string]any{"version": 1, "backups": map[string]any{"destinations": []any{d}}})
	if err != nil {
		return err
	}
	if _, err := config.Parse(data, "destination"); err != nil {
		p := configProblem(err)
		p.Slug, p.Title = problem.InvalidRequest, problem.New(problem.InvalidRequest, "").Title
		for i, e := range p.Errors {
			p.Errors[i].Pointer = strings.Replace(e.Pointer, "/backups/destinations/0", pointer, 1)
		}
		return p
	}
	return nil
}
