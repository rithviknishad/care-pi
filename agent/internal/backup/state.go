package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// Limits of the state files.
const (
	MaxRuns        = 10
	MaxDrills      = 12
	MaxCheckpoints = 20
	MaxSnapshots   = 500
)

// VerifyOutcome is one verify (schema VerifyOutcome).
type VerifyOutcome struct {
	JobID      *string   `json:"job_id"`
	FinishedAt time.Time `json:"finished_at"`
	OK         bool      `json:"ok"`
	Detail     *string   `json:"detail"`
}

// DestinationRun is one run of a destination (schema DestinationRun).
type DestinationRun struct {
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Kind       string     `json:"kind"`
	Source     string     `json:"source"`
	OK         *bool      `json:"ok"`
	SnapshotID *string    `json:"snapshot_id"`
	AddedBytes *int64     `json:"added_bytes"`
	ErrorCode  *string    `json:"error_code"`
	Error      *string    `json:"error"`
	JobID      *string    `json:"job_id"`
}

// SnapshotRecord is one snapshot of the last list of a destination.
type SnapshotRecord struct {
	ID       string    `json:"id"`
	Time     time.Time `json:"time"`
	Contents []string  `json:"contents"`
	Size     *int64    `json:"size_bytes"`
	Hostname string    `json:"hostname"`
	Tags     []string  `json:"tags"`
}

// DestState is the state file destinations/<id>.json.
type DestState struct {
	ID string `json:"id"`
	// KeyID is the key of the repository of this state. Initialized tells that it exists.
	KeyID       string `json:"key_id,omitempty"`
	Initialized bool   `json:"initialized,omitempty"`
	// Location is the repository of the state.
	Location            string           `json:"location,omitempty"`
	LastAttemptAt       *time.Time       `json:"last_attempt_at,omitempty"`
	LastSuccessAt       *time.Time       `json:"last_success_at,omitempty"`
	LastDBSnapshotAt    *time.Time       `json:"last_db_snapshot_at,omitempty"`
	LastFilesSnapshotAt *time.Time       `json:"last_files_snapshot_at,omitempty"`
	FailingSince        *time.Time       `json:"failing_since,omitempty"`
	LastError           *string          `json:"last_error,omitempty"`
	LastErrorCode       *string          `json:"last_error_code,omitempty"`
	SizeBytes           *int64           `json:"size_bytes,omitempty"`
	FreeBytes           *int64           `json:"free_bytes,omitempty"`
	TotalBytes          *int64           `json:"total_bytes,omitempty"`
	SnapshotCount       *int             `json:"snapshot_count,omitempty"`
	LastVerify          *VerifyOutcome   `json:"last_verify,omitempty"`
	Runs                []DestinationRun `json:"runs,omitempty"`
	Snapshots           []SnapshotRecord `json:"snapshots,omitempty"`
	ListedAt            *time.Time       `json:"listed_at,omitempty"`
	ListOK              *bool            `json:"list_ok,omitempty"`
	ListError           *string          `json:"list_error,omitempty"`
}

// LocalState is the state file local.json.
type LocalState struct {
	// LastScheduledOK is the result of the last full or incremental backup of a timer.
	LastScheduledOK  *bool          `json:"last_scheduled_ok,omitempty"`
	LastScheduledAt  *time.Time     `json:"last_scheduled_at,omitempty"`
	LastError        *string        `json:"last_error,omitempty"`
	LastErrorAt      *time.Time     `json:"last_error_at,omitempty"`
	LastVerify       *VerifyOutcome `json:"last_verify,omitempty"`
	WALFailingSince  *time.Time     `json:"wal_failing_since,omitempty"`
	LastFullSize     int64          `json:"last_full_size,omitempty"`
	FullBackupNeeded bool           `json:"full_backup_needed,omitempty"`
}

// DrillOutcome is one restore drill (schema DrillOutcome).
type DrillOutcome struct {
	JobID      *string   `json:"job_id"`
	FinishedAt time.Time `json:"finished_at"`
	Outcome    string    `json:"outcome"`
	DurationS  *float64  `json:"duration_s"`
	Detail     *string   `json:"detail"`
}

// Checkpoint is one checkpoint (schema Checkpoint).
type Checkpoint struct {
	Label     string    `json:"label"`
	UndoPoint time.Time `json:"undo_point"`
	Timeline  int       `json:"timeline"`
	JobID     *string   `json:"job_id"`
	JobKind   *string   `json:"job_kind"`
	Reason    *string   `json:"reason"`
}

