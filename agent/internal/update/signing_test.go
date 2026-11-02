package update

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/minisign"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
)

// TestRequireSigned: with updates.require_signed, the index and each lock need a valid .minisig of a trusted key
// (Phase 7, docs/solutions.md section 18).
func TestRequireSigned(t *testing.T) {
	e := newTEnv(t, "stable", "")
	index, err := os.ReadFile(e.fx.Path(e.fx.IndexURL))
	if err != nil {
		t.Fatal(err)
	}
	ix, err := solution.ParseIndex(index)
	if err != nil {
		t.Fatal(err)
	}
	entry := ix.Find("care-clinic").Locks[0]
	lockData, err := os.ReadFile(e.fx.Path(solution.ResolveURL(ix.DistBase, entry.URL)))
	if err != nil {
		t.Fatal(err)
	}
	pk, priv, _ := minisign.GenerateKey(rand.Reader)
	other, otherPriv, _ := minisign.GenerateKey(rand.Reader)
	files := map[string][]byte{}
	serve := func(name string, data []byte, sig []byte) {
		files["/"+name] = data
		if sig != nil {
			files["/"+name+SigSuffix] = sig
		}
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // a test server
	c := Client{Paths: e.p, HTTP: hc, RequireSigned: true, Keys: minisign.Keys{pk}}
	url := srv.URL + "/index.json"

	serve("index.json", index, nil)
	if _, err := c.FetchIndex(context.Background(), url); !problem.Is(err, problem.LockSignatureInvalid) || !strings.Contains(err.Error(), "no signature") {
		t.Fatalf("no signature: %v", err)
	}
	serve("index.json", index, minisign.Sign(otherPriv, other.ID, index, "x"))
	if _, err := c.FetchIndex(context.Background(), url); !problem.Is(err, problem.LockSignatureInvalid) {
		t.Fatalf("other key: %v", err)
	}
	serve("index.json", index, minisign.Sign(priv, pk.ID, append([]byte("x"), index...), "x"))
	if _, err := c.FetchIndex(context.Background(), url); !problem.Is(err, problem.LockSignatureInvalid) {
		t.Fatalf("signature of another file: %v", err)
	}
	serve("index.json", index, minisign.Sign(priv, pk.ID, index, "timestamp:1 file:index.json"))
	got, err := c.FetchIndex(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	// Without the key the same files fail; without require_signed they pass.
	if _, err := (Client{Paths: e.p, HTTP: hc, RequireSigned: true}).FetchIndex(context.Background(), url); !problem.Is(err, problem.LockSignatureInvalid) ||
		!strings.Contains(err.Error(), "trusts no signing key") {
		t.Fatalf("no key: %v", err)
	}
	serve("index.json", index, nil)
	if _, err := (Client{Paths: e.p, HTTP: hc}).FetchIndex(context.Background(), url); err != nil {
		t.Fatal(err)
	}

	// The lock: the URL of the entry on the test server.
	got.DistBase = srv.URL + "/"
	entry.URL = "lock.yaml"
	serve("lock.yaml", lockData, nil)
	if _, err := c.FetchLock(context.Background(), got, "care-clinic", entry); !problem.Is(err, problem.LockSignatureInvalid) {
		t.Fatalf("lock without signature: %v", err)
	}
	serve("lock.yaml", lockData, minisign.Sign(priv, pk.ID, lockData, "file:lock.yaml"))
	if _, err := c.FetchLock(context.Background(), got, "care-clinic", entry); err != nil {
		t.Fatal(err)
	}

	// A local file (care-pi update --lock-file, the dist snapshot): the signature file is next to it.
	dir := t.TempDir()
	f := filepath.Join(dir, "x.lock.yaml")
	if err := os.WriteFile(f, lockData, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.verifyLocal(f, lockData); !problem.Is(err, problem.LockSignatureInvalid) {
		t.Fatalf("local without signature: %v", err)
	}
	if err := os.WriteFile(f+SigSuffix, minisign.Sign(priv, pk.ID, lockData, "x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if id, err := c.verifyLocal(f, lockData); err != nil || id != pk.KeyID() {
		t.Fatalf("%q %v", id, err)
	}
}

// TestRequireSignedCachedIndex (m1 of the Phase 7 review): a cached index from before updates.require_signed has no
// record of a signature check, so it does not count: the index downloads again (and must be signed), and offline the
// update stops with lock-signature-invalid. A cache that a check accepted counts offline, until its file changes or
// the agent no longer trusts the key.
func TestRequireSignedCachedIndex(t *testing.T) {
	e := newTEnv(t, "stable", "")
	ctx := context.Background()
	if _, err := e.u.client().FetchIndex(ctx, e.fx.IndexURL); err != nil {
		t.Fatal(err)
	}
	pk, priv, _ := minisign.GenerateKey(rand.Reader)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(e.p.Abs(paths.KeysDir), 0o755))
	keyFile := e.p.Abs(paths.KeysDir + "/test.pub")
	must(os.WriteFile(keyFile, pk.Encode(), 0o644))
	data, err := os.ReadFile(e.cfg)
	must(err)
	must(os.WriteFile(e.cfg, append(data, []byte("  require_signed: true\n")...), 0o600))
	cfg, err := e.u.Config()
	if err != nil || !cfg.Updates.RequireSigned {
		t.Fatalf("config %v", err)
	}

	// Offline: the cache of the check without signatures does not count.
	if _, err := e.u.ResolveTarget(ctx, cfg, "", "", false); !problem.Is(err, problem.LockSignatureInvalid) || !strings.Contains(err.Error(), "cached index is unsigned") {
		t.Fatalf("offline with an unsigned cache: %v", err)
	}
	// Online, but the release has no signature: the new download fails.
	if _, err := e.u.ResolveTarget(ctx, cfg, "", "", true); !problem.Is(err, problem.LockSignatureInvalid) || !strings.Contains(err.Error(), "no signature") {
		t.Fatalf("online without signatures: %v", err)
	}
	// Online with signatures: the index downloads again, and the cache records the key.
	indexFile := e.fx.Path(e.fx.IndexURL)
	index, err := os.ReadFile(indexFile)
	must(err)
	must(os.WriteFile(indexFile+SigSuffix, minisign.Sign(priv, pk.ID, index, "file:index.json"), 0o644))
	ix, err := solution.ParseIndex(index)
	must(err)
	for _, l := range ix.Find("care-clinic").Locks {
		f := e.fx.Path(solution.ResolveURL(ix.DistBase, l.URL))
		lockData, err := os.ReadFile(f)
		must(err)
		must(os.WriteFile(f+SigSuffix, minisign.Sign(priv, pk.ID, lockData, "file:lock"), 0o644))
	}
	if _, err := e.u.ResolveTarget(ctx, cfg, "", "", true); err != nil {
		t.Fatal(err)
	}
	if meta := readMeta(t, e.p); meta.SignedBy != pk.KeyID() || meta.SignedSHA256 == "" {
		t.Fatalf("meta %+v", meta)
	}
	// Offline: the signed cache counts.
	if _, err := e.u.ResolveTarget(ctx, cfg, "", "", false); err != nil {
		t.Fatal(err)
	}
	// A changed cache file, or a key that the agent no longer trusts, does not count.
	cache := e.p.Abs(solution.IndexCacheFile)
	cached, err := os.ReadFile(cache)
	must(err)
	must(os.WriteFile(cache, append(append([]byte{}, cached...), '\n'), 0o600))
	if _, err := e.u.client().TrustedCache(); !errors.Is(err, errUnsignedCache) {
		t.Fatalf("changed cache: %v", err)
	}
	must(os.WriteFile(cache, cached, 0o600))
	if _, err := e.u.client().TrustedCache(); err != nil {
		t.Fatal(err)
	}
	must(os.Remove(keyFile))
	if _, err := e.u.client().TrustedCache(); !errors.Is(err, errUnsignedCache) {
		t.Fatalf("a key that the agent no longer trusts: %v", err)
	}
	// Without require_signed, each cache counts, as before.
	e.writeConfig("stable", "")
	if ix, err := e.u.client().TrustedCache(); err != nil || ix == nil {
		t.Fatalf("without require_signed: %v", err)
	}
}

func readMeta(t *testing.T, p paths.Paths) indexMeta {
	t.Helper()
	var meta indexMeta
	data, err := os.ReadFile(p.Abs(indexMetaFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatal(err)
	}
	return meta
}

// TestTrustedKeys: the keys of the build (none until the key ceremony) and the keys of the host.
func TestTrustedKeys(t *testing.T) {
	p := paths.New(t.TempDir())
	base, err := TrustedKeys(p)
	if err != nil {
		t.Fatal(err)
	}
	pk, _, _ := minisign.GenerateKey(rand.Reader)
	if err := os.MkdirAll(p.Abs(paths.KeysDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Abs(paths.KeysDir+"/local.pub"), pk.Encode(), 0o644); err != nil {
		t.Fatal(err)
	}
	keys, err := TrustedKeys(p)
	if err != nil || len(keys) != len(base)+1 || keys[len(keys)-1].KeyID() != pk.KeyID() {
		t.Fatalf("keys %v %v", keys.IDs(), err)
	}
	if err := os.WriteFile(p.Abs(paths.KeysDir+"/bad.pub"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := TrustedKeys(p); err == nil {
		t.Fatal("a bad key file must fail")
	}
}
