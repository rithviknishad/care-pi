package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/backup"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/service"
)

var bf struct {
	json, fromTimer, prune, create, wipe, pending, keepKey, keepOld, refresh                        bool
	scope, typ, dest, reason, snapshot, fingerprint, destJSON, accessKey, label, format, out, words string
	subset                                                                                          int
}

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Backups: status, back up now, verify, drill, destinations, disks, and the recovery key",
	Long: `The backups of docs/backups.md. pgBackRest keeps the local repository /var/lib/care-pi/backup/pg;
restic copies it, the files, and /etc/care-pi to each destination of backups.destinations.
As root, these commands need no session and no recent authentication.`,
}

// runJob follows a job of this process until it ends and returns its error.
func runJob(cmd *cobra.Command, svc *service.Service, j *jobs.Job, err error) error {
	if err != nil {
		return cliError(err)
	}
	done, err := followJob(cmd.Context(), cmd.OutOrStdout(), svc, j.ID)
	if err != nil {
		return err
	}
	// The job runs in this process: wait for its AfterDone (restore-dr ends the sessions and puts
	// back the local CA of Caddy after the event done).
	_ = svc.Jobs.Wait(cmd.Context(), j.ID)
	if done != nil && done.State == jobs.StateSucceeded && len(done.Result) > 0 && bf.json {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(done.Result))
	}
	return jobError(done)
}

var backupStatusCmd = &cobra.Command{
	Use:   "status [--json]",
	Short: "Show the backup state (GET /backups)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ov, err := newService(cmd).Backups(cmd.Context())
		if err != nil {
			return cliError(err)
		}
		w := cmd.OutOrStdout()
		if bf.json {
			return printJSON(w, ov)
		}
		l := ov.Local
		_, _ = fmt.Fprintf(w, "local repository: %s (%s key), %d backup(s)", l.State, l.Key, l.BackupCount)
		if l.WindowFrom != nil && l.WindowTo != nil {
			_, _ = fmt.Fprintf(w, ", restore window %s to %s", l.WindowFrom.Format(time.RFC3339), l.WindowTo.Format(time.RFC3339))
		}
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintf(w, "recovery key: %s\n", ov.RecoveryKey.State)
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "DESTINATION\tTYPE\tSTATE\tLAST SNAPSHOT\tLOCATION")
		for _, d := range ov.Destinations {
			last := "-"
			if d.LastSnapshotAt != nil {
				last = d.LastSnapshotAt.Format(time.RFC3339)
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", d.ID, d.Type, d.State, last, d.Location)
		}
		_ = tw.Flush()
		for _, b := range ov.Health.Banners {
			_, _ = fmt.Fprintf(w, "%s: %s (%s)\n", b.Severity, b.Message, b.Code)
		}
		return nil
	},
}

var backupHealthCmd = &cobra.Command{
	Use:   "health [--json]",
	Short: "Show the backup banners (GET /backups/health)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		h, err := newService(cmd).BackupsHealth(cmd.Context())
		if err != nil {
			return cliError(err)
		}
		if bf.json {
			return printJSON(cmd.OutOrStdout(), h)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "state:", h.State)
		for _, b := range h.Banners {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (%s)\n", b.Severity, b.Message, b.Code)
		}
		return nil
	},
}

func source() string {
	if bf.fromTimer {
		return jobs.SourceTimer
	}
	return jobs.SourceCLI
}

var backupRunCmd = &cobra.Command{
	Use:   "run [--scope all|local-db|destination] [--type incr|full] [--destination ID] [--reason TEXT]",
	Short: "Back up now (POST /backups/run)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		svc := newService(cmd)
		if bf.fromTimer && bf.scope == service.ScopeDestination && bf.snapshot != "" {
			// A destination timer: one snapshot and its retention, no job journal.
			return cliError(svc.TimerSnapshot(cmd.Context(), bf.dest, bf.snapshot))
		}
		r := service.BackupRunRequest{Scope: bf.scope, Type: bf.typ, Destination: bf.dest, Reason: bf.reason,
			Snapshot: bf.snapshot, FromTimer: bf.fromTimer}
		if bf.fromTimer && r.Reason == "" {
			r.Reason = "timer"
		}
		j, err := svc.StartBackupRun(cmd.Context(), r, source())
		return runJob(cmd, svc, j, err)
	},
}

