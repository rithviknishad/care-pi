// Package fe defines the frontend providers.
//
// A provider makes sure that the care_fe build with a given build id exists in
// /var/lib/care-pi/fe/<build id>/. apply then points fe/current at it.
// Caddy checks fe/current/index.html on each request, so it needs no reload.
//
// The providers are "none" (no build), "artifact" (download a bundle), and
// "local" (build on the host in the care-fe-build unit). docs/fe.md describes them.
package fe

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// Provider produces FE builds.
type Provider interface {
	// Name returns the value of fe.provider in config.yaml.
	Name() string
	// Ensure makes sure that the build exists. Build.Dir is "" when the provider serves no build.
	Ensure(ctx context.Context, buildID string) (Build, error)
}

// Build is a build that a provider made or found.
type Build struct {
	// Dir is the host path of the build directory, for example /var/lib/care-pi/fe/<build id>.
	Dir string
	// Record holds the fields for the fe section of the lock.
	Record lock.FE
}

// Env is what a provider factory receives.
type Env struct {
	Config *config.Config
	Paths  paths.Paths
	// CareFERepo and CareFESHA identify the source from the lock.
	CareFERepo string
	CareFESHA  string
	// BuildEnv is the merged frontend env, with REACT_CARE_API_URL.
	BuildEnv map[string]string
	// Host runs the host commands of the local provider.
	Host *host.Host
	// Out receives the progress messages. It can be nil.
	Out io.Writer
	// Follow streams the log of the local build to the terminal.
	Follow bool
}

func (e Env) out() io.Writer {
	if e.Out == nil {
		return io.Discard
	}
	return e.Out
}

// Factory creates a provider.
type Factory func(Env) (Provider, error)

var (
	mu       sync.RWMutex
	registry = map[string]Factory{
		"none": func(Env) (Provider, error) { return None{}, nil },
	}
)

// Register adds a provider factory. The providers call it from an init function.
func Register(name string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	registry[name] = f
}

// ErrNotImplemented marks a provider that this version does not have.
var ErrNotImplemented = errors.New("not implemented in this version")

// Registered reports whether a provider with this name exists.
func Registered(name string) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := registry[name]
	return ok
}

// New returns the provider for name.
func New(name string, env Env) (Provider, error) {
	mu.RLock()
	f, ok := registry[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("fe.provider %q is %w", name, ErrNotImplemented)
	}
	return f(env)
}

// None serves no build. Caddy then shows the setup status page.
type None struct{}

// Name implements Provider.
func (None) Name() string { return "none" }

// Ensure implements Provider.
func (None) Ensure(context.Context, string) (Build, error) { return Build{}, nil }

// BuildID returns the id of a build: a hash of the care_fe sha and the build env.
//
// The id is the first 16 hex digits of the SHA-256 of "care_fe=<sha>\n" followed by
// one "NAME=value\n" line for each variable, sorted by name. The agent and the
// fe-bundle workflow ("care-pi fe build-id") use this function, so their ids agree.
func BuildID(careFESHA string, env map[string]string) string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "care_fe=%s\n", careFESHA)
	for _, k := range keys {
		_, _ = fmt.Fprintf(h, "%s=%s\n", k, env[k])
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ParseEnvFile reads a frontend env file: one NAME=value line for each variable.
// The value is literal (no quotes, no expansion), like fe-build.sh reads it.
// Empty lines and lines that start with "#" are comments.
func ParseEnvFile(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok || !envNameRE.MatchString(name) {
			return nil, fmt.Errorf("line %d is not NAME=value", n)
		}
		out[name] = value
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// validID reports whether id can name a build directory.
func validID(id string) bool {
	return id != "" && id != "current" && !strings.ContainsAny(id, "/.\\") && !strings.HasPrefix(id, "-")
}

// Dir returns the host path of the build directory of buildID.
func Dir(buildID string) string { return paths.FEDir + "/" + buildID }

// Exists reports whether the build has an index.html.
func Exists(p paths.Paths, buildID string) bool {
	if !validID(buildID) {
		return false
	}
	fi, err := os.Stat(filepath.Join(p.Abs(Dir(buildID)), "index.html"))
	return err == nil && fi.Mode().IsRegular()
}

// SetCurrent points fe/current at fe/<buildID> with a relative symlink.
// The swap is atomic: a new link replaces the old one with rename.
func SetCurrent(p paths.Paths, buildID string) error {
	if !validID(buildID) {
		return fmt.Errorf("bad FE build id %q", buildID)
	}
	dir := p.Abs(paths.FEDir)
	if _, err := os.Stat(filepath.Join(dir, buildID, "index.html")); err != nil {
		return fmt.Errorf("FE build %s has no index.html: %w", buildID, err)
	}
	tmp := filepath.Join(dir, ".current-"+buildID)
	_ = os.Remove(tmp)
	if err := os.Symlink(buildID, tmp); err != nil {
		return fmt.Errorf("cannot create the FE link: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, "current")); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("cannot switch the FE link: %w", err)
	}
	return nil
}

// Current returns the build id that fe/current points at, or "".
func Current(p paths.Paths) string {
	target, err := os.Readlink(p.Abs(paths.FECurrent))
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

// MergeRecord returns dst with each non-empty field of src.
// apply uses it to keep the metadata of a build that already existed.
func MergeRecord(dst, src lock.FE) lock.FE {
	set := func(d *string, s string) {
		if s != "" {
			*d = s
		}
	}
	set(&dst.Provider, src.Provider)
	set(&dst.BuildID, src.BuildID)
	set(&dst.CareFESHA, src.CareFESHA)
	set(&dst.URL, src.URL)
	set(&dst.SHA256, src.SHA256)
	set(&dst.BuiltAt, src.BuiltAt)
	set(&dst.Duration, src.Duration)
	return dst
}
