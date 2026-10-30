package server

import (
	"errors"
	"net"
	"net/http"
	"net/mail"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/auth"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/service"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
)

type opt func(*route)

func public(r *route)     { r.public = true }
func publicRead(r *route) { r.public, r.publicRead = true, true }
func both(r *route)       { r.both = true }
func rootOnly(r *route)   { r.rootOnly = true }
func reauth(r *route)     { r.reauth = true }
func phase(n int) opt     { return func(r *route) { r.phase = n } }
func media(mt string) opt { return func(r *route) { r.mediaType = mt } }

// credBody gives the body limit of a credential operation, bigBody the limit of the config.
func credBody(r *route) { r.maxBody = CredentialBody }
func bigBody(r *route)  { r.maxBody = MaxBody }

func notImplemented(*reqCtx) error { return nil }

// table lists each operation of docs/api/openapi.yaml.
func (s *Server) table() []*route {
	var out []*route
	add := func(method, path string, h func(*reqCtx) error, opts ...opt) {
		r := &route{method: method, path: path, segs: split(path), h: h}
		for _, o := range opts {
			o(r)
		}
		out = append(out, r)
	}
	get, post, put, patch, del := http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete

	add(get, "/status", s.publicStatus, publicRead, rootOnly)
	add(get, "/health", s.health, publicRead, both)
	add(get, "/host/check", s.hostCheck)

	add(get, "/session", s.getSession)
	add(del, "/session", s.logout)
	add(post, "/session/claim", s.claim, public, credBody)
	add(post, "/session/login", s.login, public, credBody)
	add(post, "/session/recover", s.recover, public, credBody)
	add(post, "/session/reauth", s.reauth, credBody)

	add(get, "/security", s.security)
	add(put, "/security/password", s.changePassword)
	add(post, "/security/recovery-codes", s.recoveryCodes, reauth)
	add(post, "/security/totp", s.startTOTP, reauth)
	add(del, "/security/totp", s.disableTOTP, reauth)
	add(post, "/security/totp/verify", s.verifyTOTP, credBody)
	add(get, "/security/sessions", s.sessions)
	add(del, "/security/sessions/{session_ref}", s.revokeSession)
	add(put, "/security/ssh", notImplemented, reauth, phase(7))

	add(get, "/setup", s.setupState)
	add(put, "/setup/steps/recovery-codes", s.stepRecoveryCodes)
	add(put, "/setup/steps/hardware", s.stepHardware)
	add(put, "/setup/steps/identity", s.stepIdentity)
	add(put, "/setup/steps/tls", s.stepTLS, bigBody)
	add(put, "/setup/steps/size", s.stepSize)
	add(put, "/setup/steps/env", s.stepEnv, bigBody)
	add(put, "/setup/steps/backups", notImplemented, phase(3))
	add(put, "/setup/steps/care-admin", s.stepCareAdmin)
	add(post, "/setup/steps/{step}/skip", s.skipStep)
	add(post, "/setup/finish", s.finishSetup)

	add(get, "/status", s.status)
	add(get, "/config", s.getConfig)
	add(put, "/config", s.putConfig, bigBody)
	add(patch, "/config", s.patchConfig, media("application/merge-patch+json"), bigBody)
	add(get, "/config/schema", s.configSchema)
	add(get, "/config/presets", s.presets)
	add(post, "/apply", s.startApply)
	add(get, "/apply/plan", s.plan)

	add(get, "/jobs", s.listJobs)
	add(get, "/jobs/{job_id}", s.getJob)
	add(get, "/jobs/{job_id}/events", s.jobEvents)
	add(post, "/jobs/{job_id}/cancel", s.cancelJob)

	add(get, "/logs", s.logUnits)
	add(get, "/logs/{unit}", s.logs)
	add(post, "/support-bundle", s.supportBundle)
	add(get, "/support-bundle/{bundle_id}", s.downloadBundle)

	add(get, "/network", s.network)
	add(put, "/network/wifi", notImplemented, phase(6))
	add(get, "/tls", s.tls)
	add(get, "/tls/root-ca", s.rootCA, publicRead, both)
	add(get, "/tls/root-ca/mobileconfig", s.mobileconfig, publicRead, both)
	add(get, "/tls/instructions/{os}", s.instructions, publicRead, both)
	add(put, "/tls/public", notImplemented, phase(6))
	add(put, "/tls/tailscale", notImplemented, reauth, phase(6))
	add(post, "/tls/local-ca/rotate", notImplemented, reauth, phase(7))

	add(get, "/fe", s.fe)
	add(post, "/fe/build", s.feBuild)
	add(post, "/fe/prune", s.fePrune)
	add(get, "/care", s.care)
	add(post, "/care/superuser", s.careSuperuser)
	add(post, "/power/reboot", s.power("reboot"))
	add(post, "/power/shutdown", s.power("shutdown"), reauth)

	add(get, "/backups", notImplemented, phase(3))
	add(post, "/backups/run", notImplemented, phase(3))
	add(post, "/backups/destinations/test", notImplemented, phase(3))
	add(get, "/backups/recovery-key", notImplemented, reauth, phase(3))
	add(get, "/restore/points", notImplemented, phase(3))
	add(post, "/restore", notImplemented, reauth, phase(3))
	add(get, "/updates", notImplemented, phase(4))
	add(post, "/updates/check", notImplemented, phase(4))
	add(post, "/updates/apply", notImplemented, phase(4))
	add(post, "/updates/rollback", notImplemented, reauth, phase(4))
	add(get, "/plugs", notImplemented, phase(4))
	add(put, "/plugs/{plug_id}", notImplemented, phase(4))
	return out
}

