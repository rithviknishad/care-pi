// Package service is the shared layer of the console API and the CLI (CLI parity, 06 Phase 2).
// The HTTP handlers of internal/server and the commands of cmd/care-pi call the same methods.
// Each error that a client can act on is a *problem.Problem.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/auth"
	"github.com/rithviknishad/care-pi/agent/internal/buildinfo"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/hostcheck"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/render"
	"github.com/rithviknishad/care-pi/agent/internal/s3admin"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
	"github.com/rithviknishad/care-pi/agent/internal/status"
)

// Service holds the dependencies of each operation.
type Service struct {
	Paths        paths.Paths
	ConfigPath   string
	SolutionsDir string
	Host         *host.Host
	IPs          netinfo.Source
	S3           s3admin.Admin
	Auth         *auth.Manager
	Setup        *setup.Store
	Jobs         *jobs.Manager
	Log          *slog.Logger
	Now          func() time.Time
	StartedAt    time.Time
	// Internet is the background internet check. It can be nil.
	Internet *Internet
	// HostCheck runs the host checks. nil runs hostcheck.Run.
	HostCheck func(ctx context.Context) *hostcheck.Report
	// Chown sets the owners of created files (root only).
	Chown bool
	// HealthTimeout bounds the wait for a healthy care-api in the setup job. PollInterval is its poll.
	HealthTimeout time.Duration
	PollInterval  time.Duration
	// ApplyHook changes the apply options of each job. Tests use it.
	ApplyHook func(*apply.Options)
	// AfterPower runs the reboot or shutdown after the delay. nil runs systemctl.
	AfterPower func(delay time.Duration, fn func())

	mu           sync.Mutex
	carePassword string
	statusCache  cached[*Status]
	publicCache  cached[*PublicStatus]
	servedCache  cached[map[string]bool]
	probe        *DiskProbe
	configMu     sync.Mutex
}

type cached[T any] struct {
	at  time.Time
	val T
	ok  bool
}

func (c *cached[T]) get(now time.Time, ttl time.Duration) (T, bool) {
	if c.ok && now.Sub(c.at) < ttl {
		return c.val, true
	}
	var zero T
	return zero, false
}

func (c *cached[T]) set(now time.Time, v T) { c.at, c.val, c.ok = now, v, true }

func (s *Service) now() time.Time {
	if s.Now == nil {
		return time.Now().UTC().Truncate(time.Millisecond)
	}
	return s.Now().UTC().Truncate(time.Millisecond)
}

func (s *Service) log() *slog.Logger {
	if s.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return s.Log
}

func (s *Service) configPath() string {
	if s.ConfigPath != "" {
		return s.ConfigPath
	}
	return s.Paths.Abs(paths.ConfigFile)
}

var releaseTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

// DevBuild reports whether the agent is a dev build: its version is not a release tag vX.Y.Z.
func DevBuild() bool { return !releaseTag.MatchString(buildinfo.Version) }

// Config loads config.yaml.
func (s *Service) Config() (*config.Config, error) {
	cfg, err := config.Load(s.configPath())
	if err != nil {
		return nil, configProblem(err)
	}
	return cfg, nil
}

// configProblem maps a config error to invalid-config with pointers into the config document.
func configProblem(err error) *problem.Problem {
	var ve *config.ValidationError
	if !errors.As(err, &ve) {
		return problem.New(problem.InvalidConfig, err.Error())
	}
	p := problem.Newf(problem.InvalidConfig, "%d value(s) do not match the JSON Schema or the rules of config.yaml.", len(ve.Details))
	for _, d := range ve.Details {
		code := d.Keyword
		if !codeRE.MatchString(code) {
			code = ""
		}
		p.Errors = append(p.Errors, problem.FieldError{Pointer: d.Pointer, Message: d.Message, Code: code})
	}
	return p
}

var codeRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-]*$`)

// unsupported returns the valid settings that this version cannot apply (unsupported-config), or nil.
// base is the pointer of the checked object in the request body ("" for a whole config).
func unsupported(cfg *config.Config, base string) *problem.Problem {
	type item struct {
		ptr, msg string
		phase    int
	}
	var items []item
	tls := base + "/tls"
	if base == "/tls" {
		tls = ""
	}
	if cfg.TLS.Public.Enabled {
		items = append(items, item{tls + "/public/enabled", "the public certificate mode comes in Phase 6", 6})
	}
	if cfg.TLS.Tailscale.Enabled {
		items = append(items, item{tls + "/tailscale/enabled", "the Tailscale mode comes in Phase 6", 6})
	}
	if !cfg.TLS.LocalCA.Enabled {
		items = append(items, item{tls + "/local_ca/enabled", "this version serves only the local CA mode; another mode comes in Phase 6", 6})
	}
	if base == "" {
		if cfg.Solution.Track == "pinned" {
			items = append(items, item{"/solution/track", "the track pinned comes in Phase 4", 4})
		}
		if cfg.Clock.Policy == "rtc" {
			items = append(items, item{"/clock/policy", "the clock policy rtc comes in Phase 7 (spike SP9)", 7})
		}
	}
	if len(items) == 0 {
		return nil
	}
	p := problem.New(problem.UnsupportedConfig, items[0].msg+".")
	p.Phase = items[0].phase
	for _, it := range items {
		p.Errors = append(p.Errors, problem.FieldError{Pointer: it.ptr, Message: it.msg, Code: "phase"})
		p.Phase = max(p.Phase, it.phase)
	}
	return p
}

// applyProblem maps an error of apply.Run.
func applyProblem(err error) error {
	if err == nil {
		return nil
	}
	var p *problem.Problem
	if errors.As(err, &p) {
		return p
	}
	var ve *config.ValidationError
	if errors.As(err, &ve) {
		return configProblem(err)
	}
	if exitcode.Code(err) == exitcode.BadInput {
		if errors.Is(err, config.ErrNotImplemented) || strings.Contains(err.Error(), "not implemented in this version") {
			pr := problem.New(problem.UnsupportedConfig, err.Error())
			pr.Phase = 4
			return pr
		}
		return problem.New(problem.InvalidConfig, err.Error())
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	return problem.New(problem.Internal, err.Error())
}

// Hostnames returns the host names that Caddy serves.
func (s *Service) Hostnames() []string {
	cfg, err := config.Load(s.configPath())
	if err != nil {
		return []string{}
	}
	out := render.Hostnames(cfg)
	if out == nil {
		out = []string{}
	}
	return out
}

// Addresses returns the LAN IPv4 addresses now.
func (s *Service) Addresses() []string {
	out, err := netinfo.LANIPv4(s.IPs)
	if err != nil || out == nil {
		return []string{}
	}
	return out
}

// Published returns the addresses in the rendered care.pod.
func (s *Service) Published() []string {
	out := status.PublishedIPs(s.Paths)
	if out == nil {
		return []string{}
	}
	return out
}

// ServedHosts returns each host name and address that Caddy serves. The Origin check uses it.
func (s *Service) ServedHosts() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if v, ok := s.servedCache.get(now, 5*time.Second); ok {
		return v
	}
	m := map[string]bool{}
	for _, h := range s.Hostnames() {
		m[h] = true
	}
	for _, a := range s.Addresses() {
		m[a] = true
	}
	for _, a := range s.Published() {
		m[a] = true
	}
	for _, h := range s.RenderedHosts() {
		m[h] = true
	}
	s.servedCache.set(now, m)
	return m
}

// RenderedHosts returns the hosts of the console site in the rendered Caddyfile: the names that
// Caddy serves now. During the setup, config.yaml can have a new host name before the finish
// applies it, and the console must keep working on the old name.
func (s *Service) RenderedHosts() []string {
	data, err := os.ReadFile(s.Paths.Abs(paths.RenderedCaddy + "/Caddyfile"))
	if err != nil {
		return nil
	}
	port := fmt.Sprintf(":%d", paths.ConsolePort)
	var out []string
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "https://") || !strings.HasSuffix(l, "{") {
			continue
		}
		for _, a := range strings.Split(strings.TrimSuffix(l, "{"), ",") {
			a = strings.TrimSpace(a)
			if h, ok := strings.CutSuffix(strings.TrimPrefix(a, "https://"), port); ok && h != "" {
				out = append(out, h)
			}
		}
	}
	return out
}

// PrimaryHost returns the first host name, else the first address, else "care.local".
func (s *Service) PrimaryHost() string {
	if h := s.Hostnames(); len(h) > 0 {
		return h[0]
	}
	if a := s.Addresses(); len(a) > 0 {
		return a[0]
	}
	return "care.local"
}

// CareURLs returns Care on each host name and address.
func (s *Service) CareURLs() []string {
	out := []string{}
	for _, h := range s.Hostnames() {
		out = append(out, "https://"+h+"/")
	}
	for _, a := range s.Addresses() {
		out = append(out, "https://"+a+"/")
	}
	return out
}

func (s *Service) hostCheck(ctx context.Context) *hostcheck.Report {
	if s.HostCheck != nil {
		return s.HostCheck(ctx)
	}
	return hostcheck.Run(ctx, hostcheck.Env{Paths: s.Paths, Podman: s.Host.Podman})
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (s *Service) lockExists() bool { return fileExists(s.Paths.Abs(paths.LockFile)) }

// applyOptions returns the options of an apply run of a job.
func (s *Service) applyOptions(o apply.Options) apply.Options {
	o.Paths = s.Paths
	o.ConfigPath = s.configPath()
	o.SolutionsDir = s.SolutionsDir
	o.Host = s.Host
	o.IPs = s.IPs
	o.S3 = s.S3
	o.Chown = s.Chown
	if s.Now != nil {
		o.Now = s.Now
	}
	if s.ApplyHook != nil {
		s.ApplyHook(&o)
	}
	return o
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func errf(format string, a ...any) error { return fmt.Errorf(format, a...) }
