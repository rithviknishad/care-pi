package secrets

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// PlugFile returns the name of the secret file of a plug: plug-<id>.json (docs/solutions.md, section 1.4).
func PlugFile(id string) string { return paths.PlugSecretPrefix + id + ".json" }

// ReadPlug returns the secret settings of a plug by key. A missing file gives an empty map.
func (s Store) ReadPlug(id string) (map[string]any, error) {
	data, err := os.ReadFile(s.Path(PlugFile(id)))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("cannot read %s: %w", paths.Secret(PlugFile(id)), err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("%s is not a JSON object: %w", paths.Secret(PlugFile(id)), err)
	}
	return out, nil
}

// WritePlug writes the secret settings of a plug (root, 0600). An empty map deletes the file.
func (s Store) WritePlug(id string, values map[string]any) error {
	path := s.Path(PlugFile(id))
	if len(values) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// PlugIDs returns the ids of the plugs with a secret file.
func (s Store) PlugIDs() []string {
	ents, err := os.ReadDir(s.Paths.Abs(paths.SecretsDir))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		name := e.Name()
		if strings.HasPrefix(name, paths.PlugSecretPrefix) && strings.HasSuffix(name, ".json") {
			out = append(out, strings.TrimSuffix(strings.TrimPrefix(name, paths.PlugSecretPrefix), ".json"))
		}
	}
	sort.Strings(out)
	return out
}

// AllPlugs returns the secret settings of each plug with a secret file.
func (s Store) AllPlugs() (map[string]map[string]any, error) {
	out := map[string]map[string]any{}
	for _, id := range s.PlugIDs() {
		v, err := s.ReadPlug(id)
		if err != nil {
			return nil, err
		}
		out[id] = v
	}
	return out, nil
}