var backupVerifyCmd = &cobra.Command{
	Use:   "verify [--scope all|local-db|destination] [--destination ID] [--read-data-subset PCT] [--prune]",
	Short: "Verify the backups now (POST /backups/verify)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		svc := newService(cmd)
		j, err := svc.StartVerify(cmd.Context(), service.VerifyRequest{Scope: bf.scope, Destination: bf.dest,
			SubsetPct: bf.subset, Prune: bf.prune}, source())
		return runJob(cmd, svc, j, err)
	},
}

var backupDrillCmd = &cobra.Command{
	Use:   "drill [--reason TEXT]",
	Short: "Run the restore drill now (POST /backups/drill)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		svc := newService(cmd)
		j, err := svc.StartDrill(cmd.Context(), bf.reason, source(), bf.fromTimer)
		return runJob(cmd, svc, j, err)
	},
}

// ---- destinations ----

var destCmd = &cobra.Command{Use: "destination", Short: "List, test, and set the secrets of the destinations"}

var destListCmd = &cobra.Command{
	Use:   "list [--json]",
	Short: "List the destinations with their state",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		items, err := newService(cmd).Destinations(cmd.Context())
		if err != nil {
			return cliError(err)
		}
		if bf.json {
			return printJSON(cmd.OutOrStdout(), map[string]any{"items": items})
		}
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "ID\tTYPE\tSTATE\tCREDENTIALS\tLOCATION")
		for _, d := range items {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", d.ID, d.Type, d.State, d.Credentials, d.Location)
		}
		return tw.Flush()
	},
}

func readJSONFile(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return exitcode.With(exitcode.BadInput, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return exitcode.With(exitcode.BadInput, fmt.Errorf("%s: %w", path, err))
	}
	return nil
}

var destTestCmd = &cobra.Command{
	Use:   "test ID [--accept-fingerprint SHA256:FP] [--create-bucket] [--destination-json FILE] [--access-key-id KEY --secret-stdin]",
	Short: "Test a destination of config.yaml, or a candidate (POST /backups/destinations/test)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		svc := newService(cmd)
		var d config.BackupDestination
		if bf.destJSON != "" {
			if err := readJSONFile(bf.destJSON, &d); err != nil {
				return err
			}
			d.ID = args[0]
		} else {
			cfg, err := svc.Config()
			if err != nil {
				return cliError(err)
			}
			found := false
			for _, x := range cfg.Backups.Destinations {
				if x.ID == args[0] {
					d, found = x, true
				}
			}
			if !found {
				return exitcode.With(exitcode.BadInput, fmt.Errorf("no destination of config.yaml has the id %s; use --destination-json", args[0]))
			}
		}
		req := service.TestRequest{Destination: d, AcceptFingerprint: bf.fingerprint, CreateBucket: bf.create}
		if bf.accessKey != "" {
			secret, err := readSecret(cmd, "secret access key")
			if err != nil {
				return err
			}
			req.Credentials = &backup.Credentials{AccessKeyID: bf.accessKey, SecretAccessKey: secret}
		}
		res, err := svc.TestDestination(cmd.Context(), req)
		if err != nil {
			return cliError(err)
		}
		if bf.json {
			return printJSON(cmd.OutOrStdout(), res)
		}
		w := cmd.OutOrStdout()
		for _, c := range res.Checks {
			mark := "ok  "
			if !c.OK {
				mark = "FAIL"
				if c.Level == "advised" {
					mark = "warn"
				}
			}
			_, _ = fmt.Fprintf(w, "%s %-12s %s\n", mark, c.Name, c.Detail)
		}
		if res.HostKey != nil {
			_, _ = fmt.Fprintf(w, "host key: %s %s (trusted: %t)\n", res.HostKey.Type, res.HostKey.FingerprintSHA256, res.HostKey.Trusted)
			_, _ = fmt.Fprintf(w, "host_key: %s\n", res.HostKey.PublicKey)
		}
		if res.PublicKey != nil {
			_, _ = fmt.Fprintf(w, "add this line to ~/.ssh/authorized_keys on the server:\n%s\n", *res.PublicKey)
		}
		if res.Location != nil {
			_, _ = fmt.Fprintln(w, "location:", *res.Location)
		}
		_, _ = fmt.Fprintln(w, "next action:", res.NextAction)
		if !res.OK {
			return exitcode.With(exitcode.Failed, errors.New("the test failed"))
		}
		return nil
	},
}

