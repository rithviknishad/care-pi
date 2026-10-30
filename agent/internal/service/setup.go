package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/auth"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
)

// CareAdminDraft is the care-admin step without the password (schema CareAdminDraft).
type CareAdminDraft struct {
	Username    *string `json:"username"`
	Email       *string `json:"email"`
	PasswordSet bool    `json:"password_set"`
}

// Draft holds the values of the steps (schema SetupDraft).
type Draft struct {
	Identity  map[string]any `json:"identity"`
	TLS       map[string]any `json:"tls"`
	Size      map[string]any `json:"size"`
	Env       map[string]any `json:"env"`
	CareAdmin CareAdminDraft `json:"care_admin"`
	Backups   any            `json:"backups"`
}

// SetupState is the setup state machine (schema SetupState).
type SetupState struct {
	State       string           `json:"state"`
	DevBuild    bool             `json:"dev_build"`
	CurrentStep *string          `json:"current_step"`
	Steps       []setup.StepView `json:"steps"`
	Draft       Draft            `json:"draft"`
	CanFinish   bool             `json:"can_finish"`
	JobID       *string          `json:"job_id"`
	ClaimedAt   *time.Time       `json:"claimed_at"`
	ReadyAt     *time.Time       `json:"ready_at"`
}

func (s *Service) setupInput() (setup.Input, error) {
	rec, exists, err := s.Setup.Load()
	if err != nil {
		return setup.Input{}, err
	}
	in := setup.Input{Record: rec, Exists: exists, LockExists: s.lockExists(), DevBuild: DevBuild()}
	if sec, err := s.Auth.Security(); err == nil && sec != nil {
		in.Claimed = true
		t := sec.Claim.ClaimedAt
		in.ClaimedAt = &t
	}
	if rec.JobID != "" {
		if j, err := jobs.Load(s.Paths, rec.JobID); err == nil {
			in.JobState = j.State
		}
	}
	s.mu.Lock()
	in.CarePassword = s.carePassword != ""
	s.mu.Unlock()
	return in, nil
}

// SetupStateName returns the state of the setup machine.
func (s *Service) SetupStateName() string {
	in, err := s.setupInput()
	if err != nil {
		return setup.Unclaimed
	}
	return setup.StateName(in)
}

// Setup returns the setup state, the steps, and the draft.
func (s *Service) SetupState() (*SetupState, error) {
	in, err := s.setupInput()
	if err != nil {
		return nil, err
	}
	v := setup.Compute(in)
	out := &SetupState{State: v.State, DevBuild: in.DevBuild, Steps: v.Steps, CanFinish: v.CanFinish,
		ClaimedAt: in.ClaimedAt, ReadyAt: in.Record.ReadyAt}
	if v.Current != "" {
		c := v.Current
		out.CurrentStep = &c
	}
	if in.Record.JobID != "" && v.State != setup.Ready {
		id := in.Record.JobID
		out.JobID = &id
	}
	cfg, err := config.Load(s.configPath())
	if err != nil {
		cfg = config.Default()
	}
	out.Draft = draftOf(cfg)
	out.Draft.CareAdmin = CareAdminDraft{Username: strPtr(in.Record.CareAdmin.Username),
		Email: strPtr(in.Record.CareAdmin.Email), PasswordSet: in.CarePassword}
	return out, nil
}

func draftOf(cfg *config.Config) Draft {
	public := map[string]any{"enabled": cfg.TLS.Public.Enabled, "clinic_id": nil}
	if cfg.TLS.Public.ClinicID != "" {
		public["clinic_id"] = cfg.TLS.Public.ClinicID
	}
	size := map[string]any{"preset": cfg.Size.Preset}
	if cfg.Size.Preset == "custom" {
		size["custom"] = map[string]any{"gunicorn_workers": cfg.Size.Custom.GunicornWorkers,
			"celery_concurrency": cfg.Size.Custom.CeleryConcurrency, "pg_shared_buffers_mb": cfg.Size.Custom.PGSharedBuffersMB}
	}
	fe := map[string]any{"provider": cfg.FE.Provider, "artifact_url": nil, "artifact_sha256": nil}
	if cfg.FE.ArtifactURL != "" {
		fe["artifact_url"] = cfg.FE.ArtifactURL
	}
	if cfg.FE.ArtifactSHA256 != "" {
		fe["artifact_sha256"] = cfg.FE.ArtifactSHA256
	}
	env := func(m map[string]string) map[string]any {
		out := map[string]any{}
		for k, v := range m {
			out[k] = v
		}
		return out
	}
	return Draft{
		Identity: map[string]any{"display_name": cfg.Identity.DisplayName, "hostname": cfg.Identity.Hostname},
		TLS: map[string]any{"local_ca": map[string]any{"enabled": cfg.TLS.LocalCA.Enabled}, "public": public,
			"tailscale": map[string]any{"enabled": cfg.TLS.Tailscale.Enabled}},
		Size:    size,
		Env:     map[string]any{"backend": env(cfg.Env.Backend), "frontend": env(cfg.Env.Frontend), "fe": fe},
		Backups: nil,
	}
}

