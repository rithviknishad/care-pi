package update

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"sort"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/plugcfg"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/render"
	"github.com/rithviknishad/care-pi/agent/internal/secrets"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
)

// PlugBackendState is the backend part of a plug (schema PlugBackendState).
type PlugBackendState struct {
	Name      string  `json:"name"`
	Package   string  `json:"package"`
	Ref       string  `json:"ref"`
	Tracked   bool    `json:"tracked"`
	SHA       *string `json:"sha"`
	TargetSHA *string `json:"target_sha"`
	Installed bool    `json:"installed"`
	Image     string  `json:"image"`
}

// BundleState is the bundle of an FE plug on the device.
type BundleState struct {
	State     string  `json:"state"`
	Path      *string `json:"path"`
	SHA256    *string `json:"sha256"`
	SizeBytes *int64  `json:"size_bytes"`
}

// PlugConfigState is the plug_config row of an FE plug in Care.
type PlugConfigState struct {
	State     string     `json:"state"`
	MetaURL   *string    `json:"meta_url"`
	CheckedAt *time.Time `json:"checked_at"`
}

// PlugFrontendState is the frontend part of a plug (schema PlugFrontendState).
type PlugFrontendState struct {
	Slug       string          `json:"slug"`
	Repo       *string         `json:"repo"`
	Ref        *string         `json:"ref"`
	Tracked    bool            `json:"tracked"`
	SHA        *string         `json:"sha"`
	Source     string          `json:"source"`
	MetaURL    *string         `json:"meta_url"`
	Bundle     BundleState     `json:"bundle"`
	PlugConfig PlugConfigState `json:"plug_config"`
}

// PlugSetting is one field of the settings form with its value (schema PlugSetting).
type PlugSetting struct {
	solution.Setting
	Value     any    `json:"value"`
	SecretSet *bool  `json:"secret_set"`
	Source    string `json:"source"`
}

// Plug is one plug (schema Plug).
type Plug struct {
	ID               string             `json:"id"`
	Label            string             `json:"label"`
	Description      string             `json:"description"`
	Kind             string             `json:"kind"`
	Enabled          bool               `json:"enabled"`
	On               bool               `json:"on"`
	Pending          bool               `json:"pending"`
	Source           string             `json:"source"`
	Auto             *string            `json:"auto"`
	AutoActive       bool               `json:"auto_active"`
	RequiresInternet bool               `json:"requires_internet"`
	Custom           bool               `json:"custom"`
	Backend          *PlugBackendState  `json:"backend"`
	Frontend         *PlugFrontendState `json:"frontend"`
	Settings         []PlugSetting      `json:"settings"`
	SettingsComplete bool               `json:"settings_complete"`
	SetupSteps       []string           `json:"setup_steps"`
}

// PlugList is GET /plugs (schema PlugList).
type PlugList struct {
	Solution      string  `json:"solution"`
	Version       *string `json:"version"`
	Track         string  `json:"track"`
	HostnameCount int     `json:"hostname_count"`
	Pending       bool    `json:"pending"`
	Items         []Plug  `json:"items"`
}

// ChangePlan is what a plug change does (schema PlugChangePlan).
type ChangePlan struct {
	Changed            bool     `json:"changed"`
	Path               string   `json:"path"`
	LockChanged        bool     `json:"lock_changed"`
	Maintenance        bool     `json:"maintenance"`
	Checkpoint         bool     `json:"checkpoint"`
	Restarts           []string `json:"restarts"`
	Downloads          []string `json:"downloads"`
	Builds             []string `json:"builds"`
	NeedsInternet      bool     `json:"needs_internet"`
	EstimatedDowntimeS *int     `json:"estimated_downtime_s"`
	Steps              []string `json:"steps"`
	Warnings           []string `json:"warnings"`
}

// PlugChange is the body of PUT /plugs/{id} (schema PlugChange).
type PlugChange struct {
	// SetEnabled tells that the body has the member enabled; Enabled nil is "follow the solution".
	SetEnabled bool
	Enabled    *bool
	Backend    map[string]any
	Frontend   map[string]any
	Reason     string
}

// plugsContext holds what the plug views need.
type plugsContext struct {
	cfg     *config.Config
	cur     *lock.Lock
	sol     *solution.Solution
	sl      *solution.Lock
	eff     []solution.EffectivePlug
	secrets map[string]map[string]any
	hosts   int
	paths   paths.Paths
}

