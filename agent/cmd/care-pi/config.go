package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/service"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Work with config.yaml",
}

var configValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate config.yaml against the schema",
	Long:  "validate checks config.yaml against the schema. Exit code 0: valid. Exit code 2: not valid.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		p := configPath(cmd)
		if _, err := config.Load(p); err != nil {
			return exitcode.With(exitcode.BadInput, err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s is valid\n", p)
		return nil
	},
}

var configSchemaCmd = &cobra.Command{
	Use:   "schema",
	Short: "Print the JSON Schema of config.yaml",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		_, err := cmd.OutOrStdout().Write(config.SchemaJSON())
		return err
	},
}

func init() {
	configCmd.AddCommand(configValidateCmd, configSchemaCmd)
	rootCmd.AddCommand(configCmd)
}

var configFlags struct {
	json   bool
	file   bool
	dryRun bool
	apply  bool
}

var configShowCmd = &cobra.Command{
	Use:   "show [--json] [--file]",
	Short: "Print config.yaml with the defaults (or only the keys of the file with --file)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		view := "effective"
		if configFlags.file {
			view = "file"
		}
		doc, etag, err := newService(cmd).ConfigView(view)
		if err != nil {
			return cliError(err)
		}
		w := cmd.OutOrStdout()
		if configFlags.json {
			return printJSON(w, doc)
		}
		out, err := yaml.Marshal(doc)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(w, "# ETag %s\n%s", etag, out)
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set KEY=VALUE... [--dry-run] [--apply]",
	Short: "Change keys of config.yaml, print the plan, and write the file",
	Long: `set changes the keys of config.yaml. KEY is a dotted path, for example
identity.hostname or env.backend.EMAIL_PORT. VALUE is YAML: a string, a number,
true or false, or JSON for a list or a map. "null" deletes the key.
set validates the result and prints the plan of an apply. --dry-run writes
nothing. --apply also runs "care-pi apply" after the write.
The file loses its comments and its key order.
During the setup, set also marks the step of the key as done (identity, tls, size, env).`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		svc := newService(cmd)
		doc, _, err := svc.ConfigView("file")
		if err != nil {
			return cliError(err)
		}
		m, _ := doc.(map[string]any)
		steps := map[string]bool{}
		for _, kv := range args {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || k == "" {
				return exitcode.With(exitcode.BadInput, fmt.Errorf("%q is not KEY=VALUE", kv))
			}
			if err := service.SetKey(m, k, v); err != nil {
				return exitcode.With(exitcode.BadInput, err)
			}
			switch strings.SplitN(k, ".", 2)[0] {
			case "identity":
				steps[setup.StepIdentity] = true
			case "tls":
				steps[setup.StepTLS] = true
			case "size":
				steps[setup.StepSize] = true
			case "env", "fe":
				steps[setup.StepEnv] = true
			}
		}
		etag, err := svc.ETag()
		if err != nil {
			return err
		}
		res, err := svc.WriteConfig(cmd.Context(), service.ConfigWrite{IfMatch: etag, DryRun: configFlags.dryRun, Body: m, Source: jobs.SourceCLI})
		if err != nil {
			return cliError(err)
		}
		w := cmd.OutOrStdout()
		printPlan(w, res.Plan)
		if !res.Written {
			_, _ = fmt.Fprintln(w, "dry run: nothing was written")
			return nil
		}
		_, _ = fmt.Fprintf(w, "wrote %s (ETag %s)\n", configPath(cmd), res.ETag)
		if st := svc.SetupStateName(); st == setup.Claimed || st == setup.InSetup {
			_, err := svc.Setup.Update(func(r *setup.Record) error {
				for id := range steps {
					r.Steps[id] = &setup.StepRecord{Status: setup.Done, UpdatedAt: svc.Auth.Now()}
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		if configFlags.apply {
			return runApply(cmd, "config set")
		}
		return nil
	},
}

func printPlan(w io.Writer, pv *service.PlanPreview) {
	if pv == nil {
		return
	}
	if !pv.Changed {
		_, _ = fmt.Fprintln(w, "plan: no changes")
		return
	}
	_, _ = fmt.Fprintf(w, "plan (impact %s):\n", pv.Impact)
	for _, c := range pv.Changes {
		_, _ = fmt.Fprintf(w, "  %-6s %s\n", c.Kind, c.Path)
	}
	for _, a := range pv.Actions {
		_, _ = fmt.Fprintf(w, "  - %s\n", a.Text)
	}
}

var configPresetsCmd = &cobra.Command{
	Use:   "presets [--json]",
	Short: "List the presets, the memory caps, and the preset for the RAM of this host",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		pl := newService(cmd).Presets()
		w := cmd.OutOrStdout()
		if configFlags.json {
			return printJSON(w, pl)
		}
		_, _ = fmt.Fprintf(w, "host RAM: %d MiB, default: %s\n", pl.HostRAMBytes>>20, pl.Default)
		for _, p := range pl.Items {
			mark := " "
			if p.Recommended {
				mark = "*"
			}
			line := fmt.Sprintf("%s %-13s", mark, p.Name)
			if p.GunicornWorkers != nil {
				line += fmt.Sprintf(" gunicorn %d, celery %d, shared_buffers %d MB", *p.GunicornWorkers, *p.CeleryConcurrency, *p.PGSharedBuffersMB)
			}
			if p.Warning != nil {
				line += " (warning: " + *p.Warning + ")"
			}
			_, _ = fmt.Fprintln(w, line)
		}
		return nil
	},
}

func init() {
	configShowCmd.Flags().BoolVar(&configFlags.json, "json", false, "print JSON")
	configShowCmd.Flags().BoolVar(&configFlags.file, "file", false, "only the keys of the file")
	configSetCmd.Flags().BoolVar(&configFlags.dryRun, "dry-run", false, "print the plan; write nothing")
	configSetCmd.Flags().BoolVar(&configFlags.apply, "apply", false, "run care-pi apply after the write")
	addLockFlag(configSetCmd)
	configPresetsCmd.Flags().BoolVar(&configFlags.json, "json", false, "print JSON")
	configCmd.AddCommand(configShowCmd, configSetCmd, configPresetsCmd)
}
