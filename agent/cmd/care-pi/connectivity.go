package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/rithviknishad/care-pi/agent/internal/exitcode"
	"github.com/rithviknishad/care-pi/agent/internal/jobs"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/service"
)

// Phase 6: the public name, Tailscale, and Wi-Fi (docs/connectivity.md). Each command is the CLI form of an
// operation of docs/api/README.md section 15. As root, they need no session and no recent authentication.

var connFlags struct {
	json      bool
	hint      string
	acmeCA    string
	acmeSet   bool
	keyStdin  bool
	ssid      string
	pskStdin  bool
	country   string
	failover  bool
	address   string
	timeout   time.Duration
	scheduled bool
	reason    string
	newKey    bool
	// controlURL is tls.tailscale.control_url (Headscale, Phase 7).
	controlURL string
}

func printConnJob(cmd *cobra.Command, svc *service.Service, j *jobs.Job, err error) error {
	if err != nil {
		return cliError(err)
	}
	done, err := followJob(cmd.Context(), cmd.OutOrStdout(), svc, j.ID)
	if err != nil {
		return err
	}
	_ = svc.Jobs.Wait(cmd.Context(), j.ID)
	if done != nil && done.State == jobs.StateSucceeded && len(done.Result) > 0 && connFlags.json {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(done.Result))
	}
	return jobError(done)
}

// ---- tls public ----

var tlsPublicCmd = &cobra.Command{
	Use:   "public",
	Short: "The public name <id>.care-pi.rithviknishad.dev with a Let's Encrypt certificate (PUT /tls/public)",
}

var tlsPublicEnableCmd = &cobra.Command{
	Use:   "enable [--hostname-hint NAME] [--acme-ca URL]",
	Short: "Register with the DNS service and turn on the public name",
	Long: `Register the device with the DNS/ACME service, write the credentials to /etc/care-pi/secrets/acme_dns.json,
send the LAN address, and apply. The DNS service gives the id; the name is <id>.care-pi.rithviknishad.dev.
--acme-ca sets the ACME directory, for example the staging directory of Let's Encrypt
(https://acme-staging-v02.api.letsencrypt.org/directory). An empty value goes back to Let's Encrypt.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		svc := newService(cmd)
		req := service.PublicRequest{Enabled: true, HostnameHint: connFlags.hint, Reason: connFlags.reason}
		if cmd.Flags().Changed("acme-ca") {
			v := connFlags.acmeCA
			req.ACMECA = &v
		}
		j, err := svc.StartPublic(cmd.Context(), req, jobs.SourceCLI)
		return printConnJob(cmd, svc, j, err)
	},
}

var tlsPublicDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Turn off the public name and delete the registration (the name does not come back)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		svc := newService(cmd)
		j, err := svc.StartPublic(cmd.Context(), service.PublicRequest{Enabled: false, Reason: connFlags.reason}, jobs.SourceCLI)
		return printConnJob(cmd, svc, j, err)
	},
}

var tlsPublicStatusCmd = &cobra.Command{
	Use:   "status [--json]",
	Short: "Show the public name, its certificate, and the last address update",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		t := newService(cmd).TLS(cmd.Context())
		w := cmd.OutOrStdout()
		if connFlags.json {
			return printJSON(w, t.Public)
		}
		p := t.Public
		if !p.Enabled || p.Hostname == nil {
			_, _ = fmt.Fprintln(w, "public name: off")
			return nil
		}
		_, _ = fmt.Fprintf(w, "public name: https://%s/\n", *p.Hostname)
		if c := p.Certificate; c != nil {
			line := "certificate: " + c.State
			if c.NotAfter != nil {
				line += ", valid until " + c.NotAfter.Format(time.RFC3339)
			}
			if c.Staging {
				line += " (staging: browsers do not trust it)"
			}
			_, _ = fmt.Fprintln(w, line)
		}
		if p.DNS.Address != nil {
			_, _ = fmt.Fprintf(w, "DNS address: %s (sent %s)\n", *p.DNS.Address, p.DNS.UpdatedAt.Format(time.RFC3339))
		}
		if p.DNS.LastError != nil {
			_, _ = fmt.Fprintf(w, "last error: %s\n", *p.DNS.LastError)
		}
		if p.DNS.Revoked {
			_, _ = fmt.Fprintln(w, "the DNS service revoked the registration: run care-pi tls public disable, then enable")
		}
		return nil
	},
}

var tlsPublicUpdateCmd = &cobra.Command{
	Use:   "update-address [--scheduled]",
	Short: "Send the LAN address to the DNS service when it changed (care-dns-update.timer)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		w := cmd.OutOrStdout()
		res, err := newService(cmd).PublicUpdate(cmd.Context(), func(l string) { _, _ = fmt.Fprintln(w, l) })
		if err != nil {
			return cliError(err)
		}
		if connFlags.json {
			return printJSON(w, res)
		}
		if !res.Sent && !res.Applied && !connFlags.scheduled {
			_, _ = fmt.Fprintf(w, "no change: the DNS service has %s\n", res.Address)
		}
		return nil
	},
}

var tlsPublicRotateCmd = &cobra.Command{
	Use:   "rotate [--new-key]",
	Short: "Get new acme-dns credentials from the DNS service (and a new signing key with --new-key)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		w := cmd.OutOrStdout()
		return cliError(newService(cmd).PublicRotate(cmd.Context(), connFlags.newKey, func(l string) { _, _ = fmt.Fprintln(w, l) }))
	},
}

// ---- tls tailscale ----

var tlsLocalCACmd = &cobra.Command{
	Use:   "local-ca",
	Short: "The local CA: the root CA of this device for the .local names and the LAN addresses",
}

var tlsLocalCARotateCmd = &cobra.Command{
	Use:   "rotate",
	Short: "Make a new root CA with a new key (POST /tls/local-ca/rotate); each device must install it again",
	Long: `rotate makes a new root CA for the names and the LAN addresses of now, keeps the old root for the
Certificate page for 7 days (/var/lib/care-pi/ca/root.prev.crt), and restarts care-caddy for a new intermediate.
Use it when a LAN address is outside the name constraints (GET /tls: local_ca.uncovered) or when the clinic no
longer trusts the old root. Each device of the clinic must then install the new root CA: the browsers show a
certificate warning until then.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		svc := newService(cmd)
		j, err := svc.StartLocalCARotate(cmd.Context(), connFlags.reason, jobs.SourceCLI)
		return printConnJob(cmd, svc, j, err)
	},
}

