// Package paths holds every absolute path that care-pi uses.
//
// The constants are host paths. Templates use them as they are.
// A Paths value adds a root prefix for file access, so tests and the
// render command can redirect all reads and writes to a different directory.
package paths

import (
	"os"
	"path/filepath"
	"strings"
)

// Host paths. See the contract table "Paths".
const (
	Binary = "/usr/local/bin/care-pi"

	EtcDir      = "/etc/care-pi"
	ConfigFile  = "/etc/care-pi/config.yaml"
	SecretsDir  = "/etc/care-pi/secrets"
	RenderedDir = "/etc/care-pi/rendered"
	LockFile    = "/etc/care-pi/lock.yaml"

	// Rendered directories. Containers bind-mount these directories, never single files.
	RenderedCaddy = "/etc/care-pi/rendered/caddy"
	RenderedCare  = "/etc/care-pi/rendered/care"
	RenderedDB    = "/etc/care-pi/rendered/db"
	RenderedFE    = "/etc/care-pi/rendered/fe"
	// RenderedBackup holds the SSH config and known_hosts of the SFTP destinations.
	RenderedBackup = "/etc/care-pi/rendered/backup"

	// Backups (docs/backups.md). The recovery key and the S3 keys of the destinations are secrets.
	RecoveryKeyFile  = "/etc/care-pi/secrets/recovery.key"
	PendingKeyFile   = "/etc/care-pi/secrets/recovery.key.pending"
	BackupSecretsDir = "/etc/care-pi/secrets/backup"

	QuadletDir = "/etc/containers/systemd"
	SystemdDir = "/etc/systemd/system"

	DataDir     = "/var/lib/care-pi"
	PGData      = "/var/lib/care-pi/pg"
	S3Data      = "/var/lib/care-pi/s3"
	RedisData   = "/var/lib/care-pi/redis"
	CaddyData   = "/var/lib/care-pi/caddy"
	BackupDir   = "/var/lib/care-pi/backup"
	BackupPG    = "/var/lib/care-pi/backup/pg"
	FEDir       = "/var/lib/care-pi/fe"
	FECurrent   = "/var/lib/care-pi/fe/current"
	PlugsDir    = "/var/lib/care-pi/plugs"
	BuildDir    = "/var/lib/care-pi/build"
	BuildCareFE = "/var/lib/care-pi/build/care_fe"
	StateDir    = "/var/lib/care-pi/state"
	JobsDir     = "/var/lib/care-pi/state/jobs"
	LockHistory = "/var/lib/care-pi/state/locks"
	ModeDir     = "/var/lib/care-pi/state/mode"
	EnsureState = "/var/lib/care-pi/state/ensure.json"
	SupportDir  = "/var/lib/care-pi/state/support"
	// BackupStateDir holds local.json, destinations/<id>.json, drill.json, key.json, checkpoints.json.
	BackupStateDir = "/var/lib/care-pi/state/backups"
	// DisksDir holds the mount points of the backup disks (disk_label).
	DisksDir = "/var/lib/care-pi/disks"
	// RestoreDir holds the side directories of a files restore and the staging of a restore from backup.
	RestoreDir = "/var/lib/care-pi/restore"
	// DrillDir holds the data directory of a restore drill while it runs.
	DrillDir = "/var/lib/care-pi/drill"
	// ResticCache is the cache of restic (metadata of each repository).
	ResticCache = "/var/cache/care-pi/restic"

	// CaddyRootCA is the root certificate of the local CA of Caddy (Caddy data /data/caddy/pki/...).
	CaddyRootCA = "/var/lib/care-pi/caddy/caddy/pki/authorities/local/root.crt"
	// CaddyCA is the local CA of Caddy (root and intermediate, certificates and keys). Each snapshot
	// has it, so a restore from backup keeps the root that the clinic devices trust (docs/backups.md).
	CaddyCA = "/var/lib/care-pi/caddy/caddy/pki/authorities/local"
	// CaddyLocalCerts holds the certificates that the local CA of Caddy issued.
	CaddyLocalCerts = "/var/lib/care-pi/caddy/caddy/certificates/local"
	// CaddyCertsDir holds the certificates of each issuer (Phase 6: the ACME certificates of the public name).
	CaddyCertsDir = "/var/lib/care-pi/caddy/caddy/certificates"

	// Phase 6 (docs/connectivity.md). CADir holds the name-constrained root of the agent (root.crt, root.key 0600).
	// care-caddy mounts it read-only at CACaddyDir. Each snapshot has it (docs/backups.md).
	CADir      = "/var/lib/care-pi/ca"
	CACaddyDir = "/etc/care-pi-ca"
	// CAState records the switch from the root of Caddy to the root of the agent.
	CAState = "/var/lib/care-pi/state/ca.json"
	// PublicState is the registration with the DNS/ACME service (no secret); TailscaleState and WiFiState
	// are the last known state of the Tailscale and Wi-Fi jobs.
	PublicState    = "/var/lib/care-pi/state/tls-public.json"
	TailscaleState = "/var/lib/care-pi/state/tls-tailscale.json"
	WiFiState      = "/var/lib/care-pi/state/wifi.json"
	// Secret files of Phase 6 (below SecretsDir). The Wi-Fi passphrase is only in the keyfile of NetworkManager
	// (0600 root): WiFiSecretFile is the file of an older agent, which the Wi-Fi jobs delete.
	DNSClientKeyFile = "/etc/care-pi/secrets/dns_client.key"
	// DNSClientPendingKeyFile is the new key of "care-pi tls public rotate --new-key" until the service took it.
	DNSClientPendingKeyFile = "/etc/care-pi/secrets/dns_client.key.pending"
	ACMEDNSFile             = "/etc/care-pi/secrets/acme_dns.json"
	WiFiSecretFile          = "/etc/care-pi/secrets/wifi.json"
	// TailscaleSocketDir is the directory of the socket of tailscaled. care-caddy mounts it when Tailscale is on.
	TailscaleSocketDir = "/run/tailscale"
	// FirewallUnit loads the table inet care-pi (the installer writes it). FirewallDropIn (Phase 7) makes it load
	// FirewallFile, the table of the agent (security.firewall, security.ssh, tls.tailscale). FirewallDropInLegacy is
	// the drop-in of Phase 6 (the Tailscale port only); apply deletes it.
	FirewallUnit         = "care-pi-firewall.service"
	FirewallDropIn       = "/etc/systemd/system/care-pi-firewall.service.d/50-care-pi.conf"
	FirewallDropInLegacy = "/etc/systemd/system/care-pi-firewall.service.d/50-care-pi-tailscale.conf"
	RenderedFirewall     = "/etc/care-pi/rendered/firewall"
	FirewallFile         = "/etc/care-pi/rendered/firewall/care-pi.nft"
	// FirewallState records the policy of the last load and the rollback timer (Phase 7, S42 default-drop).
	FirewallState = "/var/lib/care-pi/state/firewall.json"
	// Telemetry (Phase 7, docs/telemetry.md). RenderedTelemetry holds the scrape configuration of vmagent;
	// TelemetryDir holds the text file of the agent for node_exporter and the queue of vmagent.
	RenderedTelemetry     = "/etc/care-pi/rendered/telemetry"
	TelemetryDir          = "/var/lib/care-pi/telemetry"
	TelemetryTextfileDir  = "/var/lib/care-pi/telemetry/textfile"
	TelemetryQueueDir     = "/var/lib/care-pi/telemetry/vmagent"
	TelemetryPasswordFile = "/etc/care-pi/secrets/telemetry_password"
	// KeysDir holds the minisign public keys that this host trusts in addition to the keys of the agent (Phase 7).
	KeysDir = "/etc/care-pi/keys"
	// SysctlFile sets net.ipv4.ip_nonlocal_bind=1: the pod can publish on an address that is not there yet
	// (tailscale0, a second LAN address). The installer and apply write it; systemd-sysctl.service loads it.
	SysctlFile = "/etc/sysctl.d/50-care-pi.conf"
	SysctlUnit = "systemd-sysctl.service"

	// RunDir is on a tmpfs. ApplyLock serialises apply, bootstrap, fe build, and fe prune.
	RunDir    = "/run/care-pi"
	ApplyLock = "/run/care-pi/apply.lock"
	// AgentSocket is the socket of "care-pi serve". Caddy is its only client.
	AgentSocket = "/run/care-pi/api.sock"
	// RunBackupDir holds the files of one restic run (password file, SSH key, S3 credentials). tmpfs.
	RunBackupDir = "/run/care-pi/backup"
	// AgentUnit runs "care-pi serve". The installer writes it.
	AgentUnit     = "care-pi.service"
	AgentUnitFile = "/etc/systemd/system/care-pi.service"
	// ConsolePort is the port of the console origin (S48).
	ConsolePort = 8443

	// ClockTrusted records a clock that the browser set at the claim (S52): the clock gate passes, but the clock is
	// not trusted until NTP synchronizes it. A synchronized clock deletes the file.
	ClockTrusted = "/var/lib/care-pi/state/clock-trusted.json"

	// Flag files in ModeDir. Caddy checks them on each request.
	ModeClockWait   = "clock-wait"
	ModeMaintenance = "maintenance"

	// Phase 4 (docs/solutions.md, sections 6.4 and 14).
	UpdateCache      = "/var/lib/care-pi/state/update-cache"
	UpdateLockCache  = "/var/lib/care-pi/state/update-cache/locks"
	UpdateState      = "/var/lib/care-pi/state/updates.json"
	UpdateHistory    = "/var/lib/care-pi/state/update-history.jsonl"
	StagingDir       = "/var/lib/care-pi/state/staging"
	PlugConfigState  = "/var/lib/care-pi/state/plug-config.json"
	ImagesState      = "/var/lib/care-pi/state/images.json"
	MaintenanceJSON  = "/var/lib/care-pi/state/mode/maintenance.json"
	RenderedBuild    = "/etc/care-pi/rendered/build/care"
	RenderedPlugs    = "/etc/care-pi/rendered/plugs"
	BuildPlugs       = "/var/lib/care-pi/build/plugs"
	RebootRequired   = "/run/reboot-required"
	AptConfCarePi    = "/etc/apt/apt.conf.d/52care-pi.conf"
	PlugSecretPrefix = "plug-"

	// Phase 5: the Raspberry Pi image (docs/image.md).
	// LocalDist is the dist snapshot of the image: index/index.json and the <tag>/<asset> files.
	LocalDist = "/var/lib/care-pi-dist"
	// ImageRelease describes the image (version, lock, build date, git sha). Only an image has it.
	ImageRelease = "/etc/care-pi/image-release.json"
	// PreseedConfig is a config.yaml that cloud-init write_files puts on the device at the first
	// boot. "care-pi bootstrap --from-image" installs it and applies everything (no console setup).
	PreseedConfig = "/etc/care-pi/preseed.yaml"
	// FirstbootDone marks the end of a successful "care-pi bootstrap --from-image".
	// care-firstboot.service does not run when it exists.
	FirstbootDone = "/var/lib/care-pi/state/firstboot-done"
	// FirstbootError records why the first boot did not use the pre-seed (it started setup mode).
	// GET /_care-pi/status shows it as firstboot_error until the setup is finished.
	FirstbootError = "/var/lib/care-pi/state/firstboot-error.json"
)

