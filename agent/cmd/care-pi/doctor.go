package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/service"
)

var doctorJSON bool

var doctorCmd = &cobra.Command{
	Use:   "doctor [--json]",
	Short: "Run the health checks and print each with a fix hint (GET /api/v1/doctor, Phase 7)",
	Long: `doctor checks the host (the memory controller, podman), the ports 80, 443, and 8443 (another program on a
published address, for example "tailscale serve"), the mDNS name (a collision), the internet (one check, at most 5 s),
the clock, the memory and zram, the disks, the units of Care, the certificates, the age of the last backup of each
destination, the firewall, a pending reboot, and the updates. It changes nothing. Exit code 1 when a check failed.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		svc := newService(cmd)
		// The CLI has no background internet check (S74): check once.
		svc.Internet = &service.Internet{}
		svc.Internet.CheckWithin(cmd.Context(), service.InternetProbeTimeout)
		rep, err := svc.Doctor(cmd.Context())
		if err != nil {
			return cliError(err)
		}
		w := cmd.OutOrStdout()
		if doctorJSON {
			if err := printJSON(w, rep); err != nil {
				return err
			}
		} else {
			service.WriteDoctorText(w, rep)
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
