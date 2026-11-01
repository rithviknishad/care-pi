package backup

import (
	"fmt"
	"sort"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/config"
)

// Destination states (schema DestinationState.state).
const (
	DestPending  = "pending"
	DestOK       = "ok"
	DestDegraded = "degraded"
	DestError    = "error"
)

// Credentials states (schema DestinationState.credentials).
const (
	CredNotNeeded = "not-needed"
	CredPresent   = "present"
	CredMissing   = "missing"
)

// Banner severities.
const (
	SevInfo    = "info"
	SevWarning = "warning"
	SevError   = "error"
)

// Banner is one banner (schema BackupBanner).
type Banner struct {
	Code          string     `json:"code"`
	Severity      string     `json:"severity"`
	DestinationID *string    `json:"destination_id"`
	Since         *time.Time `json:"since"`
	Message       string     `json:"message"`
}

// Health is the banners (schema BackupsHealth).
type Health struct {
	State       string    `json:"state"`
	Banners     []Banner  `json:"banners"`
	GeneratedAt time.Time `json:"generated_at"`
}

// DestInput is one destination for the health.
type DestInput struct {
	Dest        config.BackupDestination
	State       *DestState
	Credentials string
}

// HealthInput is what the health reads. The service collects it; ComputeHealth is pure.
type HealthInput struct {
	Now    time.Time
	Config config.Backups
	Key    KeyStatus
	Local  *LocalState
	Info   *RepoInfo
	PG     *PGState
	// LocalFree is the free space of the file system of the local repository.
	LocalFree *int64
	Dests     []DestInput
	Drill     *DrillOutcome
}

// LowSpace is the free space below which a destination gets destination-low-space (with 10 %).
const LowSpace = 2 << 30

// DestStatus returns the state of a destination and the error code that explains it.
func DestStatus(in HealthInput, d DestInput) (string, *string) {
	st := d.State
	if st == nil {
		st = &DestState{}
	}
	code := func(c string) *string { return &c }
	if d.Credentials == CredMissing {
		return DestError, code("missing-credentials")
	}
	if d.Dest.Type == TypeSFTP && d.Dest.HostKey == "" {
		return DestError, code("host-key-not-pinned")
	}
	if st.LastAttemptAt == nil {
		return DestPending, nil
	}
	if st.FailingSince == nil {
		return DestOK, nil
	}
	if st.LastErrorCode != nil && *st.LastErrorCode == "destination-untrusted-hostkey" {
		return DestError, st.LastErrorCode
	}
	alert := time.Duration(alertHours(in.Config)) * time.Hour
	ref := st.FailingSince
	if st.LastSuccessAt != nil {
		ref = st.LastSuccessAt
	}
	if in.Now.Sub(*ref) >= alert {
		return DestError, st.LastErrorCode
	}
	return DestDegraded, st.LastErrorCode
}

func alertHours(b config.Backups) int {
	if b.AlertAfterHours > 0 {
		return b.AlertAfterHours
	}
	return 24
}

var sevRank = map[string]int{SevError: 3, SevWarning: 2, SevInfo: 1}

