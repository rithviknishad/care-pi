package service

import (
	"context"
	"io"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/clockgate"
)

// defaultClockReloadDelay is the wait before care-caddy reloads after the claim set the clock: the answer of the
// claim goes out through Caddy first.
const defaultClockReloadDelay = 2 * time.Second

// ClockFromBrowser is the clock without internet (S52, docs/api/README.md section 16). The claim gives the time of
// the browser (client_time). When NTP is not synchronized, the agent sets the clock one time from it, and the clock
// gate passes; the clock is not trusted until NTP synchronizes it. After the jump, care-caddy reloads: its local
// leaf certificates (12 h) were issued with the old clock. The claim calls it after the claim secret is checked and
// before the session is made, so the session has the new clock. A failure is logged; the claim goes on.
func (s *Service) ClockFromBrowser(ctx context.Context, t time.Time) {
	if s.Host == nil || s.Host.Clock == nil {
		return
	}
	res, err := clockgate.FromBrowser(ctx, s.Paths, s.Host.Clock, t, s.now())
	switch {
	case err != nil:
		s.log().Error("clock: cannot set the clock from the browser", "error", err)
	case !res.Set:
		s.log().Info("clock: the clock does not change", "reason", res.Reason, "browser_time", t.UTC().Format(time.RFC3339))
	}
	if !res.Set {
		return
	}
	s.log().Warn("clock: set the clock from the browser at the claim; it is not trusted until NTP synchronizes it",
		"time", res.Marker.SetAt.Format(time.RFC3339), "before", res.Marker.HostBefore.Format(time.RFC3339))
	s.mu.Lock()
	s.publicCache = cached[*PublicStatus]{}
	s.statusCache = cached[*Status]{}
	s.mu.Unlock()
	delay := s.ClockReloadDelay
	if delay == 0 {
		delay = defaultClockReloadDelay
	}
	go func() {
		time.Sleep(delay)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		clockgate.ReloadCaddy(ctx, s.Host, false, io.Discard)
	}()
}

// clockState is the source of the clock and whether it is trusted (schemas ClockStatus and PublicStatus.clock).
func (s *Service) clockState(ctx context.Context) (source string, trusted, synchronized bool) {
	if s.Host == nil {
		return clockgate.SourceNone, false, false
	}
	return clockgate.State(ctx, s.Paths, s.Host.Clock)
}
