// Package update runs the Phase 4 update machine (docs/solutions.md, sections 7 to 12): the index,
// the tracks, the jobs update-check, update-stage, update-apply, update-rollback, os-update, and
// plug-change, the window, the checkpoint before the migrations, and the plugs.
package update

import (
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
)

// Machine states (schema UpdateMachineState).
const (
	StateIdle        = "idle"
	StateChecking    = "checking"
	StateStaging     = "staging"
	StateStaged      = "staged"
	StateApplying    = "applying"
	StateRollingBack = "rolling-back"
	StateFailed      = "failed"
)

// Change paths (schema ChangePath).
const (
	PathNone            = "none"
	PathPlugConfig      = "plug-config"
	PathRestart         = "restart"
	PathCutover         = "cutover"
	PathBuildAndCutover = "build-and-cutover"
)

// History kinds and outcomes (schema UpdateHistoryEntry).
const (
	HistUpdate     = "update"
	HistRollback   = "rollback"
	HistPlugChange = "plug-change"
	HistApply      = "apply"
	HistOSUpdate   = "os-update"

	OutcomeSucceeded      = "succeeded"
	OutcomeFailed         = "failed"
	OutcomeRolledBack     = "rolled-back"
	OutcomeRollbackFailed = "rollback-failed"
)

// InstalledLock is the header of the on-device lock (schema InstalledLock).
type InstalledLock struct {
	Version         *string   `json:"version"`
	Source          string    `json:"source"`
	Track           string    `json:"track"`
	LockSHA256      *string   `json:"lock_sha256"`
	InstalledAt     time.Time `json:"installed_at"`
	AgentVersion    string    `json:"agent_version"`
	Withdrawn       bool      `json:"withdrawn"`
	WithdrawnReason *string   `json:"withdrawn_reason"`
}

// AvailableLock is one released lock (schema AvailableLock).
type AvailableLock struct {
	Version         string    `json:"version"`
	ReleasedAt      time.Time `json:"released_at"`
	ChangelogURL    *string   `json:"changelog_url"`
	Summary         string    `json:"summary"`
	MinAgentVersion string    `json:"min_agent_version"`
	Installable     bool      `json:"installable"`
	Withdrawn       bool      `json:"withdrawn"`
	WithdrawnReason *string   `json:"withdrawn_reason"`
}

// UpdateTarget is what the next update installs (schema UpdateTarget).
type UpdateTarget struct {
	Kind               string     `json:"kind"`
	Version            *string    `json:"version"`
	TargetID           string     `json:"target_id"`
	ReleasedAt         *time.Time `json:"released_at"`
	ChangelogURL       *string    `json:"changelog_url"`
	Summary            *string    `json:"summary"`
	MinAgentVersion    *string    `json:"min_agent_version"`
	Path               string     `json:"path"`
	Staged             bool       `json:"staged"`
	NeedsBuild         bool       `json:"needs_build"`
	NeedsInternet      bool       `json:"needs_internet"`
	EstimatedDowntimeS *int       `json:"estimated_downtime_s"`
}

// UpdateComponent is one component of the running lock and of the target (schema UpdateComponent).
type UpdateComponent struct {
	ID      string  `json:"id"`
	Label   string  `json:"label"`
	Kind    string  `json:"kind"`
	Ref     *string `json:"ref"`
	Tracked bool    `json:"tracked"`
	Source  string  `json:"source"`
	Current *string `json:"current"`
	Target  *string `json:"target"`
	Changed bool    `json:"changed"`
}

// UpdateBlocker tells why the agent does not install the target (schema UpdateBlocker).
type UpdateBlocker struct {
	Code            string  `json:"code"`
	Detail          string  `json:"detail"`
	MinAgentVersion *string `json:"min_agent_version"`
}

// WindowState is updates.window with its next times (schema UpdateWindowState).
type WindowState struct {
	Days       []string   `json:"days"`
	Start      string     `json:"start"`
	End        string     `json:"end"`
	Timezone   string     `json:"timezone"`
	Open       bool       `json:"open"`
	NextOpenAt time.Time  `json:"next_open_at"`
	ClosesAt   *time.Time `json:"closes_at"`
}

