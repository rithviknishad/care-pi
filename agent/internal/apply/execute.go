package apply

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/backup"
	"github.com/rithviknishad/care-pi/agent/internal/fe"
	"github.com/rithviknishad/care-pi/agent/internal/firewall"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/plan"
	"github.com/rithviknishad/care-pi/agent/internal/plugcfg"
	"github.com/rithviknishad/care-pi/agent/internal/render"
	"github.com/rithviknishad/care-pi/agent/internal/s3admin"
	"github.com/rithviknishad/care-pi/agent/internal/secrets"
)

type executor struct {
	o            Options
	pr           *Prepared
	plan         *plan.Plan
	secretValues map[string]string
	ensure       ensureState
	provider     fe.Provider
	bucketsDone  bool
	// journal is the journal of the run (Phase 4: the job id for the maintenance state).
	journal Journal
}

func (x *executor) run(ctx context.Context, a plan.Action) error {
	h := x.o.Host
	switch a.Kind {
	case plan.EnsureDirs:
		return EnsureDirs(x.o.Paths, x.o.Chown)
	case plan.SetSecrets:
		st := readSecretState(x.o.Paths)
		for _, name := range a.Names {
			v := x.secretValues[name]
			if err := h.Podman.SecretSet(ctx, name, []byte(v)); err != nil {
				return fmt.Errorf("cannot set podman secret %s: %w", name, err)
			}
			st[name] = secrets.Hash(v)
		}
		return writeSecretState(x.o.Paths, st)
	case plan.WriteFiles:
		for _, p := range a.Paths {
			if p == paths.SysctlFile {
				if err := x.writeSysctl(ctx, x.pr.Tree[p]); err != nil {
					return err
				}
				continue
			}
			if err := writeFile(x.o.Paths, x.pr.Tree[p], x.o.Chown); err != nil {
				return err
			}
		}
		return nil
	case plan.DeleteFiles:
		for _, p := range a.Paths {
			if err := os.Remove(x.o.Paths.Abs(p)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("cannot delete %s: %w", p, err)
			}
		}
		return nil
	case plan.DaemonReload:
		return h.Systemd.DaemonReload(ctx)
	case plan.EnableTarget:
		// An older care-pi enabled care.target. Then multi-user.target waits for the whole stack.
		if _, err := os.Lstat(x.o.Paths.Abs(legacyTargetLink)); err == nil {
			if err := h.Systemd.Disable(ctx, "care.target"); err != nil {
				return err
			}
		}
		if err := h.Systemd.Enable(ctx, render.StartUnit); err != nil {
			return err
		}
		// The update timers (Phase 4): enable them and start them now.
		var timers []string
		for _, u := range a.Units {
			if u != render.StartUnit {
				timers = append(timers, u)
			}
		}
		if len(timers) > 0 {
			if err := h.Systemd.Enable(ctx, timers...); err != nil {
				return err
			}
			return h.Systemd.Start(ctx, timers...)
		}
		return nil
	case plan.PullImages:
		for _, ref := range a.Names {
			if cb := x.pr.CareBuild; cb != nil && ref == cb.Image() {
				if _, err := x.buildCare(ctx); err != nil {
					return err
				}
				continue
			}
			ok, err := h.Podman.ImageExists(ctx, ref)
			if err != nil {
				return err
			}
			if ok {
				continue
			}
			_, _ = fmt.Fprintln(x.o.Out, "    pull", ref)
			if err := h.Podman.Pull(ctx, ref); err != nil {
				return fmt.Errorf("cannot pull %s: %w", ref, err)
			}
		}
		return nil
	case plan.FullCycle:
		_, _ = fmt.Fprintln(x.o.Out, "    stop care.target, wait until every unit is inactive, then start it")
		if err := h.Systemd.Stop(ctx, "care.target"); err != nil {
			return err
		}
		if err := x.waitInactive(ctx); err != nil {
			return err
		}
		return x.recordPod(x.startTarget(ctx))
	case plan.StartTarget:
		return x.recordPod(x.startTarget(ctx))
	case plan.Restart:
		return h.Systemd.Restart(ctx, a.Units...)
	case plan.ReloadCaddy:
		_, err := h.Podman.Exec(ctx, "care-caddy", "", "caddy", "reload", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
		if err != nil {
			_, _ = fmt.Fprintf(x.o.Out, "    caddy reload failed (%v); restart care-caddy.service\n", err)
			return h.Systemd.Restart(ctx, "care-caddy.service")
		}
		return nil
	case plan.EnsureFE:
		b, err := x.provider.Ensure(ctx, x.pr.Model.FEBuildID)
		if err != nil {
			return fmt.Errorf("FE provider %s: %w", x.provider.Name(), err)
		}
		if b.Dir == "" {
			return nil
		}
		if err := fe.SetCurrent(x.o.Paths, x.pr.Model.FEBuildID); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(x.o.Out, "    fe/current -> %s\n", x.pr.Model.FEBuildID)
		rec := fe.MergeRecord(x.pr.Lock.FE, b.Record)
		if rec == x.pr.Lock.FE {
			return nil
		}
		x.pr.Lock.FE = rec
		// A plan without a lock change still records the new build.
		if !x.plan.Has(plan.WriteLock) {
			x.pr.Lock.GeneratedAt = x.o.Now().UTC().Format(time.RFC3339)
			return lock.Write(x.o.Paths, x.pr.Lock, x.o.Now())
		}
		return nil
	case plan.EnsureBucket:
		files, err := secrets.Store{Paths: x.o.Paths}.ReadAll()
		if err != nil {
			return err
		}
		b := x.pr.Model.Buckets
		err = x.o.S3.EnsureBuckets(ctx, s3admin.Spec{
			Endpoint:       x.o.S3Endpoint,
			AccessKey:      files[secrets.S3AccessKey],
			SecretKey:      files[secrets.S3SecretKey],
			Region:         b.Region,
			PatientBucket:  b.Patient,
			FacilityBucket: b.Facility,
		})
		if err != nil {
			return err
		}
		x.bucketsDone = true
		return nil
	case plan.EnsureStanza:
		if err := x.waitDB(ctx); err != nil {
			return err
		}
		out, err := h.Podman.Exec(ctx, "care-db", "postgres", "pgbackrest", "--stanza="+render.StanzaName, "stanza-create")
		if err != nil {
			return fmt.Errorf("pgbackrest stanza-create failed: %w: %s", err, strings.TrimSpace(string(out)))
		}
		if x.bucketsDone {
			return x.saveEnsure(func(e *ensureState) { e.Buckets, e.Stanza = x.ensure.Buckets, x.ensure.Stanza })
		}
		return nil
	case plan.EnsureMounts:
		x.ensure.Mounts = ""
		return x.ensureMounts(ctx)
	case plan.EnsureRepositories:
		env := backup.Env{Paths: x.o.Paths, Runner: h.Runner, Systemd: h.Systemd, Podman: h.Podman, Now: x.o.Now, Out: x.o.Out}
		backup.EnsureRepositories(ctx, env, x.pr.Config, "cli")
		return x.saveEnsure(func(e *ensureState) { e.Repos = x.ensure.Repos })
	case plan.EnsureTimers:
		return x.ensureTimers(ctx, a)
	case plan.WriteLock:
		x.pr.Lock.GeneratedAt = x.o.Now().UTC().Format(time.RFC3339)
		return lock.Write(x.o.Paths, x.pr.Lock, x.o.Now())
	case plan.Cutover:
		return x.recordPod(x.cutover(ctx, a))
	case plan.EnsureCA:
		return x.ensureCA()
	case plan.DisableUnits:
		if _, err := h.Runner.Run(ctx, host.Cmd{Name: "systemctl", Args: append([]string{"disable", "--now"}, a.Units...)}); err != nil {
			_, _ = fmt.Fprintf(x.o.Out, "    warning: cannot disable %s: %v\n", strings.Join(a.Units, " "), err)
		}
		return nil
	case plan.ReloadFirewall:
		// try-reload-or-restart does nothing when the installer did not install the firewall (--no-firewall).
		// A switch to the policy drop starts the rollback timer first (Phase 7).
		return firewall.Reload(ctx, h, x.o.Paths, render.FirewallOf(x.pr.Config).Policy, x.o.Now(), x.o.Out)
	case plan.UpdateDNS:
		return x.updateDNS(ctx)
	case plan.PlugConfig:
		rows := plugcfg.Desired(x.pr.Config, x.pr.Desire.Plugs, x.pr.Lock)
		res, err := plugcfg.Sync(ctx, h, x.o.Paths, rows, x.o.Now())
		if res != nil {
			_, _ = fmt.Fprintf(x.o.Out, "    plug_config: %d row(s) written, %d deleted\n", len(res.Written), len(res.Deleted))
		}
		return err
	}
	return fmt.Errorf("unknown action %s", a.Kind)
}

// recordPod records the care.pod of the started pod (state/pod-live.json) after a start without an error.
func (x *executor) recordPod(err error) error {
	if err != nil {
		return err
	}
	if f, ok := x.pr.Tree[podFile]; ok {
		if werr := writePodLive(x.o.Paths, f.Content); werr != nil {
			_, _ = fmt.Fprintln(x.o.Out, "    warning: cannot record the running pod:", werr)
		}
	}
	return nil
}

const podFile = paths.QuadletDir + "/care.pod"

// legacyTargetLink is the link of "systemctl enable care.target" from an older care-pi.
const legacyTargetLink = paths.SystemdDir + "/multi-user.target.wants/care.target"

// stackUnits are the units that a full cycle waits for.
func stackUnits() []string { return render.Units }

func (x *executor) startTarget(ctx context.Context) error {
	h := x.o.Host
	_, _ = fmt.Fprintln(x.o.Out, "    wait until the units are healthy; the start waits for the clock gate, and a new database runs the migrations (about 4 minutes on a Pi 5)")
	if err := h.Systemd.ResetFailed(ctx, stackUnits()...); err != nil {
		return err
	}
	return h.Systemd.Start(ctx, "care.target")
}

// waitDB polls until PostgreSQL in care-db accepts connections. stanza-create needs the
// database, not the API. After a start of care.target, care-db is already healthy
// (Notify=healthy), so the first poll passes.
func (x *executor) waitDB(ctx context.Context) error {
	deadline := time.Now().Add(x.o.DBReadyTimeout)
	said := false
	for {
		_, err := x.o.Host.Podman.Exec(ctx, "care-db", "postgres", "pg_isready", "--quiet")
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("care-db does not accept connections after %s: %w", x.o.DBReadyTimeout, err)
		}
		if !said {
			_, _ = fmt.Fprintln(x.o.Out, "    wait until care-db accepts connections")
			said = true
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(x.o.PollInterval):
		}
	}
}

