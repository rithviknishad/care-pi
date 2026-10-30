package service

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/rithviknishad/care-pi/agent/internal/buildinfo"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
	"github.com/rithviknishad/care-pi/agent/internal/status"
)

// BundleRequest holds the options of a support bundle (schema SupportBundleRequest).
type BundleRequest struct {
	LogLines   int
	SinceHours int
}

// Bundle limits.
const (
	BundleKeep = 3
	BundleTTL  = 24 * time.Hour
)

var bundleID = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}\.[0-9]{3}Z-support-bundle$`)

// secretKey matches the names of variables with a secret value.
var secretKey = regexp.MustCompile(`(?i)(secret|password|passwd|token|key|credential|private|cipher)`)

func bundlePath(p paths.Paths, id string) string {
	return p.Abs(paths.SupportDir + "/" + id + ".tar.gz")
}

// pruneBundles deletes the bundles older than 24 h and keeps the newest keep-1 others.
func (s *Service) pruneBundles(keep int) {
	dir := s.Paths.Abs(paths.SupportDir)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tar.gz") {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for i, n := range names {
		info, err := os.Stat(filepath.Join(dir, n))
		if i >= keep || (err == nil && s.now().Sub(info.ModTime()) > BundleTTL) {
			_ = os.Remove(filepath.Join(dir, n))
		}
	}
}

// StartSupportBundle starts a job of kind support-bundle. It is cancellable.
func (s *Service) StartSupportBundle(ctx context.Context, req BundleRequest, source string) (*jobs.Job, error) {
	if req.LogLines == 0 {
		req.LogLines = 1000
	}
	if req.SinceHours == 0 {
		req.SinceHours = 24
	}
	if req.LogLines < 100 || req.LogLines > 10000 {
		return nil, problem.Invalid("/log_lines", "use a number from 100 to 10000", "minimum")
	}
	if req.SinceHours < 1 || req.SinceHours > 720 {
		return nil, problem.Invalid("/since_hours", "use a number from 1 to 720", "minimum")
	}
	return s.Jobs.Start(ctx, jobs.Spec{Kind: jobs.KindSupportBundle, Source: source, Cancellable: true},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			return s.BuildSupportBundle(ctx, rec, req)
		})
}

type bundleWriter struct {
	tw      *tar.Writer
	now     time.Time
	secrets []string
}

func (b *bundleWriter) add(name string, data []byte) error {
	text := string(data)
	for _, v := range b.secrets {
		text = strings.ReplaceAll(text, v, "<redacted>")
	}
	hdr := &tar.Header{Name: "care-pi-support/" + name, Mode: 0o600, Size: int64(len(text)), ModTime: b.now}
	if err := b.tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := io.WriteString(b.tw, text)
	return err
}

// redactEnv replaces the value of each variable with a secret name.
func redactEnv(data []byte) []byte {
	var out []string
	for _, l := range strings.Split(string(data), "\n") {
		if k, _, ok := strings.Cut(l, "="); ok && !strings.HasPrefix(strings.TrimSpace(l), "#") && secretKey.MatchString(k) {
			l = k + "=<redacted>"
		}
		out = append(out, l)
	}
	return []byte(strings.Join(out, "\n"))
}

// redactDoc replaces the values of secret keys in a decoded YAML or JSON document.
func redactDoc(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if secretKey.MatchString(k) {
				if _, isMap := val.(map[string]any); !isMap {
					t[k] = "<redacted>"
					continue
				}
			}
			t[k] = redactDoc(val)
		}
	case []any:
		for i := range t {
			t[i] = redactDoc(t[i])
		}
	}
	return v
}

func (s *Service) secretValues() []string {
	var out []string
	dir := s.Paths.Abs(paths.SecretsDir)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range ents {
		if e.IsDir() || e.Name() == "agent.json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(data), "\n") {
			if v := strings.TrimSpace(l); len(v) >= 6 {
				out = append(out, v)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// BuildSupportBundle collects the bundle into /var/lib/care-pi/state/support/<job id>.tar.gz.
func (s *Service) BuildSupportBundle(ctx context.Context, rec *jobs.Recorder, req BundleRequest) (*jobs.SupportBundleResult, error) {
	if err := os.MkdirAll(s.Paths.Abs(paths.SupportDir), 0o700); err != nil {
		return nil, err
	}
	s.pruneBundles(BundleKeep - 1)
	id := rec.ID()
	path := bundlePath(s.Paths, id)
	f, err := os.OpenFile(path+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(path + ".tmp") }()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	now := s.now()
	b := &bundleWriter{tw: tw, now: now, secrets: s.secretValues()}

	end := rec.StepAction("collect: status, config, units, journals, jobs", "collect")
	err = s.collect(ctx, b, rec, req)
	end(err)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	end = rec.StepAction("archive: "+id+".tar.gz", "archive")
	err = tw.Close()
	if err == nil {
		err = gz.Close()
	}
	if err == nil {
		err = f.Close()
	} else {
		_ = f.Close()
	}
	if err == nil {
		err = os.Rename(path+".tmp", path)
	}
	end(err)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	host := strings.ReplaceAll(s.PrimaryHost(), ".", "-")
	return &jobs.SupportBundleResult{Kind: jobs.KindSupportBundle, BundleID: id,
		Filename:  "care-pi-support-" + host + "-" + now.Format("20060102T150405Z") + ".tar.gz",
		SizeBytes: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), ExpiresAt: now.Add(BundleTTL).Truncate(time.Second),
		DownloadPath: "/api/v1/support-bundle/" + id}, nil
}

func (s *Service) collect(ctx context.Context, b *bundleWriter, rec *jobs.Recorder, req BundleRequest) error {
	jsonOf := func(v any) []byte {
		data, _ := json.MarshalIndent(v, "", "  ")
		return append(data, '\n')
	}
	steps := []func() error{
		func() error {
			return b.add("version.txt", []byte(buildinfo.String()+"\n"))
		},
		func() error {
			st, err := s.Status(ctx)
			if err != nil {
				return b.add("status.error.txt", []byte(err.Error()+"\n"))
			}
			return b.add("status.json", jsonOf(st))
		},
		func() error { return b.add("host-check.json", jsonOf(s.HostCheckView(ctx, false))) },
		func() error {
			data, err := os.ReadFile(s.configPath())
			if err != nil {
				return b.add("config.error.txt", []byte(err.Error()+"\n"))
			}
			var doc any
			if yaml.Unmarshal(data, &doc) == nil {
				if out, err := yaml.Marshal(redactDoc(config.Normalize(doc))); err == nil {
					data = out
				}
			}
			return b.add("config.yaml", data)
		},
		func() error {
			for name, src := range map[string]string{"lock.yaml": paths.LockFile, "setup.json": setup.File,
				"rendered/backend.env": paths.RenderedCare + "/backend.env", "rendered/frontend.env": paths.RenderedFE + "/frontend.env"} {
				data, err := os.ReadFile(s.Paths.Abs(src))
				if err != nil {
					continue
				}
				if strings.HasSuffix(name, ".env") {
					data = redactEnv(data)
				}
				if err := b.add(name, data); err != nil {
					return err
				}
			}
			return nil
		},
		func() error {
			units := append(status.Units(), paths.AgentUnit)
			out, err := s.Host.Runner.Run(ctx, hostCmd("systemctl", append([]string{"show", "-p", "Id", "-p", "ActiveState",
				"-p", "SubState", "-p", "Result", "-p", "NRestarts", "-p", "MemoryCurrent", "-p", "MemoryPeak", "-p", "MemoryMax"}, units...)...))
			if err != nil {
				out = append(out, []byte("\nerror: "+err.Error()+"\n")...)
			}
			return b.add("units.txt", out)
		},
		func() error {
			since := "@" + strconv.FormatInt(s.now().Add(-time.Duration(req.SinceHours)*time.Hour).Unix(), 10)
			for _, u := range LogUnits {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				out, err := s.Host.Runner.Run(ctx, hostCmd("journalctl", "--unit", u.Unit, "--no-pager", "--output", "short-iso",
					"--lines", strconv.Itoa(req.LogLines), "--since", since))
				if err != nil {
					out = append(out, []byte("\nerror: "+err.Error()+"\n")...)
				}
				if err := b.add("logs/"+u.Name+".log", out); err != nil {
					return err
				}
			}
			return nil
		},
		func() error {
			list, _ := jobs.List(s.Paths, jobs.Filter{Limit: 11})
			n := 0
			for _, j := range list {
				if j.ID == rec.ID() || n == 10 {
					continue
				}
				n++
				for _, ext := range []string{".json", ".events.jsonl"} {
					data, err := os.ReadFile(s.Paths.Abs(paths.JobsDir + "/" + j.ID + ext))
					if err == nil {
						if err := b.add("jobs/"+j.ID+ext, data); err != nil {
							return err
						}
					}
				}
			}
			return nil
		},
	}
	for _, step := range steps {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := step(); err != nil {
			return err
		}
	}
	rec.Logf("collected %d sections", len(steps))
	return nil
}

// BundleFile returns the path and the download name of a support bundle.
func (s *Service) BundleFile(id string) (string, string, error) {
	if !bundleID.MatchString(id) {
		return "", "", problem.Newf(problem.NotFound, "No support bundle has the id %s.", id)
	}
	path := bundlePath(s.Paths, id)
	info, err := os.Stat(path)
	if err != nil || s.now().Sub(info.ModTime()) > BundleTTL {
		return "", "", problem.Newf(problem.NotFound, "No support bundle has the id %s. The agent keeps a bundle for 24 h.", id)
	}
	name := id + ".tar.gz"
	if j, err := jobs.Load(s.Paths, id); err == nil && len(j.Result) > 0 {
		var r jobs.SupportBundleResult
		if json.Unmarshal(j.Result, &r) == nil && r.Filename != "" {
			name = r.Filename
		}
	}
	return path, name, nil
}

// CopyBundle copies a bundle to dst.
func CopyBundle(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return fmt.Errorf("cannot write %s: %w", dst, err)
	}
	return nil
}
