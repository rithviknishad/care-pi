package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

var renderFlags struct {
	out       string
	ips       []string
	solutions string
}

var renderCmd = &cobra.Command{
	Use:   "render --config PATH --out DIR",
	Short: "Render every file to a directory, without root and without host access",
	Long: `render writes the rendered files to DIR. DIR gets the host layout, for example
DIR/etc/containers/systemd/care-api.container and DIR/etc/care-pi/lock.yaml.

render does not use the network. It reads the lock below --root when the lock
exists. For an image without a locked digest, the unit uses the tag.
By default render reads the LAN addresses of this machine. Use --ip to set them.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if renderFlags.out == "" {
			return exitcode.With(exitcode.BadInput, fmt.Errorf("--out is required"))
		}
		var ips netinfo.Source = netinfo.System{}
		if len(renderFlags.ips) > 0 {
			ips = netinfo.Static(renderFlags.ips)
		}
		pr, err := apply.Prepare(cmd.Context(), apply.PrepareOptions{
			Paths:        rootPaths(),
			ConfigPath:   configPath(cmd),
			SolutionsDir: renderFlags.solutions,
			IPs:          ips,
			Offline:      true,
			Warn:         cmd.ErrOrStderr(),
		})
		if err != nil {
			return err
		}
		out := paths.New(renderFlags.out)
		w := cmd.OutOrStdout()
		for _, p := range pr.Tree.Paths() {
			f := pr.Tree[p]
			abs := out.Abs(p)
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(abs, f.Content, f.Mode); err != nil {
				return err
			}
			if err := os.Chmod(abs, f.Mode); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(w, p)
		}
		if pr.Lock.GeneratedAt == "" {
			pr.Lock.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
		}
		data, err := pr.Lock.Marshal()
		if err != nil {
			return err
		}
		abs := out.Abs(paths.LockFile)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(abs, data, 0o600); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(w, paths.LockFile)
		return nil
	},
}

func init() {
	f := renderCmd.Flags()
	f.StringVar(&renderFlags.out, "out", "", "output directory")
	f.StringSliceVar(&renderFlags.ips, "ip", nil, "LAN IPv4 address to render (repeat the flag for more)")
	f.StringVar(&renderFlags.solutions, "solutions", "", "directory with <name>/solution.yaml (default: the embedded solutions)")
	rootCmd.AddCommand(renderCmd)
}