// ComputeHealth returns the banners of docs/api/README.md section 13.10.
func ComputeHealth(in HealthInput) Health {
	var out []Banner
	add := func(code, sev string, dest *string, since *time.Time, msg string) {
		out = append(out, Banner{Code: code, Severity: sev, DestinationID: dest, Since: since, Message: msg})
	}
	if len(in.Config.Destinations) == 0 {
		add("no-destination", SevError, nil, nil, "no backup destination is configured")
	}
	switch in.Key.State {
	case KeyNone, "":
		add("no-recovery-key", SevError, nil, nil, "no recovery key exists")
	case KeyUnconfirmed:
		add("recovery-key-unconfirmed", SevWarning, nil, in.Key.CreatedAt, "the recovery key is not confirmed")
	}
	alert := alertHours(in.Config)
	for _, d := range in.Dests {
		state, code := DestStatus(in, d)
		id := d.Dest.ID
		if state == DestError {
			var since *time.Time
			msg := fmt.Sprintf("%s has no successful snapshot for %d hours", id, alert)
			if d.State != nil {
				since = d.State.FailingSince
				if d.State.LastSuccessAt != nil {
					since = d.State.LastSuccessAt
					msg = fmt.Sprintf("%s has no successful snapshot for %d hours", id, int(in.Now.Sub(*since).Hours()))
				}
			}
			if code != nil {
				switch *code {
				case "missing-credentials":
					msg = id + " has no S3 keys"
				case "host-key-not-pinned":
					msg = id + " has no pinned SFTP host key"
				case "destination-untrusted-hostkey":
					msg = id + " presents another SSH host key"
				}
			}
			add("destination-unreachable", SevError, &id, since, msg)
		}
		if d.State != nil && d.State.FreeBytes != nil {
			free := *d.State.FreeBytes
			low := free < LowSpace
			if d.State.TotalBytes != nil && *d.State.TotalBytes > 0 && free*10 < *d.State.TotalBytes {
				low = true
			}
			if low {
				add("destination-low-space", SevWarning, &id, nil, fmt.Sprintf("%s has %d MiB free", id, free>>20))
			}
		}
	}
	if in.LocalFree != nil {
		var full int64
		if in.Info != nil {
			if b := in.Info.LastOf("full"); b != nil {
				full = b.RepoSizeBytes
				if full == 0 {
					full = b.SizeBytes
				}
			}
		}
		if in.Local != nil && in.Local.LastFullSize > full {
			full = in.Local.LastFullSize
		}
		if *in.LocalFree < 2*full+(1<<30) {
			add("local-disk-low", SevWarning, nil, nil, fmt.Sprintf("the local backup disk has %d MiB free", *in.LocalFree>>20))
		}
	}
	if in.Local != nil && in.Local.LastScheduledOK != nil && !*in.Local.LastScheduledOK {
		add("local-backup-failed", SevWarning, nil, in.Local.LastScheduledAt, "the last scheduled local backup failed")
	}
	if since := WALFailingSince(in); since != nil {
		limit := 2 * time.Duration(max(in.Config.WALArchiveTimeoutMin, 1)) * time.Minute
		if in.Now.Sub(*since) >= limit {
			add("wal-archive-failing", SevError, nil, since, "the WAL archive fails")
		}
	}
	verifyBad := in.Local != nil && in.Local.LastVerify != nil && !in.Local.LastVerify.OK
	var verifySince *time.Time
	if verifyBad {
		t := in.Local.LastVerify.FinishedAt
		verifySince = &t
	}
	for _, d := range in.Dests {
		if d.State != nil && d.State.LastVerify != nil && !d.State.LastVerify.OK {
			verifyBad = true
			if verifySince == nil {
				t := d.State.LastVerify.FinishedAt
				verifySince = &t
			}
		}
	}
	if verifyBad {
		add("verify-failed", SevError, nil, verifySince, "the last verify found damage")
	}
	if in.Drill != nil && in.Drill.Outcome == "failed" {
		t := in.Drill.FinishedAt
		add("drill-failed", SevWarning, nil, &t, "the last restore drill failed")
	}
	if in.Config.Paused {
		add("backups-paused", SevWarning, nil, nil, "the backup timers are paused")
	}
	if in.Key.LocalRepository == RepoLegacy {
		add("local-repository-legacy-key", SevInfo, nil, nil, "the local repository uses a random cipher pass; re-key it")
	}
	sort.SliceStable(out, func(i, j int) bool { return sevRank[out[i].Severity] > sevRank[out[j].Severity] })
	h := Health{State: "ok", Banners: out, GeneratedAt: in.Now.UTC().Truncate(time.Second)}
	if h.Banners == nil {
		h.Banners = []Banner{}
	}
	if len(out) > 0 {
		h.State = out[0].Severity
	}
	return h
}

// WALFailing reports whether the last archive-push failed after the last success.
func WALFailing(pg *PGState) bool {
	return pg != nil && pg.LastFailedAt != nil && (pg.LastArchivedAt == nil || pg.LastFailedAt.After(*pg.LastArchivedAt))
}

// WALFailingSince returns the start of a WAL archive failure that the agent saw, or nil.
func WALFailingSince(in HealthInput) *time.Time {
	if in.PG != nil && !WALFailing(in.PG) {
		return nil
	}
	if in.Local != nil {
		return in.Local.WALFailingSince
	}
	return nil
}

// Local repository keys (schema RecoveryKeyStatus.local_repository).
const (
	RepoDerived = "derived"
	RepoLegacy  = "legacy"
	RepoNone    = "none"
)
