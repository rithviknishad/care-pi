package fe

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// Units of the local build.
const (
	BuildUnit  = "care-fe-build.service"
	apiUnit    = "care-api.service"
	workerUnit = "care-worker.service"
	// BuildUnitFile is the quadlet of the builder. apply renders it for fe.provider: local.
	BuildUnitFile = paths.QuadletDir + "/care-fe-build.container"
	// ScaleDownDropIn sets one gunicorn worker in care-api during a build (S8).
	// apply does not own drop-in directories. apply removes only this file, and only when
	// a build stopped before its restore (RemoveScaleDown).
	ScaleDownDropIn = paths.QuadletDir + "/care-api.container.d/50-care-pi-build.conf"
)

const scaleDownContent = "[Container]\nEnvironment=GUNICORN_WORKERS=1\n"

// Defaults of the local provider.
const (
	DefaultBuildTimeout  = 40 * time.Minute
	DefaultHealthTimeout = 10 * time.Minute
	defaultPoll          = 2 * time.Second
	// The restore restarts care-api, which can take up to TimeoutStartSec (900 s).
	restoreTimeout = 20 * time.Minute
)

func init() {
	Register("local", func(env Env) (Provider, error) { return NewLocal(env), nil })
}

// Local builds care_fe on the host in care-fe-build.service (spike SP4).
//
// Before the build, it scales care-api down to one gunicorn worker and stops care-worker.
// After the build, it always restores them, also when the build fails.
type Local struct {
	env Env
	// BuildTimeout limits "systemctl start care-fe-build.service".
	BuildTimeout time.Duration
	// HealthTimeout limits the wait for care-api after the scale-down.
	HealthTimeout time.Duration
	Poll          time.Duration
	Now           func() time.Time
}

// NewLocal returns the local provider.
func NewLocal(env Env) *Local {
	return &Local{
		env:           env,
		BuildTimeout:  DefaultBuildTimeout,
		HealthTimeout: DefaultHealthTimeout,
		Poll:          defaultPoll,
		Now:           time.Now,
	}
}

// Name implements Provider.
func (l *Local) Name() string { return "local" }

// Ensure implements Provider.
func (l *Local) Ensure(ctx context.Context, buildID string) (Build, error) {
	if !validID(buildID) {
		return Build{}, fmt.Errorf("bad FE build id %q", buildID)
	}
	p := l.env.Paths
	dir := Dir(buildID)
	rec := lock.FE{Provider: l.Name(), BuildID: buildID, CareFESHA: l.env.CareFESHA}
	if Exists(p, buildID) {
		return Build{Dir: dir, Record: rec}, nil
	}
	if l.env.Host == nil {
		return Build{}, errors.New("the local FE provider needs the host clients")
	}
	unlock, err := takeLock(ctx, p, true)
	if err != nil {
		return Build{}, err
	}
	defer unlock()
	if Exists(p, buildID) {
		return Build{Dir: dir, Record: rec}, nil
	}
	image, err := l.checkUnit(buildID)
	if err != nil {
		return Build{}, err
	}
	start := l.Now()
	if err := l.build(ctx, image); err != nil {
		return Build{}, err
	}
	if !Exists(p, buildID) {
		return Build{}, fmt.Errorf("care-fe-build.service completed, but %s/index.html does not exist; see journalctl -u %s", dir, BuildUnit)
	}
	d := l.Now().Sub(start).Round(time.Second)
	rec.BuiltAt = start.UTC().Format(time.RFC3339)
	rec.Duration = d.String()
	_, _ = fmt.Fprintf(l.env.out(), "    FE build %s is ready in %s\n", buildID, d)
	return Build{Dir: dir, Record: rec}, nil
}

// checkUnit makes sure that the builder unit on disk builds buildID. It returns the image of the unit.
func (l *Local) checkUnit(buildID string) (image string, err error) {
	data, err := os.ReadFile(l.env.Paths.Abs(BuildUnitFile))
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%s does not exist; run care-pi apply", BuildUnitFile)
	}
	if err != nil {
		return "", err
	}
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "Image="); ok {
			image = strings.TrimSpace(v)
		}
		v, ok := strings.CutPrefix(line, "Environment=")
		if !ok {
			continue
		}
		for _, f := range strings.Fields(v) {
			if f == "BUILD_ID="+buildID {
				found = true
			}
		}
	}
	if !found {
		return "", fmt.Errorf("%s is not for FE build %s; run care-pi apply", BuildUnitFile, buildID)
	}
	return image, nil
}

