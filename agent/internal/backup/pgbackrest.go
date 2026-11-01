package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/host"
)

// DBContainer is the container that runs PostgreSQL and pgBackRest.
const DBContainer = "care-db"

// ReasonAnnotation is the pgBackRest annotation key of the reason of a backup.
const ReasonAnnotation = "care-pi-reason"

// LocalBackup is one backup of the local repository (schema LocalBackup).
type LocalBackup struct {
	Label         string    `json:"label"`
	Type          string    `json:"type"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	SizeBytes     int64     `json:"size_bytes"`
	RepoSizeBytes int64     `json:"repo_size_bytes"`
	Reason        *string   `json:"reason"`
}

// RepoInfo is the result of "pgbackrest info --output=json" for the stanza.
type RepoInfo struct {
	// Backups are oldest first.
	Backups []LocalBackup
	// ArchiveMax is the newest archived WAL segment ("" without an archive).
	ArchiveMax string
	ArchiveMin string
	// StatusCode is 0 (ok), 1 (missing stanza path), 2 (no valid backups), and so on.
	StatusCode    int
	StatusMessage string
	Cipher        string
}

type infoJSON struct {
	Name    string `json:"name"`
	Cipher  string `json:"cipher"`
	Archive []struct {
		Min string `json:"min"`
		Max string `json:"max"`
	} `json:"archive"`
	Backup []struct {
		Label      string            `json:"label"`
		Type       string            `json:"type"`
		Annotation map[string]string `json:"annotation"`
		Timestamp  struct {
			Start int64 `json:"start"`
			Stop  int64 `json:"stop"`
		} `json:"timestamp"`
		Info struct {
			Size       int64 `json:"size"`
			Repository struct {
				Delta int64 `json:"delta"`
				Size  int64 `json:"size"`
			} `json:"repository"`
		} `json:"info"`
	} `json:"backup"`
	Status struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"status"`
}

// ParseInfo reads "pgbackrest info --output=json".
func ParseInfo(b []byte, stanza string) (*RepoInfo, error) {
	var all []infoJSON
	if err := json.Unmarshal(bytes.TrimSpace(b), &all); err != nil {
		return nil, fmt.Errorf("cannot read pgbackrest info: %w", err)
	}
	for _, s := range all {
		if s.Name != stanza {
			continue
		}
		ri := &RepoInfo{StatusCode: s.Status.Code, StatusMessage: s.Status.Message, Cipher: s.Cipher}
		for _, a := range s.Archive {
			if a.Max > ri.ArchiveMax {
				ri.ArchiveMax = a.Max
			}
			if ri.ArchiveMin == "" || (a.Min != "" && a.Min < ri.ArchiveMin) {
				ri.ArchiveMin = a.Min
			}
		}
		for _, x := range s.Backup {
			lb := LocalBackup{Label: x.Label, Type: x.Type, StartedAt: time.Unix(x.Timestamp.Start, 0).UTC(),
				FinishedAt: time.Unix(x.Timestamp.Stop, 0).UTC(), SizeBytes: x.Info.Size, RepoSizeBytes: x.Info.Repository.Delta}
			if r := x.Annotation[ReasonAnnotation]; r != "" {
				lb.Reason = &r
			}
			ri.Backups = append(ri.Backups, lb)
		}
		sort.Slice(ri.Backups, func(i, j int) bool { return ri.Backups[i].FinishedAt.Before(ri.Backups[j].FinishedAt) })
		return ri, nil
	}
	return &RepoInfo{StatusCode: 1, StatusMessage: "missing stanza"}, nil
}

// Last returns the newest backup, or nil.
func (ri *RepoInfo) Last() *LocalBackup {
	if ri == nil || len(ri.Backups) == 0 {
		return nil
	}
	b := ri.Backups[len(ri.Backups)-1]
	return &b
}

// LastOf returns the newest backup of the type, or nil.
func (ri *RepoInfo) LastOf(t string) *LocalBackup {
	if ri == nil {
		return nil
	}
	for i := len(ri.Backups) - 1; i >= 0; i-- {
		if ri.Backups[i].Type == t {
			b := ri.Backups[i]
			return &b
		}
	}
	return nil
}

// WindowFrom returns the end of the oldest full backup: the earliest point in time of a PITR.
func (ri *RepoInfo) WindowFrom() *time.Time {
	if ri == nil {
		return nil
	}
	for _, b := range ri.Backups {
		if b.Type == "full" {
			t := b.FinishedAt
			return &t
		}
	}
	return nil
}

