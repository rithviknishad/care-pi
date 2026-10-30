package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/fe"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/setup"
)

// FELastBuild is the fe record of lock.yaml (schema FeLastBuild).
type FELastBuild struct {
	Provider string  `json:"provider"`
	BuiltAt  *string `json:"built_at"`
	Duration *string `json:"duration"`
	URL      *string `json:"url"`
	SHA256   *string `json:"sha256"`
}

// FEBuild is one FE build on disk (schema FeBuild).
type FEBuild struct {
	ID         string    `json:"id"`
	Bytes      int64     `json:"bytes"`
	ModifiedAt time.Time `json:"modified_at"`
	Current    bool      `json:"current"`
}

// FEStatus is the FE build state (schema FeStatus).
type FEStatus struct {
	Provider      string       `json:"provider"`
	BuildID       *string      `json:"build_id"`
	CareFESHA     *string      `json:"care_fe_sha"`
	Current       *string      `json:"current"`
	LastBuild     *FELastBuild `json:"last_build"`
	Builds        []FEBuild    `json:"builds"`
	ScaleDownLeft bool         `json:"scale_down_left"`
	Building      bool         `json:"building"`
}

// FE returns the FE build state (care-pi fe status).
func (s *Service) FE() (*FEStatus, error) {
	cfg, err := config.Load(s.configPath())
	if err != nil {
		cfg = config.Default()
	}
	disk, err := lock.Read(s.Paths.Abs(paths.LockFile))
	if err != nil {
		return nil, err
	}
	out := &FEStatus{Provider: cfg.FE.Provider, Current: strPtr(fe.Current(s.Paths)), Builds: []FEBuild{},
		ScaleDownLeft: fe.ScaleDownLeft(s.Paths), Building: s.FEBuilding()}
	if disk != nil {
		out.BuildID = strPtr(disk.FE.BuildID)
		out.CareFESHA = strPtr(disk.Git["care_fe"].SHA)
		r := disk.FE
		if (r.Provider == "artifact" || r.Provider == "local") && (r.BuiltAt != "" || r.URL != "") {
			out.LastBuild = &FELastBuild{Provider: r.Provider, BuiltAt: strPtr(r.BuiltAt), Duration: strPtr(r.Duration),
				URL: strPtr(r.URL), SHA256: strPtr(r.SHA256)}
		}
	}
	builds, err := fe.List(s.Paths)
	if err != nil {
		return nil, err
	}
	for _, b := range builds {
		out.Builds = append(out.Builds, FEBuild{ID: b.ID, Bytes: b.Bytes, ModifiedAt: b.ModTime.UTC().Truncate(time.Second), Current: b.Current})
	}
	return out, nil
}

// FEBuildOptions controls RunFEBuild.
type FEBuildOptions struct {
	Force  bool
	Out    io.Writer
	Follow bool
}

