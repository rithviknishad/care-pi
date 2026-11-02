package render

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"text/template"

	"github.com/rithviknishad/care-pi/agent/internal/paths"
	carebuild "github.com/rithviknishad/care-pi/images/care"
	"github.com/rithviknishad/care-pi/quadlets"
)

// File is one rendered file.
type File struct {
	// Path is the host path, for example /etc/containers/systemd/care-api.container.
	Path    string
	Content []byte
	Mode    os.FileMode
	UID     int
	GID     int
}

// Tree maps host paths to files.
type Tree map[string]File

// Paths returns the host paths in order.
func (t Tree) Paths() []string {
	out := make([]string, 0, len(t))
	for p := range t {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

type output struct {
	tmpl string
	path string
	mode os.FileMode
	gid  int
	when func(*Model) bool
}

// dynOutput is an output whose path depends on the model (Phase 4).
type dynOutput struct {
	output
	pathOf func(*Model) string
}

func always(*Model) bool         { return true }
func full(m *Model) bool         { return !m.Bootstrap }
func feBuild(m *Model) bool      { return m.FEBuild }
func careBuild(m *Model) bool    { return m.CareBuild != nil && !m.Bootstrap }
func plugBuild(m *Model) bool    { return m.PlugBuild && !m.Bootstrap }
func quadlet(name string) string { return paths.QuadletDir + "/" + name }
func systemd(name string) string { return paths.SystemdDir + "/" + name }

var outputs = []output{
	{"care.pod.tmpl", quadlet("care.pod"), 0o644, 0, always},
	{"care-caddy.container.tmpl", quadlet("care-caddy.container"), 0o644, 0, always},
	{"care-db.container.tmpl", quadlet("care-db.container"), 0o644, 0, full},
	{"care-redis.container.tmpl", quadlet("care-redis.container"), 0o644, 0, full},
	{"care-s3.container.tmpl", quadlet("care-s3.container"), 0o644, 0, full},
	{"care-beat.container.tmpl", quadlet("care-beat.container"), 0o644, 0, full},
	{"care-worker.container.tmpl", quadlet("care-worker.container"), 0o644, 0, full},
	{"care-api.container.tmpl", quadlet("care-api.container"), 0o644, 0, full},
	{"care-fe-build.container.tmpl", quadlet("care-fe-build.container"), 0o644, 0, feBuild},
	{"care.target.tmpl", systemd("care.target"), 0o644, 0, always},
	{"care-clock.service.tmpl", systemd("care-clock.service"), 0o644, 0, always},
	{"care-start.service.tmpl", systemd(StartUnit), 0o644, 0, always},
	{"Caddyfile.tmpl", paths.RenderedCaddy + "/Caddyfile", 0o600, 0, always},
	{"status-setup.html.tmpl", paths.RenderedCaddy + "/status/setup.html", 0o644, 0, always},
	{"status-maintenance.html.tmpl", paths.RenderedCaddy + "/status/maintenance.html", 0o644, 0, always},
	{"status-clock.html.tmpl", paths.RenderedCaddy + "/status/clock.html", 0o644, 0, always},
	{"backend.env.tmpl", paths.RenderedCare + "/backend.env", 0o600, 0, full},
	{"pgbackrest.conf.tmpl", paths.RenderedDB + "/pgbackrest.conf", 0o640, paths.PostgresGID, full},
	{"frontend.env.tmpl", paths.RenderedFE + "/frontend.env", 0o600, 0, feBuild},
	{"fe-build.sh.tmpl", paths.RenderedFE + "/" + FEBuildScriptName, 0o700, 0, feBuild},
	// Phase 6 (docs/connectivity.md, section 4.2): a publish on an absent optional address does not stop the pod.
	{"sysctl.conf.tmpl", paths.SysctlFile, 0o644, 0, always},
	// Phase 7 (docs/console-server.md, section 13): the drop-in that loads the table of the agent. renderFirewall
	// writes the table.
	{"firewall.conf.tmpl", paths.FirewallDropIn, 0o644, 0, always},
	// The Tailscale mode (docs/connectivity.md, section 4.1): tailscaled keeps the directory of its socket.
	{"tailscaled.conf.tmpl", paths.TailscaledDropIn, 0o644, 0, tailscaleOn},
}

func tailscaleOn(m *Model) bool { return m.TailscaleHost != "" }

// TelemetryExporterPort is the port of care-node-exporter on 127.0.0.1 (docs/telemetry.md).
const TelemetryExporterPort = 19100

// phase4Outputs are the outputs of Phase 4 (docs/solutions.md).
var phase4Outputs = []dynOutput{
	{output{"additional-plugs.json.tmpl", paths.RenderedCare + "/additional-plugs.json", 0o600, 0, full}, nil},
	{output{"care-update-check.timer.tmpl", systemd("care-update-check.timer"), 0o644, 0, full}, nil},
	{output{"care-update-check.service.tmpl", systemd("care-update-check.service"), 0o644, 0, full}, nil},
	{output{"care-update-apply.timer.tmpl", systemd("care-update-apply.timer"), 0o644, 0, full}, nil},
	{output{"care-update-apply.service.tmpl", systemd("care-update-apply.service"), 0o644, 0, full}, nil},
	{output{"care-build.build.tmpl", "", 0o644, 0, careBuild}, func(m *Model) string { return quadlet("care-build-" + m.SolutionName + ".build") }},
	{output{"care-plug-build.container.tmpl", quadlet("care-plug-build.container"), 0o644, 0, plugBuild}, nil},
	{output{"plug-build.sh.tmpl", paths.RenderedPlugs + "/plug-build.sh", 0o700, 0, plugBuild}, nil},
	// Phase 6 (docs/connectivity.md).
	{output{"care-dns-update.timer.tmpl", systemd(DNSUpdateTimer), 0o644, 0, dnsUpdate}, nil},
	{output{"care-dns-update.service.tmpl", systemd("care-dns-update.service"), 0o644, 0, dnsUpdate}, nil},
	// Phase 7: the opt-in telemetry (docs/telemetry.md).
	{output{"care-telemetry.image.tmpl", quadlet("care-telemetry.image"), 0o644, 0, telemetry}, nil},
	{output{"care-node-exporter.image.tmpl", quadlet("care-node-exporter.image"), 0o644, 0, telemetry}, nil},
	{output{"care-node-exporter.container.tmpl", quadlet("care-node-exporter.container"), 0o644, 0, telemetry}, nil},
	{output{"care-telemetry.container.tmpl", quadlet("care-telemetry.container"), 0o644, 0, telemetry}, nil},
	{output{"telemetry-scrape.yml.tmpl", paths.RenderedTelemetry + "/scrape.yml", 0o644, 0, telemetry}, nil},
}

func dnsUpdate(m *Model) bool { return m.DNSUpdate && !m.Bootstrap }

// TimerUnits are the update timers that apply enables (docs/solutions.md, section 10.2).
var TimerUnits = []string{"care-update-check.timer", "care-update-apply.timer"}

var funcs = template.FuncMap{
	"join": strings.Join,
	"path": func(name string) string {
		p, ok := HostPaths[name]
		if !ok {
			panic("render: unknown path " + name)
		}
		return p
	},
	"prefix": func(pre string, items []string) []string {
		out := make([]string, len(items))
		for i, s := range items {
			out[i] = pre + s
		}
		return out
	},
	"suffix": func(suf string, items []string) []string {
		out := make([]string, len(items))
		for i, s := range items {
			out[i] = s + suf
		}
		return out
	},
	// sd escapes a value for an Exec line of systemd: % starts a specifier.
	"sd": func(s string) string { return strings.ReplaceAll(s, "%", "%%") },
	"short": func(s string) string {
		if i := strings.Index(s, "@sha256:"); i >= 0 && len(s) > i+20 {
			return s[:i+20]
		}
		return s
	},
}

func templates() (*template.Template, error) {
	t := template.New("").Funcs(funcs).Option("missingkey=error")
	err := fs.WalkDir(quadlets.FS, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".tmpl") {
			return err
		}
		data, err := fs.ReadFile(quadlets.FS, p)
		if err != nil {
			return err
		}
		_, err = t.New(path.Base(p)).Parse(string(data))
		return err
	})
	return t, err
}

