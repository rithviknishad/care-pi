package apply

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/ca"
	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/render"
)

// connectivity is what Prepare reads from the host for the Phase 6 part of the render (docs/connectivity.md).
type connectivity struct {
	caExists     bool
	caRoot       bool
	publicIssued bool
	acmeDNS      string
	tailscaleIP  string
}

func readConnectivity(p paths.Paths, cfg *config.Config, src netinfo.Source, ips []string, now time.Time) connectivity {
	var c connectivity
	c.caExists = ca.Exists(p)
	// Without a LAN address the root cannot have an IP constraint: Caddy keeps its own CA until an address comes.
	c.caRoot = c.caExists || len(ips) > 0
	if h := render.PublicHost(cfg); h != "" {
		if data, err := os.ReadFile(p.Abs(paths.ACMEDNSFile)); err == nil {
			c.acmeDNS = strings.TrimSpace(string(data))
		}
		c.publicIssued = ca.ValidCert(p, h, now) != nil
	}
	if cfg.TLS.Tailscale.Enabled && src != nil {
		c.tailscaleIP = netinfo.TailscaleIPv4(src)
	}
	return c
}

// PublicState is state/tls-public.json: the registration with the DNS/ACME service without its secrets.
type PublicState struct {
	ClinicID   string     `json:"clinic_id"`
	FullDomain string     `json:"fulldomain"`
	ServiceURL string     `json:"service_url"`
	EnrolledAt *time.Time `json:"enrolled_at,omitempty"`
	// Address is the LAN address that the DNS service has; AddressAt is the time of the last update.
	Address   string     `json:"address,omitempty"`
	AddressAt *time.Time `json:"address_at,omitempty"`
	// LastError is the error of the last address update ("" after a success). LastErrorAt is its time.
	LastError   string     `json:"last_error,omitempty"`
	LastErrorAt *time.Time `json:"last_error_at,omitempty"`
	// Revoked is true when the service answered 410 revoked (an operator deleted the clinic): register again.
	Revoked bool `json:"revoked,omitempty"`
}

// ReadPublicState reads state/tls-public.json. A missing file gives a zero state.
func ReadPublicState(p paths.Paths) PublicState {
	var st PublicState
	if data, err := os.ReadFile(p.Abs(paths.PublicState)); err == nil {
		_ = json.Unmarshal(data, &st)
	}
	return st
}

// WritePublicState writes state/tls-public.json.
func WritePublicState(p paths.Paths, st PublicState) error {
	data, _ := json.MarshalIndent(st, "", "  ")
	return lock.WriteFileAtomic(p.Abs(paths.PublicState), data, 0o600)
}

// podLive is state/pod-live.json: the care.pod of the running pod.
type podLive struct {
	Content string `json:"content"`
}

const podLiveFile = paths.StateDir + "/pod-live.json"

func readPodLive(p paths.Paths) []byte {
	data, err := os.ReadFile(p.Abs(podLiveFile))
	if err != nil {
		return nil
	}
	var st podLive
	if json.Unmarshal(data, &st) != nil || st.Content == "" {
		return nil
	}
	return []byte(st.Content)
}

func writePodLive(p paths.Paths, content []byte) error {
	data, _ := json.Marshal(podLive{Content: string(content)})
	return lock.WriteFileAtomic(p.Abs(podLiveFile), data, 0o600)
}

// ensureCA runs the plan action ensure-ca.
func (x *executor) ensureCA() error {
	primary := ""
	if len(x.pr.Model.LocalHosts) > 0 {
		primary = x.pr.Model.LocalHosts[0]
	}
	res, err := ca.Ensure(x.o.Paths, x.pr.Model.LocalHosts, x.pr.IPs, ca.CommonName(primary), x.o.Now())
	if err != nil {
		return err
	}
	if res.Created {
		_, _ = fmt.Fprintf(x.o.Out, "    made the root CA %s (name constraints: %s; %s)\n", res.Root.Fingerprint()[:16],
			strings.Join(res.State.Constraints.DNS, " "), strings.Join(res.State.Constraints.IPs, " "))
	}
	if res.Replaced {
		_, _ = fmt.Fprintf(x.o.Out, "    warning: the root CA changed (old %s). Install the new root CA on each device of the clinic\n",
			res.State.PreviousFingerprint[:16])
	}
	return nil
}

// updateDNS runs the plan action update-dns. A failure is a warning: the timer tries again in 5 minutes.
func (x *executor) updateDNS(ctx context.Context) error {
	if x.o.DNSAddress == nil || len(x.pr.IPs) == 0 {
		return nil
	}
	if err := x.o.DNSAddress(ctx, x.pr.IPs[0]); err != nil {
		_, _ = fmt.Fprintln(x.o.Out, "    warning: the DNS service did not take the new address:", err)
	}
	return nil
}

// dnsStale reports whether the DNS service has another address than the primary LAN address.
func dnsStale(p paths.Paths, cfg *config.Config, ips []string) bool {
	if render.PublicHost(cfg) == "" || len(ips) == 0 {
		return false
	}
	return ReadPublicState(p).Address != ips[0]
}
