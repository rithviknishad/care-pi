package update

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
	"github.com/rithviknishad/care-pi/agent/internal/update/updatetest"
)

// localDist makes the dist snapshot of the Raspberry Pi image in /var/lib/care-pi-dist of the test root.
func localDist(t *testing.T, p paths.Paths) *updatetest.Fixture {
	t.Helper()
	data, err := config.DefaultYAML("Sunrise Clinic", "care", "small-clinic", "local")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(data, "config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	fx, err := updatetest.Build(p.Abs(paths.LocalDist), solutionFile, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return fx
}

func TestImportLocalDist(t *testing.T) {
	p := paths.New(t.TempDir())
	c := Client{Paths: p}
	if _, err := c.ImportLocalDist(paths.LocalDist); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("no snapshot: %v", err)
	}
	fx := localDist(t, p)
	ix, err := c.ImportLocalDist(paths.LocalDist)
	if err != nil {
		t.Fatal(err)
	}
	if ix.DistBase != fx.DistBase {
		t.Errorf("dist_base %s, want %s", ix.DistBase, fx.DistBase)
	}
	store := solution.LockStore{Paths: p}
	if got := strings.Join(store.Cached("care-clinic"), " "); got != "2026.11.0 2026.11.1" && got != "2026.11.1 2026.11.0" {
		t.Errorf("cached locks %q", got)
	}
	for _, v := range updatetest.Versions {
		l, _, err := store.Get("care-clinic", v)
		if err != nil || l.SHA256 != fx.SHA256[v] {
			t.Fatalf("lock %s: %v", v, err)
		}
	}
	cached, err := solution.CachedIndex(p)
	if err != nil || cached == nil || cached.DistBase != fx.DistBase {
		t.Fatalf("cached index: %v", err)
	}
	if url, at := IndexFetchedAt(p); url != LocalIndexURL || at.IsZero() {
		t.Errorf("index meta: %s %v", url, at)
	}

	// A lock that does not have the SHA-256 of the index is refused.
	f := p.Abs(paths.LocalDist + "/lock-care-clinic-2026.11.1/care-clinic-2026.11.1.lock.yaml")
	if err := os.WriteFile(f, append(fx.Locks["2026.11.1"], "# changed\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ImportLocalDist(paths.LocalDist); problemSlug(err) != problem.ArtefactInvalid {
		t.Fatalf("changed lock: %v", err)
	}
}

func problemSlug(err error) string {
	var p *problem.Problem
	if errors.As(err, &p) {
		return p.Slug
	}
	return ""
}

// TestIndexFallbackOrder: updates.index_url, then the cache, then the dist snapshot of the image.
func TestIndexFallbackOrder(t *testing.T) {
	e := newTEnv(t, "stable", "")
	cfg, err := config.Load(e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	snap := localDist(t, e.p)

	// No cache, the network index works: the network index wins.
	ix, err := e.u.index(ctx, cfg, true)
	if err != nil || ix.DistBase != e.fx.DistBase {
		t.Fatalf("network index: %v %+v", err, ix)
	}

	// No cache, the network index is unreachable: the snapshot.
	if err := os.RemoveAll(e.p.Abs(paths.UpdateCache)); err != nil {
		t.Fatal(err)
	}
	cfg.Updates.IndexURL = "file:///nonexistent/care-pi/index/index.json"
	ix, err = e.u.index(ctx, cfg, true)
	if err != nil || ix.DistBase != snap.DistBase {
		t.Fatalf("snapshot index: %v %+v", err, ix)
	}
	// Without network the snapshot is now the cache.
	if ix, err = e.u.index(ctx, cfg, false); err != nil || ix.DistBase != snap.DistBase {
		t.Fatalf("cached snapshot: %v", err)
	}

	// An unreachable index makes the check fail, but the snapshot fills an empty cache.
	if err := os.RemoveAll(e.p.Abs(paths.UpdateCache)); err != nil {
		t.Fatal(err)
	}
	e.writeConfig("stable", "")
	data, err := os.ReadFile(e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(data), e.fx.IndexURL, "file:///nonexistent/care-pi/index/index.json", 1)
	if err := os.WriteFile(e.cfg, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := e.job(jobs.KindUpdateCheck)
	if _, err := e.u.Check(ctx, rec); problemSlug(err) != problem.IndexUnavailable {
		t.Fatalf("check without the index: %v", err)
	}
	if cached, _ := solution.CachedIndex(e.p); cached == nil || cached.DistBase != snap.DistBase {
		t.Fatal("the check did not import the snapshot into the empty cache")
	}
}