// StartUnit starts care.target at boot without a wait. The agent enables it, not care.target.
const StartUnit = "care-start.service"

// FEBuildScriptName is the file name of the FE build script in rendered/fe.
const FEBuildScriptName = "fe-build.sh"

// FEBuildScript returns the FE build script. It does not depend on the config,
// so the fe-bundle workflow runs the same script ("care-pi fe script").
func FEBuildScript() ([]byte, error) {
	return renderOne("fe-build.sh.tmpl", &Model{Header: Header})
}

// FrontendEnvFile returns frontend.env for the variables, as apply renders it.
func FrontendEnvFile(vars map[string]string) ([]byte, error) {
	return renderOne("frontend.env.tmpl", &Model{Header: Header, FrontendVars: sortedVars(vars)})
}

func renderOne(name string, m *Model) ([]byte, error) {
	t, err := templates()
	if err != nil {
		return nil, fmt.Errorf("render: cannot parse the templates: %w", err)
	}
	var b bytes.Buffer
	if err := t.ExecuteTemplate(&b, name, m); err != nil {
		return nil, fmt.Errorf("render: %s: %w", name, err)
	}
	return tidy(b.Bytes()), nil
}

// Render executes every template for the model.
func Render(m *Model) (Tree, error) {
	t, err := templates()
	if err != nil {
		return nil, fmt.Errorf("render: cannot parse the templates: %w", err)
	}
	tree := Tree{}
	all := make([]dynOutput, 0, len(outputs)+len(phase4Outputs))
	for _, o := range outputs {
		all = append(all, dynOutput{output: o})
	}
	all = append(all, phase4Outputs...)
	for _, o := range all {
		if !o.when(m) {
			continue
		}
		var b bytes.Buffer
		if err := t.ExecuteTemplate(&b, o.tmpl, m); err != nil {
			return nil, fmt.Errorf("render: %s: %w", o.tmpl, err)
		}
		p := o.path
		if o.pathOf != nil {
			p = o.pathOf(m)
		}
		tree[p] = File{Path: p, Content: tidy(b.Bytes()), Mode: o.mode, GID: o.gid}
	}
	if err := renderFirewall(t, m, tree); err != nil {
		return nil, err
	}
	if careBuild(m) {
		cf := paths.RenderedBuild + "/Containerfile"
		tree[cf] = File{Path: cf, Content: carebuild.Containerfile, Mode: 0o644}
		ip := paths.RenderedBuild + "/install-plugs.json"
		tree[ip] = File{Path: ip, Content: []byte(m.CareBuild.PlugsJSON + "\n"), Mode: 0o644}
	}
	if !m.Bootstrap {
		if err := renderBackup(t, m, tree); err != nil {
			return nil, err
		}
	}
	return tree, nil
}

