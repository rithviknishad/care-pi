// Package restore runs the restore jobs of care-pi and the steps that they share with the job
// recovery-key-rotate (docs/api/README.md sections 13.8, 13.9; docs/backups.md "Restore").
// Each job is an ordered list of steps; the tests check the host commands of each list.
package restore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/backup"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/secrets"
)

// Stack units of the application (stop-app and start-app).
var AppUnits = []string{"care-api.service", "care-worker.service", "care-beat.service"}

// Env is what a restore job needs.
type Env struct {
	backup.Env
	Rec    *jobs.Recorder
	Config *config.Config
	// Image is the postgres image of the lock, for "podman run".
	Image string
	// Healthy waits until care-api is healthy.
	Healthy func(ctx context.Context) error
	// Poll is the interval of the waits. Timeout bounds each wait.
	Poll    time.Duration
	Timeout time.Duration
	// LockWait bounds the wait for the backup locks in pause-backups (10 minutes).
	LockWait time.Duration
}

func (e Env) poll() time.Duration {
	if e.Poll > 0 {
		return e.Poll
	}
	return 2 * time.Second
}

func (e Env) timeout() time.Duration {
	if e.Timeout > 0 {
		return e.Timeout
	}
	return 10 * time.Minute
}

// Step runs fn as one step of the job.
func (e Env) Step(name, action string, fn func() error) error {
	end := e.Rec.StepAction(name, action)
	err := fn()
	end(err)
	return err
}

func (e Env) logf(format string, a ...any) { e.Rec.Logf(format, a...) }

func (e Env) systemctl(ctx context.Context, args ...string) error {
	_, err := e.Runner.Run(ctx, host.Cmd{Name: "systemctl", Args: args})
	return err
}

// Ops returns the backup operations of the job.
func (e Env) Ops(source string) backup.Ops {
	return backup.Ops{Env: e.Env, Config: e.Config, Source: source, JobID: e.Rec.ID(), Queued: e.Rec.SetQueued,
		Logf: e.logf, Poll: e.Poll, DBWait: e.timeout()}
}

// PauseBackups stops the backup timers and waits for the backup locks (10 minutes at most).
func (e Env) PauseBackups(ctx context.Context) error {
	return e.Step("pause-backups: care-backup.target", "pause-backups", func() error {
		if err := e.systemctl(ctx, "stop", "care-backup.target"); err != nil {
			return err
		}
		wait := e.LockWait
		if wait == 0 {
			wait = 10 * time.Minute
		}
		targets := []string{backup.LocalTarget}
		for _, d := range e.Config.Backups.Destinations {
			targets = append(targets, d.ID)
		}
		for _, t := range targets {
			release, err := backup.Lock(ctx, e.Paths, t, wait, func() { e.logf("wait for the running backup of %s", t) })
			if err != nil {
				return fmt.Errorf("the backup of %s still runs: %w", t, err)
			}
			release()
		}
		return nil
	})
}

// ResumeBackups starts the backup timers again (not with backups.paused).
func (e Env) ResumeBackups(ctx context.Context) error {
	return e.Step("resume-backups: care-backup.target", "resume-backups", func() error {
		if e.Config.Backups.Paused {
			e.logf("backups.paused is true: the timers stay off")
			return nil
		}
		return e.systemctl(ctx, "start", "care-backup.target")
	})
}

// Maintenance turns the maintenance page on or off.
func (e Env) Maintenance(on bool) error {
	action, name := "maintenance-off", "maintenance-off: Caddy serves Care"
	if on {
		action, name = "maintenance-on", "maintenance-on: Caddy serves the maintenance page"
	}
	return e.Step(name, action, func() error { return SetMaintenance(e.Paths, on) })
}

// SetMaintenance writes or removes the flag file of the maintenance page.
func SetMaintenance(p paths.Paths, on bool) error {
	f := p.Abs(paths.ModeDir + "/" + paths.ModeMaintenance)
	if !on {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(p.Abs(paths.ModeDir), 0o755); err != nil {
		return err
	}
	return os.WriteFile(f, []byte("restore\n"), 0o644)
}

// StopUnits stops the units and waits until each one is inactive (SP6 finding 10).
func (e Env) StopUnits(ctx context.Context, units ...string) error {
	if err := e.systemctl(ctx, append([]string{"stop"}, units...)...); err != nil {
		return err
	}
	deadline := time.Now().Add(e.timeout())
	for {
		props, err := e.Systemd.Show(ctx, units, "ActiveState")
		if err != nil {
			return err
		}
		var busy []string
		for i, p := range props {
			if s := p["ActiveState"]; s != "inactive" && s != "failed" && s != "" {
				busy = append(busy, units[i]+"="+s)
			}
		}
		if len(busy) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("units did not stop: %s", strings.Join(busy, " "))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(e.poll()):
		}
	}
}