var tlsTailscaleCmd = &cobra.Command{
	Use:   "tailscale",
	Short: "The Tailscale mode: the MagicDNS name with the certificate of tailscaled (PUT /tls/tailscale)",
}

var tlsTailscaleEnableCmd = &cobra.Command{
	Use:   "enable [--auth-key-stdin] [--control-url URL]",
	Short: "Install tailscale, connect to the tailnet, and turn on the Tailscale name",
	Long: `Install tailscale from the repository of Tailscale (only now: the installer never installs it), then connect.
With --auth-key-stdin, the auth key comes from the standard input. Without it, the command prints nothing secret:
open Settings > Connectivity in the console, or run "care-pi tls tailscale status", for the login URL.
The tailnet must have MagicDNS and HTTPS Certificates on (the DNS page of the Tailscale admin console).
--control-url sets a self-hosted control server (Headscale) for "tailscale up --login-server"; "" goes back to
the Tailscale service. Without the flag, the value of tls.tailscale.control_url stays.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		svc := newService(cmd)
		req := service.TailscaleRequest{Enabled: true, Reason: connFlags.reason}
		if cmd.Flags().Changed("control-url") {
			req.ControlURL = &connFlags.controlURL
		}
		if connFlags.keyStdin {
			k, err := readSecret(cmd, "auth key")
			if err != nil {
				return err
			}
			req.AuthKey = k
		}
		j, err := svc.StartTailscale(cmd.Context(), req, jobs.SourceCLI)
		return printConnJob(cmd, svc, j, err)
	},
}

var tlsTailscaleDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Turn off the Tailscale name, then run tailscale down and tailscale logout (the package stays; not with tls.tailscale.manage_daemon false)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		svc := newService(cmd)
		j, err := svc.StartTailscale(cmd.Context(), service.TailscaleRequest{Enabled: false, Reason: connFlags.reason}, jobs.SourceCLI)
		return printConnJob(cmd, svc, j, err)
	},
}

var tlsTailscaleStatusCmd = &cobra.Command{
	Use:   "status [--json]",
	Short: "Show the tailnet state, the MagicDNS name, and the login URL while a login waits",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		t := newService(cmd).TLS(cmd.Context()).Tailscale
		w := cmd.OutOrStdout()
		if connFlags.json {
			return printJSON(w, t)
		}
		state := "not installed"
		if t.BackendState != nil {
			state = *t.BackendState
		}
		_, _ = fmt.Fprintf(w, "tailscale: enabled %v, %s\n", t.Enabled, state)
		if t.Hostname != nil {
			_, _ = fmt.Fprintf(w, "name: https://%s/ (published %v, HTTPS certificates %v)\n", *t.Hostname, t.Published, t.HTTPSEnabled)
		}
		if t.LoginURL != nil {
			_, _ = fmt.Fprintf(w, "login URL: %s\n", *t.LoginURL)
		}
		return nil
	},
}

// ---- network ----

var networkCmd = &cobra.Command{Use: "network", Short: "Wi-Fi, and the wait for an address of the pod unit"}

var networkWiFiCmd = &cobra.Command{Use: "wifi", Short: "The Wi-Fi connection care-pi-wifi of NetworkManager (PUT /network/wifi)"}

var networkWiFiScanCmd = &cobra.Command{
	Use:   "scan [--json]",
	Short: "List the Wi-Fi networks in range",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		res, err := newService(cmd).ScanWiFi(cmd.Context())
		if err != nil {
			return cliError(err)
		}
		w := cmd.OutOrStdout()
		if connFlags.json {
			return printJSON(w, res)
		}
		for _, n := range res.Networks {
			mark := " "
			if n.InUse {
				mark = "*"
			}
			sec := n.Security
			if sec == "" {
				sec = "open"
			}
			_, _ = fmt.Fprintf(w, "%s %3d%%  %-32s  %s\n", mark, n.Signal, n.SSID, sec)
		}
		return nil
	},
}

var networkWiFiSetCmd = &cobra.Command{
	Use:   "set --ssid SSID --psk-stdin [--country CC] [--failover]",
	Short: "Add or change the Wi-Fi connection (the passphrase comes from the standard input)",
	Long: `Write the NetworkManager profile care-pi-wifi, connect, check the routes, and apply.
--failover: Wi-Fi carries the traffic only when ethernet is down (route metric 600, ethernet 100).
Without --failover, Wi-Fi is the main network (route metric 50).
The passphrase is only in the keyfile of NetworkManager (0600, root), not in /etc/care-pi.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if connFlags.ssid == "" || !connFlags.pskStdin {
			return exitcode.With(exitcode.BadInput, fmt.Errorf("give --ssid and --psk-stdin"))
		}
		psk, err := readSecret(cmd, "passphrase")
		if err != nil {
			return err
		}
		svc := newService(cmd)
		j, err := svc.StartWiFi(cmd.Context(), service.WiFiRequest{SSID: connFlags.ssid, PSK: psk, Country: strings.ToUpper(connFlags.country),
			Failover: connFlags.failover, Reason: connFlags.reason}, jobs.SourceCLI)
		return printConnJob(cmd, svc, j, err)
	},
}

var networkWiFiRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Delete the Wi-Fi connection care-pi-wifi (other profiles stay)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		svc := newService(cmd)
		j, err := svc.StartWiFiRemove(cmd.Context(), connFlags.reason, jobs.SourceCLI)
		return printConnJob(cmd, svc, j, err)
	},
}

var networkWiFiStatusCmd = &cobra.Command{
	Use:   "status [--json]",
	Short: "Show the Wi-Fi state and the default route",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		n := newService(cmd).Network(cmd.Context())
		w := cmd.OutOrStdout()
		if connFlags.json {
			return printJSON(w, n.WiFi)
		}
		if n.WiFi == nil {
			_, _ = fmt.Fprintln(w, "wi-fi: no device")
			return nil
		}
		ws := n.WiFi
		ssid := "-"
		if ws.SSID != nil {
			ssid = *ws.SSID
		}
		_, _ = fmt.Fprintf(w, "wi-fi: network %s, connected %v, failover %v, default route %v\n", ssid, ws.Connected, ws.Failover, ws.DefaultRoute)
		if ws.Address != nil {
			_, _ = fmt.Fprintf(w, "address: %s\n", *ws.Address)
		}
		for _, p := range ws.Profiles {
			_, _ = fmt.Fprintf(w, "profile: %s (%s) active %v managed %v\n", p.Name, p.SSID, p.Active, p.Managed)
		}
		return nil
	},
}

