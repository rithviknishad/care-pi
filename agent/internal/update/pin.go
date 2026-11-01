package update

import (
	"bytes"
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
)

// setPin writes solution.pin into config.yaml and keeps the comments (POST /updates/apply with a
// version on the track pinned, docs/solutions.md section 7.2).
func setPin(path, version string) error {
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
	if sol == nil {
		sol = &yaml.Node{Kind: yaml.MappingNode}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "solution"}, sol)
	}
	pin := mapValue(sol, "pin")
	if pin == nil {
		sol.Content = append(sol.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "pin"}, &yaml.Node{Kind: yaml.ScalarNode, Value: version, Style: yaml.DoubleQuotedStyle})
	} else {
		pin.Kind, pin.Value, pin.Style, pin.Tag = yaml.ScalarNode, version, yaml.DoubleQuotedStyle, ""
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
	return lock.WriteFileAtomic(path, b.Bytes(), 0o600)
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}
