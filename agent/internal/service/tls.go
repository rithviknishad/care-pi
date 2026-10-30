package service

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"html"
	"os"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
)

// CARoot is the root CA of the local CA (schema CaRoot).
type CARoot struct {
	Subject           string    `json:"subject"`
	FingerprintSHA256 string    `json:"fingerprint_sha256"`
	FingerprintColon  string    `json:"fingerprint_sha256_colon"`
	NotBefore         time.Time `json:"not_before"`
	NotAfter          time.Time `json:"not_after"`
	NameConstraints   any       `json:"name_constraints"`
}

// TLSState is the TLS modes (schema TlsState).
type TLSState struct {
	LocalCA struct {
		Enabled   bool     `json:"enabled"`
		Root      *CARoot  `json:"root"`
		Hostnames []string `json:"hostnames"`
	} `json:"local_ca"`
	Public struct {
		Enabled  bool    `json:"enabled"`
		ClinicID *string `json:"clinic_id"`
		Hostname *string `json:"hostname"`
	} `json:"public"`
	Tailscale struct {
		Enabled  bool    `json:"enabled"`
		Hostname *string `json:"hostname"`
	} `json:"tailscale"`
}

// rootCA reads the root certificate of the local CA of Caddy.
func (s *Service) rootCA() (*x509.Certificate, []byte, error) {
	data, err := os.ReadFile(s.Paths.Abs(paths.CaddyRootCA))
	if err != nil {
		return nil, nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, nil, fmt.Errorf("%s has no PEM certificate", paths.CaddyRootCA)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot parse %s: %w", paths.CaddyRootCA, err)
	}
	return cert, block.Bytes, nil
}

func fingerprints(der []byte) (string, string) {
	sum := sha256.Sum256(der)
	h := hex.EncodeToString(sum[:])
	var parts []string
	for i := 0; i < len(h); i += 2 {
		parts = append(parts, strings.ToUpper(h[i:i+2]))
	}
	return h, strings.Join(parts, ":")
}

func caRoot(cert *x509.Certificate, der []byte) *CARoot {
	fp, colon := fingerprints(der)
	return &CARoot{Subject: cert.Subject.String(), FingerprintSHA256: fp, FingerprintColon: colon,
		NotBefore: cert.NotBefore.UTC(), NotAfter: cert.NotAfter.UTC()}
}

// TLS returns the TLS modes and the root CA.
func (s *Service) TLS() *TLSState {
	cfg, err := config.Load(s.configPath())
	if err != nil {
		cfg = config.Default()
	}
	t := &TLSState{}
	t.LocalCA.Enabled = cfg.TLS.LocalCA.Enabled
	t.LocalCA.Hostnames = append(s.Hostnames(), s.Addresses()...)
	if cert, der, err := s.rootCA(); err == nil {
		t.LocalCA.Root = caRoot(cert, der)
	}
	t.Public.Enabled = cfg.TLS.Public.Enabled
	t.Public.ClinicID = strPtr(cfg.TLS.Public.ClinicID)
	t.Tailscale.Enabled = cfg.TLS.Tailscale.Enabled
	return t
}

// RootCAFile is a download of the root CA.
type RootCAFile struct {
	Data        []byte
	ContentType string
	Filename    string
	Fingerprint string
}

func (s *Service) rootFileBase() string {
	return "care-pi-" + strings.ReplaceAll(s.PrimaryHost(), ".", "-") + "-root-ca"
}

func (s *Service) loadRoot() (*x509.Certificate, []byte, error) {
	if cfg, err := config.Load(s.configPath()); err == nil && !cfg.TLS.LocalCA.Enabled {
		return nil, nil, problem.New(problem.NotFound, "The local CA mode is off.")
	}
	cert, der, err := s.rootCA()
	if err != nil {
		return nil, nil, problem.New(problem.NotFound, "The local CA does not exist yet. Caddy makes it at its first start.")
	}
	return cert, der, nil
}

// RootCA returns the root CA as DER ("der") or PEM ("pem").
func (s *Service) RootCA(format string) (*RootCAFile, error) {
	if format == "" {
		format = "der"
	}
	if format != "der" && format != "pem" {
		return nil, problem.InvalidParam("format", "use der or pem")
	}
	_, der, err := s.loadRoot()
	if err != nil {
		return nil, err
	}
	fp, _ := fingerprints(der)
	f := &RootCAFile{Data: der, ContentType: "application/x-x509-ca-cert", Filename: s.rootFileBase() + ".crt", Fingerprint: fp}
	if format == "pem" {
		f.Data = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		f.ContentType, f.Filename = "application/x-pem-file", s.rootFileBase()+".pem"
	}
	return f, nil
}

