// Package wifi manages the Wi-Fi connection of the host with NetworkManager (S35, docs/connectivity.md).
//
// The approach is a secondary connection, not a bond (SP11): the profile care-pi-wifi is a normal Wi-Fi
// connection beside the ethernet connection. With failover on, it has a lower autoconnect priority and a higher
// route metric (600) than ethernet (100), so the default route uses ethernet while it is up, and Wi-Fi when it
// is down. The pod publishes on both LAN addresses, and avahi announces both. The active-backup bond of the gist
// is rejected: brcmfmac (the Wi-Fi of the Pi) can refuse the MAC address of the bond (fail_over_mac), and a bond
// changes the eth0 connection that the setup depends on (01 §12).
//
// The PSK never goes on a command line: the agent writes the NetworkManager keyfile (mode 0600, root) through the
// stdin of a transient unit, then loads it with "nmcli connection load". The keyfile is the only store of the PSK.
package wifi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/host"
)

// ConnName is the NetworkManager connection of care-pi.
const ConnName = "care-pi-wifi"

// KeyfilePath is the keyfile of ConnName.
const KeyfilePath = "/etc/NetworkManager/system-connections/" + ConnName + ".nmconnection"

// Route metrics: NetworkManager gives ethernet 100 and Wi-Fi 600 by default.
const (
	MetricFailover = 600
	MetricPrimary  = 50
)

// Network is one network of a scan (schema WifiNetwork).
type Network struct {
	SSID     string `json:"ssid"`
	Signal   int    `json:"signal"`
	Security string `json:"security"`
	Channel  int    `json:"channel"`
	InUse    bool   `json:"in_use"`
}

// Profile is a Wi-Fi connection of NetworkManager (schema WifiProfile).
type Profile struct {
	Name        string `json:"name"`
	SSID        string `json:"ssid"`
	Autoconnect bool   `json:"autoconnect"`
	Active      bool   `json:"active"`
	// Managed is true for care-pi-wifi. Other profiles come from the Imager settings (cloud-init) or from the operator.
	Managed bool `json:"managed"`
}

// Spec is the Wi-Fi connection that the operator wants.
type Spec struct {
	SSID     string
	PSK      string
	Country  string
	Failover bool
}

var countryRE = regexp.MustCompile(`^[A-Z]{2}$`)

// Validate checks the values of a Spec.
func (s Spec) Validate() error {
	if s.SSID == "" || len(s.SSID) > 32 {
		return errors.New("the SSID must have 1 to 32 bytes")
	}
	if l := len(s.PSK); l < 8 || l > 63 {
		return errors.New("the passphrase must have 8 to 63 characters")
	}
	for _, r := range s.PSK {
		if r < 0x20 || r > 0x7e {
			return errors.New("the passphrase must have only printable ASCII characters")
		}
	}
	if s.Country != "" && !countryRE.MatchString(s.Country) {
		return errors.New("the country must be an ISO 3166-1 alpha-2 code, for example IN")
	}
	return nil
}

// Fingerprint identifies the Spec without the PSK in clear text (state/wifi.json).
func (s Spec) Fingerprint() string {
	sum := sha256.Sum256([]byte(s.SSID + "\x00" + s.PSK + "\x00" + s.Country + "\x00" + strconv.FormatBool(s.Failover)))
	return fmt.Sprintf("%x", sum[:8])
}

// Client runs the nmcli commands.
type Client struct {
	Runner host.Runner
}

func (c Client) nmcli(ctx context.Context, args ...string) ([]byte, error) {
	return c.Runner.Run(ctx, host.Cmd{Name: "nmcli", Args: args})
}

