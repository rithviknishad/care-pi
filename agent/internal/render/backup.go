package render

import (
	"bytes"
	"fmt"
	"path"
	"strings"
	"text/template"

	"github.com/rithviknishad/care-pi/agent/internal/backup"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// BackupTarget groups the backup timers and the automount units of the backup disks.
const BackupTarget = "care-backup.target"

// Memory caps of the backup services (docs/backups.md, "Memory").
const (
	BackupMemoryMax  = "512M"
	BackupMemorySwap = "128M"
)

// BackupModel is the backup part of the model (docs/backups.md, "Units").
type BackupModel struct {
	// Wants are the units of care-backup.target: the timers (not with paused) and the automounts.
	Wants      []string
	Paused     bool
	DBFull     string
	DBIncr     string
	Files      string
	Verify     string
	Drill      string
	Drills     bool
	Dests      []BackupDest
	Mounts     []BackupMount
	Timers     []string
	Binary     string
	SSHConfig  string
	KnownHosts string
}

// BackupDest is one destination for its timers.
type BackupDest struct {
	ID          string
	IntervalMin int
}

// BackupMount is the mount of one backup disk.
type BackupMount struct {
	Label     string
	Where     string
	Mount     string
	Automount string
}

// Calendar returns the systemd calendar of a backups.schedule value. "daily HH:MM" is not a systemd
// calendar, so it becomes "*-*-* HH:MM". Each other value stays.
func Calendar(s string) string {
	s = strings.TrimSpace(s)
	if rest, ok := strings.CutPrefix(s, "daily "); ok {
		return "*-*-* " + strings.TrimSpace(rest)
	}
	return s
}

// Timer names of the local work.
const (
	TimerDBFull = "care-backup-db-full.timer"
	TimerDBIncr = "care-backup-db-incr.timer"
	TimerVerify = "care-backup-verify.timer"
	TimerDrill  = "care-backup-drill.timer"
)

// DestTimer returns the timer of a destination: kind db or files.
func DestTimer(kind, id string) string { return "care-backup-" + kind + "@" + id + ".timer" }

func newBackupModel(cfg *config.Config) BackupModel {
	b := cfg.Backups
	m := BackupModel{Paused: b.Paused, DBFull: Calendar(b.Schedule.DBFull), DBIncr: Calendar(b.Schedule.DBIncr),
		Files: Calendar(b.Schedule.Files), Verify: Calendar(b.Schedule.Verify), Drill: Calendar(b.Schedule.Drill),
		Drills: b.DrillEnabled, Binary: paths.Binary}
	m.Timers = []string{TimerDBFull, TimerDBIncr, TimerVerify}
	if m.Drills {
		m.Timers = append(m.Timers, TimerDrill)
	}
	var effective []config.BackupDestination
	for _, d := range b.Destinations {
		e := backup.Effective(d, b)
		effective = append(effective, e)
		m.Dests = append(m.Dests, BackupDest{ID: d.ID, IntervalMin: e.IntervalMin})
		m.Timers = append(m.Timers, DestTimer("db", d.ID), DestTimer("files", d.ID))
		if d.Type == backup.TypePOSIX && d.DiskLabel != "" {
			where := backup.DiskMount(d.DiskLabel)
			m.Mounts = append(m.Mounts, BackupMount{Label: d.DiskLabel, Where: where,
				Mount: backup.MountUnit(where), Automount: backup.AutomountUnit(where)})
		}
	}
	if !m.Paused {
		m.Wants = append(m.Wants, m.Timers...)
	}
	for _, mt := range m.Mounts {
		m.Wants = append(m.Wants, mt.Automount)
	}
	m.SSHConfig = strings.TrimSpace(backup.SSHConfig(effective, paths.RenderedBackup+"/known_hosts"))
	m.KnownHosts = strings.TrimSpace(backup.KnownHosts(effective))
	return m
}

type serviceData struct {
	Header, Comment, Description, ExecStart, MemoryMax, MemorySwapMax, Timeout string
}

type timerData struct {
	Header, Description, OnCalendar, Unit string
	IntervalMin                           int
}

type mountData struct {
	Header, Label, Where string
}

// renderBackup adds the backup units to the tree (full phase only).
func renderBackup(t *template.Template, m *Model, tree Tree) error {
	b := m.Backup
	exec := func(name string, data any, out string, mode int) error {
		var buf bytes.Buffer
		if err := t.ExecuteTemplate(&buf, name, data); err != nil {
			return fmt.Errorf("render: %s: %w", name, err)
		}
		tree[out] = File{Path: out, Content: tidy(buf.Bytes()), Mode: 0o644}
		if mode != 0 {
			f := tree[out]
			f.Mode = 0o600
			tree[out] = f
		}
		return nil
	}
	svc := func(unit, desc, comment, args, timeout string) error {
		return exec("care-backup.service.tmpl", serviceData{Header: Header, Comment: comment, Description: desc,
			ExecStart: b.Binary + " " + args, MemoryMax: BackupMemoryMax, MemorySwapMax: BackupMemorySwap, Timeout: timeout},
			systemd(unit), 0)
	}
	timer := func(unit, desc, cal string, interval int) error {
		return exec("care-backup.timer.tmpl", timerData{Header: Header, Description: desc, OnCalendar: cal, IntervalMin: interval},
			systemd(unit), 0)
	}
	if err := exec("care-backup.target.tmpl", m, systemd(BackupTarget), 0); err != nil {
		return err
	}
	type local struct{ unit, desc, comment, args, cal, timeout string }
	locals := []local{
		{"care-backup-db-full", "care-pi full backup of the database", "A full pgBackRest backup to the local repository. The CLI writes a job with the source timer.",
			"backup run --scope local-db --type full --from-timer", b.DBFull, "6h"},
		{"care-backup-db-incr", "care-pi incremental backup of the database", "An incremental pgBackRest backup to the local repository. The CLI writes a job with the source timer.",
			"backup run --scope local-db --type incr --from-timer", b.DBIncr, "6h"},
		{"care-backup-verify", "care-pi verify of the backups", "pgbackrest verify of the local repository; restic forget, prune, and check of each destination.",
			"backup verify --scope all --prune --from-timer", b.Verify, "12h"},
		{"care-backup-drill", "care-pi restore drill", "A restore of the newest local backup into a temporary container (archive_mode=off), then a check, then a delete.",
			"backup drill --from-timer", b.Drill, "2h"},
	}
	for _, l := range locals {
		if err := svc(l.unit+".service", l.desc, l.comment, l.args, l.timeout); err != nil {
			return err
		}
		if l.unit == "care-backup-drill" && !b.Drills {
			continue
		}
		if err := timer(l.unit+".timer", l.desc+" (timer)", l.cal, 0); err != nil {
			return err
		}
	}
	if err := svc("care-backup-db@.service", "care-pi db snapshot to the destination %i",
		"A restic snapshot of the local repository and /etc/care-pi to one destination. A timer run writes no job (docs/backups.md).",
		"backup run --scope destination --destination %i --snapshot db --from-timer", "6h"); err != nil {
		return err
	}
	if err := svc("care-backup-files@.service", "care-pi files snapshot to the destination %i",
		"A restic snapshot of the files (/var/lib/care-pi/s3, with xattrs) and /etc/care-pi to one destination.",
		"backup run --scope destination --destination %i --snapshot files --from-timer", "12h"); err != nil {
		return err
	}
	for _, d := range b.Dests {
		if err := timer(DestTimer("db", d.ID), "care-pi db snapshots to "+d.ID, "", d.IntervalMin); err != nil {
			return err
		}
		if err := timer(DestTimer("files", d.ID), "care-pi files snapshot to "+d.ID, b.Files, 0); err != nil {
			return err
		}
	}
	for _, mt := range b.Mounts {
		data := mountData{Header: Header, Label: mt.Label, Where: mt.Where}
		if err := exec("care-backup-disk.mount.tmpl", data, systemd(mt.Mount), 0); err != nil {
			return err
		}
		if err := exec("care-backup-disk.automount.tmpl", data, systemd(mt.Automount), 0); err != nil {
			return err
		}
	}
	if err := exec("backup-ssh_config.tmpl", m, paths.RenderedBackup+"/ssh_config", 1); err != nil {
		return err
	}
	return exec("backup-known_hosts.tmpl", m, paths.RenderedBackup+"/known_hosts", 1)
}

// BackupUnit reports whether a path of the systemd directory is a backup unit of care-pi.
func BackupUnit(p string) bool {
	if path.Dir(p) != paths.SystemdDir {
		return false
	}
	name := path.Base(p)
	if strings.HasPrefix(name, "care-backup") {
		switch path.Ext(name) {
		case ".service", ".timer", ".target":
			return true
		}
	}
	return MountFile(p)
}

// MountFile reports whether a path is a mount or an automount unit of a backup disk.
func MountFile(p string) bool {
	if path.Dir(p) != paths.SystemdDir {
		return false
	}
	name := path.Base(p)
	prefix := backup.EscapePath(paths.DisksDir) + "-"
	return strings.HasPrefix(name, prefix) && (path.Ext(name) == ".mount" || path.Ext(name) == ".automount")
}

// MountUnits returns the mount and the automount unit of a backup disk (the job disk-prepare writes
// them before an apply; apply renders the same files).
func MountUnits(label string) (map[string][]byte, error) {
	t, err := templates()
	if err != nil {
		return nil, err
	}
	where := backup.DiskMount(label)
	data := mountData{Header: Header, Label: label, Where: where}
	out := map[string][]byte{}
	for tmpl, name := range map[string]string{"care-backup-disk.mount.tmpl": backup.MountUnit(where), "care-backup-disk.automount.tmpl": backup.AutomountUnit(where)} {
		var b bytes.Buffer
		if err := t.ExecuteTemplate(&b, tmpl, data); err != nil {
			return nil, err
		}
		out[name] = tidy(b.Bytes())
	}
	return out, nil
}
