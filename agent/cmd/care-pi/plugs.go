package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/service"
	"github.com/rithviknishad/care-pi/agent/internal/update"
)

var plugsFlags struct {
	json        bool
	enabled     string
	settings    []string
	secretStdin string
	dryRun      bool
	apply       bool
	reason      string
}

var plugsCmd = &cobra.Command{
	Use:   "plugs",
	Short: "List the plugs of the solution, turn them on or off, and change their settings",
}

var plugsListCmd = &cobra.Command{
	Use:   "list [--json]",
	Short: "List the plugs (GET /plugs)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		l, err := newService(cmd).Plugs(cmd.Context())
		if err != nil {
			return cliError(err)
		}
		w := cmd.OutOrStdout()
		if plugsFlags.json {
			return printJSON(w, l)
		}
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "ID\tKIND\tENABLED\tON\tSOURCE\tPENDING\tSETTINGS")
		for _, p := range l.Items {
			set := "complete"
			if !p.SettingsComplete {
				set = "incomplete"
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%v\t%v\t%s\t%v\t%s\n", p.ID, p.Kind, p.Enabled, p.On, p.Source, p.Pending, set)
		}
		return tw.Flush()
	},
}

var plugsSchemaCmd = &cobra.Command{
	Use:   "schema PLUG_ID",
	Short: "Print the JSON Schema of the settings of a plug",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := newService(cmd).PlugSchema(args[0])
		if err != nil {
			return cliError(err)
		}
		return printJSON(cmd.OutOrStdout(), m)
	},
}

// parseSetting reads PART.KEY=VALUE. VALUE is YAML: true, 3, [cash, debc]; null removes the operator value.
func parseSetting(s string) (part, key string, value any, err error) {
	kv, val, ok := strings.Cut(s, "=")
	if !ok {
		return "", "", nil, fmt.Errorf("--setting %q: use PART.KEY=VALUE", s)
	}
	part, key, ok = strings.Cut(kv, ".")
	if !ok || (part != "backend" && part != "frontend") || key == "" {
		return "", "", nil, fmt.Errorf("--setting %q: PART is backend or frontend", s)
	}
	if err := yaml.Unmarshal([]byte(val), &value); err != nil {
		return "", "", nil, fmt.Errorf("--setting %q: the value is not valid YAML: %w", s, err)
	}
	// Keep the JSON types of the API.
	data, err := json.Marshal(value)
	if err != nil {
		return "", "", nil, fmt.Errorf("--setting %q: %w", s, err)
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return "", "", nil, err
	}
	return part, key, value, nil
}

var plugsSetCmd = &cobra.Command{
	Use:   "set PLUG_ID [--enabled=true|false|default] [--setting PART.KEY=VALUE]... [--secret-stdin PART.KEY] [--dry-run] [--apply]",
	Short: "Turn a plug on or off, or change its settings (PUT /plugs/{plug_id})",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		f := plugsFlags
		ch := update.PlugChange{Reason: f.reason}
		switch f.enabled {
		case "":
		case "true", "false":
			v := f.enabled == "true"
			ch.SetEnabled, ch.Enabled = true, &v
		case "default":
			ch.SetEnabled = true
		default:
			return exitcode.With(exitcode.BadInput, fmt.Errorf("--enabled: use true, false, or default"))
		}
		add := func(part, key string, v any) {
			m := &ch.Backend
			if part == "frontend" {
				m = &ch.Frontend
			}
			if *m == nil {
				*m = map[string]any{}
			}
			(*m)[key] = v
		}
		for _, s := range f.settings {
			part, key, v, err := parseSetting(s)
			if err != nil {
				return exitcode.With(exitcode.BadInput, err)
			}
			add(part, key, v)
		}
		if f.secretStdin != "" {
			part, key, ok := strings.Cut(f.secretStdin, ".")
			if !ok || part != "backend" {
				return exitcode.With(exitcode.BadInput, fmt.Errorf("--secret-stdin: use backend.KEY"))
			}
			v, err := readSecret(cmd, "secret setting")
			if err != nil {
				return err
			}
			add(part, key, v)
		}
		svc := newService(cmd)
		res, err := svc.SetPlug(cmd.Context(), service.PlugWrite{ID: args[0], Change: ch, DryRun: f.dryRun, Apply: f.apply, Source: jobs.SourceCLI})
		if err != nil {
			return cliError(err)
		}
		w := cmd.OutOrStdout()
		if plugsFlags.json {
			if err := printJSON(w, res); err != nil {
				return err
			}
		} else {
			_, _ = fmt.Fprintf(w, "plug %s: enabled %v, path %s, steps %s\n", res.Plug.ID, res.Plug.Enabled, res.Plan.Path, strings.Join(res.Plan.Steps, " "))
			for _, warn := range res.Plan.Warnings {
				_, _ = fmt.Fprintln(w, "warning:", warn)
			}
			switch {
			case !res.Written:
				_, _ = fmt.Fprintln(w, "dry run: nothing was written")
			case res.Job == nil && res.Plan.Changed:
				_, _ = fmt.Fprintln(w, "written; run care-pi plugs set --apply, or care-pi apply, to apply it")
			}
		}
		if res.Job != nil {
			return follow(cmd, svc, res.Job)
		}
		return nil
	},
}

func init() {
	plugsListCmd.Flags().BoolVar(&plugsFlags.json, "json", false, "print JSON")
	f := plugsSetCmd.Flags()
	f.StringVar(&plugsFlags.enabled, "enabled", "", "true, false, or default (follow the solution)")
	f.StringArrayVar(&plugsFlags.settings, "setting", nil, "a setting PART.KEY=VALUE (VALUE is YAML; null removes the value)")
	f.StringVar(&plugsFlags.secretStdin, "secret-stdin", "", "read the value of the secret setting backend.KEY from the standard input")
	f.BoolVar(&plugsFlags.dryRun, "dry-run", false, "validate and show the plan; write nothing")
	f.BoolVar(&plugsFlags.apply, "apply", false, "after the write, start a plug-change job")
	f.StringVar(&plugsFlags.reason, "reason", "", "a text for the job journal and the history")
	f.BoolVar(&plugsFlags.json, "json", false, "print JSON")
	addLockFlag(plugsSetCmd)
	plugsCmd.AddCommand(plugsListCmd, plugsSchemaCmd, plugsSetCmd)
	rootCmd.AddCommand(plugsCmd)
}
