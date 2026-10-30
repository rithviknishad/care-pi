// Package setup holds the setup record and the state machine of the setup wizard
// (docs/api/README.md, section 5). The record is /var/lib/care-pi/state/setup.json.
// The draft values are in config.yaml; the record holds the step states.
package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// File is the setup record.
const File = paths.StateDir + "/setup.json"

// Apply phases. bootstrap renders only care.pod, care-caddy, and the start units (setup mode).
const (
	PhaseBootstrap = "bootstrap"
	PhaseFull      = "full"
)

// States of the state machine.
const (
	Unclaimed = "unclaimed"
	Claimed   = "claimed"
	InSetup   = "setup"
	Applying  = "applying"
	Ready     = "ready"
)

// Step ids, in list order.
const (
	StepAccount       = "account"
	StepRecoveryCodes = "recovery-codes"
	StepHardware      = "hardware"
	StepIdentity      = "identity"
	StepTLS           = "tls"
	StepSize          = "size"
	StepEnv           = "env"
	StepBackups       = "backups"
	StepCareAdmin     = "care-admin"
)

// Def is one step of the wizard.
type Def struct {
	ID       string
	Required bool
	Phase    int
}

// Steps lists the steps in list order.
var Steps = []Def{
	{StepAccount, true, 2},
	{StepRecoveryCodes, true, 2},
	{StepHardware, true, 2},
	{StepIdentity, true, 2},
	{StepTLS, true, 2},
	{StepSize, true, 2},
	{StepEnv, false, 2},
	{StepBackups, true, 3},
	{StepCareAdmin, true, 2},
}

// Known reports whether id is a step.
func Known(id string) bool {
	for _, d := range Steps {
		if d.ID == id {
			return true
		}
	}
	return false
}

// Skippable reports whether the step can be skipped: env always, backups only in a dev build.
func Skippable(id string, devBuild bool) bool {
	return id == StepEnv || (id == StepBackups && devBuild)
}

// Issue is a problem or a warning of a step (schema StepIssue).
type Issue struct {
	Code    string  `json:"code"`
	Message string  `json:"message"`
	Pointer *string `json:"pointer"`
}

// StepRecord is the stored state of one step.
type StepRecord struct {
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
	Warnings  []Issue   `json:"warnings,omitempty"`
}

// Step statuses.
const (
	Done    = "done"
	Skipped = "skipped"
	Current = "current"
	Pending = "pending"
)

// CareAdmin is the first Care superuser without the password.
type CareAdmin struct {
	Username string `json:"username,omitempty"`
	Email    string `json:"email,omitempty"`
}

// Record is the setup record.
type Record struct {
	Version    int                    `json:"version"`
	ApplyPhase string                 `json:"apply_phase"`
	ClaimedAt  *time.Time             `json:"claimed_at,omitempty"`
	ReadyAt    *time.Time             `json:"ready_at,omitempty"`
	JobID      string                 `json:"job_id,omitempty"`
	Steps      map[string]*StepRecord `json:"steps"`
	CareAdmin  CareAdmin              `json:"care_admin"`
}

// Store reads and writes the record.
type Store struct {
	Paths paths.Paths
	mu    sync.Mutex
}

// Load reads the record. exists is false when the file does not exist.
func (s *Store) Load() (*Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *Store) load() (*Record, bool, error) {
	data, err := os.ReadFile(s.Paths.Abs(File))
	if errors.Is(err, os.ErrNotExist) {
		return &Record{Version: 1, ApplyPhase: PhaseFull, Steps: map[string]*StepRecord{}}, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("cannot read the setup record: %w", err)
	}
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, false, fmt.Errorf("the setup record %s is not valid: %w", File, err)
	}
	if r.Steps == nil {
		r.Steps = map[string]*StepRecord{}
	}
	if r.ApplyPhase == "" {
		r.ApplyPhase = PhaseFull
	}
	return &r, true, nil
}