// IndexState is the cached index (schema IndexState).
type IndexState struct {
	URL         string     `json:"url"`
	DistBase    *string    `json:"dist_base"`
	FetchedAt   *time.Time `json:"fetched_at"`
	GeneratedAt *time.Time `json:"generated_at"`
}

// StagingState is the staging of a target (schema StagingState).
type StagingState struct {
	State         string           `json:"state"`
	Version       *string          `json:"version"`
	TargetID      *string          `json:"target_id"`
	JobID         *string          `json:"job_id"`
	StagedAt      *time.Time       `json:"staged_at"`
	DownloadBytes *int64           `json:"download_bytes"`
	NeedsBuild    bool             `json:"needs_build"`
	Error         *problem.Problem `json:"error"`
}

// MaintenanceState is the maintenance page (schema MaintenanceState).
type MaintenanceState struct {
	Active    bool       `json:"active"`
	Kind      *string    `json:"kind"`
	JobID     *string    `json:"job_id"`
	Step      *string    `json:"step"`
	StartedAt *time.Time `json:"started_at"`
}

// Checkpoint is the result of the checkpoint before the migrations (schema Checkpoint).
type Checkpoint struct {
	TakenAt      time.Time `json:"taken_at"`
	TargetTime   time.Time `json:"target_time"`
	RestorePoint *string   `json:"restore_point"`
	BackupLabel  *string   `json:"backup_label"`
	DurationS    float64   `json:"duration_s"`
}

// RestoreRequest is the body of POST /restore of Phase 3 (schema RestoreRequest).
type RestoreRequest struct {
	Kind       string    `json:"kind"`
	TargetTime time.Time `json:"target_time"`
}

// PitrOffer is the hand-off to the restore of Phase 3 (schema PitrOffer).
type PitrOffer struct {
	Available    bool           `json:"available"`
	Reason       *string        `json:"reason"`
	CheckpointAt time.Time      `json:"checkpoint_at"`
	TargetTime   time.Time      `json:"target_time"`
	RestorePoint *string        `json:"restore_point"`
	Restore      RestoreRequest `json:"restore"`
}

// HistoryEntry is one line of update-history.jsonl (schema UpdateHistoryEntry).
type HistoryEntry struct {
	JobID       string           `json:"job_id"`
	Kind        string           `json:"kind"`
	Source      string           `json:"source"`
	Reason      *string          `json:"reason"`
	StartedAt   time.Time        `json:"started_at"`
	FinishedAt  time.Time        `json:"finished_at"`
	Outcome     string           `json:"outcome"`
	FromVersion *string          `json:"from_version"`
	ToVersion   *string          `json:"to_version"`
	FromLock    *string          `json:"from_lock"`
	ToLock      *string          `json:"to_lock"`
	PlugID      *string          `json:"plug_id"`
	Checkpoint  *Checkpoint      `json:"checkpoint"`
	PitrOffer   *PitrOffer       `json:"pitr_offer"`
	Problem     *problem.Problem `json:"problem"`
	DowntimeS   *float64         `json:"downtime_s"`
}

// History is one page of the history (schema UpdateHistory).
type History struct {
	Items      []HistoryEntry `json:"items"`
	NextBefore *string        `json:"next_before"`
}

// RollbackState is what a rollback without to_lock does (schema RollbackState).
type RollbackState struct {
	Available     bool       `json:"available"`
	ToLock        *string    `json:"to_lock"`
	ToVersion     *string    `json:"to_version"`
	Reason        *string    `json:"reason"`
	ImagesPresent bool       `json:"images_present"`
	PitrOffer     *PitrOffer `json:"pitr_offer"`
}