// ---- system ----

func (s *Server) publicStatus(c *reqCtx) error {
	s.json(c.w, http.StatusOK, s.svc.PublicStatus(c.r.Context()))
	return nil
}

func (s *Server) health(c *reqCtx) error {
	select {
	case <-s.shutdown:
		p := problem.New(problem.Unavailable, "The agent stops.")
		p.RetryAfter = 5
		return p
	default:
	}
	s.json(c.w, http.StatusOK, map[string]any{"status": "ok", "time": s.a.Now().Truncate(time.Second)})
	return nil
}

func (s *Server) hostCheck(c *reqCtx) error {
	probe, err := c.boolQuery("probe")
	if err != nil {
		return err
	}
	s.json(c.w, http.StatusOK, s.svc.HostCheckView(c.r.Context(), probe))
	return nil
}

// ---- session ----

type sessionView struct {
	Ref                    string    `json:"ref"`
	Method                 string    `json:"method"`
	CreatedAt              time.Time `json:"created_at"`
	AuthenticatedAt        time.Time `json:"authenticated_at"`
	ReauthUntil            time.Time `json:"reauth_until"`
	IdleExpiresAt          time.Time `json:"idle_expires_at"`
	ExpiresAt              time.Time `json:"expires_at"`
	CSRFToken              string    `json:"csrf_token"`
	TOTPEnabled            bool      `json:"totp_enabled"`
	RecoveryCodesRemaining int       `json:"recovery_codes_remaining"`
	SetupState             string    `json:"setup_state"`
}

func (s *Server) view(sess *auth.Session) sessionView {
	st := s.a.Status()
	return sessionView{Ref: sess.Ref(), Method: sess.Method, CreatedAt: sess.CreatedAt, AuthenticatedAt: sess.AuthenticatedAt,
		ReauthUntil: sess.ReauthUntil(), IdleExpiresAt: sess.IdleExpiresAt(), ExpiresAt: sess.ExpiresAt(),
		CSRFToken: sess.CSRF, TOTPEnabled: st.TOTPEnabled, RecoveryCodesRemaining: st.Remaining,
		SetupState: s.svc.SetupStateName()}
}

func (s *Server) getSession(c *reqCtx) error {
	s.json(c.w, http.StatusOK, s.view(c.session))
	return nil
}

