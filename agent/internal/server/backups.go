package server

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/backup"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/restore"
	"github.com/rithviknishad/care-pi/agent/internal/service"
)

var (
	destIDRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)
	diskIDRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,199}$`)
	fpRE      = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}$`)
	snapRE    = regexp.MustCompile(`^[0-9a-f]{8,64}$`)
	labelRE   = regexp.MustCompile(`^care-[a-z0-9][a-z0-9-]{0,10}$`)
	backupLRE = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}F(_[0-9]{8}-[0-9]{6}[DI])?$`)
	fileRE    = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]*(/[A-Za-z0-9_-][A-Za-z0-9._-]*)*/?$`)
	wordRE    = regexp.MustCompile(`^[a-z]{3,12}$`)
)

func (s *Server) ok(c *reqCtx, v any) error {
	s.json(c.w, http.StatusOK, v)
	return nil
}

func reasonOK(r *string) error {
	if r != nil {
		return lengthOK("/reason", *r, 0, 200)
	}
	return nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (s *Server) keyParam(c *reqCtx) (string, error) {
	v, ok := c.query("key")
	if !ok {
		return backup.Active, nil
	}
	if v != backup.Active && v != backup.Pending {
		return "", problem.InvalidParam("key", "use active or pending")
	}
	return v, nil
}

func (s *Server) getBackups(c *reqCtx) error {
	ov, err := s.svc.Backups(c.r.Context())
	if err != nil {
		return err
	}
	return s.ok(c, ov)
}

func (s *Server) backupsHealth(c *reqCtx) error {
	h, err := s.svc.BackupsHealth(c.r.Context())
	if err != nil {
		return err
	}
	return s.ok(c, h)
}

func (s *Server) listDestinations(c *reqCtx) error {
	items, err := s.svc.Destinations(c.r.Context())
	if err != nil {
		return err
	}
	return s.ok(c, map[string]any{"items": items})
}

func (s *Server) startBackup(c *reqCtx) error {
	var req struct {
		Destination *string `json:"destination"`
		Scope       *string `json:"scope"`
		Type        *string `json:"type"`
		Reason      *string `json:"reason"`
	}
	if err := c.decode(&req, false); err != nil {
		return err
	}
	r := service.BackupRunRequest{Destination: deref(req.Destination), Scope: deref(req.Scope), Type: deref(req.Type), Reason: deref(req.Reason)}
	if r.Destination != "" && !destIDRE.MatchString(r.Destination) {
		return problem.Invalid("/destination", "the destination id is not valid", "pattern")
	}
	if r.Scope != "" && r.Scope != service.ScopeAll && r.Scope != service.ScopeLocalDB && r.Scope != service.ScopeDestination {
		return problem.Invalid("/scope", "use all, local-db, or destination", "enum")
	}
	if r.Type != "" && r.Type != "incr" && r.Type != "full" {
		return problem.Invalid("/type", "use incr or full", "enum")
	}
	if err := reasonOK(req.Reason); err != nil {
		return err
	}
	j, err := s.svc.StartBackupRun(c.r.Context(), r, jobs.SourceConsole)
	return s.accepted(c, j, err)
}

func (s *Server) startVerify(c *reqCtx) error {
	var req struct {
		Scope       *string `json:"scope"`
		Destination *string `json:"destination"`
		Subset      *int    `json:"read_data_subset_pct"`
		Prune       *bool   `json:"prune"`
	}
	if err := c.decode(&req, false); err != nil {
		return err
	}
	r := service.VerifyRequest{Scope: deref(req.Scope), Destination: deref(req.Destination), SubsetPct: 5}
	if r.Scope != "" && r.Scope != service.ScopeAll && r.Scope != service.ScopeLocalDB && r.Scope != service.ScopeDestination {
		return problem.Invalid("/scope", "use all, local-db, or destination", "enum")
	}
	if r.Destination != "" && !destIDRE.MatchString(r.Destination) {
		return problem.Invalid("/destination", "the destination id is not valid", "pattern")
	}
	if req.Subset != nil {
		if *req.Subset < 1 || *req.Subset > 100 {
			return problem.Invalid("/read_data_subset_pct", "use a number from 1 to 100", "maximum")
		}
		r.SubsetPct = *req.Subset
	}
	r.Prune = req.Prune != nil && *req.Prune
	j, err := s.svc.StartVerify(c.r.Context(), r, jobs.SourceConsole)
	return s.accepted(c, j, err)
}

func (s *Server) startDrill(c *reqCtx) error {
	var req struct {
		Reason *string `json:"reason"`
	}
	if err := c.decode(&req, false); err != nil {
		return err
	}
	if err := reasonOK(req.Reason); err != nil {
		return err
	}
	j, err := s.svc.StartDrill(c.r.Context(), deref(req.Reason), jobs.SourceConsole, false)
	return s.accepted(c, j, err)
}

type credsReq struct {
	AccessKeyID     *string `json:"access_key_id"`
	SecretAccessKey *string `json:"secret_access_key"`
}

func (r *credsReq) value(ptr string) (*backup.Credentials, error) {
	if r == nil {
		return nil, nil
	}
	if r.AccessKeyID == nil {
		return nil, problem.Invalid(ptr+"/access_key_id", "access_key_id is required", "required")
	}
	if r.SecretAccessKey == nil {
		return nil, problem.Invalid(ptr+"/secret_access_key", "secret_access_key is required", "required")
	}
	if err := lengthOK(ptr+"/access_key_id", *r.AccessKeyID, 1, 256); err != nil {
		return nil, err
	}
	if err := lengthOK(ptr+"/secret_access_key", *r.SecretAccessKey, 1, 256); err != nil {
		return nil, err
	}
	return &backup.Credentials{AccessKeyID: *r.AccessKeyID, SecretAccessKey: *r.SecretAccessKey}, nil
}

func (s *Server) testDestination(c *reqCtx) error {
	var req struct {
		Destination       *config.BackupDestination `json:"destination"`
		Credentials       *credsReq                 `json:"credentials"`
		AcceptFingerprint *string                   `json:"accept_fingerprint"`
		CreateBucket      *bool                     `json:"create_bucket"`
	}
	if err := c.decode(&req, true); err != nil {
		return err
	}
	if req.Destination == nil {
		return problem.Invalid("/destination", "destination is required", "required")
	}
	creds, err := req.Credentials.value("/credentials")
	if err != nil {
		return err
	}
	fp := deref(req.AcceptFingerprint)
	if fp != "" && !fpRE.MatchString(fp) {
		return problem.Invalid("/accept_fingerprint", "use SHA256:<43 base64 characters>", "pattern")
	}
	res, err := s.svc.TestDestination(c.r.Context(), service.TestRequest{Destination: *req.Destination, Credentials: creds,
		AcceptFingerprint: fp, CreateBucket: req.CreateBucket != nil && *req.CreateBucket})
	if err != nil {
		return err
	}
	return s.ok(c, res)
}

func (s *Server) setDestinationSecrets(c *reqCtx) error {
	id := c.param("destination_id")
	if !destIDRE.MatchString(id) {
		return problem.InvalidParam("destination_id", "the destination id is not valid")
	}
	var req credsReq
	if err := c.decode(&req, true); err != nil {
		return err
	}
	cr, err := req.value("")
	if err != nil {
		return err
	}
	st, err := s.svc.SetDestinationSecrets(id, *cr)
	if err != nil {
		return err
	}
	return s.ok(c, st)
}

func (s *Server) sshKey(c *reqCtx) error {
	which, err := s.keyParam(c)
	if err != nil {
		return err
	}
	k, err := s.svc.SSHKey(which)
	if err != nil {
		return err
	}
	return s.ok(c, k)
}

func (s *Server) listDisks(c *reqCtx) error {
	l, err := s.svc.Disks(c.r.Context())
	if err != nil {
		return err
	}
	return s.ok(c, l)
}

func (s *Server) prepareDisk(c *reqCtx) error {
	id := c.param("disk_id")
	if !diskIDRE.MatchString(id) {
		return problem.InvalidParam("disk_id", "the disk id is not valid")
	}
	var req struct {
		Wipe  *bool   `json:"wipe"`
		Label *string `json:"label"`
	}
	if err := c.decode(&req, true); err != nil {
		return err
	}
	if req.Wipe == nil || !*req.Wipe {
		return problem.Invalid("/wipe", "wipe must be true: the job erases the disk", "const")
	}
	label := deref(req.Label)
	if label != "" && !labelRE.MatchString(label) {
		return problem.Invalid("/label", "use care- and 1 to 11 characters a-z, 0-9, -", "pattern")
	}
	j, err := s.svc.StartDiskPrepare(c.r.Context(), id, label, jobs.SourceConsole)
	return s.accepted(c, j, err)
}

func (s *Server) recoveryKeyStatus(c *reqCtx) error {
	return s.ok(c, s.svc.RecoveryKeyStatus())
}

func (s *Server) createRecoveryKey(c *reqCtx) error {
	k, st, err := s.svc.CreateRecoveryKey(c.r.Context())
	if err != nil {
		return err
	}
	s.json(c.w, http.StatusCreated, map[string]any{"key": k, "status": st})
	return nil
}

func (s *Server) getRecoveryKey(c *reqCtx) error {
	which, err := s.keyParam(c)
	if err != nil {
		return err
	}
	k, err := s.svc.ShowRecoveryKey(which)
	if err != nil {
		return err
	}
	return s.ok(c, k)
}

func (s *Server) downloadRecoveryKey(c *reqCtx) error {
	which, err := s.keyParam(c)
	if err != nil {
		return err
	}
	text, name, err := s.svc.RecoveryKeyText(which)
	if err != nil {
		return err
	}
	c.w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	c.w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	_, err = c.w.Write([]byte(text))
	return err
}

type wordReq struct {
	Index *int    `json:"index"`
	Word  *string `json:"word"`
}

func words(in []wordReq, ptr string) ([]backup.WordInput, error) {
	if len(in) != 3 {
		return nil, problem.Invalid(ptr, "send 3 words", "minItems")
	}
	out := make([]backup.WordInput, 0, 3)
	for i, w := range in {
		p := ptr + "/" + string(rune('0'+i))
		if w.Index == nil || *w.Index < 1 || *w.Index > 24 {
			return nil, problem.Invalid(p+"/index", "use a position from 1 to 24", "maximum")
		}
		if w.Word == nil || !wordRE.MatchString(*w.Word) {
			return nil, problem.Invalid(p+"/word", "use 3 to 12 lowercase letters", "pattern")
		}
		out = append(out, backup.WordInput{Index: *w.Index, Word: *w.Word})
	}
	return out, nil
}

func (s *Server) confirmRecoveryKey(c *reqCtx) error {
	var req struct {
		Key   *string   `json:"key"`
		Words []wordReq `json:"words"`
	}
	if err := c.decode(&req, true); err != nil {
		return err
	}
	which := backup.Active
	if req.Key != nil {
		if *req.Key != backup.Active && *req.Key != backup.Pending {
			return problem.Invalid("/key", "use active or pending", "enum")
		}
		which = *req.Key
	}
	ws, err := words(req.Words, "/words")
	if err != nil {
		return err
	}
	st, err := s.svc.ConfirmRecoveryKey(which, ws, "/words")
	if err != nil {
		return err
	}
	return s.ok(c, st)
}

func (s *Server) rotateRecoveryKey(c *reqCtx) error {
	var req struct {
		NewKey    *bool   `json:"new_key"`
		RemoveOld *bool   `json:"remove_old_repositories"`
		Reason    *string `json:"reason"`
	}
	if err := c.decode(&req, false); err != nil {
		return err
	}
	if err := reasonOK(req.Reason); err != nil {
		return err
	}
	r := service.RotateRequest{NewKey: true, RemoveOld: true, Reason: deref(req.Reason)}
	if req.NewKey != nil {
		r.NewKey = *req.NewKey
	}
	if req.RemoveOld != nil {
		r.RemoveOld = *req.RemoveOld
	}
	j, err := s.svc.StartRotate(c.r.Context(), r, jobs.SourceConsole)
	return s.accepted(c, j, err)
}

func (s *Server) restorePoints(c *reqCtx) error {
	refresh, err := c.boolQuery("refresh")
	if err != nil {
		return err
	}
	p, err := s.svc.Points(c.r.Context(), refresh)
	if err != nil {
		return err
	}
	return s.ok(c, p)
}

func parseTime(ptr string, v *string) (*time.Time, error) {
	if v == nil {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, *v)
	if err != nil {
		return nil, problem.Invalid(ptr, "use an RFC 3339 date-time", "format")
	}
	t = t.UTC()
	return &t, nil
}

func (s *Server) startRestore(c *reqCtx) error {
	var req struct {
		Kind        *string  `json:"kind"`
		Time        *string  `json:"time"`
		Checkpoint  *string  `json:"checkpoint"`
		Reason      *string  `json:"reason"`
		Destination *string  `json:"destination"`
		SnapshotID  *string  `json:"snapshot_id"`
		Paths       []string `json:"paths"`
		Target      *string  `json:"target"`
	}
	if err := c.decode(&req, true); err != nil {
		return err
	}
	if err := reasonOK(req.Reason); err != nil {
		return err
	}
	r := service.RestoreRequest{Kind: deref(req.Kind), Reason: deref(req.Reason)}
	switch r.Kind {
	case "pitr":
		if req.Destination != nil || req.SnapshotID != nil || req.Paths != nil || req.Target != nil {
			return problem.Invalid("", "a pitr restore takes time or checkpoint", "additionalProperties")
		}
		t, err := parseTime("/time", req.Time)
		if err != nil {
			return err
		}
		cp := deref(req.Checkpoint)
		if (t == nil) == (cp == "") {
			return problem.Invalid("", "send exactly one of time and checkpoint", "oneOf")
		}
		if cp != "" && !backupLRE.MatchString(cp) {
			return problem.Invalid("/checkpoint", "the value is not a backup label", "pattern")
		}
		r.Time, r.Checkpoint = t, cp
	case "files":
		if req.Time != nil || req.Checkpoint != nil {
			return problem.Invalid("", "a files restore takes destination and snapshot_id", "additionalProperties")
		}
		r.Destination, r.SnapshotID = deref(req.Destination), deref(req.SnapshotID)
		if !destIDRE.MatchString(r.Destination) {
			return problem.Invalid("/destination", "the destination id is not valid", "pattern")
		}
		if !snapRE.MatchString(r.SnapshotID) {
			return problem.Invalid("/snapshot_id", "the snapshot id is not valid", "pattern")
		}
		for i, p := range req.Paths {
			if len(p) > 1024 || !fileRE.MatchString(p) || strings.Contains(p, "..") {
				return problem.Invalid("/paths/"+itoa(i), "the path is not valid", "pattern")
			}
		}
		r.Paths = req.Paths
		r.Target = deref(req.Target)
		if r.Target != "" && r.Target != restore.InPlace && r.Target != restore.SideDir {
			return problem.Invalid("/target", "use in-place or side-dir", "enum")
		}
	default:
		return problem.Invalid("/kind", "use pitr or files", "enum")
	}
	j, err := s.svc.StartRestore(c.r.Context(), r, jobs.SourceConsole)
	return s.accepted(c, j, err)
}

type drDestReq struct {
	Type            *string `json:"type"`
	Path            *string `json:"path"`
	DiskLabel       *string `json:"disk_label"`
	Host            *string `json:"host"`
	Port            *int    `json:"port"`
	User            *string `json:"user"`
	HostKey         *string `json:"host_key"`
	Endpoint        *string `json:"endpoint"`
	Region          *string `json:"region"`
	Bucket          *string `json:"bucket"`
	Prefix          *string `json:"prefix"`
	AccessKeyID     *string `json:"access_key_id"`
	SecretAccessKey *string `json:"secret_access_key"`
}

func (d *drDestReq) value() (config.BackupDestination, *backup.Credentials, error) {
	if d == nil || d.Type == nil {
		return config.BackupDestination{}, nil, problem.Invalid("/destination/type", "type is required", "required")
	}
	out := config.BackupDestination{ID: "restore", Type: *d.Type, Path: deref(d.Path), DiskLabel: deref(d.DiskLabel),
		Host: deref(d.Host), User: deref(d.User), HostKey: deref(d.HostKey), Endpoint: deref(d.Endpoint),
		Region: deref(d.Region), Bucket: deref(d.Bucket), Prefix: deref(d.Prefix)}
	if d.Port != nil {
		out.Port = *d.Port
	}
	var cr *backup.Credentials
	if out.Type == backup.TypeS3 {
		c := credsReq{AccessKeyID: d.AccessKeyID, SecretAccessKey: d.SecretAccessKey}
		v, err := c.value("/destination")
		if err != nil {
			return out, nil, err
		}
		cr = v
	} else if d.AccessKeyID != nil || d.SecretAccessKey != nil {
		return out, nil, problem.Invalid("/destination/access_key_id", "only an s3 destination has keys", "additionalProperties")
	}
	if err := service.CheckDestination(out, "/destination"); err != nil {
		return out, nil, err
	}
	out.ID = ""
	return out, cr, nil
}

func recoveryKey(words []string) (backup.Key, error) {
	if len(words) != 24 {
		return backup.Key{}, problem.Invalid("/recovery_key/words", "the recovery key has 24 words", "minItems")
	}
	k, err := backup.ParseWords(words)
	if err != nil {
		var we *backup.WordError
		if asWord(err, &we) {
			return k, problem.Invalid("/recovery_key/words/"+itoa(we.Index-1), "not a word of the recovery key list", "word")
		}
		return k, problem.Invalid("/recovery_key/words", "the checksum is not correct: a word or the order is wrong", "checksum")
	}
	return k, nil
}

func (s *Server) drRequest(c *reqCtx, full bool) (service.DRRequest, error) {
	var req struct {
		RecoveryKey *struct {
			Words []string `json:"words"`
		} `json:"recovery_key"`
		Destination  *drDestReq `json:"destination"`
		Time         *string    `json:"time"`
		SnapshotID   *string    `json:"snapshot_id"`
		Files        *bool      `json:"files"`
		PauseBackups *bool      `json:"pause_backups"`
	}
	var r service.DRRequest
	if err := c.decode(&req, true); err != nil {
		return r, err
	}
	if !full && (req.Time != nil || req.SnapshotID != nil || req.Files != nil || req.PauseBackups != nil) {
		return r, problem.Invalid("", "the inspect takes recovery_key and destination", "additionalProperties")
	}
	if req.RecoveryKey == nil {
		return r, problem.Invalid("/recovery_key", "recovery_key is required", "required")
	}
	k, err := recoveryKey(req.RecoveryKey.Words)
	if err != nil {
		return r, err
	}
	d, cr, err := req.Destination.value()
	if err != nil {
		return r, err
	}
	t, err := parseTime("/time", req.Time)
	if err != nil {
		return r, err
	}
	snap := deref(req.SnapshotID)
	if snap != "" && !snapRE.MatchString(snap) {
		return r, problem.Invalid("/snapshot_id", "the snapshot id is not valid", "pattern")
	}
	r = service.DRRequest{Key: k, Destination: d, Credentials: cr, Time: t, SnapshotID: snap, Files: true}
	if req.Files != nil {
		r.Files = *req.Files
	}
	r.PauseBackups = req.PauseBackups != nil && *req.PauseBackups
	return r, nil
}

func (s *Server) inspectDR(c *reqCtx) error {
	r, err := s.drRequest(c, false)
	if err != nil {
		return err
	}
	out, err := s.svc.InspectDR(c.r.Context(), r)
	if err != nil {
		return err
	}
	return s.ok(c, out)
}

func (s *Server) startDR(c *reqCtx) error {
	r, err := s.drRequest(c, true)
	if err != nil {
		return err
	}
	j, err := s.svc.StartDR(c.r.Context(), r, jobs.SourceConsole)
	return s.accepted(c, j, err)
}

func (s *Server) stepBackups(c *reqCtx) error {
	var req struct {
		Destinations []config.BackupDestination `json:"destinations"`
		Credentials  map[string]credsReq        `json:"credentials"`
		Words        []wordReq                  `json:"recovery_key_words"`
	}
	if err := c.decode(&req, true); err != nil {
		return err
	}
	if len(req.Destinations) == 0 {
		return problem.Invalid("/destinations", "add 1 or more destinations", "minItems")
	}
	if req.Credentials == nil {
		return problem.Invalid("/credentials", "credentials is required", "required")
	}
	r := service.BackupsStepRequest{Destinations: req.Destinations, Credentials: map[string]backup.Credentials{}}
	for i, d := range req.Destinations {
		if err := service.CheckDestination(d, "/destinations/"+itoa(i)); err != nil {
			return err
		}
	}
	for id, cr := range req.Credentials {
		if !destIDRE.MatchString(id) {
			return problem.Invalid("/credentials/"+id, "the destination id is not valid", "pattern")
		}
		v, err := cr.value("/credentials/" + id)
		if err != nil {
			return err
		}
		r.Credentials[id] = *v
	}
	if req.Words != nil {
		ws, err := words(req.Words, "/recovery_key_words")
		if err != nil {
			return err
		}
		r.Words = ws
	}
	st, err := s.svc.StepBackups(r)
	return s.setupResult(c, st, err)
}

func itoa(i int) string { return strconv.Itoa(i) }

func asWord(err error, we **backup.WordError) bool { return errors.As(err, we) }
