package main

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/hostcheck"
	"github.com/rithviknishad/care-pi/agent/internal/service"
)

var hostCheckJSON, hostCheckProbe bool

var hostCmd = &cobra.Command{
	Use:   "host",
	Short: "Host commands",
}

var hostCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Report the host prerequisites; change nothing",
	Long: `check reports the prerequisites that the installer provides: podman 5.4 or
later with quadlet, skopeo, the cgroup memory controller, zram swap, avahi,
nftables, and the directories. It also reports the hardware (RAM, root disk,
page size, RTC, ethernet, internet, clock) as advised checks. --json prints the
HostCheck object of GET /api/v1/host/check. --probe measures the root disk (about 10 s).
check always runs the internet check once (at most 5 s): an HTTPS request to
github.com, then to 1.1.1.1, which tells "no DNS" from "no route".
check changes nothing.
Exit code 0: every required check passes. Exit code 3: a required check fails.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		svc := newService(cmd)
		// The CLI has no background check (S74): HostCheckView checks the internet once.
		svc.Internet = &service.Internet{}
		hc := svc.HostCheckView(cmd.Context(), hostCheckProbe)
		if hostCheckJSON {
			if err := printJSON(cmd.OutOrStdout(), hc); err != nil {
				return err
			}
		} else {
			r := hostcheck.Report{Checks: hc.Checks}
			r.WriteText(cmd.OutOrStdout())
		}
		if !hc.OK {
			return &exitcode.Error{Code: exitcode.HostMissing, Err: errors.New("a required host prerequisite is missing")}
		}
		return nil
	},
}

func init() {
	hostCheckCmd.Flags().BoolVar(&hostCheckJSON, "json", false, "print JSON")
	hostCheckCmd.Flags().BoolVar(&hostCheckProbe, "probe", false, "measure the write and read speed of the root disk (about 10 s) and check the internet again")
	hostCmd.AddCommand(hostCheckCmd)
	rootCmd.AddCommand(hostCmd)
}