// splitTerse splits one line of "nmcli -t": fields end at ":"; "\:" and "\\" are escapes.
func splitTerse(line string) []string {
	var out []string
	var b strings.Builder
	esc := false
	for _, r := range line {
		switch {
		case esc:
			b.WriteRune(r)
			esc = false
		case r == '\\':
			esc = true
		case r == ':':
			out = append(out, b.String())
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	return append(out, b.String())
}

// Device returns the first Wi-Fi device, or "".
func (c Client) Device(ctx context.Context) (string, error) {
	out, err := c.nmcli(ctx, "-t", "-f", "DEVICE,TYPE", "device", "status")
	if err != nil {
		return "", fmt.Errorf("nmcli device status: %w", err)
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := splitTerse(sc.Text())
		if len(f) >= 2 && f[1] == "wifi" {
			return f[0], nil
		}
	}
	return "", nil
}

// ParseScan reads "nmcli -t -f IN-USE,SSID,SIGNAL,SECURITY,CHAN device wifi list". It keeps the strongest entry
// of each SSID and drops hidden networks. The list is in order of signal, strongest first.
func ParseScan(out []byte) []Network {
	best := map[string]Network{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := splitTerse(sc.Text())
		if len(f) < 5 || f[1] == "" {
			continue
		}
		sig, _ := strconv.Atoi(f[2])
		ch, _ := strconv.Atoi(f[4])
		sec := strings.TrimSpace(f[3])
		if sec == "--" {
			sec = ""
		}
		n := Network{SSID: f[1], Signal: sig, Security: sec, Channel: ch, InUse: strings.TrimSpace(f[0]) == "*"}
		if o, ok := best[n.SSID]; !ok || n.Signal > o.Signal || n.InUse {
			n.InUse = n.InUse || o.InUse
			best[n.SSID] = n
		}
	}
	out2 := make([]Network, 0, len(best))
	for _, n := range best {
		out2 = append(out2, n)
	}
	sort.Slice(out2, func(i, j int) bool {
		if out2[i].Signal != out2[j].Signal {
			return out2[i].Signal > out2[j].Signal
		}
		return out2[i].SSID < out2[j].SSID
	})
	return out2
}

// Scan turns the radio on and lists the networks in range.
func (c Client) Scan(ctx context.Context) ([]Network, error) {
	if _, err := c.nmcli(ctx, "radio", "wifi", "on"); err != nil {
		return nil, fmt.Errorf("nmcli radio wifi on: %w", err)
	}
	out, err := c.nmcli(ctx, "-t", "-f", "IN-USE,SSID,SIGNAL,SECURITY,CHAN", "device", "wifi", "list", "--rescan", "yes")
	if err != nil {
		return nil, fmt.Errorf("nmcli device wifi list: %w", err)
	}
	return ParseScan(out), nil
}

// keyfileString escapes a GKeyFile string value.
func keyfileString(s string) string {
	var b strings.Builder
	for i, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == ' ' && i == 0:
			b.WriteString(`\s`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ssidValue writes the SSID as text when it is plain printable ASCII, else as the list of its bytes.
func ssidValue(ssid string) string {
	plain := true
	for i := 0; i < len(ssid); i++ {
		ch := ssid[i]
		if ch < 0x21 || ch > 0x7e || ch == ';' || ch == '\\' || ch == '#' || ch == '=' {
			plain = false
			break
		}
	}
	if plain {
		return ssid
	}
	parts := make([]string, 0, len(ssid))
	for i := 0; i < len(ssid); i++ {
		parts = append(parts, strconv.Itoa(int(ssid[i])))
	}
	return strings.Join(parts, ";") + ";"
}

// UUID is the fixed connection UUID of care-pi-wifi.
const UUID = "6f1b3c2a-8e4d-4c1a-9b2e-63617265702d"

// Keyfile returns the NetworkManager keyfile of the connection.
func Keyfile(s Spec, device string) []byte {
	prio, metric := 0, MetricPrimary
	if s.Failover {
		prio, metric = -10, MetricFailover
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Managed by care-pi (docs/connectivity.md). Change it with: care-pi network wifi set\n")
	fmt.Fprintf(&b, "[connection]\nid=%s\nuuid=%s\ntype=wifi\n", ConnName, UUID)
	if device != "" {
		fmt.Fprintf(&b, "interface-name=%s\n", device)
	}
	fmt.Fprintf(&b, "autoconnect=true\nautoconnect-priority=%d\n\n", prio)
	fmt.Fprintf(&b, "[wifi]\nmode=infrastructure\nssid=%s\n\n", ssidValue(s.SSID))
	fmt.Fprintf(&b, "[wifi-security]\nkey-mgmt=wpa-psk\npsk=%s\n\n", keyfileString(s.PSK))
	fmt.Fprintf(&b, "[ipv4]\nmethod=auto\nroute-metric=%d\n\n", metric)
	fmt.Fprintf(&b, "[ipv6]\naddr-gen-mode=default\nmethod=auto\nroute-metric=%d\n", metric)
	return []byte(b.String())
}

// writeKeyfileCmd writes the keyfile from stdin in a transient unit (care-pi.service cannot write /etc/NetworkManager).
func writeKeyfileCmd(data []byte) host.Cmd {
	return host.Cmd{Name: "systemd-run", Args: []string{"--wait", "--collect", "--pipe", "--quiet", "--unit=care-pi-wifi-keyfile",
		"--", "/bin/sh", "-c", KeyfileScript, "sh", KeyfilePath}, Stdin: bytes.NewReader(data)}
}

// KeyfileScript writes the keyfile of $1 from stdin. The helper allows only this script.
const KeyfileScript = `set -eu; umask 077; f="$1"; cat > "$f.tmp"; chmod 0600 "$f.tmp"; mv "$f.tmp" "$f"`

// SetCountry sets the regulatory domain. raspi-config persists it on Raspberry Pi OS (and unblocks the radio);
// another host gets "iw reg set" until the next boot.
func (c Client) SetCountry(ctx context.Context, cc string) error {
	if cc == "" {
		return nil
	}
	if _, err := c.Runner.Run(ctx, host.Cmd{Name: "sh", Args: []string{"-c", "command -v raspi-config"}}); err == nil {
		_, err := c.Runner.Run(ctx, host.Cmd{Name: "systemd-run", Args: []string{"--wait", "--collect", "--pipe", "--quiet",
			"--unit=care-pi-wifi-country", "--", "raspi-config", "nonint", "do_wifi_country", cc}})
		return err
	}
	_, err := c.Runner.Run(ctx, host.Cmd{Name: "iw", Args: []string{"reg", "set", cc}})
	return err
}

// ErrAuth is the error of a wrong passphrase.
var ErrAuth = errors.New("the network refused the passphrase")

// ErrNoDevice is the error of a host without a Wi-Fi device.
var ErrNoDevice = errors.New("this device has no Wi-Fi interface")

// Apply writes the profile, loads it, and connects. step is called at the start of each step.
func (c Client) Apply(ctx context.Context, s Spec, step func(name string) func(error)) error {
	if err := s.Validate(); err != nil {
		return err
	}
	run := func(name string, fn func() error) error {
		end := func(error) {}
		if step != nil {
			end = step(name)
		}
		err := fn()
		end(err)
		return err
	}
	var dev string
	if err := run("wifi-radio: nmcli radio wifi on", func() error {
		if _, err := c.nmcli(ctx, "radio", "wifi", "on"); err != nil {
			return fmt.Errorf("nmcli radio wifi on: %w", err)
		}
		d, err := c.Device(ctx)
		if err != nil {
			return err
		}
		if d == "" {
			return ErrNoDevice
		}
		dev = d
		if err := c.SetCountry(ctx, s.Country); err != nil {
			return fmt.Errorf("cannot set the Wi-Fi country %s: %w", s.Country, err)
		}
		return nil
	}); err != nil {
		return err
	}
	if err := run("wifi-profile: write "+KeyfilePath, func() error {
		if _, err := c.Runner.Run(ctx, writeKeyfileCmd(Keyfile(s, dev))); err != nil {
			return fmt.Errorf("cannot write the NetworkManager profile: %w", err)
		}
		if _, err := c.nmcli(ctx, "connection", "load", KeyfilePath); err != nil {
			return fmt.Errorf("nmcli connection load: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}
	return run("wifi-connect: nmcli connection up "+ConnName, func() error {
		out, err := c.nmcli(ctx, "--wait", "45", "connection", "up", ConnName)
		if err != nil {
			text := strings.ToLower(string(out) + " " + err.Error())
			if strings.Contains(text, "secrets were required") || strings.Contains(text, "802-1x") || strings.Contains(text, "no secrets") ||
				strings.Contains(text, "wrong password") || strings.Contains(text, "ssid-not-found") {
				return fmt.Errorf("%w: %s", ErrAuth, strings.TrimSpace(string(out)))
			}
			return fmt.Errorf("nmcli connection up: %w", err)
		}
		return nil
	})
}

// Remove deletes the profile. A missing profile is not an error.
func (c Client) Remove(ctx context.Context) error {
	out, err := c.nmcli(ctx, "connection", "delete", ConnName)
	if err != nil && !strings.Contains(strings.ToLower(string(out)), "unknown connection") {
		var xe *host.ExitError
		if errors.As(err, &xe) && xe.Code == 10 {
			return nil
		}
		return fmt.Errorf("nmcli connection delete: %w", err)
	}
	return nil
}

// Profiles lists the Wi-Fi connections of NetworkManager.
func (c Client) Profiles(ctx context.Context) ([]Profile, error) {
	out, err := c.nmcli(ctx, "-t", "-f", "NAME,TYPE,AUTOCONNECT,ACTIVE", "connection", "show")
	if err != nil {
		return nil, fmt.Errorf("nmcli connection show: %w", err)
	}
	var res []Profile
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := splitTerse(sc.Text())
		if len(f) < 4 || (f[1] != "802-11-wireless" && f[1] != "wifi") {
			continue
		}
		p := Profile{Name: f[0], Autoconnect: f[2] == "yes", Active: f[3] == "yes", Managed: f[0] == ConnName}
		if o, err := c.nmcli(ctx, "-t", "-g", "802-11-wireless.ssid", "connection", "show", f[0]); err == nil {
			p.SSID = strings.TrimSpace(string(o))
		}
		res = append(res, p)
	}
	return res, nil
}

// Link is the state of the Wi-Fi device (schema WifiState).
type Link struct {
	Device    string  `json:"device"`
	Radio     bool    `json:"radio"`
	Connected bool    `json:"connected"`
	SSID      *string `json:"ssid_active"`
	Signal    *int    `json:"signal"`
	Address   *string `json:"address"`
	// DefaultRoute is true when the default route of the host goes through Wi-Fi now.
	DefaultRoute bool `json:"default_route"`
}

// Route is one default route of "ip -json route show default".
type Route struct {
	Dev    string `json:"dev"`
	Metric int    `json:"metric"`
}

// DefaultRoutes reads the default routes.
func (c Client) DefaultRoutes(ctx context.Context) ([]Route, error) {
	out, err := c.Runner.Run(ctx, host.Cmd{Name: "ip", Args: []string{"-json", "route", "show", "default"}})
	if err != nil {
		return nil, err
	}
	var rs []Route
	if err := json.Unmarshal(out, &rs); err != nil {
		return nil, err
	}
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].Metric < rs[j].Metric })
	return rs, nil
}

// LinkState reads the state of the Wi-Fi device.
func (c Client) LinkState(ctx context.Context) (*Link, error) {
	l := &Link{}
	if out, err := c.nmcli(ctx, "radio", "wifi"); err == nil {
		l.Radio = strings.TrimSpace(string(out)) == "enabled"
	}
	dev, err := c.Device(ctx)
	if err != nil {
		return nil, err
	}
	l.Device = dev
	if dev == "" {
		return l, nil
	}
	if out, err := c.nmcli(ctx, "-t", "-f", "GENERAL.STATE,GENERAL.CONNECTION,IP4.ADDRESS", "device", "show", dev); err == nil {
		sc := bufio.NewScanner(bytes.NewReader(out))
		for sc.Scan() {
			k, v, _ := strings.Cut(sc.Text(), ":")
			switch {
			case k == "GENERAL.STATE":
				l.Connected = strings.HasPrefix(v, "100")
			case strings.HasPrefix(k, "IP4.ADDRESS") && l.Address == nil:
				if p, err := netip.ParsePrefix(v); err == nil {
					a := p.Addr().String()
					l.Address = &a
				}
			}
		}
	}
	if out, err := c.nmcli(ctx, "-t", "-f", "IN-USE,SSID,SIGNAL", "device", "wifi", "list", "--rescan", "no"); err == nil {
		sc := bufio.NewScanner(bytes.NewReader(out))
		for sc.Scan() {
			f := splitTerse(sc.Text())
			if len(f) >= 3 && strings.TrimSpace(f[0]) == "*" {
				ssid := f[1]
				l.SSID = &ssid
				if s, err := strconv.Atoi(f[2]); err == nil {
					l.Signal = &s
				}
			}
		}
	}
	if rs, err := c.DefaultRoutes(ctx); err == nil && len(rs) > 0 {
		l.DefaultRoute = rs[0].Dev == dev
	}
	return l, nil
}

// VerifyRoutes checks the route metrics after a change: with failover, an ethernet default route must win.
func VerifyRoutes(rs []Route, wifiDev string, failover bool) error {
	var wifi, other *Route
	for i := range rs {
		r := &rs[i]
		if r.Dev == wifiDev {
			if wifi == nil {
				wifi = r
			}
		} else if other == nil {
			other = r
		}
	}
	if wifi == nil {
		return fmt.Errorf("no default route through %s", wifiDev)
	}
	if failover && other != nil && wifi.Metric <= other.Metric {
		return fmt.Errorf("the route metric of %s (%d) is not higher than the metric of %s (%d)", wifiDev, wifi.Metric, other.Dev, other.Metric)
	}
	return nil
}

// WaitRoutes polls the default routes until VerifyRoutes passes, or until timeout ends.
func (c Client) WaitRoutes(ctx context.Context, dev string, failover bool, timeout, poll time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		rs, err := c.DefaultRoutes(ctx)
		if err == nil {
			if err = VerifyRoutes(rs, dev, failover); err == nil {
				return nil
			}
		}
		if !time.Now().Before(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}
