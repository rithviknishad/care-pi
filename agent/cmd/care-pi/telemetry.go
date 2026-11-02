package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

var metricsCmd = &cobra.Command{
	Use:   "metrics",
	Short: "Print the metrics of the device in the Prometheus text format (GET /api/v1/metrics, Phase 7)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		set, err := newService(cmd).Metrics(cmd.Context())
		if err != nil {
			return cliError(err)
		}
		_, err = set.WriteTo(cmd.OutOrStdout())
		return err
	},
}

var telemetryCmd = &cobra.Command{
	Use:   "telemetry",
	Short: "The opt-in telemetry: vmagent and node_exporter with remote write (docs/telemetry.md)",
}

var telemetryDeletePassword bool

var telemetrySetPasswordCmd = &cobra.Command{
	Use:   "set-password [--delete]",
	Short: "Read the basic auth password of the remote write from the standard input, then apply",
	Long: `set-password writes /etc/care-pi/secrets/telemetry_password (0600) from the first line of the standard input.
The apply gives it to vmagent as the podman secret care-pi-telemetry-password. config.yaml has only
telemetry.username. --delete deletes the file.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		f := rootPaths().Abs(paths.TelemetryPasswordFile)
		if telemetryDeletePassword {
			if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "deleted "+paths.TelemetryPasswordFile)
			return runApply(cmd, "telemetry password deleted")
		}
		pw, err := readSecret(cmd, "password")
		if err != nil {
			return err
		}
		if strings.TrimSpace(pw) != pw {
			return exitcode.With(exitcode.BadInput, fmt.Errorf("the password starts or ends with a space"))
		}
		if err := lock.WriteFileAtomic(f, []byte(pw), 0o600); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "wrote "+paths.TelemetryPasswordFile)
		return runApply(cmd, "telemetry password")
	},
}

func init() {
	telemetrySetPasswordCmd.Flags().BoolVar(&telemetryDeletePassword, "delete", false, "delete the password file")
	telemetryCmd.AddCommand(telemetrySetPasswordCmd)
	rootCmd.AddCommand(metricsCmd, telemetryCmd)
}