// renderFirewall writes the table inet care-pi. The boot file (paths.FirewallFile) has the confirmed policy: a
// policy drop of config.yaml that waits for its confirmation goes to paths.FirewallPendingFile, which the apply loads
// for the trial and "care-pi firewall confirm" moves over the boot file. A reboot before the confirmation loads
// accept. The pending file is the same text as the boot file after the confirmation.
func renderFirewall(t *template.Template, m *Model, tree Tree) error {
	put := func(p string, x *Model) error {
		var b bytes.Buffer
		if err := t.ExecuteTemplate(&b, "firewall.nft.tmpl", x); err != nil {
			return fmt.Errorf("render: firewall.nft.tmpl: %w", err)
		}
		tree[p] = File{Path: p, Content: tidy(b.Bytes()), Mode: 0o644}
		return nil
	}
	boot := *m
	if m.Firewall.Policy == "drop" && !m.FirewallDropConfirmed {
		if err := put(paths.FirewallPendingFile, m); err != nil {
			return err
		}
		boot.Firewall = FirewallModel{Policy: "accept", SSH: m.Firewall.SSH, Waits: true}
	}
	return put(paths.FirewallFile, &boot)
}

// tidy removes trailing spaces and collapses runs of blank lines that template actions leave.
func tidy(b []byte) []byte {
	lines := strings.Split(string(b), "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, l := range lines {
		l = strings.TrimRight(l, " \t")
		if l == "" {
			if blank {
				continue
			}
			blank = true
		} else {
			blank = false
		}
		out = append(out, l)
	}
	s := strings.TrimSpace(strings.Join(out, "\n"))
	return []byte(s + "\n")
}

// Owned reports whether care-pi owns the host path p. apply deletes owned files that the
// new tree does not contain, and never touches other files.
func Owned(p string) bool {
	switch {
	case strings.HasPrefix(p, paths.RenderedDir+"/"):
		return true
	case strings.HasPrefix(p, paths.QuadletDir+"/"):
		name := path.Base(p)
		if path.Dir(p) != paths.QuadletDir {
			return false
		}
		if name == "care.pod" {
			return true
		}
		if !strings.HasPrefix(name, "care-") {
			return false
		}
		switch path.Ext(name) {
		case ".container", ".pod", ".volume", ".image", ".build", ".network":
			return true
		}
		return false
	case p == systemd("care.target"), p == systemd("care-clock.service"), p == systemd(StartUnit):
		return true
	case BackupUnit(p):
		return true
	case p == systemd("care-update-check.timer"), p == systemd("care-update-check.service"),
		p == systemd("care-update-apply.timer"), p == systemd("care-update-apply.service"):
		return true
	case p == systemd(DNSUpdateTimer), p == systemd("care-dns-update.service"), p == paths.FirewallDropIn,
		p == paths.FirewallDropInLegacy, p == paths.SysctlFile, p == paths.TailscaledDropIn:
		return true
	}
	return false
}

// OwnedDirs lists the directories that hold owned files. apply scans them for stale files.
var OwnedDirs = []string{paths.QuadletDir, paths.SystemdDir, paths.RenderedDir, path.Dir(paths.FirewallDropIn), path.Dir(paths.SysctlFile),
	path.Dir(paths.TailscaledDropIn)}
