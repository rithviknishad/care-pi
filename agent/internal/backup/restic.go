package backup

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
)

// restic exit codes (restic 0.17 and later).
const (
	ExitRepoMissing   = 10
	ExitLockFailed    = 11
	ExitWrongPassword = 12
)

// GoMaxProcs limits the threads of restic (02 section 4).
const GoMaxProcs = "2"

// Credentials is the secret of an s3 destination (schema DestinationCredentials).
type Credentials struct {
	AccessKeyID     string    `json:"access_key_id"`
	SecretAccessKey string    `json:"secret_access_key"`
	UpdatedAt       time.Time `json:"updated_at,omitzero"`
}

// Repo is one restic repository and what a run needs to open it.
type Repo struct {
	// Dest is the destination with its defaults (Effective).
	Dest config.BackupDestination
	Key  Key
	// S3 is the secret of an s3 destination.
	S3 *Credentials
	// SSHConfig is the SSH config with the Host block of Dest (the rendered config of apply).
	// "" makes a config for this run from Dest (a candidate of a test or of a restore from backup).
	SSHConfig string
	// Hostname is the comment of the SSH key.
	Hostname string
}

// KeyID returns the key id of the repository.
func (r Repo) KeyID() string { return r.Key.ID() }

// Session is one run of restic: a directory below /run/care-pi/backup with the password file,
// the SSH key, and the S3 credentials (tmpfs, root 0600). Close deletes it.
type Session struct {
	Repo Repo
	dir  string
	env  []string
	opts []string
}

