package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
)

var logsFlags struct {
	follow bool
	lines  int
}

var logsCmd = &cobra.Command{
	Use:   "logs UNIT",
	Short: "Show the journal of one unit (api, worker, beat, db, redis, s3, caddy, pod, clock)",
	Long: `logs runs journalctl for one unit. UNIT can be a short name (api), a unit
name (care-api), or a full unit name (care-api.service). The exit code is the
exit code of journalctl.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		unit, err := unitName(args[0])
		if err != nil {
			return exitcode.With(exitcode.BadInput, err)
		}
		return passthrough(newHost(cmd).Journal.Show(cmd.Context(), unit, logsFlags.follow, logsFlags.lines))
	},
}

// unitName maps a short name to a care-pi unit name.
func unitName(s string) (string, error) {
	s = strings.TrimSuffix(s, ".service")
	switch s {
	case "target", "care", "care.target":
		return "care.target", nil
	}
	s = strings.TrimPrefix(s, "care-")
	switch s {
	case "api", "worker", "beat", "db", "redis", "s3", "caddy", "pod", "clock", "fe-build":
		return "care-" + s + ".service", nil
	}
	return "", fmt.Errorf("unknown unit %q; use api, worker, beat, db, redis, s3, caddy, pod, clock, fe-build, or target", s)
}

func init() {
	f := logsCmd.Flags()
	f.BoolVarP(&logsFlags.follow, "follow", "f", false, "follow the journal")
	f.IntVarP(&logsFlags.lines, "lines", "n", 100, "number of lines to show")
	rootCmd.AddCommand(logsCmd)
}