func uuidOf(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	h := strings.ToUpper(hex.EncodeToString(sum[:16]))
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// Mobileconfig returns an unsigned Apple profile with the root CA (07 §4.3).
func (s *Service) Mobileconfig() (*RootCAFile, error) {
	cert, der, err := s.loadRoot()
	if err != nil {
		return nil, err
	}
	fp, _ := fingerprints(der)
	host := s.PrimaryHost()
	name := cert.Subject.CommonName
	if name == "" {
		name = "care-pi root CA"
	}
	esc := html.EscapeString
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array>
		<dict>
			<key>PayloadCertificateFileName</key>
			<string>` + esc(s.rootFileBase()) + `.cer</string>
			<key>PayloadContent</key>
			<data>` + base64.StdEncoding.EncodeToString(der) + `</data>
			<key>PayloadDescription</key>
			<string>The root certificate of the care-pi device ` + esc(host) + `.</string>
			<key>PayloadDisplayName</key>
			<string>` + esc(name) + `</string>
			<key>PayloadIdentifier</key>
			<string>dev.rithviknishad.care-pi.root.` + fp[:16] + `</string>
			<key>PayloadType</key>
			<string>com.apple.security.root</string>
			<key>PayloadUUID</key>
			<string>` + uuidOf(fp+":root") + `</string>
			<key>PayloadVersion</key>
			<integer>1</integer>
		</dict>
	</array>
	<key>PayloadDescription</key>
	<string>Install this profile so that the device trusts CARE on ` + esc(host) + `. SHA-256 fingerprint: ` + fp + `.</string>
	<key>PayloadDisplayName</key>
	<string>care-pi ` + esc(host) + ` root certificate</string>
	<key>PayloadIdentifier</key>
	<string>dev.rithviknishad.care-pi.profile.` + fp[:16] + `</string>
	<key>PayloadRemovalDisallowed</key>
	<false/>
	<key>PayloadType</key>
	<string>Configuration</string>
	<key>PayloadUUID</key>
	<string>` + uuidOf(fp+":profile") + `</string>
	<key>PayloadVersion</key>
	<integer>1</integer>
</dict>
</plist>
`)
	return &RootCAFile{Data: []byte(b.String()), ContentType: "application/x-apple-aspen-config",
		Filename: s.rootFileBase() + ".mobileconfig", Fingerprint: fp}, nil
}

// InstructionStep is one step as a message key with parameters (schema InstructionStep).
type InstructionStep struct {
	Key    string            `json:"key"`
	Params map[string]string `json:"params"`
}

// TLSInstructions are the install steps for one OS (schema TlsInstructions).
type TLSInstructions struct {
	OS   string `json:"os"`
	Root struct {
		FingerprintSHA256 string `json:"fingerprint_sha256"`
		FingerprintColon  string `json:"fingerprint_sha256_colon"`
		Filename          string `json:"filename"`
		DownloadPath      string `json:"download_path"`
		MediaType         string `json:"media_type"`
	} `json:"root"`
	Steps []InstructionStep `json:"steps"`
	Notes []InstructionStep `json:"notes"`
}

// InstructionTexts are the English texts of the message keys, in ASD-STE100.
// The console has its own text for each key; docs/console-server.md lists these texts.
var InstructionTexts = map[string]string{
	"tls.install.compare-fingerprint":        "Compare the SHA-256 fingerprint of the certificate with {fingerprint}. If the two values are not the same, do not install the certificate.",
	"tls.install.ios.download":               "Open the download link in Safari. Tap Allow to download the configuration profile.",
	"tls.install.ios.open-settings":          "Open Settings. Tap Profile Downloaded.",
	"tls.install.ios.install-profile":        "Tap Install. Enter the passcode of the device. Tap Install again.",
	"tls.install.ios.trust-settings":         "Open Settings > General > About > Certificate Trust Settings. Turn on full trust for {name}.",
	"tls.note.ios.ipados":                    "Use the same steps on an iPad.",
	"tls.install.android.download":           "Download the certificate file {filename}.",
	"tls.install.android.open-settings":      "Open Settings > Security > Encryption and credentials > Install a certificate > CA certificate.",
	"tls.install.android.install-anyway":     "Tap Install anyway. Select the file {filename}.",
	"tls.note.android.vendor-path":           "The path in Settings is different on some phones. Search the settings for \"CA certificate\".",
	"tls.note.android.firefox":               "Firefox does not use the certificates of the device by default. In Firefox, open about:config and set security.enterprise_roots.enabled to true.",
	"tls.install.windows.download":           "Download the certificate file {filename}.",
	"tls.install.windows.open":               "Open the file. Click Install Certificate.",
	"tls.install.windows.store":              "Select Current User. Select \"Place all certificates in the following store\". Click Browse. Select Trusted Root Certification Authorities.",
	"tls.install.windows.confirm":            "Click Finish. In the security warning, click Yes.",
	"tls.note.windows.restart-browser":       "Close the browser and open it again.",
	"tls.install.macos.download":             "Download the certificate file {filename}.",
	"tls.install.macos.open":                 "Open the file. Keychain Access opens. Add the certificate to the login keychain.",
	"tls.install.macos.trust":                "In Keychain Access, open the certificate {name}. Expand Trust. Set \"When using this certificate\" to Always Trust.",
	"tls.install.macos.confirm":              "Close the window. Enter your password to save the change.",
	"tls.install.linux.download":             "Download the certificate file {filename}.",
	"tls.install.linux.system":               "On Debian or Ubuntu: copy the file to /usr/local/share/ca-certificates/ with the extension .crt. Then run: sudo update-ca-certificates",
	"tls.install.linux.browser":              "Chrome and Firefox can use their own certificate stores. In the certificate settings of the browser, import the file as an authority.",
	"tls.install.chromeos.download":          "Download the certificate file {filename}.",
	"tls.install.chromeos.open-settings":     "Open Settings > Privacy and security > Security > Manage certificates > Authorities.",
	"tls.install.chromeos.import":            "Click Import. Select the file {filename}. Select \"Trust this certificate for identifying websites\". Click OK.",
	"tls.note.common.one-certificate-per-pi": "Each care-pi device has its own root certificate.",
}

// ClientOSes are the values of {os}.
var ClientOSes = []string{"ios", "android", "windows", "macos", "linux", "chromeos"}

// Instructions returns the install steps of the root CA for one OS. prefix is "/api/v1" or "/_care-pi".
func (s *Service) Instructions(clientOS, prefix string) (*TLSInstructions, error) {
	known := false
	for _, o := range ClientOSes {
		known = known || o == clientOS
	}
	if !known {
		return nil, problem.InvalidParam("os", "use one of "+strings.Join(ClientOSes, ", "))
	}
	cert, der, err := s.loadRoot()
	if err != nil {
		return nil, err
	}
	fp, colon := fingerprints(der)
	out := &TLSInstructions{OS: clientOS, Steps: []InstructionStep{}, Notes: []InstructionStep{}}
	out.Root.FingerprintSHA256, out.Root.FingerprintColon = fp, colon
	base := s.rootFileBase()
	switch clientOS {
	case "ios":
		out.Root.Filename, out.Root.DownloadPath, out.Root.MediaType = base+".mobileconfig", prefix+"/tls/root-ca/mobileconfig", "application/x-apple-aspen-config"
	case "linux":
		out.Root.Filename, out.Root.DownloadPath, out.Root.MediaType = base+".pem", prefix+"/tls/root-ca?format=pem", "application/x-pem-file"
	default:
		out.Root.Filename, out.Root.DownloadPath, out.Root.MediaType = base+".crt", prefix+"/tls/root-ca?format=der", "application/x-x509-ca-cert"
	}
	name := cert.Subject.CommonName
	params := map[string]string{"name": name, "filename": out.Root.Filename, "fingerprint": colon}
	step := func(key string, keys ...string) InstructionStep {
		p := map[string]string{}
		for _, k := range keys {
			p[k] = params[k]
		}
		return InstructionStep{Key: key, Params: p}
	}
	compare := step("tls.install.compare-fingerprint", "fingerprint")
	switch clientOS {
	case "ios":
		out.Steps = []InstructionStep{step("tls.install.ios.download"), step("tls.install.ios.open-settings"),
			step("tls.install.ios.install-profile"), compare, step("tls.install.ios.trust-settings", "name")}
		out.Notes = []InstructionStep{step("tls.note.ios.ipados")}
	case "android":
		out.Steps = []InstructionStep{step("tls.install.android.download", "filename"), compare,
			step("tls.install.android.open-settings"), step("tls.install.android.install-anyway", "filename")}
		out.Notes = []InstructionStep{step("tls.note.android.vendor-path"), step("tls.note.android.firefox")}
	case "windows":
		out.Steps = []InstructionStep{step("tls.install.windows.download", "filename"), compare,
			step("tls.install.windows.open"), step("tls.install.windows.store"), step("tls.install.windows.confirm")}
		out.Notes = []InstructionStep{step("tls.note.windows.restart-browser")}
	case "macos":
		out.Steps = []InstructionStep{step("tls.install.macos.download", "filename"), compare,
			step("tls.install.macos.open"), step("tls.install.macos.trust", "name"), step("tls.install.macos.confirm")}
	case "linux":
		out.Steps = []InstructionStep{step("tls.install.linux.download", "filename"), compare,
			step("tls.install.linux.system"), step("tls.install.linux.browser")}
	case "chromeos":
		out.Steps = []InstructionStep{step("tls.install.chromeos.download", "filename"), compare,
			step("tls.install.chromeos.open-settings"), step("tls.install.chromeos.import", "filename")}
	}
	out.Notes = append(out.Notes, step("tls.note.common.one-certificate-per-pi"))
	return out, nil
}
