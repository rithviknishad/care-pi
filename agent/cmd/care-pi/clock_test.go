package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/host"
)

func TestClockPassedReloadsCaddy(t *testing.T) {
	reload := "podman exec care-caddy " + strings.Join(caddyReload, " ")
	for name, c := range map[string]struct {
		fail bool
		want []string
	}{
		"reload":   {false, []string{reload}},
		"fallback": {true, []string{reload, "systemctl try-restart care-caddy.service"}},
	} {
		t.Run(name, func(t *testing.T) {
			flag := filepath.Join(t.TempDir(), "clock-wait")
			if err := os.WriteFile(flag, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			fake := host.NewFake()
			if c.fail {
				fake.On(reload, host.Response{Code: 125})
			}
			var out bytes.Buffer
			clockPassed(context.Background(), host.New(fake), flag, &out)
			if !reflect.DeepEqual(fake.Calls, c.want) {
				t.Fatalf("commands:\n got  %q\n want %q", fake.Calls, c.want)
			}
			if _, err := os.Stat(flag); !os.IsNotExist(err) {
				t.Fatalf("the clock-wait flag still exists: %v", err)
			}
		})
	}
}