// StartTarget starts care.target (SP6 finding 6: through the target, never a single unit).
func (e Env) StartTarget(ctx context.Context) error {
	if err := e.Systemd.ResetFailed(ctx, append(append([]string{}, AppUnits...), "care-db.service")...); err != nil {
		return err
	}
	return e.systemctl(ctx, "start", "care.target")
}

// WaitPromote waits until PostgreSQL is out of recovery.
func (e Env) WaitPromote(ctx context.Context) error {
	pg := backup.PG{Podman: e.Podman}
	deadline := time.Now().Add(e.timeout())
	for {
		out, err := pg.Psql(ctx, "select pg_is_in_recovery()")
		if err == nil && strings.TrimSpace(string(out)) == "f" {
			return nil
		}
		if time.Now().After(deadline) {
			if err == nil {
				err = errors.New("the database is still in recovery")
			}
			return fmt.Errorf("care-db did not promote in %s: %w", e.timeout(), err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(e.poll()):
		}
	}
}

// PodmanRun returns a "podman run --rm" of the postgres image in a transient unit with the data
// directory, the repository, and the pgBackRest config at the paths of care-db (SP6: pgBackRest writes
// --pg1-path into restore_command).
func PodmanRun(image, name, pgData string, repoRO bool, entrypoint string, args ...string) host.Cmd {
	repo := paths.BackupPG + ":/var/lib/pgbackrest"
	if repoRO {
		repo += ":ro"
	}
	a := []string{"--quiet", "--wait", "--pipe", "--collect", "--property=MemoryMax=512M", "--property=MemorySwapMax=256M",
		"--description=care-pi " + name, "--", "podman", "run", "--rm", "--name", name, "--network", "none", "--user", "postgres",
		"--volume", pgData + ":/var/lib/postgresql/data", "--volume", repo,
		"--volume", paths.RenderedDB + ":/etc/pgbackrest:ro",
		"--secret", secrets.PodmanPGBackRestCipher + ",type=env,target=PGBACKREST_REPO1_CIPHER_PASS"}
	if entrypoint != "" {
		a = append(a, "--entrypoint", entrypoint)
	}
	a = append(a, image)
	return host.Cmd{Name: "systemd-run", Args: append(a, args...)}
}

// TargetArg formats a time for "pgbackrest restore --target".
func TargetArg(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05.000000") + "+00" }

// RestoreArgs returns the arguments of "pgbackrest restore": a time, a checkpoint (a backup set), or
// the end of the WAL (time zero and no set).
func RestoreArgs(t *time.Time, set string, delta bool) []string {
	a := []string{"--stanza=" + backup.Stanza, "restore"}
	if delta {
		a = append(a, "--delta")
	}
	switch {
	case set != "":
		a = append(a, "--set="+set, "--type=immediate", "--target-action=promote")
	case t != nil:
		a = append(a, "--type=time", "--target="+TargetArg(*t), "--target-action=promote")
	}
	return a
}

// RunRestore runs "pgbackrest restore" in a container while care-db is stopped.
func (e Env) RunRestore(ctx context.Context, t *time.Time, set string) error {
	delta := false
	if _, err := os.Stat(e.Paths.Abs(paths.PGData + "/PG_VERSION")); err == nil {
		delta = true
	}
	out, err := e.Runner.Run(ctx, PodmanRun(e.Image, "care-pi-restore", paths.PGData, false, "pgbackrest", RestoreArgs(t, set, delta)...))
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			e.Rec.Log("stdout", l)
		}
	}
	if err != nil {
		var xe *host.ExitError
		if errors.As(err, &xe) {
			return fmt.Errorf("pgbackrest restore: exit code %d: %s", xe.Code, lastLines(xe.Stderr+string(out), 3))
		}
		return err
	}
	return nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " ")
}
