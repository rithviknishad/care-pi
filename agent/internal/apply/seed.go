package apply

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/secrets"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
)

// The pre-migrated database of the Raspberry Pi image (S72, docs/image.md section 6.5). The image build ran
// migrate, sync_permissions_roles, and sync_valueset of the CARE image of its lock against an empty PostgreSQL and
// stored the data directory as /var/lib/care-pi/seed/pg-<sha12>.tar.zst. The first start of care.target on a device
// with an empty data directory extracts it, sets the password of the device, and Care then runs only the migrations
// that a newer lock adds (on a Pi 5 the migrate of an empty database takes about 3 min 20 s).

// SeedPasswordContainer is the name of the one-time container that sets the password.
const SeedPasswordContainer = "care-seed-password"

// SeedPasswordScript sets the password of the user postgres to the podman secret of the device (env SEED_PASSWORD)
// with PostgreSQL in single-user mode: no server, no network. The password has only letters and digits
// (secrets.Generate). The helper allows exactly this script (internal/helper).
const SeedPasswordScript = `case "$SEED_PASSWORD" in "" | *[!A-Za-z0-9]*) echo "the password is empty or has other characters" >&2; exit 2 ;; esac; ` +
	`out="$(echo "ALTER USER postgres PASSWORD '$SEED_PASSWORD';" | postgres --single -D /var/lib/postgresql/data postgres 2>&1)"; ` +
	`rc=$?; case "$out" in *ERROR:*) echo "$out" >&2; exit 1 ;; esac; exit $rc`

// SeedPasswordCmd returns the command of the password change: a transient unit with podman run of the postgres image.
func SeedPasswordCmd(image string) host.Cmd {
	return host.Cmd{Name: "systemd-run", Args: []string{"--quiet", "--wait", "--pipe", "--collect",
		"--property=MemoryMax=512M", "--property=MemorySwapMax=256M", "--description=care-pi " + SeedPasswordContainer,
		"--", "podman", "run", "--rm", "--name", SeedPasswordContainer, "--network", "none", "--user", "postgres",
		"--volume", paths.PGData + ":/var/lib/postgresql/data",
		"--secret", secrets.PodmanPostgresPassword + ",type=env,target=SEED_PASSWORD",
		image, "sh", "-c", SeedPasswordScript}}
}

// SeedRecord is state/seed-db.json.
type SeedRecord struct {
	File        string    `json:"file"`
	CareGitSHA  string    `json:"care_git_sha"`
	RestoredAt  time.Time `json:"restored_at"`
	PasswordSet bool      `json:"password_set"`
}

func readSeedRecord(p paths.Paths) (SeedRecord, bool) {
	var r SeedRecord
	data, err := os.ReadFile(p.Abs(paths.SeedRecord))
	if err != nil || json.Unmarshal(data, &r) != nil {
		return SeedRecord{}, false
	}
	return r, true
}

func writeSeedRecord(p paths.Paths, r SeedRecord) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.Abs(paths.StateDir), 0o755); err != nil {
		return err
	}
	return lock.WriteFileAtomic(p.Abs(paths.SeedRecord), append(data, '\n'), 0o600)
}

// chooseSeed returns the seed of the CARE commit careSHA, else the newest seed. sha is the commit of the seed (from
// its .json, else from its name).
func chooseSeed(p paths.Paths, careSHA string) (file, sha string, ok bool) {
	matches, _ := filepath.Glob(p.Abs(paths.SeedDir + "/pg-*.tar.zst"))
	if len(matches) == 0 {
		return "", "", false
	}
	shaOf := func(f string) string {
		var meta struct {
			CareGitSHA string `json:"care_git_sha"`
		}
		if data, err := os.ReadFile(strings.TrimSuffix(f, ".tar.zst") + ".json"); err == nil && json.Unmarshal(data, &meta) == nil && meta.CareGitSHA != "" {
			return meta.CareGitSHA
		}
		return strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), "pg-"), ".tar.zst")
	}
	if careSHA != "" {
		for _, f := range matches {
			if s := shaOf(f); s == careSHA || s == solution.GitTag(careSHA) {
				return f, s, true
			}
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		a, _ := os.Stat(matches[i])
		b, _ := os.Stat(matches[j])
		return a != nil && b != nil && a.ModTime().After(b.ModTime())
	})
	return matches[0], shaOf(matches[0]), true
}

// careSHAOf returns the CARE commit of the target: components.care of the lock.
func careSHAOf(pr *Prepared) string {
	if pr == nil || pr.Lock == nil || pr.Lock.Components.Care == nil {
		return ""
	}
	return pr.Lock.Components.Care.GitSHA
}