// UID and GID of the postgres user in the postgres image.
const (
	PostgresUID = 999
	PostgresGID = 999
)

// Paths maps host paths to paths below Root.
type Paths struct {
	Root string
}

// New returns a Paths for root. An empty root means "/".
func New(root string) Paths {
	if root == "" {
		root = "/"
	}
	return Paths{Root: root}
}

// Abs returns the location of the host path p below Root.
func (p Paths) Abs(hostPath string) string {
	if p.Root == "" || p.Root == "/" {
		return filepath.Clean(hostPath)
	}
	return filepath.Join(p.Root, hostPath)
}

// Host returns the host path for a path below Root. It is the inverse of Abs.
func (p Paths) Host(abs string) string {
	if p.Root == "" || p.Root == "/" {
		return abs
	}
	rel, err := filepath.Rel(p.Root, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return abs
	}
	return "/" + filepath.ToSlash(rel)
}

// Secret returns the host path of the secret file name.
func Secret(name string) string {
	return SecretsDir + "/" + name
}

// Dir is one directory of the host layout.
type Dir struct {
	Path string
	Mode os.FileMode
	UID  int
	GID  int
}

// Layout returns the directories that bootstrap and apply create when they are missing.
// The order puts parents before children.
func Layout() []Dir {
	return []Dir{
		{EtcDir, 0o700, 0, 0},
		{SecretsDir, 0o700, 0, 0},
		{RenderedDir, 0o700, 0, 0},
		{RenderedCaddy, 0o700, 0, 0},
		{RenderedCare, 0o700, 0, 0},
		{RenderedDB, 0o750, 0, PostgresGID},
		{RenderedFE, 0o700, 0, 0},
		{RenderedBackup, 0o700, 0, 0},
		{QuadletDir, 0o755, 0, 0},
		{SystemdDir, 0o755, 0, 0},
		{DataDir, 0o755, 0, 0},
		{PGData, 0o700, PostgresUID, PostgresGID},
		{S3Data, 0o700, 0, 0},
		{RedisData, 0o700, 0, 0},
		{CaddyData, 0o700, 0, 0},
		{CADir, 0o700, 0, 0},
		{BackupDir, 0o700, 0, 0},
		{BackupPG, 0o700, PostgresUID, PostgresGID},
		{FEDir, 0o755, 0, 0},
		{PlugsDir, 0o755, 0, 0},
		{BuildDir, 0o755, 0, 0},
		{BuildCareFE, 0o755, 0, 0},
		{StateDir, 0o700, 0, 0},
		{JobsDir, 0o700, 0, 0},
		{LockHistory, 0o700, 0, 0},
		{ModeDir, 0o755, 0, 0},
		{BackupStateDir, 0o700, 0, 0},
		{DisksDir, 0o755, 0, 0},
		{RestoreDir, 0o700, 0, 0},
		{UpdateCache, 0o700, 0, 0},
		{UpdateLockCache, 0o700, 0, 0},
		{StagingDir, 0o700, 0, 0},
	}
}
