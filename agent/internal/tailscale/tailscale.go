// Package tailscale runs the host commands of the Tailscale mode (P11, S37, docs/connectivity.md).
//
// Tailscale runs on the host from the apt repository of Tailscale, never in a container. The agent installs it
// only when the operator turns the mode on (S37: strictly opt-in); the installer never does. The agent writes
// the apt source and the key in a transient unit (systemd-run), as the OS update does: care-pi.service has
// ProtectSystem=strict. Caddy gets the certificate of the MagicDNS name from tailscaled through its socket.
package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/host"
)

// Backend states of tailscaled (ipn.State).
const (
	StateNoState          = "NoState"
	StateNeedsLogin       = "NeedsLogin"
	StateNeedsMachineAuth = "NeedsMachineAuth"
	StateStopped          = "Stopped"
	StateStarting         = "Starting"
	StateRunning          = "Running"
)

// Units of the transient commands.
const (
	InstallUnit = "care-pi-tailscale-install"
	UpUnit      = "care-pi-tailscale-up"
)

// UpTimeout bounds the wait of "tailscale up" for a login in the browser.
const UpTimeout = 15 * time.Minute

// Status is the part of "tailscale status --json" that the agent uses.
type Status struct {
	Installed    bool     `json:"installed"`
	Version      string   `json:"version"`
	BackendState string   `json:"backend_state"`
	AuthURL      string   `json:"-"`
	DNSName      string   `json:"dns_name"`
	HostName     string   `json:"host_name"`
	Tailnet      string   `json:"tailnet"`
	IPv4         string   `json:"ipv4"`
	Online       bool     `json:"online"`
	CertDomains  []string `json:"cert_domains"`
	Health       []string `json:"health"`
}

// HTTPSEnabled reports whether the tailnet gives certificates for the name (MagicDNS and HTTPS on in the admin console).
func (s *Status) HTTPSEnabled() bool {
	for _, d := range s.CertDomains {
		if strings.EqualFold(strings.TrimSuffix(d, "."), s.DNSName) {
			return true
		}
	}
	return false
}

type rawStatus struct {
	Version      string   `json:"Version"`
	BackendState string   `json:"BackendState"`
	AuthURL      string   `json:"AuthURL"`
	TailscaleIPs []string `json:"TailscaleIPs"`
	Self         *struct {
		HostName     string   `json:"HostName"`
		DNSName      string   `json:"DNSName"`
		TailscaleIPs []string `json:"TailscaleIPs"`
		Online       bool     `json:"Online"`
	} `json:"Self"`
	CurrentTailnet *struct {
		Name           string `json:"Name"`
		MagicDNSSuffix string `json:"MagicDNSSuffix"`
	} `json:"CurrentTailnet"`
	CertDomains []string `json:"CertDomains"`
	Health      []string `json:"Health"`
}

// ParseStatus reads the JSON of "tailscale status --json".
func ParseStatus(data []byte) (*Status, error) {
	var r rawStatus
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("tailscale status: %w", err)
	}
	s := &Status{Installed: true, Version: r.Version, BackendState: r.BackendState, AuthURL: r.AuthURL,
		CertDomains: r.CertDomains, Health: r.Health}
	ips := r.TailscaleIPs
	if r.Self != nil {
		s.DNSName = strings.TrimSuffix(r.Self.DNSName, ".")
		s.HostName = r.Self.HostName
		s.Online = r.Self.Online
		if len(r.Self.TailscaleIPs) > 0 {
			ips = r.Self.TailscaleIPs
		}
	}
	for _, ip := range ips {
		if !strings.Contains(ip, ":") {
			s.IPv4 = ip
			break
		}
	}
	if r.CurrentTailnet != nil {
		s.Tailnet = r.CurrentTailnet.Name
	}
	if s.CertDomains == nil {
		s.CertDomains = []string{}
	}
	if s.Health == nil {
		s.Health = []string{}
	}
	return s, nil
}

