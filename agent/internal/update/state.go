package update

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"sort"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
)

// KeepHistoryLines is the number of lines that update-history.jsonl keeps.
const KeepHistoryLines = 200

// State is state/updates.json: the state of the update machine (docs/solutions.md, section 6.4).
type State struct {
	LastCheckAt    *time.Time       `json:"last_check_at,omitempty"`
	LastCheckError *problem.Problem `json:"last_check_error,omitempty"`
	// Target is the target that the last check found.
	Target *TargetRecord `json:"target,omitempty"`
	// Blocked is the lock that the last check found but this agent cannot install (agent-too-old).
	Blocked *BlockedRecord `json:"blocked,omitempty"`
	// Staging is the last staging.
	Staging *StagingRecord `json:"staging,omitempty"`
	// RebootScheduledAt is the reboot that the agent does in the next window.
	RebootScheduledAt *time.Time `json:"reboot_scheduled_at,omitempty"`
	// Cutover is the phase of a running cutover (section 8.6).
	Cutover *CutoverRecord `json:"cutover,omitempty"`
	// OSCheckedAt is the time of the last apt-get update of the agent.
	OSCheckedAt *time.Time `json:"os_checked_at,omitempty"`
}

// TargetRecord is the target of the last check.
type TargetRecord struct {
	Kind               string   `json:"kind"`
	Version            string   `json:"version,omitempty"`
	TargetID           string   `json:"target_id"`
	Path               string   `json:"path"`
	NeedsBuild         bool     `json:"needs_build"`
	ChangedComponents  []string `json:"changed_components,omitempty"`
	EstimatedDowntimeS int      `json:"estimated_downtime_s"`
	// FromVersion is the running version at the check. A target is stale after another update.
	FromVersion string `json:"from_version,omitempty"`
	FromSHA256  string `json:"from_sha256,omitempty"`
}

// BlockedRecord is the lock of a check that stopped with agent-too-old. GET /updates shows it as blocked_by
// while the lock is an update of the running version and the agent is still too old for it.
type BlockedRecord struct {
	Version         string `json:"version"`
	MinAgentVersion string `json:"min_agent_version"`
	Detail          string `json:"detail"`
}

// StagingRecord is one staging.
type StagingRecord struct {
	State         string           `json:"state"`
	Version       string           `json:"version,omitempty"`
	TargetID      string           `json:"target_id,omitempty"`
	JobID         string           `json:"job_id,omitempty"`
	StagedAt      *time.Time       `json:"staged_at,omitempty"`
	DownloadBytes int64            `json:"download_bytes"`
	NeedsBuild    bool             `json:"needs_build"`
	Error         *problem.Problem `json:"error,omitempty"`
}

// CutoverRecord is the phase of a cutover. The agent writes it before each cutover step.
type CutoverRecord struct {
	JobID    string `json:"job_id"`
	Kind     string `json:"kind"`
	Step     string `json:"step"`
	TargetID string `json:"target_id"`
	// Activating is written at the start of the step activate, before its first write.
	Activating bool        `json:"activating,omitempty"`
	Activated  bool        `json:"activated"`
	Migrated   bool        `json:"migrated"`
	Checkpoint *Checkpoint `json:"checkpoint,omitempty"`
}

// ReadState reads updates.json. A missing or bad file gives an empty state.
func ReadState(p paths.Paths) State {
	var st State
	data, err := os.ReadFile(p.Abs(paths.UpdateState))
	if err == nil {
		_ = json.Unmarshal(data, &st)
	}
	return st
}

// WriteState writes updates.json.
func WriteState(p paths.Paths, st State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return lock.WriteFileAtomic(p.Abs(paths.UpdateState), data, 0o600)
}

// UpdateState changes updates.json with fn.
func UpdateState(p paths.Paths, fn func(*State)) error {
	st := ReadState(p)
	fn(&st)
	return WriteState(p, st)
}

// ReadHistory returns the entries of update-history.jsonl, newest first.
func ReadHistory(p paths.Paths) []HistoryEntry {
	data, err := os.ReadFile(p.Abs(paths.UpdateHistory))
	if err != nil {
		return nil
	}
	var out []HistoryEntry
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var e HistoryEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.JobID != "" {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].JobID > out[j].JobID })
	return out
}

// AppendHistory appends an entry and keeps the last KeepHistoryLines lines.
func AppendHistory(p paths.Paths, e HistoryEntry) error {
	all := ReadHistory(p)
	all = append([]HistoryEntry{e}, all...)
	if len(all) > KeepHistoryLines {
		all = all[:KeepHistoryLines]
	}
	var b bytes.Buffer
	for i := len(all) - 1; i >= 0; i-- {
		line, err := json.Marshal(all[i])
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return lock.WriteFileAtomic(p.Abs(paths.UpdateHistory), b.Bytes(), 0o600)
}

// HistoryPage returns one page of the history, newest first.
func HistoryPage(p paths.Paths, limit int, before string) History {
	if limit <= 0 {
		limit = 20
	}
	out := History{Items: []HistoryEntry{}}
	for _, e := range ReadHistory(p) {
		if before != "" && e.JobID >= before {
			continue
		}
		if len(out.Items) == limit {
			id := out.Items[len(out.Items)-1].JobID
			out.NextBefore = &id
			break
		}
		out.Items = append(out.Items, e)
	}
	return out
}