// build scales the stack down, runs the builder unit, and restores the stack.
func (l *Local) build(ctx context.Context, image string) (err error) {
	h := l.env.Host
	out := l.env.out()
	// Pull the builder image before the scale-down, so the API keeps all workers during the pull.
	if image != "" {
		ok, err := h.Podman.ImageExists(ctx, image)
		if err != nil {
			return fmt.Errorf("cannot check the builder image %s: %w", image, err)
		}
		if !ok {
			_, _ = fmt.Fprintln(out, "    pull", image)
			if err := h.Podman.Pull(ctx, image); err != nil {
				return fmt.Errorf("cannot pull the builder image %s: %w", image, err)
			}
		}
	}
	// Ctrl-C, SIGTERM, or SIGHUP (for example an SSH disconnect) stops the build,
	// and the restore below still runs.
	ctx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stopSignals()
	// Ctrl-C under install.sh also stops tee, the reader of stdout. Then a write gives
	// SIGPIPE, and SIGPIPE stops care-pi before the restore. With SIGPIPE ignored, the
	// write gives EPIPE, and the restore continues. Go cannot undo signal.Ignore, so the
	// rest of the command also ignores SIGPIPE. apply then completes its last steps.
	signal.Ignore(syscall.SIGPIPE)
	states, err := h.Systemd.Show(ctx, []string{apiUnit, workerUnit}, "ActiveState")
	if err != nil {
		return fmt.Errorf("cannot read the state of care-api and care-worker: %w", err)
	}
	apiActive := states[0]["ActiveState"] == "active"
	workerActive := states[1]["ActiveState"] == "active"
	workers := 1
	if l.env.Config != nil {
		workers = l.env.Config.Sizing().GunicornWorkers
	}
	scaleAPI := apiActive && workers > 1

	var droppedIn, workerStopped bool
	defer func() {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), restoreTimeout)
		defer cancel()
		if rerr := l.restore(rctx, droppedIn, workerStopped); rerr != nil {
			err = errors.Join(err, fmt.Errorf("cannot restore care-api and care-worker after the FE build: %w", rerr))
		}
	}()

	if scaleAPI {
		_, _ = fmt.Fprintln(out, "    scale down for the FE build: care-api to 1 gunicorn worker")
		droppedIn = true
		if err := l.writeDropIn(); err != nil {
			return err
		}
		if err := h.Systemd.DaemonReload(ctx); err != nil {
			return err
		}
		if err := h.Systemd.Restart(ctx, apiUnit); err != nil {
			return fmt.Errorf("cannot restart care-api with 1 worker: %w", err)
		}
	}
	if workerActive {
		_, _ = fmt.Fprintln(out, "    stop care-worker for the FE build")
		workerStopped = true
		if err := h.Systemd.Stop(ctx, workerUnit); err != nil {
			return fmt.Errorf("cannot stop care-worker: %w", err)
		}
	}
	if scaleAPI {
		if err := l.waitHealthy(ctx); err != nil {
			return err
		}
	}

	_, _ = fmt.Fprintf(out, "    start %s: about 1 minute warm, 3 minutes cold on a Pi 5\n", BuildUnit)
	bctx, cancel := context.WithTimeout(ctx, l.BuildTimeout)
	defer cancel()
	stop := l.follow(bctx)
	err = h.Systemd.Start(bctx, BuildUnit)
	stop()
	if err != nil {
		// systemctl can end before the unit ends (timeout, signal). Stop the unit before the restore.
		// For a unit that failed, the stop does nothing.
		sctx, scancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer scancel()
		_ = h.Systemd.Stop(sctx, BuildUnit)
		switch {
		case ctx.Err() != nil:
			return fmt.Errorf("care-pi stopped the FE build: %w", ctx.Err())
		case bctx.Err() != nil:
			return fmt.Errorf("the FE build did not complete within %s, so care-pi stopped it", l.BuildTimeout)
		}
		return fmt.Errorf("the FE build failed: %w. Run: journalctl -u %s", err, BuildUnit)
	}
	return nil
}

