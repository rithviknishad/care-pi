// Package updatetest makes a local dist base for tests: two solution locks of a solution
// (2026.11.0 and 2026.11.1), their bundles, and index.json, with file:// URLs. It is the "local
// index" of docs/solutions.md section 2.2 that the e2e tests and the lumine test plan use.
package updatetest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/fe"
	"github.com/rithviknishad/care-pi/agent/internal/render"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
)

// Versions are the lock versions of the fixture, oldest first.
var Versions = []string{"2026.11.0", "2026.11.1"}

// Fixture is a local dist base.
type Fixture struct {
	Dir      string
	DistBase string
	IndexURL string
	// Locks holds the lock files by version.
	Locks map[string][]byte
	// SHA256 holds the SHA-256 of the lock files by version.
	SHA256 map[string]string
	// CareFESHA holds the care_fe sha by version. FEBuildID is the FE build id of the config by version.
	CareFESHA map[string]string
	FEBuildID map[string]string
}

// Digest returns a fake digest for a seed.
func Digest(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// SHA returns a fake git sha for a seed.
func SHA(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])[:40]
}

// TarGz returns a tar.gz file with the files.
func TarGz(files map[string]string) []byte {
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, name := range sortedKeys(files) {
		data := files[name]
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(data))
	}
	_ = tw.Close()
	_ = gz.Close()
	return b.Bytes()
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func write(dir, tag, asset string, data []byte) (rel, sum string, size int64, err error) {
	d := filepath.Join(dir, tag)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", "", 0, err
	}
	if err := os.WriteFile(filepath.Join(d, asset), data, 0o644); err != nil {
		return "", "", 0, err
	}
	s := sha256.Sum256(data)
	sum = hex.EncodeToString(s[:])
	if err := os.WriteFile(filepath.Join(d, asset+".sha256"), []byte(sum+"  "+asset+"\n"), 0o644); err != nil {
		return "", "", 0, err
	}
	return tag + "/" + asset, sum, int64(len(data)), nil
}

// Option changes the fixture.
type Option func(*options)

type options struct{ minAgent string }

// MinAgent sets min_agent_version of each lock (default v0.0.1).
func MinAgent(v string) Option { return func(o *options) { o.minAgent = v } }

