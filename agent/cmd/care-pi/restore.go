package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/backup"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/restore"
	"github.com/rithviknishad/care-pi/agent/internal/service"
)

var rf struct {
	time, checkpoint, dest, snapshot, destJSON, reason string
	paths                                              []string
	sideDir, inspect, keyStdin, noFiles, pause, json   bool
	refresh                                            bool
}

var restoreCmd = &cobra.Command{
	Use:   "restore",
	Short: "Restore on this device (pitr, files) or rebuild a new device from a backup (dr)",
}

var restorePointsCmd = &cobra.Command{
	Use:   "points [--json] [--refresh]",
	Short: "Show the restore window and the restore points (GET /restore/points)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		p, err := newService(cmd).Points(cmd.Context(), rf.refresh)
		if err != nil {
			return cliError(err)
		}
		w := cmd.OutOrStdout()
		if rf.json {
			return printJSON(w, p)
		}
		if p.PITR != nil {
			_, _ = fmt.Fprintf(w, "restore window: %s to %s (timeline %d)\n", p.PITR.From.Format(time.RFC3339), p.PITR.To.Format(time.RFC3339), p.PITR.Timeline)
		}
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "BACKUP\tTYPE\tEND\tREASON")
		for _, b := range p.DBBackups {
			r := ""
			if b.Reason != nil {
				r = *b.Reason
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", b.Label, b.Type, b.FinishedAt.Format(time.RFC3339), r)
		}
		_ = tw.Flush()
		for _, c := range p.Checkpoints {
			_, _ = fmt.Fprintf(w, "checkpoint %s (undo point %s)\n", c.Label, c.UndoPoint.Format(time.RFC3339))
		}
		tw = tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "DESTINATION\tSNAPSHOT\tTIME\tCONTENTS")
		for _, s := range p.FileSnapshots {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%v\n", *s.Destination, s.SnapshotID, s.Time.Format(time.RFC3339), s.Contents)
		}
		return tw.Flush()
	},
}

var restorePITRCmd = &cobra.Command{
	Use:   "pitr --time TIME | --checkpoint LABEL [--reason TEXT]",
	Short: "Restore the database of this device to a point in time (POST /restore, kind pitr)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if (rf.time == "") == (rf.checkpoint == "") {
			return exitcode.With(exitcode.BadInput, errors.New("use exactly one of --time and --checkpoint"))
		}
		r := service.RestoreRequest{Kind: "pitr", Checkpoint: rf.checkpoint, Reason: rf.reason}
		if rf.time != "" {
			t, err := time.Parse(time.RFC3339, rf.time)
			if err != nil {
				return exitcode.With(exitcode.BadInput, fmt.Errorf("--time: use RFC 3339, for example 2026-10-30T09:40:00Z: %w", err))
			}
			t = t.UTC()
			r.Time = &t
		}
		svc := newService(cmd)
		j, err := svc.StartRestore(cmd.Context(), r, jobs.SourceCLI)
		return runJob(cmd, svc, j, err)
	},
}

var restoreFilesCmd = &cobra.Command{
	Use:   "files --destination ID --snapshot ID [--path PATH]... [--side-dir]",
	Short: "Restore the files of one snapshot (POST /restore, kind files)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if rf.dest == "" || rf.snapshot == "" {
			return exitcode.With(exitcode.BadInput, errors.New("--destination and --snapshot are required"))
		}
		target := restore.InPlace
		if rf.sideDir {
			target = restore.SideDir
		}
		svc := newService(cmd)
		j, err := svc.StartRestore(cmd.Context(), service.RestoreRequest{Kind: "files", Destination: rf.dest, SnapshotID: rf.snapshot,
			Paths: rf.paths, Target: target, Reason: rf.reason}, jobs.SourceCLI)
		return runJob(cmd, svc, j, err)
	},
}

// drDestination is the destination file of "restore dr" (schema DrDestination).
type drDestination struct {
	config.BackupDestination
	AccessKeyID     string `json:"access_key_id,omitempty"`
	SecretAccessKey string `json:"secret_access_key,omitempty"`
}

