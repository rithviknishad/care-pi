package paths

import "testing"

func TestAbsAndHost(t *testing.T) {
	p := New("/tmp/root")
	if got := p.Abs(ConfigFile); got != "/tmp/root/etc/care-pi/config.yaml" {
		t.Fatalf("Abs = %s", got)
	}
	if got := p.Host("/tmp/root/etc/care-pi/config.yaml"); got != ConfigFile {
		t.Fatalf("Host = %s", got)
	}
	if got := New("").Abs(ConfigFile); got != ConfigFile {
		t.Fatalf("root / Abs = %s", got)
	}
}

func TestLayoutParentsFirst(t *testing.T) {
	seen := map[string]bool{"/": true, "/etc": true, "/var/lib": true, "/etc/containers": true, "/etc/systemd": true}
	for _, d := range Layout() {
		parent := d.Path[:lastSlash(d.Path)]
		if parent == "" {
			parent = "/"
		}
		if !seen[parent] {
			t.Errorf("%s comes before its parent %s", d.Path, parent)
		}
		seen[d.Path] = true
	}
}

func lastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return 0
}