func (u *Updater) plugsContext(cfg *config.Config) (*plugsContext, error) {
	cur, err := lock.Load(u.Paths)
	if err != nil {
		return nil, err
	}
	sol, sl := u.solutionOf(cfg, cur)
	hosts := len(render.Hostnames(cfg))
	eff, err := sol.Effective(cfg, hosts)
	if err != nil {
		return nil, problem.New(problem.InvalidConfig, err.Error())
	}
	sec, err := secrets.Store{Paths: u.Paths}.AllPlugs()
	if err != nil {
		return nil, err
	}
	return &plugsContext{cfg: cfg, cur: cur, sol: sol, sl: sl, eff: eff, secrets: sec, hosts: hosts, paths: u.Paths}, nil
}

// Plugs returns GET /plugs.
func (u *Updater) Plugs(ctx context.Context) (*PlugList, error) {
	cfg, err := u.Config()
	if err != nil {
		return nil, problem.New(problem.InvalidConfig, err.Error())
	}
	pc, err := u.plugsContext(cfg)
	if err != nil {
		return nil, err
	}
	out := &PlugList{Solution: cfg.Solution.Name, Track: cfg.Solution.Track, HostnameCount: pc.hosts, Items: []Plug{}}
	if pc.sl != nil {
		out.Version = strp(solution.NormalizeLockVersion(pc.sl.Version))
	}
	for _, p := range pc.eff {
		v := u.plugView(pc, p)
		out.Pending = out.Pending || v.Pending
		out.Items = append(out.Items, v)
	}
	return out, nil
}

func (u *Updater) plugView(pc *plugsContext, p solution.EffectivePlug) Plug {
	v := Plug{ID: p.ID, Label: p.Label, Description: p.Description, Enabled: p.On, Source: p.Reason, AutoActive: p.AutoActive,
		RequiresInternet: p.RequiresInternet, Custom: p.Custom, Settings: []PlugSetting{}, SetupSteps: []string{}}
	if v.Label == "" {
		v.Label = p.ID
	}
	if p.SetupSteps != nil {
		v.SetupSteps = p.SetupSteps
	}
	if p.Auto != "" {
		v.Auto = strp(p.Auto)
	}
	switch {
	case p.Backend != nil && p.Frontend != nil:
		v.Kind = "both"
	case p.Backend != nil:
		v.Kind = "backend"
	default:
		v.Kind = "frontend"
	}
	var comp lock.PlugComponent
	if pc.cur != nil {
		comp = pc.cur.Components.Plugs[p.ID]
		v.On = comp.Enabled
	}
	if p.Backend != nil {
		b := &PlugBackendState{Name: p.Backend.Name, Package: p.Backend.Package, Ref: p.Backend.Ref, Tracked: !isSHA(p.Backend.Ref), Image: "none"}
		if comp.Backend != nil {
			b.SHA, b.Installed = strp(comp.Backend.SHA), comp.Backend.Installed
		}
		if pc.sl != nil {
			if lp := pc.sl.Plugs[p.ID]; lp.Backend != nil {
				b.TargetSHA = strp(lp.Backend.SHA)
			}
			for _, id := range pc.sl.SolutionImage.InstalledPlugs {
				if id == p.ID {
					b.Installed = true
				}
			}
		}
		if b.Installed {
			b.Image = "solution-image"
			if pc.cur != nil && pc.cur.Components.Care != nil && pc.cur.Components.Care.Build != nil {
				b.Image = "local-build"
			}
		}
		v.Backend = b
	}
	if p.Frontend != nil {
		f := &PlugFrontendState{Slug: p.Frontend.Slug, Repo: strp(p.Frontend.Repo), Ref: strp(p.Frontend.Ref), Tracked: p.Frontend.Repo != "" && !isSHA(p.Frontend.Ref),
			Source: solution.FrontendExternal, Bundle: BundleState{State: "external"}, PlugConfig: PlugConfigState{State: "missing"}}
		if p.Frontend.Repo != "" {
			f.Source = solution.FrontendBuilt
		}
		if c := comp.Frontend; c != nil {
			f.Source, f.SHA, f.MetaURL = c.Source, strp(c.SHA), strp(c.MetaURL)
			if c.Source == solution.FrontendExternal {
				f.Repo, f.Ref, f.Tracked = nil, nil, false
			}
		}
		if f.Source != solution.FrontendExternal {
			f.Bundle = BundleState{State: "missing"}
			if f.SHA != nil && len(*f.SHA) >= 12 {
				dir := paths.PlugsDir + "/" + p.Frontend.Slug + "/" + (*f.SHA)[:12]
				if _, err := os.Stat(u.Paths.Abs(dir)); err == nil {
					f.Bundle = BundleState{State: "present", Path: strp(dir)}
					if comp.Frontend != nil {
						f.Bundle.SHA256 = strp(comp.Frontend.SHA256)
					}
				}
			}
		}
		st := plugcfg.ReadState(u.Paths)
		if seen, ok := st.Seen[p.Frontend.Slug]; ok {
			f.PlugConfig = PlugConfigState{State: "synced", MetaURL: strp(seen), CheckedAt: st.CheckedAt}
			if f.MetaURL != nil && seen != *f.MetaURL {
				f.PlugConfig.State = "differs"
			}
		} else {
			f.PlugConfig.CheckedAt = st.CheckedAt
		}
		v.Frontend = f
	}
	v.Settings, v.SettingsComplete = settingsView(pc.cfg, p.Plug, pc.secrets[p.ID])
	v.Pending = v.Enabled != v.On || settingsPending(pc, p)
	return v
}