// AfterClaim records the claim. A host of Phase 1 (a lock and no setup record) is ready at once.
func (s *Service) AfterClaim(at time.Time) error {
	_, exists, err := s.Setup.Load()
	if err != nil {
		return err
	}
	legacy := !exists && s.lockExists()
	_, err = s.Setup.Update(func(r *setup.Record) error {
		t := at
		r.ClaimedAt = &t
		if legacy {
			r.ReadyAt = &t
			r.ApplyPhase = setup.PhaseFull
		}
		return nil
	})
	return err
}

// editable returns the record when the steps can change.
func (s *Service) editable() (*setup.Record, error) {
	in, err := s.setupInput()
	if err != nil {
		return nil, err
	}
	switch setup.StateName(in) {
	case setup.Applying:
		return nil, problem.Running(in.Record.JobID)
	case setup.Ready:
		return nil, problem.New(problem.SetupFinished, "Setup is finished. Use the config operations to change a value.")
	case setup.Unclaimed:
		return nil, problem.New(problem.NotClaimed, "The device is not claimed.")
	}
	return in.Record, nil
}

func (s *Service) markStep(id, status string, warnings []setup.Issue) error {
	_, err := s.Setup.Update(func(r *setup.Record) error {
		r.Steps[id] = &setup.StepRecord{Status: status, UpdatedAt: s.now(), Warnings: warnings}
		return nil
	})
	return err
}

func ptr(s string) *string { return &s }

// StepRecoveryCodes checks one of the recovery codes. It does not use the code.
func (s *Service) StepRecoveryCodes(code string) (*SetupState, error) {
	if _, err := s.editable(); err != nil {
		return nil, err
	}
	if !s.Auth.CheckRecoveryCode(code) {
		return nil, problem.Invalid("/confirm_code", "the code is not one of the recovery codes that are not used", "not-a-recovery-code")
	}
	if err := s.markStep(setup.StepRecoveryCodes, setup.Done, nil); err != nil {
		return nil, err
	}
	return s.SetupState()
}

// StepHardware runs the host check again. A failed required check gives host-check-failed.
func (s *Service) StepHardware(ctx context.Context) (*SetupState, error) {
	if _, err := s.editable(); err != nil {
		return nil, err
	}
	hc := s.HostCheckView(ctx, false)
	if err := hostCheckProblem(hc); err != nil {
		return nil, err
	}
	var warn []setup.Issue
	hw := hc.Hardware
	if hw.RootDisk.Kind == "sd" {
		warn = append(warn, setup.Issue{Code: "sd-card", Message: "the root disk is an SD card"})
	}
	if hw.RootDisk.SizeBytes > 0 && hw.RootDisk.SizeBytes < 64e9 {
		warn = append(warn, setup.Issue{Code: "small-disk", Message: "the root disk is smaller than 64 GB"})
	}
	if hw.DiskProbe != nil && hw.DiskProbe.WriteMBs < 20 {
		warn = append(warn, setup.Issue{Code: "slow-disk", Message: "the root disk writes less than 20 MB/s"})
	}
	if hw.InternetReachable != nil && !*hw.InternetReachable {
		warn = append(warn, setup.Issue{Code: "no-internet", Message: "the device cannot reach the internet; the first apply needs it for the images"})
	}
	if err := s.markStep(setup.StepHardware, setup.Done, warn); err != nil {
		return nil, err
	}
	return s.SetupState()
}

func hostCheckProblem(hc *HostCheck) error {
	if hc.OK {
		return nil
	}
	var failed []string
	var detail string
	for _, c := range hc.Checks {
		if c.Level == "required" && !c.OK {
			failed = append(failed, c.Name)
			if detail == "" {
				detail = c.Name + ": " + c.Detail
			}
		}
	}
	p := problem.New(problem.HostCheckFailed, detail)
	p.FailedChecks = failed
	return p
}

