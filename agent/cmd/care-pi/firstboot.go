package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/update"
)

// The first boot of the Raspberry Pi image: "care-pi bootstrap --from-image" (docs/image.md).

// imageRelease is /etc/care-pi/image-release.json. The image build writes it.
type imageRelease struct {
	Version     string `json:"version"`
	Solution    string `json:"solution"`
	LockVersion string `json:"lock_version"`
	BuiltAt     string `json:"built_at"`
	GitSHA      string `json:"git_sha"`
}

func readImageRelease(p paths.Paths) (json.RawMessage, *imageRelease) {
	data, err := os.ReadFile(p.Abs(paths.ImageRelease))
	if err != nil {
		return nil, nil
	}
	var r imageRelease
	if json.Unmarshal(data, &r) != nil || !json.Valid(data) {
		return nil, nil
	}
	return json.RawMessage(data), &r
}

// imageInfo prints the image release.
func imageInfo(w io.Writer, p paths.Paths) {
	if _, r := readImageRelease(p); r != nil {
		_, _ = fmt.Fprintf(w, "==> image %s: %s %s, built %s (care-pi %.12s)\n", r.Version, r.Solution, r.LockVersion, r.BuiltAt, r.GitSHA)
		return
	}
	_, _ = fmt.Fprintf(w, "warning: %s is missing; this host does not come from the care-pi image\n", paths.ImageRelease)
}

// installPreseed installs the pre-seed config.yaml of the image as config.yaml, when config.yaml does
// not exist. cloud-init writes the pre-seed from the user-data (write_files). It returns true when a
// pre-seed exists: bootstrap then applies everything (--unattended).
// A pre-seed that cannot be used (not readable, not valid, or a feature that this version does not have) does not
// stop the first boot, so the device is never silent: bootstrap starts setup mode, the file becomes
// preseed.yaml.rejected, and state/firstboot-error.json records why. The setup pages show it (firstboot_error).
func installPreseed(w io.Writer, p paths.Paths, cfgPath string) (bool, error) {
	data, err := os.ReadFile(p.Abs(paths.PreseedConfig))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err == nil {
		err = checkPreseed(p, data)
	}
	if err != nil {
		return false, rejectPreseed(w, p, err)
	}
	if _, err := os.Stat(cfgPath); err == nil {
		_, _ = fmt.Fprintf(w, "==> pre-seed %s: keep the existing %s\n", paths.PreseedConfig, cfgPath)
		return true, nil
	}
	if err := lock.WriteFileAtomic(cfgPath, data, 0o600); err != nil {
		return false, fmt.Errorf("cannot write %s: %w", cfgPath, err)
	}
	_, _ = fmt.Fprintf(w, "==> installed the pre-seed %s as %s; apply everything now, no console setup\n", paths.PreseedConfig, cfgPath)
	return true, nil
}

// checkPreseed validates the pre-seed and runs the apply-time checks on it, before it becomes config.yaml.
func checkPreseed(p paths.Paths, data []byte) error {
	cfg, err := config.Parse(data, paths.PreseedConfig)
	if err != nil {
		return fmt.Errorf("the pre-seed is not valid: %w", err)
	}
	if err := apply.CheckImplemented(p, cfg); err != nil {
		return fmt.Errorf("the pre-seed asks for something that this version cannot do: %w", err)
	}
	return nil
}

// FirstbootError is state/firstboot-error.json.
type FirstbootError struct {
	At     time.Time `json:"at"`
	File   string    `json:"file"`
	Error  string    `json:"error"`
	Action string    `json:"action"`
}

// rejectPreseed records why the pre-seed is not used and moves it away: the first boot continues in setup mode.
func rejectPreseed(w io.Writer, p paths.Paths, cause error) error {
	rejected := paths.PreseedConfig + ".rejected"
	rec := FirstbootError{At: time.Now().UTC().Truncate(time.Second), File: rejected, Error: cause.Error(),
		Action: "The device starts setup mode: claim it in the console. Or correct the user-data and flash the card again."}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	if err := lock.WriteFileAtomic(p.Abs(paths.FirstbootError), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("cannot write %s: %w", paths.FirstbootError, err)
	}
	if err := os.Rename(p.Abs(paths.PreseedConfig), p.Abs(rejected)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("cannot move the pre-seed to %s: %w", rejected, err)
	}
	_, _ = fmt.Fprintf(w, "warning: %v\n==> the pre-seed is not used (now %s); start setup mode. The console shows this error\n", cause, rejected)
	return nil
}

// importLocalDist puts the dist snapshot of the image into the update cache. Without internet, the
// first apply then installs the solution lock of the image. A failure is a warning: with internet,
// the agent gets the index from updates.index_url.
func importLocalDist(w io.Writer, p paths.Paths) {
	ix, err := (update.Client{Paths: p}).ImportLocalDist(paths.LocalDist)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		_, _ = fmt.Fprintf(w, "warning: the image has no dist snapshot (%s/index/index.json)\n", paths.LocalDist)
	case err != nil:
		_, _ = fmt.Fprintf(w, "warning: cannot use the dist snapshot %s: %v\n", paths.LocalDist, err)
	default:
		n := 0
		for _, s := range ix.Solutions {
			n += len(s.Locks)
		}
		_, _ = fmt.Fprintf(w, "==> dist snapshot %s: %d solution lock(s) in the update cache\n", paths.LocalDist, n)
	}
}

// firstbootRecord is the content of the firstboot-done marker.
type firstbootRecord struct {
	DoneAt       time.Time       `json:"done_at"`
	Phase        string          `json:"phase"`
	Preseed      bool            `json:"preseed"`
	ImageRelease json.RawMessage `json:"image_release"`
}

// finishFirstboot writes the firstboot-done marker, and renames an installed pre-seed to *.applied.
func finishFirstboot(w io.Writer, p paths.Paths, phase string, preseed bool) error {
	raw, _ := readImageRelease(p)
	if raw == nil {
		raw = json.RawMessage("null")
	}
	data, err := json.MarshalIndent(firstbootRecord{DoneAt: time.Now().UTC().Truncate(time.Second), Phase: phase, Preseed: preseed, ImageRelease: raw}, "", "  ")
	if err != nil {
		return err
	}
	if err := lock.WriteFileAtomic(p.Abs(paths.FirstbootDone), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("cannot write %s: %w", paths.FirstbootDone, err)
	}
	if preseed {
		src := p.Abs(paths.PreseedConfig)
		if err := os.Rename(src, src+".applied"); err != nil && !errors.Is(err, fs.ErrNotExist) {
			_, _ = fmt.Fprintf(w, "warning: cannot rename %s: %v\n", paths.PreseedConfig, err)
		}
	}
	_, _ = fmt.Fprintf(w, "==> first boot done (%s)\n", paths.FirstbootDone)
	return nil
}
