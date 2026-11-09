package render

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
)

// Wave 13 (S73): a backend plug in a directory of its repository gets the version @<sha>#subdirectory=<dir> in
// ADDITIONAL_PLUGS; package_name stays the bare repository. A plug without a subdirectory keeps @<sha>.
func TestAdditionalPlugsSubdirectory(t *testing.T) {
	cfg, err := config.Load(filepath.Join("testdata", "config-public.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	render := func(sub string) string {
		sol, err := solution.Load("care-clinic", "testdata/solutions")
		if err != nil {
			t.Fatal(err)
		}
		for i := range sol.Plugs {
			if sol.Plugs[i].ID == "care_multihost_files_be" {
				sol.Plugs[i].Backend.Subdirectory = sub
			}
		}
		pl, err := DesireLock(cfg, sol, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		lk, err := lock.Resolver{Skopeo: fakeSkopeo{}, Git: fakeGit(), OS: "linux", Arch: "arm64"}.Resolve(context.Background(), pl.Wants, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewModel(Input{Config: cfg, Solution: sol, Lock: lk, IPs: TestIPs, CARoot: true, ACMEDNS: true, PublicIssued: true})
		if err != nil {
			t.Fatal(err)
		}
		sha := lk.Plugs["care_multihost_files_be"].SHA
		if len(sha) != 40 {
			t.Fatalf("sha %q", sha)
		}
		return strings.ReplaceAll(m.AdditionalPlugsJSON, sha, "SHA")
	}
	plain := render("")
	if !strings.Contains(plain, `"package_name":"git+https://github.com/ohcnetwork/care_multihost_files_be.git","version":"@SHA"`) {
		t.Fatalf("without a subdirectory: %s", plain)
	}
	sub := render("plugs/multihost")
	if !strings.Contains(sub, `"package_name":"git+https://github.com/ohcnetwork/care_multihost_files_be.git","version":"@SHA#subdirectory=plugs/multihost"`) {
		t.Fatalf("with a subdirectory: %s", sub)
	}
}