// updateConfig changes the keys of config.yaml with fn, validates the result, and writes it.
// base prefixes the pointers of the errors (the place of the changed keys in the request body).
func (s *Service) updateConfig(fn func(doc map[string]any)) error {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	doc, _, err := s.readConfigDoc()
	if err != nil {
		return err
	}
	fn(doc)
	data, err := marshalConfig(doc)
	if err != nil {
		return err
	}
	if _, err := config.Parse(data, "config.yaml"); err != nil {
		return configProblem(err)
	}
	return s.writeConfig(data)
}

func section(doc map[string]any, key string) map[string]any {
	m, ok := doc[key].(map[string]any)
	if !ok {
		m = map[string]any{}
		doc[key] = m
	}
	return m
}

// StepIdentity writes identity.display_name and identity.hostname.
func (s *Service) StepIdentity(displayName, hostname string) (*SetupState, error) {
	if _, err := s.editable(); err != nil {
		return nil, err
	}
	old := ""
	if cfg, err := config.Load(s.configPath()); err == nil {
		old = cfg.Identity.Hostname
	}
	err := s.updateConfig(func(doc map[string]any) {
		id := section(doc, "identity")
		id["display_name"], id["hostname"] = displayName, hostname
	})
	if err != nil {
		return nil, rebase(err, "/identity", "")
	}
	var warn []setup.Issue
	if old != "" && old != hostname {
		warn = append(warn, setup.Issue{Code: "hostname-changes-url", Pointer: ptr("/hostname"),
			Message: fmt.Sprintf("the finish moves the console to https://%s:%d/; open the new address and log in again",
				netinfo.MDNSName(hostname), paths.ConsolePort)})
	}
	if err := s.markStep(setup.StepIdentity, setup.Done, warn); err != nil {
		return nil, err
	}
	return s.SetupState()
}

// rebase moves the pointers of an invalid-config problem from the config document into the step body.
func rebase(err error, from, to string) error {
	p, ok := err.(*problem.Problem)
	if !ok {
		return err
	}
	for i, e := range p.Errors {
		if rest, ok := strings.CutPrefix(e.Pointer, from); ok {
			p.Errors[i].Pointer = to + rest
		}
	}
	return p
}

// StepTLS writes tls.*. Phase 2 accepts only the local CA mode.
func (s *Service) StepTLS(body map[string]any) (*SetupState, error) {
	if _, err := s.editable(); err != nil {
		return nil, err
	}
	cand, err := s.candidate(func(doc map[string]any) { doc["tls"] = body })
	if err != nil {
		return nil, rebase(err, "/tls", "")
	}
	if p := unsupported(cand, "/tls"); p != nil {
		return nil, p
	}
	if err := s.updateConfig(func(doc map[string]any) { doc["tls"] = body }); err != nil {
		return nil, rebase(err, "/tls", "")
	}
	if err := s.markStep(setup.StepTLS, setup.Done, nil); err != nil {
		return nil, err
	}
	return s.SetupState()
}

// candidate returns the config after fn without a write.
func (s *Service) candidate(fn func(doc map[string]any)) (*config.Config, error) {
	doc, _, err := s.readConfigDoc()
	if err != nil {
		return nil, err
	}
	fn(doc)
	data, err := marshalConfig(doc)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Parse(data, "config.yaml")
	if err != nil {
		return nil, configProblem(err)
	}
	return cfg, nil
}

// StepSize writes size.*. busy-counter on less than 3 GiB RAM gives a warning.
func (s *Service) StepSize(body map[string]any) (*SetupState, error) {
	if _, err := s.editable(); err != nil {
		return nil, err
	}
	if err := s.updateConfig(func(doc map[string]any) { doc["size"] = body }); err != nil {
		return nil, rebase(err, "/size", "")
	}
	var warn []setup.Issue
	if body["preset"] == "busy-counter" {
		if ram := memTotal(s.Paths); ram > 0 && ram < 3<<30 {
			warn = append(warn, setup.Issue{Code: "busy-counter-low-ram", Pointer: ptr("/preset"),
				Message: fmt.Sprintf("busy-counter needs 4 GiB RAM or more; this host has %d MiB", ram>>20)})
		}
	}
	if err := s.markStep(setup.StepSize, setup.Done, warn); err != nil {
		return nil, err
	}
	return s.SetupState()
}