// Update reads the record (a new record when the file does not exist), runs fn, and writes it.
func (s *Store) Update(fn func(r *Record) error) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, _, err := s.load()
	if err != nil {
		return nil, err
	}
	if err := fn(r); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, err
	}
	path := s.Paths.Abs(File)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := lock.WriteFileAtomic(path, data, 0o600); err != nil {
		return nil, fmt.Errorf("cannot write the setup record: %w", err)
	}
	return r, nil
}

// Phase returns the apply phase of the host: bootstrap while setup mode is not finished, else full.
func Phase(p paths.Paths) string {
	r, ok, err := (&Store{Paths: p}).Load()
	if err != nil || !ok || r.ReadyAt != nil {
		return PhaseFull
	}
	return r.ApplyPhase
}

// Input is what the state machine reads.
type Input struct {
	Record *Record
	// Exists tells that the record file exists.
	Exists  bool
	Claimed bool
	// ClaimedAt is the claim time of the account.
	ClaimedAt *time.Time
	// LockExists tells that lock.yaml exists (an upgrade from Phase 1).
	LockExists bool
	// JobState is the state of the job Record.JobID, or "".
	JobState string
	DevBuild bool
	// CarePassword tells that the agent has the Care superuser password in memory.
	CarePassword bool
}

// StepView is one step for the API (schema SetupStep).
type StepView struct {
	ID        string     `json:"id"`
	Status    string     `json:"status"`
	Required  bool       `json:"required"`
	Skippable bool       `json:"skippable"`
	Phase     int        `json:"phase"`
	UpdatedAt *time.Time `json:"updated_at"`
	Errors    []Issue    `json:"errors"`
	Warnings  []Issue    `json:"warnings"`
}

// View is the computed state.
type View struct {
	State     string
	Current   string
	Steps     []StepView
	CanFinish bool
	Missing   []string
}

// StateName returns the state of the machine.
func StateName(in Input) string {
	r := in.Record
	switch {
	case !in.Claimed:
		return Unclaimed
	case r != nil && r.ReadyAt != nil:
		return Ready
	case !in.Exists && in.LockExists:
		return Ready
	case r != nil && r.JobID != "" && (in.JobState == "running" || in.JobState == "queued"):
		return Applying
	}
	if r != nil {
		for id := range r.Steps {
			if id != StepAccount {
				return InSetup
			}
		}
	}
	return Claimed
}

// Compute returns the state, the steps, and what finish misses.
func Compute(in Input) View {
	v := View{State: StateName(in)}
	r := in.Record
	if r == nil {
		r = &Record{Steps: map[string]*StepRecord{}}
	}
	ready := v.State == Ready
	editable := v.State == Claimed || v.State == InSetup
	for _, d := range Steps {
		sv := StepView{ID: d.ID, Required: d.Required, Phase: d.Phase, Errors: []Issue{}, Warnings: []Issue{}}
		sv.Skippable = editable && Skippable(d.ID, in.DevBuild)
		rec := r.Steps[d.ID]
		status := ""
		switch {
		case d.ID == StepAccount:
			if in.Claimed {
				status = Done
				sv.UpdatedAt = in.ClaimedAt
			}
		case rec != nil:
			status = rec.Status
			t := rec.UpdatedAt
			sv.UpdatedAt = &t
			if rec.Warnings != nil {
				sv.Warnings = rec.Warnings
			}
		case ready:
			status = Done
		}
		if d.ID == StepCareAdmin && status == Done && !in.CarePassword && !ready {
			status = ""
			sv.Errors = append(sv.Errors, Issue{Code: "password-needed",
				Message: "the agent restarted and does not have the Care superuser password; enter it again"})
		}
		if status != Done && status != Skipped && v.Current == "" && !ready {
			status = Current
			v.Current = d.ID
		}
		if status == "" {
			status = Pending
		}
		sv.Status = status
		if d.Required && status != Done && (status != Skipped || !Skippable(d.ID, in.DevBuild)) {
			v.Missing = append(v.Missing, d.ID)
		}
		v.Steps = append(v.Steps, sv)
	}
	if ready {
		v.Missing = nil
	}
	v.CanFinish = editable && len(v.Missing) == 0
	return v
}
