package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/fe"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/render"
	"github.com/rithviknishad/care-pi/agent/internal/service"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
)

var feStatusJSON bool

var feFlags struct {
	force     bool
	keep      int
	sha       string
	envFile   string
	solution  string
	solutions string
}

var feCmd = &cobra.Command{
	Use:   "fe",
	Short: "Build and manage the care_fe build (docs/fe.md)",
}

var feBuildCmd = &cobra.Command{
	Use:   "build [--force]",
	Short: "Make the FE build of the lock now, with the provider of config.yaml",
	Long: `build runs the FE provider for the build id in lock.yaml, then points
fe/current at the build. apply does the same when the build id changes.

The build id of config.yaml must be the build id in lock.yaml. After a change
to config.yaml, run "care-pi apply" instead. apply also makes the FE build.

--force removes the build directory first. When the build is current, the site
shows the setup page until the new build is ready.

build holds the lock /run/care-pi/apply.lock for its full run, so apply waits
until the build ends.

Exit codes: 0 when the build is current, 1 when the build fails,
2 when the config is not valid or fe.provider is none.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		w := cmd.OutOrStdout()
		release, err := takeApplyLock(cmd)
		if err != nil {
			return err
		}
		defer release()
		svc := newService(cmd)
		svc.SolutionsDir = feFlags.solutions
		rec, err := jobs.Create(jobs.CreateOptions{Paths: svc.Paths, Kind: jobs.KindFEBuild, Source: jobs.SourceCLI, Cancellable: true})
		if err != nil {
			return err
		}
		end := rec.StepAction("ensure-fe: build of the lock", "ensure-fe")
		res, err := svc.RunFEBuild(cmd.Context(), service.FEBuildOptions{Force: feFlags.force, Out: io.MultiWriter(w, rec.Writer()), Follow: w == io.Writer(os.Stdout)})
		end(err)
		if err != nil {
			rec.Finish(err, nil)
			return err
		}
		rec.Finish(nil, res)
		return nil
	},
}

var feStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the FE provider, the build id, the current build, and the builds on disk",
	Long: `status shows the FE provider, the build id of lock.yaml, the target of
fe/current, the last build, and each build in /var/lib/care-pi/fe.

Exit codes: 0 when fe/current is the build of lock.yaml (or fe.provider is none),
1 when it is not: run care-pi apply or care-pi fe build.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		w := cmd.OutOrStdout()
		if feStatusJSON {
			st, err := newService(cmd).FE()
			if err != nil {
				return err
			}
			return printJSON(w, st)
		}
		p := rootPaths()
		provider := "?"
		if cfg, err := config.Load(configPath(cmd)); err == nil {
			provider = cfg.FE.Provider
		} else {
			_, _ = fmt.Fprintln(w, "warning:", err)
		}
		disk, err := lock.Read(p.Abs(paths.LockFile))
		if err != nil {
			return err
		}
		var rec lock.FE
		var sha string
		if disk != nil {
			rec, sha = disk.FE, disk.Git["care_fe"].SHA
		}
		cur := fe.Current(p)
		_, _ = fmt.Fprintf(w, "provider:   %s\n", provider)
		_, _ = fmt.Fprintf(w, "build id:   %s\n", orNone(rec.BuildID))
		_, _ = fmt.Fprintf(w, "care_fe:    %s\n", orNone(sha))
		_, _ = fmt.Fprintf(w, "current:    %s\n", orNone(cur))
		switch {
		case rec.Duration != "":
			_, _ = fmt.Fprintf(w, "last build: %s, %s (provider %s)\n", rec.BuiltAt, rec.Duration, rec.Provider)
		case rec.URL != "":
			_, _ = fmt.Fprintf(w, "bundle:     %s (sha256 %s)\n", rec.URL, rec.SHA256)
		}
		if provider == "artifact" && rec.URL == "" && sha != "" && rec.BuildID != "" {
			_, _ = fmt.Fprintf(w, "bundle:     %s (default)\n", fe.DefaultArtifactURL(solutionName(cmd), sha, rec.BuildID))
		}
		builds, err := fe.List(p)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(w, "builds:")
		if len(builds) == 0 {
			_, _ = fmt.Fprintln(w, "  (none)")
		}
		for _, b := range builds {
			mark := " "
			if b.Current {
				mark = "*"
			}
			_, _ = fmt.Fprintf(w, "  %s %-16s  %8s  %s\n", mark, b.ID, humanBytes(b.Bytes), b.ModTime.UTC().Format("2006-01-02 15:04 UTC"))
		}
		if _, err := os.Stat(p.Abs(fe.ScaleDownDropIn)); err == nil {
			_, _ = fmt.Fprintf(w, "warning: %s exists, so care-api runs 1 gunicorn worker.\n", fe.ScaleDownDropIn)
			_, _ = fmt.Fprintln(w, "  When no FE build runs, run: care-pi apply. apply removes the file and restores care-api and care-worker.")
		}
		if provider != "none" && (rec.BuildID == "" || cur != rec.BuildID) {
			return &exitcode.Error{Code: exitcode.Failed, Err: errors.New("fe/current is not the FE build of lock.yaml; run care-pi apply or care-pi fe build")}
		}
		return nil
	},
}