// Find returns the backup with the label, or nil.
func (ri *RepoInfo) Find(label string) *LocalBackup {
	if ri == nil {
		return nil
	}
	for _, b := range ri.Backups {
		if b.Label == label {
			bb := b
			return &bb
		}
	}
	return nil
}

// PG runs pgBackRest and psql in care-db (podman exec as the user postgres).
type PG struct {
	Podman host.Podman
}

// Stanza is the pgBackRest stanza (render.StanzaName).
const Stanza = "main"

// Exec runs pgbackrest with the stanza.
func (pg PG) Exec(ctx context.Context, args ...string) ([]byte, error) {
	a := append([]string{"pgbackrest", "--stanza=" + Stanza}, args...)
	out, err := pg.Podman.Exec(ctx, DBContainer, "postgres", a...)
	if err != nil {
		return out, fmt.Errorf("pgbackrest %s: %w", strings.Join(args, " "), detail(err, out))
	}
	return out, nil
}

func detail(err error, out []byte) error {
	var xe *host.ExitError
	if errors.As(err, &xe) {
		msg := strings.TrimSpace(xe.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(string(out))
		}
		lines := strings.Split(msg, "\n")
		if len(lines) > 4 {
			lines = lines[len(lines)-4:]
		}
		return fmt.Errorf("exit code %d: %s", xe.Code, strings.Join(lines, " "))
	}
	return err
}

// Info runs "pgbackrest info --output=json".
func (pg PG) Info(ctx context.Context) (*RepoInfo, error) {
	out, err := pg.Exec(ctx, "info", "--output=json")
	if err != nil {
		return nil, err
	}
	return ParseInfo(out, Stanza)
}

// PGBackupArgs returns the arguments of a pgBackRest backup of the type with a reason annotation.
// The backup ends with pg_backup_stop, which switches the WAL segment; archive-check waits until
// the segment is in the repository (the checkpoint of docs/api/README.md section 13.11).
func PGBackupArgs(typ, reason string) []string {
	a := []string{"backup", "--type=" + typ, "--archive-check", "--archive-timeout=120"}
	if reason != "" {
		a = append(a, "--annotation="+ReasonAnnotation+"="+cleanReason(reason))
	}
	return a
}

func cleanReason(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == '=' {
			return ' '
		}
		return r
	}, s)
	if len(s) > 200 {
		s = s[:200]
	}
	return strings.TrimSpace(s)
}

// PGState is the state of the archiver and the cluster from psql.
type PGState struct {
	LastArchivedAt *time.Time
	LastFailedAt   *time.Time
	Timeline       int
	DatabaseBytes  int64
	InRecovery     bool
}

// StateQuery is the psql query of PGState: one line with | between the values.
const StateQuery = "select coalesce(extract(epoch from last_archived_time)::bigint::text,''), " +
	"coalesce(extract(epoch from last_failed_time)::bigint::text,''), " +
	"(select timeline_id from pg_control_checkpoint())::text, " +
	"pg_database_size(current_database())::text, pg_is_in_recovery()::text from pg_stat_archiver"

// ParseState reads the line of StateQuery.
func ParseState(out []byte) (*PGState, error) {
	f := strings.Split(strings.TrimSpace(string(out)), "|")
	if len(f) != 5 {
		return nil, fmt.Errorf("unexpected psql output %q", strings.TrimSpace(string(out)))
	}
	st := &PGState{InRecovery: f[4] == "true" || f[4] == "t"}
	epoch := func(s string) *time.Time {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || s == "" {
			return nil
		}
		t := time.Unix(n, 0).UTC()
		return &t
	}
	st.LastArchivedAt, st.LastFailedAt = epoch(f[0]), epoch(f[1])
	st.Timeline, _ = strconv.Atoi(f[2])
	st.DatabaseBytes, _ = strconv.ParseInt(f[3], 10, 64)
	return st, nil
}

// Psql runs one query in care-db and returns the unaligned output.
func (pg PG) Psql(ctx context.Context, query string) ([]byte, error) {
	out, err := pg.Podman.Exec(ctx, DBContainer, "postgres", "psql", "-X", "-q", "-At", "-d", "care", "-c", query)
	if err != nil {
		return out, fmt.Errorf("psql: %w", detail(err, out))
	}
	return out, nil
}

// State reads the archiver and the cluster state.
func (pg PG) State(ctx context.Context) (*PGState, error) {
	out, err := pg.Psql(ctx, StateQuery)
	if err != nil {
		return nil, err
	}
	return ParseState(out)
}
