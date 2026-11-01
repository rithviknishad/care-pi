// Package problem holds the RFC 9457 problem details of the console API (docs/api/README.md, section 7).
//
// The service layer returns a *Problem as an error. The HTTP server writes it as
// application/problem+json. The CLI prints its detail and maps its status to an exit code.
package problem

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Base is the prefix of each problem type URI.
const Base = "https://care-pi.rithviknishad.dev/problems/"

// Slugs of the catalogue.
const (
	MalformedRequest     = "malformed-request"
	InvalidRequest       = "invalid-request"
	InvalidConfig        = "invalid-config"
	UnsupportedConfig    = "unsupported-config"
	WeakPassword         = "weak-password"
	HostCheckFailed      = "host-check-failed"
	NotAuthenticated     = "not-authenticated"
	BadCredentials       = "bad-credentials"
	TOTPRequired         = "totp-required"
	CSRF                 = "csrf"
	ReauthRequired       = "reauth-required"
	NotFound             = "not-found"
	JobNotFound          = "job-not-found"
	UnitNotFound         = "unit-not-found"
	NotClaimed           = "not-claimed"
	AlreadyClaimed       = "already-claimed"
	SetupIncomplete      = "setup-incomplete"
	SetupFinished        = "setup-finished"
	StepNotSkippable     = "step-not-skippable"
	JobRunning           = "job-running"
	JobNotCancellable    = "job-not-cancellable"
	CareUserExists       = "care-user-exists"
	StateConflict        = "state-conflict"
	ConfigChanged        = "config-changed"
	PayloadTooLarge      = "payload-too-large"
	UnsupportedMediaType = "unsupported-media-type"
	PreconditionRequired = "precondition-required"
	RateLimited          = "rate-limited"
	LockedOut            = "locked-out"
	Internal             = "internal"
	NotImplemented       = "not-implemented-in-this-version"
	CareNotRunning       = "care-not-running"
	Unavailable          = "unavailable"

	// Phase 3 (docs/api/README.md section 13.13).
	DestinationUnreachable = "destination-unreachable"
	DestinationUntrusted   = "destination-untrusted-hostkey"
	RecoveryKeyMismatch    = "recovery-key-mismatch"
	RestoreWindowExceeded  = "restore-window-exceeded"
	DiskInUse              = "disk-in-use"
	NoDestination          = "no-destination"
	NoRecoveryKey          = "no-recovery-key"
	InsufficientSpace      = "insufficient-space"
	VerifyFailed           = "verify-failed"
	// Phase 4 (docs/api/README.md, section 14.8).
	UpdateWindowClosed   = "update-window-closed"
	NoUpdateAvailable    = "no-update-available"
	LockNotFound         = "lock-not-found"
	AgentTooOld          = "agent-too-old"
	PlugUnknown          = "plug-unknown"
	PlugSettingsInvalid  = "plug-settings-invalid"
	BuildRequiredOffline = "build-required-offline"
	IndexUnavailable     = "index-unavailable"
	ArtefactInvalid      = "artefact-invalid"
	RollbackUnavailable  = "rollback-unavailable"
	CheckpointFailed     = "checkpoint-failed"
	MigrationFailed      = "migration-failed"
	RollbackFailed       = "rollback-failed"
	OSUpdateFailed       = "os-update-failed"

	// Phase 6 (docs/api/README.md section 15).
	DNSServiceUnreachable  = "dns-service-unreachable"
	DNSServiceRejected     = "dns-service-rejected"
	ACMEPending            = "acme-pending"
	TailscaleInstallFailed = "tailscale-install-failed"
	TailscaleLoginRequired = "tailscale-login-required"
	TailscaleHTTPSDisabled = "tailscale-https-disabled"
	WiFiAuthFailed         = "wifi-auth-failed"
	WiFiNoDevice           = "wifi-no-device"
	WiFiFailed             = "wifi-failed"
)

type entry struct {
	status int
	title  string
}