// seedDB restores the seed into an empty data directory and sets the password of the device, before the first start
// of care-db (S72). It does nothing when the data directory has a database (PG_VERSION) and no password change is
// pending, or when the image has no seed. When the password change fails right after the restore, it removes the
// restored data: care-db then initializes an empty database, as without a seed. A pending password change of an
// earlier run (an interrupted apply) never removes data: it fails, and the next apply tries again.
func seedDB(ctx context.Context, o Options, pr *Prepared) error {
	p := o.Paths
	pg := p.Abs(paths.PGData)
	rec, recOK := readSeedRecord(p)
	restored := false
	if _, err := os.Stat(filepath.Join(pg, "PG_VERSION")); err == nil {
		if !recOK || rec.PasswordSet {
			return nil
		}
	} else {
		if entries, err := os.ReadDir(pg); err == nil && len(entries) > 0 {
			return nil
		}
		careSHA := careSHAOf(pr)
		file, sha, ok := chooseSeed(p, careSHA)
		if !ok {
			return nil
		}
		_, _ = fmt.Fprintf(o.Out, "    seed-db: restore the pre-migrated database of CARE %s (%s)\n", solution.GitTag(sha), filepath.Base(file))
		if careSHA != "" && sha != careSHA && sha != solution.GitTag(careSHA) {
			_, _ = fmt.Fprintf(o.Out, "    seed-db: the seed is of CARE %s, the lock has CARE %s: migrate applies the difference\n",
				solution.GitTag(sha), solution.GitTag(careSHA))
		}
		start := time.Now()
		if err := extractSeed(file, pg, o.Chown); err != nil {
			return fmt.Errorf("cannot restore the database seed %s: %w", file, err)
		}
		_, _ = fmt.Fprintf(o.Out, "    seed-db: restored in %s\n", time.Since(start).Round(100*time.Millisecond))
		restored = true
		rec = SeedRecord{File: filepath.Base(file), CareGitSHA: sha, RestoredAt: time.Now().UTC().Truncate(time.Second)}
		if err := writeSeedRecord(p, rec); err != nil {
			return err
		}
	}
	image := ""
	if pr != nil && pr.Lock != nil {
		image = pr.Lock.Images["postgres"].Pull()
	}
	if image == "" {
		return errors.New("seed-db: the lock has no postgres image")
	}
	_, _ = fmt.Fprintln(o.Out, "    seed-db: set the database password of this device")
	if out, err := o.Host.Runner.Run(ctx, SeedPasswordCmd(image)); err != nil {
		if !restored {
			return fmt.Errorf("seed-db: the password change failed: %w: %s", err, strings.TrimSpace(string(out)))
		}
		_, _ = fmt.Fprintf(o.Out, "    seed-db: the password change failed (%v): %s; remove the seed, and care-db initializes an empty database\n",
			err, strings.TrimSpace(string(out)))
		if err := emptyDir(pg); err != nil {
			return fmt.Errorf("cannot empty %s after the failed password change: %w", paths.PGData, err)
		}
		_ = os.Remove(p.Abs(paths.SeedRecord))
		return nil
	}
	rec.PasswordSet = true
	return writeSeedRecord(p, rec)
}

// emptyDir removes the content of dir and keeps dir (its owner and mode).
func emptyDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// extractSeed extracts a .tar.zst of a data directory into dir (empty): first into a sibling directory, then a
// rename. With chown, each entry gets the owner of the postgres image (999:999).
func extractSeed(file, dir string, chown bool) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	zr, err := zstd.NewReader(f)
	if err != nil {
		return err
	}
	defer zr.Close()
	tmp := dir + ".seed"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return err
	}
	own := func(path string) error {
		if !chown {
			return nil
		}
		return os.Lchown(path, paths.PostgresUID, paths.PostgresGID)
	}
	tr := tar.NewReader(zr)
	for {
		hd, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			_ = os.RemoveAll(tmp)
			return err
		}
		clean := filepath.Clean(strings.TrimPrefix(hd.Name, "./"))
		if clean == "." {
			continue
		}
		if clean == ".." || strings.HasPrefix(clean, "../") || filepath.IsAbs(clean) {
			_ = os.RemoveAll(tmp)
			return fmt.Errorf("the seed entry %s leaves the directory", hd.Name)
		}
		target := filepath.Join(tmp, clean)
		mode := os.FileMode(hd.Mode & 0o777)
		switch hd.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(target, mode|0o700)
			if err == nil {
				err = os.Chmod(target, mode|0o700)
			}
		case tar.TypeReg:
			var w *os.File
			if w, err = os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode|0o600); err == nil {
				_, err = io.Copy(w, tr)
				if cerr := w.Close(); err == nil {
					err = cerr
				}
			}
		case tar.TypeSymlink:
			err = os.Symlink(hd.Linkname, target)
		default:
			continue
		}
		if err == nil {
			err = own(target)
		}
		if err != nil {
			_ = os.RemoveAll(tmp)
			return err
		}
	}
	if err := os.Chmod(tmp, 0o700); err != nil {
		return err
	}
	if err := own(tmp); err != nil {
		return err
	}
	// The data directory is empty: replace it.
	if err := os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.RemoveAll(tmp)
		return err
	}
	return os.Rename(tmp, dir)
}
