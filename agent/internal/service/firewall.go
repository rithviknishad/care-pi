package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/firewall"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
	"github.com/rithviknishad/care-pi/agent/internal/render"
)

// FirewallState is the firewall in GET /security (schema FirewallState, Phase 7).
type FirewallState struct {
	// Policy is security.firewall.policy of config.yaml.
	Policy string `json:"policy"`
	// LivePolicy is the policy of the last load of the table of the agent, or nil before the first load.
	LivePolicy *string `json:"live_policy"`
	SSHAllowed bool    `json:"ssh_allowed"`
	// UnitActive is false without care-pi-firewall.service (install.sh --no-firewall, ufw, firewalld).
	UnitActive   bool       `json:"unit_active"`
	PendingUntil *time.Time `json:"pending_until"`
	ConfirmedAt  *time.Time `json:"confirmed_at"`
	RolledBackAt *time.Time `json:"rolled_back_at"`
	// RollbackReason is timeout or manual.
	RollbackReason *string `json:"rollback_reason"`
}

// Firewall returns the firewall state.
func (s *Service) Firewall(ctx context.Context) (*FirewallState, error) {
	cfg, err := s.Config()
	if err != nil {
		return nil, err
	}
	fw := render.FirewallOf(cfg)
	st := firewall.Read(s.Paths)
	out := &FirewallState{Policy: fw.Policy, LivePolicy: strPtr(st.Policy), SSHAllowed: fw.SSH,
		UnitActive: firewall.UnitActive(ctx, s.Host), ConfirmedAt: st.ConfirmedAt, RolledBackAt: st.RolledBackAt,
		RollbackReason: strPtr(st.RollbackReason)}
	if st.PendingUntil != nil {
		out.PendingUntil = st.PendingUntil
	}
	return out, nil
}

// FirewallConfirm stops the rollback timer of the policy drop (POST /security/firewall/confirm, care-pi firewall confirm).
func (s *Service) FirewallConfirm(ctx context.Context, by string) (*FirewallState, error) {
	if _, err := firewall.Confirm(ctx, s.Host.Runner, s.Paths, s.now(), by); err != nil {
		if errors.Is(err, firewall.ErrNothingPending) {
			return nil, problem.New(problem.StateConflict, "No firewall change waits for a confirmation.")
		}
		return nil, err
	}
	s.log().Info("firewall policy drop confirmed", "by", by)
	return s.Firewall(ctx)
}

// FirewallRollback sets the policy to accept: at once in nftables, then in config.yaml and in the table of the agent,
// without a full apply ("care-pi firewall rollback"; the rollback timer runs it with reason timeout). It works
// while a job holds the apply lock: it changes only the firewall files, which the next apply renders the same.
func (s *Service) FirewallRollback(ctx context.Context, reason string) error {
	nowErr := firewall.AcceptNow(ctx, s.Host.Runner)
	if err := s.updateConfig(func(doc map[string]any) {
		section(section(doc, "security"), "firewall")["policy"] = firewall.Accept
	}); err != nil {
		return fmt.Errorf("the live policy is accept (%v), but config.yaml did not change: %w", nowErr, err)
	}
	cfg, err := s.Config()
	if err != nil {
		return err
	}
	table, err := render.FirewallTable(cfg)
	if err != nil {
		return err
	}
	f := s.Paths.Abs(paths.FirewallFile)
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(f+".tmp", table, 0o644); err != nil {
		return err
	}
	if err := os.Rename(f+".tmp", f); err != nil {
		return err
	}
	if fileExists(s.Paths.Abs(paths.FirewallDropIn)) {
		_, err := s.Host.Runner.Run(ctx, hostCmd("systemctl", "try-reload-or-restart", paths.FirewallUnit))
		if err != nil && nowErr != nil {
			return fmt.Errorf("cannot load the policy accept: %v; %w", nowErr, err)
		}
	}
	if _, err := firewall.MarkRolledBack(s.Paths, s.now(), reason); err != nil {
		return err
	}
	s.log().Warn("firewall policy back to accept", "reason", reason)
	return nil
}
