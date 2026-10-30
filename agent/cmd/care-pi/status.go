package main

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/render"
	"github.com/rithviknishad/care-pi/agent/internal/status"
)

var statusJSON bool

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the units, health, memory, lock, and the names and addresses in use",
	Long:  "status shows the state of the stack. Exit code 0: every unit is active. Exit code 1: degraded.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		var names []string
		if cfg, err := config.Load(configPath(cmd)); err == nil {
			names = render.Hostnames(cfg)
		}
		r, err := status.Collect(cmd.Context(), newHost(cmd), rootPaths(), names, netinfo.System{})
		if err != nil {
			return err
		}
		if statusJSON {
			if err := r.WriteJSON(cmd.OutOrStdout()); err != nil {
				return err
			}
		} else {
			r.WriteText(cmd.OutOrStdout())
		}
		if r.Degraded {
			return &exitcode.Error{Code: exitcode.Failed, Err: errors.New("the stack is degraded")}
		}
		return nil
	},
}

func init() {
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "print JSON")
	rootCmd.AddCommand(statusCmd)
}