// catalogue gives the status and the title of each slug. The title is the same for each occurrence.
var catalogue = map[string]entry{
	MalformedRequest:     {400, "The request is malformed"},
	InvalidRequest:       {422, "The request is not valid"},
	InvalidConfig:        {422, "The config is not valid"},
	UnsupportedConfig:    {422, "This version cannot apply the config"},
	WeakPassword:         {422, "The password is too weak"},
	HostCheckFailed:      {422, "A required host check failed"},
	NotAuthenticated:     {401, "Log in first"},
	BadCredentials:       {401, "The credentials are not correct"},
	TOTPRequired:         {401, "A TOTP code is necessary"},
	CSRF:                 {403, "The CSRF token or the origin is not correct"},
	ReauthRequired:       {403, "Enter the password again"},
	NotFound:             {404, "The resource does not exist"},
	JobNotFound:          {404, "The job does not exist"},
	UnitNotFound:         {404, "The unit does not exist"},
	NotClaimed:           {409, "The device is not claimed"},
	AlreadyClaimed:       {409, "The device is already claimed"},
	SetupIncomplete:      {409, "Setup is not complete"},
	SetupFinished:        {409, "Setup is finished"},
	StepNotSkippable:     {409, "The step cannot be skipped"},
	JobRunning:           {409, "A job runs now"},
	JobNotCancellable:    {409, "The job cannot be cancelled"},
	CareUserExists:       {409, "The Care user exists"},
	StateConflict:        {409, "The state does not allow this operation"},
	ConfigChanged:        {412, "The config changed"},
	PayloadTooLarge:      {413, "The request body is too large"},
	UnsupportedMediaType: {415, "The content type is not supported"},
	PreconditionRequired: {428, "An If-Match header is necessary"},
	RateLimited:          {429, "Too many requests"},
	LockedOut:            {429, "Too many failed attempts"},
	Internal:             {500, "Internal error"},
	NotImplemented:       {501, "This version does not have the feature"},
	CareNotRunning:       {503, "Care does not run"},
	Unavailable:          {503, "The agent is not ready"},

	DestinationUnreachable: {422, "The backup destination does not answer"},
	DestinationUntrusted:   {422, "The SFTP host key is not trusted"},
	RecoveryKeyMismatch:    {422, "The recovery key does not match"},
	RestoreWindowExceeded:  {422, "The time is outside the restore window"},
	DiskInUse:              {409, "The disk is in use"},
	NoDestination:          {409, "No backup destination"},
	NoRecoveryKey:          {409, "No recovery key"},
	InsufficientSpace:      {409, "Not enough free disk space"},
	VerifyFailed:           {500, "The verify failed"},

	UpdateWindowClosed:   {409, "The update window is closed"},
	NoUpdateAvailable:    {409, "No update is available"},
	LockNotFound:         {404, "The lock does not exist"},
	AgentTooOld:          {409, "The agent is too old for this lock"},
	PlugUnknown:          {404, "The plug is not in the solution"},
	PlugSettingsInvalid:  {422, "The plug settings are not valid"},
	BuildRequiredOffline: {409, "The change needs the internet"},
	IndexUnavailable:     {502, "The index is not available"},
	ArtefactInvalid:      {502, "A downloaded file is not valid"},
	RollbackUnavailable:  {409, "No previous lock exists"},
	CheckpointFailed:     {500, "The checkpoint failed"},
	MigrationFailed:      {500, "A migration failed"},
	RollbackFailed:       {500, "The rollback failed"},
	OSUpdateFailed:       {500, "The OS update failed"},

	DNSServiceUnreachable:  {502, "The DNS service does not answer"},
	DNSServiceRejected:     {502, "The DNS service refused the request"},
	ACMEPending:            {504, "The certificate is not issued yet"},
	TailscaleInstallFailed: {500, "Tailscale did not install"},
	TailscaleLoginRequired: {409, "Tailscale needs a login"},
	TailscaleHTTPSDisabled: {409, "The tailnet gives no HTTPS certificates"},
	WiFiAuthFailed:         {422, "The Wi-Fi network refused the passphrase"},
	WiFiNoDevice:           {409, "This device has no Wi-Fi"},
	WiFiFailed:             {500, "The Wi-Fi connection failed"},
}

// Known reports whether slug is in the catalogue.
func Known(slug string) bool {
	_, ok := catalogue[slug]
	return ok
}