// State reads and writes the state files below /var/lib/care-pi/state/backups. A file lock
// serialises the updates of the agent and of the CLI of the timers.
type State struct {
	Paths paths.Paths
	// Now is the clock (time.Now when nil).
	Now func() time.Time
}

func (s State) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC().Truncate(time.Second)
	}
	return time.Now().UTC().Truncate(time.Second)
}

func (s State) path(name string) string { return s.Paths.Abs(paths.BackupStateDir + "/" + name) }

func (s State) read(name string, v any) {
	if data, err := os.ReadFile(s.path(name)); err == nil {
		_ = json.Unmarshal(data, v)
	}
}

func (s State) write(name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	p := s.path(name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return lock.WriteFileAtomic(p, data, 0o600)
}

// update runs fn on the decoded file under the state lock and writes the result.
func update[T any](s State, name string, fn func(*T)) (*T, error) {
	release, err := s.flock()
	if err != nil {
		return nil, err
	}
	defer release()
	var v T
	s.read(name, &v)
	fn(&v)
	return &v, s.write(name, &v)
}

func (s State) flock() (func(), error) {
	if err := os.MkdirAll(s.Paths.Abs(paths.RunDir), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(s.Paths.Abs(paths.RunDir+"/backup-state.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

func destFile(id string) string { return "destinations/" + id + ".json" }

// Dest returns the state of a destination.
func (s State) Dest(id string) *DestState {
	var d DestState
	s.read(destFile(id), &d)
	d.ID = id
	return &d
}

// UpdateDest changes the state of a destination.
func (s State) UpdateDest(id string, fn func(*DestState)) (*DestState, error) {
	return update(s, destFile(id), func(d *DestState) {
		d.ID = id
		fn(d)
	})
}

// RemoveDest deletes the state of a destination that config.yaml does not have.
func (s State) RemoveDest(id string) { _ = os.Remove(s.path(destFile(id))) }

// DestIDs returns the ids of the destination state files.
func (s State) DestIDs() []string {
	ents, err := os.ReadDir(s.path("destinations"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if n := e.Name(); filepath.Ext(n) == ".json" {
			out = append(out, n[:len(n)-5])
		}
	}
	sort.Strings(out)
	return out
}

// Local returns local.json.
func (s State) Local() *LocalState {
	var l LocalState
	s.read("local.json", &l)
	return &l
}

// UpdateLocal changes local.json.
func (s State) UpdateLocal(fn func(*LocalState)) (*LocalState, error) {
	return update(s, "local.json", fn)
}

type drillFile struct {
	Runs []DrillOutcome `json:"runs"`
}

// Drills returns the last drills, newest first.
func (s State) Drills() []DrillOutcome {
	var f drillFile
	s.read("drill.json", &f)
	return f.Runs
}

// AddDrill records a drill.
func (s State) AddDrill(o DrillOutcome) error {
	_, err := update(s, "drill.json", func(f *drillFile) {
		f.Runs = append([]DrillOutcome{o}, f.Runs...)
		if len(f.Runs) > MaxDrills {
			f.Runs = f.Runs[:MaxDrills]
		}
	})
	return err
}

type checkpointFile struct {
	Items []Checkpoint `json:"items"`
}

// Checkpoints returns the recorded checkpoints, newest first.
func (s State) Checkpoints() []Checkpoint {
	var f checkpointFile
	s.read("checkpoints.json", &f)
	return f.Items
}

// AddCheckpoint records a checkpoint. keep removes the checkpoints whose backup expired.
func (s State) AddCheckpoint(c Checkpoint) error {
	_, err := update(s, "checkpoints.json", func(f *checkpointFile) {
		f.Items = append([]Checkpoint{c}, f.Items...)
		if len(f.Items) > MaxCheckpoints {
			f.Items = f.Items[:MaxCheckpoints]
		}
	})
	return err
}

// PruneCheckpoints keeps only the checkpoints whose label is still in the repository.
func (s State) PruneCheckpoints(ri *RepoInfo) {
	if ri == nil {
		return
	}
	_, _ = update(s, "checkpoints.json", func(f *checkpointFile) {
		var keep []Checkpoint
		for _, c := range f.Items {
			if ri.Find(c.Label) != nil {
				keep = append(keep, c)
			}
		}
		f.Items = keep
	})
}

// AddRun records the start of a run of a destination and returns its finish function.
func (s State) AddRun(id string, run DestinationRun) func(ok bool, snapshotID string, added *int64, err error) {
	_, _ = s.UpdateDest(id, func(d *DestState) {
		t := run.StartedAt
		d.LastAttemptAt = &t
		d.Runs = append([]DestinationRun{run}, d.Runs...)
		if len(d.Runs) > MaxRuns {
			d.Runs = d.Runs[:MaxRuns]
		}
	})
	return func(ok bool, snapshotID string, added *int64, runErr error) {
		now := s.now()
		_, _ = s.UpdateDest(id, func(d *DestState) {
			for i := range d.Runs {
				r := &d.Runs[i]
				if !r.StartedAt.Equal(run.StartedAt) || r.Kind != run.Kind || r.FinishedAt != nil {
					continue
				}
				r.FinishedAt = &now
				r.OK = &ok
				if snapshotID != "" {
					r.SnapshotID = &snapshotID
				}
				r.AddedBytes = added
				if runErr != nil {
					code, msg := ErrorCode(runErr), runErr.Error()
					r.ErrorCode, r.Error = &code, &msg
				}
				break
			}
			if ok {
				d.FailingSince, d.LastError, d.LastErrorCode = nil, nil, nil
				if run.Kind == TagDB || run.Kind == TagFiles {
					d.LastSuccessAt = &now
				}
				switch run.Kind {
				case TagDB:
					d.LastDBSnapshotAt = &now
				case TagFiles:
					d.LastFilesSnapshotAt = &now
				}
				return
			}
			if runErr != nil && !errors.Is(runErr, context.Canceled) {
				code, msg := ErrorCode(runErr), runErr.Error()
				d.LastError, d.LastErrorCode = &msg, &code
				if d.FailingSince == nil {
					t := run.StartedAt
					d.FailingSince = &t
				}
			}
		})
	}
}

// SetSnapshots records the result of a snapshot list.
func (s State) SetSnapshots(id string, list []Snapshot, listErr error, now time.Time) {
	_, _ = s.UpdateDest(id, func(d *DestState) {
		t := now.UTC().Truncate(time.Second)
		d.ListedAt = &t
		ok := listErr == nil
		d.ListOK = &ok
		if listErr != nil {
			msg := listErr.Error()
			d.ListError = &msg
			return
		}
		d.ListError = nil
		n := len(list)
		d.SnapshotCount = &n
		d.Snapshots = d.Snapshots[:0]
		for i, sn := range list {
			if i >= MaxSnapshots {
				break
			}
			d.Snapshots = append(d.Snapshots, SnapshotRecord{ID: sn.ShortID, Time: sn.Time.UTC(), Contents: sn.Contents(),
				Size: sn.Size(), Hostname: sn.Hostname, Tags: sn.Tags})
		}
		for _, sn := range list {
			t := sn.Time.UTC().Truncate(time.Second)
			if sn.HasTag(TagDB) && (d.LastDBSnapshotAt == nil || t.After(*d.LastDBSnapshotAt)) {
				d.LastDBSnapshotAt = &t
			}
			if sn.HasTag(TagFiles) && (d.LastFilesSnapshotAt == nil || t.After(*d.LastFilesSnapshotAt)) {
				d.LastFilesSnapshotAt = &t
			}
		}
	})
}

// Running reports whether a lock file of the target is held now.
func Running(p paths.Paths, target string) bool {
	f, err := os.OpenFile(LockPath(p, target), os.O_RDWR, 0o600)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

// LocalTarget is the lock target of the local repository.
const LocalTarget = "local"

// LockPath returns the lock of a target: /run/care-pi/backup-<id>.lock.
func LockPath(p paths.Paths, target string) string {
	return p.Abs(fmt.Sprintf("%s/backup-%s.lock", paths.RunDir, target))
}

// ErrLockTimeout tells that another run holds the lock of the target.
var ErrLockTimeout = errors.New("another backup run of the same target holds its lock")

// Lock takes the lock of a target. It waits up to wait (0: no wait). waiting runs one time when
// the lock is not free at once (the job state queued).
func Lock(ctx context.Context, p paths.Paths, target string, wait time.Duration, waiting func()) (func(), error) {
	if err := os.MkdirAll(p.Abs(paths.RunDir), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(LockPath(p, target), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	said := false
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, err
		}
		if !time.Now().Before(deadline) {
			_ = f.Close()
			return nil, ErrLockTimeout
		}
		if !said && waiting != nil {
			waiting()
			said = true
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