// StepEnv writes env.backend, env.frontend, and fe.
func (s *Service) StepEnv(backend, frontend, fe map[string]any) (*SetupState, error) {
	if _, err := s.editable(); err != nil {
		return nil, err
	}
	err := s.updateConfig(func(doc map[string]any) {
		env := section(doc, "env")
		env["backend"], env["frontend"] = backend, frontend
		doc["fe"] = fe
	})
	if err != nil {
		return nil, rebase(rebase(err, "/env", ""), "/fe", "/fe")
	}
	if err := s.markStep(setup.StepEnv, setup.Done, nil); err != nil {
		return nil, err
	}
	return s.SetupState()
}

var careUserRE = strings.NewReplacer("..", "", "._", "", ".-", "", "_.", "", "__", "", "_-", "", "-.", "", "-_", "", "--", "")

// CheckCareUsername applies the Care rule: no two of "._-" in a row.
func CheckCareUsername(u string) error {
	if careUserRE.Replace(u) != u {
		return problem.Invalid("/username", "the user name has two of . _ - in a row", "pattern")
	}
	return nil
}

func (s *Service) checkCarePassword(pw, pointer string) error {
	if reason, code := auth.CheckPassword(pw); reason != "" {
		return problem.New(problem.WeakPassword, "The password is too weak: "+reason+".").
			WithErrors(problem.FieldError{Pointer: pointer, Message: reason, Code: code})
	}
	if s.Auth.VerifyConsolePassword(pw) {
		return problem.New(problem.WeakPassword, "The Care superuser password must not be the console password.").
			WithErrors(problem.FieldError{Pointer: pointer, Message: "the password is the console password", Code: "reused-password"})
	}
	return nil
}

