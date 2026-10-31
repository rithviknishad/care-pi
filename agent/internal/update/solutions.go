package update

import (
	"context"
	"sort"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
)

// indexState returns the cached index state.
func (u *Updater) indexState(cfg *config.Config) IndexState {
	is := IndexState{URL: IndexURL(cfg.Updates.IndexURL)}
	if url, fetched := IndexFetchedAt(u.Paths); url == is.URL {
		is.FetchedAt = timep(fetched)
	}
	if ix, _ := solution.CachedIndex(u.Paths); ix != nil {
		is.DistBase = strp(ix.DistBase)
		is.GeneratedAt = timep(parseTime(ix.GeneratedAt))
	}
	return is
}

// Solutions returns GET /solutions: the embedded solutions and the solutions of the cached index.
func (u *Updater) Solutions() (*SolutionList, error) {
	cfg, err := u.Config()
	if err != nil {
		cfg = config.Default()
	}
	ix, _ := solution.CachedIndex(u.Paths)
	names := map[string]bool{}
	for _, n := range u.embeddedNames() {
		names[n] = true
	}
	if ix != nil {
		for _, s := range ix.Solutions {
			names[s.Name] = true
		}
	}
	out := &SolutionList{Items: []SolutionSummary{}, Index: u.indexState(cfg)}
	var sorted []string
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	for _, n := range sorted {
		d, err := u.solutionDetail(cfg, ix, n)
		if err != nil {
			continue
		}
		s := SolutionSummary{Name: n, Title: d.Title, Description: d.Description, Source: d.Source, Selected: d.Selected,
			LockCount: len(d.Locks)}
		for _, l := range d.Locks {
			if !l.Withdrawn {
				v := l.Version
				s.LatestVersion = &v
				break
			}
		}
		if plugs, ok := d.Document["plugs"].([]any); ok {
			s.PlugCount = len(plugs)
		}
		out.Items = append(out.Items, s)
	}
	return out, nil
}

func (u *Updater) embeddedNames() []string {
	if u.SolutionsDir == "" {
		return solution.EmbeddedNames()
	}
	if _, err := solution.Load("care-clinic", u.SolutionsDir); err == nil {
		return []string{"care-clinic"}
	}
	return nil
}

// Solution returns GET /solutions/{name}.
func (u *Updater) Solution(name string) (*SolutionDetail, error) {
	cfg, err := u.Config()
	if err != nil {
		cfg = config.Default()
	}
	ix, _ := solution.CachedIndex(u.Paths)
	return u.solutionDetail(cfg, ix, name)
}

func (u *Updater) solutionDetail(cfg *config.Config, ix *solution.Index, name string) (*SolutionDetail, error) {
	embedded, _ := solution.Load(name, u.SolutionsDir)
	inIndex := ix.Find(name)
	if embedded == nil && inIndex == nil {
		return nil, problem.Newf(problem.NotFound, "No solution has the name %s.", name)
	}
	d := &SolutionDetail{Name: name, Selected: cfg.Solution.Name == name, Locks: u.entries(ix, name), EmbeddedLocks: []string{}}
	for _, v := range u.store().Embedded(name) {
		d.EmbeddedLocks = append(d.EmbeddedLocks, solution.NormalizeLockVersion(v))
	}
	if d.Locks == nil {
		d.Locks = []AvailableLock{}
	}
	switch {
	case embedded != nil && inIndex != nil:
		d.Source = "both"
	case embedded != nil:
		d.Source = "embedded"
	default:
		d.Source = "index"
	}
	// The document of the newest lock that the agent has, else the embedded solution.yaml.
	for _, l := range d.Locks {
		if sl, _, err := u.store().Get(name, l.Version); err == nil {
			d.Document, d.DocumentVersion = sl.Solution.Doc, strp(l.Version)
			d.Title, d.Description = sl.Solution.Title, sl.Solution.Description
			break
		}
	}
	if d.Document == nil && embedded != nil {
		d.Document, d.Title, d.Description = embedded.Doc, embedded.Title, embedded.Description
	}
	if d.Document == nil && inIndex != nil {
		d.Document = map[string]any{"apiVersion": "care-pi/v1", "kind": "Solution", "name": name, "title": inIndex.Title,
			"description": inIndex.Description, "care": map[string]any{"image": "ghcr.io/ohcnetwork/care", "ref": "latest"},
			"care_fe": map[string]any{"repo": "https://github.com/ohcnetwork/care_fe", "ref": "develop"}}
		d.Title, d.Description = inIndex.Title, inIndex.Description
	}
	if d.Title == "" {
		d.Title = name
	}
	return d, nil
}