// FieldError is one validation problem (schema ValidationError).
type FieldError struct {
	Pointer   string `json:"pointer"`
	Message   string `json:"message"`
	Code      string `json:"code,omitempty"`
	Parameter string `json:"parameter,omitempty"`
}

// Problem is one error response.
type Problem struct {
	Slug     string
	Status   int
	Title    string
	Detail   string
	Instance string

	Errors []FieldError
	// JobID is set for job-running. HasJobID sends the member also when JobID is empty (null).
	JobID        string
	HasJobID     bool
	RetryAfter   int
	MissingSteps []string
	FailedChecks []string
	Phase        int
	// Phase 3 members (docs/api/README.md section 13.13).
	DestinationID string
	HostKey       *HostKey
	Window        *Window
	Space         *Space
	// Phase 4 extension members (docs/api/README.md, section 14.8).
	MinAgentVersion string
	NextWindowAt    *time.Time
	// PitrOffer is the PITR offer (schema PitrOffer) of a failed update, rollback, or plug change.
	PitrOffer any
}

// HostKey is the host key that an SFTP server presents (schema HostKey).
type HostKey struct {
	Type              string `json:"type"`
	FingerprintSHA256 string `json:"fingerprint_sha256"`
	PublicKey         string `json:"public_key"`
	Trusted           bool   `json:"trusted"`
}

// Window is the restore window of restore-window-exceeded. A nil time is null.
type Window struct {
	From *string `json:"from"`
	To   *string `json:"to"`
}

// Space is the space of insufficient-space.
type Space struct {
	Path          string `json:"path"`
	RequiredBytes int64  `json:"required_bytes"`
	FreeBytes     int64  `json:"free_bytes"`
}

// New returns a problem of the slug with the status and the title of the catalogue.
func New(slug, detail string) *Problem {
	e, ok := catalogue[slug]
	if !ok {
		e = catalogue[Internal]
		slug = Internal
	}
	return &Problem{Slug: slug, Status: e.status, Title: e.title, Detail: detail}
}

// Newf is New with a format.
func Newf(slug, format string, a ...any) *Problem {
	return New(slug, fmt.Sprintf(format, a...))
}

// Error implements error.
func (p *Problem) Error() string {
	msg := p.Detail
	if msg == "" {
		msg = p.Title
	}
	for _, e := range p.Errors {
		where := e.Pointer
		if e.Parameter != "" {
			where = e.Parameter
		}
		if where != "" && !strings.Contains(msg, e.Message) {
			msg += "\n  - " + where + ": " + e.Message
		}
	}
	return msg
}

// Type returns the type URI.
func (p *Problem) Type() string { return Base + p.Slug }

// WithErrors adds validation errors.
func (p *Problem) WithErrors(errs ...FieldError) *Problem {
	p.Errors = append(p.Errors, errs...)
	return p
}

// Invalid returns invalid-request with one error at pointer.
func Invalid(pointer, message, code string) *Problem {
	return New(InvalidRequest, message).WithErrors(FieldError{Pointer: pointer, Message: message, Code: code})
}

// InvalidParam returns invalid-request for a query, path, or header parameter.
func InvalidParam(name, message string) *Problem {
	return New(InvalidRequest, name+": "+message).WithErrors(FieldError{Pointer: "", Message: message, Parameter: name})
}

// Running returns job-running with the id of the job that holds the apply lock ("" gives null).
func Running(jobID string) *Problem {
	p := New(JobRunning, "Another job holds the apply lock. Wait until it ends.")
	p.JobID, p.HasJobID = jobID, true
	return p
}

// Later returns a feature of a later phase (501).
func Later(phase int, detail string) *Problem {
	p := New(NotImplemented, detail)
	p.Phase = phase
	return p
}

// Limited returns rate-limited or locked-out with the wait in seconds (1 or more).
func Limited(slug string, retryAfterS int, detail string) *Problem {
	if retryAfterS < 1 {
		retryAfterS = 1
	}
	p := New(slug, detail)
	p.RetryAfter = retryAfterS
	return p
}

