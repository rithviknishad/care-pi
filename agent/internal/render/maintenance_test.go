package render

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/jobs"
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

// TestMaintenancePageRestart: a restart of care-api behind the page (S62, the step restart) shows the restart text,
// not the sentence of the console for the step restart.
func TestMaintenancePageRestart(t *testing.T) {
	out, err := renderOne("status-maintenance.html.tmpl", &Model{Header: Header, DisplayName: "Sunrise Clinic",
		MaintenanceSteps: MaintenanceSteps, MaintenanceRestart: MaintenanceRestart})
	if err != nil {
		t.Fatal(err)
	}
	page := string(out)
	if MaintenanceRestart != "CARE restarts. This takes about 1 minute." {
		t.Fatalf("restart text %q", MaintenanceRestart)
	}
	for _, want := range []string{"var restart = " + strconv.Quote(MaintenanceRestart) + ";", `(m.step === "restart" && restart)`} {
		if !strings.Contains(page, want) {
			t.Errorf("the page has no %s", want)
		}
	}
}

// consoleJobTitles reads strings.jobs.kinds of console/src/lib/strings.ts: one `key: "title",` on each line.
func consoleJobTitles(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile(consoleStrings)
	if err != nil {
		t.Fatalf("read the console strings: %v", err)
	}
	src := string(data)
	i := strings.Index(src, "\n  jobs: {\n")
	if i < 0 {
		t.Fatalf("%s: no strings.jobs block", consoleStrings)
	}
	src = src[i:]
	const open = "\n    kinds: {\n"
	j := strings.Index(src, open)
	if j < 0 {
		t.Fatalf("%s: no strings.jobs.kinds block", consoleStrings)
	}
	body := src[j+len(open):]
	k := strings.Index(body, "\n    } as Record<string, string>,")
	if k < 0 {
		t.Fatalf("%s: strings.jobs.kinds has no end", consoleStrings)
	}
	out := map[string]string{}
	for _, line := range strings.Split(body[:k], "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		m := consoleEntry.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("%s: cannot read the line %q of strings.jobs.kinds", consoleStrings, line)
		}
		v, err := strconv.Unquote(`"` + m[3] + `"`)
		if err != nil {
			t.Fatalf("%s: %q: %v", consoleStrings, line, err)
		}
		out[m[1]+m[2]] = v
	}
	return out
}

// TestJobTitlesMatchConsole: GET /_care-pi/status and the status pages give the title of a job kind that the console
// shows (S67), and each kind of the contract has a title.
func TestJobTitlesMatchConsole(t *testing.T) {
	console := consoleJobTitles(t)
	if len(console) < 20 {
		t.Fatalf("only %d titles in the console table: the parser is wrong", len(console))
	}
	for k, v := range console {
		if got := JobTitles[k]; got != v {
			t.Errorf("render.JobTitles[%q] = %q, the console has %q", k, got, v)
		}
	}
	for k := range JobTitles {
		if _, ok := console[k]; !ok {
			t.Errorf("render.JobTitles has %q, the console has not", k)
		}
	}
	for _, k := range jobs.Kinds {
		if JobTitles[k] == "" {
			t.Errorf("no title for the job kind %q", k)
		}
		if JobTitle(k) != JobTitles[k] {
			t.Errorf("JobTitle(%q) = %q", k, JobTitle(k))
		}
	}
	if JobTitle("unknown-kind") != "unknown-kind" {
		t.Error("an unknown kind must show as it is")
	}
	for k := range JobNext {
		if k != "" && JobTitles[k] == "" {
			t.Errorf("render.JobNext has the unknown kind %q", k)
		}
	}
}

// TestStatusPagesJob: the maintenance and setup pages open the public job stream of the job of the public status, show
// the steps and the progress, link to the job in the console, and have "Copy status" (S67, S68). The pages load no
// other file: each style and script is in the page.
func TestStatusPagesJob(t *testing.T) {
	m := &Model{Header: Header, DisplayName: "Sunrise <Clinic>", MaintenanceSteps: MaintenanceSteps,
		MaintenanceRestart: MaintenanceRestart, JobTitles: JobTitles, JobNext: JobNext, ConsolePort: 8443, Canonical: "care.local",
		Sites: []string{"care.local", "192.168.1.10"}}
	for _, name := range []string{"status-maintenance.html.tmpl", "status-setup.html.tmpl"} {
		out, err := renderOne(name, m)
		if err != nil {
			t.Fatal(err)
		}
		page := string(out)
		for _, want := range []string{
			`new EventSource("/_care-pi/jobs/" + encodeURIComponent(job.id) + "/events")`,
			`"/_care-pi/jobs/" + encodeURIComponent(id) + "/events?after=" + last`,
			`fetch("/_care-pi/status"`,
			"carePiJob.update(s);",
			`<button type="button" id="copy">Copy status</button>`,
			`"https://" + location.hostname + ":" + port + "/jobs/" + encodeURIComponent(job.id)`,
			`id="job-progress" class="bar" role="progressbar"`,
			`var port = 8443;`,
			`"update-apply":"Install an update"`,
			`"setup":"When the setup is complete, this page opens CARE.`,
			`Sunrise &lt;Clinic&gt;`,
		} {
			if !strings.Contains(page, want) {
				t.Errorf("%s has no %s", name, want)
			}
		}
		for _, bad := range []string{`<script src`, `<link `, `http-equiv="refresh" content="15">` + "\n<style>", "@import", "url("} {
			if strings.Contains(page, bad) {
				t.Errorf("%s loads another file or reloads itself: %q", name, bad)
			}
		}
	}
}
