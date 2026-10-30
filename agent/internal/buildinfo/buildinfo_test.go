package buildinfo

import (
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	for name, value := range map[string]string{
		"Version": Version,
		"Commit":  Commit,
		"Date":    Date,
	} {
		if value == "" {
			t.Errorf("%s is empty", name)
		}
	}
}

func TestString(t *testing.T) {
	got := String()
	for _, want := range []string{"version=" + Version, "commit=" + Commit, "date=" + Date} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, want it to contain %q", got, want)
		}
	}
}