// SolutionLock returns GET /solutions/{name}/locks/{version}: the cache, the agent, state/locks, or
// a download through the index (checked by its SHA-256).
func (u *Updater) SolutionLock(ctx context.Context, name, version string) (any, error) {
	if l, _, err := u.store().Get(name, version); err == nil {
		return l.JSON()
	}
	ix, _ := solution.CachedIndex(u.Paths)
	if s := ix.Find(name); s != nil {
		if e := s.Lock(version); e != nil {
			l, err := u.client().FetchLock(ctx, ix, name, *e)
			if err != nil {
				return nil, err
			}
			return l.JSON()
		}
	}
	if _, err := u.Solution(name); err != nil {
		return nil, err
	}
	return nil, problem.Newf(problem.LockNotFound, "%s has no lock %s in the index, the cache, or the agent.", name, version)
}

// ---- the scheduled runs of the timers ----

// ScheduledPlan is what "care-pi update apply --scheduled" does now (docs/solutions.md, section 10.2).
type ScheduledPlan struct {
	WindowOpen bool
	Check      bool
	Update     bool
	OS         bool
	Reboot     bool
	Reason     string
}

// Scheduled returns the work of the window now. jobRunning tells that a job holds the apply lock.
func (u *Updater) Scheduled(ctx context.Context, jobRunning bool) (*ScheduledPlan, error) {
	cfg, err := u.Config()
	if err != nil {
		return nil, err
	}
	sp := &ScheduledPlan{}
	open, closes, _ := u.WindowOpen(cfg)
	sp.WindowOpen = open
	if !open {
		sp.Reason = "the update window is closed"
		return sp, nil
	}
	st := ReadState(u.Paths)
	sp.Check = st.LastCheckAt == nil || st.LastCheckError != nil || u.now().Sub(*st.LastCheckAt) > time.Hour
	remain := closes.Sub(u.now())
	sp.Update = cfg.Updates.Auto && remain >= 30*time.Minute
	sp.OS = cfg.Updates.OSFull
	sp.Reboot = !jobRunning && (cfg.Updates.OSReboot && fileExists(u.Paths.Abs("/run/reboot-required")) || st.RebootScheduledAt != nil)
	return sp, nil
}

// UpdateReady reports whether the last check found an update that nothing blocks.
func (u *Updater) UpdateReady(ctx context.Context) bool {
	o, err := u.Overview(ctx)
	return err == nil && o.UpdateAvailable && o.BlockedBy == nil
}

// ---- recovery of an interrupted cutover (docs/solutions.md, section 8.6) ----

// Recovery tells what the agent does after an interrupted cutover.
type Recovery struct {
	JobID string
	// Rollback is true when the target tree is on disk: a job update-rollback to the lock on disk.
	Rollback bool
	// Restart is true when the previous tree is on disk: start the stack and turn the page off.
	Restart bool
	Offer   *PitrOffer
}

// PendingRecovery returns the recovery of an interrupted cutover, or nil.
func (u *Updater) PendingRecovery() *Recovery {
	st := ReadState(u.Paths)
	c := st.Cutover
	if c == nil {
		return nil
	}
	if j, err := jobs.Load(u.Paths, c.JobID); err == nil && j.State == jobs.StateRunning {
		return nil
	}
	r := &Recovery{JobID: c.JobID}
	if !c.Activated {
		r.Restart = true
		return r
	}
	r.Rollback = true
	if c.Migrated {
		r.Offer = offerOf(c.Checkpoint)
	}
	return r
}

// Recover runs a recovery job body: the rollback to the lock on disk (it is the previous lock: the
// interruption came before write-lock), or the start of the stack.
func (u *Updater) Recover(ctx context.Context, rec *jobs.Recorder, r *Recovery) (*RollbackResult, error) {
	started := u.now()
	cur, err := lock.Load(u.Paths)
	if err != nil {
		return nil, err
	}
	hist := HistoryEntry{JobID: rec.ID(), Kind: HistRollback, Source: jobs.SourceTimer, Reason: strp("recovery " + r.JobID),
		StartedAt: started, PitrOffer: r.Offer}
	if cur != nil {
		hist.FromVersion, hist.ToVersion = strp(cur.Solution.Version), strp(cur.Solution.Version)
	}
	err = u.rollbackTo(ctx, rec, cur)
	hist.FinishedAt = u.now()
	if err != nil {
		hist.Outcome = OutcomeRollbackFailed
		p := problem.Newf(problem.RollbackFailed, "The recovery after the interrupted job %s failed: %v. The maintenance page stays.", r.JobID, err)
		p.PitrOffer = r.Offer
		hist.Problem = p
		_ = AppendHistory(u.Paths, hist)
		return nil, p
	}
	_ = UpdateState(u.Paths, func(s *State) { s.Cutover = nil })
	hist.Outcome = OutcomeRolledBack
	_ = AppendHistory(u.Paths, hist)
	d := hist.FinishedAt.Sub(started).Seconds()
	id := ""
	if cur != nil {
		id = cur.History.PreviousID()
	}
	if id == "" {
		id = started.UTC().Format("20060102T150405Z")
	}
	return &RollbackResult{Kind: jobs.KindUpdateRollback, FromVersion: hist.FromVersion, ToVersion: hist.ToVersion, ToLock: id,
		PitrOffer: r.Offer, DowntimeS: d}, nil
}
