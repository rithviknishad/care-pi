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

	// CaddyRootCA is the root certificate of the local CA of Caddy (Caddy data /data/caddy/pki/...).
	CaddyRootCA = "/var/lib/care-pi/caddy/caddy/pki/authorities/local/root.crt"

	// RunDir is on a tmpfs. ApplyLock serialises apply, bootstrap, fe build, and fe prune.
	RunDir    = "/run/care-pi"
	ApplyLock = "/run/care-pi/apply.lock"
	// AgentSocket is the socket of "care-pi serve". Caddy is its only client.
	AgentSocket = "/run/care-pi/api.sock"
	// AgentUnit runs "care-pi serve". The installer writes it.
	AgentUnit     = "care-pi.service"
	AgentUnitFile = "/etc/systemd/system/care-pi.service"
	// ConsolePort is the port of the console origin (S48).
	ConsolePort = 8443

	// Flag files in ModeDir. Caddy checks them on each request.
	ModeClockWait   = "clock-wait"
	ModeMaintenance = "maintenance"
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
		{QuadletDir, 0o755, 0, 0},
		{SystemdDir, 0o755, 0, 0},
		{DataDir, 0o755, 0, 0},
		{PGData, 0o700, PostgresUID, PostgresGID},
		{S3Data, 0o700, 0, 0},
		{RedisData, 0o700, 0, 0},
		{CaddyData, 0o700, 0, 0},
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
	}
}
