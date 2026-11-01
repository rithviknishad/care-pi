package backup

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1" //nolint:gosec // pgBackRest derives the key of its info files with EVP_BytesToKey and SHA-1.
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/hostcheck"
)

// LocalResult is the local part of BackupRunResult.
type LocalResult struct {
	Label         string    `json:"label"`
	Type          string    `json:"type"`
	UndoPoint     time.Time `json:"undo_point"`
	Timeline      int       `json:"timeline"`
	RepoSizeBytes int64     `json:"repo_size_bytes"`
}

// DestResult is one destination of BackupRunResult.
type DestResult struct {
	ID              string `json:"id"`
	DBSnapshotID    string `json:"db_snapshot_id"`
	FilesSnapshotID string `json:"files_snapshot_id"`
	AddedBytes      int64  `json:"added_bytes"`
}

// RunResult is the result of a backup-run job (schema BackupRunResult).
type RunResult struct {
	Kind         string       `json:"kind"`
	Scope        string       `json:"scope"`
	Local        *LocalResult `json:"local"`
	Destinations []DestResult `json:"destinations"`
}

// VerifyDest is one destination of BackupVerifyResult.
type VerifyDest struct {
	ID          string  `json:"id"`
	SubsetPct   int     `json:"read_data_subset_pct"`
	PrunedBytes *int64  `json:"pruned_bytes"`
	DurationS   float64 `json:"duration_s"`
}

// VerifyResult is the result of a backup-verify job (schema BackupVerifyResult).
type VerifyResult struct {
	Kind         string         `json:"kind"`
	Local        *VerifyOutcome `json:"local"`
	Destinations []VerifyDest   `json:"destinations"`
}

// DrillResult is the result of a restore-drill job (schema RestoreDrillResult).
type DrillResult struct {
	Kind          string            `json:"kind"`
	Outcome       string            `json:"outcome"`
	BackupLabel   *string           `json:"backup_label"`
	RestoredBytes *int64            `json:"restored_bytes"`
	DurationS     float64           `json:"duration_s"`
	Checks        []hostcheck.Check `json:"checks"`
	SkippedReason *string           `json:"skipped_reason"`
}

// RotateDest is one destination of RecoveryKeyRotateResult.
type RotateDest struct {
	ID         string `json:"id"`
	Location   string `json:"location"`
	SnapshotID string `json:"snapshot_id"`
}

// RotateResult is the result of a recovery-key-rotate job (schema RecoveryKeyRotateResult).
type RotateResult struct {
	Kind                string       `json:"kind"`
	NewKey              bool         `json:"new_key"`
	KeyID               string       `json:"key_id"`
	PreviousKeyID       *string      `json:"previous_key_id"`
	LocalRekeyed        bool         `json:"local_rekeyed"`
	FullBackupLabel     string       `json:"full_backup_label"`
	Destinations        []RotateDest `json:"destinations"`
	RemovedRepositories []string     `json:"removed_repositories"`
}

// ---- backup.info of a repository in a snapshot ----

// DecryptInfo decrypts a pgBackRest info file of an encrypted repository (OpenSSL "Salted__",
// EVP_BytesToKey with SHA-1, AES-256-CBC), as "openssl enc -d -aes-256-cbc -md sha1" does.
func DecryptInfo(data []byte, pass string) ([]byte, error) {
	if !bytes.HasPrefix(data, []byte("Salted__")) || len(data) < 16+aes.BlockSize {
		return nil, errors.New("the info file is not encrypted with a salt")
	}
	salt, ct := data[8:16], data[16:]
	if len(ct)%aes.BlockSize != 0 {
		return nil, errors.New("the info file has a wrong length")
	}
	key, iv := bytesToKey([]byte(pass), salt, 32, aes.BlockSize)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, ct)
	pad := int(out[len(out)-1])
	if pad < 1 || pad > aes.BlockSize || pad > len(out) {
		return nil, errors.New("the recovery key cannot decrypt the info file")
	}
	for _, b := range out[len(out)-pad:] {
		if int(b) != pad {
			return nil, errors.New("the recovery key cannot decrypt the info file")
		}
	}
	return out[:len(out)-pad], nil
}

func bytesToKey(pass, salt []byte, keyLen, ivLen int) ([]byte, []byte) {
	var out, prev []byte
	for len(out) < keyLen+ivLen {
		h := sha1.New() //nolint:gosec // see the import
		h.Write(prev)
		h.Write(pass)
		h.Write(salt)
		prev = h.Sum(nil)
		out = append(out, prev...)
	}
	return out[:keyLen], out[keyLen : keyLen+ivLen]
}

// ParseBackupInfo reads the backups of a decrypted backup.info, oldest first.
func ParseBackupInfo(plain []byte) []LocalBackup {
	var out []LocalBackup
	sc := bufio.NewScanner(bytes.NewReader(plain))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	section := ""
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]") {
			section = l[1 : len(l)-1]
			continue
		}
		if section != "backup:current" {
			continue
		}
		label, raw, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		var v struct {
			Type      string `json:"backup-type"`
			Start     int64  `json:"backup-timestamp-start"`
			Stop      int64  `json:"backup-timestamp-stop"`
			Size      int64  `json:"backup-info-size"`
			RepoDelta int64  `json:"backup-info-repo-size-delta"`
		}
		if json.Unmarshal([]byte(raw), &v) != nil {
			continue
		}
		out = append(out, LocalBackup{Label: label, Type: v.Type, StartedAt: time.Unix(v.Start, 0).UTC(),
			FinishedAt: time.Unix(v.Stop, 0).UTC(), SizeBytes: v.Size, RepoSizeBytes: v.RepoDelta})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FinishedAt.Before(out[j].FinishedAt) })
	return out
}
