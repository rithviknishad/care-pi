// Package config defines config.yaml (version 1), its defaults, and its validation.
package config

// Config is the desired state of one care-pi host.
// Keep it in sync with schema/config.schema.json and docs/config.md.
type Config struct {
	Version   int       `yaml:"version" json:"version"`
	Identity  Identity  `yaml:"identity" json:"identity"`
	Solution  Solution  `yaml:"solution" json:"solution"`
	Size      Size      `yaml:"size" json:"size"`
	TLS       TLS       `yaml:"tls" json:"tls"`
	Clock     Clock     `yaml:"clock" json:"clock"`
	Backups   Backups   `yaml:"backups" json:"backups"`
	Updates   Updates   `yaml:"updates" json:"updates"`
	Env       Env       `yaml:"env" json:"env"`
	FE        FE        `yaml:"fe" json:"fe"`
	Images    Images    `yaml:"images" json:"images"`
	Uploads   Uploads   `yaml:"uploads" json:"uploads"`
	Network   Network   `yaml:"network" json:"network"`
	Telemetry Telemetry `yaml:"telemetry" json:"telemetry"`
}

// Identity names the clinic and the host.
type Identity struct {
	DisplayName string `yaml:"display_name" json:"display_name"`
	// Hostname is the mDNS label. The host is <hostname>.local.
	Hostname string `yaml:"hostname" json:"hostname"`
}

// Solution selects the solution and its track.
type Solution struct {
	Name   string         `yaml:"name" json:"name"`
	Track  string         `yaml:"track" json:"track"`
	Pin    string         `yaml:"pin,omitempty" json:"pin,omitempty"`
	Custom SolutionCustom `yaml:"custom" json:"custom"`
}

// SolutionCustom holds the operator refs for track "custom".
type SolutionCustom struct {
	CareRef   string       `yaml:"care_ref,omitempty" json:"care_ref,omitempty"`
	CareFERef string       `yaml:"care_fe_ref,omitempty" json:"care_fe_ref,omitempty"`
	Plugs     []PlugCustom `yaml:"plugs,omitempty" json:"plugs,omitempty"`
}

// PlugCustom adds a plug or changes a plug of the solution.
type PlugCustom struct {
	ID       string              `yaml:"id" json:"id"`
	Enabled  *bool               `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Backend  *PlugCustomBackend  `yaml:"backend,omitempty" json:"backend,omitempty"`
	Frontend *PlugCustomFrontend `yaml:"frontend,omitempty" json:"frontend,omitempty"`
}

// PlugCustomBackend overrides the backend part of a plug.
type PlugCustomBackend struct {
	Name    string         `yaml:"name,omitempty" json:"name,omitempty"`
	Package string         `yaml:"package,omitempty" json:"package,omitempty"`
	Ref     string         `yaml:"ref,omitempty" json:"ref,omitempty"`
	Configs map[string]any `yaml:"configs,omitempty" json:"configs,omitempty"`
}

// PlugCustomFrontend overrides the frontend part of a plug.
type PlugCustomFrontend struct {
	Repo string `yaml:"repo,omitempty" json:"repo,omitempty"`
	Ref  string `yaml:"ref,omitempty" json:"ref,omitempty"`
	URL  string `yaml:"url,omitempty" json:"url,omitempty"`
}

// Size selects the CPU preset.
type Size struct {
	Preset string     `yaml:"preset" json:"preset"`
	Custom SizeCustom `yaml:"custom" json:"custom"`
}

// SizeCustom holds the values for preset "custom".
type SizeCustom struct {
	GunicornWorkers   int `yaml:"gunicorn_workers,omitempty" json:"gunicorn_workers,omitempty"`
	CeleryConcurrency int `yaml:"celery_concurrency,omitempty" json:"celery_concurrency,omitempty"`
	PGSharedBuffersMB int `yaml:"pg_shared_buffers_mb,omitempty" json:"pg_shared_buffers_mb,omitempty"`
}

// TLS holds the TLS modes. Phase 1 renders only local_ca.
type TLS struct {
	LocalCA   TLSLocalCA   `yaml:"local_ca" json:"local_ca"`
	Public    TLSPublic    `yaml:"public" json:"public"`
	Tailscale TLSTailscale `yaml:"tailscale" json:"tailscale"`
}

// TLSLocalCA is the offline mode: <hostname>.local and the LAN IPs with Caddy's local CA.
type TLSLocalCA struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
}

// TLSPublic is the public certificate mode (Phase 6).
type TLSPublic struct {
	Enabled  bool   `yaml:"enabled" json:"enabled"`
	ClinicID string `yaml:"clinic_id,omitempty" json:"clinic_id,omitempty"`
}

// TLSTailscale is the Tailscale mode (Phase 6).
type TLSTailscale struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
}

// Clock selects the clock gate policy.
type Clock struct {
	Policy string `yaml:"policy" json:"policy"`
}

// Backups holds the backup settings. Phase 1 uses only WALArchiveTimeoutMin and Retention.DBFull.
type Backups struct {
	WALArchiveTimeoutMin int                 `yaml:"wal_archive_timeout_min" json:"wal_archive_timeout_min"`
	Schedule             BackupSchedule      `yaml:"schedule" json:"schedule"`
	Retention            BackupRetention     `yaml:"retention" json:"retention"`
	Destinations         []BackupDestination `yaml:"destinations,omitempty" json:"destinations,omitempty"`
}

// BackupSchedule holds systemd calendar expressions (Phase 3).
type BackupSchedule struct {
	DBFull string `yaml:"db_full" json:"db_full"`
	DBIncr string `yaml:"db_incr" json:"db_incr"`
	Files  string `yaml:"files" json:"files"`
	Verify string `yaml:"verify" json:"verify"`
}

// BackupRetention holds the retention counts.
type BackupRetention struct {
	DBFull          int `yaml:"db_full" json:"db_full"`
	ArchiveDays     int `yaml:"archive_days" json:"archive_days"`
	FilesKeepDaily  int `yaml:"files_keep_daily" json:"files_keep_daily"`
	FilesKeepWeekly int `yaml:"files_keep_weekly" json:"files_keep_weekly"`
}

// BackupDestination is one restic destination (Phase 3).
type BackupDestination struct {
	ID       string `yaml:"id" json:"id"`
	Type     string `yaml:"type" json:"type"`
	Path     string `yaml:"path,omitempty" json:"path,omitempty"`
	Host     string `yaml:"host,omitempty" json:"host,omitempty"`
	Port     int    `yaml:"port,omitempty" json:"port,omitempty"`
	User     string `yaml:"user,omitempty" json:"user,omitempty"`
	Endpoint string `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
	Bucket   string `yaml:"bucket,omitempty" json:"bucket,omitempty"`
	Region   string `yaml:"region,omitempty" json:"region,omitempty"`
}

