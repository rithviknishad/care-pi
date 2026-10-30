package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/auth"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/hostcheck"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/s3admin"
	"github.com/rithviknishad/care-pi/agent/internal/secrets"
	"github.com/rithviknishad/care-pi/agent/internal/server"
	"github.com/rithviknishad/care-pi/agent/internal/service"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
	"github.com/rithviknishad/care-pi/console"
)

type serveOptions struct {
	listen       string
	insecureDev  bool
	fakeHost     bool
	allowOrigins []string
}

var serveFlags serveOptions

var serveCmd = &cobra.Command{
	Use:   "serve [--listen unix:///run/care-pi/api.sock]",
	Short: "Run the agent: the console API and the console on the agent socket (care-pi.service)",
	Long: `serve runs the HTTP API of docs/api/openapi.yaml and the console SPA.
It listens on the unix socket /run/care-pi/api.sock. Caddy is its only client:
it sends each request of port 8443 and /_care-pi/* of port 443 to the socket.
serve tells systemd when it is ready (Type=notify) and stops on SIGTERM.

For the development of the console only:
  care-pi serve --root ./.dev-root --listen tcp://127.0.0.1:8443 --insecure-dev
--insecure-dev serves plain HTTP on a loopback address, with a session cookie
without Secure. It makes a dev root with a claim code when the root is empty.
--fake-host answers the host commands (systemctl, podman) with plausible output.
Never use --insecure-dev on a clinic device.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		f := serveFlags
		p := rootPaths()
		// Check each flag before Listen: Listen replaces an old socket.
		if err := checkServeFlags(f, p); err != nil {
			return exitcode.With(exitcode.BadInput, err)
		}
		log := slog.New(slog.NewTextHandler(cmd.OutOrStdout(), &slog.HandlerOptions{
			ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
				if a.Key == slog.TimeKey {
					return slog.Attr{}
				}
				return a
			},
		}))
		applySoftMemoryLimit(log)
		l, err := server.Listen(f.listen, p, f.insecureDev)
		if err != nil {
			return exitcode.With(exitcode.BadInput, err)
		}
		svc := newService(cmd)
		svc.Log = log
		svc.Auth = auth.New(auth.Options{Paths: p, OS: auth.Shadow{Paths: p}, Log: log})
		svc.Jobs = &jobs.Manager{Paths: p, Log: log}
		svc.Host = host.New(host.Transient{})
		if global.verbose {
			svc.Host = host.New(host.Transient{Exec: host.Exec{Log: cmd.ErrOrStderr()}})
		}
		if f.fakeHost {
			svc.Host = host.New(host.NewDev())
			svc.Host.Git = host.DevGit{}
			svc.IPs = netinfo.Static{"192.168.1.10"}
			svc.S3 = &s3admin.Fake{}
			svc.Chown = false
			svc.HostCheck = func(context.Context) *hostcheck.Report {
				return &hostcheck.Report{Checks: []hostcheck.Check{{Name: "podman", Level: hostcheck.Required, OK: true, Detail: "podman 5.4.2 (fake host)"}}}
			}
			svc.ApplyHook = func(o *apply.Options) { o.PollInterval = 10 * time.Millisecond }
			svc.AfterPower = func(d time.Duration, _ func()) { log.Info("fake host: no power action", "delay", d) }
		} else {
			svc.Internet = &service.Internet{}
		}
		if f.insecureDev {
			if err := devRoot(cmd, svc); err != nil {
				return err
			}
		}
		if ids := jobs.MarkInterrupted(p, time.Now()); len(ids) > 0 {
			log.Warn("jobs were interrupted by a stop of their process", "ids", ids)
		}
		dist, err := fs.Sub(console.Dist, "dist")
		if err != nil {
			return err
		}
		srv := server.New(server.Options{Service: svc, Log: log, Console: dist, Dev: f.insecureDev, AllowOrigins: f.allowOrigins})
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if svc.Internet != nil {
			go svc.Internet.Run(ctx)
		}
		log.Info("care-pi serve: ready", "listen", l.Addr().String(), "dev", f.insecureDev, "setup_state", svc.SetupStateName())
		if err := server.Notify("READY=1"); err != nil {
			log.Warn("sd_notify failed", "error", err)
		}
		err = srv.Serve(ctx, l)
		_ = server.Notify("STOPPING=1")
		wait, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		svc.Jobs.Shutdown(wait)
		if l.Addr().Network() == "unix" {
			_ = os.Remove(l.Addr().String())
		}
		log.Info("care-pi serve: stopped")
		return err
	},
}

// checkServeFlags checks the flags of serve: the dev flags need --insecure-dev, --insecure-dev
// needs --root with a dev directory, and a TCP listener needs --insecure-dev and a loopback address.
func checkServeFlags(f serveOptions, p paths.Paths) error {
	if (f.fakeHost || len(f.allowOrigins) > 0) && !f.insecureDev {
		return errors.New("--fake-host and --allow-origin need --insecure-dev")
	}
	if f.insecureDev && (p.Root == "/" || p.Root == "") {
		return errors.New("--insecure-dev needs --root with a dev directory, for example --root ./.dev-root")
	}
	return server.CheckListen(f.listen, f.insecureDev)
}

// devRoot prepares an empty dev root: directories, secrets, config.yaml (fe.provider none),
// the setup record of setup mode, and a claim code.
func devRoot(cmd *cobra.Command, svc *service.Service) error {
	p := svc.Paths
	if err := apply.EnsureDirs(p, false); err != nil {
		return err
	}
	if _, err := (secrets.Store{Paths: p}).EnsureAll(); err != nil {
		return err
	}
	if _, err := os.Stat(svc.ConfigPath); errors.Is(err, fs.ErrNotExist) {
		data, err := config.DefaultYAML("Dev Clinic", "care", config.DefaultPreset, "none")
		if err != nil {
			return err
		}
		if err := lock.WriteFileAtomic(svc.ConfigPath, data, 0o600); err != nil {
			return err
		}
		if _, err := svc.Setup.Update(func(r *setup.Record) error { r.ApplyPhase = setup.PhaseBootstrap; return nil }); err != nil {
			return err
		}
	}
	if code, _, err := svc.Auth.EnsureClaimCode(); err == nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "dev root %s: claim code %s\n", p.Root, code)
	}
	return nil
}

func init() {
	f := serveCmd.Flags()
	f.StringVar(&serveFlags.listen, "listen", "", "unix://PATH or tcp://HOST:PORT (default: unix:///run/care-pi/api.sock below --root)")
	f.BoolVar(&serveFlags.insecureDev, "insecure-dev", false, "plain HTTP on a loopback TCP address, for console development only")
	f.BoolVar(&serveFlags.fakeHost, "fake-host", false, "answer the host commands with plausible output (needs --insecure-dev)")
	f.StringArrayVar(&serveFlags.allowOrigins, "allow-origin", nil, "one more origin for unsafe requests (needs --insecure-dev)")
	rootCmd.AddCommand(serveCmd)
}
