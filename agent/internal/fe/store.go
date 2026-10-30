package fe

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// Info describes one build on the host.
type Info struct {
	ID      string
	Bytes   int64
	ModTime time.Time
	Current bool
}

// List returns the builds in /var/lib/care-pi/fe, the newest first.
// A build is a directory with an index.html. List skips the temporary directories.
func List(p paths.Paths) ([]Info, error) {
	dir := p.Abs(paths.FEDir)
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", paths.FEDir, err)
	}
	cur := Current(p)
	var out []Info
	for _, e := range ents {
		if !e.IsDir() || !validID(e.Name()) || !Exists(p, e.Name()) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, Info{
			ID:      e.Name(),
			Bytes:   dirSize(filepath.Join(dir, e.Name())),
			ModTime: fi.ModTime(),
			Current: e.Name() == cur,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].ModTime.Equal(out[j].ModTime) {
			return out[i].ModTime.After(out[j].ModTime)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func dirSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

// leftover reports whether name is a temporary directory of an incomplete build.
func leftover(name string) bool {
	return strings.HasPrefix(name, ".tmp-") || strings.HasSuffix(name, ".tmp")
}

// Prune removes all builds except the keep newest ones and the current build.
// It also removes the temporary directories of incomplete builds.
// It returns the names that it removed. It fails with ErrBusy during a build.
func Prune(ctx context.Context, p paths.Paths, keep int) ([]string, error) {
	if keep < 1 {
		return nil, fmt.Errorf("keep must be 1 or more, not %d", keep)
	}
	unlock, err := takeLock(ctx, p, false)
	if err != nil {
		return nil, err
	}
	defer unlock()
	builds, err := List(p)
	if err != nil {
		return nil, err
	}
	var removed []string
	kept := 0
	for _, b := range builds {
		if b.Current || kept < keep {
			kept++
			continue
		}
		if err := os.RemoveAll(p.Abs(Dir(b.ID))); err != nil {
			return removed, fmt.Errorf("cannot remove FE build %s: %w", b.ID, err)
		}
		removed = append(removed, b.ID)
	}
	ents, err := os.ReadDir(p.Abs(paths.FEDir))
	if err != nil {
		return removed, err
	}
	for _, e := range ents {
		if e.IsDir() && leftover(e.Name()) {
			if err := os.RemoveAll(filepath.Join(p.Abs(paths.FEDir), e.Name())); err != nil {
				return removed, fmt.Errorf("cannot remove %s: %w", e.Name(), err)
			}
			removed = append(removed, e.Name())
		}
	}
	return removed, nil
}
