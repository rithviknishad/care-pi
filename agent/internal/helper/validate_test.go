package helper

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/backup"
	"github.com/rithviknishad/care-pi/agent/internal/firewall"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/restore"
	"github.com/rithviknishad/care-pi/agent/internal/tailscale"
	"github.com/rithviknishad/care-pi/agent/internal/update"
	"github.com/rithviknishad/care-pi/agent/internal/wifi"
)

func cmd(name string, args ...string) host.Cmd { return host.Cmd{Name: name, Args: args} }

// pgImage is the postgres image of the lock of testRoot.
const pgImage = "ghcr.io/rithviknishad/care-pi/postgres-pgbackrest@sha256:1"

// testRoot returns a root with lock.yaml (the postgres image pgImage) and config.yaml (images.caddy), for the image
// check of "podman run" (M2 of the Phase 7 review).
func testRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(p, data string) {
		f := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(paths.LockFile, "version: 2\nimages:\n  postgres:\n    ref: ghcr.io/rithviknishad/care-pi/postgres-pgbackrest:17\n    digest: sha256:1\n")
	write(paths.ConfigFile, "images:\n  caddy: localhost/caddy:test\n  care: img\n")
	return root
}

// TestAllowed: the commands of the agent (the shapes of the command catalogue) pass.
func TestAllowed(t *testing.T) {
	root := testRoot(t)
	ok := []host.Cmd{
		cmd("systemctl", "daemon-reload"),
		cmd("systemctl", "start", "care.target"),
		cmd("systemctl", "start", "--no-block", "care-backup-db-full.service"),
		cmd("systemctl", "restart", "care-api.service", "care-worker.service"),
		cmd("systemctl", "enable", "care-start.service"),
		cmd("systemctl", "disable", "--now", "care-dns-update.timer"),
		cmd("systemctl", "try-restart", "avahi-daemon.service"),
		cmd("systemctl", "try-reload-or-restart", "care-pi-firewall.service"),
		cmd("systemctl", "reset-failed", "care-pod.service", "care-db.service"),
		cmd("systemctl", "show", "-p", "ActiveState", "-p", "MemoryPeak", "care.target", "care-api.service"),
		cmd("systemctl", "show", "--property", "ActiveState", "care.target"),
		cmd("systemctl", "list-units", "--all", "--plain", "--no-legend", "--no-pager", "var-lib-care*pi-disks-*"),
		cmd("systemctl", "list-timers", "--all", "--output=json", "care-backup-*"),
		cmd("systemctl", "start", `var-lib-care\x2dpi-disks-usb.automount`),
		cmd("systemctl", "stop", "care-pi-firewall-rollback.timer"),
		cmd("systemctl", "reboot"),
		cmd("podman", "version", "--format", "{{.Client.Version}}"),
		cmd("podman", "secret", "ls", "--format", "{{.Name}}"),
		cmd("podman", "secret", "create", "--replace", "care-pi-django-secret-key", "-"),
		cmd("podman", "secret", "rm", "--ignore", "care-pi-acme-dns"),
		cmd("podman", "image", "exists", "ghcr.io/rithviknishad/care-pi/caddy@sha256:0123"),
		cmd("podman", "pull", "--quiet", "docker.io/library/redis:8-alpine"),
		cmd("podman", "ps", "--all", "--filter", "pod=care", "--format", "{{.Names}}\t{{.Status}}"),
		cmd("podman", "exec", "--user", "postgres", "care-db", "pgbackrest", "--stanza=main", "info", "--output=json"),
		cmd("podman", "exec", "care-caddy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"),
		cmd("podman", "exec", "-i", "care-api", "python", "manage.py", "shell"),
		cmd("podman", "stop", "--ignore", "--time", "30", "care-drill"),
		cmd("podman", "rm", "--force", "--ignore", "care-drill"),
		cmd("podman", "rm", "--force", "--ignore", "care-drill-restore"),
		restore.HostPodman("rm", "--force", "--ignore", "care-drill"),
		restore.HostPodman("rm", "--force", "--ignore", "care-drill-restore"),
		restore.HostPodman("stop", "--ignore", "--time", "30", "care-drill"),
		cmd("journalctl", "--unit", "care-api.service", "--output", "json", "--no-pager", "--lines", "200", "--follow"),
		cmd("journalctl", "--unit", "care.target", "--output", "json", "--no-pager", "--after-cursor", "s=1;i=2", "--since", "@1790000000"),
		cmd("skopeo", "inspect", "--override-os", "linux", "--override-arch", "arm64", "--format", "{{.Digest}}", "docker://ghcr.io/ohcnetwork/care:latest"),
		cmd("timedatectl", "show", "--property", "NTPSynchronized", "--value"),
		{Name: "restic", Args: []string{"-o", "s3.bucket-lookup=path", "-o", "s3.region=auto", "snapshots", "--json"}, Env: []string{"RESTIC_REPOSITORY=s3:x", "RESTIC_PASSWORD_FILE=/run/care-pi-backup/s-1/pw"}},
		cmd("restic", "-o", "sftp.args=-F /run/care-pi-backup/s-1/ssh_config -i /run/care-pi-backup/s-1/id_ed25519", "init"),
		cmd("restic", "-o", "sftp.args=-F /etc/care-pi/rendered/backup/ssh_config -i /run/care-pi-backup/s-1/id_ed25519", "cat", "config"),
		cmd("restic", append([]string{"-o", "s3.bucket-lookup=path"}, backup.SnapshotArgs(backup.TagDB, "care", paths.CADir, paths.CaddyCA)...)...),
		cmd("restic", backup.SnapshotArgs(backup.TagFiles, "sunrise")...),
		cmd("restic", backup.ForgetArgs(backup.TagFiles, backup.Retention{KeepWithinDays: 7, KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 12})...),
		cmd("restic", backup.ForgetArgs(backup.TagDB, backup.Retention{})...),
		cmd("restic", "forget", "--keep-tag", "care-pi-keep-nothing"),
		cmd("restic", backup.CheckArgs(5)...),
		cmd("restic", "stats", "--mode", "raw-data", "--json"),
		cmd("restic", "snapshots", "--json", "4f1e9a2c"),
		cmd("restic", "unlock"),
		cmd("restic", "prune"),
		cmd("restic", "dump", "4f1e9a2c", paths.ConfigFile),
		cmd("restic", backup.RestoreArgs("4f1e9a2c", paths.S3Data, "/var/lib/care-pi/restore/job-1", []string{"bucket/a.pdf"}, true)...),
		cmd("restic", backup.RestoreArgs("4f1e9a2c", paths.S3Data, paths.S3Data, nil, false)...),
		cmd("restic", backup.RestoreArgs("4f1e9a2c", paths.BackupPG, paths.BackupPG, nil, false)...),
		cmd("restic", backup.RestoreArgs("4f1e9a2c", paths.EtcDir, paths.RestoreDir+"/job-1", nil, false)...),
		cmd("nmcli", "--wait", "45", "connection", "up", wifi.ConnName),
		cmd("nmcli", "-t", "-f", "GENERAL.STATE,GENERAL.CONNECTION,IP4.ADDRESS", "device", "show", "wlan0"),
		cmd("nmcli", "-t", "-g", "802-11-wireless.ssid", "connection", "show", "Clinic Wi-Fi"),
		cmd("journalctl", "--unit", "care-backup-*", "--output", "json", "--no-pager", "--lines", "100"),
		cmd("tailscale", "up", "--hostname=care", "--accept-dns=false", "--accept-routes=false", "--reset", "--login-server=https://hs.example.org", "--auth-key=file:/run/care-pi/tailscale-auth-key", "--timeout=2m0s"),
		cmd("tailscale", "status", "--json"),
		cmd("tailscale", "serve", "status", "--json"),
		cmd("tailscale", "up", "--hostname=care", "--accept-dns=false", "--accept-routes=false", "--reset", "--login-server=https://hs.example.org", "--force-reauth", "--auth-key=file:/run/care-pi/tailscale-auth-key", "--timeout=2m0s"),
		cmd("hostnamectl", "set-hostname", "sunrise"),
		cmd("apt", "list", "--upgradable"),
		cmd("apt-cache", "policy", "podman", "crun"),
		cmd("dpkg-query", "-W", "-f=${Package}\t${Version}\n", "podman"),
		cmd("lsblk", "--json", "--bytes", "--output", "NAME,PATH,SIZE,TYPE,TRAN,RM,MODEL,SERIAL,FSTYPE,LABEL,MOUNTPOINTS"),
		cmd("wipefs", "--all", "--force", "/dev/sda1"),
		cmd("sfdisk", "--wipe", "always", "--wipe-partitions", "always", "--label", "gpt", "/dev/sda"),
		cmd("mkfs.ext4", "-F", "-q", "-m", "0", "-L", "care-usb", "/dev/sda1"),
		cmd("udevadm", "settle", "--timeout=30"),
		cmd("ip", "-json", "address", "show"),
		cmd("iw", "reg", "set", "IN"),
		cmd("sh", "-c", "command -v raspi-config"),
		cmd("sftp", "-F", "/run/care-pi-backup/s-1/ssh_config", "-i", "/run/care-pi-backup/s-1/id_ed25519", "-b", "-", "care-dest-nas"),
		host.WriteAptConfCmd(true),
		host.WriteAptConfCmd(false),
		cmd(paths.Binary, "backup", "destination", "probe", "/srv/care-backups"),
		cmd("nft", "chain inet care-pi input { policy accept ; }"),
		cmd("ss", "-ltnpH"),
		firewall.TimerCmd(firewall.RollbackDelay),
		firewall.LoadPendingCmd(),
		firewall.StopTimerCmd(),
		update.AptRun("care-pi-apt", "apt-get", "-o", "DPkg::Lock::Timeout=600", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold", "-y", "install", "podman=5.4.2+ds1-2"),
		update.AptRun("care-pi-apt-mark", "apt-mark", "-o", "DPkg::Lock::Timeout=600", "hold", "podman", "crun"),
		restore.PodmanRun(pgImage, "care-pi-restore", paths.PGData, false, "pgbackrest", "--stanza=main", "restore", "--delta"),
		restore.PodmanRun("ghcr.io/rithviknishad/care-pi/postgres-pgbackrest:17", "care-drill", paths.DrillDir+"/x", true, "", "postgres"),
		tailscale.InstallCmd(),
		apply.WAFModulesCmd("ghcr.io/rithviknishad/care-pi/caddy@sha256:2"),
		cmd("systemd-run", "--wait", "--collect", "--pipe", "--quiet", "--unit="+host.SetClockUnit, "--", "/bin/sh", "-c", host.SetClockScript, "sh", "1793600000"),
		cmd("systemd-run", "--wait", "--collect", "--pipe", "--quiet", "--unit=care-pi-wifi-keyfile", "--", "/bin/sh", "-c", wifi.KeyfileScript, "sh", wifi.KeyfilePath),
		cmd("systemd-run", "--wait", "--collect", "--pipe", "--quiet", "--unit=care-pi-sysctl", "--", "/bin/sh", "-c", apply.SysctlScript, "sh", paths.SysctlFile),
		cmd("systemd-run", "--wait", "--collect", "--pipe", "--quiet", "--unit=care-pi-wifi-country", "--", "raspi-config", "nonint", "do_wifi_country", "IN"),
		cmd("systemd-run", "--collect", "--quiet", "--unit="+tailscale.UpUnit, "--", "tailscale", "up", "--hostname=care", "--accept-dns=false", "--accept-routes=false", "--reset", "--timeout=15m0s"),
	}
	for _, c := range ok {
		if err := ValidateIn(root, c); err != nil {
			t.Errorf("%s: %v", c.String(), err)
		}
	}
	_ = time.Second
}

// TestDenied is a table of bad commands: other programs, other units, flags that give more power, paths outside
// care-pi, shell scripts, and environment variables.
func TestDenied(t *testing.T) {
	root := testRoot(t)
	bad := []host.Cmd{
		cmd("bash", "-c", "id"),
		cmd("/bin/sh", "-c", "id"),
		cmd("rm", "-rf", "/"),
		cmd("sh", "-c", "id"),
		cmd("systemctl", "start", "ssh.service"),
		cmd("systemctl", "stop", "systemd-journald.service"),
		cmd("systemctl", "enable", "--now", "evil.service"),
		cmd("systemctl", "start", "care.target", "--user"),
		cmd("systemctl", "edit", "care-api.service"),
		cmd("systemctl", "set-property", "care-api.service", "MemoryMax=1T"),
		cmd("systemctl", "daemon-reload", "now"),
		cmd("systemd-run", "--wait", "--unit=care-pi-apt-conf", "--", "tee", "/etc/shadow"),
		cmd("systemd-run", "--wait", "--unit=care-pi-apt-conf", "--", "tee", "-a", "/etc/apt/apt.conf.d/52care-pi.conf"),
		cmd("journalctl", "--unit", "ssh*", "--lines", "10"),
		cmd("systemctl", "show", "-p", "Environment;id", "care.target"),
		cmd("systemctl", "list-units", "*"),
		cmd("systemctl"),
		cmd("podman", "run", "--privileged", "alpine"),
		cmd("podman", "rm", "--force", "--ignore", "care-db"),
		cmd("podman", "rm", "--force", "care-drill", "care-api"),
		cmd("podman", "rm", "--all", "--force"),
		restore.HostPodman("rm", "--force", "--ignore", "care-api"),
		restore.HostPodman("stop", "care-db"),
		cmd("podman", "run", "--rm", "--volume", "/:/host", "alpine"),
		cmd("podman", "run", "--rm", "--volume", "/var/lib/care-pi/../../etc:/x", "alpine"),
		cmd("podman", "exec", "--privileged", "care-api", "sh"),
		cmd("podman", "exec", "other-container", "sh"),
		cmd("podman", "exec", "--env", "LD_PRELOAD=/x", "care-api", "sh"),
		cmd("podman", "exec", "care-api"),
		cmd("podman", "secret", "create", "--replace", "other", "-"),
		cmd("podman", "secret", "inspect", "--showsecret", "care-pi-django-secret-key"),
		cmd("podman", "pull", "--quiet", "--tls-verify=false", "x"),
		cmd("podman", "image", "rm", "-f", "x"),
		cmd("podman", "system", "reset", "-f"),
		cmd("podman", "volume", "rm", "--all"),
		cmd("journalctl", "--unit", "ssh.service"),
		cmd("journalctl", "--no-pager"),
		cmd("journalctl", "--unit", "care-api.service", "--output-fields", "x"),
		cmd("journalctl", "--unit", "care-api.service", "--since", "yesterday; id"),
		cmd("systemd-run", "--", "/bin/sh", "-c", "id"),
		cmd("systemd-run", "--unit=care-pi-x", "--", "/bin/sh", "-c", host.SetClockScript+" ", "sh", "1"),
		cmd("systemd-run", "--unit=care-pi-x", "--", "/bin/sh", "-c", host.SetClockScript, "sh", "1; id"),
		cmd("systemd-run", "--unit=care-pi-sysctl", "--", "/bin/sh", "-c", apply.SysctlScript, "sh", "/etc/shadow"),
		cmd("systemd-run", "--unit=care-pi-wifi-keyfile", "--", "/bin/sh", "-c", wifi.KeyfileScript, "sh", "/etc/passwd"),
		cmd("systemd-run", "--unit=evil", "--", "podman", "version"),
		cmd("systemd-run", "--property=ExecStartPre=/bin/id", "--", "podman", "pull", "--quiet", "x"),
		cmd("systemd-run", "-p", "User=0", "--", "podman", "pull", "--quiet", "x"),
		cmd("systemd-run", "--setenv=LD_PRELOAD=/tmp/x.so", "--", "podman", "pull", "--quiet", "x"),
		cmd("systemd-run", "--scope", "--", "podman", "pull", "--quiet", "x"),
		cmd("systemd-run", "--wait", "--", "apt-get", "install", "evil"),
		cmd("systemd-run", "--wait", "--", "apt-get", "-o", "DPkg::Lock::Timeout=600", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold", "-y", "install", "-o", "APT::Update::Pre-Invoke=id"),
		cmd("systemd-run", "--wait", "--", "apt-mark", "-o", "DPkg::Lock::Timeout=600", "hold", "x=1"),
		cmd("systemd-run", "--wait", "--"),
		cmd("restic", "-o", "sftp.command=ssh -oProxyCommand=id", "init"),
		cmd("restic", "-o", "sftp.args=-F /home/x/cfg -i /run/care-pi-backup/s/k", "init"),
		cmd("restic", "restore", "abc", "--target", "/etc"),
		cmd("restic", "restore", "abc", "--target", "/var/lib/care-pi/../../etc"),
		cmd("restic", "mount", "/mnt"),
		{Name: "restic", Args: []string{"snapshots"}, Env: []string{"RESTIC_PASSWORD_COMMAND=id"}},
		{Name: "systemctl", Args: []string{"daemon-reload"}, Env: []string{"SYSTEMD_EDITOR=id"}},
		cmd("nmcli", "connection", "delete", "Wired connection 1"),
		cmd("nmcli", "connection", "load", "/tmp/x.nmconnection"),
		cmd("nmcli", "general", "hostname", "x"),
		cmd("tailscale", "up", "--exit-node=100.64.0.1"),
		cmd("tailscale", "up", "--login-server=http://plain.example.org"),
		cmd("tailscale", "up", "--auth-key=file:/etc/shadow"),
		cmd("tailscale", "serve", "443"),
		cmd("tailscale", "serve", "reset"),
		cmd("tailscale", "serve", "status", "--json", "--bg"),
		cmd("tailscale", "serve", "--bg", "status", "--json"),
		cmd("tailscale", "ssh", "root@x"),
		cmd("hostnamectl", "set-hostname", "a;id"),
		cmd("hostnamectl", "set-hostname", "Care"),
		cmd("apt", "install", "x"),
		cmd("dpkg-query", "-W", "-f=${Package}", "x"),
		cmd("wipefs", "--all", "--force", "/dev/../etc/passwd"),
		cmd("mkfs.ext4", "-F", "-q", "-m", "0", "-L", "x", "/etc/passwd"),
		cmd("sfdisk", "/dev/sda"),
		cmd("ip", "link", "set", "eth0", "down"),
		cmd("iw", "dev", "wlan0", "del"),
		cmd("sftp", "-F", "/tmp/cfg", "-i", "/run/care-pi-backup/s/k", "-b", "-", "care-dest-x"),
		cmd("sftp", "-o", "ProxyCommand=id", "care-dest-x"),
		cmd(paths.Binary, "serve"),
		cmd(paths.Binary, "backup", "destination", "probe", "/srv/../etc"),
		cmd("/tmp/care-pi", "firewall", "rollback", "--reason", "timeout"),
		cmd("nft", "flush", "ruleset"),
		cmd("nft", "-f", "/tmp/x.nft"),
		cmd("nft", "-f", "/etc/care-pi/rendered/firewall/care-pi.nft", "-f", "/tmp/x.nft"),
		cmd("nft", "-f", "/etc/care-pi/rendered/firewall/../../../../tmp/x.nft"),
		cmd("ss", "-K", "dst", "1.2.3.4"),
		cmd("date", "-s", "@0"),
		cmd("reboot"),
		{Name: "systemctl", Args: []string{"reboot"}, Attach: true},
		cmd("systemctl", "start", "care.target\x00"),
	}
	for _, c := range bad {
		if err := ValidateIn(root, c); !errors.Is(err, ErrDenied) {
			t.Errorf("allowed: %q %q %v", c.Name, c.Args, c.Env)
		}
	}
}

// TestValidateFuzz: an extra argument at each position of an allowed command never panics, and a hostile one is
// refused, except after the container name of "podman exec" or the image of "podman run" (the command inside a Care
// container is not checked: the
// daemon owns the data of Care; the helper guards the host). A change of the program name is refused. The bypasses of
// the Phase 7 review (M2) are refused: the restic flags of other programs or of other targets, a volume through a
// symbolic link, an image outside the lock, a tee of any text, and the SSH files below /run/care-pi.
func TestValidateFuzz(t *testing.T) {
	root := testRoot(t)
	snap := "4f1e9a2c"
	base := []host.Cmd{
		cmd("systemctl", "restart", "care-api.service"),
		cmd("podman", "exec", "--user", "postgres", "care-db", "pg_isready", "--quiet"),
		restore.PodmanRun("img", "care-pi-restore", paths.PGData, true, "pgbackrest", "--stanza=main", "restore"),
		cmd("journalctl", "--unit", "care-api.service", "--lines", "10"),
		host.WriteAptConfCmd(true),
		cmd("restic", "-o", "sftp.args=-F /run/care-pi-backup/s-1/ssh_config -i /run/care-pi-backup/s-1/id_ed25519", "snapshots", "--json"),
		cmd("restic", backup.SnapshotArgs(backup.TagDB, "care")...),
		cmd("restic", backup.RestoreArgs(snap, paths.S3Data, paths.RestoreDir+"/x", nil, true)...),
		cmd("restic", backup.ForgetArgs(backup.TagFiles, backup.Retention{KeepDaily: 7})...),
		cmd("sftp", "-F", "/run/care-pi-backup/s-1/ssh_config", "-i", "/run/care-pi-backup/s-1/id_ed25519", "-b", "-", "care-dest-nas"),
	}
	inserts := []string{"--", "-", "", "; id", "$(id)", "`id`", "../", "/etc/shadow", "--privileged", "-p", "--setenv=X=1", "\n", "care.target",
		"--password-command=id", "--password-command", "-t", "/", "--target=/", "--repo=/tmp/r", "rclone.program=/tmp/x"}
	hostile := map[string]bool{"; id": true, "$(id)": true, "`id`": true, "../": true, "/etc/shadow": true, "--privileged": true,
		"--setenv=X=1": true, "\n": true, "--password-command=id": true, "--password-command": true, "-t": true, "/": true,
		"--target=/": true, "--repo=/tmp/r": true, "rclone.program=/tmp/x": true}
	for _, b := range base {
		if err := ValidateIn(root, b); err != nil {
			t.Errorf("base %s: %v", b.String(), err)
		}
		for i := 0; i <= len(b.Args); i++ {
			for _, ins := range inserts {
				args := append(append(append([]string{}, b.Args[:i]...), ins), b.Args[i:]...)
				err := ValidateIn(root, host.Cmd{Name: b.Name, Args: args})
				insideContainer := (b.Name == "podman" && b.Args[0] == "exec" && i > 3) || (slices.Index(b.Args, "img") >= 0 && i > slices.Index(b.Args, "img"))
				if hostile[ins] && !insideContainer && err == nil {
					t.Errorf("%s %q passed", b.Name, args)
				}
			}
		}
		if err := ValidateIn(root, host.Cmd{Name: b.Name + "x", Args: b.Args}); err == nil {
			t.Errorf("%s: another program passed", b.Name)
		}
		if !strings.HasPrefix(Describe(b), b.Name) {
			t.Errorf("describe %q", Describe(b))
		}
	}

	// M2 of the Phase 7 review. Symbolic links below the directories of the agent that point outside them.
	for link, target := range map[string]string{paths.DrillDir + "/evil": "/etc", paths.RestoreDir + "/evil": "/etc",
		paths.DataDir + "/up": filepath.Join(root, "etc")} {
		f := filepath.Join(root, link)
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, f); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "nothing"), filepath.Join(root, paths.DrillDir, "dangling")); err != nil {
		t.Fatal(err)
	}
	pgVol := []string{"podman", "run", "--rm", "--name", "care-drill", "--network", "none", "--user", "postgres", "--volume"}
	refused := []host.Cmd{
		// (a) restic: other flags after the subcommand, other options, other targets.
		cmd("restic", "snapshots", "--json", "--password-command", "id"),
		cmd("restic", "snapshots", "--password-command=id", "--json"),
		cmd("restic", "-o", "sftp.command=ssh -oProxyCommand=id x", "init"),
		cmd("restic", "-o", "rclone.program=/tmp/x", "init"),
		cmd("restic", "init", "-o", "sftp.command=id"),
		cmd("restic", "init", "--option", "rclone.program=/tmp/x"),
		cmd("restic", "-r", "/tmp/r", "init"),
		cmd("restic", "restore", snap+":/etc/care-pi", "--target", "/"),
		cmd("restic", "restore", snap+":/etc/care-pi", "-t", "/"),
		cmd("restic", "restore", snap+":/etc/care-pi", "--target=/"),
		cmd("restic", "restore", snap+":/etc/care-pi", "--target", paths.RestoreDir+"/x", "--target", "/"),
		cmd("restic", "restore", snap+":/etc/care-pi", "--target", paths.RestoreDir+"/evil/cron.d"),
		cmd("restic", "restore", snap+":/etc/care-pi", "--target", paths.CADir),
		cmd("restic", "restore", snap+":/etc/care-pi"),
		cmd("restic", "restore", "latest:/etc/care-pi", "--target", paths.RestoreDir+"/x"),
		cmd("restic", "backup", "--json", "/etc/shadow"),
		cmd("restic", "backup", "--json", paths.DataDir+"/up/shadow"),
		cmd("restic", "backup", "--json", "--files-from", "/tmp/list"),
		cmd("restic", "forget", "--tag", "db", "--prune", "--password-command", "id"),
		cmd("restic", "check", "--read-data-subset=5%", "--password-command", "id"),
		cmd("restic", "dump", snap, paths.ConfigFile, "--password-command", "id"),
		cmd("restic", "mount", "/mnt"),
		cmd("restic", "key", "add"),
		cmd("restic", "self-update"),
		// (b) podman run: a volume through a symbolic link, and an image outside lock.yaml and config.yaml.
		cmd(pgVol[0], append(pgVol[1:], paths.DrillDir+"/evil:/var/lib/postgresql/data", pgImage)...),
		cmd(pgVol[0], append(pgVol[1:], paths.DrillDir+"/evil/shadow:/x", pgImage)...),
		cmd(pgVol[0], append(pgVol[1:], paths.DataDir+"/up:/x", pgImage)...),
		cmd(pgVol[0], append(pgVol[1:], paths.DrillDir+"/dangling:/x", pgImage)...),
		cmd(pgVol[0], append(pgVol[1:], paths.DrillDir+"/x:/var/lib/postgresql/data", "docker.io/library/alpine:latest", "sh")...),
		restore.PodmanRun("docker.io/attacker/postgres:17", "care-drill", paths.DrillDir+"/x", true, "", "postgres"),
		// The modules of a Caddy image (m2): only this fixed command, without a volume or a network.
		cmd("podman", "run", "--rm", "--network", "none", "docker.io/x/caddy:2", "caddy", "run"),
		cmd("podman", "run", "--rm", "--network", "host", "docker.io/x/caddy:2", "caddy", "list-modules", "--skip-standard"),
		cmd("podman", "run", "--rm", "--network", "none", "--volume", "/:/host", "docker.io/x/caddy:2", "caddy", "list-modules", "--skip-standard"),
		cmd("podman", "run", "--rm", "--network", "none", "docker.io/x/caddy:2", "caddy", "list-modules", "--skip-standard", "--versions"),
		cmd("podman", "run", "--rm", "--network", "none", "--privileged", "caddy", "list-modules", "--skip-standard"),
		// (c) the apt configuration: only the typed request, on or off.
		cmd("systemd-run", "--wait", "--collect", "--pipe", "--quiet", "--unit=care-pi-apt-conf", "--", "tee", paths.AptConfCarePi),
		cmd("systemd-run", "--wait", "--unit=care-pi-apt-conf", "-p", "MemoryMax=512M", "--setenv=DEBIAN_FRONTEND=noninteractive", "--", "tee", paths.AptConfCarePi),
		cmd(host.WriteAptConf, "/etc/shadow"),
		cmd(host.WriteAptConf, "on", "off"),
		cmd(host.WriteAptConf),
		// (d) the SSH files of a run are not below /run/care-pi (care-caddy mounts it).
		cmd("sftp", "-F", "/run/care-pi/backup/s-1/ssh_config", "-i", "/run/care-pi-backup/s-1/id_ed25519", "-b", "-", "care-dest-nas"),
		cmd("sftp", "-F", "/run/care-pi-backup/s-1/ssh_config", "-i", "/run/care-pi/backup/s-1/id_ed25519", "-b", "-", "care-dest-nas"),
		cmd("restic", "-o", "sftp.args=-F /run/care-pi/backup/s-1/ssh_config -i /run/care-pi-backup/s-1/id_ed25519", "init"),
		cmd("restic", "-o", "sftp.args=-F /run/care-pi/api.sock -i /run/care-pi-backup/s-1/id_ed25519", "init"),
	}
	for _, c := range refused {
		if err := ValidateIn(root, c); !errors.Is(err, ErrDenied) {
			t.Errorf("allowed: %s", c.String())
		}
	}
	// The same drill with the volume of a real directory passes.
	if err := os.MkdirAll(filepath.Join(root, paths.DrillDir, "ok"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := ValidateIn(root, cmd(pgVol[0], append(pgVol[1:], paths.DrillDir+"/ok:/var/lib/postgresql/data", pgImage, "postgres")...)); err != nil {
		t.Fatal(err)
	}
}
