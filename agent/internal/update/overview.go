package update

import (
	"context"
	"os"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/apply"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/solution"
)

// solutionOf returns the solution of the running lock (its solution lock), else the embedded solution.yaml.
func (u *Updater) solutionOf(cfg *config.Config, cur *lock.Lock) (*solution.Solution, *solution.Lock) {
	if cur != nil && cur.Solution.Version != "" && cur.Solution.Name == cfg.Solution.Name {
		if l, _, err := u.store().Get(cur.Solution.Name, cur.Solution.Version); err == nil {
			return l.Solution, l
		}
	}
	sol, err := solution.Load(cfg.Solution.Name, u.SolutionsDir)
	if err != nil {
		return &solution.Solution{Name: cfg.Solution.Name, Title: cfg.Solution.Name}, nil
	}
	return sol, nil
}

// entries returns the locks of the solution, newest first: the locks of the index when it lists the solution; else
// (no index, or an index without the solution) the cached and the embedded locks (section 7.4). The cache also
// holds the lock of a --lock-file of the CLI (a test of a candidate); with an index, such a lock is not a release.
func (u *Updater) entries(ix *solution.Index, name string) []AvailableLock {
	var out []AvailableLock
	if s := ix.Find(name); s != nil {
		for _, l := range s.Locks {
			out = append(out, availableOf(l, u.agent()))
		}
		sortAvailable(out)
		return out
	}
	for _, v := range u.store().Versions(name) {
		l, _, err := u.store().Get(name, v)
		if err != nil {
			continue
		}
		old, _ := solution.AgentTooOld(u.agent(), l.MinAgentVersion)
		summary := firstLine(l.Notes)
		out = append(out, AvailableLock{Version: v, ReleasedAt: parseTime(l.CreatedAt), Summary: summary,
			MinAgentVersion: l.MinAgentVersion, Installable: !old})
	}
	sortAvailable(out)
	return out
}

func firstLine(s string) string {
	for i, c := range s {
		if c == '\n' {
			return s[:i]
		}
	}
	return s
}

func sortAvailable(a []AvailableLock) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && solution.CompareLockVersions(a[j].Version, a[j-1].Version) > 0; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

func installedOf(cur *lock.Lock, entries []AvailableLock) *InstalledLock {
	if cur == nil {
		return nil
	}
	il := &InstalledLock{Version: strp(cur.Solution.Version), Source: cur.Solution.Source, Track: cur.Solution.Track,
		LockSHA256: strp(cur.Solution.LockSHA256), InstalledAt: parseTime(cur.GeneratedAt), AgentVersion: cur.AgentVersion}
	if il.Source == "" {
		il.Source = lock.SourceSolutionRefs
	}
	if il.InstalledAt.IsZero() {
		il.InstalledAt = time.Unix(0, 0).UTC()
	}
	for _, e := range entries {
		if cur.Solution.Version != "" && solution.CompareLockVersions(e.Version, cur.Solution.Version) == 0 {
			il.Withdrawn, il.WithdrawnReason = e.Withdrawn, e.WithdrawnReason
		}
	}
	return il
}

// machineState returns the state of the update machine.
func (u *Updater) machineState(st State) (string, *jobs.Job) {
	j := runningJob(u.Paths, UpdateKinds...)
	if j != nil {
		switch j.Kind {
		case jobs.KindUpdateCheck:
			return StateChecking, j
		case jobs.KindUpdateRollback:
			return StateRollingBack, j
		case jobs.KindUpdateStage:
			return StateStaging, j
		}
		if _, on := apply.ReadMaintenance(u.Paths); on {
			return StateApplying, j
		}
		if j.Kind == jobs.KindUpdateApply {
			return StateStaging, j
		}
		return StateApplying, j
	}
	if st.Cutover != nil {
		return StateFailed, nil
	}
	if st.Staging != nil && st.Staging.State == "staged" {
		return StateStaged, nil
	}
	return StateIdle, nil
}

