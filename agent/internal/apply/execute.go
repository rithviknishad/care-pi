package apply

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/fe"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/plan"
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
		return h.Systemd.Enable(ctx, render.StartUnit)
	case plan.PullImages:
		for _, ref := range a.Names {
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
		return x.startTarget(ctx)
	case plan.StartTarget:
		return x.startTarget(ctx)
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
			return writeEnsure(x.o.Paths, x.ensure)
		}
		return nil
	case plan.WriteLock:
		x.pr.Lock.GeneratedAt = x.o.Now().UTC().Format(time.RFC3339)
		return lock.Write(x.o.Paths, x.pr.Lock, x.o.Now())
	}
	return fmt.Errorf("unknown action %s", a.Kind)
}

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
