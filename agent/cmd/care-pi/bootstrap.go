package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/auth"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/hostcheck"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/render"
	"github.com/rithviknishad/care-pi/agent/internal/s3admin"
	"github.com/rithviknishad/care-pi/agent/internal/secrets"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
)

var bootstrapFlags struct {
	hostname    string
	preset      string
	displayName string
	feProvider  string
	yes         bool
	unattended  bool
}

var bootstrapCmd = &cobra.Command{
	Use:   "bootstrap [--hostname NAME] [--preset NAME] [--fe-provider NAME] [--yes] [--unattended]",
	Short: "First run: check the host, create the directories and secrets, write config.yaml, then apply",
	Long: `bootstrap prepares a new host. Steps:

1. Check the host prerequisites. Stop with exit code 3 when one is missing.
2. Create the missing directories.
3. Generate the missing secrets. bootstrap never replaces a secret.
4. Write a default config.yaml when the file does not exist.
   --hostname, --preset, --display-name, and --fe-provider go into this file.
   When config.yaml exists, bootstrap keeps it and ignores these flags.
5. Make a claim code when the console is not claimed, and start care-pi.service.
6. Setup mode (the default on a new host): apply --phase=bootstrap. Only care.pod
   and care-caddy start. Caddy serves the setup page on port 443 and the console on
   port 8443. bootstrap prints the claim code and the console URLs. The operator
   finishes the setup in the console, or with "care-pi setup finish".
   --unattended: apply everything now (the Phase 1 behaviour). With fe.provider
   local, apply builds care_fe on this host (about 3 minutes, needs internet).
   A host with a lock.yaml (an earlier install) always gets the full apply.

bootstrap holds the lock /run/care-pi/apply.lock for its full run.
bootstrap asks no questions. --yes exists for the installer.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		w := cmd.OutOrStdout()
		p := rootPaths()
		h := newHost(cmd)
		f := bootstrapFlags
		if f.preset != "" && f.preset != "solo" && f.preset != "small-clinic" && f.preset != "busy-counter" {
			return exitcode.With(exitcode.BadInput, fmt.Errorf("--preset must be solo, small-clinic, or busy-counter"))
		}
		hostname := f.hostname
		if hostname != "" && !hostnameRE.MatchString(hostname) {
			return exitcode.With(exitcode.BadInput, fmt.Errorf("--hostname %q is not a valid DNS label (a-z, 0-9, dash)", hostname))
		}
		if f.feProvider != "none" && f.feProvider != "artifact" && f.feProvider != "local" {
			return exitcode.With(exitcode.BadInput, fmt.Errorf("--fe-provider must be none, artifact, or local"))
		}
		release, err := takeApplyLock(cmd)
		if err != nil {
			return err
		}
		defer release()

		_, _ = fmt.Fprintln(w, "==> host check")
		r := hostcheck.Run(cmd.Context(), hostcheck.Env{Paths: p, Podman: h.Podman, SkipDirs: true})
		if !r.OK() {
			r.WriteText(w)
			return &exitcode.Error{Code: exitcode.HostMissing, Err: errors.New("a required host prerequisite is missing; run the installer, then run bootstrap again")}
		}

		_, _ = fmt.Fprintln(w, "==> directories")
		chown := os.Geteuid() == 0
		if err := apply.EnsureDirs(p, chown); err != nil {
			return err
		}

		_, _ = fmt.Fprintln(w, "==> secrets")
		made, err := secrets.Store{Paths: p}.EnsureAll()
		if err != nil {
			return err
		}
		if len(made) > 0 {
			_, _ = fmt.Fprintf(w, "    generated: %s\n", strings.Join(made, " "))
		}

		cfgPath := configPath(cmd)
		if _, err := os.Stat(cfgPath); errors.Is(err, fs.ErrNotExist) {
			if hostname == "" {
				hostname = osHostname()
			}
			data, err := config.DefaultYAML(f.displayName, hostname, f.preset, f.feProvider)
			if err != nil {
				return exitcode.With(exitcode.BadInput, err)
			}
			if err := lock.WriteFileAtomic(cfgPath, data, 0o600); err != nil {
				return fmt.Errorf("cannot write %s: %w", cfgPath, err)
			}
			_, _ = fmt.Fprintf(w, "==> wrote %s (hostname %s, fe.provider %s)\n", cfgPath, hostname, f.feProvider)
		} else {
			_, _ = fmt.Fprintf(w, "==> keep %s\n", cfgPath)
		}

		phase, err := bootstrapPhase(w, p, f.unattended)
		if err != nil {
			return err
		}
		code, err := bootstrapClaimCode(newAuth())
		if err != nil {
			return err
		}

		if _, err := os.Stat(p.Abs(paths.AgentUnitFile)); err == nil {
			_, _ = fmt.Fprintf(w, "==> start %s\n", paths.AgentUnit)
			if err := h.Systemd.Enable(cmd.Context(), paths.AgentUnit); err != nil {
				_, _ = fmt.Fprintf(w, "warning: cannot enable %s: %v\n", paths.AgentUnit, err)
			} else if err := h.Systemd.Start(cmd.Context(), paths.AgentUnit); err != nil {
				_, _ = fmt.Fprintf(w, "warning: cannot start %s: %v; the console is not available until it runs\n", paths.AgentUnit, err)
			}
		} else {
			_, _ = fmt.Fprintf(w, "warning: %s does not exist; run the installer again to install the console agent\n", paths.AgentUnitFile)
		}

		_, _ = fmt.Fprintf(w, "==> apply (phase %s)\n", phase)
		_, err = apply.Run(cmd.Context(), apply.Options{
			Paths:      p,
			ConfigPath: cfgPath,
			Reason:     "bootstrap",
			Out:        w,
			Host:       h,
			IPs:        netinfo.System{},
			S3:         s3admin.MinIO{},
			Chown:      chown,
			LockHeld:   true,
			Phase:      phase,
		})
		if err != nil {
			return err
		}
		if code != "" {
			var names []string
			if cfg, err := config.Load(cfgPath); err == nil {
				names = render.Hostnames(cfg)
			}
			ips, _ := netinfo.LANIPv4(netinfo.System{})
			printClaimBox(w, code, names, ips)
		}
		return nil
	},
}

// bootstrapPhase chooses the apply phase: setup mode on a new host, else the full apply.
func bootstrapPhase(w io.Writer, p paths.Paths, unattended bool) (string, error) {
	st := &setup.Store{Paths: p}
	rec, exists, err := st.Load()
	if err != nil {
		return "", err
	}
	_, lockErr := os.Stat(p.Abs(paths.LockFile))
	hasLock := lockErr == nil
	switch {
	case unattended:
		_, _ = fmt.Fprintln(w, "==> unattended: apply everything now; the console setup is not necessary")
		return setup.PhaseFull, nil
	case exists && rec.ReadyAt == nil && rec.ApplyPhase == setup.PhaseBootstrap:
		return setup.PhaseBootstrap, nil
	case exists || hasLock:
		_, _ = fmt.Fprintln(w, "==> this host has an earlier install: apply everything")
		return setup.PhaseFull, nil
	}
	if _, err := st.Update(func(r *setup.Record) error { r.ApplyPhase = setup.PhaseBootstrap; return nil }); err != nil {
		return "", err
	}
	_, _ = fmt.Fprintln(w, "==> setup mode: start Caddy and the console only; Care starts when the setup finishes")
	return setup.PhaseBootstrap, nil
}

// bootstrapClaimCode returns the claim code of an unclaimed device ("" after the claim).
// When it makes the code, it records the claim hint claim-code: bootstrap prints the code,
// so the console shows the claim code field first.
func bootstrapClaimCode(a *auth.Manager) (string, error) {
	if a.Claimed() {
		return "", nil
	}
	code, made, err := a.EnsureClaimCode()
	if err != nil || !made {
		return code, err
	}
	if err := a.SetClaimHint(auth.ClaimCode); err != nil {
		return "", err
	}
	return code, nil
}

// printClaimBox prints the claim code and the console URLs in a box.
func printClaimBox(w io.Writer, code string, names, ips []string) {
	lines := []string{"Claim code: " + code, "", "Open the console and enter the claim code:"}
	for _, n := range append(append([]string{}, names...), ips...) {
		lines = append(lines, fmt.Sprintf("  https://%s:%d/", n, paths.ConsolePort))
	}
	lines = append(lines, "", "Print the code again: sudo care-pi auth claim-code")
	width := 0
	for _, l := range lines {
		width = max(width, len(l))
	}
	border := "+" + strings.Repeat("-", width+4) + "+"
	_, _ = fmt.Fprintln(w, border)
	for _, l := range lines {
		_, _ = fmt.Fprintf(w, "|  %-*s  |\n", width, l)
	}
	_, _ = fmt.Fprintln(w, border)
}

var hostnameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// osHostname returns the first label of the OS host name as a valid DNS label, or "care".
func osHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return config.DefaultHostname
	}
	h = strings.ToLower(strings.SplitN(h, ".", 2)[0])
	h = regexp.MustCompile(`[^a-z0-9-]+`).ReplaceAllString(h, "-")
	h = strings.Trim(h, "-")
	if len(h) > 63 {
		h = strings.Trim(h[:63], "-")
	}
	if !hostnameRE.MatchString(h) || h == "localhost" {
		return config.DefaultHostname
	}
	return h
}

func init() {
	f := bootstrapCmd.Flags()
	f.StringVar(&bootstrapFlags.hostname, "hostname", "", "mDNS host label (default: the OS host name)")
	f.StringVar(&bootstrapFlags.preset, "preset", "", "solo, small-clinic, or busy-counter (default: small-clinic)")
	f.StringVar(&bootstrapFlags.displayName, "display-name", "", "name of the clinic (default: CARE)")
	f.StringVar(&bootstrapFlags.feProvider, "fe-provider", config.DefaultFEProvider, "fe.provider of the new config.yaml: none, artifact, or local")
	f.BoolVar(&bootstrapFlags.yes, "yes", false, "do not ask questions (bootstrap asks none)")
	f.BoolVar(&bootstrapFlags.unattended, "unattended", false, "apply everything now (Phase 1 behaviour); no console setup")
	addLockFlag(bootstrapCmd)
	rootCmd.AddCommand(bootstrapCmd)
}