// Maintenance returns the maintenance state (schema MaintenanceState).
func Maintenance(p paths.Paths) MaintenanceState {
	var m MaintenanceState
	fi, err := os.Stat(p.Abs(paths.ModeDir + "/" + paths.ModeMaintenance))
	if err != nil {
		return m
	}
	m.Active = true
	t := fi.ModTime().UTC().Truncate(time.Second)
	m.StartedAt = &t
	if info, ok := apply.ReadMaintenance(p); ok {
		st := info.StartedAt.UTC().Truncate(time.Second)
		m.StartedAt = &st
		if info.Kind != "" {
			m.Kind = strp(info.Kind)
		}
		m.JobID = strp(info.JobID)
	}
	var j *jobs.Job
	if m.JobID != nil {
		j, _ = jobs.Load(p, *m.JobID)
	}
	if j == nil {
		j = jobs.Latest(p, func(j *jobs.Job) bool { return j.State == jobs.StateRunning && jobs.LockKinds[j.Kind] })
		if j != nil && m.JobID == nil {
			kind, id := j.Kind, j.ID
			m.Kind, m.JobID = &kind, &id
		}
	}
	if j != nil && j.State == jobs.StateRunning {
		for i := len(j.Steps) - 1; i >= 0; i-- {
			if j.Steps[i].State == jobs.StepRunning && j.Steps[i].Action != nil {
				a := *j.Steps[i].Action
				m.Step = &a
				break
			}
		}
	}
	// A restart behind the page (S62) names its action in maintenance.json.
	if info, ok := apply.ReadMaintenance(p); ok && m.Step == nil && info.Step != "" {
		m.Step = strp(info.Step)
	}
	return m
}

// Overview returns GET /updates. It reads state files and the cached index; it never uses the network.
func (u *Updater) Overview(ctx context.Context) (*Overview, error) { return u.overview(ctx, true) }