// waitInactive polls until every unit of the stack is inactive or failed.
// "systemctl stop care.target" returns before the PartOf stops finish (SP6 finding 10).
func (x *executor) waitInactive(ctx context.Context) error {
	deadline := time.Now().Add(x.o.StopTimeout)
	units := stackUnits()
	for {
		props, err := x.o.Host.Systemd.Show(ctx, units, "Id", "ActiveState")
		if err != nil {
			return err
		}
		var busy []string
		for i, p := range props {
			s := p["ActiveState"]
			if s != "inactive" && s != "failed" && s != "" {
				busy = append(busy, units[i]+"="+s)
			}
		}
		if len(busy) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("units did not stop within %s: %s", x.o.StopTimeout, strings.Join(busy, " "))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(x.o.PollInterval):
		}
	}
}

// EnsureDirs creates the missing directories of the layout. It does not change existing ones.
func EnsureDirs(p paths.Paths, chown bool) error {
	for _, d := range paths.Layout() {
		abs := p.Abs(d.Path)
		if _, err := os.Stat(abs); err == nil {
			continue
		}
		if err := os.MkdirAll(abs, d.Mode); err != nil {
			return fmt.Errorf("cannot create %s: %w", d.Path, err)
		}
		if err := os.Chmod(abs, d.Mode); err != nil {
			return fmt.Errorf("cannot set the mode of %s: %w", d.Path, err)
		}
		if chown {
			if err := os.Chown(abs, d.UID, d.GID); err != nil {
				return fmt.Errorf("cannot set the owner of %s: %w", d.Path, err)
			}
		}
	}
	return nil
}

