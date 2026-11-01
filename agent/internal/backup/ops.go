package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
)

// Ops runs the backup work of the jobs and of the timers.
type Ops struct {
	Env
	Config *config.Config
	// Source is console, cli, or timer. JobID is "" for a timer snapshot.
	Source string
	JobID  string
	// Queued marks the job queued while it waits for a lock (nil: nothing).
	Queued func(bool)
	// Logf writes one line of the job log (nil: Out).
	Logf func(format string, a ...any)
	// LockWait bounds the wait for the lock of a target. DBWait bounds the wait for care-db.
	LockWait time.Duration
	DBWait   time.Duration
	Poll     time.Duration
}

func (o Ops) logf(format string, a ...any) {
	if o.Logf != nil {
		o.Logf(format, a...)
		return
	}
	_, _ = fmt.Fprintf(o.out(), format+"\n", a...)
}

func (o Ops) poll() time.Duration {
	if o.Poll > 0 {
		return o.Poll
	}
	return 2 * time.Second
}

// lock takes the lock of a target; the job is queued while it waits.
func (o Ops) lock(ctx context.Context, target string) (func(), error) {
	wait := o.LockWait
	if wait == 0 {
		wait = time.Hour
	}
	queued := false
	release, err := Lock(ctx, o.Paths, target, wait, func() {
		o.logf("another run of %s holds its lock; waiting", target)
		if o.Queued != nil {
			o.Queued(true)
			queued = true
		}
	})
	if queued {
		o.Queued(false)
	}
	return release, err
}

func (o Ops) pg() PG { return PG{Podman: o.Podman} }

// WaitDB waits until care-db accepts connections.
func (o Ops) WaitDB(ctx context.Context) error {
	wait := o.DBWait
	if wait == 0 {
		wait = 10 * time.Minute
	}
	deadline := time.Now().Add(wait)
	said := false
	for {
		_, err := o.Podman.Exec(ctx, DBContainer, "postgres", "pg_isready", "--quiet")
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("care-db does not accept connections after %s: %w", wait, err)
		}
		if !said {
			o.logf("wait until care-db accepts connections")
			said = true
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(o.poll()):
		}
	}
}

// Free returns the free and the total bytes of the file system of a path.
func Free(path string) (free, total int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), int64(st.Blocks) * int64(st.Bsize), nil
}

// FullSpaceNeeded returns the free space that a full backup needs: 1.2 times the last full backup.
func FullSpaceNeeded(ri *RepoInfo) int64 {
	if b := ri.LastOf("full"); b != nil {
		n := b.RepoSizeBytes
		return n + n/5
	}
	return 0
}

// LocalBackup runs a pgBackRest backup of the type (full or incr) under the local lock.
func (o Ops) LocalBackup(ctx context.Context, typ, reason string) (*LocalResult, error) {
	release, err := o.lock(ctx, LocalTarget)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := o.WaitDB(ctx); err != nil {
		return nil, err
	}
	pg := o.pg()
	if typ == "full" {
		if ri, err := pg.Info(ctx); err == nil {
			need := FullSpaceNeeded(ri)
			if free, _, err := Free(o.Paths.Abs(paths.BackupPG)); err == nil && need > 0 && free < need {
				return nil, problem.NeedSpace(paths.BackupPG, need, free, "A full backup needs free space of 1.2 times the last full backup.")
			}
		}
	}
	o.logf("pgbackrest backup --type=%s", typ)
	if _, err := pg.Exec(ctx, PGBackupArgs(typ, reason)...); err != nil {
		return nil, err
	}
	ri, err := pg.Info(ctx)
	if err != nil {
		return nil, err
	}
	last := ri.Last()
	if last == nil {
		return nil, errors.New("pgbackrest info shows no backup after the backup")
	}
	res := &LocalResult{Label: last.Label, Type: typ, UndoPoint: last.FinishedAt, RepoSizeBytes: last.RepoSizeBytes, Timeline: 1}
	if res.Type != "full" {
		res.Type = "incr"
	}
	if st, err := pg.State(ctx); err == nil && st.Timeline > 0 {
		res.Timeline = st.Timeline
	}
	_, _ = o.State().UpdateLocal(func(l *LocalState) {
		if typ == "full" {
			l.LastFullSize = last.RepoSizeBytes
			l.FullBackupNeeded = false
		}
		l.LastError, l.LastErrorAt = nil, nil
	})
	o.State().PruneCheckpoints(ri)
	o.logf("backup %s ends at %s", last.Label, last.FinishedAt.Format(time.RFC3339))
	return res, nil
}