// overview with full checks each image of the previous lock (rollback.images_present).
func (u *Updater) overview(ctx context.Context, full bool) (*Overview, error) {
	cfg, err := u.Config()
	if err != nil {
		return nil, err
	}
	cur, err := lock.Load(u.Paths)
	if err != nil {
		return nil, err
	}
	st := ReadState(u.Paths)
	ix, _ := solution.CachedIndex(u.Paths)
	sol, _ := u.solutionOf(cfg, cur)
	o := &Overview{Track: cfg.Solution.Track, Pin: strp(solution.NormalizeLockVersion(cfg.Solution.Pin)), Auto: cfg.Updates.Auto,
		Available: []AvailableLock{}, Components: []UpdateComponent{}, LastCheckAt: st.LastCheckAt, LastCheckError: st.LastCheckError}
	o.Solution.Name, o.Solution.Title = cfg.Solution.Name, sol.Title
	if o.Solution.Title == "" {
		o.Solution.Title = cfg.Solution.Name
	}
	entries := u.entries(ix, cfg.Solution.Name)
	o.Current = installedOf(cur, entries)
	curV := ""
	if cur != nil {
		curV = cur.Solution.Version
	}
	for i := range entries {
		e := entries[i]
		if o.Latest == nil && !e.Withdrawn {
			o.Latest = &e
		}
		if curV == "" || solution.CompareLockVersions(e.Version, curV) > 0 {
			o.Available = append(o.Available, e)
		}
	}
	if t := st.Target; t != nil && (cur == nil || (t.FromVersion == cur.Solution.Version && t.FromSHA256 == cur.Solution.LockSHA256)) {
		ut := &UpdateTarget{Kind: t.Kind, Version: strp(t.Version), TargetID: t.TargetID, Path: t.Path, NeedsBuild: t.NeedsBuild}
		d := t.EstimatedDowntimeS
		ut.EstimatedDowntimeS = &d
		for _, e := range entries {
			if t.Version != "" && solution.CompareLockVersions(e.Version, t.Version) == 0 {
				rel := e.ReleasedAt
				ut.ReleasedAt, ut.ChangelogURL, ut.Summary, ut.MinAgentVersion = &rel, e.ChangelogURL, strp(e.Summary), strp(e.MinAgentVersion)
				if !e.Installable && !e.Withdrawn {
					o.BlockedBy = &UpdateBlocker{Code: problem.AgentTooOld, Detail: cfg.Solution.Name + " " + e.Version + " needs agent " +
						e.MinAgentVersion + " or newer. Run the installer again.", MinAgentVersion: strp(e.MinAgentVersion)}
				}
			}
		}
		ut.Staged = st.Staging != nil && st.Staging.State == "staged" && st.Staging.TargetID == t.TargetID
		ut.NeedsInternet = !ut.Staged
		o.Target = ut
		o.UpdateAvailable = updateAvailable(cfg, cur, t) && o.BlockedBy == nil
		if tl, err := lock.Read(stagingDir(u.Paths, t.TargetID) + "/lock.yaml"); err == nil && tl != nil {
			o.Components = components(cur, tl)
		}
	}
	if o.BlockedBy == nil && st.LastCheckError != nil && st.LastCheckError.Slug == problem.AgentTooOld {
		o.BlockedBy = &UpdateBlocker{Code: problem.AgentTooOld, Detail: st.LastCheckError.Detail, MinAgentVersion: strp(st.LastCheckError.MinAgentVersion)}
	}
	o.State, _ = u.machineState(st)
	if j := runningJob(u.Paths, UpdateKinds...); j != nil {
		s := j.Summary()
		o.Job = &s
	}
	o.Window = u.windowState(cfg)
	ready := u.ready()
	if ready {
		t := nextCheck(u.now(), u.loc())
		o.NextCheckAt = &t
	}
	reboot := fileExists(u.Paths.Abs(paths.RebootRequired))
	if (o.UpdateAvailable && cfg.Updates.Auto) || cfg.Updates.OSFull || (reboot && cfg.Updates.OSReboot) || st.RebootScheduledAt != nil {
		o.NextApplyAt = timep(o.Window.NextOpenAt)
	}
	url, fetched := IndexFetchedAt(u.Paths)
	o.Index = IndexState{URL: IndexURL(cfg.Updates.IndexURL), FetchedAt: timep(fetched)}
	if url != "" && url != o.Index.URL {
		o.Index.FetchedAt = nil
	}
	if ix != nil {
		o.Index.DistBase = strp(ix.DistBase)
		o.Index.GeneratedAt = timep(parseTime(ix.GeneratedAt))
	}
	o.Staging = StagingState{State: "none"}
	if s := st.Staging; s != nil {
		o.Staging = StagingState{State: s.State, Version: strp(s.Version), TargetID: strp(s.TargetID), JobID: strp(s.JobID),
			StagedAt: s.StagedAt, NeedsBuild: s.NeedsBuild, Error: s.Error}
		if s.DownloadBytes > 0 || s.State == "staged" {
			n := s.DownloadBytes
			o.Staging.DownloadBytes = &n
		}
	}
	o.Maintenance = Maintenance(u.Paths)
	if h := ReadHistory(u.Paths); len(h) > 0 {
		o.LastResult = &h[0]
	}
	o.Rollback = u.rollbackState(ctx, cur, full)
	o.OS = u.osSummary(cfg, st)
	return o, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// nextCheck returns the next 12:00 local time (care-update-check.timer without its random delay).
func nextCheck(now time.Time, loc *time.Location) time.Time {
	l := now.In(loc)
	t := time.Date(l.Year(), l.Month(), l.Day(), 12, 0, 0, 0, loc)
	if !t.After(now) {
		t = time.Date(l.Year(), l.Month(), l.Day()+1, 12, 0, 0, 0, loc)
	}
	return t.UTC()
}

func (u *Updater) windowState(cfg *config.Config) WindowState {
	w := windowOf(cfg)
	ws := WindowState{Days: append([]string{}, w.Days...), Start: w.Start, End: w.End, Timezone: u.tzName()}
	open, closes, next := u.WindowOpen(cfg)
	ws.Open = open
	if open {
		ws.ClosesAt = timep(closes)
	}
	ws.NextOpenAt = next.UTC()
	if next.IsZero() {
		ws.NextOpenAt = u.now()
	}
	return ws
}

func (u *Updater) rollbackState(ctx context.Context, cur *lock.Lock, checkImages bool) RollbackState {
	rs := RollbackState{}
	if cur == nil || cur.History.PreviousID() == "" {
		rs.Reason = strp("No previous lock exists.")
		return rs
	}
	id := cur.History.PreviousID()
	prev, err := lock.ReadHistory(u.Paths, id)
	if err != nil || prev == nil {
		rs.Reason = strp("The previous lock " + id + " is not in " + paths.LockHistory + ".")
		return rs
	}
	rs.Available, rs.ToLock, rs.ToVersion = true, &id, strp(prev.Solution.Version)
	if checkImages {
		rs.ImagesPresent = u.imagesPresent(ctx, prev)
	}
	rs.PitrOffer = u.installOffer(cur)
	return rs
}

// components compares the running lock with the target lock (schema UpdateComponent).
func components(cur, tgt *lock.Lock) []UpdateComponent {
	if cur == nil {
		cur = &lock.Lock{}
	}
	var out []UpdateComponent
	src := func(l *lock.Lock) string {
		if l.Solution.Source == lock.SourceLock {
			return lock.SourceLock
		}
		return lock.SourceSolution
	}
	img := func(id, label, name string) {
		a, b := cur.Images[name], tgt.Images[name]
		if a.Ref == "" && b.Ref == "" {
			return
		}
		_, tag, _ := lock.SplitImage(b.Ref)
		c := UpdateComponent{ID: id, Label: label, Kind: "image", Ref: strp(tag), Tracked: !b.Pinned && !b.Local, Source: src(tgt),
			Current: strp(a.Digest), Target: strp(b.Digest)}
		if c.Current == nil && a.Ref != "" {
			c.Current = strp(a.Ref)
		}
		if c.Target == nil && b.Ref != "" {
			c.Target = strp(b.Ref)
		}
		c.Changed = a.Pull() != b.Pull()
		out = append(out, c)
	}
	img("care", "CARE backend", "care")
	if b, ok := tgt.Git["care_fe"]; ok {
		a := cur.Git["care_fe"]
		out = append(out, UpdateComponent{ID: "care_fe", Label: "CARE frontend", Kind: "git", Ref: strp(b.Ref), Tracked: !b.Pinned,
			Source: src(tgt), Current: strp(a.SHA), Target: strp(b.SHA), Changed: a.SHA != b.SHA})
	}
	for _, n := range []struct{ name, label string }{{"postgres", "PostgreSQL"}, {"caddy", "Web server"}, {"redis", "Redis"}, {"s3", "File storage"}, {"node", "Node.js builder"}} {
		img("images."+n.name, n.label, n.name)
	}
	for _, id := range sortedKeys(tgt.Components.Plugs) {
		b := tgt.Components.Plugs[id]
		a := cur.Components.Plugs[id]
		if b.Backend != nil {
			c := UpdateComponent{ID: "plugs." + id + ".backend", Label: id + " (backend)", Kind: "plug-backend", Ref: strp(b.Backend.Ref),
				Tracked: b.Backend.Tracked, Source: src(tgt), Target: strp(b.Backend.SHA)}
			if a.Backend != nil {
				c.Current = strp(a.Backend.SHA)
			}
			c.Changed = ptrS(c.Current) != ptrS(c.Target) || a.Enabled != b.Enabled
			out = append(out, c)
		}
		if b.Frontend != nil {
			c := UpdateComponent{ID: "plugs." + id + ".frontend", Label: id + " (frontend)", Kind: "plug-frontend", Ref: strp(b.Frontend.Ref),
				Tracked: b.Frontend.Tracked, Source: src(tgt), Target: strp(firstNonEmpty(b.Frontend.SHA, b.Frontend.URL))}
			if a.Frontend != nil {
				c.Current = strp(firstNonEmpty(a.Frontend.SHA, a.Frontend.URL))
			}
			c.Changed = ptrS(c.Current) != ptrS(c.Target) || a.Enabled != b.Enabled
			out = append(out, c)
		}
	}
	return out
}

func ptrS(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// Summary returns the updates member of GET /status (schema UpdatesSummary).
func (u *Updater) Summary(ctx context.Context) (*Summary, error) {
	o, err := u.overview(ctx, false)
	if err != nil {
		return nil, err
	}
	s := &Summary{Track: o.Track, UpdateAvailable: o.UpdateAvailable, State: o.State, NextApplyAt: o.NextApplyAt,
		LastCheckAt: o.LastCheckAt, RollbackAvailable: o.Rollback.Available, OSRebootRequired: o.OS.RebootRequired}
	if o.Current != nil {
		s.CurrentVersion = o.Current.Version
	}
	if o.Latest != nil {
		s.LatestVersion = &o.Latest.Version
	}
	if o.Target != nil {
		s.TargetVersion = o.Target.Version
	}
	if o.Staging.State == "staged" {
		s.StagedVersion = o.Staging.Version
	}
	if o.BlockedBy != nil {
		s.BlockedBy = &o.BlockedBy.Code
	}
	if r := o.LastResult; r != nil {
		s.LastResult = &SummaryResult{JobID: r.JobID, Kind: r.Kind, Outcome: r.Outcome, FinishedAt: r.FinishedAt, ToVersion: r.ToVersion}
	}
	return s, nil
}
