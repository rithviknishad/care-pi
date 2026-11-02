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
	"sync/atomic"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/auth"
	"github.com/rithviknishad/care-pi/agent/internal/backup"
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
	"github.com/rithviknishad/care-pi/agent/internal/solution"
	"github.com/rithviknishad/care-pi/agent/internal/status"
	"github.com/rithviknishad/care-pi/agent/internal/update"
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
	// BackupTester replaces the destination test (tests). nil: the real SSH and S3 clients.
	BackupTester *backup.Tester
	// UpdateHook changes the update machine of each call (Phase 4). Tests set the verifier and the checkpoint.
	UpdateHook func(*update.Updater)
	// StopOnSignal is update.Updater.StopOnSignal: a CLI process sets it, care-pi serve does not.
	StopOnSignal bool
	// Phase 6 (docs/connectivity.md). DNSURL is the DNS/ACME service ("" is dnsclient.DefaultBaseURL); DNSBackoff
	// is the first wait of its retries. CertWait bounds the wait for the public certificate in the enrol job, and
	// LoginWait the wait for the Tailscale login. Tests set them.
	DNSURL     string
	DNSBackoff time.Duration
	CertWait   time.Duration
	LoginWait  time.Duration
	// ClockReloadDelay is the wait before care-caddy reloads after the claim set the clock (S52). 0: 2 s.
	ClockReloadDelay time.Duration

	mu           sync.Mutex
	carePassword string
	statusCache  cached[*Status]
	publicCache  cached[*PublicStatus]
	servedCache  cached[map[string]bool]
	localCache   cached[*localSnap]
	backupsCache cached[*BackupsOverview]
	probe        *DiskProbe
	configMu     sync.Mutex
	// recovering is true while the restart of an interrupted cutover runs (RecoverUpdates).
	recovering atomic.Bool
	// recovered holds the ids of the cutover jobs that RecoverUpdates handled: one try in each process.
	recovered sync.Map
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

// DevBuild reports whether the agent is a dev build: its version is not a release tag vX.Y.Z.
func DevBuild() bool { return !solution.IsRelease(buildinfo.Version) }

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
	// The setup step turns on only the local CA mode. The public name and Tailscale need their enrol jobs
	// (PUT /tls/public, PUT /tls/tailscale) after the setup.
	if base == "/tls" && cfg.TLS.Public.Enabled {
		items = append(items, item{tls + "/public/enabled", "turn on the public name after the setup, in Settings > Connectivity", 0})
	}
	if base == "/tls" && cfg.TLS.Tailscale.Enabled {
		items = append(items, item{tls + "/tailscale/enabled", "turn on Tailscale after the setup, in Settings > Connectivity", 0})
	}
	if !cfg.TLS.LocalCA.Enabled {
		items = append(items, item{tls + "/local_ca/enabled", "the local CA mode stays on: it serves the LAN addresses and works offline (S36)", 7})
	}
	if base == "" {
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
		code := "phase"
		if it.phase == 0 {
			code = "enrol"
		}
		p.Errors = append(p.Errors, problem.FieldError{Pointer: it.ptr, Message: it.msg, Code: code})
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
	// Phase 4: the plan action cutover takes the checkpoint of Phase 3 (docs/api/README.md section 13.11).
	if o.Checkpoint == nil {
		o.Checkpoint = s.Checkpoint
	}
	// Phase 6: the plan action update-dns sends a new LAN address to the DNS service.
	if o.DNSAddress == nil {
		o.DNSAddress = s.UpdateDNSAddress
	}
	if s.ApplyHook != nil {
		s.ApplyHook(&o)
	}
	return o
}

// Checkpoint takes the checkpoint of Phase 3 (docs/api/README.md section 13.11) for the plan action cutover of an
// apply (apply.Options.Checkpoint). jobID labels it; "" gives the label apply-<time> and no job in RestorePoints.
func (s *Service) Checkpoint(ctx context.Context, jobID string) error {
	if jobID == "" {
		jobID = "apply-" + s.now().Format("20060102T150405Z")
	}
	_, err := s.Updater().Checkpointer(jobID).Checkpoint(ctx)
	return err
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func errf(format string, a ...any) error { return fmt.Errorf(format, a...) }
