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

func always(*Model) bool         { return true }
func feBuild(m *Model) bool      { return m.FEBuild }
func quadlet(name string) string { return paths.QuadletDir + "/" + name }
func systemd(name string) string { return paths.SystemdDir + "/" + name }

var outputs = []output{
	{"care.pod.tmpl", quadlet("care.pod"), 0o644, 0, always},
	{"care-caddy.container.tmpl", quadlet("care-caddy.container"), 0o644, 0, always},
	{"care-db.container.tmpl", quadlet("care-db.container"), 0o644, 0, always},
	{"care-redis.container.tmpl", quadlet("care-redis.container"), 0o644, 0, always},
	{"care-s3.container.tmpl", quadlet("care-s3.container"), 0o644, 0, always},
	{"care-beat.container.tmpl", quadlet("care-beat.container"), 0o644, 0, always},
	{"care-worker.container.tmpl", quadlet("care-worker.container"), 0o644, 0, always},
	{"care-api.container.tmpl", quadlet("care-api.container"), 0o644, 0, always},
	{"care-fe-build.container.tmpl", quadlet("care-fe-build.container"), 0o644, 0, feBuild},
	{"care.target.tmpl", systemd("care.target"), 0o644, 0, always},
	{"care-clock.service.tmpl", systemd("care-clock.service"), 0o644, 0, always},
	{"care-start.service.tmpl", systemd(StartUnit), 0o644, 0, always},
	{"Caddyfile.tmpl", paths.RenderedCaddy + "/Caddyfile", 0o600, 0, always},
	{"status-setup.html.tmpl", paths.RenderedCaddy + "/status/setup.html", 0o644, 0, always},
	{"status-maintenance.html.tmpl", paths.RenderedCaddy + "/status/maintenance.html", 0o644, 0, always},
	{"status-clock.html.tmpl", paths.RenderedCaddy + "/status/clock.html", 0o644, 0, always},
	{"backend.env.tmpl", paths.RenderedCare + "/backend.env", 0o600, 0, always},
	{"pgbackrest.conf.tmpl", paths.RenderedDB + "/pgbackrest.conf", 0o640, paths.PostgresGID, always},
	{"frontend.env.tmpl", paths.RenderedFE + "/frontend.env", 0o600, 0, feBuild},
	{"fe-build.sh.tmpl", paths.RenderedFE + "/" + FEBuildScriptName, 0o700, 0, feBuild},
}

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
	for _, o := range outputs {
		if !o.when(m) {
			continue
		}
		var b bytes.Buffer
		if err := t.ExecuteTemplate(&b, o.tmpl, m); err != nil {
			return nil, fmt.Errorf("render: %s: %w", o.tmpl, err)
		}
		tree[o.path] = File{Path: o.path, Content: tidy(b.Bytes()), Mode: o.mode, GID: o.gid}
	}
	return tree, nil
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
	}
	return false
}

// OwnedDirs lists the directories that hold owned files. apply scans them for stale files.
var OwnedDirs = []string{paths.QuadletDir, paths.SystemdDir, paths.RenderedDir}
