package main

import (
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/auth"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
)

var authFlags struct {
	passwordStdin bool
	rotate        bool
	regenerate    bool
	json          bool
}

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage the console account on the host: password, claim code, recovery codes, TOTP, sessions",
	Long: `auth manages the operator account of the console (docs/api/README.md, section 2).
Root on the host needs no session. Each command writes /etc/care-pi/secrets/agent.json
or /var/lib/care-pi/state/sessions.json. The agent reads the change at once.`,
}

func printCodes(cmd *cobra.Command, codes []string) {
	w := cmd.OutOrStdout()
	_, _ = fmt.Fprintln(w, "Recovery codes. Keep them in a safe place. Each code works one time:")
	for _, c := range codes {
		_, _ = fmt.Fprintln(w, "  "+c)
	}
}

func needStdin(flag bool, what string) error {
	if !flag {
		return exitcode.With(exitcode.BadInput, fmt.Errorf("give the %s on the standard input with --password-stdin", what))
	}
	return nil
}

var authSetPasswordCmd = &cobra.Command{
	Use:   "set-password --password-stdin",
	Short: "Set the console password; on an unclaimed device, also claim it",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := needStdin(authFlags.passwordStdin, "password"); err != nil {
			return err
		}
		pw, err := readSecret(cmd, "password")
		if err != nil {
			return err
		}
		a := newAuth()
		codes, claimed, err := a.SetPassword(pw)
		if err != nil {
			return cliError(err)
		}
		if claimed {
			if err := newService(cmd).AfterClaim(a.Now()); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "The device is claimed. The console password is set.")
			printCodes(cmd, codes)
			return nil
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "The console password is set. Each session ended.")
		return nil
	},
}

var authResetCmd = &cobra.Command{
	Use:   "reset --password-stdin",
	Short: "Last fallback (S46): set the password, make 8 new recovery codes, turn TOTP off, end each session",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := needStdin(authFlags.passwordStdin, "password"); err != nil {
			return err
		}
		pw, err := readSecret(cmd, "password")
		if err != nil {
			return err
		}
		codes, err := newAuth().Reset(pw)
		if err != nil {
			return cliError(err)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "The account is reset: new password, TOTP off, no session, no lock.")
		printCodes(cmd, codes)
		return nil
	},
}

var authClaimCodeCmd = &cobra.Command{
	Use:   "claim-code [--rotate]",
	Short: "Print the claim code of an unclaimed device; --rotate makes a new code",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		a := newAuth()
		var code string
		var err error
		if authFlags.rotate {
			code, err = a.RotateClaimCode()
		} else {
			code, _, err = a.EnsureClaimCode()
		}
		if errors.Is(err, auth.ErrClaimed) {
			return exitcode.With(exitcode.Failed, err)
		}
		if err != nil {
			return err
		}
		svc := newService(cmd)
		printClaimBox(cmd.OutOrStdout(), code, svc.Hostnames(), svc.Addresses())
		return nil
	},
}

var authRecoveryCmd = &cobra.Command{
	Use:   "recovery-codes --regenerate",
	Short: "Make 8 new recovery codes; the old codes stop working",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if !authFlags.regenerate {
			return exitcode.With(exitcode.BadInput, errors.New("add --regenerate: the agent keeps only hashes, so it cannot show the old codes"))
		}
		codes, _, err := newAuth().RegenerateCodes()
		if err != nil {
			return cliError(err)
		}
		printCodes(cmd, codes)
		return nil
	},
}

var authTOTPCmd = &cobra.Command{Use: "totp", Short: "Turn TOTP on or off"}

var authTOTPEnrolCmd = &cobra.Command{
	Use:   "enrol",
	Short: "Turn TOTP on: print the secret, then read the first code from the standard input",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		a := newAuth()
		svc := newService(cmd)
		e, err := a.StartTOTP(svc.PrimaryHost())
		if err != nil {
			return cliError(err)
		}
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(w, "secret: %s\nURI:    %s\n", e.Secret, e.URI)
		_, _ = fmt.Fprintln(w, "Add the secret to the authenticator app. Then type the 6-digit code:")
		code, err := readSecret(cmd, "code")
		if err != nil {
			return err
		}
		if err := a.VerifyTOTPEnrolment(auth.Client{Addr: "local", UA: "care-pi CLI"}, strings.TrimSpace(code)); err != nil {
			return cliError(err)
		}
		_, _ = fmt.Fprintln(w, "TOTP is on.")
		return nil
	},
}

var authTOTPDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Turn TOTP off",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := newAuth().DisableTOTP(); err != nil {
			return cliError(err)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "TOTP is off.")
		return nil
	},
}

var authSessionsCmd = &cobra.Command{
	Use:   "sessions [--json]",
	Short: "List the console sessions",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		list := newAuth().Sessions("")
		if authFlags.json {
			return printJSON(cmd.OutOrStdout(), map[string]any{"items": list})
		}
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "REF\tMETHOD\tCREATED\tLAST SEEN\tADDRESS\tUSER AGENT")
		for _, s := range list {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", s.Ref, s.Method, s.CreatedAt.Format(time.RFC3339),
				s.LastSeenAt.Format(time.RFC3339), s.Address, s.UserAgent)
		}
		return tw.Flush()
	},
}

var authSessionsRevokeCmd = &cobra.Command{
	Use:   "revoke REF",
	Short: "End one session",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := newAuth().Revoke(args[0]); err != nil {
			return exitcode.With(exitcode.Failed, err)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "The session ended.")
		return nil
	},
}

var authUnlockCmd = &cobra.Command{
	Use:   "unlock",
	Short: "Remove each lock of the account, the addresses, and the claim",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := newAuth().Unlock(); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Each lock is removed.")
		return nil
	},
}

var authStatusCmd = &cobra.Command{
	Use:   "status [--json]",
	Short: "Show the security settings (no secret)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		a := newAuth()
		st, err := a.Security()
		if err != nil {
			return err
		}
		w := cmd.OutOrStdout()
		if st == nil {
			if authFlags.json {
				return printJSON(w, map[string]any{"claimed": false, "claim_methods": a.ClaimMethods()})
			}
			_, _ = fmt.Fprintf(w, "The device is not claimed. Claim methods: %s\n", strings.Join(a.ClaimMethods(), ", "))
			return nil
		}
		if authFlags.json {
			return printJSON(w, st)
		}
		_, _ = fmt.Fprintf(w, "password set:    %s\n", st.Password.SetAt.Format(time.RFC3339))
		_, _ = fmt.Fprintf(w, "recovery codes:  %d of 8 (made %s)\n", st.RecoveryCodes.Remaining, st.RecoveryCodes.GeneratedAt.Format(time.RFC3339))
		_, _ = fmt.Fprintf(w, "TOTP:            %v\n", st.TOTP.Enabled)
		lockText := "no"
		if st.Lockout.LockedUntil != nil {
			lockText = "until " + st.Lockout.LockedUntil.Format(time.RFC3339)
		}
		_, _ = fmt.Fprintf(w, "account lock:    %s (%d failures in a row)\n", lockText, st.Lockout.ConsecutiveFailures)
		_, _ = fmt.Fprintf(w, "claimed:         %s by %s (%s, %s)\n", st.Claim.ClaimedAt.Format(time.RFC3339), st.Claim.Address, st.Claim.Method, st.Claim.UserAgent)
		_, _ = fmt.Fprintf(w, "setup state:     %s\n", newService(cmd).SetupStateName())
		return nil
	},
}

func init() {
	for _, c := range []*cobra.Command{authSetPasswordCmd, authResetCmd} {
		c.Flags().BoolVar(&authFlags.passwordStdin, "password-stdin", false, "read the password from the standard input")
	}
	authClaimCodeCmd.Flags().BoolVar(&authFlags.rotate, "rotate", false, "make a new claim code")
	authRecoveryCmd.Flags().BoolVar(&authFlags.regenerate, "regenerate", false, "make 8 new recovery codes")
	authSessionsCmd.Flags().BoolVar(&authFlags.json, "json", false, "print JSON")
	authStatusCmd.Flags().BoolVar(&authFlags.json, "json", false, "print JSON")
	authTOTPCmd.AddCommand(authTOTPEnrolCmd, authTOTPDisableCmd)
	authSessionsCmd.AddCommand(authSessionsRevokeCmd)
	authCmd.AddCommand(authSetPasswordCmd, authResetCmd, authClaimCodeCmd, authRecoveryCmd, authTOTPCmd,
		authSessionsCmd, authUnlockCmd, authStatusCmd)
	rootCmd.AddCommand(authCmd)
}