func writeFile(p paths.Paths, f render.File, chown bool) error {
	abs := p.Abs(f.Path)
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return fmt.Errorf("cannot create the directory of %s: %w", f.Path, err)
	}
	if err := lock.WriteFileAtomic(abs, f.Content, f.Mode); err != nil {
		return fmt.Errorf("cannot write %s: %w", f.Path, err)
	}
	if chown {
		if err := os.Chown(abs, f.UID, f.GID); err != nil {
			return fmt.Errorf("cannot set the owner of %s: %w", f.Path, err)
		}
	}
	return nil
}

// writeSysctl writes the sysctl file of the pod (docs/connectivity.md, section 4.2) and loads it: systemd-sysctl.service
// loads each file of /etc/sysctl.d again. care-pi.service cannot write /etc/sysctl.d (ProtectSystem=strict), so there
// the file goes through a transient unit. The installer writes the same file, so the agent seldom writes it.
func (x *executor) writeSysctl(ctx context.Context, f render.File) error {
	err := writeFile(x.o.Paths, f, x.o.Chown)
	if err != nil && (errors.Is(err, syscall.EROFS) || errors.Is(err, fs.ErrPermission)) {
		script := `set -eu; f="$1"; mkdir -p "${f%/*}"; cat > "$f.tmp"; chmod 0644 "$f.tmp"; mv "$f.tmp" "$f"`
		_, err = x.o.Host.Runner.Run(ctx, host.Cmd{Name: "systemd-run", Args: []string{"--wait", "--collect", "--pipe", "--quiet",
			"--unit=care-pi-sysctl", "--", "/bin/sh", "-c", script, "sh", x.o.Paths.Abs(f.Path)}, Stdin: bytes.NewReader(f.Content)})
		if err != nil {
			err = fmt.Errorf("cannot write %s: %w", f.Path, err)
		}
	}
	if err != nil {
		return err
	}
	if err := x.o.Host.Systemd.Restart(ctx, paths.SysctlUnit); err != nil {
		return fmt.Errorf("cannot load %s: %w", f.Path, err)
	}
	return nil
}

