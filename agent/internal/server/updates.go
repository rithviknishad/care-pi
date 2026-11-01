package server

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/service"
	"github.com/rithviknishad/care-pi/agent/internal/update"
)

// Phase 4 handlers (docs/api/README.md, section 14).

var (
	lockVersionRE  = regexp.MustCompile(`^[0-9]{4}\.[0-9]{1,2}\.[0-9]+$`)
	lockIDRE       = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z$`)
	plugIDRE       = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	solutionNameRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
)

// optString reads an optional string member that can be null.
func optString(ptr string, raw json.RawMessage, max int, re *regexp.Regexp) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", problem.Invalid(ptr, "must be a string or null", "type")
	}
	if max > 0 && len([]rune(v)) > max {
		return "", problem.Invalid(ptr, "the value is too long", "maxLength")
	}
	if re != nil && !re.MatchString(v) {
		return "", problem.Invalid(ptr, "the value does not match the pattern", "pattern")
	}
	return v, nil
}

func optBool(ptr string, raw json.RawMessage) (bool, error) {
	if len(raw) == 0 {
		return false, nil
	}
	var v bool
	if err := json.Unmarshal(raw, &v); err != nil {
		return false, problem.Invalid(ptr, "must be true or false", "type")
	}
	return v, nil
}

// members decodes an optional JSON object body into its members and rejects unknown members.
func members(c *reqCtx, allowed ...string) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := c.decode(&m, false); err != nil {
		return nil, err
	}
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	for k := range m {
		if !ok[k] {
			return nil, problem.Invalid("/"+k, "the member is not allowed", "additionalProperties")
		}
	}
	if m == nil {
		m = map[string]json.RawMessage{}
	}
	return m, nil
}

func (s *Server) getUpdates(c *reqCtx) error {
	o, err := s.svc.Updates(c.r.Context())
	if err != nil {
		return err
	}
	s.json(c.w, http.StatusOK, o)
	return nil
}

func (s *Server) startUpdateCheck(c *reqCtx) error {
	if _, err := members(c); err != nil {
		return err
	}
	j, err := s.svc.StartUpdateCheck(c.r.Context(), service.CheckRequest{}, jobs.SourceConsole)
	return s.accepted(c, j, err)
}

func (s *Server) startUpdateApply(c *reqCtx) error {
	m, err := members(c, "version", "now", "reason")
	if err != nil {
		return err
	}
	var req service.UpdateRequest
	if req.Version, err = optString("/version", m["version"], 0, lockVersionRE); err != nil {
		return err
	}
	if req.Now, err = optBool("/now", m["now"]); err != nil {
		return err
	}
	if req.Reason, err = optString("/reason", m["reason"], 200, nil); err != nil {
		return err
	}
	j, err := s.svc.StartUpdateApply(c.r.Context(), req, jobs.SourceConsole)
	return s.accepted(c, j, err)
}

func (s *Server) startUpdateStage(c *reqCtx) error {
	m, err := members(c, "version")
	if err != nil {
		return err
	}
	var req service.StageRequest
	if req.Version, err = optString("/version", m["version"], 0, lockVersionRE); err != nil {
		return err
	}
	j, err := s.svc.StartUpdateStage(c.r.Context(), req, jobs.SourceConsole)
	return s.accepted(c, j, err)
}

func (s *Server) startRollback(c *reqCtx) error {
	m, err := members(c, "to_lock", "reason")
	if err != nil {
		return err
	}
	var req service.RollbackRequest
	if req.ToLock, err = optString("/to_lock", m["to_lock"], 0, lockIDRE); err != nil {
		return err
	}
	if req.Reason, err = optString("/reason", m["reason"], 200, nil); err != nil {
		return err
	}
	j, err := s.svc.StartUpdateRollback(c.r.Context(), req, jobs.SourceConsole)
	return s.accepted(c, j, err)
}

func (s *Server) updateHistory(c *reqCtx) error {
	limit := 20
	if v, ok := c.query("limit"); ok {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			return problem.InvalidParam("limit", "use a number from 1 to 100")
		}
		limit = n
	}
	before := ""
	if v, ok := c.query("before"); ok {
		if !jobs.IDPattern.MatchString(v) {
			return problem.InvalidParam("before", "the value is not a job id")
		}
		before = v
	}
	s.json(c.w, http.StatusOK, s.svc.UpdateHistory(limit, before))
	return nil
}

func (s *Server) osUpdates(c *reqCtx) error {
	o, err := s.svc.OSUpdates(c.r.Context())
	if err != nil {
		return err
	}
	s.json(c.w, http.StatusOK, o)
	return nil
}

func (s *Server) startOSUpdate(c *reqCtx) error {
	m, err := members(c, "now", "reboot_if_required", "reason")
	if err != nil {
		return err
	}
	var req service.OSRequest
	if req.Now, err = optBool("/now", m["now"]); err != nil {
		return err
	}
	if req.RebootIfRequired, err = optBool("/reboot_if_required", m["reboot_if_required"]); err != nil {
		return err
	}
	if req.Reason, err = optString("/reason", m["reason"], 200, nil); err != nil {
		return err
	}
	j, err := s.svc.StartOSUpdate(c.r.Context(), req, jobs.SourceConsole)
	return s.accepted(c, j, err)
}

func (s *Server) osReboot(c *reqCtx) error {
	m, err := members(c, "now", "delay_s")
	if err != nil {
		return err
	}
	now, err := optBool("/now", m["now"])
	if err != nil {
		return err
	}
	delay := 5
	if raw := m["delay_s"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &delay); err != nil || delay < 1 || delay > 300 {
			return problem.Invalid("/delay_s", "use a number from 1 to 300", "maximum")
		}
	}
	r, err := s.svc.RebootForOS(now, time.Duration(delay)*time.Second)
	if err != nil {
		return err
	}
	s.json(c.w, http.StatusAccepted, r)
	return nil
}

func (s *Server) listPlugs(c *reqCtx) error {
	l, err := s.svc.Plugs(c.r.Context())
	if err != nil {
		return err
	}
	s.json(c.w, http.StatusOK, l)
	return nil
}

func (s *Server) plugID(c *reqCtx) (string, error) {
	id := c.param("plug_id")
	if !plugIDRE.MatchString(id) {
		return "", problem.Newf(problem.PlugUnknown, "No plug has the id %q.", id)
	}
	return id, nil
}

func (s *Server) plugSchema(c *reqCtx) error {
	id, err := s.plugID(c)
	if err != nil {
		return err
	}
	m, err := s.svc.PlugSchema(id)
	if err != nil {
		return err
	}
	c.w.Header().Set("Content-Type", "application/schema+json")
	c.w.WriteHeader(http.StatusOK)
	return json.NewEncoder(c.w).Encode(m)
}

func (s *Server) setPlug(c *reqCtx) error {
	id, err := s.plugID(c)
	if err != nil {
		return err
	}
	dry, err := c.boolQuery("dry_run")
	if err != nil {
		return err
	}
	ap, err := c.boolQuery("apply")
	if err != nil {
		return err
	}
	var body map[string]json.RawMessage
	if err := c.decode(&body, true); err != nil {
		return err
	}
	if body == nil {
		return problem.Invalid("", "the body must be a JSON object", "type")
	}
	var ch update.PlugChange
	for k, raw := range body {
		switch k {
		case "enabled":
			ch.SetEnabled = true
			if string(raw) != "null" {
				var v bool
				if err := json.Unmarshal(raw, &v); err != nil {
					return problem.Invalid("/enabled", "must be true, false, or null", "type")
				}
				ch.Enabled = &v
			}
		case "settings":
			var st map[string]json.RawMessage
			if err := json.Unmarshal(raw, &st); err != nil {
				return problem.Invalid("/settings", "must be an object", "type")
			}
			for part, pr := range st {
				var vals map[string]any
				if err := json.Unmarshal(pr, &vals); err != nil {
					return problem.Invalid("/settings/"+part, "must be an object", "type")
				}
				switch part {
				case "backend":
					ch.Backend = vals
				case "frontend":
					ch.Frontend = vals
				default:
					return problem.Invalid("/settings/"+part, "use backend or frontend", "additionalProperties")
				}
			}
		case "reason":
			if ch.Reason, err = optString("/reason", raw, 200, nil); err != nil {
				return err
			}
		default:
			return problem.Invalid("/"+k, "the member is not allowed", "additionalProperties")
		}
	}
	res, err := s.svc.SetPlug(c.r.Context(), service.PlugWrite{ID: id, Change: ch, IfMatch: c.r.Header.Get("If-Match"),
		DryRun: dry, Apply: ap, Source: jobs.SourceConsole})
	if err != nil {
		return err
	}
	c.w.Header().Set("ETag", res.ETag)
	if res.Job != nil {
		c.w.Header().Set("Location", APIPrefix+"/jobs/"+res.Job.ID)
		s.json(c.w, http.StatusAccepted, res)
		return nil
	}
	s.json(c.w, http.StatusOK, res)
	return nil
}

func (s *Server) listSolutions(c *reqCtx) error {
	l, err := s.svc.Solutions()
	if err != nil {
		return err
	}
	s.json(c.w, http.StatusOK, l)
	return nil
}

func (s *Server) solutionName(c *reqCtx) (string, error) {
	n := c.param("name")
	if !solutionNameRE.MatchString(n) || len(n) > 40 {
		return "", problem.Newf(problem.NotFound, "No solution has the name %s.", n)
	}
	return n, nil
}

func (s *Server) getSolution(c *reqCtx) error {
	n, err := s.solutionName(c)
	if err != nil {
		return err
	}
	d, err := s.svc.Solution(n)
	if err != nil {
		return err
	}
	s.json(c.w, http.StatusOK, d)
	return nil
}

func (s *Server) getSolutionLock(c *reqCtx) error {
	n, err := s.solutionName(c)
	if err != nil {
		return err
	}
	v := c.param("version")
	if !lockVersionRE.MatchString(v) {
		return problem.Newf(problem.LockNotFound, "%s has no lock %s.", n, v)
	}
	l, err := s.svc.SolutionLock(c.r.Context(), n, v)
	if err != nil {
		return err
	}
	s.json(c.w, http.StatusOK, l)
	return nil
}
