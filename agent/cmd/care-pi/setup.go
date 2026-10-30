package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/service"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
)

var setupFlags struct {
	json          bool
	careUsername  string
	careEmail     string
	carePassStdin bool
}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "The setup wizard on the host: status, skip a step, finish",
	Long: `setup works on the same setup record as the console (docs/api/README.md, section 5).
"care-pi config set" writes the values of the steps identity, tls, size, and env.`,
}

func printSetup(cmd *cobra.Command, st *service.SetupState) error {
	w := cmd.OutOrStdout()
	if setupFlags.json {
		return printJSON(w, st)
	}
	_, _ = fmt.Fprintf(w, "state: %s (dev build: %v)\n", st.State, st.DevBuild)
	for _, s := range st.Steps {
		line := fmt.Sprintf("  %-15s %s", s.ID, s.Status)
		for _, e := range s.Errors {
			line += " (" + e.Message + ")"
		}
		for _, wa := range s.Warnings {
			line += " [warning: " + wa.Message + "]"
		}
		_, _ = fmt.Fprintln(w, line)
	}
	if st.JobID != nil {
		_, _ = fmt.Fprintf(w, "job: %s\n", *st.JobID)
	}
	return nil
}

var setupStatusCmd = &cobra.Command{
	Use:   "status [--json]",
	Short: "Show the setup state and the steps",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		st, err := newService(cmd).SetupState()
		if err != nil {
			return err
		}
		return printSetup(cmd, st)
	},
}

var setupSkipCmd = &cobra.Command{
	Use:   "skip STEP",
	Short: "Skip env, or backups in a dev build",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := newService(cmd).Skip(args[0])
		if err != nil {
			return cliError(err)
		}
		return printSetup(cmd, st)
	},
}

var setupFinishCmd = &cobra.Command{
	Use:   "finish [--care-username U --care-email E --care-password-stdin]",
	Short: "Finish the setup: apply the config, start Care, and create the Care superuser",
	Long: `finish runs the setup job and prints its events. As root, finish counts the
steps account (after "care-pi auth set-password"), recovery-codes, and hardware
as done. Set the identity, tls, size, and env values with "care-pi config set".
The Care superuser password goes on the standard input (--care-password-stdin).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		f := setupFlags
		svc := newService(cmd)
		if !svc.Auth.Claimed() {
			return exitcode.With(exitcode.BadInput, errors.New("the device is not claimed; run care-pi auth set-password --password-stdin first"))
		}
		if _, err := svc.Setup.Update(func(r *setup.Record) error {
			for _, id := range []string{setup.StepRecoveryCodes, setup.StepHardware} {
				if r.Steps[id] == nil {
					r.Steps[id] = &setup.StepRecord{Status: setup.Done, UpdatedAt: svc.Auth.Now()}
				}
			}
			return nil
		}); err != nil {
			return err
		}
		if f.careUsername != "" || f.careEmail != "" || f.carePassStdin {
			if f.careUsername == "" || f.careEmail == "" || !f.carePassStdin {
				return exitcode.With(exitcode.BadInput, errors.New("--care-username, --care-email, and --care-password-stdin go together"))
			}
			pw, err := readSecret(cmd, "Care superuser password")
			if err != nil {
				return err
			}
			if _, err := svc.StepCareAdmin(f.careUsername, f.careEmail, pw); err != nil {
				return cliError(err)
			}
		}
		j, err := svc.FinishSetup(cmd.Context(), jobs.SourceCLI)
		if err != nil {
			var missing []string
			if st, e := svc.SetupState(); e == nil {
				for _, s := range st.Steps {
					if s.Status != setup.Done && s.Status != setup.Skipped && s.Required {
						missing = append(missing, s.ID)
					}
				}
			}
			if len(missing) > 0 {
				err = fmt.Errorf("%w\nmissing steps: %s", err, strings.Join(missing, ", "))
			}
			return cliError(err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "setup job %s\n", j.ID)
		done, err := followJob(cmd.Context(), cmd.OutOrStdout(), svc, j.ID)
		if err != nil {
			return err
		}
		return jobError(done)
	},
}

func init() {
	setupStatusCmd.Flags().BoolVar(&setupFlags.json, "json", false, "print JSON")
	setupSkipCmd.Flags().BoolVar(&setupFlags.json, "json", false, "print JSON")
	f := setupFinishCmd.Flags()
	f.StringVar(&setupFlags.careUsername, "care-username", "", "user name of the first Care superuser")
	f.StringVar(&setupFlags.careEmail, "care-email", "", "email address of the first Care superuser")
	f.BoolVar(&setupFlags.carePassStdin, "care-password-stdin", false, "read the Care superuser password from the standard input")
	addLockFlag(setupFinishCmd)
	setupCmd.AddCommand(setupStatusCmd, setupSkipCmd, setupFinishCmd)
	rootCmd.AddCommand(setupCmd)
}