// Client runs the tailscale commands.
type Client struct {
	Runner host.Runner
	// RunDir holds the auth key file while "tailscale up" runs (a tmpfs, /run/care-pi).
	RunDir string
	// Poll is the interval of the status polls (default 2 s).
	Poll time.Duration
}

func (c Client) run(ctx context.Context, args ...string) ([]byte, error) {
	return c.Runner.Run(ctx, host.Cmd{Name: "tailscale", Args: args})
}

// Status returns the state of tailscaled. A host without tailscale gives Installed false and no error.
func (c Client) Status(ctx context.Context) (*Status, error) {
	if _, err := c.run(ctx, "version"); err != nil {
		return &Status{CertDomains: []string{}, Health: []string{}}, nil
	}
	out, err := c.run(ctx, "status", "--json")
	if len(out) == 0 && err != nil {
		return &Status{Installed: true, BackendState: StateNoState, CertDomains: []string{}, Health: []string{}}, nil
	}
	// "tailscale status" exits with 1 when the backend is not running, but it prints the JSON.
	return ParseStatus(out)
}

// InstallScript is the shell script of the install unit. It adds the stable apt repository of Tailscale for the
// distribution of the host (/etc/os-release), installs the package, and starts tailscaled.
const InstallScript = `set -eu
. /etc/os-release
case "${ID:-}" in
  debian | raspbian | ubuntu) repo="$ID" ;;
  *) repo=debian ;;
esac
code="${VERSION_CODENAME:?no VERSION_CODENAME in /etc/os-release}"
keyring=/usr/share/keyrings/tailscale-archive-keyring.gpg
curl -fsSL "https://pkgs.tailscale.com/stable/$repo/$code.noarmor.gpg" -o "$keyring.tmp"
mv "$keyring.tmp" "$keyring"
chmod 0644 "$keyring"
printf 'deb [signed-by=%s] https://pkgs.tailscale.com/stable/%s %s main\n' "$keyring" "$repo" "$code" > /etc/apt/sources.list.d/tailscale.list
apt-get -o DPkg::Lock::Timeout=600 update
apt-get -o DPkg::Lock::Timeout=600 -y install tailscale
systemctl enable --now tailscaled.service
`

// InstallCmd returns the command that installs tailscale in a transient unit.
func InstallCmd() host.Cmd {
	return host.Cmd{Name: "systemd-run", Args: []string{"--wait", "--collect", "--pipe", "--quiet", "--unit=" + InstallUnit,
		"-p", "MemoryMax=512M", "--setenv=DEBIAN_FRONTEND=noninteractive", "--", "/bin/sh", "-c", InstallScript}}
}

// Install installs tailscale when the host does not have it. It returns true when it installed the package.
func (c Client) Install(ctx context.Context) (bool, error) {
	if _, err := c.run(ctx, "version"); err == nil {
		return false, nil
	}
	if _, err := c.Runner.Run(ctx, InstallCmd()); err != nil {
		return false, fmt.Errorf("cannot install tailscale: %w", err)
	}
	if _, err := c.run(ctx, "version"); err != nil {
		return true, fmt.Errorf("tailscale is not usable after the install: %w", err)
	}
	return true, nil
}

// UpArgs are the flags of "tailscale up": the host name, and no DNS settings from the tailnet on the host
// (the agent never resolves names through MagicDNS, and the clinic DNS of the host must stay).
// A controlURL (tls.tailscale.control_url, O4) adds "--login-server" for a self-hosted control server such as
// Headscale. "--reset" sets each flag that is not given to its default, so the flag is necessary at each "up".
func UpArgs(hostname, controlURL string) []string {
	args := []string{"up", "--hostname=" + hostname, "--accept-dns=false", "--accept-routes=false", "--reset"}
	if controlURL != "" {
		args = append(args, "--login-server="+controlURL)
	}
	return args
}