// saveEnsure updates the ensure record with the parts that a step ensured.
func (x *executor) saveEnsure(fn func(*ensureState)) error {
	st := readEnsure(x.o.Paths)
	fn(&st)
	return writeEnsure(x.o.Paths, st)
}

// listUnits returns the loaded units that match the pattern: name and active state.
func listUnits(ctx context.Context, r host.Runner, pattern string) map[string]string {
	out, err := r.Run(ctx, host.Cmd{Name: "systemctl", Args: []string{"list-units", "--all", "--plain", "--no-legend", "--no-pager", pattern}})
	m := map[string]string{}
	if err != nil {
		return m
	}
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) >= 3 {
			m[f[0]] = f[2]
		}
	}
	return m
}

// ensureMounts starts the automount unit of each backup disk and stops the units of a removed disk.
// A disk that is not connected is not an error: the automount waits for its first use.
func (x *executor) ensureMounts(ctx context.Context) error {
	h := x.o.Host
	want := map[string]bool{}
	for _, m := range x.pr.Model.Backup.Mounts {
		want[m.Mount], want[m.Automount] = true, true
		if err := h.Systemd.Start(ctx, m.Automount); err != nil {
			_, _ = fmt.Fprintf(x.o.Out, "    warning: cannot start %s: %v\n", m.Automount, err)
			continue
		}
		_, _ = fmt.Fprintf(x.o.Out, "    %s at %s\n", m.Label, m.Where)
	}
	for u, state := range listUnits(ctx, h.Runner, "var-lib-care*pi-disks-*") {
		if !want[u] && state != "inactive" {
			_, _ = fmt.Fprintln(x.o.Out, "    stop", u)
			_ = h.Systemd.Stop(ctx, u)
		}
	}
	mounts := x.ensureMountsRecord()
	return x.saveEnsure(func(e *ensureState) { e.Mounts = mounts })
}

func (x *executor) ensureMountsRecord() string {
	return ensureRecord(x.pr, x.o.Paths).Mounts
}

// ensureTimers enables and starts care-backup.target, restarts the changed timers, and stops each
// backup timer that the target does not want (backups.paused, a removed destination). When the local
// repository has no backup yet, it starts one full backup.
func (x *executor) ensureTimers(ctx context.Context, a plan.Action) error {
	h := x.o.Host
	b := x.pr.Model.Backup
	if err := h.Systemd.Enable(ctx, render.BackupTarget); err != nil {
		return err
	}
	if err := h.Systemd.Start(ctx, render.BackupTarget); err != nil {
		return err
	}
	want := map[string]bool{}
	for _, u := range b.Wants {
		want[u] = true
	}
	var restart []string
	for _, u := range a.Units {
		if want[u] {
			restart = append(restart, u)
		}
	}
	if len(restart) > 0 {
		if err := h.Systemd.Restart(ctx, restart...); err != nil {
			return err
		}
	}
	for u, state := range listUnits(ctx, h.Runner, "care-backup-*.timer") {
		if !want[u] && state != "inactive" {
			_, _ = fmt.Fprintln(x.o.Out, "    stop", u)
			_ = h.Systemd.Stop(ctx, u)
		}
	}
	if b.Paused {
		_, _ = fmt.Fprintln(x.o.Out, "    backups.paused is true: the backup timers do not run")
	}
	if !x.o.NoFirstBackup {
		if info, err := (backup.PG{Podman: h.Podman}).Info(ctx); err == nil && info.StatusCode != 1 && len(info.Backups) == 0 {
			_, _ = fmt.Fprintln(x.o.Out, "    the local repository has no backup: start care-backup-db-full.service")
			if _, err := h.Runner.Run(ctx, host.Cmd{Name: "systemctl", Args: []string{"start", "--no-block", "care-backup-db-full.service"}}); err != nil {
				_, _ = fmt.Fprintln(x.o.Out, "    warning:", err)
			}
		}
	}
	timers := ensureRecord(x.pr, x.o.Paths).Timers
	return x.saveEnsure(func(e *ensureState) { e.Timers = timers })
}