func isSHA(ref string) bool {
	if len(ref) != 40 {
		return false
	}
	for _, c := range ref {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// settingsPending reports whether a setting changed after the last apply.
func settingsPending(pc *plugsContext, p solution.EffectivePlug) bool {
	if !p.On {
		return false
	}
	if p.Backend != nil {
		_, red := render.PlugConfigs(pc.cfg, p.Plug, pc.secrets[p.ID])
		if ap, ok := applied(pc.paths, p.Backend.Name); ok && !reflect.DeepEqual(normalize(ap), normalize(red)) {
			return true
		}
	}
	if p.Frontend != nil && pc.cur != nil {
		rows := plugcfg.Desired(pc.cfg, []solution.EffectivePlug{p}, pc.cur)
		st := plugcfg.ReadState(pc.paths)
		for _, r := range rows {
			if h, ok := st.Managed[r.Slug]; ok && h != r.Hash() {
				return true
			}
		}
	}
	return false
}

// applied returns the redacted configs of a plug in rendered/care/additional-plugs.json (what runs).
func applied(p paths.Paths, name string) (map[string]any, bool) {
	data, err := os.ReadFile(p.Abs(paths.RenderedCare + "/additional-plugs.json"))
	if err != nil {
		return nil, false
	}
	var list []render.AdditionalPlug
	if json.Unmarshal(data, &list) != nil {
		return nil, false
	}
	for _, e := range list {
		if e.Name == name {
			return e.Configs, true
		}
	}
	return nil, false
}

// normalize makes two JSON-like values comparable (numbers and nested types).
func normalize(v any) any {
	data, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(data, &out)
	return out
}

func settingsView(cfg *config.Config, p solution.Plug, sec map[string]any) ([]PlugSetting, bool) {
	out := []PlugSetting{}
	complete := true
	var op map[string]map[string]any
	if ch, ok := cfg.Solution.Plugs[p.ID]; ok && ch.Settings != nil {
		op = map[string]map[string]any{solution.PartBackend: ch.Settings.Backend, solution.PartFrontend: ch.Settings.Frontend}
	}
	for _, s := range p.TypedSettings() {
		v := PlugSetting{Setting: s, Source: "default"}
		if s.Type == solution.TypeSecret {
			set := sec[s.Key] != nil && !solution.Empty(sec[s.Key])
			v.SecretSet = &set
			if set {
				v.Source = "operator"
			}
			if s.Required && !set {
				complete = false
			}
			out = append(out, v)
			continue
		}
		if x, ok := op[s.Part][s.Key]; ok && x != nil {
			v.Value, v.Source = x, "operator"
		} else if s.Default != nil {
			v.Value = s.Default
		} else if s.Part == solution.PartBackend && p.Backend != nil {
			v.Value = p.Backend.Configs[s.Key]
		} else if s.Part == solution.PartFrontend && p.Frontend != nil {
			v.Value, _ = solution.GetPath(p.Frontend.Meta, s.Key)
		}
		if s.Required && solution.Empty(v.Value) {
			complete = false
		}
		out = append(out, v)
	}
	return out, complete
}

// SettingsSchema returns GET /plugs/{id}/settings-schema.
func (u *Updater) SettingsSchema(id string) (map[string]any, error) {
	cfg, err := u.Config()
	if err != nil {
		return nil, problem.New(problem.InvalidConfig, err.Error())
	}
	pc, err := u.plugsContext(cfg)
	if err != nil {
		return nil, err
	}
	for _, p := range pc.eff {
		if p.ID == id {
			return solution.SettingsSchema(p.Plug), nil
		}
	}
	return nil, plugUnknown(cfg.Solution.Name, id)
}

func plugUnknown(sol, id string) error {
	return problem.Newf(problem.PlugUnknown, "%s has no plug %s. Add a plug with solution.custom.plugs.", sol, id)
}

// ChangeOutcome is the result of PlanChange: the new choice, the secret file, and the plan.
type ChangeOutcome struct {
	Choice     config.PlugChoice
	Secrets    map[string]any
	SecretsSet bool
	Plan       ChangePlan
	// After is the plug view with the change.
	After Plug
	// NewConfig is config.yaml with the change.
	NewConfig *config.Config
}

// PlanChange validates a plug change and returns the plan (docs/solutions.md, section 12.1).
func (u *Updater) PlanChange(ctx context.Context, cfg *config.Config, id string, ch PlugChange) (*ChangeOutcome, error) {
	pc, err := u.plugsContext(cfg)
	if err != nil {
		return nil, err
	}
	var before *solution.EffectivePlug
	for i := range pc.eff {
		if pc.eff[i].ID == id {
			before = &pc.eff[i]
		}
	}
	if before == nil {
		return nil, plugUnknown(cfg.Solution.Name, id)
	}
	choice := cfg.Solution.Plugs[id]
	if ch.SetEnabled {
		choice.Enabled = ch.Enabled
	}
	sec := map[string]any{}
	for k, v := range pc.secrets[id] {
		sec[k] = v
	}
	var errs []problem.FieldError
	settings := before.TypedSettings()
	find := func(part, key string) *solution.Setting {
		for i := range settings {
			if settings[i].Part == part && settings[i].Key == key {
				return &settings[i]
			}
		}
		return nil
	}
	secretsSet := false
	for part, vals := range map[string]map[string]any{solution.PartBackend: ch.Backend, solution.PartFrontend: ch.Frontend} {
		for _, key := range sortedKeys(vals) {
			val := vals[key]
			s := find(part, key)
			if s == nil {
				errs = append(errs, problem.FieldError{Pointer: solution.Pointer(part, key), Message: "the plug has no such setting", Code: "additionalProperties"})
				continue
			}
			if fe := solution.CheckValue(*s, val); fe != nil {
				errs = append(errs, problem.FieldError{Pointer: fe.Pointer, Message: fe.Message, Code: fe.Code})
				continue
			}
			if s.Type == solution.TypeSecret {
				secretsSet = true
				if val == nil {
					delete(sec, key)
				} else {
					sec[key] = val
				}
				continue
			}
			if choice.Settings == nil {
				choice.Settings = &config.PlugSettings{}
			}
			m := &choice.Settings.Backend
			if part == solution.PartFrontend {
				m = &choice.Settings.Frontend
			}
			if *m == nil {
				*m = map[string]any{}
			}
			if val == nil {
				delete(*m, key)
			} else {
				(*m)[key] = val
			}
		}
	}
	if choice.Settings != nil && len(choice.Settings.Backend) == 0 && len(choice.Settings.Frontend) == 0 {
		choice.Settings = nil
	}
	newCfg := *cfg
	newCfg.Solution.Plugs = map[string]config.PlugChoice{}
	for k, v := range cfg.Solution.Plugs {
		newCfg.Solution.Plugs[k] = v
	}
	if choice.Enabled == nil && choice.Settings == nil {
		delete(newCfg.Solution.Plugs, id)
	} else {
		newCfg.Solution.Plugs[id] = choice
	}
	if len(newCfg.Solution.Plugs) == 0 {
		newCfg.Solution.Plugs = nil
	}
	effAfter, err := pc.sol.Effective(&newCfg, pc.hosts)
	if err != nil {
		return nil, problem.New(problem.InvalidConfig, err.Error())
	}
	var after solution.EffectivePlug
	for _, p := range effAfter {
		if p.ID == id {
			after = p
		}
	}
	pcAfter := *pc
	pcAfter.cfg = &newCfg
	pcAfter.secrets = map[string]map[string]any{}
	for k, v := range pc.secrets {
		pcAfter.secrets[k] = v
	}
	pcAfter.secrets[id] = sec
	view := u.plugView(&pcAfter, after)
	if after.On && !view.SettingsComplete {
		for _, s := range view.Settings {
			if s.Required && ((s.SecretSet != nil && !*s.SecretSet) || (s.SecretSet == nil && solution.Empty(s.Value))) {
				errs = append(errs, problem.FieldError{Pointer: solution.Pointer(s.Part, s.Key), Message: "a value is required when the plug is on", Code: "required"})
			}
		}
	}
	if len(errs) > 0 {
		sort.Slice(errs, func(i, j int) bool { return errs[i].Pointer < errs[j].Pointer })
		return nil, problem.Newf(problem.PlugSettingsInvalid, "%d setting(s) are not valid.", len(errs)).WithErrors(errs...)
	}
	plan := planChange(pc, before, &after, cfg, &newCfg, pc.secrets[id], sec)
	return &ChangeOutcome{Choice: choice, Secrets: sec, SecretsSet: secretsSet, Plan: plan, After: view, NewConfig: &newCfg}, nil
}

// planChange computes the path of a plug change.
func planChange(pc *plugsContext, before, after *solution.EffectivePlug, cfg, newCfg *config.Config, secBefore, secAfter map[string]any) ChangePlan {
	p := ChangePlan{Path: PathNone, Restarts: []string{}, Downloads: []string{}, Builds: []string{}, Steps: []string{}, Warnings: []string{}}
	backendChanged := false
	if after.Backend != nil {
		a, _ := render.PlugConfigs(cfg, before.Plug, secBefore)
		b, _ := render.PlugConfigs(newCfg, after.Plug, secAfter)
		backendChanged = after.On && !reflect.DeepEqual(a, b)
	}
	frontendChanged := false
	if after.Frontend != nil && pc.cur != nil {
		ra := plugcfg.Desired(cfg, []solution.EffectivePlug{*before}, pc.cur)
		rb := plugcfg.Desired(newCfg, []solution.EffectivePlug{*after}, pc.cur)
		frontendChanged = !reflect.DeepEqual(ra, rb)
	}
	installed := false
	if pc.sl != nil {
		for _, id := range pc.sl.SolutionImage.InstalledPlugs {
			installed = installed || id == after.ID
		}
	} else if pc.cur != nil {
		if c := pc.cur.Components.Plugs[after.ID]; c.Backend != nil {
			installed = c.Backend.Installed
		}
	}
	onChanged := before.On != after.On
	cutoverSteps := []string{"render", "cutover-maintenance-on", "stop", "checkpoint", "activate", "migrate", "start", "plug-config", "verify", "write-lock", "maintenance-off"}
	switch {
	case after.Backend != nil && !before.On && after.On && installed:
		p.Path, p.Steps = PathCutover, cutoverSteps
	case after.Backend != nil && !before.On && after.On:
		p.Path, p.Steps = PathBuildAndCutover, append([]string{"stage-pull", "stage-build"}, cutoverSteps...)
		p.Builds = append(p.Builds, "localhost/care-pi/care")
	case after.Backend != nil && (before.On && !after.On || backendChanged):
		p.Path, p.Steps = PathRestart, []string{"render", "activate", "restart", "plug-config", "write-lock"}
	case after.Frontend != nil && (onChanged || frontendChanged):
		p.Path, p.Steps = PathPlugConfig, []string{"stage-bundles", "plug-config", "verify", "write-lock"}
	}
	p.Changed = p.Path != PathNone
	p.LockChanged = onChanged
	p.Maintenance = p.Path == PathCutover || p.Path == PathBuildAndCutover
	p.Checkpoint = p.Maintenance
	if p.Path == PathRestart || p.Maintenance {
		p.Restarts = []string{"care-beat.service", "care-worker.service", "care-api.service"}
	}
	d := downtime(p.Path)
	if p.Path == PathCutover || p.Path == PathBuildAndCutover {
		d = 120
	}
	p.EstimatedDowntimeS = &d
	if after.On && after.Frontend != nil && pc.cur != nil {
		if c := pc.cur.Components.Plugs[after.ID].Frontend; c != nil && c.Source == solution.FrontendBuilt && c.SHA != "" {
			if _, err := os.Stat(pc.pathsAbs(paths.PlugsDir + "/" + c.Slug + "/" + c.SHA[:12])); err != nil {
				p.Downloads = append(p.Downloads, c.URL)
			}
		}
	}
	p.NeedsInternet = len(p.Downloads) > 0 || len(p.Builds) > 0
	if after.On && after.RequiresInternet {
		p.Warnings = append(p.Warnings, after.Label+" needs internet access at run time.")
	}
	return p
}

func (pc *plugsContext) pathsAbs(p string) string { return pc.paths.Abs(p) }

// PlugChangeOptions controls RunPlugChange.
type PlugChangeOptions struct {
	ID     string
	Path   string
	Source string
	Reason string
	// Previous is solution.plugs.<id> and the secret file before PUT /plugs wrote them. A failed
	// cutover writes them back before its rollback. nil: no restore.
	Previous *PlugSnapshot
}

// PlugSnapshot is the operator choice and the secret file of one plug before a change.
type PlugSnapshot struct {
	// Choice is the value of solution.plugs.<id> in config.yaml; nil when the key was absent.
	Choice any
	// Secrets is the content of secrets/plug-<id>.json; nil when the file was absent.
	Secrets []byte
}

// restorePlug writes solution.plugs.<id> of the snapshot into config.yaml (the other keys and the
// comments stay) and puts the secret file back.
func (u *Updater) restorePlug(id string, snap *PlugSnapshot) error {
	path := u.configPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("config.yaml is not a mapping")
	}
	root := doc.Content[0]
	sol := mapValue(root, "solution")
	if sol == nil && snap.Choice != nil {
		sol = &yaml.Node{Kind: yaml.MappingNode}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "solution"}, sol)
	}
	if sol != nil {
		plugs := mapValue(sol, "plugs")
		if plugs == nil && snap.Choice != nil {
			plugs = &yaml.Node{Kind: yaml.MappingNode}
			sol.Content = append(sol.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "plugs"}, plugs)
		}
		if plugs != nil {
			deleteKey(plugs, id)
			if snap.Choice != nil {
				var v yaml.Node
				if err := v.Encode(snap.Choice); err != nil {
					return err
				}
				plugs.Content = append(plugs.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: id}, &v)
			}
			if len(plugs.Content) == 0 {
				deleteKey(sol, "plugs")
			}
		}
	}
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	_ = enc.Close()
	if _, err := config.Parse(b.Bytes(), "config.yaml"); err != nil {
		return err
	}
	if err := lock.WriteFileAtomic(path, b.Bytes(), 0o600); err != nil {
		return err
	}
	file := secrets.Store{Paths: u.Paths}.Path(secrets.PlugFile(id))
	if snap.Secrets == nil {
		if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	return lock.WriteFileAtomic(file, snap.Secrets, 0o600)
}

