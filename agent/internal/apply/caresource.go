package apply

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/fe"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/render"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
)

// MinRAMCareSourceBuild is the RAM that a local build of CARE from its git source needs (S71). A Pi with 2 GB
// cannot build CARE; CI builds the image instead.
const MinRAMCareSourceBuild = 3500 << 20

// maxCareSource limits the download of the source archive.
const maxCareSource = 512 << 20

// gitCare is the result of the resolution of a care.source git.
type gitCare struct {
	// Ref is the ref that the agent resolved (solution.care.ref, or solution.custom.care_ref); SHA is its commit.
	Ref, SHA, Repo string
	// Source is the local build from the git source, or nil when the registry has the image of the commit.
	Source *render.CareSource
}

// resolveGitCare resolves a care.source git (S71, docs/solutions.md section 13.4): the ref (or
// solution.custom.care_ref on the track custom) to a commit, and the commit to the image that CI built
// (<care.image>:<sha12>). It returns a copy of the solution with care.Resolved set. Without the image of the commit
// in the registry, a host with 4 GB of RAM or more builds docker/prod.Dockerfile of the commit (a local build); a
// host with less RAM stops with unsupported-config. A solution lock pins the commit: no network then.
func resolveGitCare(ctx context.Context, o PrepareOptions, cfg *config.Config, sol *solution.Solution, sl *solution.Lock,
	prev *lock.Lock, r lock.Resolver, update bool) (*solution.Solution, *gitCare, error) {
	if sol == nil || !sol.Care.IsGit() {
		return sol, nil, nil
	}
	g := &gitCare{Ref: sol.Care.Ref, Repo: sol.Care.Repo}
	custom := cfg.Solution.Track == "custom" && cfg.Solution.Custom.CareRef != ""
	if custom {
		g.Ref = cfg.Solution.Custom.CareRef
	}
	image := sol.Care.ImageRepo()
	switch {
	case sl != nil && !custom && sl.Care.IsGit() && sl.Care.GitSHA != nil:
		g.SHA = *sl.Care.GitSHA
	default:
		seed := &lock.Lock{Git: map[string]lock.GitRef{}}
		if prev != nil && prev.Components.Care != nil {
			if c := prev.Components.Care; c.GitSHA != "" && c.Ref == g.Ref {
				seed.Git["care"] = lock.GitRef{Repo: g.Repo, Ref: g.Ref, SHA: c.GitSHA}
			}
		}
		res, err := r.Resolve(ctx, lock.Wants{Git: map[string]lock.GitWant{"care": {Repo: g.Repo, Ref: g.Ref}}}, seed, update)
		if err != nil {
			return nil, nil, err
		}
		g.SHA = res.Git["care"].SHA
	}
	tag := solution.GitTag(g.SHA)
	if g.SHA == "" {
		// Offline (care-pi render) without a commit in a lock: the resolver warned; the unit names the ref.
		tag = tagOf(g.Ref)
	}
	ref := image + ":" + tag
	if g.SHA != "" && !r.Offline && r.Skopeo != nil && !knownImage(prev, sl, ref) {
		if _, err := r.Skopeo.Digest(ctx, ref, r.OS, r.Arch); err != nil {
			ram := memTotal(o.Paths)
			if ram > 0 && ram < MinRAMCareSourceBuild {
				p := problem.Newf(problem.UnsupportedConfig, "CARE %s (%s of %s) has no image %s: CI builds it (the workflows solution-lock and "+
					"solution-image). A local build of CARE needs 4 GB of RAM or more; this host has %d MiB. Run the workflow, or use a "+
					"host with 4 GB.", tag, g.Ref, g.Repo, ref, ram>>20)
				return nil, nil, exitcode.With(exitcode.BadInput, p)
			}
			g.Source = &render.CareSource{Repo: g.Repo, SHA: g.SHA}
			image = solution.CareBaseLocal
			if o.Progress != nil {
				_, _ = fmt.Fprintf(o.Progress, "the registry has no %s (%v); build CARE %s from its git source on this host\n", ref, err, tag)
			}
		}
	}
	cp := *sol
	cp.Care.Image, cp.Care.Resolved = image, tag
	return &cp, g, nil
}

