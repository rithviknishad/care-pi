package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
)

// Env is what the backup operations need from the host.
type Env struct {
	Paths   paths.Paths
	Runner  host.Runner
	Systemd host.Systemd
	Podman  host.Podman
	Now     func() time.Time
	Out     io.Writer
}

func (e Env) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC().Truncate(time.Second)
	}
	return time.Now().UTC().Truncate(time.Second)
}

func (e Env) out() io.Writer {
	if e.Out == nil {
		return io.Discard
	}
	return e.Out
}

// State returns the state files of the env.
func (e Env) State() State { return State{Paths: e.Paths, Now: e.Now} }

// Keys returns the key store of the env.
func (e Env) Keys() *KeyStore { return &KeyStore{Paths: e.Paths, Now: e.Now} }

func secretPath(p paths.Paths, id string) string {
	return p.Abs(paths.BackupSecretsDir + "/" + id + ".json")
}

// ReadCredentials returns the stored secret of a destination, or nil.
func ReadCredentials(p paths.Paths, id string) *Credentials {
	data, err := os.ReadFile(secretPath(p, id))
	if err != nil {
		return nil
	}
	var c Credentials
	if json.Unmarshal(data, &c) != nil || c.AccessKeyID == "" || c.SecretAccessKey == "" {
		return nil
	}
	return &c
}