func deleteKey(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// RunPlugChange runs a plug-change job: the path of the plan with the config that is on disk now.
func (u *Updater) RunPlugChange(ctx context.Context, rec *jobs.Recorder, o PlugChangeOptions) (*PlugChangeJobResult, error) {
	started := u.now()
	cur, err := lock.Load(u.Paths)
	if err != nil {
		return nil, err
	}
	out := rec.Writer()
	ao := u.applyOptions(out)
	var pr *apply.Prepared
	if err := step(rec, "plan", o.Path, func() error {
		pr, err = u.Prepare(ctx, nil, false, out)
		return err
	}); err != nil {
		return nil, err
	}
	enabled := false
	if c, ok := pr.Lock.Components.Plugs[o.ID]; ok {
		enabled = c.Enabled
	}
	res := &PlugChangeJobResult{Kind: jobs.KindPlugChange, PlugID: o.ID, Enabled: enabled, Path: o.Path,
		LockChanged: !lock.Equal(cur, pr.Lock)}
	hist := HistoryEntry{JobID: rec.ID(), Kind: HistPlugChange, Source: o.Source, Reason: strp(o.Reason), StartedAt: started, PlugID: strp(o.ID)}
	if cur != nil {
		hist.FromVersion, hist.ToVersion = strp(cur.Solution.Version), strp(cur.Solution.Version)
	}
	cutover := o.Path == PathCutover || o.Path == PathBuildAndCutover
	restored := false
	// restore writes the choice before the change back, so that the rollback renders the previous plug set
	// and config.yaml matches the lock that runs again.
	restore := func() {
		if restored || o.Previous == nil || !cutover {
			return
		}
		restored = true
		if err := u.restorePlug(o.ID, o.Previous); err != nil {
			rec.Logf("warning: cannot write solution.plugs.%s of before the change back: %v", o.ID, err)
			return
		}
		rec.Logf("solution.plugs.%s and its secret file have their values of before the change again", o.ID)
	}
	finish := func(err error) (*PlugChangeJobResult, error) {
		if err != nil {
			restore()
		}
		hist.FinishedAt = u.now()
		if err != nil {
			hist.Outcome, hist.Problem = OutcomeFailed, errorsAs(err)
			if p := errorsAs(err); p != nil && p.PitrOffer != nil {
				hist.Outcome = OutcomeRolledBack
			}
			_ = AppendHistory(u.Paths, hist)
			return nil, err
		}
		hist.Outcome = OutcomeSucceeded
		_ = AppendHistory(u.Paths, hist)
		return res, nil
	}
	switch o.Path {
	case PathCutover, PathBuildAndCutover:
		if err := step(rec, "stage-pull", "", func() error {
			pulled, _, err := apply.EnsureImages(ctx, ao, pr)
			recordImages(u.Paths, pulled)
			return err
		}); err != nil {
			return finish(err)
		}
		if err := step(rec, "stage-bundles", "", func() error { _, _, err := u.stageBundles(ctx, pr, out); return err }); err != nil {
			return finish(err)
		}
		if err := step(rec, "stage-build", "", func() error { _, err := u.stageBuild(ctx, pr, out); return err }); err != nil {
			return finish(err)
		}
		id := TargetID(pr.Lock)
		if err := step(rec, "render", id, func() error { return writeStaging(u.Paths, id, pr) }); err != nil {
			return finish(err)
		}
		r, err := u.cutover(ctx, rec, cutoverSpec{kind: jobs.KindPlugChange, target: pr, targetID: id, prev: cur, path: o.Path,
			beforeRollback: restore, lockPlugs: true})
		res.Checkpoint, hist.Checkpoint, hist.PitrOffer = r.checkpoint, r.checkpoint, r.offer
		d := r.downtime.Seconds()
		res.DowntimeS, hist.DowntimeS = &d, &d
		if err == nil {
			hist.FromLock = strp(r.fromLock)
		}
		return finish(err)
	case PathRestart:
		t0 := u.now()
		if err := step(rec, "activate", "", func() error { _, err := apply.Activate(ctx, ao, pr); return err }); err != nil {
			return finish(err)
		}
		if err := step(rec, "restart", "care-beat.service care-worker.service care-api.service", func() error {
			return u.Host.Systemd.Restart(ctx, apply.AppUnits...)
		}); err != nil {
			return finish(err)
		}
		d := u.now().Sub(t0).Seconds()
		res.DowntimeS, hist.DowntimeS = &d, &d
	default:
		if err := step(rec, "stage-bundles", "", func() error { _, _, err := u.stageBundles(ctx, pr, out); return err }); err != nil {
			return finish(err)
		}
		if _, err := apply.Activate(ctx, ao, pr); err != nil {
			return finish(err)
		}
	}
	rows := plugcfg.Desired(pr.Config, pr.Desire.Plugs, pr.Lock)
	if err := step(rec, "plug-config", fmt.Sprintf("%d row(s)", len(rows)), func() error {
		_, err := plugcfg.Sync(ctx, u.Host, u.Paths, rows, u.now())
		return err
	}); err != nil {
		return finish(err)
	}
	if res.LockChanged {
		if err := step(rec, "write-lock", "", func() error {
			pr.Lock.GeneratedAt = u.now().Format(time.RFC3339)
			if err := lock.Write(u.Paths, pr.Lock, u.now()); err != nil {
				return err
			}
			hist.FromLock = strp(pr.Lock.History.PreviousID())
			return nil
		}); err != nil {
			return finish(err)
		}
	}
	return finish(nil)
}