// tagOf makes an image tag of a git ref: each character that a tag cannot have becomes "-".
func tagOf(ref string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_.-", r) {
			return r
		}
		return '-'
	}, ref)
}

// knownImage reports whether the previous lock or the solution lock has a digest of ref: then the registry has it.
func knownImage(prev *lock.Lock, sl *solution.Lock, ref string) bool {
	if prev != nil {
		for _, img := range prev.Images {
			if img.Ref == ref && img.Digest != "" {
				return true
			}
		}
		if c := prev.Components.Care; c != nil && c.Build != nil && c.Image+":"+solution.GitTag(c.GitSHA) == ref && c.Build.BaseDigest != "" {
			return true
		}
	}
	return sl != nil && sl.Care.IsGit() && sl.Care.BaseRef() == ref && sl.Care.Digest != ""
}

// memTotal returns MemTotal of /proc/meminfo in bytes, or 0.
func memTotal(p paths.Paths) int64 {
	f, err := os.Open(p.Abs("/proc/meminfo"))
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			v, _ := strconv.ParseInt(fields[1], 10, 64)
			return v << 10
		}
	}
	return 0
}

// ensureCareSource builds the local CARE image from its git source when it does not exist (S71): the agent extracts
// the archive of the commit to the build directory and starts the build unit, with Care scaled down. It reports
// whether it built.
func ensureCareSource(ctx context.Context, o Options, pr *Prepared) (bool, error) {
	cs := pr.CareSource
	if cs == nil {
		return false, nil
	}
	h := o.Host
	ok, err := h.Podman.ImageExists(ctx, cs.Image())
	if err != nil || ok {
		return false, err
	}
	if ram := memTotal(o.Paths); ram > 0 && ram < MinRAMCareSourceBuild {
		return false, fmt.Errorf("a local build of CARE needs 4 GB of RAM or more; this host has %d MiB", ram>>20)
	}
	dir := o.Paths.Abs(cs.Dir())
	if _, err := os.Stat(filepath.Join(dir, "docker", "prod.Dockerfile")); err != nil {
		_, _ = fmt.Fprintf(o.Out, "    download %s\n", cs.TarballURL())
		if err := fetchSource(ctx, nil, cs.TarballURL(), dir); err != nil {
			return false, fmt.Errorf("cannot download the source of CARE %s: %w", cs.Tag(), err)
		}
	}
	env := fe.Env{Config: pr.Config, Paths: o.Paths, Host: h, Out: o.Out}
	if err := fe.RunScaled(ctx, env, "", cs.Unit(pr.Config.Solution.Name), "the build of CARE "+cs.Tag()+" from its git source", 2*time.Hour); err != nil {
		return false, err
	}
	return true, nil
}

// fetchSource downloads a .tar.gz of a repository and extracts it to dir without its first path component (the
// archive of GitHub has one directory <repo>-<sha>/). It writes to dir.tmp first.
func fetchSource(ctx context.Context, c *http.Client, url, dir string) error {
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	tmp := dir + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := extractStripped(io.LimitReader(resp.Body, maxCareSource), tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	_ = os.RemoveAll(dir)
	return os.Rename(tmp, dir)
}

// extractStripped extracts a .tar.gz to dir and drops the first path component of each entry. It refuses an entry
// that leaves dir.
func extractStripped(r io.Reader, dir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for {
		hd, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		_, rest, ok := strings.Cut(strings.TrimPrefix(hd.Name, "./"), "/")
		if !ok || rest == "" {
			continue
		}
		clean := filepath.Clean(rest)
		if clean == ".." || strings.HasPrefix(clean, "../") || filepath.IsAbs(clean) {
			return fmt.Errorf("the archive entry %s leaves the directory", hd.Name)
		}
		target := filepath.Join(dir, clean)
		switch hd.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if hd.Mode&0o111 != 0 {
				mode = 0o755
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			link := filepath.Clean(filepath.Join(filepath.Dir(clean), hd.Linkname))
			if filepath.IsAbs(hd.Linkname) || link == ".." || strings.HasPrefix(link, "../") {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(hd.Linkname, target); err != nil {
				return err
			}
		}
	}
}
