package backup

import (
	"fmt"
	"strings"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// Destination types.
const (
	TypePOSIX = "posix"
	TypeSFTP  = "sftp"
	TypeS3    = "s3"
)

// Defaults of a destination (docs/config.md).
const (
	DefaultIntervalMin    = 15
	DefaultKeepWithinDays = 14
	DefaultKeepMonthly    = 6
	DefaultSFTPPort       = 22
	// DBKeepWithin is the --keep-within of the db snapshots (owner decision of wave 5a). A db snapshot
	// holds the whole local repository of its time, so the snapshots of the last 48 hours plus the
	// daily, weekly, and monthly ones give each restore point. 14 days of 15-minute snapshots would be
	// 1344 snapshots and a slow forget.
	DBKeepWithin = "48h"
)

// Snapshot tags.
const (
	TagDB    = "db"
	TagFiles = "files"
)

// Retention is the effective restic forget policy of a destination.
type Retention struct {
	KeepWithinDays int `json:"keep_within_days"`
	KeepDaily      int `json:"keep_daily"`
	KeepWeekly     int `json:"keep_weekly"`
	KeepMonthly    int `json:"keep_monthly"`
}

// Effective returns the destination with the defaults of interval_min, retention, and port.
func Effective(d config.BackupDestination, b config.Backups) config.BackupDestination {
	if d.IntervalMin == 0 {
		d.IntervalMin = DefaultIntervalMin
	}
	if d.Type == TypeSFTP && d.Port == 0 {
		d.Port = DefaultSFTPPort
	}
	r := RetentionOf(d, b)
	d.Retention = &config.DestinationRetention{KeepWithinDays: &r.KeepWithinDays, KeepDaily: &r.KeepDaily,
		KeepWeekly: &r.KeepWeekly, KeepMonthly: &r.KeepMonthly}
	return d
}

// RetentionOf returns the effective retention of a destination.
func RetentionOf(d config.BackupDestination, b config.Backups) Retention {
	r := Retention{KeepWithinDays: DefaultKeepWithinDays, KeepDaily: b.Retention.FilesKeepDaily,
		KeepWeekly: b.Retention.FilesKeepWeekly, KeepMonthly: DefaultKeepMonthly}
	if x := d.Retention; x != nil {
		if x.KeepWithinDays != nil {
			r.KeepWithinDays = *x.KeepWithinDays
		}
		if x.KeepDaily != nil {
			r.KeepDaily = *x.KeepDaily
		}
		if x.KeepWeekly != nil {
			r.KeepWeekly = *x.KeepWeekly
		}
		if x.KeepMonthly != nil {
			r.KeepMonthly = *x.KeepMonthly
		}
	}
	return r
}

// RepoDir returns the directory name of the restic repository of a key: care-pi-<key id>.
func RepoDir(keyID string) string { return "care-pi-" + keyID }

// DiskMount returns the mount point of a backup disk.
func DiskMount(label string) string { return paths.DisksDir + "/" + label }

// Base returns the place that holds the care-pi-<key id> directories, without the scheme.
func Base(d config.BackupDestination) string {
	switch d.Type {
	case TypePOSIX:
		if d.DiskLabel != "" {
			return DiskMount(d.DiskLabel)
		}
		return strings.TrimRight(d.Path, "/")
	case TypeSFTP:
		return strings.TrimRight(d.Path, "/")
	case TypeS3:
		b := strings.TrimRight(d.Endpoint, "/") + "/" + d.Bucket
		if d.Prefix != "" {
			b += "/" + d.Prefix
		}
		return b
	}
	return ""
}

// Location returns the restic repository without secrets, as the console shows it.
func Location(d config.BackupDestination, keyID string) string {
	switch d.Type {
	case TypeSFTP:
		return fmt.Sprintf("sftp:%s@%s:%s/%s", d.User, d.Host, strings.TrimRight(d.Path, "/"), RepoDir(keyID))
	case TypeS3:
		return "s3:" + Base(d) + "/" + RepoDir(keyID)
	}
	return Base(d) + "/" + RepoDir(keyID)
}

// SSHAlias returns the Host alias of an SFTP destination in the SSH config of the agent.
func SSHAlias(id string) string {
	if id == "" {
		id = "candidate"
	}
	return "care-dest-" + id
}

// Repository returns RESTIC_REPOSITORY. An SFTP destination uses its alias of the SSH config, which
// has the host, the port, the user, and the pinned host key.
func Repository(d config.BackupDestination, keyID string) string {
	if d.Type == TypeSFTP {
		return fmt.Sprintf("sftp:%s:%s/%s", SSHAlias(d.ID), strings.TrimRight(d.Path, "/"), RepoDir(keyID))
	}
	return Location(d, keyID)
}

// HostKeyType returns the key type of a host_key value ("ecdsa-sha2-nistp256 AAAA…").
func HostKeyType(hostKey string) string {
	t, _, _ := strings.Cut(strings.TrimSpace(hostKey), " ")
	return t
}

// hostKeyAlgorithms returns the HostKeyAlgorithms of a pinned key type. An RSA key allows the
// SHA-2 signatures; OpenSSH 8.8 and later refuse ssh-rsa (SHA-1).
func hostKeyAlgorithms(keyType string) string {
	if keyType == "ssh-rsa" {
		return "rsa-sha2-512,rsa-sha2-256,ssh-rsa"
	}
	return keyType
}

// SSHConfig returns the SSH config of the SFTP destinations: one Host block for each destination
// with its pinned host key. knownHosts is the path of the known_hosts file.
func SSHConfig(dests []config.BackupDestination, knownHosts string) string {
	var b strings.Builder
	for _, d := range dests {
		if d.Type != TypeSFTP {
			continue
		}
		port := d.Port
		if port == 0 {
			port = DefaultSFTPPort
		}
		alias := SSHAlias(d.ID)
		fmt.Fprintf(&b, "Host %s\n", alias)
		fmt.Fprintf(&b, "  HostName %s\n", d.Host)
		fmt.Fprintf(&b, "  Port %d\n", port)
		fmt.Fprintf(&b, "  User %s\n", d.User)
		fmt.Fprintf(&b, "  HostKeyAlias %s\n", alias)
		if t := HostKeyType(d.HostKey); t != "" {
			fmt.Fprintf(&b, "  HostKeyAlgorithms %s\n", hostKeyAlgorithms(t))
		}
		fmt.Fprintf(&b, "  UserKnownHostsFile %s\n", knownHosts)
		b.WriteString("  GlobalKnownHostsFile /dev/null\n")
		b.WriteString("  StrictHostKeyChecking yes\n")
		b.WriteString("  UpdateHostKeys no\n")
		b.WriteString("  IdentitiesOnly yes\n")
		b.WriteString("  PasswordAuthentication no\n")
		b.WriteString("  KbdInteractiveAuthentication no\n")
		b.WriteString("  BatchMode yes\n")
		b.WriteString("  ConnectTimeout 20\n")
		b.WriteString("  ServerAliveInterval 15\n")
		b.WriteString("  ServerAliveCountMax 4\n\n")
	}
	return b.String()
}

// KnownHosts returns the known_hosts lines of the SFTP destinations with a pinned host key.
func KnownHosts(dests []config.BackupDestination) string {
	var b strings.Builder
	for _, d := range dests {
		if d.Type == TypeSFTP && d.HostKey != "" {
			fmt.Fprintf(&b, "%s %s\n", SSHAlias(d.ID), strings.TrimSpace(d.HostKey))
		}
	}
	return b.String()
}
