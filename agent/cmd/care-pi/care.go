package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
)

var careCmd = &cobra.Command{
	Use:   "care",
	Short: "Run CARE management tasks in the care-api container",
}

var superuserFlags struct {
	username      string
	email         string
	passwordStdin bool
}

var createSuperuserCmd = &cobra.Command{
	Use:   "create-superuser --username U --email E [--password-stdin]",
	Short: "Create a CARE superuser with manage.py createsuperuser",
	Long: `create-superuser runs "python manage.py createsuperuser" in care-api.
With --password-stdin, the command reads the password from the standard input
and passes it in the environment, not on the command line.
Without it, Django asks for the password on the terminal.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		f := superuserFlags
		if f.username == "" || f.email == "" {
			return exitcode.With(exitcode.BadInput, fmt.Errorf("--username and --email are required"))
		}
		args := []string{"python", "manage.py", "createsuperuser", "--username", f.username, "--email", f.email}
		h := newHost(cmd)
		if !f.passwordStdin {
			return passthrough(h.Podman.ExecAttached(cmd.Context(), "care-api", true, nil, nil, args...))
		}
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return fmt.Errorf("cannot read the password: %w", err)
		}
		pw := strings.TrimRight(string(data), "\r\n")
		if pw == "" {
			return exitcode.With(exitcode.BadInput, fmt.Errorf("the password on the standard input is empty"))
		}
		args = append(args, "--noinput")
		env := []string{"DJANGO_SUPERUSER_PASSWORD=" + pw}
		return passthrough(h.Podman.ExecAttached(cmd.Context(), "care-api", false, env, strings.NewReader(""), args...))
	},
}

func init() {
	f := createSuperuserCmd.Flags()
	f.StringVar(&superuserFlags.username, "username", "", "user name")
	f.StringVar(&superuserFlags.email, "email", "", "email address")
	f.BoolVar(&superuserFlags.passwordStdin, "password-stdin", false, "read the password from the standard input")
	careCmd.AddCommand(createSuperuserCmd)
	rootCmd.AddCommand(careCmd)
}