var networkWaitAddressCmd = &cobra.Command{
	Use:    "wait-address --address IP [--timeout 30s]",
	Short:  "Wait until an interface has the address (care-pod.service runs it for the Tailscale address)",
	Args:   cobra.NoArgs,
	Hidden: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if netinfo.WaitAddress(cmd.Context(), netinfo.System{}, connFlags.address, connFlags.timeout, time.Second) {
			return nil
		}
		return exitcode.With(exitcode.Failed, fmt.Errorf("no interface has the address %s after %s", connFlags.address, connFlags.timeout))
	},
}

func init() {
	for _, c := range []*cobra.Command{tlsPublicEnableCmd, tlsPublicDisableCmd, tlsTailscaleEnableCmd, tlsTailscaleDisableCmd,
		networkWiFiSetCmd, networkWiFiRemoveCmd, tlsLocalCARotateCmd} {
		c.Flags().StringVar(&connFlags.reason, "reason", "", "the reason in the job journal")
		c.Flags().BoolVar(&connFlags.json, "json", false, "print the result as JSON")
	}
	for _, c := range []*cobra.Command{tlsPublicStatusCmd, tlsPublicUpdateCmd, tlsTailscaleStatusCmd, networkWiFiScanCmd, networkWiFiStatusCmd} {
		c.Flags().BoolVar(&connFlags.json, "json", false, "print JSON")
	}
	tlsPublicEnableCmd.Flags().StringVar(&connFlags.hint, "hostname-hint", "", "the start of the id (default: identity.hostname)")
	tlsPublicEnableCmd.Flags().StringVar(&connFlags.acmeCA, "acme-ca", "", "the ACME directory URL (empty: Let's Encrypt)")
	tlsPublicUpdateCmd.Flags().BoolVar(&connFlags.scheduled, "scheduled", false, "print only changes (the timer)")
	tlsTailscaleEnableCmd.Flags().BoolVar(&connFlags.keyStdin, "auth-key-stdin", false, "read the auth key from the standard input")
	tlsTailscaleEnableCmd.Flags().StringVar(&connFlags.controlURL, "control-url", "", "the URL of a self-hosted control server (Headscale); \"\" is the Tailscale service")
	networkWiFiSetCmd.Flags().StringVar(&connFlags.ssid, "ssid", "", "the network name")
	networkWiFiSetCmd.Flags().BoolVar(&connFlags.pskStdin, "psk-stdin", false, "read the passphrase from the standard input")
	networkWiFiSetCmd.Flags().StringVar(&connFlags.country, "country", "", "the Wi-Fi country, for example IN")
	networkWiFiSetCmd.Flags().BoolVar(&connFlags.failover, "failover", false, "use Wi-Fi only when ethernet is down")
	networkWaitAddressCmd.Flags().StringVar(&connFlags.address, "address", "", "the address")
	networkWaitAddressCmd.Flags().DurationVar(&connFlags.timeout, "timeout", 30*time.Second, "the longest wait")

	tlsPublicRotateCmd.Flags().BoolVar(&connFlags.newKey, "new-key", false, "also replace the Ed25519 key of the device")
	tlsPublicCmd.AddCommand(tlsPublicEnableCmd, tlsPublicDisableCmd, tlsPublicStatusCmd, tlsPublicUpdateCmd, tlsPublicRotateCmd)
	tlsTailscaleCmd.AddCommand(tlsTailscaleEnableCmd, tlsTailscaleDisableCmd, tlsTailscaleStatusCmd)
	tlsLocalCACmd.AddCommand(tlsLocalCARotateCmd)
	tlsCmd.AddCommand(tlsPublicCmd, tlsTailscaleCmd, tlsLocalCACmd)
	networkWiFiCmd.AddCommand(networkWiFiScanCmd, networkWiFiSetCmd, networkWiFiRemoveCmd, networkWiFiStatusCmd)
	networkCmd.AddCommand(networkWiFiCmd, networkWaitAddressCmd)
	rootCmd.AddCommand(networkCmd)
}