// OSSummary is the OS part of GET /updates (schema OsUpdatesSummary).
type OSSummary struct {
	SecurityAuto        bool       `json:"security_auto"`
	RebootRequired      bool       `json:"reboot_required"`
	RebootScheduledAt   *time.Time `json:"reboot_scheduled_at"`
	UpgradableCount     int        `json:"upgradable_count"`
	HeldUpgradableCount int        `json:"held_upgradable_count"`
	LastFullUpgradeAt   *time.Time `json:"last_full_upgrade_at"`
}

// Overview is GET /updates (schema UpdatesOverview).
type Overview struct {
	Solution struct {
		Name  string `json:"name"`
		Title string `json:"title"`
	} `json:"solution"`
	Track           string            `json:"track"`
	Pin             *string           `json:"pin"`
	Current         *InstalledLock    `json:"current"`
	Latest          *AvailableLock    `json:"latest"`
	Target          *UpdateTarget     `json:"target"`
	UpdateAvailable bool              `json:"update_available"`
	Available       []AvailableLock   `json:"available"`
	Components      []UpdateComponent `json:"components"`
	State           string            `json:"state"`
	BlockedBy       *UpdateBlocker    `json:"blocked_by"`
	Auto            bool              `json:"auto"`
	Window          WindowState       `json:"window"`
	NextCheckAt     *time.Time        `json:"next_check_at"`
	NextApplyAt     *time.Time        `json:"next_apply_at"`
	LastCheckAt     *time.Time        `json:"last_check_at"`
	LastCheckError  *problem.Problem  `json:"last_check_error"`
	Index           IndexState        `json:"index"`
	Staging         StagingState      `json:"staging"`
	Maintenance     MaintenanceState  `json:"maintenance"`
	LastResult      *HistoryEntry     `json:"last_result"`
	Rollback        RollbackState     `json:"rollback"`
	OS              OSSummary         `json:"os"`
	Job             *jobs.Summary     `json:"job"`
}

// SummaryResult is the short form of last_result (UpdatesSummary.last_result).
type SummaryResult struct {
	JobID      string    `json:"job_id"`
	Kind       string    `json:"kind"`
	Outcome    string    `json:"outcome"`
	FinishedAt time.Time `json:"finished_at"`
	ToVersion  *string   `json:"to_version"`
}

// Summary is the updates member of GET /status (schema UpdatesSummary).
type Summary struct {
	Track             string         `json:"track"`
	CurrentVersion    *string        `json:"current_version"`
	LatestVersion     *string        `json:"latest_version"`
	UpdateAvailable   bool           `json:"update_available"`
	TargetVersion     *string        `json:"target_version"`
	StagedVersion     *string        `json:"staged_version"`
	State             string         `json:"state"`
	BlockedBy         *string        `json:"blocked_by"`
	NextApplyAt       *time.Time     `json:"next_apply_at"`
	LastCheckAt       *time.Time     `json:"last_check_at"`
	LastResult        *SummaryResult `json:"last_result"`
	RollbackAvailable bool           `json:"rollback_available"`
	OSRebootRequired  bool           `json:"os_reboot_required"`
}

// CheckResult is the result of an update-check job (schema UpdateCheckResult).
type CheckResult struct {
	Kind              string         `json:"kind"`
	CheckedAt         time.Time      `json:"checked_at"`
	Track             string         `json:"track"`
	CurrentVersion    *string        `json:"current_version"`
	LatestVersion     *string        `json:"latest_version"`
	TargetVersion     *string        `json:"target_version"`
	TargetID          *string        `json:"target_id"`
	UpdateAvailable   bool           `json:"update_available"`
	BlockedBy         *UpdateBlocker `json:"blocked_by"`
	ChangedComponents []string       `json:"changed_components"`
	StageJobID        *string        `json:"stage_job_id"`
}

// StageResult is the result of an update-stage job (schema UpdateStageResult).
type StageResult struct {
	Kind          string   `json:"kind"`
	Version       *string  `json:"version"`
	TargetID      string   `json:"target_id"`
	PulledImages  []string `json:"pulled_images"`
	Downloaded    []string `json:"downloaded"`
	Built         []string `json:"built"`
	DownloadBytes int64    `json:"download_bytes"`
	DurationS     float64  `json:"duration_s"`
}

