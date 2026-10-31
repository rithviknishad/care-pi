package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// imagesState is state/images.json: the images that care-pi pulled or built (docs/solutions.md, section 8.7).
type imagesState struct {
	Images []string `json:"images"`
}

func readImages(p paths.Paths) imagesState {
	var st imagesState
	if data, err := os.ReadFile(p.Abs(paths.ImagesState)); err == nil {
		_ = json.Unmarshal(data, &st)
	}
	return st
}

// recordImages adds images to state/images.json.
func recordImages(p paths.Paths, refs []string) {
	if len(refs) == 0 {
		return
	}
	st := readImages(p)
	seen := map[string]bool{}
	for _, r := range st.Images {
		seen[r] = true
	}
	for _, r := range refs {
		if !seen[r] {
			st.Images = append(st.Images, r)
			seen[r] = true
		}
	}
	sort.Strings(st.Images)
	_ = writeJSON(p.Abs(paths.ImagesState), st)
}

// lockImages returns the pull references of the images of a lock, with the base of a local build.
func lockImages(l *lock.Lock) map[string]bool {
	out := map[string]bool{}
	if l == nil {
		return out
	}
	for _, img := range l.Images {
		out[img.Pull()] = true
	}
	if c := l.Components.Care; c != nil && c.Build != nil {
		out[c.Image+"@"+c.Build.BaseDigest] = true
	}
	return out
}

// pruneImages removes each image that care-pi pulled or built and that neither the current lock nor
// history.previous uses. Then "podman image prune -f" removes the dangling layers. It never removes
// an image that care-pi did not pull.
func (u *Updater) pruneImages(ctx context.Context, out io.Writer) (int, error) {
	cur, err := lock.Load(u.Paths)
	if err != nil || cur == nil {
		return 0, err
	}
	keep := lockImages(cur)
	if id := cur.History.PreviousID(); id != "" {
		if prev, err := lock.ReadHistory(u.Paths, id); err == nil {
			for k := range lockImages(prev) {
				keep[k] = true
			}
		}
	}
	st := readImages(u.Paths)
	var left []string
	n := 0
	for _, ref := range st.Images {
		if keep[ref] {
			left = append(left, ref)
			continue
		}
		if _, err := u.Host.Runner.Run(ctx, host.Cmd{Name: "podman", Args: []string{"image", "rm", ref}}); err != nil {
			_, _ = fmt.Fprintf(out, "    keep %s: %v\n", ref, err)
			left = append(left, ref)
			continue
		}
		_, _ = fmt.Fprintln(out, "    removed", ref)
		n++
	}
	st.Images = left
	_ = writeJSON(u.Paths.Abs(paths.ImagesState), st)
	if _, err := u.Host.Runner.Run(ctx, host.Cmd{Name: "podman", Args: []string{"image", "prune", "-f"}}); err != nil {
		return n, err
	}
	return n, nil
}

// imagesPresent reports whether each image of a lock is in the local storage.
func (u *Updater) imagesPresent(ctx context.Context, l *lock.Lock) bool {
	if l == nil || u.Host == nil {
		return false
	}
	for name, img := range l.Images {
		if name == "node" {
			continue
		}
		ok, err := u.Host.Podman.ImageExists(ctx, img.Pull())
		if err != nil || !ok {
			return false
		}
	}
	return true
}
