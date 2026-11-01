package fe

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// InstallBundle downloads a tar.gz bundle (https:// or file://), checks its SHA-256 (when wantSHA256
// is not ""), extracts it, runs check on the extracted root, and moves the root to dst (a path below
// the root of the host). Phase 4 uses it for the FE plug bundles and the solution lock bundles
// (docs/solutions.md, section 8.2). It returns the SHA-256 and the size of the file.
func InstallBundle(ctx context.Context, url, wantSHA256, dst string, check func(root string) error) (string, int64, error) {
	a := NewArtifact(Env{})
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", 0, err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dst), ".tmp-"+filepath.Base(dst)+"-")
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	file := filepath.Join(tmp, "bundle.tar.gz")
	sum, size, err := a.download(ctx, url, file, false)
	if err != nil {
		return "", 0, err
	}
	if wantSHA256 != "" && wantSHA256 != sum {
		return sum, size, fmt.Errorf("the SHA-256 of %s is %s, but the lock has %s", url, sum, wantSHA256)
	}
	root := filepath.Join(tmp, "root")
	if err := extractTarGz(file, root); err != nil {
		return sum, size, fmt.Errorf("cannot extract %s: %w", url, err)
	}
	if check != nil {
		if err := check(root); err != nil {
			return sum, size, err
		}
	}
	if err := os.Chmod(root, 0o755); err != nil {
		return sum, size, err
	}
	if err := os.RemoveAll(dst); err != nil {
		return sum, size, err
	}
	if err := os.Rename(root, dst); err != nil {
		return sum, size, fmt.Errorf("cannot move the bundle to %s: %w", dst, err)
	}
	return sum, size, nil
}