// restore removes the drop-in, restarts care-api, and starts care-worker again.
// It acts first and prints last, because a print can fail when stdout has no reader.
func (l *Local) restore(ctx context.Context, droppedIn, workerStopped bool) error {
	h := l.env.Host
	var errs []error
	if droppedIn {
		if err := os.Remove(l.env.Paths.Abs(ScaleDownDropIn)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("cannot remove %s: %w", ScaleDownDropIn, err))
		}
		if err := h.Systemd.DaemonReload(ctx); err != nil {
			errs = append(errs, err)
		}
		if err := h.Systemd.Restart(ctx, apiUnit); err != nil {
			errs = append(errs, err)
		}
	}
	if workerStopped {
		if err := h.Systemd.Start(ctx, workerUnit); err != nil {
			errs = append(errs, err)
		}
	}
	if droppedIn || workerStopped {
		_, _ = fmt.Fprintln(l.env.out(), "    restored care-api and care-worker after the FE build")
	}
	return errors.Join(errs...)
}

// ScaleDownLeft reports whether the scale-down drop-in exists.
func ScaleDownLeft(p paths.Paths) bool {
	_, err := os.Lstat(p.Abs(ScaleDownDropIn))
	return err == nil
}

// RemoveScaleDown removes the drop-in of a build that stopped before its restore,
// for example after a power loss. Then it runs daemon-reload. With restart, it also
// restarts care-api and starts care-worker. The caller must hold the apply lock
// (applock), so that no build runs now.
func RemoveScaleDown(ctx context.Context, p paths.Paths, h *host.Host, restart bool) error {
	if err := os.Remove(p.Abs(ScaleDownDropIn)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("cannot remove %s: %w", ScaleDownDropIn, err)
	}
	if err := h.Systemd.DaemonReload(ctx); err != nil {
		return err
	}
	if !restart {
		return nil
	}
	if err := h.Systemd.Restart(ctx, apiUnit); err != nil {
		return err
	}
	return h.Systemd.Start(ctx, workerUnit)
}

func (l *Local) writeDropIn() error {
	path := l.env.Paths.Abs(ScaleDownDropIn)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("cannot create %s: %w", filepath.Dir(ScaleDownDropIn), err)
	}
	if err := lock.WriteFileAtomic(path, []byte(scaleDownContent), 0o644); err != nil {
		return fmt.Errorf("cannot write %s: %w", ScaleDownDropIn, err)
	}
	return nil
}

// waitHealthy waits until care-api is active and its health check passes.
func (l *Local) waitHealthy(ctx context.Context) error {
	h := l.env.Host
	deadline := l.Now().Add(l.HealthTimeout)
	for {
		props, err := h.Systemd.Show(ctx, []string{apiUnit}, "ActiveState")
		if err != nil {
			return err
		}
		if props[0]["ActiveState"] == "active" {
			health, err := h.Podman.Health(ctx, "care")
			if err != nil {
				return err
			}
			if health["care-api"] == "healthy" {
				return nil
			}
		}
		if l.Now().After(deadline) {
			return fmt.Errorf("care-api did not become healthy within %s after the scale-down", l.HealthTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(l.Poll):
		}
	}
}

// follow streams the journal of the builder to the terminal. It is best effort.
// The returned function stops the stream.
func (l *Local) follow(ctx context.Context) func() {
	if !l.env.Follow || l.env.Host.Runner == nil {
		return func() {}
	}
	fctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = l.env.Host.Runner.Run(fctx, host.Cmd{
			Name:   "journalctl",
			Args:   []string{"--unit", BuildUnit, "--follow", "--lines", "0", "--output", "cat", "--no-pager"},
			Attach: true,
		})
	}()
	return func() {
		// journald can deliver the last lines a moment after the unit stops.
		time.Sleep(500 * time.Millisecond)
		cancel()
		wg.Wait()
	}
}
