package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/firewall"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/service"
)

var firewallFlags struct {
	json   bool
	reason string
}

var firewallCmd = &cobra.Command{
	Use:   "firewall",
	Short: "The table inet care-pi: the policy, SSH, and the rollback timer of the policy drop (Phase 7)",
}

var firewallStatusCmd = &cobra.Command{
	Use:   "status [--json]",
	Short: "Show the policy of config.yaml, the loaded policy, and the rollback timer (GET /security/firewall)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		st, err := newService(cmd).Firewall(cmd.Context())
		if err != nil {
			return cliError(err)
		}
		w := cmd.OutOrStdout()
		if firewallFlags.json {
			return printJSON(w, st)
		}
		live := "not loaded by care-pi yet"
		if st.LivePolicy != nil {
			live = *st.LivePolicy
		}
		_, _ = fmt.Fprintf(w, "policy (config.yaml): %s\nloaded policy:        %s\nSSH (22/tcp):         %v\nunit active:          %v\n",
			st.Policy, live, st.SSHAllowed, st.UnitActive)
		if st.PendingUntil != nil {
			_, _ = fmt.Fprintf(w, "rollback at:          %s (run: care-pi firewall confirm)\n", st.PendingUntil.Local().Format(time.RFC3339))
		}
		if st.RolledBackAt != nil {
			_, _ = fmt.Fprintf(w, "rolled back at:       %s (%s)\n", st.RolledBackAt.Local().Format(time.RFC3339), deref(st.RollbackReason))
		}
		return nil
	},
}

// setFirewallKey writes one key of security in config.yaml and runs the apply (as "config set KEY=VALUE --apply").
func setFirewallKey(cmd *cobra.Command, key, value string) error {
	svc := newService(cmd)
	doc, _, err := svc.ConfigView("file")
	if err != nil {
		return cliError(err)
	}
	m, _ := doc.(map[string]any)
	if err := service.SetKey(m, key, value); err != nil {
		return exitcode.With(exitcode.BadInput, err)
	}
	etag, err := svc.ETag()
	if err != nil {
		return err
	}
	res, err := svc.WriteConfig(cmd.Context(), service.ConfigWrite{IfMatch: etag, Body: m, Source: jobs.SourceCLI})
	if err != nil {
		return cliError(err)
	}
	printPlan(cmd.OutOrStdout(), res.Plan)
	return runApply(cmd, "firewall: "+key+"="+value)
}

var firewallPolicyCmd = &cobra.Command{
	Use:   "policy accept|drop",
	Short: "Set security.firewall.policy and apply",
	Long: `policy sets security.firewall.policy and runs the apply. drop accepts only the allow-list
(docs/console-server.md, section 13). A switch to drop starts a rollback timer of 90 s:
open a NEW SSH session and the console to check them, then run "care-pi firewall confirm".
Without the confirmation, the policy goes back to accept.`,
	Args:      cobra.ExactArgs(1),
	ValidArgs: []string{firewall.Accept, firewall.Drop},
	RunE: func(cmd *cobra.Command, args []string) error {
		if args[0] != firewall.Accept && args[0] != firewall.Drop {
			return exitcode.With(exitcode.BadInput, fmt.Errorf("the policy is accept or drop, not %q", args[0]))
		}
		return setFirewallKey(cmd, "security.firewall.policy", args[0])
	},
}

var firewallAllowSSHCmd = &cobra.Command{
	Use:   "allow-ssh true|false",
	Short: "Set security.ssh.allow (accept 22/tcp with the policy drop) and apply",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if args[0] != "true" && args[0] != "false" {
			return exitcode.With(exitcode.BadInput, fmt.Errorf("use true or false, not %q", args[0]))
		}
		return setFirewallKey(cmd, "security.ssh.allow", args[0])
	},
}

var firewallConfirmCmd = &cobra.Command{
	Use:   "confirm",
	Short: "Keep the policy drop: stop the rollback timer (POST /security/firewall/confirm)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if _, err := newService(cmd).FirewallConfirm(cmd.Context(), "cli"); err != nil {
			return cliError(err)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "the policy drop stays")
		return nil
	},
}

var firewallRollbackCmd = &cobra.Command{
	Use:   "rollback [--reason timeout|manual]",
	Short: "Set the policy to accept at once, in nftables and in config.yaml (the rollback timer runs it)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := newService(cmd).FirewallRollback(cmd.Context(), firewallFlags.reason); err != nil {
			return cliError(err)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "the firewall policy is accept")
		return nil
	},
}

func init() {
	firewallStatusCmd.Flags().BoolVar(&firewallFlags.json, "json", false, "print JSON (schema FirewallState)")
	firewallRollbackCmd.Flags().StringVar(&firewallFlags.reason, "reason", "manual", "timeout or manual (state/firewall.json)")
	firewallCmd.AddCommand(firewallStatusCmd, firewallPolicyCmd, firewallAllowSSHCmd, firewallConfirmCmd, firewallRollbackCmd)
	rootCmd.AddCommand(firewallCmd)
}