var restoreDRCmd = &cobra.Command{
	Use:   "dr [--inspect] --destination-json FILE --recovery-key-stdin [--time TIME] [--snapshot ID] [--no-files] [--pause-backups]",
	Short: "Rebuild this new device from one destination and the recovery key (POST /restore/dr)",
	Long: `The branch "Restore from backup" of the setup wizard. The destination file is a JSON object:
{"type":"sftp","host":"nas.lan","user":"care","path":"/volume1/care","host_key":"ssh-ed25519 AAAA..."},
{"type":"posix","disk_label":"care-usb1"}, or {"type":"s3","endpoint":...,"bucket":...,"access_key_id":...,"secret_access_key":...}.
The standard input has the 24 words, the text file of the key, or the QR payload.
After the job, log in to the console with the console password of the old device.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if rf.destJSON == "" || !rf.keyStdin {
			return exitcode.With(exitcode.BadInput, errors.New("--destination-json and --recovery-key-stdin are required"))
		}
		var dd drDestination
		if err := readJSONFile(rf.destJSON, &dd); err != nil {
			return err
		}
		raw, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return err
		}
		k, err := backup.ParseText(string(raw))
		if err != nil {
			return exitcode.With(exitcode.BadInput, err)
		}
		d := dd.BackupDestination
		d.ID = "restore"
		if err := service.CheckDestination(d, "/destination"); err != nil {
			return cliError(err)
		}
		d.ID = ""
		r := service.DRRequest{Key: k, Destination: d, SnapshotID: rf.snapshot, Files: !rf.noFiles, PauseBackups: rf.pause}
		if d.Type == backup.TypeS3 {
			r.Credentials = &backup.Credentials{AccessKeyID: dd.AccessKeyID, SecretAccessKey: dd.SecretAccessKey}
		}
		if rf.time != "" {
			t, err := time.Parse(time.RFC3339, rf.time)
			if err != nil {
				return exitcode.With(exitcode.BadInput, err)
			}
			t = t.UTC()
			r.Time = &t
		}
		svc := newService(cmd)
		if rf.inspect {
			ins, err := svc.InspectDR(cmd.Context(), r)
			if err != nil {
				return cliError(err)
			}
			return printJSON(cmd.OutOrStdout(), ins)
		}
		j, err := svc.StartDR(cmd.Context(), r, jobs.SourceCLI)
		if err := runJob(cmd, svc, j, err); err != nil {
			return err
		}
		if done, err := jobs.Load(svc.Paths, j.ID); err == nil && len(done.Result) > 0 {
			var res restore.DRResult
			if json.Unmarshal(done.Result, &res) == nil {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Restored: %s (%s)\nCare: %s\nConsole: %s (log in with the console password of the old device)\n", res.DisplayName, res.Hostname, res.CareURL, res.ConsoleURL)
			}
		}
		return nil
	},
}

func init() {
	restorePointsCmd.Flags().BoolVar(&rf.json, "json", false, "print JSON")
	restorePointsCmd.Flags().BoolVar(&rf.refresh, "refresh", false, "list the snapshots of each destination now")
	restorePITRCmd.Flags().StringVar(&rf.time, "time", "", "the point in time (RFC 3339)")
	restorePITRCmd.Flags().StringVar(&rf.checkpoint, "checkpoint", "", "the label of a checkpoint (undo a restore)")
	for _, c := range []*cobra.Command{restorePITRCmd, restoreFilesCmd} {
		c.Flags().StringVar(&rf.reason, "reason", "", "a text for the job journal")
		addLockFlag(c)
	}
	restoreFilesCmd.Flags().StringVar(&rf.dest, "destination", "", "the destination id")
	restoreFilesCmd.Flags().StringVar(&rf.snapshot, "snapshot", "", "the snapshot id")
	restoreFilesCmd.Flags().StringArrayVar(&rf.paths, "path", nil, "a file or a directory below /var/lib/care-pi/s3 (the first part is the bucket)")
	restoreFilesCmd.Flags().BoolVar(&rf.sideDir, "side-dir", false, "restore into /var/lib/care-pi/restore/<job id>/ and change nothing else")
	restoreDRCmd.Flags().BoolVar(&rf.inspect, "inspect", false, "only read what the destination holds (POST /restore/dr/inspect)")
	restoreDRCmd.Flags().StringVar(&rf.destJSON, "destination-json", "", "the destination (JSON file)")
	restoreDRCmd.Flags().BoolVar(&rf.keyStdin, "recovery-key-stdin", false, "read the recovery key from the standard input")
	restoreDRCmd.Flags().StringVar(&rf.time, "time", "", "the point in time of the database (default: the newest)")
	restoreDRCmd.Flags().StringVar(&rf.snapshot, "snapshot", "", "the db snapshot (default: the newest)")
	restoreDRCmd.Flags().BoolVar(&rf.noFiles, "no-files", false, "do not restore the files")
	restoreDRCmd.Flags().BoolVar(&rf.pause, "pause-backups", false, "write backups.paused: true (a test on a second device)")
	addLockFlag(restoreDRCmd)
	restoreCmd.AddCommand(restorePointsCmd, restorePITRCmd, restoreFilesCmd, restoreDRCmd)
	rootCmd.AddCommand(restoreCmd)
}
