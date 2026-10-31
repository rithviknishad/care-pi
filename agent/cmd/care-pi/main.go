// Command care-pi is the CLI and agent for a self-hosted CARE on a Raspberry Pi.
//
// To add a subcommand, create one file in this directory.
// In that file, define a *cobra.Command and register it on rootCmd in an init() function.
package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/applock"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

var global struct {
	config  string
	root    string
	verbose bool
}

var rootCmd = &cobra.Command{
	Use:           "care-pi",
	Short:         "Run CARE on a Raspberry Pi from one config file",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	},
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringVar(&global.config, "config", paths.ConfigFile, "path of the config file")
	pf.StringVar(&global.root, "root", "/", "prefix for every absolute path (tests use it)")
	pf.BoolVarP(&global.verbose, "verbose", "v", false, "print each host command before it runs")
}

// rootPaths returns the paths below --root.
func rootPaths() paths.Paths { return paths.New(global.root) }

// configPath returns --config. When the flag is not set, the default path goes below --root.
func configPath(cmd *cobra.Command) string {
	if f := cmd.Flags().Lookup("config"); f != nil && f.Changed {
		return global.config
	}
	return rootPaths().Abs(paths.ConfigFile)
}

// lockTimeout is --lock-timeout of the commands that take the apply lock.
var lockTimeout time.Duration

// addLockFlag adds --lock-timeout to a command that takes the apply lock.
func addLockFlag(c *cobra.Command) {
	c.Flags().DurationVar(&lockTimeout, "lock-timeout", applock.DefaultTimeout,
		"maximum wait when another care-pi command holds the lock (0: no limit)")
}

// addressWait is --address-wait of apply and bootstrap.
var addressWait time.Duration

// defaultAddressWait is the default of --address-wait. At the boot of lumine (Pi OS trixie),
// network-online.target came before the DHCP lease of eth0 (IPv6 came first), so the
// NetworkManager hook ran apply without the LAN address (wave 4d).
const defaultAddressWait = 60 * time.Second

// addAddressWaitFlag adds --address-wait to a command that renders the pod and the Caddyfile.
func addAddressWaitFlag(c *cobra.Command) {
	c.Flags().DurationVar(&addressWait, "address-wait", defaultAddressWait,
		"maximum wait for a LAN IPv4 address before the render (0: no wait); without one, render without it")
}

// takeApplyLock takes the apply lock for the full run of the command (applock).
func takeApplyLock(cmd *cobra.Command) (func(), error) {
	return applock.Acquire(cmd.Context(), rootPaths(), lockTimeout, cmd.OutOrStdout())
}

// newHost returns the exec-based host clients.
func newHost(cmd *cobra.Command) *host.Host {
	var log io.Writer
	if global.verbose {
		log = cmd.ErrOrStderr()
	}
	return host.New(host.Exec{Log: log})
}

// errSilent ends a passthrough command with its exit code and no message.
type errSilent struct{}

func (errSilent) Error() string { return "" }

// passthrough keeps the exit code of an attached host command.
func passthrough(err error) error {
	if xe, ok := err.(*host.ExitError); ok {
		return &exitcode.Error{Code: xe.Code, Err: errSilent{}}
	}
	return err
}

func main() {
	err := rootCmd.Execute()
	if err == nil {
		return
	}
	if msg := err.Error(); msg != "" {
		_, _ = fmt.Fprintln(os.Stderr, "care-pi:", msg)
	}
	os.Exit(exitcode.Code(err))
}
