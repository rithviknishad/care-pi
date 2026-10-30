package lock

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

type countSkopeo struct{ n int }

func (c *countSkopeo) Digest(_ context.Context, ref, _, _ string) (string, error) {
	c.n++
	if strings.Contains(ref, "broken") {
		return "", errors.New("manifest unknown")
	}
	return "sha256:" + strings.Repeat("d", 63) + string(rune('0'+c.n)), nil
}

func TestSplitImage(t *testing.T) {
	cases := map[string][3]string{
		"ghcr.io/ohcnetwork/care:latest":             {"ghcr.io/ohcnetwork/care", "latest", ""},
		"ghcr.io/ohcnetwork/care":                    {"ghcr.io/ohcnetwork/care", "latest", ""},
		"localhost:5000/x/y:1":                       {"localhost:5000/x/y", "1", ""},
		"docker.io/library/redis@sha256:abc":         {"docker.io/library/redis", "", "sha256:abc"},
		"docker.io/library/redis:8-alpine@sha256:ab": {"docker.io/library/redis", "8-alpine", "sha256:ab"},
	}
	for in, want := range cases {
		r, tg, d := SplitImage(in)
		if [3]string{r, tg, d} != want {
			t.Errorf("SplitImage(%s) = %s %s %s", in, r, tg, d)
		}
	}
}

