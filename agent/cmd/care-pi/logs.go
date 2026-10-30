package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/service"
)

var logsFlags struct {
	follow bool
	lines  int
	since  string
	json   bool
}

var logsCmd = &cobra.Command{
	Use:   "logs UNIT [-n N] [--since TIME] [-f] [--json]",
	Short: "Show the journal of one unit (api, worker, beat, db, redis, s3, caddy, pod, clock, fe-build, target, agent)",
	Long: `logs runs journalctl for one unit. UNIT can be a short name (api), a unit
name (care-api), or a full unit name (care-api.service). agent is care-pi.service.
--since takes an RFC 3339 time (2026-10-30T10:15:00Z) or a journalctl time ("-1h", "today").
--json prints one page (the LogPage of the API) or, with -f, one JSON line for each line.
Without --json, the exit code is the exit code of journalctl.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		unit, err := unitName(args[0])
		if err != nil {
			return exitcode.With(exitcode.BadInput, err)
		}
		f := logsFlags
		if !f.json {
			jargs := []string{"--unit", unit, "--no-pager"}
			if f.lines > 0 {
				jargs = append(jargs, "--lines", fmt.Sprint(f.lines))
			}
			if f.since != "" {
				jargs = append(jargs, "--since", f.since)
			}
			if f.follow {
				jargs = append(jargs, "--follow")
			}
			_, err := newHost(cmd).Runner.Run(cmd.Context(), host.Cmd{Name: "journalctl", Args: jargs, Attach: true})
			return passthrough(err)
		}
		q := service.LogQuery{N: f.lines}
		if f.since != "" {
			t, err := time.Parse(time.RFC3339, f.since)
			if err != nil {
				return exitcode.With(exitcode.BadInput, fmt.Errorf("--since with --json needs an RFC 3339 time: %w", err))
			}
			q.Since = &t
		}
		svc := newService(cmd)
		w := cmd.OutOrStdout()
		if f.follow {
			enc := json.NewEncoder(w)
			return cliError(svc.FollowLogs(cmd.Context(), args[0], q, func(l service.LogLine) error { return enc.Encode(l) }))
		}
		page, err := svc.Logs(cmd.Context(), args[0], q)
		if err != nil {
			return cliError(err)
		}
		return printJSON(w, page)
	},
}

// unitName maps a short name to a care-pi unit name.
func unitName(s string) (string, error) {
	if u, ok := service.UnitOf(s); ok {
		return u, nil
	}
	var names []string
	for _, u := range service.LogUnits {
		names = append(names, u.Name)
	}
	return "", fmt.Errorf("unknown unit %q; use %s", s, strings.Join(names, ", "))
}

func init() {
	f := logsCmd.Flags()
	f.BoolVarP(&logsFlags.follow, "follow", "f", false, "follow the journal")
	f.IntVarP(&logsFlags.lines, "lines", "n", 100, "number of lines to show")
	f.StringVar(&logsFlags.since, "since", "", "only lines at or after this time")
	f.BoolVar(&logsFlags.json, "json", false, "print JSON")
	rootCmd.AddCommand(logsCmd)
}
