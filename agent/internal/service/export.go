package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/buildinfo"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/render"
)

// The plain-text exports of S68: the "Copy" target of a job, a journal, and the doctor report in the console. Each
// text starts with the same header (the device, the agent, the lock, the time) and goes through the Redactor.

// SupportHeader returns the header of a plain-text export: the subject, the device, the agent, the lock, and the time.
func (s *Service) SupportHeader(subject string) string {
	var b strings.Builder
	name := ""
	if cfg, err := config.Load(s.configPath()); err == nil {
		name = cfg.Identity.DisplayName
	}
	fmt.Fprintf(&b, "CARE support text: %s\n", subject)
	fmt.Fprintf(&b, "Device: %s (%s)\n", name, s.PrimaryHost())
	agent := buildinfo.Version
	if buildinfo.Commit != "" {
		agent += ", commit " + buildinfo.Commit
	}
	if buildinfo.Date != "" {
		agent += ", built " + buildinfo.Date
	}
	if DevBuild() {
		agent += ", dev build"
	}
	fmt.Fprintf(&b, "Agent: %s\n", agent)
	fmt.Fprintf(&b, "Lock: %s\n", s.lockLine())
	fmt.Fprintf(&b, "Time: %s\n", s.now().UTC().Truncate(time.Second).Format(time.RFC3339))
	return b.String()
}

// lockLine tells the solution lock that runs, from the header of lock.yaml.
func (s *Service) lockLine() string {
	lk, err := lock.Load(s.Paths)
	if err != nil || lk == nil {
		return "none (no lock.yaml: the setup is not finished)"
	}
	v := lk.Solution.Version
	if v == "" {
		v = "no solution lock"
	}
	parts := []string{}
	if lk.Solution.Name != "" {
		parts = append(parts, "solution "+lk.Solution.Name)
	}
	if lk.Solution.Track != "" {
		parts = append(parts, "track "+lk.Solution.Track)
	}
	if lk.Solution.Source != "" {
		parts = append(parts, "source "+lk.Solution.Source)
	}
	if lk.GeneratedAt != "" {
		parts = append(parts, "generated "+lk.GeneratedAt)
	}
	if len(parts) == 0 {
		return v
	}
	return v + " (" + strings.Join(parts, ", ") + ")"
}