// ApplyResult is the result of an update-apply job (schema UpdateApplyResult).
type ApplyResult struct {
	Kind         string      `json:"kind"`
	FromVersion  *string     `json:"from_version"`
	ToVersion    *string     `json:"to_version"`
	TargetID     string      `json:"target_id"`
	FromLock     string      `json:"from_lock"`
	Checkpoint   *Checkpoint `json:"checkpoint"`
	DowntimeS    float64     `json:"downtime_s"`
	PrunedImages int         `json:"pruned_images"`
	Warnings     []string    `json:"warnings"`
}

// RollbackResult is the result of an update-rollback job (schema UpdateRollbackResult).
type RollbackResult struct {
	Kind        string      `json:"kind"`
	FromVersion *string     `json:"from_version"`
	ToVersion   *string     `json:"to_version"`
	ToLock      string      `json:"to_lock"`
	Checkpoint  *Checkpoint `json:"checkpoint"`
	PitrOffer   *PitrOffer  `json:"pitr_offer"`
	DowntimeS   float64     `json:"downtime_s"`
}

// PackageChange is one package of an OS update.
type PackageChange struct {
	Name string  `json:"name"`
	From *string `json:"from"`
	To   *string `json:"to"`
}

// OSUpdateResult is the result of an os-update job (schema OsUpdateResult).
type OSUpdateResult struct {
	Kind           string          `json:"kind"`
	Upgraded       []PackageChange `json:"upgraded"`
	HeldChanged    []string        `json:"held_changed"`
	CareRestarted  bool            `json:"care_restarted"`
	RebootRequired bool            `json:"reboot_required"`
	Rebooting      bool            `json:"rebooting"`
	DurationS      float64         `json:"duration_s"`
}

// PlugChangeJobResult is the result of a plug-change job (schema PlugChangeJobResult).
type PlugChangeJobResult struct {
	Kind        string      `json:"kind"`
	PlugID      string      `json:"plug_id"`
	Enabled     bool        `json:"enabled"`
	Path        string      `json:"path"`
	LockChanged bool        `json:"lock_changed"`
	Checkpoint  *Checkpoint `json:"checkpoint"`
	DowntimeS   *float64    `json:"downtime_s"`
}

// SolutionSummary is one solution of GET /solutions (schema SolutionSummary).
type SolutionSummary struct {
	Name          string  `json:"name"`
	Title         string  `json:"title"`
	Description   string  `json:"description"`
	Source        string  `json:"source"`
	Selected      bool    `json:"selected"`
	LatestVersion *string `json:"latest_version"`
	LockCount     int     `json:"lock_count"`
	PlugCount     int     `json:"plug_count"`
}

// SolutionList is GET /solutions (schema SolutionList).
type SolutionList struct {
	Items []SolutionSummary `json:"items"`
	Index IndexState        `json:"index"`
}

// SolutionDetail is GET /solutions/{name} (schema SolutionDetail).
type SolutionDetail struct {
	Name            string          `json:"name"`
	Title           string          `json:"title"`
	Description     string          `json:"description"`
	Source          string          `json:"source"`
	Selected        bool            `json:"selected"`
	Document        map[string]any  `json:"document"`
	DocumentVersion *string         `json:"document_version"`
	Locks           []AvailableLock `json:"locks"`
	EmbeddedLocks   []string        `json:"embedded_locks"`
}

func strp(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func timep(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func availableOf(l solution.IndexLock, agentVersion string) AvailableLock {
	old, _ := solution.AgentTooOld(agentVersion, l.MinAgentVersion)
	return AvailableLock{Version: solution.NormalizeLockVersion(l.Version), ReleasedAt: parseTime(l.ReleasedAt),
		ChangelogURL: l.ChangelogURL, Summary: l.Summary, MinAgentVersion: l.MinAgentVersion,
		Installable: !old && !l.Withdrawn, Withdrawn: l.Withdrawn, WithdrawnReason: l.WithdrawnReason}
}
