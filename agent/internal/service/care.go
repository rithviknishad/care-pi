package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/applock"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
)

// CareInfo holds the Care URLs and the hand-off link (schema CareInfo).
type CareInfo struct {
	APIHealthy bool     `json:"api_healthy"`
	AppVersion *string  `json:"app_version"`
	PrimaryURL string   `json:"primary_url"`
	URLs       []string `json:"urls"`
	HandoffURL string   `json:"handoff_url"`
}

// CareAPI is the debug publish of care-api on the host loopback (care.pod).
var CareAPI = "http://127.0.0.1:19000"

// CareInfo returns the Care URLs and the hand-off URL.
func (s *Service) CareInfo(ctx context.Context) *CareInfo {
	primary := "https://" + s.PrimaryHost() + "/"
	out := &CareInfo{APIHealthy: s.careHealthy(ctx), PrimaryURL: primary, URLs: s.CareURLs(), HandoffURL: primary + "login"}
	if out.APIHealthy {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if req, err := http.NewRequestWithContext(ctx, http.MethodGet, CareAPI+"/app_version/", nil); err == nil {
			if resp, err := http.DefaultClient.Do(req); err == nil {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
				_ = resp.Body.Close()
				var v struct {
					Version string `json:"version"`
				}
				if resp.StatusCode == http.StatusOK && json.Unmarshal(body, &v) == nil && v.Version != "" {
					out.AppVersion = &v.Version
				}
			}
		}
	}
	return out
}

// CareSuperuser is a new Care superuser without the password (schema CareSuperuser).
type CareSuperuser struct {
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

// createSuperuser runs "manage.py createsuperuser --noinput" in care-api. The password goes in the
// environment of podman ("--env NAME" copies it), never on the command line.
func (s *Service) createSuperuser(ctx context.Context, username, email, password string) (*CareSuperuser, error) {
	out, err := s.Host.Runner.Run(ctx, host.Cmd{Name: "podman", Args: []string{
		"exec", "--env", "DJANGO_SUPERUSER_PASSWORD", "care-api",
		"python", "manage.py", "createsuperuser", "--noinput", "--username", username, "--email", email,
	}, Env: []string{"DJANGO_SUPERUSER_PASSWORD=" + password}})
	if err != nil {
		text := string(out)
		var xe *host.ExitError
		if ok := asExit(err, &xe); ok {
			text += xe.Stderr
		}
		if strings.Contains(text, "already taken") || strings.Contains(text, "already exists") {
			return nil, problem.Newf(problem.CareUserExists, "The Care user name %s exists.", username)
		}
		return nil, errf("manage.py createsuperuser failed: %s", strings.TrimSpace(firstLine(text, err.Error())))
	}
	return &CareSuperuser{Username: username, Email: email, CreatedAt: s.now().Truncate(time.Second)}, nil
}

func asExit(err error, xe **host.ExitError) bool {
	e, ok := err.(*host.ExitError)
	if ok {
		*xe = e
	}
	return ok
}

func firstLine(text, fallback string) string {
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.Contains(l, "DJANGO_SUPERUSER_PASSWORD") {
			return l
		}
	}
	return fallback
}

// CreateSuperuser creates a Care superuser. Setup must be finished, and care-api must be healthy.
func (s *Service) CreateSuperuser(ctx context.Context, username, email, password string) (*CareSuperuser, error) {
	if s.SetupStateName() != setup.Ready {
		return nil, problem.New(problem.SetupIncomplete, "Finish the setup first. The setup job creates the first superuser.")
	}
	if err := CheckCareUsername(username); err != nil {
		return nil, err
	}
	if err := s.checkCarePassword(password, "/password"); err != nil {
		return nil, err
	}
	if !s.careHealthy(ctx) {
		p := problem.New(problem.CareNotRunning, "care-api.service is not healthy.")
		p.RetryAfter = 30
		return nil, p
	}
	return s.createSuperuser(ctx, username, email, password)
}

// PowerAction is the reboot or shutdown that the agent will do (schema PowerAction).
type PowerAction struct {
	Action string    `json:"action"`
	At     time.Time `json:"at"`
}

// Power reboots ("reboot") or shuts down ("shutdown") the host after delay.
// While a job holds the apply lock, it returns job-running.
func (s *Service) Power(action string, delay time.Duration) (*PowerAction, error) {
	if applock.Held(s.Paths) {
		return nil, problem.Running(jobs.RunningLockJob(s.Paths))
	}
	verb := "reboot"
	if action == "shutdown" {
		verb = "poweroff"
	}
	run := func() {
		if applock.Held(s.Paths) {
			s.log().Warn("power: a job holds the apply lock; the agent does not run " + verb)
			return
		}
		s.log().Info("power: systemctl " + verb)
		if _, err := s.Host.Runner.Run(context.Background(), hostCmd("systemctl", verb)); err != nil {
			s.log().Error("power: systemctl "+verb+" failed", "error", err)
		}
	}
	if s.AfterPower != nil {
		s.AfterPower(delay, run)
	} else {
		time.AfterFunc(delay, run)
	}
	s.log().Info("power: scheduled", "action", action, "delay", delay)
	return &PowerAction{Action: action, At: s.now().Add(delay).Truncate(time.Second)}, nil
}