// UpWithKey runs "tailscale up" with an auth key. The key goes through a file in RunDir (mode 0600), never
// through the command line: "--auth-key=file:<path>".
func (c Client) UpWithKey(ctx context.Context, hostname, controlURL, authKey string) error {
	if c.RunDir == "" {
		return errors.New("tailscale: no RunDir for the auth key")
	}
	if err := os.MkdirAll(c.RunDir, 0o700); err != nil {
		return err
	}
	f := filepath.Join(c.RunDir, "tailscale-auth-key")
	if err := os.WriteFile(f, []byte(strings.TrimSpace(authKey)+"\n"), 0o600); err != nil {
		return err
	}
	defer func() { _ = os.Remove(f) }()
	args := append(UpArgs(hostname, controlURL), "--auth-key=file:"+f, "--timeout=2m")
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if _, err := c.run(ctx, args...); err != nil {
		return fmt.Errorf("tailscale up with the auth key: %w", err)
	}
	return nil
}

// StartLogin starts "tailscale up" without a key in a transient unit: tailscaled then waits for a login in the
// browser (BackendState NeedsLogin with an AuthURL). The unit ends at the login, or after UpTimeout.
func (c Client) StartLogin(ctx context.Context, hostname, controlURL string) error {
	_, _ = c.Runner.Run(ctx, host.Cmd{Name: "systemctl", Args: []string{"stop", UpUnit + ".service"}})
	args := append([]string{"--collect", "--quiet", "--unit=" + UpUnit, "--", "tailscale"},
		append(UpArgs(hostname, controlURL), "--timeout="+UpTimeout.String())...)
	if _, err := c.Runner.Run(ctx, host.Cmd{Name: "systemd-run", Args: args}); err != nil {
		return fmt.Errorf("cannot start tailscale up: %w", err)
	}
	return nil
}

// WaitFor polls the status until accept returns true, or until timeout ends. It calls each with each new status.
func (c Client) WaitFor(ctx context.Context, timeout time.Duration, each func(*Status), accept func(*Status) bool) (*Status, error) {
	poll := c.Poll
	if poll <= 0 {
		poll = 2 * time.Second
	}
	deadline := time.Now().Add(timeout)
	var last *Status
	for {
		st, err := c.Status(ctx)
		if err == nil {
			last = st
			if each != nil {
				each(st)
			}
			if accept(st) {
				return st, nil
			}
		}
		if !time.Now().Before(deadline) {
			if last == nil {
				return nil, fmt.Errorf("tailscale status: %v", err)
			}
			return last, fmt.Errorf("tailscale is %s after %s", last.BackendState, timeout)
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(poll):
		}
	}
}

// ServePorts returns the TCP ports of the node that "tailscale serve" or "tailscale funnel" uses (the TCP map of
// "tailscale serve status --json"). tailscaled listens on these ports of the Tailscale address itself, so the pod
// cannot publish on them: a publish on a port in use stops the pod, and then all of Care. A host without a serve
// configuration, or a version without the JSON output, gives no ports.
func (c Client) ServePorts(ctx context.Context) []int {
	out, err := c.run(ctx, "serve", "status", "--json")
	if err != nil {
		return nil
	}
	return ParseServePorts(out)
}

// ParseServePorts reads the TCP ports of "tailscale serve status --json" (ipn.ServeConfig), in increasing order.
func ParseServePorts(data []byte) []int {
	var cfg struct {
		TCP map[string]json.RawMessage `json:"TCP"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return nil
	}
	var ports []int
	for k := range cfg.TCP {
		var p int
		if _, err := fmt.Sscanf(k, "%d", &p); err == nil && p > 0 {
			ports = append(ports, p)
		}
	}
	sort.Ints(ports)
	return ports
}

// Down disconnects the host from the tailnet; Logout removes the node key. The package stays installed.
func (c Client) Down(ctx context.Context) error {
	_, _ = c.Runner.Run(ctx, host.Cmd{Name: "systemctl", Args: []string{"stop", UpUnit + ".service"}})
	if _, err := c.run(ctx, "down"); err != nil {
		return fmt.Errorf("tailscale down: %w", err)
	}
	return nil
}

// Logout removes the node from the tailnet.
func (c Client) Logout(ctx context.Context) error {
	if _, err := c.run(ctx, "logout"); err != nil {
		return fmt.Errorf("tailscale logout: %w", err)
	}
	return nil
}