func stamp(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

func span(from, to *time.Time) string {
	if from == nil || to == nil {
		return ""
	}
	return " (" + to.Sub(*from).Round(100*time.Millisecond).String() + ")"
}

// stepSentence returns the operator sentence of a step (strings.steps.actions of the console), or "".
func stepSentence(st jobs.Step) string {
	if st.Action == nil {
		return ""
	}
	return render.MaintenanceSteps[*st.Action]
}

// JobTranscript returns the job as plain text (GET /jobs/{job_id}/export, care-pi jobs export): the header, the job,
// each step with its times, each log line and each reported problem, and the problem of a failed job.
func (s *Service) JobTranscript(ctx context.Context, id string) (string, error) {
	j, err := jobs.Load(s.Paths, id)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(s.SupportHeader("job " + j.ID))
	fmt.Fprintf(&b, "\nJob: %s\n", j.ID)
	fmt.Fprintf(&b, "Kind: %s (%s)\n", j.Kind, render.JobTitle(j.Kind))
	fmt.Fprintf(&b, "State: %s\n", j.State)
	fmt.Fprintf(&b, "Source: %s\n", j.Source)
	if j.Reason != nil {
		fmt.Fprintf(&b, "Reason: %s\n", *j.Reason)
	}
	fmt.Fprintf(&b, "Created: %s\n", stamp(&j.CreatedAt))
	fmt.Fprintf(&b, "Started: %s\n", stamp(j.StartedAt))
	fmt.Fprintf(&b, "Finished: %s%s\n", stamp(j.FinishedAt), span(j.StartedAt, j.FinishedAt))

	fmt.Fprintf(&b, "\nSteps (%d):\n", len(j.Steps))
	for _, st := range j.Steps {
		name := st.Name
		if t := stepSentence(st); t != "" {
			name = t + " (" + st.Name + ")"
		}
		fmt.Fprintf(&b, "  %d. [%s] %s\n", st.Index+1, st.State, name)
		fmt.Fprintf(&b, "     %s to %s%s\n", stamp(st.StartedAt), stamp(st.FinishedAt), span(st.StartedAt, st.FinishedAt))
		if st.Detail != nil && *st.Detail != "" {
			fmt.Fprintf(&b, "     %s\n", *st.Detail)
		}
	}

	var log strings.Builder
	lines := 0
	c := jobs.NewCursor(s.Paths, id, 0)
	for ctx.Err() == nil {
		evs, err := c.Next(jobs.MaxPageItems)
		if err != nil {
			return "", err
		}
		if len(evs) == 0 {
			break
		}
		for _, e := range evs {
			switch e.Event {
			case "log":
				var l jobs.LogLine
				if json.Unmarshal(e.Data, &l) != nil {
					continue
				}
				step := "-"
				if l.StepIndex != nil {
					step = fmt.Sprint(*l.StepIndex + 1)
				}
				fmt.Fprintf(&log, "%s %s %s | %s\n", stamp(&l.Time), l.Stream, step, l.Line)
				lines++
			case "error":
				var p problem.Problem
				if json.Unmarshal(e.Data, &p) == nil {
					fmt.Fprintf(&log, "problem: %s: %s\n", p.Title, p.Detail)
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "\nLog (%d lines; time, stream, step | line):\n", lines)
	b.WriteString(log.String())

	if j.Error != nil {
		fmt.Fprintf(&b, "\nProblem:\n  Type: %s\n  Title: %s\n  Status: %d\n", j.Error.Type(), j.Error.Title, j.Error.Status)
		if j.Error.Detail != "" {
			fmt.Fprintf(&b, "  Detail: %s\n", j.Error.Detail)
		}
	}
	return s.Redactor().Text(b.String()), nil
}

var priorities = []string{"emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"}

// LogsText returns one page of the journal of a unit as plain text (GET /logs/{unit}/export).
func (s *Service) LogsText(ctx context.Context, name string, q LogQuery) (string, error) {
	page, err := s.Logs(ctx, name, q)
	if err != nil {
		return "", err
	}
	unit, _ := UnitOf(name)
	var b strings.Builder
	b.WriteString(s.SupportHeader("the journal of " + unit))
	fmt.Fprintf(&b, "\nUnit: %s (%s), %d lines (time, priority, unit: message):\n", unit, page.Unit, len(page.Lines))
	for _, l := range page.Lines {
		pr := fmt.Sprint(l.Priority)
		if l.Priority >= 0 && l.Priority < len(priorities) {
			pr = priorities[l.Priority]
		}
		fmt.Fprintf(&b, "%s %s %s: %s\n", l.Time.UTC().Format("2006-01-02T15:04:05.000000Z"), pr, l.Unit, l.Message)
	}
	return s.Redactor().Text(b.String()), nil
}

// WriteDoctorText writes the report as the text of care-pi doctor: a table, the fix hints, and the status.
func WriteDoctorText(w io.Writer, rep *DoctorReport) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "STATUS\tCHECK\tDETAIL")
	for _, c := range rep.Checks {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", strings.ToUpper(c.Status), c.Title, c.Detail)
	}
	_ = tw.Flush()
	for _, c := range rep.Checks {
		if c.Fix != nil {
			_, _ = fmt.Fprintf(w, "fix %s: %s\n", c.ID, *c.Fix)
		}
	}
	_, _ = fmt.Fprintf(w, "status: %s\n", rep.Status)
}

// DoctorText returns the checks of care-pi doctor as plain text (GET /doctor/export).
func (s *Service) DoctorText(ctx context.Context) (string, error) {
	rep, err := s.Doctor(ctx)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(s.SupportHeader("care-pi doctor"))
	fmt.Fprintf(&b, "Checked: %s\n\n", rep.GeneratedAt.UTC().Format(time.RFC3339))
	WriteDoctorText(&b, rep)
	return s.Redactor().Text(b.String()), nil
}

// JobWithTail is a job with the last log lines (GET /jobs/{job_id}, contract 0.9.0).
type JobWithTail struct {
	*jobs.Job
	LogTail jobs.LogTail `json:"log_tail"`
}

// JobDetail returns the job and its last jobs.MaxLogTail log lines up to its last_event_id, without secrets: after a
// reload the console shows them, then follows the event stream after last_event_id.
func (s *Service) JobDetail(id string) (*JobWithTail, error) {
	j, err := jobs.Load(s.Paths, id)
	if err != nil {
		return nil, err
	}
	tail, err := jobs.Tail(s.Paths, id, j.LastEventID, jobs.MaxLogTail)
	if err != nil {
		return nil, err
	}
	if len(tail.Lines) > 0 {
		r := s.Redactor()
		for i := range tail.Lines {
			tail.Lines[i].Line = r.Text(tail.Lines[i].Line)
		}
	}
	return &JobWithTail{Job: j, LogTail: tail}, nil
}
