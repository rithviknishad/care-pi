package lock

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// TestMigrateV1: a version 1 lock (the Phase 1 golden lock) reads as version 2 (docs/solutions.md, section 6.3).
func TestMigrateV1(t *testing.T) {
	data, err := os.ReadFile("testdata/v1-custom.lock.yaml")
	if err != nil {
		t.Fatal(err)
	}
	l, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if l.FileVersion != 1 || l.Version != FileVersion {
		t.Fatalf("versions: file %d, lock %d", l.FileVersion, l.Version)
	}
	if l.Solution.Source != SourceCustom || l.Solution.Version != "" || l.Solution.LockSHA256 != "" {
		t.Fatalf("solution %+v", l.Solution)
	}
	c := l.Components
	if c.Care == nil || c.Care.Image != "ghcr.io/rithviknishad/care-pi/care" || c.Care.Ref != "care-clinic-latest" || !c.Care.Tracked || c.Care.Source != SourceSolution || c.Care.GitSHA != "" {
		t.Fatalf("care %+v", c.Care)
	}
	if c.CareFE == nil || c.CareFE.Ref != "v25.2.0" || c.CareFE.SHA == "" || !c.CareFE.Tracked {
		t.Fatalf("care_fe %+v", c.CareFE)
	}
	p := c.Plugs["care_filly"]
	if !p.Enabled || p.Reason != SourceSolution || p.Backend == nil || !p.Backend.Installed || p.Backend.Tracked || p.Backend.SHA != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("plug %+v backend %+v", p, p.Backend)
	}
	// The resolver form stays: Git and Plugs.
	if l.Git["care_fe"].SHA == "" || l.Plugs["care_filly"].SHA == "" {
		t.Fatalf("git %+v plugs %+v", l.Git, l.Plugs)
	}
	out, err := l.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"version: 2\n", "  source: custom\n", "components:\n", "history:\n", "  previous: null\n"} {
		if !strings.Contains(s, want) {
			t.Fatalf("version 2 lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "\ngit:") || strings.Contains(s, "\nplugs:") {
		t.Fatalf("version 2 has the version 1 members:\n%s", s)
	}
	// Read back: the same identities, file version 2.
	l2, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if l2.FileVersion != 2 || !sameIdentity(l, l2, false) || l2.Plugs["care_filly"].SHA != l.Plugs["care_filly"].SHA {
		t.Fatalf("round trip %+v", l2)
	}
	if Equal(l, l2) {
		t.Fatal("a version 1 file must not equal a version 2 lock: apply then writes version 2")
	}
}

// TestHistoryPrevious: Write keeps the previous lock in state/locks and points history.previous at it;
// a change of the FE record only keeps history.previous. Load fills it for a version 1 file.
func TestHistoryPrevious(t *testing.T) {
	p := paths.New(t.TempDir())
	v1, _ := os.ReadFile("testdata/v1-busy-counter.lock.yaml")
	for _, d := range []string{paths.LockHistory, paths.EtcDir} {
		if err := os.MkdirAll(p.Abs(d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(p.Abs(paths.LockHistory+"/20261001T000000Z.yaml"), v1, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Abs(paths.LockFile), v1, 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if l.History.Previous != "20261001T000000Z.yaml" {
		t.Fatalf("previous of a version 1 file: %q", l.History.Previous)
	}
	next := *l
	next.Images = map[string]Image{}
	for k, v := range l.Images {
		next.Images[k] = v
	}
	img := next.Images["care"]
	img.Digest = "sha256:" + strings.Repeat("1", 64)
	next.Images["care"] = img
	next.Components = Components{}
	t1 := time.Date(2026, 11, 22, 2, 7, 41, 0, time.UTC)
	if err := Write(p, &next, t1); err != nil {
		t.Fatal(err)
	}
	if next.History.Previous != "20261122T020741Z.yaml" {
		t.Fatalf("previous after an update: %q", next.History.Previous)
	}
	if _, err := os.Stat(filepath.Join(p.Abs(paths.LockHistory), "20261122T020741Z.yaml")); err != nil {
		t.Fatal(err)
	}
	fe := next
	fe.FE.URL = "https://example.org/fe.tar.gz"
	if err := Write(p, &fe, t1.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if fe.History.Previous != "20261122T020741Z.yaml" {
		t.Fatalf("an FE record change moved history.previous to %q", fe.History.Previous)
	}
	if ids := HistoryIDs(p); len(ids) != 3 || ids[0] != "20261122T020841Z" {
		t.Fatalf("history ids %v", ids)
	}
	got, err := ReadHistory(p, "20261122T020741Z")
	if err != nil || got.FileVersion != 1 {
		t.Fatalf("read history: %v %+v", err, got)
	}
	if _, err := ReadHistory(p, "../lock"); err == nil {
		t.Fatal("a bad lock id must fail")
	}
}
