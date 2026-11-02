package main

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/service"
)

var doctorJSON bool

var doctorCmd = &cobra.Command{
	Use:   "doctor [--json]",
	Short: "Run the health checks and print each with a fix hint (GET /api/v1/doctor, Phase 7)",
	Long: `doctor checks the host (the memory controller, podman), the ports 80, 443, and 8443 (another program on a
published address, for example "tailscale serve"), the mDNS name (a collision), the clock, the memory and zram, the
disks, the units of Care, the certificates, the age of the last backup of each destination, the firewall, a pending
reboot, and the updates. It changes nothing. Exit code 1 when a check failed.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		rep, err := newService(cmd).Doctor(cmd.Context())
		if err != nil {
			return cliError(err)
		}
		w := cmd.OutOrStdout()
		if doctorJSON {
			if err := printJSON(w, rep); err != nil {
				return err
			}
		} else {
			tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "STATUS\tCHECK\tDETAIL")
			for _, c := range rep.Checks {
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", strings.ToUpper(c.Status), c.Title, c.Detail)
			}
			_ = tw.Flush()
			for _, c := range rep.Checks {
				if c.Fix != nil {
					_, _ = fmt.Fprintf(w, "fix %s: %s\n", c.ID, *c.Fix)
				}
			}
			_, _ = fmt.Fprintf(w, "status: %s\n", rep.Status)
		}
		if rep.Status == service.DoctorFail {
			return exitcode.With(exitcode.Failed, fmt.Errorf("a check failed"))
		}
		return nil
	},
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "print JSON (schema DoctorReport)")
	rootCmd.AddCommand(doctorCmd)
}
