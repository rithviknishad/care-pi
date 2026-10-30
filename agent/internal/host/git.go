package host

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Git lists the refs of a remote repository, like "git ls-remote".
type Git interface {
	// LsRemote returns a map from ref name (for example "refs/heads/develop") to commit sha.
	// Peeled tags appear as "refs/tags/<tag>^{}".
	LsRemote(ctx context.Context, repo string) (map[string]string, error)
}

// HTTPGit reads the ref advertisement of the git smart HTTP protocol.
// The host needs no git binary.
type HTTPGit struct {
	Client *http.Client
}

// LsRemote implements Git.
func (g HTTPGit) LsRemote(ctx context.Context, repo string) (map[string]string, error) {
	c := g.Client
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	u := strings.TrimSuffix(repo, "/")
	if !strings.HasSuffix(u, ".git") {
		u += ".git"
	}
	u += "/info/refs?service=git-upload-pack"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "git/2.45.0 care-pi")
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s: %w", repo, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned HTTP %d", u, resp.StatusCode)
	}
	return ParseRefAdvertisement(resp.Body)
}

// ParseRefAdvertisement reads pkt-lines of a git-upload-pack ref advertisement.
func ParseRefAdvertisement(r io.Reader) (map[string]string, error) {
	br := bufio.NewReader(r)
	refs := map[string]string{}
	for {
		head := make([]byte, 4)
		if _, err := io.ReadFull(br, head); err != nil {
			if err == io.EOF {
				return refs, nil
			}
			return nil, fmt.Errorf("cannot read the ref list: %w", err)
		}
		n, err := strconv.ParseUint(string(head), 16, 16)
		if err != nil {
			return nil, fmt.Errorf("bad pkt-line length %q", head)
		}
		if n == 0 {
			continue
		}
		if n < 4 {
			return nil, fmt.Errorf("bad pkt-line length %d", n)
		}
		buf := make([]byte, n-4)
		if _, err := io.ReadFull(br, buf); err != nil {
			return nil, fmt.Errorf("cannot read the ref list: %w", err)
		}
		line := strings.TrimSuffix(string(buf), "\n")
		if strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.IndexByte(line, 0); i >= 0 {
			line = line[:i]
		}
		sha, name, ok := strings.Cut(line, " ")
		if !ok || len(sha) != 40 {
			continue
		}
		refs[name] = sha
	}
}

// ResolveRef picks the commit sha for ref from a ref list.
// Order: full sha, HEAD, branch, peeled tag, tag.
func ResolveRef(refs map[string]string, ref string) (string, bool) {
	if IsFullSHA(ref) {
		return ref, true
	}
	for _, name := range []string{
		ref,
		"refs/heads/" + ref,
		"refs/tags/" + ref + "^{}",
		"refs/tags/" + ref,
	} {
		if name == ref && ref != "HEAD" && !strings.HasPrefix(ref, "refs/") {
			continue
		}
		if sha, ok := refs[name]; ok {
			return sha, true
		}
	}
	return "", false
}

// IsFullSHA reports whether s is a 40-character hex commit id.
func IsFullSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
