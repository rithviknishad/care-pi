package main

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/firewall"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/service"
)

var firewallFlags struct {
	json    bool
	reason  string
	iface   string
	comment string
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
		printAllowPorts(w, st)
		return nil
	},
}

// setFirewallKey writes one key of security in config.yaml and runs the apply (as "config set KEY=VALUE --apply").
func setFirewallKey(cmd *cobra.Command, key, value string) error {
	return editConfigAndApply(cmd, "firewall: "+key+"="+value, func(m map[string]any) error {
		if err := service.SetKey(m, key, value); err != nil {
			return exitcode.With(exitcode.BadInput, err)
		}
		return nil
	})
}

// editConfigAndApply changes config.yaml (view file) with edit, writes it with the validation of the API, and runs
// the apply.
func editConfigAndApply(cmd *cobra.Command, reason string, edit func(map[string]any) error) error {
	svc := newService(cmd)
	doc, etag, err := svc.ConfigView("file")
	if err != nil {
		return cliError(err)
	}
	m, _ := doc.(map[string]any)
	if err := edit(m); err != nil {
		return err
	}
	res, err := svc.WriteConfig(cmd.Context(), service.ConfigWrite{IfMatch: etag, Body: m, Source: jobs.SourceCLI})
	if err != nil {
		return cliError(err)
	}
	printPlan(cmd.OutOrStdout(), res.Plan)
	return runApply(cmd, reason)
}

// parsePortProto parses PORT or PORT/PROTO (tcp or udp; default tcp).
func parsePortProto(s string) (int, string, error) {
	num, proto, ok := strings.Cut(s, "/")
	if !ok {
		proto = "tcp"
	}
	port, err := strconv.Atoi(num)
	if err != nil || port < 1 || port > 65535 {
		return 0, "", exitcode.With(exitcode.BadInput, fmt.Errorf("the port is a number from 1 to 65535 with an optional /tcp or /udp, not %q", s))
	}
	if proto != "tcp" && proto != "udp" {
		return 0, "", exitcode.With(exitcode.BadInput, fmt.Errorf("the protocol is tcp or udp, not %q", proto))
	}
	return port, proto, nil
}

var firewallAllowCmd = &cobra.Command{
	Use:   "allow",
	Short: "The extra ports of the table: security.firewall.allow_ports (S64)",
	Long: `allow edits security.firewall.allow_ports: the extra ports that the table accepts after the rules of care-pi,
for example a node_exporter of the host. They matter only with the policy drop. add and remove write config.yaml
and apply (the plan action reload-firewall). A service of the host on the tailnet needs an entry too
(--iface tailscale0): the table has no rule for all of tailscale0.`,
}

var firewallAllowAddCmd = &cobra.Command{
	Use:   "add PORT[/PROTO] [--iface IF] [--comment TEXT]",
	Short: "Accept PORT (tcp by default) on each interface or on --iface, and apply",
	Example: `  care-pi firewall allow add 9100 --iface tailscale0 --comment "node exporter"
  care-pi firewall allow add 51820/udp`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		port, proto, err := parsePortProto(args[0])
		if err != nil {
			return err
		}
		entry := config.AllowPort{Port: port, Proto: proto, Iface: firewallFlags.iface, Comment: firewallFlags.comment}
		err = editConfigAndApply(cmd, fmt.Sprintf("firewall: allow %d/%s", port, proto), func(m map[string]any) error {
			if !service.AddAllowPort(m, entry) {
				return errNoChange
			}
			return nil
		})
		if errors.Is(err, errNoChange) {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%d/%s on %s is already in security.firewall.allow_ports\n", port, proto, ifaceText(entry.Iface))
			return nil
		}
		return err
	},
}

var firewallAllowRemoveCmd = &cobra.Command{
	Use:   "remove PORT[/PROTO] [--iface IF]",
	Short: "Remove the entries of PORT (tcp by default), or only the entry of --iface, and apply",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		port, proto, err := parsePortProto(args[0])
		if err != nil {
			return err
		}
		return editConfigAndApply(cmd, fmt.Sprintf("firewall: remove %d/%s", port, proto), func(m map[string]any) error {
			if service.RemoveAllowPort(m, port, proto, firewallFlags.iface) == 0 {
				return exitcode.With(exitcode.BadInput, fmt.Errorf("%d/%s on %s is not in security.firewall.allow_ports (care-pi firewall allow list)",
					port, proto, ifaceText(firewallFlags.iface)))
			}
			return nil
		})
	},
}

var firewallAllowListCmd = &cobra.Command{
	Use:   "list [--json]",
	Short: "Show security.firewall.allow_ports",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		st, err := newService(cmd).Firewall(cmd.Context())
		if err != nil {
			return cliError(err)
		}
		w := cmd.OutOrStdout()
		if firewallFlags.json {
			return printJSON(w, st.AllowPorts)
		}
		printAllowPorts(w, st)
		return nil
	},
}

// errNoChange stops editConfigAndApply when the edit changes nothing.
var errNoChange = errors.New("no change")

func ifaceText(iface string) string {
	if iface == "" {
		return "each interface"
	}
	return iface
}

func printAllowPorts(w io.Writer, st *service.FirewallState) {
	if len(st.AllowPorts) == 0 {
		_, _ = fmt.Fprintln(w, "extra ports:          none (care-pi firewall allow add PORT[/PROTO])")
	} else {
		_, _ = fmt.Fprintln(w, "extra ports:")
		for _, a := range st.AllowPorts {
			line := fmt.Sprintf("  %d/%s on %s", a.Port, a.Proto, ifaceText(deref(a.Iface)))
			if a.Comment != nil {
				line += " (" + *a.Comment + ")"
			}
			_, _ = fmt.Fprintln(w, line)
		}
	}
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

var firewallBootCheckCmd = &cobra.Command{
	Use:    "boot-check",
	Short:  "End a policy drop that waits for its confirmation (ExecStartPre of care-pi-firewall.service)",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		stale, err := newService(cmd).FirewallBootCheck()
		if err != nil {
			return cliError(err)
		}
		if stale {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "the policy drop was not confirmed: the firewall policy is accept")
		}
		return nil
	},
}

func init() {
	firewallStatusCmd.Flags().BoolVar(&firewallFlags.json, "json", false, "print JSON (schema FirewallState)")
	firewallRollbackCmd.Flags().StringVar(&firewallFlags.reason, "reason", "manual", "timeout or manual (state/firewall.json)")
	firewallAllowAddCmd.Flags().StringVar(&firewallFlags.iface, "iface", "", "the input interface, for example eth0, wlan0, or tailscale0 (default: each interface)")
	firewallAllowAddCmd.Flags().StringVar(&firewallFlags.comment, "comment", "", "the comment of the rule (up to 60 characters)")
	firewallAllowRemoveCmd.Flags().StringVar(&firewallFlags.iface, "iface", "", "remove only the entry of this interface")
	firewallAllowListCmd.Flags().BoolVar(&firewallFlags.json, "json", false, "print JSON (a list of the schema FirewallAllowPort)")
	firewallAllowCmd.AddCommand(firewallAllowAddCmd, firewallAllowRemoveCmd, firewallAllowListCmd)
	firewallCmd.AddCommand(firewallStatusCmd, firewallPolicyCmd, firewallAllowSSHCmd, firewallAllowCmd, firewallConfirmCmd, firewallRollbackCmd, firewallBootCheckCmd)
	rootCmd.AddCommand(firewallCmd)
}
