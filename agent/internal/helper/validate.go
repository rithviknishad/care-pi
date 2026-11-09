// Package helper is the privileged helper of the agent (Phase 7, plans/04-security.md section 2, docs/console-server.md
// section 14): "care-pi helper" runs as root on the unix socket /run/care-pi-helper/helper.sock (0660 root:care-pi,
// care-pi-helper.socket). It runs only the commands of an allow-list, each argument checked against the shapes that
// the agent uses (validate.go), never through a shell of its own, and it logs each request. "care-pi serve --helper"
// sends each host command through it (host.HelperClient).
package helper

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/backup"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/disks"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/tailscale"
	"github.com/rithviknishad/care-pi/agent/internal/wifi"
)

// ErrDenied is the error of a command outside the allow-list.
var ErrDenied = errors.New("not allowed")

func deny(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrDenied, fmt.Sprintf(format, a...))
}

var (
	unitRE      = regexp.MustCompile(`^[A-Za-z0-9@._:\\-]{1,200}\.(service|timer|target|socket|mount|automount|path)$`)
	secretRE    = regexp.MustCompile(`^care[a-z0-9-]{0,60}$`)
	containerRE = regexp.MustCompile(`^care-[a-z0-9-]{1,60}$`)
	imageRE     = regexp.MustCompile(`^[a-z0-9][a-z0-9._/:@-]{0,250}$`)
	userRE      = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	envNameRE   = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)
	numberRE    = regexp.MustCompile(`^[0-9]{1,10}$`)
	hostnameRE  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	deviceRE    = regexp.MustCompile(`^/dev/(sd[a-z]{1,2}[0-9]{0,3}|nvme[0-9]{1,2}n[0-9]{1,2}(p[0-9]{1,3})?|mmcblk[0-9]{1,2}(p[0-9]{1,3})?)$`)
	labelRE     = regexp.MustCompile(`^[A-Za-z0-9_-]{1,16}$`)
	countryRE   = regexp.MustCompile(`^[A-Z]{2}$`)
	pkgVerRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]{0,62}(=[A-Za-z0-9.+:~-]{1,80})?$`)
	propRE      = regexp.MustCompile(`^[A-Za-z]{1,40}$`)
	cursorRE    = regexp.MustCompile(`^[A-Za-z0-9=;_.-]{1,200}$`)
	sinceRE     = regexp.MustCompile(`^@[0-9]{1,12}$`)
	outputRE    = regexp.MustCompile(`^(json|cat|short-iso)$`)
	durationRE  = regexp.MustCompile(`^[0-9]{1,6}(ms|s|m|h)?([0-9]{1,6}(ms|s|m|h))*$`)
	descRE      = regexp.MustCompile(`^[A-Za-z0-9 ._:,()/-]{1,120}$`)
	seedImageRE = regexp.MustCompile(`^ghcr\.io/rithviknishad/care-pi/postgres-pgbackrest@sha256:[0-9a-f]{64}$`)
	niceRE      = regexp.MustCompile(`^-?[0-9]{1,2}$`)
	archRE      = regexp.MustCompile(`^(arm64|amd64)$`)
	regionRE    = regexp.MustCompile(`^s3\.region=[a-z0-9-]{1,40}$`)
	ifaceRE     = regexp.MustCompile(`^[a-z][a-z0-9]{0,14}$`)
	sftpAliasRE = regexp.MustCompile(`^care-dest-[a-z0-9-]{1,40}$`)
	ctlURLRE    = regexp.MustCompile(`^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?(/[^\s]*)?$`)
	transientRE = regexp.MustCompile(`^care-pi-[a-z0-9-]{1,40}$`)
)

// otherUnits are the units outside care* that the agent controls.
var otherUnits = map[string]bool{"avahi-daemon.service": true, "systemd-sysctl.service": true, "tailscaled.service": true}

// unitPatterns are the glob patterns of "systemctl list-units", "list-timers", and "show".
var unitPatterns = map[string]bool{"var-lib-care*pi-disks-*": true, "care-backup-*.timer": true, "care-backup-*": true,
	"care-*.service": true, "care-pi*.service": true}

func allowedUnit(u string) bool {
	if !unitRE.MatchString(u) {
		return false
	}
	return strings.HasPrefix(u, "care") || strings.HasPrefix(u, `var-lib-care\x2dpi-disks-`) || otherUnits[u]
}

// absUnder reports whether p is a clean absolute path below one of the directories.
func absUnder(p string, dirs ...string) bool {
	if !strings.HasPrefix(p, "/") || path.Clean(p) != p || strings.ContainsAny(p, "\x00\n") {
		return false
	}
	for _, d := range dirs {
		if strings.HasPrefix(p, d+"/") {
			return true
		}
	}
	return false
}

// envAllowed lists the environment variables that a command can get (no secret: the restic variables are paths).
var envAllowed = map[string]map[string]bool{
	"restic": {"RESTIC_REPOSITORY": true, "RESTIC_PASSWORD_FILE": true, "RESTIC_CACHE_DIR": true, "GOMAXPROCS": true,
		"RESTIC_PROGRESS_FPS": true, "AWS_SHARED_CREDENTIALS_FILE": true, "AWS_PROFILE": true},
	"podman": {"DJANGO_SUPERUSER_PASSWORD": true},
}

// Validate checks a command against the allow-list. It returns nil or an error that wraps ErrDenied.
func Validate(c host.Cmd) error { return ValidateIn("", c) }

// ValidateIn is Validate with the paths of the agent below root (Server.Root of a test; "" on a host). The arguments
// are host paths; the rules read the symbolic links, lock.yaml, and config.yaml below root.
func ValidateIn(root string, c host.Cmd) error {
	if c.Attach {
		return deny("a terminal command")
	}
	for _, a := range append([]string{c.Name}, c.Args...) {
		if strings.ContainsRune(a, 0) {
			return deny("a NUL byte in an argument")
		}
	}
	for _, kv := range c.Env {
		k, _, ok := strings.Cut(kv, "=")
		if !ok || !envAllowed[c.Name][k] {
			return deny("the environment variable %q for %s", k, c.Name)
		}
	}
	rule, ok := rules[c.Name]
	if !ok {
		return deny("the program %q", c.Name)
	}
	return rule(view{root: root}, c.Args)
}

// view is what a rule reads from the host (M2 of the Phase 7 review): the symbolic links of a path, and the images of
// lock.yaml and config.yaml. root is the root of the paths of the agent: "" on a host.
type view struct{ root string }

func (v view) abs(p string) string {
	if v.root == "" {
		return p
	}
	return filepath.Join(v.root, p)
}

// realPath resolves the symbolic links of p. A part that does not exist yet stays as it is (it cannot be a link); a
// link to nothing is an error.
func realPath(p string) (string, error) {
	q, err := filepath.EvalSymlinks(p)
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return q, err
	}
	if fi, lerr := os.Lstat(p); lerr == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return "", fmt.Errorf("%s is a symbolic link to nothing", p)
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p, nil
	}
	r, err := realPath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(r, filepath.Base(p)), nil
}

// under reports whether the host path p is a clean absolute path below one of the directories, also after the
// resolution of its symbolic links: a link below an allowed directory that points outside it does not pass.
func (v view) under(p string, dirs ...string) bool {
	if !absUnder(p, dirs...) {
		return false
	}
	real, err := realPath(v.abs(p))
	if err != nil {
		return false
	}
	for _, d := range dirs {
		if rd, err := realPath(v.abs(d)); err == nil && strings.HasPrefix(real, rd+"/") {
			return true
		}
	}
	return false
}

// images returns the images that "podman run" with a volume of the host may start: each image of lock.yaml (the
// reference and the reference with the digest) and each images.* of config.yaml.
func (v view) images() map[string]bool {
	out := map[string]bool{}
	if lk, err := lock.Read(v.abs(paths.LockFile)); err == nil && lk != nil {
		for _, img := range lk.Images {
			out[img.Pull()], out[img.Ref] = true, true
		}
	}
	var cfg struct {
		Images config.Images `yaml:"images"`
	}
	if data, err := os.ReadFile(v.abs(paths.ConfigFile)); err == nil && yaml.Unmarshal(data, &cfg) == nil {
		im := cfg.Images
		for _, ref := range []string{im.Care, im.Postgres, im.Caddy, im.Redis, im.S3, im.Node} {
			if ref != "" {
				out[ref] = true
			}
		}
	}
	delete(out, "")
	return out
}

type rule func(v view, args []string) error

// pure is a rule that reads nothing from the host.
func pure(f func(args []string) error) rule {
	return func(_ view, args []string) error { return f(args) }
}

var rules map[string]rule

func init() {
	rules = map[string]rule{
		"systemctl":       pure(systemctl),
		"podman":          podman,
		"journalctl":      pure(journalctl),
		"systemd-run":     systemdRun,
		"skopeo":          pure(skopeo),
		"timedatectl":     pure(oneOf([]string{"show", "--property", "NTPSynchronized", "--value"})),
		"restic":          restic,
		"nmcli":           pure(nmcli),
		"tailscale":       pure(tailscaleRule),
		"hostnamectl":     pure(hostnamectl),
		"apt":             pure(oneOf([]string{"list", "--upgradable"})),
		"apt-cache":       pure(aptCache),
		"dpkg-query":      pure(dpkgQuery),
		"lsblk":           pure(oneOf(disks.LsblkArgs)),
		"wipefs":          pure(wipefs),
		"sfdisk":          pure(sfdisk),
		"partprobe":       pure(oneDevice),
		"mkfs.ext4":       pure(mkfs),
		"udevadm":         pure(oneOf([]string{"settle", "--timeout=30"})),
		"ip":              pure(oneOf([]string{"-json", "route", "show", "default"}, []string{"-json", "address", "show"})),
		"iw":              pure(iw),
		"sh":              pure(oneOf([]string{"-c", "command -v raspi-config"})),
		"sftp":            pure(sftp),
		paths.Binary:      pure(carePiSelf),
		"nft":             pure(oneOf([]string{"list", "ruleset"}, []string{"chain inet care-pi input { policy accept ; }"}, firewallLoadPending)),
		"ss":              pure(oneOf([]string{"-ltnpH"}, []string{"-ltnupH"})),
		host.WriteAptConf: pure(oneOf([]string{"on"}, []string{"off"})),
	}
}

// firewallLoadPending is firewall.LoadPendingCmd: the drop table of a trial (docs/console-server.md, section 13).
var firewallLoadPending = []string{"-f", paths.FirewallPendingFile}

func oneOf(wants ...[]string) func(args []string) error {
	return func(args []string) error {
		for _, w := range wants {
			if strings.Join(args, "\x00") == strings.Join(w, "\x00") {
				return nil
			}
		}
		return deny("the arguments %q", args)
	}
}

func systemctl(args []string) error {
	if len(args) == 0 {
		return deny("systemctl without a verb")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "daemon-reload", "reboot", "poweroff":
		if len(rest) != 0 {
			return deny("systemctl %s with arguments", verb)
		}
		return nil
	case "list-units":
		return flagsThenPatterns(rest, map[string]bool{"--all": true, "--plain": true, "--no-legend": true, "--no-pager": true})
	case "list-timers":
		return flagsThenPatterns(rest, map[string]bool{"--all": true, "--output=json": true})
	case "show":
		units := 0
		for i := 0; i < len(rest); i++ {
			a := rest[i]
			switch {
			case a == "-p" || a == "--property":
				if i+1 >= len(rest) || !propRE.MatchString(rest[i+1]) {
					return deny("systemctl show: a bad property")
				}
				i++
			case allowedUnit(a) || unitPatterns[a]:
				units++
			default:
				return deny("systemctl show %q", a)
			}
		}
		if units == 0 {
			return deny("systemctl show without a unit")
		}
		return nil
	case "enable", "disable", "start", "stop", "restart", "try-restart", "try-reload-or-restart", "reset-failed":
		flags := map[string]bool{}
		switch verb {
		case "enable", "disable":
			flags["--now"] = true
		case "start":
			flags["--no-block"] = true
		}
		units := 0
		for _, a := range rest {
			switch {
			case flags[a]:
			case allowedUnit(a):
				units++
			default:
				return deny("systemctl %s %q", verb, a)
			}
		}
		if units == 0 {
			return deny("systemctl %s without a unit", verb)
		}
		return nil
	}
	return deny("systemctl %s", verb)
}

func flagsThenPatterns(args []string, flags map[string]bool) error {
	n := 0
	for _, a := range args {
		switch {
		case flags[a]:
		case unitPatterns[a] || allowedUnit(a):
			n++
		default:
			return deny("the argument %q", a)
		}
	}
	if n == 0 {
		return deny("no unit pattern")
	}
	return nil
}

func podman(v view, args []string) error {
	if len(args) == 0 {
		return deny("podman without a command")
	}
	j := strings.Join(args, " ")
	switch args[0] {
	case "version":
		return oneOf([]string{"version", "--format", "{{.Client.Version}}"})(args)
	case "secret":
		switch {
		case j == "secret ls --format {{.Name}}":
			return nil
		case len(args) == 5 && args[1] == "create" && args[2] == "--replace" && secretRE.MatchString(args[3]) && args[4] == "-":
			return nil
		case len(args) == 4 && args[1] == "rm" && args[2] == "--ignore" && secretRE.MatchString(args[3]):
			return nil
		}
	case "image":
		switch {
		case len(args) == 3 && (args[1] == "exists" || args[1] == "rm") && imageRE.MatchString(args[2]):
			return nil
		case j == "image prune -f":
			return nil
		}
	case "pull":
		if len(args) == 3 && args[1] == "--quiet" && imageRE.MatchString(args[2]) {
			return nil
		}
	case "ps":
		if len(args) == 6 && args[1] == "--all" && args[2] == "--filter" && args[3] == "pod=care" && args[4] == "--format" ||
			len(args) == 4 && args[1] == "--all" && args[2] == "--format" {
			return nil
		}
	case "stop":
		if j == "stop --ignore --time 30 care-drill" {
			return nil
		}
	case "rm":
		// The containers of the restore drill only (restore.removeDrill).
		if j == "rm --force --ignore care-drill" || j == "rm --force --ignore care-drill-restore" {
			return nil
		}
	case "exec":
		return podmanExec(args[1:])
	case "run":
		return podmanRun(v, args[1:])
	}
	return deny("podman %q", args)
}

// podmanExec allows "exec [--user U] [-i|--interactive] [--tty] [--env NAME]... care-<name> COMMAND...". The command
// runs in a container of Care; --privileged, --workdir, and the other options are refused.
func podmanExec(args []string) error {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-i" || a == "--interactive" || a == "--tty":
		case a == "--user" || a == "--env":
			if i+1 >= len(args) {
				return deny("podman exec %s without a value", a)
			}
			v := args[i+1]
			if (a == "--user" && !userRE.MatchString(v)) || (a == "--env" && !envNameRE.MatchString(v)) {
				return deny("podman exec %s %q", a, v)
			}
			i++
		case containerRE.MatchString(a):
			if i+1 >= len(args) {
				return deny("podman exec without a command")
			}
			return nil
		default:
			return deny("podman exec %q", a)
		}
	}
	return deny("podman exec without a container")
}

// podmanRun allows the "podman run --rm" of a restore or a drill (restore.PodmanRun): the image is an image of
// lock.yaml or config.yaml, and each volume source is below /var/lib/care-pi or /etc/care-pi/rendered, also after
// the resolution of its symbolic links.
func podmanRun(v view, args []string) error {
	// "caddy list-modules" of the Caddy image of a target (apply.WAFModulesCmd): no volume, no secret, no network, and
	// a fixed command, so the image can be one that lock.yaml does not have yet (an update or a rollback).
	if len(args) == 7 && strings.Join(args[:3], " ") == "--rm --network none" && imageRE.MatchString(args[3]) &&
		strings.Join(args[4:], " ") == "caddy list-modules --skip-standard" {
		return nil
	}
	// The password change of the database seed (S72, apply.SeedPasswordCmd): the first full apply runs it before
	// lock.yaml has the postgres image, so the image is the postgres-pgbackrest image of care-pi by digest (or
	// images.postgres of config.yaml); the form and the script are fixed.
	want := apply.SeedPasswordCmd("IMAGE").Args
	if inner := want[slices.Index(want, "run")+1:]; len(args) == len(inner) {
		img := args[len(args)-4]
		same := true
		for i := range inner {
			if inner[i] != "IMAGE" && inner[i] != args[i] {
				same = false
				break
			}
		}
		if same && (seedImageRE.MatchString(img) || v.images()[img]) {
			return nil
		}
	}
	var sources []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		val := ""
		if a != "--rm" && strings.HasPrefix(a, "--") {
			if i+1 >= len(args) {
				return deny("podman run %s without a value", a)
			}
			i++
			val = args[i]
		}
		switch a {
		case "--rm":
		case "--name":
			if !containerRE.MatchString(val) {
				return deny("podman run --name %q", val)
			}
		case "--network":
			if val != "none" {
				return deny("podman run --network %q", val)
			}
		case "--user":
			if val != "postgres" {
				return deny("podman run --user %q", val)
			}
		case "--entrypoint":
			if val != "pgbackrest" {
				return deny("podman run --entrypoint %q", val)
			}
		case "--volume":
			src, rest, _ := strings.Cut(val, ":")
			if !absUnder(src, paths.DataDir, paths.RenderedDir) || strings.Contains(rest, ",") {
				return deny("podman run --volume %q", val)
			}
			sources = append(sources, src)
		case "--secret":
			name, _, _ := strings.Cut(val, ",")
			if !secretRE.MatchString(name) {
				return deny("podman run --secret %q", val)
			}
		default:
			if strings.HasPrefix(a, "-") || !imageRE.MatchString(a) {
				return deny("podman run %q", a)
			}
			for _, src := range sources {
				if !v.under(src, paths.DataDir, paths.RenderedDir) {
					return deny("podman run --volume %q: a symbolic link outside the directories of the agent", src)
				}
			}
			if !v.images()[a] {
				return deny("podman run of the image %q: not an image of lock.yaml or config.yaml", a)
			}
			return nil
		}
	}
	return deny("podman run without an image")
}

func journalctl(args []string) error {
	unit := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		var re *regexp.Regexp
		switch a {
		case "--no-pager", "--follow":
			continue
		case "--unit":
			if i+1 >= len(args) || (!allowedUnit(args[i+1]) && !unitPatterns[args[i+1]]) {
				return deny("journalctl --unit: a unit outside care-pi")
			}
			unit = true
			i++
			continue
		case "--lines":
			re = numberRE
		case "--output":
			re = outputRE
		case "--after-cursor":
			re = cursorRE
		case "--since":
			re = sinceRE
		default:
			return deny("journalctl %q", a)
		}
		if i+1 >= len(args) || !re.MatchString(args[i+1]) {
			return deny("journalctl %s: a bad value", a)
		}
		i++
	}
	if !unit {
		return deny("journalctl without --unit")
	}
	return nil
}

// innerScripts are the scripts that systemd-run may start with "/bin/sh -c": only the constants of the agent, each
// with a check of its arguments.
var innerScripts = map[string]func(args []string) bool{
	host.SetClockScript:     func(a []string) bool { return len(a) == 1 && numberRE.MatchString(a[0]) },
	wifi.KeyfileScript:      func(a []string) bool { return len(a) == 1 && a[0] == wifi.KeyfilePath },
	apply.SysctlScript:      func(a []string) bool { return len(a) == 1 && a[0] == paths.SysctlFile },
	tailscale.InstallScript: func(a []string) bool { return len(a) == 0 },
}

var setenvAllowed = map[string]bool{"DEBIAN_FRONTEND": true, "RESTIC_REPOSITORY": true, "RESTIC_PASSWORD_FILE": true,
	"RESTIC_CACHE_DIR": true, "GOMAXPROCS": true, "RESTIC_PROGRESS_FPS": true, "AWS_SHARED_CREDENTIALS_FILE": true, "AWS_PROFILE": true}

var propAllowed = map[string]*regexp.Regexp{
	"MemoryMax":         regexp.MustCompile(`^[0-9]{1,5}[MG]$`),
	"MemorySwapMax":     regexp.MustCompile(`^[0-9]{1,5}[MG]$`),
	"IOSchedulingClass": regexp.MustCompile(`^idle$`),
}

func systemdRun(v view, args []string) error {
	i := 0
	for ; i < len(args) && args[i] != "--"; i++ {
		a := args[i]
		k, v, hasV := strings.Cut(a, "=")
		switch {
		case a == "--quiet" || a == "--wait" || a == "--pipe" || a == "--collect":
		case k == "--unit" && hasV && (transientRE.MatchString(v) || v == "care-backup-drill-pg.service"):
		case k == "--description" && hasV && descRE.MatchString(v):
		case k == "--nice" && hasV && niceRE.MatchString(v):
		case k == "--on-active" && hasV && durationRE.MatchString(v):
		case a == "--timer-property=AccuracySec=1s":
		case k == "--setenv" && hasV:
			name, _, _ := strings.Cut(v, "=")
			if !setenvAllowed[name] {
				return deny("systemd-run --setenv %s", name)
			}
		case k == "--property" && hasV:
			if err := runProperty(v); err != nil {
				return err
			}
		case a == "-p":
			if i+1 >= len(args) {
				return deny("systemd-run -p without a value")
			}
			if err := runProperty(args[i+1]); err != nil {
				return err
			}
			i++
		default:
			return deny("systemd-run %q", a)
		}
	}
	if i >= len(args)-1 {
		return deny("systemd-run without a command after --")
	}
	inner := args[i+1:]
	name, rest := inner[0], inner[1:]
	switch name {
	case "/bin/sh":
		if len(rest) >= 2 && rest[0] == "-c" {
			if check := innerScripts[rest[1]]; check != nil {
				params := rest[2:]
				if len(params) > 0 && params[0] == "sh" {
					params = params[1:]
				}
				if check(params) {
					return nil
				}
			}
		}
		return deny("systemd-run: a shell script that is not a script of the agent")
	case "raspi-config":
		if len(rest) == 3 && rest[0] == "nonint" && rest[1] == "do_wifi_country" && countryRE.MatchString(rest[2]) {
			return nil
		}
	case "apt-get":
		return aptGet(rest)
	case "apt-mark":
		return aptMark(rest)
	case "podman", "restic", "tailscale", "wipefs", "sfdisk", "partprobe", "mkfs.ext4", paths.Binary:
		return rules[name](v, rest)
	}
	return deny("systemd-run -- %q", inner)
}

func runProperty(v string) error {
	k, val, _ := strings.Cut(v, "=")
	if re := propAllowed[k]; re == nil || !re.MatchString(val) {
		return deny("systemd-run property %q", v)
	}
	return nil
}

var aptOptions = []string{"-o", "DPkg::Lock::Timeout=600", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold"}

func aptGet(args []string) error {
	if len(args) < len(aptOptions) || strings.Join(args[:len(aptOptions)], "\x00") != strings.Join(aptOptions, "\x00") {
		return deny("apt-get without the options of the agent")
	}
	rest := args[len(aptOptions):]
	switch {
	case len(rest) == 1 && rest[0] == "update":
		return nil
	case len(rest) == 2 && rest[0] == "-y" && rest[1] == "full-upgrade":
		return nil
	case len(rest) >= 3 && rest[0] == "-y" && rest[1] == "install":
		for _, p := range rest[2:] {
			if !pkgVerRE.MatchString(p) {
				return deny("apt-get install %q", p)
			}
		}
		return nil
	}
	return deny("apt-get %q", rest)
}

func aptMark(args []string) error {
	if len(args) < 4 || args[0] != "-o" || args[1] != "DPkg::Lock::Timeout=600" || (args[2] != "hold" && args[2] != "unhold") {
		return deny("apt-mark %q", args)
	}
	for _, p := range args[3:] {
		if !pkgVerRE.MatchString(p) || strings.Contains(p, "=") {
			return deny("apt-mark %q", p)
		}
	}
	return nil
}

func skopeo(args []string) error {
	if len(args) == 8 && args[0] == "inspect" && args[1] == "--override-os" && args[2] == "linux" && args[3] == "--override-arch" &&
		archRE.MatchString(args[4]) && args[5] == "--format" && args[6] == "{{.Digest}}" &&
		strings.HasPrefix(args[7], "docker://") && imageRE.MatchString(strings.TrimPrefix(args[7], "docker://")) {
		return nil
	}
	return deny("skopeo %q", args)
}

var (
	sftpArgsRE   = regexp.MustCompile(`^sftp\.args=-F (\S+) -i (\S+)$`)
	snapIDRE     = regexp.MustCompile(`^[0-9a-f]{8,64}$`)
	tagRE        = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
	keepWithinRE = regexp.MustCompile(`^[0-9]{1,5}[hdwmy]$`)
	readSubsetRE = regexp.MustCompile(`^--read-data-subset=[0-9]{1,3}%$`)
	snapPathRE   = regexp.MustCompile(`^/[^\x00\n]{0,1000}$`)
)

// resticExcludes are the --exclude values of a snapshot (backup.Excludes).
var resticExcludes = func() map[string]bool {
	m := map[string]bool{}
	for _, x := range backup.Excludes {
		m[x] = true
	}
	return m
}()

// restic allows the options of a care-pi repository, then one subcommand of the agent with only the flags that the
// agent gives it (backup/restic.go, backup/ops.go, restore/*.go). So --password-command, -o sftp.command,
// -o rclone.program, --repo, and a --target or -t outside the directories of a restore do not pass. The SSH options
// of an SFTP repository must name the files of the agent (no ProxyCommand option).
func restic(v view, args []string) error {
	i := 0
	for ; i+1 < len(args) && args[i] == "-o"; i += 2 {
		o := args[i+1]
		switch {
		case o == "s3.bucket-lookup=path":
		case regionRE.MatchString(o):
		case sftpArgsRE.MatchString(o):
			m := sftpArgsRE.FindStringSubmatch(o)
			cfg, key := m[1], m[2]
			if (cfg != paths.RenderedBackup+"/ssh_config" && !absUnder(cfg, paths.RunBackupDir)) || !absUnder(key, paths.RunBackupDir) {
				return deny("restic sftp.args %q", o)
			}
		default:
			return deny("restic -o %q", o)
		}
	}
	if i >= len(args) {
		return deny("restic without a subcommand")
	}
	sub, rest := args[i], args[i+1:]
	ok := false
	switch sub {
	case "init", "unlock", "prune":
		ok = len(rest) == 0
	case "cat":
		ok = len(rest) == 1 && rest[0] == "config"
	case "snapshots":
		ok = len(rest) >= 1 && rest[0] == "--json" && (len(rest) == 1 || len(rest) == 2 && snapIDRE.MatchString(rest[1]))
	case "stats":
		ok = strings.Join(rest, " ") == "--mode raw-data --json"
	case "check":
		ok = len(rest) == 1 && readSubsetRE.MatchString(rest[0])
	case "dump":
		ok = len(rest) == 2 && snapIDRE.MatchString(rest[0]) && snapPathRE.MatchString(rest[1])
	case "forget":
		if len(rest) == 2 && rest[0] == "--keep-tag" && rest[1] == "care-pi-keep-nothing" {
			return nil
		}
		return resticFlags(sub, rest, map[string]func(string) bool{
			"--tag": tagRE.MatchString, "--group-by": func(x string) bool { return x == "host,tags" },
			"--keep-last": numberRE.MatchString, "--keep-within": keepWithinRE.MatchString, "--keep-daily": numberRE.MatchString,
			"--keep-weekly": numberRE.MatchString, "--keep-monthly": numberRE.MatchString,
		}, nil, nil)
	case "backup":
		// The paths of a snapshot: /etc/care-pi and the directories below /var/lib/care-pi (backup.SnapshotPaths, the CAs).
		return resticFlags(sub, rest, map[string]func(string) bool{
			"--host": hostnameRE.MatchString, "--tag": tagRE.MatchString, "--exclude": func(x string) bool { return resticExcludes[x] },
		}, map[string]bool{"--json": true}, func(p string) bool { return p == paths.EtcDir || v.under(p, paths.DataDir) })
	case "restore":
		// "restore SNAPSHOT:/dir --target DIR [--include /path]... [--verify]" (backup.RestoreArgs). The target is a
		// directory below /var/lib/care-pi/restore, or /var/lib/care-pi/backup/pg or /var/lib/care-pi/s3 in place.
		if len(rest) == 0 {
			return deny("restic restore without a snapshot")
		}
		id, dir, found := strings.Cut(rest[0], ":")
		if !found || !snapIDRE.MatchString(id) || !strings.HasPrefix(dir, "/") || path.Clean(dir) != dir {
			return deny("restic restore %q", rest[0])
		}
		targets := 0
		err := resticFlags(sub, rest[1:], map[string]func(string) bool{
			"--target": func(t string) bool {
				targets++
				return t == paths.BackupPG || t == paths.S3Data || v.under(t, paths.RestoreDir)
			},
			"--include": snapPathRE.MatchString,
		}, map[string]bool{"--verify": true}, nil)
		if err == nil && targets != 1 {
			return deny("restic restore without one --target below %s", paths.RestoreDir)
		}
		return err
	}
	if !ok {
		return deny("restic %q", args)
	}
	return nil
}

// resticFlags checks the arguments of a restic subcommand: each flag of values takes the next argument and checks it;
// each flag of bare has no value; pos checks each other argument (nil: none).
func resticFlags(sub string, args []string, values map[string]func(string) bool, bare map[string]bool, pos func(string) bool) error {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch check := values[a]; {
		case check != nil:
			if i+1 >= len(args) || !check(args[i+1]) {
				return deny("restic %s %s: a bad value", sub, a)
			}
			i++
		case bare[a]:
		case pos != nil && !strings.HasPrefix(a, "-") && pos(a):
		default:
			return deny("restic %s %q", sub, a)
		}
	}
	return nil
}

func nmcli(args []string) error {
	j := strings.Join(args, " ")
	switch j {
	case "-t -f DEVICE,TYPE device status", "radio wifi on", "radio wifi",
		"-t -f IN-USE,SSID,SIGNAL,SECURITY,CHAN device wifi list --rescan yes", "-t -f IN-USE,SSID,SIGNAL device wifi list --rescan no",
		"connection load " + wifi.KeyfilePath, "--wait 45 connection up " + wifi.ConnName, "connection delete " + wifi.ConnName,
		"-t -f NAME,TYPE,AUTOCONNECT,ACTIVE connection show":
		return nil
	}
	if len(args) == 6 && strings.Join(args[:5], " ") == "-t -g 802-11-wireless.ssid connection show" && !strings.HasPrefix(args[5], "-") {
		return nil
	}
	if len(args) == 6 && strings.Join(args[:5], " ") == "-t -f GENERAL.STATE,GENERAL.CONNECTION,IP4.ADDRESS device show" &&
		ifaceRE.MatchString(args[5]) {
		return nil
	}
	return deny("nmcli %q", args)
}

func tailscaleRule(args []string) error {
	if len(args) == 0 {
		return deny("tailscale without a command")
	}
	switch strings.Join(args, " ") {
	case "version", "status --json", "serve status --json", "down", "logout":
		return nil
	}
	if args[0] != "up" {
		return deny("tailscale %q", args)
	}
	for _, a := range args[1:] {
		k, v, _ := strings.Cut(a, "=")
		switch {
		case a == "--accept-dns=false" || a == "--accept-routes=false" || a == "--reset" || a == "--force-reauth":
		case k == "--hostname" && hostnameRE.MatchString(v):
		case k == "--login-server" && ctlURLRE.MatchString(v):
		case k == "--auth-key" && v == "file:"+paths.RunDir+"/tailscale-auth-key":
		case k == "--timeout" && durationRE.MatchString(v):
		default:
			return deny("tailscale up %q", a)
		}
	}
	return nil
}

func hostnamectl(args []string) error {
	if len(args) == 2 && args[0] == "set-hostname" && hostnameRE.MatchString(args[1]) {
		return nil
	}
	return deny("hostnamectl %q", args)
}

func packages(args []string, what string) error {
	for _, p := range args {
		if !pkgVerRE.MatchString(p) || strings.Contains(p, "=") {
			return deny("%s %q", what, p)
		}
	}
	return nil
}

func aptCache(args []string) error {
	if len(args) >= 2 && args[0] == "policy" {
		return packages(args[1:], "apt-cache policy")
	}
	return deny("apt-cache %q", args)
}

func dpkgQuery(args []string) error {
	if len(args) >= 2 && args[0] == "-W" && args[1] == "-f=${Package}\t${Version}\n" {
		return packages(args[2:], "dpkg-query")
	}
	return deny("dpkg-query %q", args)
}

func oneDevice(args []string) error {
	if len(args) == 1 && deviceRE.MatchString(args[0]) {
		return nil
	}
	return deny("the device %q", args)
}

func wipefs(args []string) error {
	if len(args) == 3 && args[0] == "--all" && args[1] == "--force" && deviceRE.MatchString(args[2]) {
		return nil
	}
	return deny("wipefs %q", args)
}

func sfdisk(args []string) error {
	if len(args) == 7 && strings.Join(args[:6], " ") == "--wipe always --wipe-partitions always --label gpt" && deviceRE.MatchString(args[6]) {
		return nil
	}
	return deny("sfdisk %q", args)
}

func mkfs(args []string) error {
	if len(args) == 7 && strings.Join(args[:4], " ") == "-F -q -m 0" && args[4] == "-L" && labelRE.MatchString(args[5]) && deviceRE.MatchString(args[6]) {
		return nil
	}
	return deny("mkfs.ext4 %q", args)
}

func iw(args []string) error {
	if len(args) == 3 && args[0] == "reg" && args[1] == "set" && countryRE.MatchString(args[2]) {
		return nil
	}
	return deny("iw %q", args)
}

func sftp(args []string) error {
	if len(args) == 7 && args[0] == "-F" && args[2] == "-i" && args[4] == "-b" && args[5] == "-" && sftpAliasRE.MatchString(args[6]) &&
		(args[1] == paths.RenderedBackup+"/ssh_config" || absUnder(args[1], paths.RunBackupDir)) && absUnder(args[3], paths.RunBackupDir) {
		return nil
	}
	return deny("sftp %q", args)
}

func carePiSelf(args []string) error {
	switch {
	case len(args) == 4 && args[0] == "backup" && args[1] == "destination" && args[2] == "probe" && strings.HasPrefix(args[3], "/") &&
		path.Clean(args[3]) == args[3]:
		return nil
	case len(args) == 4 && args[0] == "firewall" && args[1] == "rollback" && args[2] == "--reason" && (args[3] == "timeout" || args[3] == "manual"):
		return nil
	}
	return deny("care-pi %q", args)
}

// Describe returns a short text of a command for the journal: the program, the first arguments, and the count.
// Secrets never are arguments (they go through stdin or a file), but a stdin is never logged.
func Describe(c host.Cmd) string {
	a := c.Args
	if len(a) > 8 {
		a = a[:8]
	}
	return strings.TrimSpace(c.Name+" "+strings.Join(a, " ")) + " (" + strconv.Itoa(len(c.Args)) + " args)"
}