// WriteCredentials stores the secret of a destination (root 0600).
func WriteCredentials(p paths.Paths, id string, c Credentials) error {
	if err := os.MkdirAll(p.Abs(paths.BackupSecretsDir), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return lock.WriteFileAtomic(secretPath(p, id), append(data, '\n'), 0o600)
}

// CredentialsState returns not-needed, present, or missing.
func CredentialsState(p paths.Paths, d config.BackupDestination) string {
	if d.Type != TypeS3 {
		return CredNotNeeded
	}
	if ReadCredentials(p, d.ID) != nil {
		return CredPresent
	}
	return CredMissing
}

// ActiveKey returns the active recovery key, or a no-recovery-key problem.
func ActiveKey(e Env) (Key, error) {
	k, _, err := e.Keys().Get(Active)
	if errors.Is(err, ErrNoKey) {
		return k, problem.New(problem.NoRecoveryKey, "No recovery key exists. Make it with POST /backups/recovery-key or \"care-pi backup recovery-key create\".")
	}
	return k, err
}

// OpenDest opens a session for a destination of config.yaml with the key.
func OpenDest(e Env, cfg *config.Config, d config.BackupDestination, k Key) (*Session, error) {
	ed := Effective(d, cfg.Backups)
	r := Repo{Dest: ed, Key: k, Hostname: cfg.Identity.Hostname}
	switch ed.Type {
	case TypePOSIX:
		if err := DiskPresent(e.Paths, ed); err != nil {
			return nil, err
		}
	case TypeS3:
		r.S3 = ReadCredentials(e.Paths, ed.ID)
		if r.S3 == nil {
			p := problem.Newf(problem.DestinationUnreachable, "The destination %s has no S3 keys. Set them with PUT /backups/destinations/%s/secrets.", ed.ID, ed.ID)
			p.DestinationID = ed.ID
			return nil, p
		}
	case TypeSFTP:
		if ed.HostKey == "" {
			p := problem.Newf(problem.DestinationUntrusted, "The destination %s has no pinned host key. Test it and accept its host key.", ed.ID)
			p.DestinationID = ed.ID
			return nil, p
		}
		cfgPath := paths.RenderedBackup + "/ssh_config"
		if _, err := os.Stat(e.Paths.Abs(cfgPath)); err == nil && sshConfigHas(e.Paths.Abs(cfgPath), ed) {
			r.SSHConfig = cfgPath
		}
	}
	return Open(e.Paths, r)
}

// DiskPresent fails fast when the disk of a disk_label destination is not connected. Without the
// check, a run waits for the automount, which waits 90 s for the device (wave 5a, reboot on lumine).
func DiskPresent(p paths.Paths, d config.BackupDestination) error {
	if d.Type != TypePOSIX || d.DiskLabel == "" {
		return nil
	}
	if _, err := os.Stat(p.Abs("/dev/disk/by-label/" + d.DiskLabel)); err != nil {
		return problem.Unreachable(d.ID, "the backup disk "+d.DiskLabel+" is not connected")
	}
	return nil
}

// sshConfigHas reports whether the rendered SSH config has the Host block of the destination as
// the config now wants it (a test before the apply uses a config of the run).
func sshConfigHas(file string, d config.BackupDestination) bool {
	data, err := os.ReadFile(file)
	if err != nil {
		return false
	}
	want := SSHConfig([]config.BackupDestination{d}, paths.RenderedBackup+"/known_hosts")
	if !strings.Contains(string(data), strings.TrimSpace(want)) {
		return false
	}
	kh, err := os.ReadFile(filepath.Join(filepath.Dir(file), "known_hosts"))
	return err == nil && strings.Contains(string(kh), strings.TrimSpace(KnownHosts([]config.BackupDestination{d})))
}

// EnsureRepository opens or makes the restic repository of a destination and records the result.
func EnsureRepository(ctx context.Context, e Env, cfg *config.Config, d config.BackupDestination, k Key, source string) error {
	st := e.State()
	loc := Location(Effective(d, cfg.Backups), k.ID())
	cur := st.Dest(d.ID)
	if cur.Initialized && cur.KeyID == k.ID() && cur.Location == loc {
		return nil
	}
	s, err := OpenDest(e, cfg, d, k)
	if err != nil {
		recordInitError(st, d.ID, err, e.now(), source)
		return err
	}
	defer s.Close()
	start := e.now()
	_, err = s.Run(ctx, e.Runner, "cat", "config")
	var rm *RepoMissingError
	made := false
	if errors.As(err, &rm) {
		finish := st.AddRun(d.ID, DestinationRun{StartedAt: start, Kind: "init", Source: source})
		_, err = s.Run(ctx, e.Runner, "init")
		finish(err == nil, "", nil, err)
		made = err == nil
	}
	if err != nil {
		if !made {
			recordInitError(st, d.ID, err, start, "")
		}
		return err
	}
	_, err = st.UpdateDest(d.ID, func(ds *DestState) {
		if ds.KeyID != k.ID() || ds.Location != loc {
			*ds = DestState{ID: d.ID, Runs: ds.Runs}
		}
		ds.KeyID, ds.Initialized, ds.Location = k.ID(), true, loc
	})
	return err
}

func recordInitError(st State, id string, err error, at time.Time, source string) {
	if source != "" {
		st.AddRun(id, DestinationRun{StartedAt: at, Kind: "init", Source: source})(false, "", nil, err)
		return
	}
	_, _ = st.UpdateDest(id, func(d *DestState) {
		code, msg := ErrorCode(err), err.Error()
		d.LastError, d.LastErrorCode = &msg, &code
		if d.FailingSince == nil {
			t := at
			d.FailingSince = &t
		}
	})
}

// EnsureRepositories ensures the repository of each destination. A destination that fails does not
// fail the call: its state gets the error and its timer tries again (docs/api/README.md section 13.5).
// It returns a warning for each failure. It also removes the secrets and the state of each
// destination that config.yaml does not have.
func EnsureRepositories(ctx context.Context, e Env, cfg *config.Config, source string) []string {
	RemoveStale(e, cfg)
	if len(cfg.Backups.Destinations) == 0 {
		return nil
	}
	k, err := ActiveKey(e)
	if err != nil {
		return []string{"no recovery key: the destinations get their repositories after \"care-pi backup recovery-key create\""}
	}
	var warn []string
	for _, d := range cfg.Backups.Destinations {
		c, cancel := context.WithTimeout(ctx, 3*time.Minute)
		err := EnsureRepository(c, e, cfg, d, k, source)
		cancel()
		if err != nil {
			w := fmt.Sprintf("%s: %v", d.ID, err)
			_, _ = fmt.Fprintln(e.out(), "    warning:", w)
			warn = append(warn, w)
			continue
		}
		_, _ = fmt.Fprintf(e.out(), "    %s: %s\n", d.ID, Location(Effective(d, cfg.Backups), k.ID()))
	}
	return warn
}

// RemoveStale deletes the secret and the state file of each destination that config.yaml does not have.
func RemoveStale(e Env, cfg *config.Config) {
	want := map[string]bool{}
	for _, d := range cfg.Backups.Destinations {
		want[d.ID] = true
	}
	if ents, err := os.ReadDir(e.Paths.Abs(paths.BackupSecretsDir)); err == nil {
		for _, en := range ents {
			id, ok := strings.CutSuffix(en.Name(), ".json")
			if ok && !want[id] {
				if err := os.Remove(filepath.Join(e.Paths.Abs(paths.BackupSecretsDir), en.Name())); err == nil || errors.Is(err, fs.ErrNotExist) {
					_, _ = fmt.Fprintf(e.out(), "    removed the secret of %s\n", id)
				}
			}
		}
	}
	for _, id := range e.State().DestIDs() {
		if !want[id] {
			e.State().RemoveDest(id)
		}
	}
}

// Fingerprint returns a stable text of the destinations and the key for the ensure record.
func Fingerprint(cfg *config.Config, keyID string) string {
	var parts []string
	for _, d := range cfg.Backups.Destinations {
		parts = append(parts, d.ID+"="+Location(Effective(d, cfg.Backups), keyID)+"#"+HostKeyType(d.HostKey))
	}
	sort.Strings(parts)
	return keyID + ";" + strings.Join(parts, ",")
}
