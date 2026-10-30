package jobs

import "time"

// ApplyResult is the result of an apply job (schema ApplyResult).
type ApplyResult struct {
	Kind        string  `json:"kind"`
	Changed     bool    `json:"changed"`
	LockChanged bool    `json:"lock_changed"`
	FEBuildID   *string `json:"fe_build_id"`
}

// SetupResult is the result of the finish job (schema SetupResult).
type SetupResult struct {
	Kind         string   `json:"kind"`
	CareURL      string   `json:"care_url"`
	ConsoleURL   string   `json:"console_url"`
	HandoffURL   string   `json:"handoff_url"`
	URLs         []string `json:"urls"`
	CareUsername *string  `json:"care_username"`
}

// FEBuildResult is the result of an FE build job (schema FeBuildResult).
type FEBuildResult struct {
	Kind      string  `json:"kind"`
	BuildID   string  `json:"build_id"`
	Provider  string  `json:"provider"`
	DurationS float64 `json:"duration_s"`
}

// FEPruneResult is the result of an FE prune job (schema FePruneResult).
type FEPruneResult struct {
	Kind    string   `json:"kind"`
	Removed []string `json:"removed"`
}

// SupportBundleResult is the result of a support-bundle job (schema SupportBundleResult).
type SupportBundleResult struct {
	Kind         string    `json:"kind"`
	BundleID     string    `json:"bundle_id"`
	Filename     string    `json:"filename"`
	SizeBytes    int64     `json:"size_bytes"`
	SHA256       string    `json:"sha256"`
	ExpiresAt    time.Time `json:"expires_at"`
	DownloadPath string    `json:"download_path"`
}
