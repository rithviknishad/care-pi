package restore

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/backup"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
)

// Targets of a files restore.
const (
	InPlace = "in-place"
	SideDir = "side-dir"
)

// FilesRequest is one files restore.
type FilesRequest struct {
	Dest       config.BackupDestination
	SnapshotID string
	Paths      []string
	Target     string
	Source     string
}

// FilesResult is the result of a restore-files job (schema RestoreFilesResult).
type FilesResult struct {
	Kind         string    `json:"kind"`
	Destination  string    `json:"destination"`
	SnapshotID   string    `json:"snapshot_id"`
	SnapshotTime time.Time `json:"snapshot_time"`
	Target       string    `json:"target"`
	Path         string    `json:"path"`
	Files        int64     `json:"files"`
	Bytes        int64     `json:"bytes"`
	Verified     bool      `json:"verified"`
}

// Files runs the steps of restore-files (docs/api/README.md section 13.9.2).
func Files(ctx context.Context, e Env, req FilesRequest, key backup.Key) (*FilesResult, error) {
	for _, d := range PruneOld(e.Paths, time.Now()) {
		e.logf("deleted the old directory %s", d)
	}
	res := &FilesResult{Kind: jobs.KindRestoreFiles, Destination: req.Dest.ID, SnapshotID: req.SnapshotID, Target: req.Target}
	target := paths.S3Data
	if req.Target == SideDir {
		target = paths.RestoreDir + "/" + e.Rec.ID()
	}
	res.Path = target
	s, err := backup.OpenDest(e.Env, e.Config, req.Dest, key)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	snap, err := findSnapshot(ctx, e, s, req.SnapshotID)
	if err != nil {
		return nil, err
	}
	if !snap.HasTag(backup.TagFiles) {
		return nil, problem.Invalid("/snapshot_id", "the snapshot has no files; choose a snapshot with files in contents", "no-files")
	}
	res.SnapshotTime = snap.Time.UTC().Truncate(time.Second)
	inPlace := req.Target != SideDir
	if inPlace {
		if err := e.PauseBackups(ctx); err != nil {
			return nil, err
		}
		defer func() { _ = e.ResumeBackups(context.WithoutCancel(ctx)) }()
	}
	if err := e.Step("check-space: "+target, "check-space", func() error {
		need := int64(0)
		if sz := snap.Size(); sz != nil {
			need = *sz + *sz/10
		}
		probe := target
		if !inPlace {
			probe = paths.RestoreDir
			if err := os.MkdirAll(e.Paths.Abs(probe), 0o700); err != nil {
				return err
			}
		}
		free, _, err := backup.Free(e.Paths.Abs(probe))
		if err != nil {
			return err
		}
		if free < need {
			return problem.NeedSpace(probe, need, free, "The restore needs the size of the files plus 10 %.")
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if inPlace {
		if err := e.Maintenance(true); err != nil {
			return nil, err
		}
		defer func() { _ = SetMaintenance(e.Paths, false) }()
		if err := e.Step("stop-s3: care-s3.service", "stop-s3", func() error { return e.StopUnits(ctx, "care-s3.service") }); err != nil {
			return nil, err
		}
	} else if err := os.MkdirAll(e.Paths.Abs(target), 0o700); err != nil {
		return nil, err
	}
	rerr := e.Step(fmt.Sprintf("restore-files: %s (%s) to %s", snap.ShortID, req.Dest.ID, target), "restore-files", func() error {
		_, err := s.Run(ctx, e.Runner, backup.RestoreArgs(snap.ID, paths.S3Data, target, req.Paths, true)...)
		return err
	})
	if rerr == nil {
		rerr = e.Step("verify-files: "+target, "verify-files", func() error {
			files, bytes, err := count(e.Paths.Abs(target), req.Paths)
			res.Files, res.Bytes, res.Verified = files, bytes, err == nil
			e.Rec.SetDetail(fmt.Sprintf("%d files, %d bytes", files, bytes))
			return err
		})
	}
	if inPlace {
		serr := e.Step("start-s3: care.target", "start-s3", func() error { return e.StartTarget(context.WithoutCancel(ctx)) })
		_ = e.Maintenance(false)
		if rerr == nil {
			rerr = serr
		}
	}
	if rerr != nil {
		return nil, rerr
	}
	return res, nil
}

func findSnapshot(ctx context.Context, e Env, s *backup.Session, id string) (*backup.Snapshot, error) {
	out, err := s.Run(ctx, e.Runner, "snapshots", "--json", id)
	if err != nil {
		return nil, err
	}
	list, err := backup.ParseSnapshots(out)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, problem.Newf(problem.NotFound, "The destination %s has no snapshot %s.", s.Repo.Dest.ID, id)
	}
	return &list[0], nil
}

// count walks the restored files (below the given paths) and counts them.
func count(root string, only []string) (int64, int64, error) {
	var files, bytes int64
	roots := []string{root}
	if len(only) > 0 {
		roots = nil
		for _, p := range only {
			roots = append(roots, filepath.Join(root, strings.TrimSuffix(p, "/")))
		}
	}
	for _, r := range roots {
		err := filepath.WalkDir(r, func(_ string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type().IsRegular() {
				info, err := d.Info()
				if err != nil {
					return err
				}
				files++
				bytes += info.Size()
			}
			return nil
		})
		if err != nil {
			return files, bytes, err
		}
	}
	return files, bytes, nil
}

// walk gives the size of each regular file below dir to fn.
func walk(dir string, fn func(int64)) error {
	return filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				fn(info.Size())
			}
		}
		return nil
	})
}

// SideDirMaxAge is the age of a side directory (and of the stage of a restore from backup) that
// PruneOld deletes (docs/api/README.md section 13.9.2).
const SideDirMaxAge = 7 * 24 * time.Hour

// DrillDirMaxAge is the age of a drill directory that PruneOld deletes. A drill that the agent
// could not clean up (a crash, a reboot) leaves its directory.
const DrillDirMaxAge = 24 * time.Hour

// PruneOld deletes the side directories of the files restores and the stages of restores from
// backup that are older than SideDirMaxAge, and the drill directories older than DrillDirMaxAge.
// The files restore and the daily timer of the incremental backup call it. It returns the deleted paths.
func PruneOld(p paths.Paths, now time.Time) []string {
	var out []string
	for _, d := range []struct {
		dir string
		age time.Duration
	}{{paths.RestoreDir, SideDirMaxAge}, {paths.DrillDir, DrillDirMaxAge}} {
		entries, err := os.ReadDir(p.Abs(d.dir))
		if err != nil {
			continue
		}
		for _, en := range entries {
			info, err := en.Info()
			if err != nil || now.Sub(info.ModTime()) < d.age {
				continue
			}
			full := p.Abs(d.dir + "/" + en.Name())
			if os.RemoveAll(full) == nil {
				out = append(out, full)
			}
		}
	}
	return out
}
