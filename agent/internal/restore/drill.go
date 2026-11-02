package restore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/backup"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/hostcheck"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
)

// Drill names on the host.
const (
	DrillContainer        = "care-drill"
	DrillRestoreContainer = "care-drill-restore"
	DrillUnit             = "care-backup-drill-pg.service"
)

// DrillSpace returns the free space and the space that a drill needs: 2 times the database size.
// FreeOverride (the environment variable CARE_PI_DRILL_FREE_BYTES) replaces the free space in a test.
func DrillSpace(ctx context.Context, e Env) (need, free int64, err error) {
	st, err := (backup.PG{Podman: e.Podman}).State(ctx)
	if err != nil {
		return 0, 0, err
	}
	need = 2 * st.DatabaseBytes
	free, _, err = backup.Free(e.Paths.Abs(paths.DataDir))
	if v := os.Getenv("CARE_PI_DRILL_FREE_BYTES"); v != "" {
		var n int64
		if _, perr := fmt.Sscan(v, &n); perr == nil {
			free = n
		}
	}
	return need, free, err
}

// Drill runs the steps of restore-drill (docs/api/README.md section 13.8): restore the newest local
// backup into a temporary directory, start PostgreSQL there with archive_mode=off, check it, and delete it.
// skipOnSpace records a skip (a timer run) instead of the error insufficient-space.
func Drill(ctx context.Context, e Env, skipOnSpace bool) (*backup.DrillResult, error) {
	start := time.Now()
	res := &backup.DrillResult{Kind: jobs.KindRestoreDrill, Outcome: "passed", Checks: []hostcheck.Check{}}
	record := func(outcome string, detail error) {
		d := time.Since(start).Seconds()
		o := backup.DrillOutcome{FinishedAt: time.Now().UTC().Truncate(time.Second), Outcome: outcome, DurationS: &d}
		if id := e.Rec.ID(); id != "" {
			o.JobID = &id
		}
		if detail != nil {
			msg := detail.Error()
			o.Detail = &msg
		}
		_ = e.State().AddDrill(o)
	}
	var info *backup.RepoInfo
	err := e.Step("check-space: "+paths.DataDir, "check-space", func() error {
		need, free, err := DrillSpace(ctx, e)
		if err != nil {
			return err
		}
		if free < need {
			return problem.NeedSpace(paths.DataDir, need, free, "The restore drill needs free space of 2 times the database size.")
		}
		info, err = (backup.PG{Podman: e.Podman}).Info(ctx)
		if err == nil && info.Last() == nil {
			err = ErrNoBackup
		}
		return err
	})
	if err != nil {
		if skipOnSpace && problem.Is(err, problem.InsufficientSpace) {
			reason := err.Error()
			res.Outcome, res.SkippedReason = "skipped", &reason
			res.DurationS = time.Since(start).Seconds()
			record("skipped", err)
			return res, nil
		}
		record("failed", err)
		return nil, err
	}
	last := info.Last()
	res.BackupLabel = &last.Label
	dir := paths.DrillDir + "/" + e.Rec.ID()
	// removeDrill deletes the containers of a drill. "systemctl stop" of DrillUnit can stop "podman run --rm" while it
	// removes the stopped container, and a container that stays blocks the name of each later drill.
	removeDrill := func(ctx context.Context) {
		for _, name := range []string{DrillRestoreContainer, DrillContainer} {
			_, _ = e.Runner.Run(ctx, host.Cmd{Name: "podman", Args: []string{"rm", "--force", "--ignore", name}})
		}
	}
	cleanup := func() error {
		bg := context.WithoutCancel(ctx)
		_, _ = e.Runner.Run(bg, host.Cmd{Name: "podman", Args: []string{"stop", "--ignore", "--time", "30", DrillContainer}})
		_ = e.systemctl(bg, "stop", DrillUnit)
		_, _ = e.Runner.Run(bg, host.Cmd{Name: "systemctl", Args: []string{"reset-failed", DrillUnit}})
		removeDrill(bg)
		return os.RemoveAll(e.Paths.Abs(dir))
	}
	fail := func(err error) (*backup.DrillResult, error) {
		_ = e.Step("drill-cleanup: "+dir, "drill-cleanup", cleanup)
		record("failed", err)
		return nil, problem.New(problem.VerifyFailed, "The restore drill failed: "+err.Error())
	}
	if err := e.Step("drill-restore: "+last.Label+" to "+dir, "drill-restore", func() error {
		// A container of an earlier drill that was not removed (see removeDrill).
		removeDrill(ctx)
		if err := os.MkdirAll(e.Paths.Abs(dir), 0o700); err != nil {
			return err
		}
		if err := os.Chown(e.Paths.Abs(dir), paths.PostgresUID, paths.PostgresGID); err != nil && !errors.Is(err, os.ErrPermission) {
			return err
		}
		args := []string{"--stanza=" + backup.Stanza, "restore", "--set=" + last.Label, "--type=immediate",
			"--target-action=promote", "--archive-mode=off"}
		out, err := e.Runner.Run(ctx, PodmanRun(e.Image, DrillRestoreContainer, dir, true, "pgbackrest", args...))
		if err != nil {
			return fmt.Errorf("pgbackrest restore: %w: %s", err, lastLines(string(out), 3))
		}
		return nil
	}); err != nil {
		return fail(err)
	}
	if err := e.Step("drill-start: PostgreSQL with archive_mode=off", "drill-start", func() error {
		// max_connections must not be lower than on the primary (40), else the recovery stops.
		run := PodmanRun(e.Image, DrillContainer, dir, true, "", "postgres", "-c", "archive_mode=off",
			"-c", "shared_buffers=32MB", "-c", "max_connections=40", "-c", "listen_addresses=")
		// A service without --wait: PostgreSQL runs until drill-cleanup stops it.
		args := []string{"--quiet", "--collect", "--unit=" + DrillUnit}
		for _, a := range run.Args {
			if a == "--wait" || a == "--pipe" || a == "--quiet" || a == "--collect" {
				continue
			}
			args = append(args, a)
		}
		if _, err := e.Runner.Run(ctx, host.Cmd{Name: "systemd-run", Args: args}); err != nil {
			return err
		}
		deadline := time.Now().Add(min(e.timeout(), 5*time.Minute))
		for {
			_, err := e.Podman.Exec(ctx, DrillContainer, "postgres", "pg_isready", "--quiet")
			if err == nil {
				return nil
			}
			if props, serr := e.Systemd.Show(ctx, []string{DrillUnit}, "ActiveState"); serr == nil {
				if st := props[0]["ActiveState"]; st == "failed" || st == "inactive" {
					return fmt.Errorf("the drill database stopped (%s); see journalctl -u %s", st, DrillUnit)
				}
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("the drill database does not accept connections: %w", err)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(e.poll()):
			}
		}
	}); err != nil {
		return fail(err)
	}
	if err := e.Step("drill-check: pg_controldata, select 1", "drill-check", func() error {
		out, err := e.Podman.Exec(ctx, DrillContainer, "postgres", "pg_controldata", "/var/lib/postgresql/data")
		state := ""
		for _, l := range strings.Split(string(out), "\n") {
			if v, ok := strings.CutPrefix(l, "Database cluster state:"); ok {
				state = strings.TrimSpace(v)
			}
		}
		ok := err == nil && state == "in production"
		res.Checks = append(res.Checks, hostcheck.Check{Name: "pg_controldata", Level: hostcheck.Required, OK: ok, Detail: "cluster state: " + state})
		out2, err2 := e.Podman.Exec(ctx, DrillContainer, "postgres", "psql", "-X", "-At", "-d", "care", "-c", "select 1")
		ok2 := err2 == nil && strings.TrimSpace(string(out2)) == "1"
		res.Checks = append(res.Checks, hostcheck.Check{Name: "select 1", Level: hostcheck.Required, OK: ok2, Detail: strings.TrimSpace(string(out2))})
		if !ok || !ok2 {
			return fmt.Errorf("the drill database failed a check (state %q, select 1 %q)", state, strings.TrimSpace(string(out2)))
		}
		return nil
	}); err != nil {
		return fail(err)
	}
	if du, err := dirSize(e.Paths.Abs(dir)); err == nil {
		res.RestoredBytes = &du
	}
	if err := e.Step("drill-cleanup: "+dir, "drill-cleanup", cleanup); err != nil {
		record("failed", err)
		return nil, err
	}
	res.DurationS = time.Since(start).Seconds()
	record("passed", nil)
	return res, nil
}

func dirSize(dir string) (int64, error) {
	var n int64
	err := walk(dir, func(sz int64) { n += sz })
	return n, err
}
