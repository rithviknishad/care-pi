package render

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// consoleStrings is the string table of the console. The test reads it; it never changes it.
const consoleStrings = "../../../console/src/lib/strings.ts"

var consoleEntry = regexp.MustCompile(`^(?:"([^"]+)"|([A-Za-z][\w-]*))\s*:\s*"((?:[^"\\]|\\.)*)",?$`)

// consoleStepSentences reads strings.steps.actions of console/src/lib/strings.ts: one `key: "sentence",` on each line.
func consoleStepSentences(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile(consoleStrings)
	if err != nil {
		t.Fatalf("read the console strings: %v", err)
	}
	src := string(data)
	const open = "\n  steps: {\n    actions: {\n"
	i := strings.Index(src, open)
	if i < 0 {
		t.Fatalf("%s: no strings.steps.actions block", consoleStrings)
	}
	body := src[i+len(open):]
	j := strings.Index(body, "\n    } as Record<string, string>,")
	if j < 0 {
		t.Fatalf("%s: strings.steps.actions has no end", consoleStrings)
	}
	out := map[string]string{}
	for _, line := range strings.Split(body[:j], "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		m := consoleEntry.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("%s: cannot read the line %q of strings.steps.actions (use one `key: \"sentence\",` on each line)", consoleStrings, line)
		}
		key := m[1] + m[2]
		v, err := strconv.Unquote(`"` + m[3] + `"`)
		if err != nil {
			t.Fatalf("%s: %q: %v", consoleStrings, line, err)
		}
		out[key] = v
	}
	return out
}

// TestMaintenanceStepsMatchConsole: the maintenance page of Caddy shows the same sentence for each step as the
// console (docs/api/README.md section 14.3, docs/console-ui.md section 9).
func TestMaintenanceStepsMatchConsole(t *testing.T) {
	console := consoleStepSentences(t)
	if len(console) < 40 {
		t.Fatalf("only %d sentences in the console table: the parser is wrong", len(console))
	}
	for k, v := range console {
		if got, ok := MaintenanceSteps[k]; !ok {
			t.Errorf("render.MaintenanceSteps has no %q (console: %q)", k, v)
		} else if got != v {
			t.Errorf("render.MaintenanceSteps[%q] = %q, the console has %q", k, got, v)
		}
	}
	for k := range MaintenanceSteps {
		if _, ok := console[k]; !ok {
			t.Errorf("render.MaintenanceSteps has %q, the console has not", k)
		}
	}
	// The cutover steps of the update jobs must have a sentence.
	for _, k := range []string{"cutover-maintenance-on", "stop", "checkpoint", "activate", "migrate", "start", "plug-config",
		"verify", "write-lock", "maintenance-off", "rollback", "cutover", "restart", "full-cycle", "os-upgrade", "reboot"} {
		if MaintenanceSteps[k] == "" {
			t.Errorf("no sentence for the step %q", k)
		}
	}
}

// TestMaintenancePageSteps: the rendered page has the sentence of each step in its script.
func TestMaintenancePageSteps(t *testing.T) {
	out, err := renderOne("status-maintenance.html.tmpl", &Model{Header: Header, DisplayName: "Sunrise Clinic", MaintenanceSteps: MaintenanceSteps})
	if err != nil {
		t.Fatal(err)
	}
	page := string(out)
	for _, k := range []string{"migrate", "checkpoint", "rollback"} {
		if want := strconv.Quote(k) + ": " + strconv.Quote(MaintenanceSteps[k]) + ","; !strings.Contains(page, want) {
			t.Errorf("the page has no %s", want)
		}
	}
	if strings.Contains(page, "(step: ") {
		t.Error("the page shows the raw step key")
	}
}
