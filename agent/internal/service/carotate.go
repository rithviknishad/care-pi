package service

import (
	"context"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/ca"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/render"
)

// CaddyRestartDelay lets the answer of the request and the first events of the job pass Caddy before its restart.
var CaddyRestartDelay = 2 * time.Second

// StartLocalCARotate starts the job tls-local-ca-rotate (POST /tls/local-ca/rotate, care-pi tls local-ca rotate, Phase 7):
// a new root CA with a new key and the name constraints of the names and addresses of now. Caddy restarts to make a new
// intermediate below it. Each device of the clinic must then install the new root.
func (s *Service) StartLocalCARotate(ctx context.Context, reason, source string) (*jobs.Job, error) {
	if err := s.requireReady(); err != nil {
		return nil, err
	}
	cfg, err := s.Config()
	if err != nil {
		return nil, err
	}
	if !cfg.TLS.LocalCA.Enabled {
		return nil, problem.New(problem.StateConflict, "The local CA mode is off.")
	}
	if !ca.Exists(s.Paths) {
		return nil, problem.New(problem.StateConflict, "The device has no root CA of its own yet. The next apply makes it.")
	}
	return s.Jobs.Start(ctx, jobs.Spec{Kind: jobs.KindTLSLocalCARotate, Source: source, Reason: reason, Lock: true},
		func(ctx context.Context, rec *jobs.Recorder) (any, error) {
			hosts := render.LocalHostnames(cfg)
			primary := ""
			if len(hosts) > 0 {
				primary = hosts[0]
			}
			var res *ca.EnsureResult
			if err := step(rec, "ca-rotate: make a new root CA", func() error {
				var err error
				res, err = ca.Rotate(s.Paths, hosts, s.Addresses(), ca.CommonName(primary), s.now())
				if err != nil {
					return err
				}
				rec.Logf("new root %s (old %s)", res.Root.Fingerprint()[:16], res.State.PreviousFingerprint[:16])
				rec.Logf("install the new root CA on each device of the clinic: Certificate page, or care-pi tls root-ca")
				return nil
			}); err != nil {
				return nil, err
			}
			if err := step(rec, "restart-caddy: care-caddy.service makes a new intermediate", func() error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(CaddyRestartDelay):
				}
				return s.Host.Systemd.TryRestart(ctx, "care-caddy.service")
			}); err != nil {
				return nil, err
			}
			s.log().Warn("local root CA rotated", "fingerprint", res.Root.Fingerprint(), "previous", res.State.PreviousFingerprint)
			return jobs.TLSLocalCARotateResult{Kind: jobs.KindTLSLocalCARotate, Fingerprint: res.Root.Fingerprint(),
				PreviousFingerprint: res.State.PreviousFingerprint}, nil
		})
}