var destSecretsCmd = &cobra.Command{
	Use:   "secrets ID --access-key-id KEY --secret-stdin",
	Short: "Store the S3 keys of a destination (PUT /backups/destinations/{id}/secrets)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if bf.accessKey == "" {
			return exitcode.With(exitcode.BadInput, errors.New("--access-key-id is required"))
		}
		secret, err := readSecret(cmd, "secret access key")
		if err != nil {
			return err
		}
		st, err := newService(cmd).SetDestinationSecrets(args[0], backup.Credentials{AccessKeyID: bf.accessKey, SecretAccessKey: secret})
		if err != nil {
			return cliError(err)
		}
		return printJSON(cmd.OutOrStdout(), st)
	},
}

var destProbeCmd = &cobra.Command{
	Use:    "probe PATH",
	Short:  "Write, read, and delete 1 MiB in PATH (the posix test runs it in a transient unit)",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(backup.ProbePath(args[0]))
	},
}

var sshKeyCmd = &cobra.Command{
	Use:   "ssh-key [--pending]",
	Short: "Print the SSH key of the agent for SFTP destinations (GET /backups/ssh-key)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		which := backup.Active
		if bf.pending {
			which = backup.Pending
		}
		k, err := newService(cmd).SSHKey(which)
		if err != nil {
			return cliError(err)
		}
		if bf.json {
			return printJSON(cmd.OutOrStdout(), k)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), k.AuthorizedKeysLine)
		return nil
	},
}

// ---- disks ----

var diskCmd = &cobra.Command{Use: "disk", Short: "List and prepare USB backup disks"}

var diskListCmd = &cobra.Command{
	Use:   "list [--json]",
	Short: "List the block devices (GET /backups/disks)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		l, err := newService(cmd).Disks(cmd.Context())
		if err != nil {
			return cliError(err)
		}
		if bf.json {
			return printJSON(cmd.OutOrStdout(), l)
		}
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "ID\tDEVICE\tSIZE\tUSAGE\tLABEL")
		for _, d := range l.Items {
			label := "-"
			if d.CareLabel != nil {
				label = *d.CareLabel
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%d MiB\t%s\t%s\n", d.ID, d.Device, d.SizeBytes>>20, d.Usage, label)
		}
		return tw.Flush()
	},
}

var diskPrepareCmd = &cobra.Command{
	Use:   "prepare DISK_ID [--label LABEL] --wipe",
	Short: "Erase a disk and prepare it for backups (POST /backups/disks/{id}/prepare)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !bf.wipe {
			return exitcode.With(exitcode.BadInput, errors.New("the job erases the disk: confirm with --wipe"))
		}
		svc := newService(cmd)
		j, err := svc.StartDiskPrepare(cmd.Context(), args[0], bf.label, jobs.SourceCLI)
		return runJob(cmd, svc, j, err)
	},
}

// ---- recovery key ----

var rkCmd = &cobra.Command{Use: "recovery-key", Short: "Make, show, confirm, and rotate the recovery key"}

func printKey(w io.Writer, k *service.RecoveryKeyView) {
	_, _ = fmt.Fprintf(w, "recovery key %s (%s), key id %s\n", k.Key, k.CreatedAt.Format("2006-01-02"), k.KeyID)
	for i, word := range k.Words {
		_, _ = fmt.Fprintf(w, "%2d %-9s", i+1, word)
		if i%6 == 5 {
			_, _ = fmt.Fprintln(w)
		}
	}
	_, _ = fmt.Fprintln(w, "Write the 24 words on paper and keep them away from the device.")
}

var rkCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Make the recovery key, or the pending key of a rotation (POST /backups/recovery-key)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		k, st, err := newService(cmd).CreateRecoveryKey(cmd.Context())
		if err != nil {
			return cliError(err)
		}
		if bf.json {
			return printJSON(cmd.OutOrStdout(), map[string]any{"key": k, "status": st})
		}
		printKey(cmd.OutOrStdout(), k)
		positions := st.ConfirmPositions
		if k.Key == backup.Pending && st.Pending != nil {
			positions = st.Pending.ConfirmPositions
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Then confirm the words at the positions %v: care-pi backup recovery-key confirm%s\n", positions, pendingFlag(k.Key))
		return nil
	},
}

