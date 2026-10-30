package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
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