// RunFEBuild makes the FE build of the lock with the provider of config.yaml and points fe/current
// at it (care-pi fe build). The caller holds the apply lock.
func (s *Service) RunFEBuild(ctx context.Context, o FEBuildOptions) (*jobs.FEBuildResult, error) {
	w := o.Out
	if w == nil {
		w = io.Discard
	}
	p := s.Paths
	start := time.Now()
	// Offline: the lock already has the care_fe sha. An image without a locked digest does not matter here.
	pr, err := apply.Prepare(ctx, apply.PrepareOptions{Paths: p, ConfigPath: s.configPath(), SolutionsDir: s.SolutionsDir,
		IPs: s.IPs, Offline: true, Phase: setup.PhaseFull})
	if err != nil {
		return nil, applyProblem(err)
	}
	if pr.Config.FE.Provider == "none" {
		return nil, exitcode.With(exitcode.BadInput, problem.Invalid("/fe/provider",
			"fe.provider is none; set fe.provider to artifact or local, then run care-pi apply", "enum"))
	}
	disk, err := lock.Read(p.Abs(paths.LockFile))
	if err != nil {
		return nil, err
	}
	id := pr.Model.FEBuildID
	switch {
	case disk == nil || disk.FE.BuildID == "":
		return nil, errors.New("lock.yaml has no FE build id; run care-pi apply")
	case id != disk.FE.BuildID || disk.FE.Provider != pr.Config.FE.Provider:
		return nil, fmt.Errorf("config.yaml changed after the last apply (config: FE build %s, provider %s; lock.yaml: FE build %s, provider %s); run care-pi apply, which also makes the FE build",
			id, pr.Config.FE.Provider, disk.FE.BuildID, disk.FE.Provider)
	}
	if o.Force && fe.Exists(p, id) {
		if fe.Current(p) == id {
			_, _ = fmt.Fprintln(w, "warning: FE build "+id+" is current; the site shows the setup page until the new build is ready")
		}
		if err := os.RemoveAll(p.Abs(fe.Dir(id))); err != nil {
			return nil, fmt.Errorf("cannot remove FE build %s: %w", id, err)
		}
	}
	prov, err := fe.New(pr.Config.FE.Provider, fe.Env{Config: pr.Config, Paths: p, CareFERepo: disk.Git["care_fe"].Repo,
		CareFESHA: disk.Git["care_fe"].SHA, BuildEnv: pr.Desire.Frontend, Host: s.Host, Out: w, Follow: o.Follow})
	if err != nil {
		return nil, exitcode.With(exitcode.BadInput, err)
	}
	_, _ = fmt.Fprintf(w, "FE build %s (provider %s)\n", id, prov.Name())
	b, err := prov.Ensure(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := fe.SetCurrent(p, id); err != nil {
		return nil, err
	}
	disk.FE = fe.MergeRecord(disk.FE, b.Record)
	if err := lock.Write(p, disk, time.Now()); err != nil {
		return nil, fmt.Errorf("cannot record the FE build in lock.yaml: %w", err)
	}
	_, _ = fmt.Fprintf(w, "fe/current -> %s\n", id)
	return &jobs.FEBuildResult{Kind: jobs.KindFEBuild, BuildID: id, Provider: prov.Name(),
		DurationS: float64(time.Since(start).Milliseconds()) / 1000}, nil
}

func (s *Service) requireReady() error {
	if s.SetupStateName() != setup.Ready {
		return problem.New(problem.SetupIncomplete, "Finish the setup first.")
	}
	return nil
}

// StartFEBuild starts a job of kind fe-build. It is cancellable.
func (s *Service) StartFEBuild(ctx context.Context, force bool, source string) (*jobs.Job, error) {
	if err := s.requireReady(); err != nil {
		return nil, err
	}
	cfg, err := s.Config()
	if err != nil {
		return nil, err
	}
	if cfg.FE.Provider == "none" {
		return nil, problem.New(problem.UnsupportedConfig, "fe.provider is none; set fe.provider to artifact or local first.").
			WithErrors(problem.FieldError{Pointer: "/fe/provider", Message: "fe.provider is none", Code: "enum"})
	}
	return s.Jobs.Start(ctx, jobs.Spec{Kind: jobs.KindFEBuild, Source: source, Lock: true, Cancellable: true},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			end := rec.StepAction("ensure-fe: build of the lock", "ensure-fe")
			r, err := s.RunFEBuild(ctx, FEBuildOptions{Force: force, Out: rec.Writer()})
			end(err)
			if err != nil {
				return nil, err
			}
			return r, nil
		})
}

// StartFEPrune starts a job of kind fe-prune that keeps the keep newest builds and the current build.
func (s *Service) StartFEPrune(ctx context.Context, keep int, source string) (*jobs.Job, error) {
	if err := s.requireReady(); err != nil {
		return nil, err
	}
	return s.Jobs.Start(ctx, jobs.Spec{Kind: jobs.KindFEPrune, Source: source, Lock: true},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			end := rec.StepAction("prune: keep "+fmt.Sprint(keep), "prune")
			removed, err := fe.Prune(ctx, s.Paths, keep)
			for _, r := range removed {
				rec.Logf("removed %s", r)
			}
			end(err)
			if err != nil {
				return nil, err
			}
			if removed == nil {
				removed = []string{}
			}
			return jobs.FEPruneResult{Kind: jobs.KindFEPrune, Removed: removed}, nil
		})
}