func pendingFlag(which string) string {
	if which == backup.Pending {
		return " --pending"
	}
	return ""
}

var rkStatusCmd = &cobra.Command{
	Use:   "status [--json]",
	Short: "Show the state of the recovery key",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		st := newService(cmd).RecoveryKeyStatus()
		if bf.json {
			return printJSON(cmd.OutOrStdout(), st)
		}
		id := "-"
		if st.KeyID != nil {
			id = *st.KeyID
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "state %s, key id %s, local repository %s\n", st.State, id, st.LocalRepository)
		if st.ConfirmPositions != nil {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "confirm the words at the positions %v\n", st.ConfirmPositions)
		}
		if st.Pending != nil {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "pending key %s, confirmed: %t\n", st.Pending.KeyID, st.Pending.ConfirmedAt != nil)
		}
		return nil
	},
}

var rkShowCmd = &cobra.Command{
	Use:   "show [--pending] [--format words|txt|json] [--out FILE]",
	Short: "Show the recovery key (GET /backups/recovery-key, GET /backups/recovery-key/download)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		svc := newService(cmd)
		which := backup.Active
		if bf.pending {
			which = backup.Pending
		}
		w := cmd.OutOrStdout()
		if bf.out != "" {
			f, err := os.OpenFile(bf.out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()
			w = f
		}
		switch bf.format {
		case "txt":
			text, _, err := svc.RecoveryKeyText(which)
			if err != nil {
				return cliError(err)
			}
			_, err = io.WriteString(w, text)
			return err
		case "json":
			k, err := svc.ShowRecoveryKey(which)
			if err != nil {
				return cliError(err)
			}
			return printJSON(w, k)
		}
		k, err := svc.ShowRecoveryKey(which)
		if err != nil {
			return cliError(err)
		}
		printKey(w, k)
		return nil
	},
}

// parseWords reads "3=able,11=acce,20=act".
func parseWords(s string) ([]backup.WordInput, error) {
	var out []backup.WordInput
	for _, part := range strings.Split(s, ",") {
		i, w, ok := strings.Cut(strings.TrimSpace(part), "=")
		n, err := strconv.Atoi(i)
		if !ok || err != nil {
			return nil, exitcode.With(exitcode.BadInput, fmt.Errorf("use --words POS=WORD,POS=WORD,POS=WORD"))
		}
		out = append(out, backup.WordInput{Index: n, Word: strings.ToLower(strings.TrimSpace(w))})
	}
	return out, nil
}

var rkConfirmCmd = &cobra.Command{
	Use:   "confirm [--pending] [--words POS=WORD,...]",
	Short: "Type 3 words of the recovery key again (POST /backups/recovery-key/confirm)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		svc := newService(cmd)
		which := backup.Active
		if bf.pending {
			which = backup.Pending
		}
		st := svc.RecoveryKeyStatus()
		positions := st.ConfirmPositions
		if which == backup.Pending && st.Pending != nil {
			positions = st.Pending.ConfirmPositions
		}
		var words []backup.WordInput
		if bf.words != "" {
			ws, err := parseWords(bf.words)
			if err != nil {
				return err
			}
			words = ws
		} else {
			if positions == nil {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "The key is confirmed.")
				return nil
			}
			r := bufio.NewReader(cmd.InOrStdin())
			for _, p := range positions {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "word %d: ", p)
				line, err := r.ReadString('\n')
				if err != nil && !errors.Is(err, io.EOF) {
					return err
				}
				words = append(words, backup.WordInput{Index: p, Word: strings.ToLower(strings.TrimSpace(line))})
			}
		}
		out, err := svc.ConfirmRecoveryKey(which, words, "/words")
		if err != nil {
			return cliError(err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "The recovery key is %s.\n", out.State)
		return nil
	},
}

var rkRotateCmd = &cobra.Command{
	Use:   "rotate [--keep-key] [--keep-old-repositories] [--reason TEXT]",
	Short: "Use the pending key, or re-key a legacy local repository with --keep-key",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		svc := newService(cmd)
		j, err := svc.StartRotate(cmd.Context(), service.RotateRequest{NewKey: !bf.keepKey, RemoveOld: !bf.keepOld, Reason: bf.reason}, jobs.SourceCLI)
		return runJob(cmd, svc, j, err)
	},
}

