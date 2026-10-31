package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
)

var solutionFlags struct {
	json      bool
	lock      string
	input     string
	distBase  string
	locks     []string
	withdrawn []string
	held      string
	agent     string
	agentURL  string
	generated string
	changelog string
}

var solutionCmd = &cobra.Command{
	Use:   "solution",
	Short: "List and show the solutions and their locks; validate, make, and index solution files",
}

var solutionListCmd = &cobra.Command{
	Use:   "list [--json]",
	Short: "List the solutions of the agent and of the cached index.json (GET /solutions)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		l, err := newService(cmd).Solutions()
		if err != nil {
			return cliError(err)
		}
		w := cmd.OutOrStdout()
		if solutionFlags.json {
			return printJSON(w, l)
		}
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "NAME\tTITLE\tSOURCE\tSELECTED\tLATEST\tLOCKS\tPLUGS")
		for _, s := range l.Items {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%v\t%s\t%d\t%d\n", s.Name, s.Title, s.Source, s.Selected, deref(s.LatestVersion), s.LockCount, s.PlugCount)
		}
		return tw.Flush()
	},
}

var solutionShowCmd = &cobra.Command{
	Use:   "show NAME [--lock VERSION] [--json]",
	Short: "Show a solution and its locks, or one solution lock",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		svc := newService(cmd)
		w := cmd.OutOrStdout()
		if solutionFlags.lock != "" {
			l, err := svc.SolutionLock(cmd.Context(), args[0], solutionFlags.lock)
			if err != nil {
				return cliError(err)
			}
			if solutionFlags.json {
				return printJSON(w, l)
			}
			return yaml.NewEncoder(w).Encode(l)
		}
		d, err := svc.Solution(args[0])
		if err != nil {
			return cliError(err)
		}
		if solutionFlags.json {
			return printJSON(w, d)
		}
		_, _ = fmt.Fprintf(w, "%s: %s (%s)\n", d.Name, d.Title, d.Source)
		for _, l := range d.Locks {
			mark := ""
			if l.Withdrawn {
				mark = " withdrawn: " + deref(l.WithdrawnReason)
			}
			_, _ = fmt.Fprintf(w, "  %s  %s  %s%s\n", l.Version, l.ReleasedAt.Format("2006-01-02"), l.Summary, mark)
		}
		return nil
	},
}

var solutionValidateCmd = &cobra.Command{
	Use:   "validate FILE",
	Short: "Validate a solution.yaml, a solution lock, or an index.json (by its kind)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		data, err := os.ReadFile(args[0])
		if err != nil {
			return exitcode.With(exitcode.BadInput, err)
		}
		kind, err := solution.Validate(data)
		if err != nil {
			return exitcode.With(exitcode.BadInput, fmt.Errorf("%s: %w", args[0], err))
		}
		if solutionFlags.json {
			doc, err := solution.DocumentJSON(data)
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), doc)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: a valid %s document\n", args[0], kind)
		return nil
	},
}