// Build makes the fixture in dir for the solution file and the config (its frontend env gives the FE
// build id). The two versions differ in the care image, the solution image, and care_fe.
func Build(dir, solutionFile string, cfg *config.Config, opts ...Option) (*Fixture, error) {
	o := options{minAgent: "v0.0.1"}
	for _, f := range opts {
		f(&o)
	}
	data, err := os.ReadFile(solutionFile)
	if err != nil {
		return nil, err
	}
	sol, err := solution.Parse(data, solutionFile)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	f := &Fixture{Dir: abs, DistBase: "file://" + filepath.ToSlash(abs) + "/", Locks: map[string][]byte{}, SHA256: map[string]string{},
		CareFESHA: map[string]string{}, FEBuildID: map[string]string{}}
	f.IndexURL = f.DistBase + "index/index.json"
	des, err := render.Desire(cfg, sol)
	if err != nil {
		return nil, err
	}
	var releases []solution.IndexRelease
	for i, v := range Versions {
		in := solution.LockInput{Solution: data, Version: v, CreatedAt: fmt.Sprintf("2026-11-%02dT10:00:00Z", 14+7*i),
			CreatedBy: "updatetest", Notes: "Test lock " + v + ".\n", MinAgentVersion: o.minAgent, Commit: SHA("commit-" + v),
			CareFESHA: SHA("care_fe-" + v), Plugs: map[string]solution.PlugInput{}, Images: map[string]solution.LockImg{}}
		in.Care.Digest, in.Care.GitSHA = Digest("care-"+v), SHA("care-git-"+v)
		in.SolutionImage.Image, in.SolutionImage.Tag, in.SolutionImage.Digest = render.ImageCareSolution, sol.Name+"-"+v, Digest("solution-image-"+v)
		for name, ref := range map[string]string{"postgres": render.ImagePostgres, "caddy": render.ImageCaddy, "redis": render.ImageRedis,
			"s3": render.ImageS3, "node": render.ImageNode} {
			in.Images[name] = solution.LockImg{Ref: ref, Digest: Digest(name)}
		}
		id := fe.BuildID(in.CareFESHA, des.Frontend)
		f.CareFESHA[v], f.FEBuildID[v] = in.CareFESHA, id
		sha12 := in.CareFESHA[:12]
		tag := "fe-" + sol.Name + "-" + sha12 + "-" + id
		bundle := TarGz(map[string]string{"index.html": "<!doctype html><title>care " + v + "</title>",
			fe.MarkerFile: `{"build_id":"` + id + `","care_fe_sha":"` + in.CareFESHA + `"}`})
		rel, sum, size, err := write(abs, tag, "care_fe-"+sol.Name+"-"+sha12+"-"+id+".tar.gz", bundle)
		if err != nil {
			return nil, err
		}
		var keys []string
		for k := range des.Frontend {
			keys = append(keys, k)
		}
		in.FE = solution.LockFE{BuildID: id, URL: rel, SHA256: sum, SizeBytes: size, EnvKeys: keys}
		for _, p := range sol.Plugs {
			pi := solution.PlugInput{}
			if p.Backend != nil {
				pi.BackendSHA = SHA(p.ID + "-backend")
			}
			if p.Frontend != nil && p.Frontend.Repo != "" {
				pi.FrontendSHA = SHA(p.ID + "-frontend")
				psha12 := pi.FrontendSHA[:12]
				ptag := "plug-" + p.Frontend.Slug + "-" + psha12
				entry := p.Frontend.EntryPath()
				pb := TarGz(map[string]string{entry: "// remoteEntry of " + p.ID, "care-pi-plug.json": `{"slug":"` + p.Frontend.Slug + `"}`})
				prel, psum, psize, err := write(abs, ptag, ptag+".tar.gz", pb)
				if err != nil {
					return nil, err
				}
				pi.FrontendURL, pi.FrontendSHA256, pi.FrontendSizeBytes = prel, psum, psize
			}
			in.Plugs[p.ID] = pi
		}
		lockData, err := solution.AssembleLock(in)
		if err != nil {
			return nil, err
		}
		ltag := "lock-" + sol.Name + "-" + v
		lrel, lsum, _, err := write(abs, ltag, sol.Name+"-"+v+".lock.yaml", lockData)
		if err != nil {
			return nil, err
		}
		f.Locks[v], f.SHA256[v] = lockData, lsum
		releases = append(releases, solution.IndexRelease{Data: lockData, URL: lrel, ReleasedAt: in.CreatedAt,
			ChangelogURL: "https://github.com/rithviknishad/care-pi/releases/tag/" + ltag})
	}
	ix, err := solution.AssembleIndex(solution.IndexInput{DistBase: f.DistBase, GeneratedAt: "2026-11-21T09:00:00Z", Releases: releases,
		HeldPackages: map[string]map[string]string{"trixie": {"podman": "5.4.2+ds1-2"}}})
	if err != nil {
		return nil, err
	}
	if _, _, _, err := write(abs, "index", "index.json", ix); err != nil {
		return nil, err
	}
	return f, nil
}

// Withdraw rewrites index.json with the version withdrawn.
func (f *Fixture) Withdraw(name, version, reason string) error {
	var releases []solution.IndexRelease
	for _, v := range Versions {
		releases = append(releases, solution.IndexRelease{Data: f.Locks[v], URL: "lock-" + name + "-" + v + "/" + name + "-" + v + ".lock.yaml",
			ReleasedAt: "2026-11-14T10:00:00Z"})
	}
	ix, err := solution.AssembleIndex(solution.IndexInput{DistBase: f.DistBase, GeneratedAt: "2026-11-22T09:00:00Z", Releases: releases,
		Withdrawn: map[string]string{name + "/" + version: reason}})
	if err != nil {
		return err
	}
	_, _, _, err = write(f.Dir, "index", "index.json", ix)
	return err
}

// Path returns the local path of a dist URL.
func (f *Fixture) Path(url string) string { return strings.TrimPrefix(url, "file://") }
