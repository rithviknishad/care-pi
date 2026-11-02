package main

import (
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/helper"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/server"
)

var helperSocket string

var helperCmd = &cobra.Command{
	Use:   "helper [--socket PATH]",
	Short: "Run the privileged helper of the agent (care-pi-helper.service, Phase 7)",
	Long: `helper runs as root on /run/care-pi-helper/helper.sock (0660 root:care-pi; systemd gives the socket of
care-pi-helper.socket). It runs the host commands of "care-pi serve --helper": only the commands of its allow-list,
each argument checked against the shapes that the agent uses, never through a shell of its own. It logs each request
and each refusal to the journal (journalctl -u care-pi-helper). docs/console-server.md section 14.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if os.Geteuid() != 0 {
			return exitcode.With(exitcode.BadInput, errors.New("care-pi helper must run as root (care-pi-helper.service)"))
		}
		log := slog.New(slog.NewTextHandler(cmd.OutOrStdout(), &slog.HandlerOptions{
			ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
				if a.Key == slog.TimeKey {
					return slog.Attr{}
				}
				return a
			},
		}))
		if err := os.MkdirAll(paths.HelperDir, 0o750); err != nil {
			return err
		}
		l, err := helper.Listener(helperSocket)
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		srv := &helper.Server{Exec: host.Transient{}, Log: log, Allowed: helper.PeerCheck(paths.AgentUser)}
		log.Info("care-pi helper: ready", "socket", l.Addr().String())
		if err := server.Notify("READY=1"); err != nil {
			log.Warn("sd_notify failed", "error", err)
		}
		return srv.Serve(ctx, l)
	},
}

func init() {
	helperCmd.Flags().StringVar(&helperSocket, "socket", paths.HelperSocket, "the socket path when systemd gives none")
	rootCmd.AddCommand(helperCmd)
}