// Open writes the files of a run.
func Open(p paths.Paths, r Repo) (*Session, error) {
	name := r.Dest.ID
	if name == "" {
		name = "candidate"
	}
	var rnd [4]byte
	_, _ = rand.Read(rnd[:])
	dir := p.Abs(paths.RunBackupDir + "/" + name + "-" + hex.EncodeToString(rnd[:]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("cannot create %s: %w", dir, err)
	}
	s := &Session{Repo: r, dir: dir}
	host := func(f string) string { return p.Host(filepath.Join(dir, f)) }
	if err := s.write("password", []byte(r.Key.ResticPassword()+"\n")); err != nil {
		s.Close()
		return nil, err
	}
	s.env = []string{
		"RESTIC_REPOSITORY=" + Repository(r.Dest, r.KeyID()),
		"RESTIC_PASSWORD_FILE=" + host("password"),
		"RESTIC_CACHE_DIR=" + paths.ResticCache,
		"GOMAXPROCS=" + GoMaxProcs,
		"RESTIC_PROGRESS_FPS=0.0166",
	}
	switch r.Dest.Type {
	case TypeS3:
		s.opts = append(s.opts, "-o", "s3.bucket-lookup=path")
		if r.Dest.Region != "" {
			s.opts = append(s.opts, "-o", "s3.region="+r.Dest.Region)
		}
		if r.S3 != nil {
			cred := fmt.Sprintf("[default]\naws_access_key_id = %s\naws_secret_access_key = %s\n", r.S3.AccessKeyID, r.S3.SecretAccessKey)
			if err := s.write("aws-credentials", []byte(cred)); err != nil {
				s.Close()
				return nil, err
			}
			s.env = append(s.env, "AWS_SHARED_CREDENTIALS_FILE="+host("aws-credentials"), "AWS_PROFILE=default")
		}
	case TypeSFTP:
		pemKey, err := r.Key.SSHPrivatePEM(SSHComment(r.Hostname, r.KeyID()))
		if err != nil {
			s.Close()
			return nil, err
		}
		if err := s.write("id_ed25519", pemKey); err != nil {
			s.Close()
			return nil, err
		}
		cfg := r.SSHConfig
		if cfg == "" {
			if err := s.write("known_hosts", []byte(KnownHosts([]config.BackupDestination{r.Dest}))); err != nil {
				s.Close()
				return nil, err
			}
			text := SSHConfig([]config.BackupDestination{r.Dest}, host("known_hosts"))
			if err := s.write("ssh_config", []byte(text)); err != nil {
				s.Close()
				return nil, err
			}
			cfg = host("ssh_config")
		}
		s.opts = append(s.opts, "-o", "sftp.args=-F "+cfg+" -i "+host("id_ed25519"))
	}
	return s, nil
}

func (s *Session) write(name string, data []byte) error {
	if err := os.WriteFile(filepath.Join(s.dir, name), data, 0o600); err != nil {
		return fmt.Errorf("cannot write the run file %s: %w", name, err)
	}
	return nil
}

// Dir returns the directory of the run (below --root).
func (s *Session) Dir() string { return s.dir }

// Close deletes the files of the run.
func (s *Session) Close() {
	if s != nil && s.dir != "" {
		_ = os.RemoveAll(s.dir)
	}
}

// Env returns the environment of restic. It has paths, never a secret.
func (s *Session) Env() []string { return append([]string(nil), s.env...) }

// Cmd returns a restic command with the options of the repository.
func (s *Session) Cmd(args ...string) host.Cmd {
	a := append(append([]string{}, s.opts...), args...)
	return host.Cmd{Name: "restic", Args: a, Env: s.Env()}
}

// Run runs restic and maps a failure to a problem of the destination.
func (s *Session) Run(ctx context.Context, r host.Runner, args ...string) ([]byte, error) {
	out, err := r.Run(ctx, s.Cmd(args...))
	if err != nil {
		return out, Classify(s.Repo.Dest.ID, err)
	}
	return out, nil
}

// SnapshotPaths returns the paths of a snapshot of the tag.
func SnapshotPaths(tag string) []string {
	if tag == TagFiles {
		return []string{paths.S3Data, paths.EtcDir}
	}
	return []string{paths.BackupPG, paths.EtcDir}
}

// Excludes are never in a snapshot: the recovery key, and the temporary files of pgBackRest.
var Excludes = []string{paths.RecoveryKeyFile, paths.PendingKeyFile, "*.pgbackrest.tmp"}

// SnapshotArgs returns the arguments of a restic snapshot of the tag. extra are more paths (the
// local CA of Caddy when it exists).
func SnapshotArgs(tag, hostname string, extra ...string) []string {
	a := []string{"backup", "--json", "--host", hostname, "--tag", tag}
	for _, x := range Excludes {
		a = append(a, "--exclude", x)
	}
	return append(append(a, SnapshotPaths(tag)...), extra...)
}

// HasPath reports whether the snapshot has the path.
func (s Snapshot) HasPath(p string) bool {
	for _, x := range s.Paths {
		if x == p {
			return true
		}
	}
	return false
}

// ForgetArgs returns the arguments of the retention of the tag (docs/backups.md, "Retention").
// The newest snapshot of each tag stays (--keep-last 1).
func ForgetArgs(tag string, r Retention) []string {
	a := []string{"forget", "--tag", tag, "--group-by", "host,tags", "--keep-last", "1"}
	within := ""
	switch {
	case tag == TagDB:
		within = DBKeepWithin
	case r.KeepWithinDays > 0:
		within = strconv.Itoa(r.KeepWithinDays) + "d"
	}
	if within != "" {
		a = append(a, "--keep-within", within)
	}
	for _, kv := range []struct {
		flag string
		n    int
	}{{"--keep-daily", r.KeepDaily}, {"--keep-weekly", r.KeepWeekly}, {"--keep-monthly", r.KeepMonthly}} {
		if kv.n > 0 {
			a = append(a, kv.flag, strconv.Itoa(kv.n))
		}
	}
	return a
}

// CheckArgs returns the arguments of a check that reads back pct percent of the data.
func CheckArgs(pct int) []string {
	return []string{"check", fmt.Sprintf("--read-data-subset=%d%%", pct)}
}

// RestoreArgs returns the arguments of a restore of the directory sub of a snapshot into target.
// includes are relative to sub. verify reads each restored file back.
func RestoreArgs(snapshot, sub, target string, includes []string, verify bool) []string {
	a := []string{"restore", snapshot + ":" + sub, "--target", target}
	for _, i := range includes {
		a = append(a, "--include", "/"+strings.TrimPrefix(i, "/"))
	}
	if verify {
		a = append(a, "--verify")
	}
	return a
}

// Snapshot is one item of "restic snapshots --json".
type Snapshot struct {
	ID       string    `json:"id"`
	ShortID  string    `json:"short_id"`
	Time     time.Time `json:"time"`
	Paths    []string  `json:"paths"`
	Hostname string    `json:"hostname"`
	Tags     []string  `json:"tags"`
	Summary  *struct {
		TotalBytesProcessed int64 `json:"total_bytes_processed"`
		DataAdded           int64 `json:"data_added"`
	} `json:"summary,omitempty"`
}

// HasTag reports whether the snapshot has the tag.
func (s Snapshot) HasTag(t string) bool {
	for _, x := range s.Tags {
		if x == t {
			return true
		}
	}
	return false
}

// Contents returns db, files, and config from the paths of the snapshot.
func (s Snapshot) Contents() []string {
	var out []string
	has := map[string]bool{}
	for _, p := range s.Paths {
		has[p] = true
	}
	if has[paths.BackupPG] {
		out = append(out, "db")
	}
	if has[paths.S3Data] {
		out = append(out, "files")
	}
	if has[paths.EtcDir] {
		out = append(out, "config")
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// Size returns the bytes of the snapshot, when restic tells them.
func (s Snapshot) Size() *int64 {
	if s.Summary == nil {
		return nil
	}
	n := s.Summary.TotalBytesProcessed
	return &n
}

// ParseSnapshots reads "restic snapshots --json". The list is newest first.
func ParseSnapshots(b []byte) ([]Snapshot, error) {
	var out []Snapshot
	if err := json.Unmarshal(bytes.TrimSpace(b), &out); err != nil {
		return nil, fmt.Errorf("cannot read the snapshot list of restic: %w", err)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, nil
}

// BackupSummary is the summary line of "restic backup --json".
type BackupSummary struct {
	SnapshotID          string `json:"snapshot_id"`
	DataAdded           int64  `json:"data_added"`
	DataAddedPacked     int64  `json:"data_added_packed"`
	TotalBytesProcessed int64  `json:"total_bytes_processed"`
	TotalFiles          int64  `json:"total_files_processed"`
}

// ParseBackupSummary reads the summary of "restic backup --json".
func ParseBackupSummary(b []byte) (*BackupSummary, error) {
	for _, l := range bytes.Split(b, []byte("\n")) {
		var m struct {
			Type string `json:"message_type"`
			BackupSummary
		}
		if json.Unmarshal(l, &m) == nil && m.Type == "summary" && m.SnapshotID != "" {
			s := m.BackupSummary
			return &s, nil
		}
	}
	return nil, errors.New("restic printed no summary")
}

// RawSize reads the total size of "restic stats --mode raw-data --json".
func RawSize(b []byte) (int64, error) {
	var m struct {
		TotalSize int64 `json:"total_size"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(b), &m); err != nil {
		return 0, err
	}
	return m.TotalSize, nil
}

// errorText returns the useful text of a restic error: the message of a JSON error line, else the
// last lines of stderr.
func errorText(stderr string) string {
	var lines []string
	for _, l := range strings.Split(stderr, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		var m struct {
			Type    string `json:"message_type"`
			Message string `json:"message"`
			Error   any    `json:"error"`
		}
		if strings.HasPrefix(l, "{") && json.Unmarshal([]byte(l), &m) == nil {
			switch {
			case m.Message != "":
				l = m.Message
			case m.Error != nil:
				l = fmt.Sprint(m.Error)
			default:
				continue
			}
		}
		lines = append(lines, l)
	}
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	return strings.Join(lines, "; ")
}

// Classify maps a failed restic command to a problem of the destination id.
func Classify(id string, err error) error {
	var xe *host.ExitError
	if !errors.As(err, &xe) {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return problem.Unreachable(id, err.Error())
	}
	text := errorText(xe.Stderr)
	if text == "" {
		text = fmt.Sprintf("restic exit code %d", xe.Code)
	}
	low := strings.ToLower(text)
	switch {
	case xe.Code == ExitWrongPassword || strings.Contains(low, "wrong password"):
		p := problem.New(problem.RecoveryKeyMismatch, "The recovery key cannot open the repository: "+text)
		return p
	case strings.Contains(low, "host key verification failed") || strings.Contains(low, "remote host identification has changed"):
		p := problem.New(problem.DestinationUntrusted, "The SFTP server presents a host key that is not pinned: "+text)
		p.DestinationID = id
		return p
	case xe.Code == ExitRepoMissing:
		return &RepoMissingError{Detail: text}
	}
	return problem.Unreachable(id, text)
}

// RepoMissingError tells that the repository does not exist at the destination.
type RepoMissingError struct{ Detail string }

func (e *RepoMissingError) Error() string { return "the repository does not exist: " + e.Detail }

// ErrorCode returns the slug of a problem, "repository-missing", or "internal".
func ErrorCode(err error) string {
	var p *problem.Problem
	if errors.As(err, &p) {
		return p.Slug
	}
	var rm *RepoMissingError
	if errors.As(err, &rm) {
		return "repository-missing"
	}
	return problem.Internal
}
