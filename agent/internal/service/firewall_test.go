package service

import (
	"reflect"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/rithviknishad/care-pi/agent/internal/config"
)

// S64: the edits of "care-pi firewall allow add|remove" on a config document (view file).
func TestAllowPortEdits(t *testing.T) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte("version: 1\nsecurity:\n  firewall:\n    policy: drop\n    allow_ports:\n      - {port: 9100, iface: eth0}\n"), &doc); err != nil {
		t.Fatal(err)
	}
	if AddAllowPort(doc, config.AllowPort{Port: 9100, Proto: "tcp", Iface: "eth0"}) {
		t.Fatal("the same entry (proto tcp by default) changed the document")
	}
	if !AddAllowPort(doc, config.AllowPort{Port: 9100, Iface: "eth0", Comment: "node exporter"}) {
		t.Fatal("a new comment did not change the document")
	}
	if !AddAllowPort(doc, config.AllowPort{Port: 9100, Iface: "tailscale0"}) || !AddAllowPort(doc, config.AllowPort{Port: 9256, Proto: "udp"}) {
		t.Fatal("new entries did not change the document")
	}
	want := []any{
		map[string]any{"port": 9100, "iface": "eth0", "comment": "node exporter"},
		map[string]any{"port": 9100, "proto": "tcp", "iface": "tailscale0"},
		map[string]any{"port": 9256, "proto": "udp"},
	}
	if got := allowPortsOf(doc); !reflect.DeepEqual(got, want) {
		t.Fatalf("allow_ports\n got  %v\n want %v", got, want)
	}
	data, err := marshalConfig(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.Parse(data, "edited"); err != nil {
		t.Fatalf("%v\n%s", err, data)
	}
	if n := RemoveAllowPort(doc, 9256, "tcp", ""); n != 0 {
		t.Fatalf("9256/tcp removed %d", n)
	}
	if n := RemoveAllowPort(doc, 9100, "tcp", "tailscale0"); n != 1 || len(allowPortsOf(doc)) != 2 {
		t.Fatalf("9100/tcp on tailscale0 removed %d: %v", n, allowPortsOf(doc))
	}
	if n := RemoveAllowPort(doc, 9100, "tcp", ""); n != 1 {
		t.Fatalf("9100/tcp removed %d", n)
	}
	if n := RemoveAllowPort(doc, 9256, "udp", ""); n != 1 {
		t.Fatalf("9256/udp removed %d", n)
	}
	fw := section(section(doc, "security"), "firewall")
	if _, ok := fw["allow_ports"]; ok || fw["policy"] != "drop" {
		t.Fatalf("an empty list removes the key: %v", fw)
	}
}