// StepCareAdmin keeps the user name and the email in the record and the password in memory only.
func (s *Service) StepCareAdmin(username, email, password string) (*SetupState, error) {
	if _, err := s.editable(); err != nil {
		return nil, err
	}
	if err := CheckCareUsername(username); err != nil {
		return nil, err
	}
	if err := s.checkCarePassword(password, "/password"); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.carePassword = password
	s.mu.Unlock()
	_, err := s.Setup.Update(func(r *setup.Record) error {
		r.CareAdmin = setup.CareAdmin{Username: username, Email: email}
		r.Steps[setup.StepCareAdmin] = &setup.StepRecord{Status: setup.Done, UpdatedAt: s.now()}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.SetupState()
}

// StepBackups is Phase 3.
func (s *Service) StepBackups() (*SetupState, error) {
	return nil, problem.Later(3, "The backups step comes in Phase 3. A dev build can skip it.")
}

// Skip skips an optional step: env always, backups only in a dev build.
func (s *Service) Skip(step string) (*SetupState, error) {
	if _, err := s.editable(); err != nil {
		return nil, err
	}
	if !setup.Skippable(step, DevBuild()) {
		msg := "Only env can be skipped, and backups in a dev build."
		if step == setup.StepBackups {
			msg = "This is a release build. The backups step is required, and it comes in Phase 3."
		}
		return nil, problem.New(problem.StepNotSkippable, msg)
	}
	if err := s.markStep(step, setup.Skipped, nil); err != nil {
		return nil, err
	}
	return s.SetupState()
}

// CarePasswordSet reports whether the agent has the Care superuser password.
func (s *Service) CarePasswordSet() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.carePassword != ""
}

// FinishSetup checks the steps, the config, and the host, then starts the setup job.
func (s *Service) FinishSetup(ctx context.Context, source string) (*jobs.Job, error) {
	in, err := s.setupInput()
	if err != nil {
		return nil, err
	}
	v := setup.Compute(in)
	switch v.State {
	case setup.Applying:
		return nil, problem.Running(in.Record.JobID)
	case setup.Ready:
		return nil, problem.New(problem.SetupFinished, "Setup is finished.")
	case setup.Unclaimed:
		return nil, problem.New(problem.NotClaimed, "The device is not claimed.")
	}
	if len(v.Missing) > 0 {
		p := problem.Newf(problem.SetupIncomplete, "Complete the steps in missing_steps first: %s.", strings.Join(v.Missing, ", "))
		p.MissingSteps = v.Missing
		return nil, p
	}
	cfg, err := s.Config()
	if err != nil {
		return nil, err
	}
	if p := unsupported(cfg, ""); p != nil {
		return nil, p
	}
	if err := hostCheckProblem(s.HostCheckView(ctx, false)); err != nil {
		return nil, err
	}
	s.mu.Lock()
	password := s.carePassword
	s.mu.Unlock()
	admin := in.Record.CareAdmin
	return s.Jobs.Start(ctx, jobs.Spec{Kind: jobs.KindSetup, Source: source, Reason: "setup finish", Lock: true,
		OnStart: func(id string) {
			_, err := s.Setup.Update(func(r *setup.Record) error {
				r.JobID = id
				r.ApplyPhase = setup.PhaseFull
				return nil
			})
			if err != nil {
				s.log().Error("cannot record the setup job", "error", err)
			}
		}}, func(ctx context.Context, rec *jobs.Recorder) (any, error) {
		return s.runSetup(ctx, rec, admin, password)
	})
}

func (s *Service) runSetup(ctx context.Context, rec *jobs.Recorder, admin setup.CareAdmin, password string) (any, error) {
	end := rec.StepAction("validate: config.yaml", "validate")
	cfg, err := s.Config()
	end(err)
	if err != nil {
		return nil, err
	}
	end = rec.StepAction("host-check: required checks", "host-check")
	err = hostCheckProblem(s.HostCheckView(ctx, false))
	end(err)
	if err != nil {
		return nil, err
	}
	if err := s.setHostname(ctx, rec, cfg.Identity.Hostname); err != nil {
		return nil, err
	}
	o := s.applyOptions(apply.Options{Reason: "setup finish", Out: rec.Writer(), Journal: rec, LockHeld: true,
		Phase: setup.PhaseFull})
	if _, err := apply.Run(ctx, o); err != nil {
		return nil, applyProblem(err)
	}
	end = rec.StepAction("wait-healthy: care-api.service", "wait-healthy")
	err = s.waitCareHealthy(ctx, rec)
	end(err)
	if err != nil {
		return nil, err
	}
	end = rec.StepAction("create-care-superuser: "+admin.Username, "create-care-superuser")
	_, err = s.createSuperuser(ctx, admin.Username, admin.Email, password)
	if problem.Is(err, problem.CareUserExists) {
		rec.Logf("the Care user %s exists; the setup keeps it", admin.Username)
		rec.SetDetail("the user exists")
		err = nil
	}
	end(err)
	if err != nil {
		return nil, err
	}
	now := s.now()
	if _, err := s.Setup.Update(func(r *setup.Record) error {
		r.ReadyAt = &now
		r.JobID = ""
		return nil
	}); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.carePassword = ""
	s.mu.Unlock()
	primary := s.PrimaryHost()
	if h := cfg.Identity.Hostname; cfg.TLS.LocalCA.Enabled && h != "" {
		primary = netinfo.MDNSName(h)
	}
	user := admin.Username
	return jobs.SetupResult{Kind: jobs.KindSetup, CareURL: "https://" + primary + "/",
		ConsoleURL: fmt.Sprintf("https://%s:%d/", primary, paths.ConsolePort), HandoffURL: "https://" + primary + "/login",
		URLs: s.CareURLs(), CareUsername: &user}, nil
}

// waitCareHealthy polls the health of care-api.
func (s *Service) waitCareHealthy(ctx context.Context, rec *jobs.Recorder) error {
	timeout, poll := s.HealthTimeout, s.PollInterval
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	if poll == 0 {
		poll = 2 * time.Second
	}
	deadline := time.Now().Add(timeout)
	said := false
	for {
		if s.careHealthy(ctx) {
			return nil
		}
		if time.Now().After(deadline) {
			return problem.Newf(problem.CareNotRunning, "care-api.service did not become healthy in %s.", timeout)
		}
		if !said && rec != nil {
			rec.Logf("waiting for care-api.service to become healthy")
			said = true
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

func (s *Service) careHealthy(ctx context.Context) bool {
	h, err := s.Host.Podman.Health(ctx, "care")
	return err == nil && h["care-api"] == "healthy"
}

// readConfigDoc reads config.yaml as a JSON-compatible map and returns the ETag.
func (s *Service) readConfigDoc() (map[string]any, string, error) {
	data, err := readFile(s.configPath())
	if err != nil {
		return nil, "", problem.New(problem.InvalidConfig, "cannot read config.yaml: "+err.Error())
	}
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, "", configProblem(&config.ValidationError{Source: "config.yaml",
			Details: []config.Detail{{Message: "YAML syntax: " + err.Error(), Keyword: "syntax"}}})
	}
	doc, _ := config.Normalize(raw).(map[string]any)
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, etagOf(data), nil
}