var fePruneCmd = &cobra.Command{
	Use:   "prune [--keep N]",
	Short: "Remove old FE builds; keep the N newest builds and the current build",
	Long: `prune removes the FE builds in /var/lib/care-pi/fe, except the N newest
builds. It never removes the current build. It also removes the temporary
directories of incomplete builds. prune does not touch the build cache in
/var/lib/care-pi/build.

Exit codes: 0 when prune completes, 1 when a build runs now or a removal fails,
2 when N is less than 1.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if feFlags.keep < 1 {
			return exitcode.With(exitcode.BadInput, fmt.Errorf("--keep must be 1 or more"))
		}
		release, err := takeApplyLock(cmd)
		if err != nil {
			return err
		}
		defer release()
		removed, err := fe.Prune(cmd.Context(), rootPaths(), feFlags.keep)
		w := cmd.OutOrStdout()
		for _, r := range removed {
			_, _ = fmt.Fprintln(w, "removed", r)
		}
		if err != nil {
			return err
		}
		if len(removed) == 0 {
			_, _ = fmt.Fprintln(w, "nothing to remove")
		}
		return nil
	},
}

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

var feBuildIDCmd = &cobra.Command{
	Use:    "build-id --care-fe-sha SHA --env-file FILE",
	Short:  "Print the FE build id for a care_fe sha and a frontend.env file (a tool for CI)",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if !fullSHA.MatchString(feFlags.sha) {
			return exitcode.With(exitcode.BadInput, fmt.Errorf("--care-fe-sha must be a full git sha (40 hex digits)"))
		}
		f, err := os.Open(feFlags.envFile)
		if err != nil {
			return exitcode.With(exitcode.BadInput, err)
		}
		defer func() { _ = f.Close() }()
		env, err := fe.ParseEnvFile(f)
		if err != nil {
			return exitcode.With(exitcode.BadInput, fmt.Errorf("%s: %w", feFlags.envFile, err))
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), fe.BuildID(feFlags.sha, env))
		return err
	},
}

var feEnvCmd = &cobra.Command{
	Use:    "env --solution NAME",
	Short:  "Print the frontend.env of a solution with the default config (a tool for CI)",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg := config.Default()
		cfg.Solution.Name = feFlags.solution
		sol, err := solution.Load(feFlags.solution, feFlags.solutions)
		if err != nil {
			return exitcode.With(exitcode.BadInput, err)
		}
		des, err := render.Desire(cfg, sol)
		if err != nil {
			return exitcode.With(exitcode.BadInput, err)
		}
		data, err := render.FrontendEnvFile(des.Frontend)
		if err != nil {
			return err
		}
		_, err = cmd.OutOrStdout().Write(data)
		return err
	},
}

var feScriptCmd = &cobra.Command{
	Use:    "script",
	Short:  "Print fe-build.sh, the build script of the local provider (a tool for CI)",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		data, err := render.FEBuildScript()
		if err != nil {
			return err
		}
		_, err = cmd.OutOrStdout().Write(data)
		return err
	},
}

func solutionName(cmd *cobra.Command) string {
	if cfg, err := config.Load(configPath(cmd)); err == nil {
		return cfg.Solution.Name
	}
	return config.DefaultSolution
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func init() {
	feBuildCmd.Flags().BoolVar(&feFlags.force, "force", false, "remove the build directory first, then make the build again")
	feBuildCmd.Flags().StringVar(&feFlags.solutions, "solutions", "", "directory with <name>/solution.yaml (default: the embedded solutions)")
	_ = feBuildCmd.Flags().MarkHidden("solutions")
	fePruneCmd.Flags().IntVar(&feFlags.keep, "keep", 2, "number of newest builds to keep")
	feStatusCmd.Flags().BoolVar(&feStatusJSON, "json", false, "print the FeStatus object of GET /api/v1/fe")
	addLockFlag(feBuildCmd)
	addLockFlag(fePruneCmd)
	feBuildIDCmd.Flags().StringVar(&feFlags.sha, "care-fe-sha", "", "full git sha of care_fe")
	feBuildIDCmd.Flags().StringVar(&feFlags.envFile, "env-file", "", "frontend.env file (NAME=value lines)")
	_ = feBuildIDCmd.MarkFlagRequired("care-fe-sha")
	_ = feBuildIDCmd.MarkFlagRequired("env-file")
	feEnvCmd.Flags().StringVar(&feFlags.solution, "solution", config.DefaultSolution, "solution name")
	feEnvCmd.Flags().StringVar(&feFlags.solutions, "solutions", "", "directory with <name>/solution.yaml (default: the embedded solutions)")
	feCmd.AddCommand(feBuildCmd, feStatusCmd, fePruneCmd, feBuildIDCmd, feEnvCmd, feScriptCmd)
	rootCmd.AddCommand(feCmd)
}