func (s *Server) logout(c *reqCtx) error {
	if err := s.a.Logout(c.sessionID); err != nil {
		return err
	}
	s.clearCookie(c.w)
	c.w.WriteHeader(http.StatusNoContent)
	return nil
}

func lengthOK(ptr, v string, min, max int) error {
	n := len([]rune(v))
	switch {
	case n < min:
		return problem.Invalid(ptr, "the value is too short", "minLength")
	case n > max:
		return problem.Invalid(ptr, "the value is too long", "maxLength")
	}
	return nil
}

var (
	totpRE     = regexp.MustCompile(`^[0-9]{6}$`)
	codeRE     = regexp.MustCompile(`^[0-9A-Za-z -]+$`)
	careUserRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,14}[a-z0-9]$`)
	refRE      = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

func checkCode(ptr, v string) error {
	if err := lengthOK(ptr, v, 10, 16); err != nil {
		return err
	}
	if !codeRE.MatchString(v) {
		return problem.Invalid(ptr, "the code has characters that are not allowed", "pattern")
	}
	return nil
}

func (s *Server) claim(c *reqCtx) error {
	var req struct {
		Method      *string `json:"method"`
		Secret      *string `json:"secret"`
		NewPassword *string `json:"new_password"`
	}
	if err := c.decode(&req, true); err != nil {
		return err
	}
	switch {
	case req.Method == nil:
		return problem.Invalid("/method", "method is required", "required")
	case *req.Method != auth.ClaimOSPassword && *req.Method != auth.ClaimCode:
		return problem.Invalid("/method", "use os-password or claim-code", "enum")
	case req.Secret == nil:
		return problem.Invalid("/secret", "secret is required", "required")
	case req.NewPassword == nil:
		return problem.Invalid("/new_password", "new_password is required", "required")
	}
	if err := lengthOK("/secret", *req.Secret, 1, 256); err != nil {
		return err
	}
	g, err := s.a.Claim(c.client, *req.Method, *req.Secret, *req.NewPassword)
	if err != nil {
		return err
	}
	if err := s.svc.AfterClaim(g.Session.CreatedAt); err != nil {
		s.log.Error("cannot record the claim in the setup record", "error", err)
	}
	s.setCookie(c.w, g.ID)
	s.json(c.w, http.StatusCreated, map[string]any{"session": s.view(g.Session), "recovery_codes": g.Codes})
	return nil
}

func (s *Server) login(c *reqCtx) error {
	var req struct {
		Password *string `json:"password"`
		TOTPCode *string `json:"totp_code"`
	}
	if err := c.decode(&req, true); err != nil {
		return err
	}
	if req.Password == nil {
		return problem.Invalid("/password", "password is required", "required")
	}
	if err := lengthOK("/password", *req.Password, 1, 128); err != nil {
		return err
	}
	code := ""
	if req.TOTPCode != nil {
		if !totpRE.MatchString(*req.TOTPCode) {
			return problem.Invalid("/totp_code", "use 6 digits", "pattern")
		}
		code = *req.TOTPCode
	}
	g, err := s.a.Login(c.client, *req.Password, code)
	if err != nil {
		return err
	}
	s.setCookie(c.w, g.ID)
	s.json(c.w, http.StatusOK, s.view(g.Session))
	return nil
}

func (s *Server) recover(c *reqCtx) error {
	var req struct {
		RecoveryCode *string `json:"recovery_code"`
		NewPassword  *string `json:"new_password"`
	}
	if err := c.decode(&req, true); err != nil {
		return err
	}
	if req.RecoveryCode == nil {
		return problem.Invalid("/recovery_code", "recovery_code is required", "required")
	}
	if req.NewPassword == nil {
		return problem.Invalid("/new_password", "new_password is required", "required")
	}
	if err := checkCode("/recovery_code", *req.RecoveryCode); err != nil {
		return err
	}
	g, err := s.a.Recover(c.client, *req.RecoveryCode, *req.NewPassword)
	if err != nil {
		return err
	}
	s.setCookie(c.w, g.ID)
	s.json(c.w, http.StatusOK, map[string]any{"session": s.view(g.Session), "recovery_codes_remaining": g.Remaining})
	return nil
}

func (s *Server) reauth(c *reqCtx) error {
	var req struct {
		Password *string `json:"password"`
	}
	if err := c.decode(&req, true); err != nil {
		return err
	}
	if req.Password == nil {
		return problem.Invalid("/password", "password is required", "required")
	}
	if err := lengthOK("/password", *req.Password, 1, 128); err != nil {
		return err
	}
	sess, err := s.a.Reauth(c.sessionID, c.client, *req.Password)
	if err != nil {
		return err
	}
	s.json(c.w, http.StatusOK, s.view(sess))
	return nil
}

// ---- security ----

func (s *Server) security(c *reqCtx) error {
	st, err := s.a.Security()
	if err != nil {
		return err
	}
	if st == nil {
		return problem.New(problem.NotClaimed, "The device is not claimed.")
	}
	s.json(c.w, http.StatusOK, st)
	return nil
}

func (s *Server) changePassword(c *reqCtx) error {
	var req struct {
		Current *string `json:"current_password"`
		New     *string `json:"new_password"`
	}
	if err := c.decode(&req, true); err != nil {
		return err
	}
	if req.Current == nil {
		return problem.Invalid("/current_password", "current_password is required", "required")
	}
	if req.New == nil {
		return problem.Invalid("/new_password", "new_password is required", "required")
	}
	if err := lengthOK("/current_password", *req.Current, 1, 128); err != nil {
		return err
	}
	g, err := s.a.ChangePassword(c.sessionID, c.client, *req.Current, *req.New)
	if err != nil {
		return err
	}
	s.setCookie(c.w, g.ID)
	s.json(c.w, http.StatusOK, s.view(g.Session))
	return nil
}

func (s *Server) recoveryCodes(c *reqCtx) error {
	codes, at, err := s.a.RegenerateCodes()
	if err != nil {
		return err
	}
	s.json(c.w, http.StatusOK, map[string]any{"codes": codes, "generated_at": at})
	return nil
}

func (s *Server) requestHost(c *reqCtx) string {
	if h, _, err := net.SplitHostPort(c.r.Host); err == nil {
		return h
	}
	if c.r.Host != "" {
		return c.r.Host
	}
	return s.svc.PrimaryHost()
}

func (s *Server) startTOTP(c *reqCtx) error {
	e, err := s.a.StartTOTP(s.requestHost(c))
	if err != nil {
		return err
	}
	s.json(c.w, http.StatusOK, e)
	return nil
}

func (s *Server) disableTOTP(c *reqCtx) error {
	if err := s.a.DisableTOTP(); err != nil {
		return err
	}
	c.w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) verifyTOTP(c *reqCtx) error {
	var req struct {
		Code *string `json:"code"`
	}
	if err := c.decode(&req, true); err != nil {
		return err
	}
	if req.Code == nil || !totpRE.MatchString(*req.Code) {
		return problem.Invalid("/code", "use 6 digits", "pattern")
	}
	if err := s.a.VerifyTOTPEnrolment(c.client, *req.Code); err != nil {
		return err
	}
	return s.security(c)
}

func (s *Server) sessions(c *reqCtx) error {
	s.json(c.w, http.StatusOK, map[string]any{"items": s.a.Sessions(c.sessionID)})
	return nil
}

func (s *Server) revokeSession(c *reqCtx) error {
	ref := c.param("session_ref")
	if !refRE.MatchString(ref) {
		return problem.Newf(problem.NotFound, "No session has the reference %s.", ref)
	}
	if err := s.a.Revoke(ref); err != nil {
		if errors.Is(err, auth.ErrNoSession) {
			return problem.Newf(problem.NotFound, "No session has the reference %s.", ref)
		}
		return err
	}
	c.w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- setup ----

func (s *Server) setupResult(c *reqCtx, st *service.SetupState, err error) error {
	if err != nil {
		return err
	}
	s.json(c.w, http.StatusOK, st)
	return nil
}

func (s *Server) setupState(c *reqCtx) error {
	st, err := s.svc.SetupState()
	return s.setupResult(c, st, err)
}

func (s *Server) stepRecoveryCodes(c *reqCtx) error {
	var req struct {
		Code *string `json:"confirm_code"`
	}
	if err := c.decode(&req, true); err != nil {
		return err
	}
	if req.Code == nil {
		return problem.Invalid("/confirm_code", "confirm_code is required", "required")
	}
	if err := checkCode("/confirm_code", *req.Code); err != nil {
		return err
	}
	st, err := s.svc.StepRecoveryCodes(*req.Code)
	return s.setupResult(c, st, err)
}

func (s *Server) stepHardware(c *reqCtx) error {
	var req struct {
		Acknowledged *bool `json:"acknowledged"`
	}
	if err := c.decode(&req, true); err != nil {
		return err
	}
	if req.Acknowledged == nil || !*req.Acknowledged {
		return problem.Invalid("/acknowledged", "acknowledged must be true", "const")
	}
	st, err := s.svc.StepHardware(c.r.Context())
	return s.setupResult(c, st, err)
}

func (s *Server) stepIdentity(c *reqCtx) error {
	var req struct {
		DisplayName *string `json:"display_name"`
		Hostname    *string `json:"hostname"`
	}
	if err := c.decode(&req, true); err != nil {
		return err
	}
	if req.DisplayName == nil {
		return problem.Invalid("/display_name", "display_name is required", "required")
	}
	if req.Hostname == nil {
		return problem.Invalid("/hostname", "hostname is required", "required")
	}
	st, err := s.svc.StepIdentity(*req.DisplayName, *req.Hostname)
	return s.setupResult(c, st, err)
}

func (s *Server) object(c *reqCtx) (map[string]any, error) {
	var m map[string]any
	if err := c.decode(&m, true); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, problem.Invalid("", "the body must be a JSON object", "type")
	}
	return m, nil
}

func (s *Server) stepTLS(c *reqCtx) error {
	m, err := s.object(c)
	if err != nil {
		return err
	}
	st, err := s.svc.StepTLS(m)
	return s.setupResult(c, st, err)
}

func (s *Server) stepSize(c *reqCtx) error {
	m, err := s.object(c)
	if err != nil {
		return err
	}
	st, err := s.svc.StepSize(m)
	return s.setupResult(c, st, err)
}

func (s *Server) stepEnv(c *reqCtx) error {
	var req struct {
		Backend  map[string]any `json:"backend"`
		Frontend map[string]any `json:"frontend"`
		FE       map[string]any `json:"fe"`
	}
	if err := c.decode(&req, true); err != nil {
		return err
	}
	for ptr, v := range map[string]map[string]any{"/backend": req.Backend, "/frontend": req.Frontend, "/fe": req.FE} {
		if v == nil {
			return problem.Invalid(ptr, strings.TrimPrefix(ptr, "/")+" is required", "required")
		}
	}
	st, err := s.svc.StepEnv(req.Backend, req.Frontend, req.FE)
	return s.setupResult(c, st, err)
}

func checkEmail(ptr, v string) error {
	a, err := mail.ParseAddress(v)
	if err != nil || a.Address != v || len(v) > 254 {
		return problem.Invalid(ptr, "the value is not an email address", "format")
	}
	return nil
}

func checkCareUser(ptr, v string) error {
	if !careUserRE.MatchString(v) {
		return problem.Invalid(ptr, "use 4 to 16 characters: a-z, 0-9, and . _ - (not at the start or the end)", "pattern")
	}
	return nil
}

type careUserReq struct {
	Username *string `json:"username"`
	Email    *string `json:"email"`
	Password *string `json:"password"`
}

func (r careUserReq) check() error {
	switch {
	case r.Username == nil:
		return problem.Invalid("/username", "username is required", "required")
	case r.Email == nil:
		return problem.Invalid("/email", "email is required", "required")
	case r.Password == nil:
		return problem.Invalid("/password", "password is required", "required")
	}
	if err := checkCareUser("/username", *r.Username); err != nil {
		return err
	}
	return checkEmail("/email", *r.Email)
}

func (s *Server) stepCareAdmin(c *reqCtx) error {
	var req careUserReq
	if err := c.decode(&req, true); err != nil {
		return err
	}
	if err := req.check(); err != nil {
		return err
	}
	st, err := s.svc.StepCareAdmin(*req.Username, *req.Email, *req.Password)
	return s.setupResult(c, st, err)
}

func (s *Server) skipStep(c *reqCtx) error {
	step := c.param("step")
	if !setup.Known(step) {
		return problem.Newf(problem.StepNotSkippable, "The step %q does not exist.", step)
	}
	st, err := s.svc.Skip(step)
	return s.setupResult(c, st, err)
}

func (s *Server) accepted(c *reqCtx, j *jobs.Job, err error) error {
	if err != nil {
		return err
	}
	c.w.Header().Set("Location", APIPrefix+"/jobs/"+j.ID)
	s.json(c.w, http.StatusAccepted, j)
	return nil
}

func (s *Server) finishSetup(c *reqCtx) error {
	j, err := s.svc.FinishSetup(c.r.Context(), jobs.SourceConsole)
	return s.accepted(c, j, err)
}

// ---- status and config ----

func (s *Server) status(c *reqCtx) error {
	st, err := s.svc.Status(c.r.Context())
	if err != nil {
		return err
	}
	s.json(c.w, http.StatusOK, st)
	return nil
}

func (s *Server) getConfig(c *reqCtx) error {
	view, ok := c.query("view")
	if !ok {
		view = "effective"
	}
	if view != "effective" && view != "file" {
		return problem.InvalidParam("view", "use effective or file")
	}
	doc, etag, err := s.svc.ConfigView(view)
	if err != nil {
		return err
	}
	c.w.Header().Set("ETag", etag)
	s.json(c.w, http.StatusOK, doc)
	return nil
}

func (s *Server) writeConfig(c *reqCtx, patch bool) error {
	dry, err := c.boolQuery("dry_run")
	if err != nil {
		return err
	}
	ap, err := c.boolQuery("apply")
	if err != nil {
		return err
	}
	ifMatch := c.r.Header.Get("If-Match")
	if ifMatch == "" {
		return problem.New(problem.PreconditionRequired, "Read the config with GET /config, then send its ETag in If-Match.")
	}
	body, err := s.object(c)
	if err != nil {
		return err
	}
	res, err := s.svc.WriteConfig(c.r.Context(), service.ConfigWrite{IfMatch: ifMatch, DryRun: dry, Apply: ap,
		Source: jobs.SourceConsole, Body: body, Patch: patch})
	if err != nil {
		return err
	}
	c.w.Header().Set("ETag", res.ETag)
	if res.Job != nil {
		c.w.Header().Set("Location", APIPrefix+"/jobs/"+res.Job.ID)
		s.json(c.w, http.StatusAccepted, res)
		return nil
	}
	s.json(c.w, http.StatusOK, res)
	return nil
}

func (s *Server) putConfig(c *reqCtx) error   { return s.writeConfig(c, false) }
func (s *Server) patchConfig(c *reqCtx) error { return s.writeConfig(c, true) }

func (s *Server) configSchema(c *reqCtx) error {
	c.w.Header().Set("Content-Type", "application/schema+json")
	_, err := c.w.Write(config.SchemaJSON())
	return err
}

func (s *Server) presets(c *reqCtx) error {
	s.json(c.w, http.StatusOK, s.svc.Presets())
	return nil
}

func (s *Server) startApply(c *reqCtx) error {
	var req struct {
		Reason *string `json:"reason"`
		Update *bool   `json:"update"`
	}
	if err := c.decode(&req, false); err != nil {
		return err
	}
	ar := service.ApplyRequest{}
	if req.Reason != nil {
		if err := lengthOK("/reason", *req.Reason, 0, 200); err != nil {
			return err
		}
		ar.Reason = *req.Reason
	}
	if req.Update != nil {
		ar.Update = *req.Update
	}
	j, err := s.svc.StartApply(c.r.Context(), ar, jobs.SourceConsole)
	return s.accepted(c, j, err)
}

func (s *Server) plan(c *reqCtx) error {
	update, err := c.boolQuery("update")
	if err != nil {
		return err
	}
	diff, err := c.boolQuery("diff")
	if err != nil {
		return err
	}
	pv, err := s.svc.Plan(c.r.Context(), update, diff)
	if err != nil {
		return err
	}
	s.json(c.w, http.StatusOK, pv)
	return nil
}

// ---- jobs ----

var (
	jobKinds  = map[string]bool{"apply": true, "setup": true, "fe-build": true, "fe-prune": true, "support-bundle": true, "backup": true, "verify": true, "restore": true, "update-check": true, "update": true, "rollback": true, "tls": true}
	jobStates = map[string]bool{"queued": true, "running": true, "succeeded": true, "failed": true, "cancelled": true}
)

func (s *Server) listJobs(c *reqCtx) error {
	f := jobs.Filter{Limit: 20}
	if v, ok := c.query("kind"); ok {
		if !jobKinds[v] {
			return problem.InvalidParam("kind", "the kind is not a job kind")
		}
		f.Kind = v
	}
	if v, ok := c.query("state"); ok {
		if !jobStates[v] {
			return problem.InvalidParam("state", "the state is not a job state")
		}
		f.State = v
	}
	if v, ok := c.query("limit"); ok {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			return problem.InvalidParam("limit", "use a number from 1 to 100")
		}
		f.Limit = n
	}
	if v, ok := c.query("before"); ok {
		if !jobs.IDPattern.MatchString(v) {
			return problem.InvalidParam("before", "the value is not a job id")
		}
		f.Before = v
	}
	items, next := jobs.List(s.svc.Paths, f)
	var nb any
	if next != "" {
		nb = next
	}
	s.json(c.w, http.StatusOK, map[string]any{"items": items, "next_before": nb})
	return nil
}

func jobNotFound(id string) error {
	return problem.Newf(problem.JobNotFound, "No job has the id %s.", id)
}

func (s *Server) getJob(c *reqCtx) error {
	id := c.param("job_id")
	j, err := jobs.Load(s.svc.Paths, id)
	if errors.Is(err, jobs.ErrNotFound) {
		return jobNotFound(id)
	}
	if err != nil {
		return err
	}
	s.json(c.w, http.StatusOK, j)
	return nil
}

func (s *Server) cancelJob(c *reqCtx) error {
	id := c.param("job_id")
	if _, err := s.svc.Jobs.Cancel(id); err != nil {
		if errors.Is(err, jobs.ErrNotFound) {
			return jobNotFound(id)
		}
		return err
	}
	j, err := jobs.Load(s.svc.Paths, id)
	return s.accepted(c, j, err)
}

// ---- logs and support ----

func (s *Server) logUnits(c *reqCtx) error {
	s.json(c.w, http.StatusOK, map[string]any{"items": service.LogUnits})
	return nil
}

func (s *Server) supportBundle(c *reqCtx) error {
	var req struct {
		LogLines   *int `json:"log_lines"`
		SinceHours *int `json:"since_hours"`
	}
	if err := c.decode(&req, false); err != nil {
		return err
	}
	br := service.BundleRequest{}
	if req.LogLines != nil {
		br.LogLines = *req.LogLines
		if br.LogLines == 0 {
			br.LogLines = -1
		}
	}
	if req.SinceHours != nil {
		br.SinceHours = *req.SinceHours
		if br.SinceHours == 0 {
			br.SinceHours = -1
		}
	}
	j, err := s.svc.StartSupportBundle(c.r.Context(), br, jobs.SourceConsole)
	return s.accepted(c, j, err)
}

func (s *Server) downloadBundle(c *reqCtx) error {
	path, name, err := s.svc.BundleFile(c.param("bundle_id"))
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return problem.New(problem.NotFound, "The support bundle does not exist.")
	}
	c.w.Header().Set("Content-Type", "application/gzip")
	c.w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	c.w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, err = c.w.Write(data)
	return err
}

// ---- network and tls ----

func (s *Server) network(c *reqCtx) error {
	s.json(c.w, http.StatusOK, s.svc.Network(c.r.Context()))
	return nil
}

func (s *Server) tls(c *reqCtx) error {
	s.json(c.w, http.StatusOK, s.svc.TLS())
	return nil
}

func (s *Server) sendFile(c *reqCtx, f *service.RootCAFile) error {
	h := c.w.Header()
	h.Set("Content-Type", f.ContentType)
	h.Set("Content-Disposition", `attachment; filename="`+f.Filename+`"`)
	h.Set("X-Care-Pi-Fingerprint-SHA256", f.Fingerprint)
	h.Set("Cache-Control", "public, max-age=300")
	h.Set("Content-Length", strconv.Itoa(len(f.Data)))
	_, err := c.w.Write(f.Data)
	return err
}

func (s *Server) rootCA(c *reqCtx) error {
	format, _ := c.query("format")
	f, err := s.svc.RootCA(format)
	if err != nil {
		return err
	}
	return s.sendFile(c, f)
}

func (s *Server) mobileconfig(c *reqCtx) error {
	f, err := s.svc.Mobileconfig()
	if err != nil {
		return err
	}
	return s.sendFile(c, f)
}

func (s *Server) instructions(c *reqCtx) error {
	in, err := s.svc.Instructions(c.param("os"), c.prefix)
	if err != nil {
		return err
	}
	s.json(c.w, http.StatusOK, in)
	return nil
}

// ---- fe, care, power ----

func (s *Server) fe(c *reqCtx) error {
	st, err := s.svc.FE()
	if err != nil {
		return err
	}
	s.json(c.w, http.StatusOK, st)
	return nil
}

func (s *Server) feBuild(c *reqCtx) error {
	var req struct {
		Force *bool `json:"force"`
	}
	if err := c.decode(&req, false); err != nil {
		return err
	}
	j, err := s.svc.StartFEBuild(c.r.Context(), req.Force != nil && *req.Force, jobs.SourceConsole)
	return s.accepted(c, j, err)
}

func (s *Server) fePrune(c *reqCtx) error {
	var req struct {
		Keep *int `json:"keep"`
	}
	if err := c.decode(&req, false); err != nil {
		return err
	}
	keep := 2
	if req.Keep != nil {
		if *req.Keep < 1 || *req.Keep > 20 {
			return problem.Invalid("/keep", "use a number from 1 to 20", "maximum")
		}
		keep = *req.Keep
	}
	j, err := s.svc.StartFEPrune(c.r.Context(), keep, jobs.SourceConsole)
	return s.accepted(c, j, err)
}

func (s *Server) care(c *reqCtx) error {
	s.json(c.w, http.StatusOK, s.svc.CareInfo(c.r.Context()))
	return nil
}

func (s *Server) careSuperuser(c *reqCtx) error {
	var req careUserReq
	if err := c.decode(&req, true); err != nil {
		return err
	}
	if err := req.check(); err != nil {
		return err
	}
	u, err := s.svc.CreateSuperuser(c.r.Context(), *req.Username, *req.Email, *req.Password)
	if err != nil {
		return err
	}
	s.json(c.w, http.StatusCreated, u)
	return nil
}

func (s *Server) power(action string) func(*reqCtx) error {
	return func(c *reqCtx) error {
		var req struct {
			DelayS *int `json:"delay_s"`
		}
		if err := c.decode(&req, false); err != nil {
			return err
		}
		delay := 5
		if req.DelayS != nil {
			if *req.DelayS < 1 || *req.DelayS > 300 {
				return problem.Invalid("/delay_s", "use a number from 1 to 300", "maximum")
			}
			delay = *req.DelayS
		}
		pa, err := s.svc.Power(action, time.Duration(delay)*time.Second)
		if err != nil {
			return err
		}
		s.json(c.w, http.StatusAccepted, pa)
		return nil
	}
}
