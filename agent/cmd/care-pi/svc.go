package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/auth"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/s3admin"
	"github.com/rithviknishad/care-pi/agent/internal/service"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
)

// newAuth returns the auth manager of the host.
func newAuth() *auth.Manager {
	p := rootPaths()
	return auth.New(auth.Options{Paths: p, OS: auth.Shadow{Paths: p}})
}

// newService returns the service layer for a CLI command. Root on the host needs no session.
func newService(cmd *cobra.Command) *service.Service {
	p := rootPaths()
	return &service.Service{
		Paths:      p,
		ConfigPath: configPath(cmd),
		Host:       newHost(cmd),
		IPs:        netinfo.System{},
		S3:         s3admin.MinIO{},
		Auth:       newAuth(),
		Setup:      &setup.Store{Paths: p},
		Jobs:       &jobs.Manager{Paths: p, LockWait: lockTimeout},
		StartedAt:  time.Now(),
		Chown:      os.Geteuid() == 0,
	}
}

// cliError maps a problem to an exit code: 2 for a request or config problem, else 1.
func cliError(err error) error {
	if err == nil {
		return nil
	}
	var p *problem.Problem
	if errors.As(err, &p) {
		if p.Status == 422 || p.Status == 400 || p.Slug == problem.SetupIncomplete || p.Slug == problem.NotClaimed {
			return exitcode.With(exitcode.BadInput, err)
		}
		return exitcode.With(exitcode.Failed, err)
	}
	return err
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// readSecret reads one line from the standard input.
func readSecret(cmd *cobra.Command, what string) (string, error) {
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("cannot read the %s: %w", what, err)
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return "", exitcode.With(exitcode.BadInput, fmt.Errorf("the %s on the standard input is empty", what))
	}
	return line, nil
}

// followJob prints the events of a job until it ends. It reads the event file, so it also follows
// a job of another process.
func followJob(ctx context.Context, w io.Writer, svc *service.Service, id string) (*jobs.Job, error) {
	last := 0
	for {
		evs, err := jobs.Events(svc.Paths, id, last, jobs.MaxPageItems)
		if err != nil {
			return nil, err
		}
		for _, e := range evs {
			last = e.ID
			printEvent(w, e)
			if e.Event == "done" {
				return jobs.Load(svc.Paths, id)
			}
		}
		if len(evs) == 0 {
			if j, err := jobs.Load(svc.Paths, id); err == nil && j.Done() {
				return j, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func printEvent(w io.Writer, e jobs.Event) {
	switch e.Event {
	case "step":
		var s jobs.Step
		_ = json.Unmarshal(e.Data, &s)
		mark := map[string]string{"running": "==>", "succeeded": "ok ", "failed": "ERR", "cancelled": "-- ", "skipped": "-- "}[s.State]
		line := fmt.Sprintf("%s %s", mark, s.Name)
		if s.State == "failed" && s.Detail != nil {
			line += ": " + *s.Detail
		}
		if s.State != "succeeded" || s.FinishedAt == nil {
			_, _ = fmt.Fprintln(w, line)
		}
	case "log":
		var l jobs.LogLine
		_ = json.Unmarshal(e.Data, &l)
		_, _ = fmt.Fprintln(w, "   ", l.Line)
	case "error":
		var p problem.Problem
		_ = json.Unmarshal(e.Data, &p)
		_, _ = fmt.Fprintln(w, "error:", p.Detail)
	case "done":
		var j jobs.Job
		_ = json.Unmarshal(e.Data, &j)
		_, _ = fmt.Fprintf(w, "job %s: %s\n", j.ID, j.State)
	}
}

// jobError returns the error of a job that did not succeed.
func jobError(j *jobs.Job) error {
	if j == nil || j.State == jobs.StateSucceeded {
		return nil
	}
	if j.Error != nil {
		return exitcode.With(exitcode.Failed, fmt.Errorf("job %s %s: %s", j.ID, j.State, j.Error.Detail))
	}
	return exitcode.With(exitcode.Failed, fmt.Errorf("job %s %s", j.ID, j.State))
}