func init() {
	for _, c := range []*cobra.Command{backupStatusCmd, backupHealthCmd, destListCmd, destTestCmd, diskListCmd, rkCreateCmd,
		rkStatusCmd, sshKeyCmd, backupRunCmd, backupVerifyCmd, backupDrillCmd} {
		c.Flags().BoolVar(&bf.json, "json", false, "print JSON")
	}
	for _, c := range []*cobra.Command{backupRunCmd, backupVerifyCmd, backupDrillCmd} {
		c.Flags().BoolVar(&bf.fromTimer, "from-timer", false, "the run of a timer (care-backup-*.service)")
		_ = c.Flags().MarkHidden("from-timer")
		c.Flags().StringVar(&bf.reason, "reason", "", "a text for the job journal")
	}
	for _, c := range []*cobra.Command{backupRunCmd, backupVerifyCmd} {
		c.Flags().StringVar(&bf.scope, "scope", "", "all, local-db, or destination")
		c.Flags().StringVar(&bf.dest, "destination", "", "the destination id")
	}
	backupRunCmd.Flags().StringVar(&bf.typ, "type", "incr", "incr or full (the local backup)")
	backupRunCmd.Flags().StringVar(&bf.snapshot, "snapshot", "", "db or files only (the timers)")
	_ = backupRunCmd.Flags().MarkHidden("snapshot")
	backupVerifyCmd.Flags().IntVar(&bf.subset, "read-data-subset", 5, "the percent of the data that restic check reads")
	backupVerifyCmd.Flags().BoolVar(&bf.prune, "prune", false, "run restic forget and prune first")
	destTestCmd.Flags().StringVar(&bf.fingerprint, "accept-fingerprint", "", "the SFTP host key that you accept (SHA256:...)")
	destTestCmd.Flags().BoolVar(&bf.create, "create-bucket", false, "create the S3 bucket when it does not exist")
	destTestCmd.Flags().StringVar(&bf.destJSON, "destination-json", "", "a destination object (JSON) instead of config.yaml")
	for _, c := range []*cobra.Command{destTestCmd, destSecretsCmd} {
		c.Flags().StringVar(&bf.accessKey, "access-key-id", "", "the S3 access key id; the secret comes from the standard input")
		c.Flags().Bool("secret-stdin", true, "read the secret access key from the standard input")
	}
	sshKeyCmd.Flags().BoolVar(&bf.pending, "pending", false, "the key of the pending recovery key")
	rkShowCmd.Flags().BoolVar(&bf.pending, "pending", false, "the pending key of a rotation")
	rkShowCmd.Flags().StringVar(&bf.format, "format", "words", "words, txt, or json")
	rkShowCmd.Flags().StringVar(&bf.out, "out", "", "write to a file (mode 0600)")
	rkConfirmCmd.Flags().BoolVar(&bf.pending, "pending", false, "the pending key of a rotation")
	rkConfirmCmd.Flags().StringVar(&bf.words, "words", "", "POS=WORD,POS=WORD,POS=WORD (else the command asks)")
	rkRotateCmd.Flags().BoolVar(&bf.keepKey, "keep-key", false, "keep the key; re-key only a legacy local repository")
	rkRotateCmd.Flags().BoolVar(&bf.keepOld, "keep-old-repositories", false, "do not delete the old repositories")
	rkRotateCmd.Flags().StringVar(&bf.reason, "reason", "", "a text for the job journal")
	diskPrepareCmd.Flags().StringVar(&bf.label, "label", "", "the file system label (care-...)")
	diskPrepareCmd.Flags().BoolVar(&bf.wipe, "wipe", false, "confirm that the job erases the disk")
	addLockFlag(diskPrepareCmd)
	addLockFlag(rkRotateCmd)

	destCmd.AddCommand(destListCmd, destTestCmd, destSecretsCmd, destProbeCmd)
	diskCmd.AddCommand(diskListCmd, diskPrepareCmd)
	rkCmd.AddCommand(rkCreateCmd, rkStatusCmd, rkShowCmd, rkConfirmCmd, rkRotateCmd)
	backupCmd.AddCommand(backupStatusCmd, backupHealthCmd, backupRunCmd, backupVerifyCmd, backupDrillCmd, destCmd, sshKeyCmd, diskCmd, rkCmd)
	rootCmd.AddCommand(backupCmd)
}
