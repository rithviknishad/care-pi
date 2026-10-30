package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/buildinfo"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version, commit, and build date",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "version: %s\ncommit:  %s\ndate:    %s\n",
			buildinfo.Version, buildinfo.Commit, buildinfo.Date)
		return err
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
