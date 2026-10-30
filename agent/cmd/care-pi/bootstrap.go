package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/hostcheck"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/s3admin"
	"github.com/rithviknishad/care-pi/agent/internal/secrets"
)

var bootstrapFlags struct {
	hostname    string
	preset      string
	displayName string
	feProvider  string
	yes         bool
}

var bootstrapCmd = &cobra.Command{
	Use:   "bootstrap [--hostname NAME] [--preset NAME] [--fe-provider NAME] [--yes]",
	Short: "First run: check the host, create the directories and secrets, write config.yaml, then apply",
	Long: `bootstrap prepares a new host. Steps:

1. Check the host prerequisites. Stop with exit code 3 when one is missing.
2. Create the missing directories.
3. Generate the missing secrets. bootstrap never replaces a secret.
4. Write a default config.yaml when the file does not exist.
   --hostname, --preset, --display-name, and --fe-provider go into this file.
   When config.yaml exists, bootstrap keeps it and ignores these flags.
5. Run apply. With fe.provider local, apply builds care_fe on this host.
   The first build takes about 3 minutes and needs internet.

bootstrap holds the lock /run/care-pi/apply.lock for its full run.
bootstrap asks no questions. --yes exists for the installer and changes nothing in Phase 1.`,
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

		_, _ = fmt.Fprintln(w, "==> apply")
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
		})
		return err
	},
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
	f.BoolVar(&bootstrapFlags.yes, "yes", false, "do not ask questions (bootstrap asks none in Phase 1)")
	addLockFlag(bootstrapCmd)
	rootCmd.AddCommand(bootstrapCmd)
}
