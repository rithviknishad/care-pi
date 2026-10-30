package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/plan"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/render"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
)

func readFile(path string) ([]byte, error) { return os.ReadFile(path) }

// etagOf returns the first 16 hex digits of the SHA-256 of the file, in quotes.
func etagOf(data []byte) string {
	sum := sha256.Sum256(data)
	return `"` + hex.EncodeToString(sum[:])[:16] + `"`
}

// topOrder is the key order of a config that the agent writes.
var topOrder = []string{"version", "identity", "solution", "size", "tls", "clock", "backups", "updates", "env", "fe",
	"images", "uploads", "network", "telemetry"}

// marshalConfig writes the document as YAML: the top keys in the order of docs/config.md, the other keys sorted.
func marshalConfig(doc map[string]any) ([]byte, error) {
	root := &yaml.Node{Kind: yaml.MappingNode}
	var keys []string
	seen := map[string]bool{}
	for _, k := range topOrder {
		if _, ok := doc[k]; ok {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	var rest []string
	for k := range doc {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range append(keys, rest...) {
		var v yaml.Node
		if err := v.Encode(doc[k]); err != nil {
			return nil, err
		}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, &v)
	}
	var b bytes.Buffer
	b.WriteString("# care-pi desired state. The console wrote this file: comments and the key order are lost.\n")
	b.WriteString("# Reference: docs/config.md\n")
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (s *Service) writeConfig(data []byte) error {
	path := s.configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := lock.WriteFileAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("cannot write config.yaml: %w", err)
	}
	s.mu.Lock()
	s.servedCache.ok = false
	s.mu.Unlock()
	return nil
}

// ConfigView returns the config as JSON and its ETag. view is "effective" (with the defaults) or "file".
func (s *Service) ConfigView(view string) (any, string, error) {
	doc, etag, err := s.readConfigDoc()
	if err != nil {
		return nil, "", err
	}
	if view == "file" {
		return doc, etag, nil
	}
	cfg, err := s.Config()
	if err != nil {
		return nil, "", err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, "", err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, "", err
	}
	return out, etag, nil
}

// ETag returns the ETag of config.yaml.
func (s *Service) ETag() (string, error) {
	data, err := readFile(s.configPath())
	if err != nil {
		return "", err
	}
	return etagOf(data), nil
}

// PlanAction is one action of a plan (schema PlanAction).
type PlanAction struct {
	Kind   string   `json:"kind"`
	Text   string   `json:"text"`
	Units  []string `json:"units"`
	Paths  []string `json:"paths"`
	Names  []string `json:"names"`
	Reason *string  `json:"reason"`
}

// PlanChange is one file change (schema PlanChange).
type PlanChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

// PlanFEBuild is the FE build of a plan (schema PlanFeBuild).
type PlanFEBuild struct {
	BuildID  string `json:"build_id"`
	Provider string `json:"provider"`
	Exists   bool   `json:"exists"`
}

// PlanSummary is the first lines of the apply output.
type PlanSummary struct {
	Solution  string   `json:"solution"`
	Track     string   `json:"track"`
	Preset    string   `json:"preset"`
	Hostnames []string `json:"hostnames"`
	Addresses []string `json:"addresses"`
}

// PlanPreview is the plan of an apply (schema PlanPreview).
type PlanPreview struct {
	Changed     bool         `json:"changed"`
	Summary     PlanSummary  `json:"summary"`
	Changes     []PlanChange `json:"changes"`
	Actions     []PlanAction `json:"actions"`
	FEBuild     *PlanFEBuild `json:"fe_build"`
	Impact      string       `json:"impact"`
	Restarts    []string     `json:"restarts"`
	LockChanged bool         `json:"lock_changed"`
	Warnings    []string     `json:"warnings"`
	Diff        *string      `json:"diff"`
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func previewOf(r *apply.Result, withDiff bool) *PlanPreview {
	pv := &PlanPreview{Changed: r.Changed, LockChanged: r.LockChanged, Impact: "none",
		Summary: PlanSummary{Solution: r.Solution, Track: r.Track, Preset: r.Preset, Hostnames: nonNil(r.Hostnames),
			Addresses: nonNil(r.Addresses)},
		Changes: []PlanChange{}, Actions: []PlanAction{}, Restarts: []string{}, Warnings: nonNil(r.Warnings)}
	rank := map[string]int{"none": 0, "reload": 1, "restart": 2, "full-cycle": 3}
	raise := func(i string) {
		if rank[i] > rank[pv.Impact] {
			pv.Impact = i
		}
	}
	var diff strings.Builder
	for _, c := range r.Plan.Changes {
		pv.Changes = append(pv.Changes, PlanChange{Path: c.Path, Kind: string(c.Kind)})
		if withDiff {
			a, b := c.Old, c.New
			if c.Kind == plan.Add {
				a = nil
			}
			if c.Kind == plan.Delete {
				b = nil
			}
			diff.WriteString(plan.UnifiedDiff(c.Path, a, b))
		}
	}
	for _, a := range r.Plan.Actions {
		pa := PlanAction{Kind: string(a.Kind), Text: a.String(), Units: nonNil(a.Units), Paths: nonNil(a.Paths),
			Names: nonNil(a.Names), Reason: strPtr(a.Reason)}
		pv.Actions = append(pv.Actions, pa)
		switch a.Kind {
		case plan.ReloadCaddy:
			raise("reload")
		case plan.Restart:
			raise("restart")
			pv.Restarts = append(pv.Restarts, a.Units...)
		case plan.FullCycle, plan.StartTarget:
			raise("full-cycle")
			units := render.Units
			if r.Phase == setup.PhaseBootstrap {
				units = render.BootstrapUnits
			}
			pv.Restarts = append(pv.Restarts, units...)
		case plan.EnsureFE:
			pv.FEBuild = &PlanFEBuild{BuildID: r.FEBuildID, Provider: r.FEProvider, Exists: r.FEExists}
		}
	}
	if withDiff {
		d := diff.String()
		pv.Diff = &d
	}
	return pv
}

// Plan previews an apply of config.yaml (care-pi apply --dry-run). It holds the apply lock.
func (s *Service) Plan(ctx context.Context, update, withDiff bool) (*PlanPreview, error) {
	return s.planFor(ctx, s.configPath(), update, withDiff)
}

func (s *Service) planFor(ctx context.Context, cfgPath string, update, withDiff bool) (*PlanPreview, error) {
	release, err := s.Jobs.AcquireLock(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	o := s.applyOptions(apply.Options{DryRun: true, Update: update, LockHeld: true})
	o.ConfigPath = cfgPath
	o.Journal = nil
	r, err := apply.Run(ctx, o)
	if err != nil {
		return nil, applyProblem(err)
	}
	return previewOf(r, withDiff), nil
}

// ConfigChangeResult is the result of a config write (schema ConfigChangeResult).
type ConfigChangeResult struct {
	Written bool         `json:"written"`
	ETag    string       `json:"etag"`
	Plan    *PlanPreview `json:"plan"`
	Job     *jobs.Job    `json:"job"`
}

// ConfigWrite describes a PUT or PATCH of the config.
type ConfigWrite struct {
	IfMatch string
	DryRun  bool
	Apply   bool
	Source  string
	// Body is the new document (PUT), or the merge patch (Patch true).
	Body  map[string]any
	Patch bool
}

// WriteConfig validates a new config, previews the plan, writes the file, and can start an apply job.
func (s *Service) WriteConfig(ctx context.Context, w ConfigWrite) (*ConfigChangeResult, error) {
	if w.IfMatch == "" {
		return nil, problem.New(problem.PreconditionRequired, "Read the config with GET /config, then send its ETag in If-Match.")
	}
	if w.DryRun && w.Apply {
		return nil, problem.InvalidParam("apply", "apply=true is not possible together with dry_run=true")
	}
	s.configMu.Lock()
	defer s.configMu.Unlock()
	cur, etag, err := s.readConfigDoc()
	if err != nil {
		return nil, err
	}
	if w.IfMatch != etag && w.IfMatch != "*" {
		return nil, problem.Newf(problem.ConfigChanged, "The ETag is now %s. Read the config again.", etag)
	}
	doc := w.Body
	if w.Patch {
		merged, _ := MergePatch(cur, w.Body).(map[string]any)
		if merged == nil {
			merged = map[string]any{}
		}
		doc = merged
	}
	data, err := marshalConfig(doc)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Parse(data, "config.yaml")
	if err != nil {
		return nil, configProblem(err)
	}
	if p := unsupported(cfg, ""); p != nil {
		return nil, p
	}
	if w.Apply && s.SetupStateName() != setup.Ready {
		return nil, problem.New(problem.SetupIncomplete, "Finish the setup first. Then apply=true is possible.")
	}
	tmp, err := os.CreateTemp(s.Paths.Abs(paths.StateDir), "config-preview-*.yaml")
	if err != nil {
		if err := os.MkdirAll(s.Paths.Abs(paths.StateDir), 0o700); err != nil {
			return nil, err
		}
		if tmp, err = os.CreateTemp(s.Paths.Abs(paths.StateDir), "config-preview-*.yaml"); err != nil {
			return nil, err
		}
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	_ = tmp.Close()
	pv, err := s.planFor(ctx, tmp.Name(), false, false)
	if err != nil {
		return nil, err
	}
	res := &ConfigChangeResult{ETag: etag, Plan: pv}
	if w.DryRun {
		return res, nil
	}
	if err := s.writeConfig(data); err != nil {
		return nil, err
	}
	res.Written, res.ETag = true, etagOf(data)
	if w.Apply {
		j, err := s.StartApply(ctx, ApplyRequest{Reason: "config change"}, w.Source)
		if err != nil {
			return res, err
		}
		res.Job = j
	}
	return res, nil
}

// MergePatch applies a JSON merge patch (RFC 7396).
func MergePatch(target, patch any) any {
	p, ok := patch.(map[string]any)
	if !ok {
		return patch
	}
	t, ok := target.(map[string]any)
	if !ok {
		t = map[string]any{}
	}
	out := map[string]any{}
	for k, v := range t {
		out[k] = v
	}
	for k, v := range p {
		if v == nil {
			delete(out, k)
			continue
		}
		out[k] = MergePatch(out[k], v)
	}
	return out
}

// SetKey sets a dotted key (identity.hostname) in the document. The value is YAML.
// "null" deletes the key. The CLI "config set" uses it.
func SetKey(doc map[string]any, key, value string) error {
	parts := strings.Split(key, ".")
	m := doc
	for _, p := range parts[:len(parts)-1] {
		if p == "" {
			return fmt.Errorf("the key %q is not valid", key)
		}
		next, ok := m[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[p] = next
		}
		m = next
	}
	last := parts[len(parts)-1]
	var v any
	if err := yaml.Unmarshal([]byte(value), &v); err != nil {
		return fmt.Errorf("the value of %s is not valid YAML: %w", key, err)
	}
	if v == nil {
		delete(m, last)
		return nil
	}
	// A value that looks like a number stays a string for env variables.
	if len(parts) >= 3 && parts[0] == "env" {
		v = value
	}
	m[last] = config.Normalize(v)
	return nil
}

// ApplyRequest holds the options of an apply (schema ApplyRequest).
type ApplyRequest struct {
	Reason string
	Update bool
}

// StartApply validates config.yaml and starts an apply job.
func (s *Service) StartApply(ctx context.Context, req ApplyRequest, source string) (*jobs.Job, error) {
	if s.SetupStateName() != setup.Ready {
		return nil, problem.New(problem.SetupIncomplete, "Finish the setup first. The setup job runs the first full apply.")
	}
	cfg, err := s.Config()
	if err != nil {
		return nil, err
	}
	if p := unsupported(cfg, ""); p != nil {
		return nil, p
	}
	return s.Jobs.Start(ctx, jobs.Spec{Kind: jobs.KindApply, Source: source, Reason: req.Reason, Lock: true},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			o := s.applyOptions(apply.Options{Reason: req.Reason, Update: req.Update, Out: rec.Writer(), Journal: rec, LockHeld: true})
			r, err := apply.Run(ctx, o)
			if err != nil {
				return nil, applyProblem(err)
			}
			return r.JobResult(), nil
		})
}

// Preset is one preset (schema Preset).
type Preset struct {
	Name              string               `json:"name"`
	GunicornWorkers   *int                 `json:"gunicorn_workers"`
	CeleryConcurrency *int                 `json:"celery_concurrency"`
	PGSharedBuffersMB *int                 `json:"pg_shared_buffers_mb"`
	Memory            map[string]MemoryCap `json:"memory"`
	Recommended       bool                 `json:"recommended"`
	Warning           *string              `json:"warning"`
}

// MemoryCap is MemoryMax and MemorySwapMax in MiB (schema MemoryCap).
type MemoryCap struct {
	MaxMB     int `json:"max_mb"`
	SwapMaxMB int `json:"swap_max_mb"`
}

// PresetList is the list of presets (schema PresetList).
type PresetList struct {
	Default      string   `json:"default"`
	HostRAMBytes int64    `json:"host_ram_bytes"`
	Items        []Preset `json:"items"`
}

// Presets lists the presets with the memory caps and the recommendation for the RAM of this host.
func (s *Service) Presets() *PresetList {
	ram := memTotal(s.Paths)
	out := &PresetList{Default: config.DefaultPreset, HostRAMBytes: ram, Items: []Preset{}}
	rec := "small-clinic"
	switch {
	case ram > 0 && ram < 1536<<20:
		rec = "solo"
	case ram >= 3<<30:
		rec = "busy-counter"
	}
	for _, name := range config.PresetNames {
		p := Preset{Name: name, Recommended: name == rec}
		if sz, ok := config.Presets[name]; ok {
			g, c, b := sz.GunicornWorkers, sz.CeleryConcurrency, sz.PGSharedBuffersMB
			p.GunicornWorkers, p.CeleryConcurrency, p.PGSharedBuffersMB = &g, &c, &b
			memCap := func(m config.Mem) MemoryCap { return MemoryCap{MaxMB: m.MaxMB, SwapMaxMB: m.SwapMB} }
			p.Memory = map[string]MemoryCap{
				"care-api": memCap(config.MemAPI(g)), "care-worker": memCap(config.MemWorker(c)), "care-beat": memCap(config.MemBeat),
				"care-db": memCap(config.MemDB(b)), "care-redis": memCap(config.MemRedis), "care-s3": memCap(config.MemS3),
				"care-caddy": memCap(config.MemCaddy),
			}
		}
		if name == "busy-counter" && ram > 0 && ram < 3<<30 {
			p.Warning = ptr("busy-counter-low-ram")
		}
		out.Items = append(out.Items, p)
	}
	return out
}

// Arch returns the CPU architecture for the API (arm64 or amd64).
func Arch() string {
	if runtime.GOARCH == "arm64" {
		return "arm64"
	}
	return "amd64"
}
