// Package plugcfg writes the plug_config rows of Care for the FE plugs (docs/solutions.md, section 12.2).
//
// The agent runs a fixed script in "python manage.py shell" in care-api: the script validates each row
// with PlugConfigSerializer, saves it, deletes the managed rows of plugs that are off, and deletes the
// cache key of the list, as the viewset of Care does. The agent manages only the slugs of the FE plugs
// of the effective plug set; state/plug-config.json records them.
package plugcfg

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
)

// Row is one plug_config row.
type Row struct {
	Slug string         `json:"slug"`
	Meta map[string]any `json:"meta"`
}

// Hash returns the SHA-256 of the canonical JSON of the meta.
func (r Row) Hash() string {
	js, _ := json.Marshal(r.Meta)
	sum := sha256.Sum256(js)
	return hex.EncodeToString(sum[:])
}

// MetaURL returns meta.url of the row.
func (r Row) MetaURL() string {
	s, _ := r.Meta["url"].(string)
	return s
}

// Desired returns the rows of the FE plugs that are on: frontend.meta of the solution with the frontend
// settings at their paths (operator value, else the default), and url (the meta_url of the on-device lock).
func Desired(cfg *config.Config, plugs []solution.EffectivePlug, lk *lock.Lock) []Row {
	var out []Row
	for _, p := range plugs {
		if !p.On || p.Frontend == nil {
			continue
		}
		c, ok := lk.Components.Plugs[p.ID]
		if !ok || c.Frontend == nil || c.Frontend.MetaURL == "" {
			continue
		}
		meta := map[string]any{}
		if p.Frontend.Meta != nil {
			meta, _ = solution.DeepCopy(p.Frontend.Meta).(map[string]any)
		}
		var op map[string]any
		if ch, ok := cfg.Solution.Plugs[p.ID]; ok && ch.Settings != nil {
			op = ch.Settings.Frontend
		}
		for _, s := range p.TypedSettings() {
			if s.Part != solution.PartFrontend {
				continue
			}
			if v, ok := op[s.Key]; ok && v != nil {
				solution.SetPath(meta, s.Key, solution.DeepCopy(v))
			} else if s.Default != nil {
				solution.SetPath(meta, s.Key, solution.DeepCopy(s.Default))
			}
		}
		meta["url"] = c.Frontend.MetaURL
		out = append(out, Row{Slug: p.Frontend.Slug, Meta: meta})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}

// State is state/plug-config.json: the managed slugs with the SHA-256 of their meta.
type State struct {
	Managed   map[string]string `json:"managed"`
	SyncedAt  *time.Time        `json:"synced_at"`
	CheckedAt *time.Time        `json:"checked_at"`
	// Seen holds meta.url of each row that the last sync read back from Care.
	Seen map[string]string `json:"seen,omitempty"`
}

// ReadState reads state/plug-config.json. A missing file gives an empty state.
func ReadState(p paths.Paths) State {
	st := State{Managed: map[string]string{}}
	data, err := os.ReadFile(p.Abs(paths.PlugConfigState))
	if err == nil {
		_ = json.Unmarshal(data, &st)
	}
	if st.Managed == nil {
		st.Managed = map[string]string{}
	}
	return st
}

// WriteState writes state/plug-config.json.
func WriteState(p paths.Paths, st State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return lock.WriteFileAtomic(p.Abs(paths.PlugConfigState), data, 0o600)
}

// NeedsSync reports whether the rows differ from the last sync.
func NeedsSync(p paths.Paths, rows []Row) bool {
	if _, err := os.Stat(p.Abs(paths.PlugConfigState)); errors.Is(err, fs.ErrNotExist) {
		return len(rows) > 0
	}
	st := ReadState(p)
	want := map[string]string{}
	for _, r := range rows {
		want[r.Slug] = r.Hash()
	}
	if len(want) != len(st.Managed) {
		return true
	}
	for k, v := range want {
		if st.Managed[k] != v {
			return true
		}
	}
	return false
}

const marker = host.PlugConfigMarker

// script returns the Python script for "manage.py shell". The rows go in as base64 JSON, so no
// value of a setting can change the script.
func script(upsert []Row, del []string) string {
	payload, _ := json.Marshal(map[string]any{"upsert": upsert, "delete": del})
	return `import base64, json
from django.core.cache import cache
from care.users.models import PlugConfig
from care.users.api.serializers.plug_config import PlugConfigSerializer
data = json.loads(base64.b64decode("` + base64.StdEncoding.EncodeToString(payload) + `").decode())
for row in data["upsert"]:
    obj = PlugConfig.objects.filter(slug=row["slug"]).first()
    ser = PlugConfigSerializer(obj, data=row) if obj else PlugConfigSerializer(data=row)
    ser.is_valid(raise_exception=True)
    ser.save()
for slug in data["delete"]:
    PlugConfig.objects.filter(slug=slug).delete()
cache.delete("care_plug_viewset_list")
print("` + marker + `" + json.dumps([{"slug": c.slug, "meta": c.meta} for c in PlugConfig.objects.all().order_by("slug")]))
`
}

// ShellCmd is the command that runs the script.
var ShellCmd = []string{"exec", "-i", "care-api", "python", "manage.py", "shell"}

// Result tells what a sync did.
type Result struct {
	Written []string
	Deleted []string
	// Rows are the rows in Care after the sync.
	Rows []Row
}

// Sync writes the rows and deletes the managed rows of plugs that are off. It records the state.
func Sync(ctx context.Context, h *host.Host, p paths.Paths, rows []Row, now time.Time) (*Result, error) {
	st := ReadState(p)
	want := map[string]bool{}
	for _, r := range rows {
		want[r.Slug] = true
	}
	var del []string
	for slug := range st.Managed {
		if !want[slug] {
			del = append(del, slug)
		}
	}
	sort.Strings(del)
	if rows == nil {
		rows = []Row{}
	}
	if del == nil {
		del = []string{}
	}
	out, err := h.Runner.Run(ctx, host.Cmd{Name: "podman", Args: ShellCmd, Stdin: strings.NewReader(script(rows, del))})
	if err != nil {
		return nil, fmt.Errorf("cannot write the plug_config rows in care-api: %w", err)
	}
	got, err := parseRows(out)
	if err != nil {
		return nil, err
	}
	res := &Result{Deleted: del, Rows: got}
	st.Managed = map[string]string{}
	st.Seen = map[string]string{}
	for _, r := range rows {
		res.Written = append(res.Written, r.Slug)
		st.Managed[r.Slug] = r.Hash()
	}
	for _, r := range got {
		if want[r.Slug] {
			st.Seen[r.Slug] = r.MetaURL()
		}
	}
	t := now.UTC().Truncate(time.Second)
	st.SyncedAt, st.CheckedAt = &t, &t
	if err := WriteState(p, st); err != nil {
		return res, err
	}
	for _, r := range rows {
		if st.Seen[r.Slug] != r.MetaURL() {
			return res, fmt.Errorf("the plug_config row %s has meta.url %q after the write, not %q", r.Slug, st.Seen[r.Slug], r.MetaURL())
		}
	}
	return res, nil
}

func parseRows(out []byte) ([]Row, error) {
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, marker); ok {
			var rows []Row
			if err := json.Unmarshal([]byte(rest), &rows); err != nil {
				return nil, fmt.Errorf("cannot read the plug_config rows: %w", err)
			}
			return rows, nil
		}
	}
	return nil, errors.New("the plug_config script printed no rows; see the journal of care-api")
}

// FakeOutput returns the output of the script for the rows, for tests and the dev host.
func FakeOutput(rows []Row) string {
	if rows == nil {
		rows = []Row{}
	}
	js, _ := json.Marshal(rows)
	return marker + string(js) + "\n"
}
