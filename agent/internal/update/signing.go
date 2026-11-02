package update

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rithviknishad/care-pi/agent/internal/minisign"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/solutions"
)

// SigSuffix is the suffix of a minisign signature file (docs/solutions.md section 18).
const SigSuffix = ".minisig"

// MaxSigBytes bounds a signature file.
const MaxSigBytes = 4 << 10

// TrustedKeys returns the minisign keys that the agent trusts: the keys of the agent build (solutions/keys/*.pub)
// and the keys of the host (/etc/care-pi/keys/*.pub).
func TrustedKeys(p paths.Paths) (minisign.Keys, error) {
	var keys minisign.Keys
	add := func(name string, data []byte) error {
		k, err := minisign.ParsePublicKey(data)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		keys = append(keys, k)
		return nil
	}
	embedded, _ := fs.Glob(solutions.Keys, "keys/*.pub")
	sort.Strings(embedded)
	for _, n := range embedded {
		data, err := fs.ReadFile(solutions.Keys, n)
		if err != nil {
			return nil, err
		}
		if err := add("solutions/"+n, data); err != nil {
			return nil, err
		}
	}
	local, _ := filepath.Glob(p.Abs(paths.KeysDir + "/*.pub"))
	sort.Strings(local)
	for _, n := range local {
		data, err := os.ReadFile(n)
		if err != nil {
			return nil, err
		}
		if err := add(n, data); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

func sigProblem(format string, a ...any) *problem.Problem {
	return problem.Newf(problem.LockSignatureInvalid, format, a...)
}

// VerifyFile checks the signature of data with the trusted keys. what names the file in the problem.
func VerifyFile(keys minisign.Keys, data, sig []byte, what string) (minisign.PublicKey, error) {
	if len(keys) == 0 {
		return minisign.PublicKey{}, sigProblem("%s: this agent trusts no signing key (solutions/keys/*.pub of the build, %s/*.pub).", what, paths.KeysDir)
	}
	k, err := keys.Verify(data, sig)
	if err != nil {
		return k, sigProblem("%s: %v", what, err)
	}
	return k, nil
}

// verifyRemote downloads <url>.minisig and checks data when the client requires signatures. It returns the key id
// of the signature ("" when the client requires none).
func (c Client) verifyRemote(ctx context.Context, url string, data []byte) (string, error) {
	if !c.RequireSigned {
		return "", nil
	}
	resp, sig, err := c.get(ctx, url+SigSuffix, nil, MaxSigBytes)
	if err != nil {
		return "", sigProblem("GET %s%s failed: %v", url, SigSuffix, err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", sigProblem("GET %s%s gave HTTP %d: the file has no signature.", url, SigSuffix, resp.StatusCode)
	}
	k, err := VerifyFile(c.Keys, data, sig, url)
	if err != nil {
		return "", err
	}
	return k.KeyID(), nil
}

// verifyLocal checks the signature file next to a local file (a lock file of the CLI, the dist snapshot). It returns
// the key id of the signature ("" when the client requires none).
func (c Client) verifyLocal(file string, data []byte) (string, error) {
	if !c.RequireSigned {
		return "", nil
	}
	sig, err := os.ReadFile(file + SigSuffix)
	if err != nil {
		return "", sigProblem("%s has no signature file %s%s.", file, filepath.Base(file), SigSuffix)
	}
	k, err := VerifyFile(c.Keys, data, sig, strings.TrimPrefix(file, c.Paths.Abs("/")))
	if err != nil {
		return "", err
	}
	return k.KeyID(), nil
}