// RecordScheduled records the result of a timer run of a local backup (banner local-backup-failed).
func (o Ops) RecordScheduled(err error) {
	now := o.now()
	_, _ = o.State().UpdateLocal(func(l *LocalState) {
		ok := err == nil
		l.LastScheduledOK, l.LastScheduledAt = &ok, &now
		if err != nil {
			msg := err.Error()
			l.LastError, l.LastErrorAt = &msg, &now
		}
	})
}

// Dest returns the destination of config.yaml with the id, or a not-found problem.
func (o Ops) Dest(id string) (config.BackupDestination, error) {
	for _, d := range o.Config.Backups.Destinations {
		if d.ID == id {
			return d, nil
		}
	}
	return config.BackupDestination{}, problem.Newf(problem.NotFound, "No destination of config.yaml has the id %s.", id)
}

// Snapshot makes one restic snapshot of the tag to a destination under its lock, then runs the
// retention of the tag and lists the snapshots. A timer run writes only the destination state.
func (o Ops) Snapshot(ctx context.Context, d config.BackupDestination, tag string, k Key, forget bool) (*BackupSummary, error) {
	release, err := o.lock(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	defer release()
	return o.snapshotLocked(ctx, d, tag, k, forget)
}

func (o Ops) run(id, kind string) func(bool, string, *int64, error) {
	r := DestinationRun{StartedAt: o.now(), Kind: kind, Source: o.Source}
	if o.JobID != "" {
		j := o.JobID
		r.JobID = &j
	}
	return o.State().AddRun(id, r)
}

func (o Ops) snapshotLocked(ctx context.Context, d config.BackupDestination, tag string, k Key, forget bool) (*BackupSummary, error) {
	finish := o.run(d.ID, tag)
	s, err := OpenDest(o.Env, o.Config, d, k)
	if err != nil {
		finish(false, "", nil, err)
		return nil, err
	}
	defer s.Close()
	sum, err := o.backup(ctx, s, tag)
	var rm *RepoMissingError
	if errors.As(err, &rm) {
		o.logf("%s: the repository does not exist; restic init", d.ID)
		if _, err = s.Run(ctx, o.Runner, "init"); err == nil {
			sum, err = o.backup(ctx, s, tag)
		}
	}
	if err != nil {
		finish(false, "", nil, err)
		return nil, err
	}
	added := sum.DataAddedPacked
	finish(true, sum.SnapshotID[:min(8, len(sum.SnapshotID))], &added, nil)
	_, _ = o.State().UpdateDest(d.ID, func(ds *DestState) {
		ds.KeyID, ds.Initialized, ds.Location = k.ID(), true, Location(Effective(d, o.Config.Backups), k.ID())
	})
	o.logf("%s: snapshot %s (%s), %d bytes added", d.ID, sum.SnapshotID[:min(8, len(sum.SnapshotID))], tag, sum.DataAddedPacked)
	if forget {
		o.unlockStale(ctx, s, d)
		if err := o.forgetLocked(ctx, s, d, tag); err != nil {
			o.logf("%s: forget: %v", d.ID, err)
		}
	}
	o.list(ctx, s, d)
	return sum, nil
}

func (o Ops) backup(ctx context.Context, s *Session, tag string) (*BackupSummary, error) {
	host := o.Config.Identity.Hostname
	if host == "" {
		host = "care"
	}
	var extra []string
	if _, err := os.Stat(o.Paths.Abs(paths.CaddyCA)); err == nil {
		extra = append(extra, paths.CaddyCA)
	}
	out, err := o.Runner.Run(ctx, s.Cmd(SnapshotArgs(tag, host, extra...)...))
	if err != nil {
		var xe *hostExit
		if errors.As(err, &xe) && xe.Code == 3 {
			// Some files were not readable; restic still made the snapshot.
			if sum, perr := ParseBackupSummary(out); perr == nil {
				o.logf("warning: restic could not read some files: %s", errorText(xe.Stderr))
				return sum, nil
			}
		}
		return nil, Classify(s.Repo.Dest.ID, err)
	}
	return ParseBackupSummary(out)
}

// Forget runs the retention of a tag on a destination under its lock.
func (o Ops) Forget(ctx context.Context, d config.BackupDestination, tag string, k Key) error {
	release, err := o.lock(ctx, d.ID)
	if err != nil {
		return err
	}
	defer release()
	s, err := OpenDest(o.Env, o.Config, d, k)
	if err != nil {
		return err
	}
	defer s.Close()
	o.unlockStale(ctx, s, d)
	return o.forgetLocked(ctx, s, d, tag)
}

// unlockStale removes the stale restic locks of a destination ("restic unlock" without --remove-all: a lock that
// is older than 30 minutes, or of a process of this host that ended). A run that lost its connection (an SFTP
// destination that went away) leaves its lock, and then each exclusive operation (forget, prune, check) fails
// until the lock is gone. care-pi runs one restic process for each destination at a time (o.lock), so a live lock
// of this device cannot be here. A failure is a warning only: the next command reports the real error.
func (o Ops) unlockStale(ctx context.Context, s *Session, d config.BackupDestination) {
	if _, err := s.Run(ctx, o.Runner, "unlock"); err != nil {
		o.logf("warning: cannot remove the stale locks of %s: %v", d.ID, err)
	}
}

func (o Ops) forgetLocked(ctx context.Context, s *Session, d config.BackupDestination, tag string) error {
	finish := o.run(d.ID, "forget")
	_, err := s.Run(ctx, o.Runner, ForgetArgs(tag, RetentionOf(d, o.Config.Backups))...)
	finish(err == nil, "", nil, err)
	return err
}

// list records the snapshot list and the free space of a destination.
func (o Ops) list(ctx context.Context, s *Session, d config.BackupDestination) {
	out, err := s.Run(ctx, o.Runner, "snapshots", "--json")
	var snaps []Snapshot
	if err == nil {
		snaps, err = ParseSnapshots(out)
	}
	o.State().SetSnapshots(d.ID, snaps, err, o.now())
	if d.Type == TypePOSIX {
		if free, total, err := Free(o.Paths.Abs(Base(Effective(d, o.Config.Backups)))); err == nil {
			_, _ = o.State().UpdateDest(d.ID, func(ds *DestState) { ds.FreeBytes, ds.TotalBytes = &free, &total })
		}
	}
}

// List lists the snapshots of a destination now (GET /restore/points?refresh=true).
func (o Ops) List(ctx context.Context, d config.BackupDestination, k Key) error {
	s, err := OpenDest(o.Env, o.Config, d, k)
	if err != nil {
		o.State().SetSnapshots(d.ID, nil, err, o.now())
		return err
	}
	defer s.Close()
	o.list(ctx, s, d)
	return nil
}

// VerifyLocal runs "pgbackrest verify" under the local lock and records the outcome.
func (o Ops) VerifyLocal(ctx context.Context) (*VerifyOutcome, error) {
	release, err := o.lock(ctx, LocalTarget)
	if err != nil {
		return nil, err
	}
	defer release()
	out, err := o.pg().Exec(ctx, "verify")
	vo := &VerifyOutcome{FinishedAt: o.now(), OK: err == nil}
	if o.JobID != "" {
		j := o.JobID
		vo.JobID = &j
	}
	if err != nil {
		msg := err.Error()
		vo.Detail = &msg
	}
	_ = out
	_, _ = o.State().UpdateLocal(func(l *LocalState) { l.LastVerify = vo })
	if err != nil {
		return vo, problem.New(problem.VerifyFailed, "pgbackrest verify of the local repository failed: "+err.Error())
	}
	return vo, nil
}

// VerifyDest runs forget and prune (with prune) and "restic check --read-data-subset" on a destination.
func (o Ops) VerifyDest(ctx context.Context, d config.BackupDestination, k Key, pct int, prune bool, step func(name, action string) func(error)) (*VerifyDest, error) {
	release, err := o.lock(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	defer release()
	s, err := OpenDest(o.Env, o.Config, d, k)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	res := &VerifyDest{ID: d.ID, SubsetPct: pct}
	start := time.Now()
	o.unlockStale(ctx, s, d)
	if prune {
		end := step("forget: "+d.ID, "forget")
		var ferr error
		for _, tag := range []string{TagDB, TagFiles} {
			if err := o.forgetLocked(ctx, s, d, tag); err != nil && ferr == nil {
				ferr = err
			}
		}
		end(ferr)
		if ferr != nil {
			return nil, ferr
		}
		end = step("prune: "+d.ID, "prune")
		before := o.rawSize(ctx, s)
		finish := o.run(d.ID, "prune")
		_, err := s.Run(ctx, o.Runner, "prune")
		finish(err == nil, "", nil, err)
		end(err)
		if err != nil {
			return nil, err
		}
		if after := o.rawSize(ctx, s); before != nil && after != nil {
			n := max(*before-*after, 0)
			res.PrunedBytes = &n
			_, _ = o.State().UpdateDest(d.ID, func(ds *DestState) { ds.SizeBytes = after })
		}
	}
	end := step("verify-destination: "+d.ID, "verify-destination")
	finish := o.run(d.ID, "check")
	out, err := o.Runner.Run(ctx, s.Cmd(CheckArgs(pct)...))
	vo := &VerifyOutcome{FinishedAt: o.now(), OK: err == nil}
	if o.JobID != "" {
		j := o.JobID
		vo.JobID = &j
	}
	if err != nil {
		cerr := Classify(d.ID, err)
		if problem.Is(cerr, problem.DestinationUnreachable) && looksDamaged(err, out) {
			cerr = problem.Newf(problem.VerifyFailed, "restic check of %s found damage: %s", d.ID, errorText(stderrOf(err)))
		}
		msg := cerr.Error()
		vo.Detail = &msg
		finish(false, "", nil, cerr)
		end(cerr)
		if problem.Is(cerr, problem.VerifyFailed) {
			_, _ = o.State().UpdateDest(d.ID, func(ds *DestState) { ds.LastVerify = vo })
		}
		return nil, cerr
	}
	finish(true, "", nil, nil)
	end(nil)
	_, _ = o.State().UpdateDest(d.ID, func(ds *DestState) { ds.LastVerify = vo })
	res.DurationS = time.Since(start).Seconds()
	o.list(ctx, s, d)
	return res, nil
}

func (o Ops) rawSize(ctx context.Context, s *Session) *int64 {
	out, err := s.Run(ctx, o.Runner, "stats", "--mode", "raw-data", "--json")
	if err != nil {
		return nil
	}
	n, err := RawSize(out)
	if err != nil {
		return nil
	}
	return &n
}

type hostExit = host.ExitError

func stderrOf(err error) string {
	var xe *hostExit
	if errors.As(err, &xe) {
		return xe.Stderr
	}
	return err.Error()
}

// looksDamaged reports whether a failed restic check found damage (not a connection problem).
func looksDamaged(err error, out []byte) bool {
	text := strings.ToLower(stderrOf(err) + string(out))
	for _, s := range []string{"error for tree", "pack ", "check failed", "errors were found", "invalid data", "hash mismatch", "ciphertext verification failed"} {
		if strings.Contains(text, s) {
			return true
		}
	}
	return false
}

// RemoveRepository deletes an old repository of a destination (the job recovery-key-rotate with
// remove_old_repositories). posix: the directory. s3: each object with the prefix. sftp: each
// snapshot and its data (restic forget and prune); the empty repository stays.
func (o Ops) RemoveRepository(ctx context.Context, d config.BackupDestination, old Key) (string, error) {
	ed := Effective(d, o.Config.Backups)
	loc := Location(ed, old.ID())
	switch ed.Type {
	case TypePOSIX:
		return loc, os.RemoveAll(o.Paths.Abs(loc))
	case TypeS3:
		cr := ReadCredentials(o.Paths, d.ID)
		if cr == nil {
			return loc, problem.Unreachable(d.ID, "no S3 keys")
		}
		cl, err := NewMinioS3(ed, cr)
		if err != nil {
			return loc, err
		}
		prefix := RepoDir(old.ID()) + "/"
		if ed.Prefix != "" {
			prefix = ed.Prefix + "/" + prefix
		}
		return loc, cl.RemovePrefix(ctx, ed.Bucket, prefix)
	}
	s, err := OpenDest(o.Env, o.Config, d, old)
	if err != nil {
		return loc, err
	}
	defer s.Close()
	o.unlockStale(ctx, s, d)
	if _, err := s.Run(ctx, o.Runner, "forget", "--keep-tag", "care-pi-keep-nothing"); err != nil {
		return loc, err
	}
	_, err = s.Run(ctx, o.Runner, "prune")
	return loc, err
}