var solutionLockCmd = &cobra.Command{
	Use:   "lock --input FILE.json",
	Short: "Make a solution lock from resolved identities (CI and solutions/scripts/make-lock.sh)",
	Long: `Make a solution lock (docs/solutions.md, section 3) from a JSON file of resolved identities:
version, created_by, notes, commit, run_url, care.digest, care.git_sha, solution_image.{image,tag,digest},
care_fe_sha, fe.{build_id,url,sha256,size_bytes,env_keys}, plugs.<id>.{backend_sha,frontend_sha,
frontend_url,frontend_sha256,frontend_size_bytes}, images.<name>.{ref,digest}, and solution_file.
The command checks the lock with lock.schema.json and prints it.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		data, err := os.ReadFile(solutionFlags.input)
		if err != nil {
			return exitcode.With(exitcode.BadInput, err)
		}
		var in solution.LockInput
		if err := json.Unmarshal(data, &in); err != nil {
			return exitcode.With(exitcode.BadInput, fmt.Errorf("%s: %w", solutionFlags.input, err))
		}
		file := in.SolutionFile
		if file != "" && !filepath.IsAbs(file) {
			file = filepath.Join(filepath.Dir(solutionFlags.input), file)
		}
		if in.Solution, err = os.ReadFile(file); err != nil {
			return exitcode.With(exitcode.BadInput, fmt.Errorf("solution_file: %w", err))
		}
		out, err := solution.AssembleLock(in)
		if err != nil {
			return exitcode.With(exitcode.BadInput, err)
		}
		_, err = cmd.OutOrStdout().Write(out)
		return err
	},
}

var solutionIndexCmd = &cobra.Command{
	Use:   "index --dist-base URL --lock FILE... [--withdrawn FILE]... [--held-packages FILE.json]",
	Short: "Make index.json from solution lock files (CI)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		f := solutionFlags
		in := solution.IndexInput{DistBase: f.distBase, GeneratedAt: f.generated, Withdrawn: map[string]string{}}
		if f.agent != "" {
			in.Agent = &solution.IndexAgent{Latest: f.agent, ReleaseURL: f.agentURL}
		}
		for _, p := range f.locks {
			data, err := os.ReadFile(p)
			if err != nil {
				return exitcode.With(exitcode.BadInput, err)
			}
			l, err := solution.ParseLock(data)
			if err != nil {
				return exitcode.With(exitcode.BadInput, fmt.Errorf("%s: %w", p, err))
			}
			rel := solution.IndexRelease{Data: data}
			if f.changelog != "" {
				rel.ChangelogURL = strings.TrimSuffix(f.changelog, "/") + "/lock-" + l.Name + "-" + solution.NormalizeLockVersion(l.Version)
			}
			in.Releases = append(in.Releases, rel)
		}
		for _, p := range f.withdrawn {
			data, err := os.ReadFile(p)
			if err != nil {
				return exitcode.With(exitcode.BadInput, err)
			}
			var m map[string]string
			if err := yaml.Unmarshal(data, &m); err != nil {
				return exitcode.With(exitcode.BadInput, fmt.Errorf("%s: %w", p, err))
			}
			// solutions/<name>/locks/withdrawn.yaml
			name := filepath.Base(filepath.Dir(filepath.Dir(p)))
			for v, reason := range m {
				in.Withdrawn[name+"/"+solution.NormalizeLockVersion(v)] = reason
			}
		}
		if f.held != "" {
			data, err := os.ReadFile(f.held)
			if err != nil {
				return exitcode.With(exitcode.BadInput, err)
			}
			if err := json.Unmarshal(data, &in.HeldPackages); err != nil {
				return exitcode.With(exitcode.BadInput, fmt.Errorf("%s: %w", f.held, err))
			}
		}
		out, err := solution.AssembleIndex(in)
		if err != nil {
			return exitcode.With(exitcode.BadInput, err)
		}
		_, err = cmd.OutOrStdout().Write(out)
		return err
	},
}

func init() {
	solutionListCmd.Flags().BoolVar(&solutionFlags.json, "json", false, "print JSON")
	solutionShowCmd.Flags().BoolVar(&solutionFlags.json, "json", false, "print JSON")
	solutionShowCmd.Flags().StringVar(&solutionFlags.lock, "lock", "", "show the solution lock of this version")
	solutionValidateCmd.Flags().BoolVar(&solutionFlags.json, "json", false, "print the valid document as JSON")
	solutionLockCmd.Flags().StringVar(&solutionFlags.input, "input", "", "the JSON file of resolved identities")
	_ = solutionLockCmd.MarkFlagRequired("input")
	f := solutionIndexCmd.Flags()
	f.StringVar(&solutionFlags.distBase, "dist-base", solution.DefaultDistBase, "dist_base of the index (ends with /)")
	f.StringArrayVar(&solutionFlags.locks, "lock", nil, "a solution lock file (repeat)")
	f.StringArrayVar(&solutionFlags.withdrawn, "withdrawn", nil, "a solutions/<name>/locks/withdrawn.yaml file (repeat)")
	f.StringVar(&solutionFlags.held, "held-packages", "", "a JSON file: os.held_packages of the index")
	f.StringVar(&solutionFlags.agent, "agent-latest", "", "the newest agent release vX.Y.Z")
	f.StringVar(&solutionFlags.agentURL, "agent-url", "", "the release page of that agent")
	f.StringVar(&solutionFlags.generated, "generated-at", "", "generated_at (default: now)")
	f.StringVar(&solutionFlags.changelog, "changelog-base", "", "the base of the release pages, for example https://github.com/OWNER/REPO/releases/tag")
	solutionCmd.AddCommand(solutionListCmd, solutionShowCmd, solutionValidateCmd, solutionLockCmd, solutionIndexCmd)
	rootCmd.AddCommand(solutionCmd)
}
