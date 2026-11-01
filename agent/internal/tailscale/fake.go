package tailscale

// StatusJSON is a trimmed "tailscale status --json" of tailscale 1.90 for the tests of other packages:
// the node care.tail1234.ts.net with the address 100.101.102.103 and HTTPS certificates on.
func StatusJSON(state, authURL string) string {
	return `{"Version":"1.90.4-t1234","BackendState":"` + state + `","AuthURL":"` + authURL + `",
"TailscaleIPs":["100.101.102.103","fd7a:115c:a1e0::1"],
"Self":{"HostName":"care","DNSName":"care.tail1234.ts.net.","TailscaleIPs":["100.101.102.103","fd7a:115c:a1e0::1"],"Online":true},
"CurrentTailnet":{"Name":"clinic@example.com","MagicDNSSuffix":"tail1234.ts.net","MagicDNSEnabled":true},
"CertDomains":["care.tail1234.ts.net"],"Health":[]}`
}
