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

	"github.com/rithviknishad/care-pi/agent/internal/backup"
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
	tw  *tar.Writer
	now time.Time
	red *Redactor
}

// credentialREs match the credentials that a log line can carry: a bearer token (the JWT of Care), the session
// cookie of the console, and a Tailscale auth key. Since the review of Phase 7 (M3), the audit log of the WAF has no
// request headers; this is a second guard for each file of the bundle and each plain-text export (S68).
var credentialREs = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+`),
	regexp.MustCompile(`(__Host-care_pi_session=)[^;\s"'\\,]+`),
	regexp.MustCompile(`(tskey-)[A-Za-z0-9-]+`),
}

// secretAssignment matches a key with a secret name and its value: password=x, "token": "x", X-CSRF-Token: x,
// DJANGO_SECRET_KEY=x. Each part of the key between "_" or "-" is a word, so csrftoken=x is not a match.
var secretAssignment = regexp.MustCompile(`(?i)\b((?:[a-z0-9]+[_-])*(?:password|passwd|passphrase|secret|token|auth[_-]?key|api[_-]?key|access[_-]?key|private[_-]?key)(?:[_-][a-z0-9]+)*)("?[ \t]*[:=][ \t]*"?)([^\s"'&;,)}\]]+)`)

// redactCredentials replaces the value of each credential of credentialREs and of each secretAssignment with
// <redacted>. A value that is true, false, null, an object, or an array stays: it is not a secret, and the JSON
// files of a bundle stay valid.
func redactCredentials(text string) string {
	for _, re := range credentialREs {
		text = re.ReplaceAllString(text, "${1}<redacted>")
	}
	return secretAssignment.ReplaceAllStringFunc(text, func(m string) string {
		sub := secretAssignment.FindStringSubmatch(m)
		switch v := strings.ToLower(sub[3]); {
		case v == "true", v == "false", v == "null", strings.HasPrefix(v, "{"), strings.HasPrefix(v, "["), v == "<redacted>":
			return m
		}
		return sub[1] + sub[2] + "<redacted>"
	})
}

// Redactor removes the secrets from a text: each secret value of the device (the files of the secrets directory, the
// S3 keys of the destinations, the values of the recovery keys) and each credential of redactCredentials. The support
// bundle and the plain-text exports (S68) use it.
type Redactor struct {
	secrets []string
}

// Redactor returns a redactor with the secret values of the device now.
func (s *Service) Redactor() *Redactor { return &Redactor{secrets: s.secretValues()} }

// Text returns the text without secrets.
func (r *Redactor) Text(text string) string {
	for _, v := range r.secrets {
		text = strings.ReplaceAll(text, v, "<redacted>")
	}
	return redactCredentials(text)
}

func (b *bundleWriter) add(name string, data []byte) error {
	text := b.red.Text(string(data))
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
	// The S3 keys of the destinations, and the values that come from the recovery keys.
	if ents, err := os.ReadDir(filepath.Join(dir, "backup")); err == nil {
		for _, e := range ents {
			if c := backup.ReadCredentials(s.Paths, strings.TrimSuffix(e.Name(), ".json")); c != nil {
				out = append(out, c.AccessKeyID, c.SecretAccessKey)
			}
		}
	}
	for _, which := range []string{backup.Active, backup.Pending} {
		if k, _, err := s.keys().Get(which); err == nil {
			out = append(out, k.Hex(), k.ResticPassword(), k.CipherPass(), strings.Join(k.Words(), " "), k.QRPayload())
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
	b := &bundleWriter{tw: tw, now: now, red: s.Redactor()}

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
		// Phase 7: the result of care-pi doctor, the rendered tree, the firewall, Tailscale, the containers, and the
		// memory peaks of the cgroups.
		func() error {
			rep, err := s.Doctor(ctx)
			if err != nil {
				return b.add("doctor.error.txt", []byte(err.Error()+"\n"))
			}
			return b.add("doctor.json", jsonOf(rep))
		},
		func() error { return s.collectRendered(b) },
		func() error {
			for name, c := range map[string][]string{
				"nft-ruleset.txt":       {"nft", "list", "ruleset"},
				"podman-ps.txt":         {"podman", "ps", "--all", "--format", "{{.Names}}\t{{.Image}}\t{{.Status}}"},
				"tailscale-status.json": {"tailscale", "status", "--json"},
				"memory-peaks.txt":      {"systemctl", "show", "-p", "Id", "-p", "MemoryPeak", "-p", "MemorySwapPeak", "-p", "MemoryMax", "care-*.service", "care-pi*.service"},
				"firewall-state.json":   nil,
				"listening-sockets.txt": {"ss", "-ltnupH"},
			} {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				var out []byte
				if c == nil {
					out = jsonOf(firewallStateOf(s))
				} else {
					var err error
					out, err = s.Host.Runner.Run(ctx, hostCmd(c[0], c[1:]...))
					if err != nil {
						out = append(out, []byte("\nerror: "+err.Error()+"\n")...)
					}
				}
				if name == "tailscale-status.json" {
					out = redactTailscale(out)
				}
				if err := b.add(name, out); err != nil {
					return err
				}
			}
			return nil
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

// collectRendered adds the rendered tree (the quadlets, the units, rendered/) to the bundle. env files lose their
// secret values; a file with a secret mode (0600) that is not an env file or the Caddyfile is left out.
func (s *Service) collectRendered(b *bundleWriter) error {
	for _, dir := range []string{paths.QuadletDir, paths.RenderedDir} {
		root := s.Paths.Abs(dir)
		err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			if dir == paths.QuadletDir && !strings.HasPrefix(filepath.Base(p), "care") {
				return nil
			}
			// The plug configs of Care can have secret settings (the podman secret care-additional-plugs).
			if filepath.Base(p) == "additional-plugs.json" {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			if strings.HasSuffix(p, ".env") {
				data = redactEnv(data)
			}
			return b.add("rendered-tree"+dir+"/"+filepath.ToSlash(rel), data)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// firewallStateOf is the firewall state for the bundle (nil on an error).
func firewallStateOf(s *Service) any {
	st, err := s.Firewall(context.Background())
	if err != nil {
		return map[string]string{"error": err.Error()}
	}
	return st
}

// tailscaleRedact lists the members of "tailscale status --json" that the bundle hides: the keys and the addresses
// and names of the other devices of the tailnet.
var tailscaleRedact = map[string]bool{"PublicKey": true, "PrivateKey": true, "NodeKey": true, "MachineKey": true,
	"Peer": true, "User": true, "AuthURL": true, "ClientVersion": true}

// redactTailscale hides the keys and the peers of "tailscale status --json"; text that is not JSON stays.
func redactTailscale(data []byte) []byte {
	var doc map[string]any
	if json.Unmarshal(data, &doc) != nil {
		return data
	}
	var walk func(v any) any
	walk = func(v any) any {
		switch t := v.(type) {
		case map[string]any:
			for k, val := range t {
				if tailscaleRedact[k] {
					t[k] = "<redacted>"
					continue
				}
				t[k] = walk(val)
			}
		case []any:
			for i := range t {
				t[i] = walk(t[i])
			}
		}
		return v
	}
	out, err := json.MarshalIndent(walk(doc), "", "  ")
	if err != nil {
		return data
	}
	return append(out, '\n')
}