func TestResolveReusesAndUpdates(t *testing.T) {
	sk := &countSkopeo{}
	g := &host.FakeGit{Refs: map[string]map[string]string{
		"https://example.org/fe": {"refs/heads/develop": strings.Repeat("1", 40)},
	}}
	r := Resolver{Skopeo: sk, Git: g, OS: "linux", Arch: "arm64"}
	w := Wants{
		Images: map[string]string{
			"care":  "ghcr.io/ohcnetwork/care:latest",
			"redis": "docker.io/library/redis@sha256:" + strings.Repeat("e", 64),
			"pg":    "localhost/care-pi/postgres:17",
		},
		Git: map[string]GitWant{"care_fe": {Repo: "https://example.org/fe", Ref: "develop"}},
	}
	ctx := context.Background()
	l1, err := r.Resolve(ctx, w, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if sk.n != 1 || len(g.Calls) != 1 {
		t.Fatalf("first resolve: skopeo %d, git %d", sk.n, len(g.Calls))
	}
	if !l1.Images["redis"].Pinned || !l1.Images["pg"].Local || l1.Images["pg"].Pull() != "localhost/care-pi/postgres:17" {
		t.Fatalf("pinned or local image wrong: %+v", l1.Images)
	}
	if !strings.HasPrefix(l1.Images["care"].Pull(), "ghcr.io/ohcnetwork/care@sha256:") {
		t.Fatalf("care pull ref: %s", l1.Images["care"].Pull())
	}
	// Same wants: reuse, no network.
	l2, err := r.Resolve(ctx, w, l1, false)
	if err != nil || sk.n != 1 || len(g.Calls) != 1 || !Equal(l1, l2) {
		t.Fatalf("reuse failed: %v skopeo=%d git=%d", err, sk.n, len(g.Calls))
	}
	// --update: tracked refs resolve again; pinned ones do not.
	l3, err := r.Resolve(ctx, w, l1, true)
	if err != nil || sk.n != 2 || len(g.Calls) != 2 {
		t.Fatalf("update failed: %v skopeo=%d git=%d", err, sk.n, len(g.Calls))
	}
	if got := ImagesChanged(l1, l3); len(got) != 1 || got[0] != "care" {
		t.Fatalf("ImagesChanged = %v", got)
	}
	// A changed ref resolves only that component.
	w.Images["care"] = "ghcr.io/ohcnetwork/care:latest-1284"
	if _, err := r.Resolve(ctx, w, l3, false); err != nil || sk.n != 3 || len(g.Calls) != 2 {
		t.Fatalf("changed ref: %v skopeo=%d git=%d", err, sk.n, len(g.Calls))
	}
	// Offline: no network, the plain ref stays.
	var warned []string
	off := Resolver{Offline: true, Warn: func(m string) { warned = append(warned, m) }}
	l4, err := off.Resolve(ctx, w, nil, false)
	if err != nil || len(warned) != 2 || l4.Images["care"].Pull() != "ghcr.io/ohcnetwork/care:latest-1284" {
		t.Fatalf("offline: %v %v %+v", err, warned, l4.Images["care"])
	}
	// An error names the image.
	w.Images["care"] = "ghcr.io/x/broken:1"
	if _, err := r.Resolve(ctx, w, nil, false); err == nil || !strings.Contains(err.Error(), "ghcr.io/x/broken:1") {
		t.Fatalf("want an error that names the image, got %v", err)
	}
}

// Each network resolution prints one line, so a long resolution is not silent.
func TestResolveProgress(t *testing.T) {
	var out bytes.Buffer
	sk := &countSkopeo{}
	g := &host.FakeGit{Refs: map[string]map[string]string{
		"https://example.org/fe": {"refs/heads/develop": "0123456789ab" + strings.Repeat("f", 28)},
	}}
	r := Resolver{Skopeo: sk, Git: g, OS: "linux", Arch: "arm64", Progress: &out}
	w := Wants{
		Images: map[string]string{
			"care":  "ghcr.io/ohcnetwork/care:latest",
			"redis": "docker.io/library/redis@sha256:" + strings.Repeat("e", 64),
			"pg":    "localhost/care-pi/postgres:17",
		},
		Git: map[string]GitWant{"care_fe": {Repo: "https://example.org/fe", Ref: "develop"}},
	}
	ctx := context.Background()
	l1, err := r.Resolve(ctx, w, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	want := "resolve ghcr.io/ohcnetwork/care:latest … dddddddddddd\n" +
		"resolve care_fe develop … 0123456789ab\n"
	if out.String() != want {
		t.Fatalf("progress:\n got  %q\n want %q", out.String(), want)
	}
	// A reused, pinned, or local entry uses no network and prints nothing.
	out.Reset()
	if _, err := r.Resolve(ctx, w, l1, false); err != nil || out.Len() != 0 {
		t.Fatalf("reuse printed %q (%v)", out.String(), err)
	}
	// A failed resolution ends its line.
	out.Reset()
	w.Images["care"] = "ghcr.io/x/broken:1"
	if _, err := r.Resolve(ctx, w, l1, false); err == nil || out.String() != "resolve ghcr.io/x/broken:1 … failed\n" {
		t.Fatalf("failure: %v, progress %q", err, out.String())
	}
}

func TestWriteKeepsHistory(t *testing.T) {
	p := paths.New(t.TempDir())
	l := &Lock{Version: 1, Images: map[string]Image{"care": {Ref: "a:1", Digest: "sha256:1"}}}
	now := time.Date(2026, 10, 30, 1, 2, 3, 0, time.UTC)
	if err := Write(p, l, now); err != nil {
		t.Fatal(err)
	}
	l.Images["care"] = Image{Ref: "a:1", Digest: "sha256:2"}
	if err := Write(p, l, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	hist, _ := filepath.Glob(p.Abs(paths.LockHistory + "/*.yaml"))
	if len(hist) != 1 || filepath.Base(hist[0]) != "20261030T020203Z.yaml" {
		t.Fatalf("history: %v", hist)
	}
	got, err := Read(p.Abs(paths.LockFile))
	if err != nil || got.Images["care"].Digest != "sha256:2" {
		t.Fatalf("read back: %v %+v", err, got)
	}
	st, _ := os.Stat(p.Abs(paths.LockFile))
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("lock mode %v", st.Mode())
	}
	if l, err := Read(p.Abs("/nope.yaml")); l != nil || err != nil {
		t.Fatal("a missing lock must return nil, nil")
	}
}