// Updates holds the update settings (Phase 4).
type Updates struct {
	Auto       bool         `yaml:"auto" json:"auto"`
	Window     UpdateWindow `yaml:"window" json:"window"`
	OSSecurity bool         `yaml:"os_security" json:"os_security"`
}

// UpdateWindow is the time window for automatic updates.
type UpdateWindow struct {
	Days  []string `yaml:"days" json:"days"`
	Start string   `yaml:"start" json:"start"`
	End   string   `yaml:"end" json:"end"`
}

// Env holds operator environment overrides. Values are not secret.
type Env struct {
	Backend  map[string]string `yaml:"backend,omitempty" json:"backend,omitempty"`
	Frontend map[string]string `yaml:"frontend,omitempty" json:"frontend,omitempty"`
}

// FE selects the frontend provider (docs/fe.md).
type FE struct {
	Provider string `yaml:"provider" json:"provider"`
	// ArtifactURL is the bundle of the artifact provider. Empty means the default bundle of the solution.
	ArtifactURL string `yaml:"artifact_url,omitempty" json:"artifact_url,omitempty"`
	// ArtifactSHA256 is the expected SHA-256 (hex) of the bundle.
	ArtifactSHA256 string `yaml:"artifact_sha256,omitempty" json:"artifact_sha256,omitempty"`
}

// Images overrides the default container images. Each value is a full image reference.
type Images struct {
	Care     string `yaml:"care,omitempty" json:"care,omitempty"`
	Postgres string `yaml:"postgres,omitempty" json:"postgres,omitempty"`
	Caddy    string `yaml:"caddy,omitempty" json:"caddy,omitempty"`
	Redis    string `yaml:"redis,omitempty" json:"redis,omitempty"`
	S3       string `yaml:"s3,omitempty" json:"s3,omitempty"`
	Node     string `yaml:"node,omitempty" json:"node,omitempty"`
}

// Uploads limits the request body size for file uploads.
type Uploads struct {
	MaxBodyMB int `yaml:"max_body_mb" json:"max_body_mb"`
}

// Network holds network settings (Phase 6).
type Network struct {
	WiFi *WiFi `yaml:"wifi,omitempty" json:"wifi,omitempty"`
}

// WiFi is a Wi-Fi profile (Phase 6).
type WiFi struct {
	SSID     string `yaml:"ssid" json:"ssid"`
	Failover bool   `yaml:"failover" json:"failover"`
}

// Telemetry holds the opt-in telemetry settings (Phase 7).
type Telemetry struct {
	Enabled        bool   `yaml:"enabled" json:"enabled"`
	RemoteWriteURL string `yaml:"remote_write_url,omitempty" json:"remote_write_url,omitempty"`
}