// NeedSpace returns insufficient-space.
func NeedSpace(path string, required, free int64, detail string) *Problem {
	p := New(InsufficientSpace, detail)
	p.Space = &Space{Path: path, RequiredBytes: max(required, 0), FreeBytes: max(free, 0)}
	return p
}

// Unreachable returns destination-unreachable for a destination ("" for a candidate without an id).
func Unreachable(id, detail string) *Problem {
	p := New(DestinationUnreachable, detail)
	p.DestinationID = id
	return p
}

// From returns the problem in err, or an internal problem with a generic detail.
func From(err error) *Problem {
	var p *Problem
	if errors.As(err, &p) {
		return p
	}
	return New(Internal, "The agent cannot complete the request. The journal of care-pi.service has the detail.")
}

// Is reports whether err is a problem with the slug.
func Is(err error, slug string) bool {
	var p *Problem
	return errors.As(err, &p) && p.Slug == slug
}

type wire struct {
	Type         string          `json:"type"`
	Title        string          `json:"title"`
	Status       int             `json:"status"`
	Detail       string          `json:"detail"`
	Instance     string          `json:"instance"`
	Errors       []FieldError    `json:"errors,omitempty"`
	JobID        json.RawMessage `json:"job_id,omitempty"`
	RetryAfter   int             `json:"retry_after_s,omitempty"`
	MissingSteps []string        `json:"missing_steps,omitempty"`
	FailedChecks []string        `json:"failed_checks,omitempty"`
	Phase        int             `json:"phase,omitempty"`
	DestID       string          `json:"destination_id,omitempty"`
	HostKey      *HostKey        `json:"host_key,omitempty"`
	Window       *Window         `json:"restore_window,omitempty"`
	Space        *Space          `json:"space,omitempty"`
	MinAgent     string          `json:"min_agent_version,omitempty"`
	NextWindowAt *time.Time      `json:"next_window_at,omitempty"`
	PitrOffer    json.RawMessage `json:"pitr_offer,omitempty"`
}

// MarshalJSON writes the RFC 9457 form.
func (p *Problem) MarshalJSON() ([]byte, error) {
	w := wire{
		Type: p.Type(), Title: p.Title, Status: p.Status, Detail: p.Detail, Instance: p.Instance,
		Errors: p.Errors, RetryAfter: p.RetryAfter, MissingSteps: p.MissingSteps,
		FailedChecks: p.FailedChecks, Phase: p.Phase, DestID: p.DestinationID, HostKey: p.HostKey,
		Window: p.Window, Space: p.Space, MinAgent: p.MinAgentVersion, NextWindowAt: p.NextWindowAt,
	}
	if p.PitrOffer != nil {
		raw, err := json.Marshal(p.PitrOffer)
		if err != nil {
			return nil, err
		}
		if string(raw) != "null" {
			w.PitrOffer = raw
		}
	}
	if w.Instance == "" {
		w.Instance = "/"
	}
	if p.HasJobID {
		if p.JobID == "" {
			w.JobID = json.RawMessage("null")
		} else {
			w.JobID = json.RawMessage(strconv.Quote(p.JobID))
		}
	}
	return json.Marshal(w)
}

// UnmarshalJSON reads the RFC 9457 form.
func (p *Problem) UnmarshalJSON(b []byte) error {
	var w wire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	*p = Problem{
		Slug: strings.TrimPrefix(w.Type, Base), Status: w.Status, Title: w.Title, Detail: w.Detail,
		Instance: w.Instance, Errors: w.Errors, RetryAfter: w.RetryAfter, MissingSteps: w.MissingSteps,
		FailedChecks: w.FailedChecks, Phase: w.Phase, DestinationID: w.DestID, HostKey: w.HostKey,
		Window: w.Window, Space: w.Space, MinAgentVersion: w.MinAgent, NextWindowAt: w.NextWindowAt,
	}
	if len(w.PitrOffer) > 0 {
		var v any
		if json.Unmarshal(w.PitrOffer, &v) == nil {
			p.PitrOffer = v
		}
	}
	if len(w.JobID) > 0 {
		p.HasJobID = true
		_ = json.Unmarshal(w.JobID, &p.JobID)
	}
	return nil
}
