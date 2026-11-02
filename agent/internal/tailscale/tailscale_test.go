package tailscale

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/host"
)

func TestParseStatus(t *testing.T) {
	st, err := ParseStatus([]byte(StatusJSON(StateRunning, "")))
	if err != nil {
		t.Fatal(err)
	}
	if st.DNSName != "care.tail1234.ts.net" || st.IPv4 != "100.101.102.103" || st.Tailnet != "clinic@example.com" ||
		!st.Online || !st.HTTPSEnabled() || st.BackendState != StateRunning {
		t.Fatalf("status %+v", st)
	}
	st, _ = ParseStatus([]byte(`{"BackendState":"NeedsLogin","AuthURL":"https://login.tailscale.com/a/abc","Self":{"DNSName":""}}`))
	if st.AuthURL == "" || st.HTTPSEnabled() || st.IPv4 != "" {
		t.Fatalf("needs login %+v", st)
	}
}

func TestInstallOnlyWhenMissing(t *testing.T) {
	f := host.NewFake()
	f.On("tailscale version", host.Response{Code: 127}, host.Response{Out: "1.90.4"})
	c := Client{Runner: f}
	did, err := c.Install(context.Background())
	if err != nil || !did {
		t.Fatalf("install %v %v", did, err)
	}
	calls := f.Filter("systemd-run")
	if len(calls) != 1 || !strings.Contains(calls[0], "--unit="+InstallUnit) || !strings.Contains(calls[0], "pkgs.tailscale.com/stable") ||
		!strings.Contains(calls[0], "apt-get -o DPkg::Lock::Timeout=600 -y install tailscale") {
		t.Fatalf("install command %q", calls)
	}
	did, err = c.Install(context.Background())
	if err != nil || did {
		t.Fatalf("second install %v %v", did, err)
	}
	if len(f.Filter("systemd-run")) != 1 {
		t.Fatal("installed twice")
	}
}

func TestUpWithKeyKeepsTheKeyOffTheCommandLine(t *testing.T) {
	f := host.NewFake()
	dir := t.TempDir()
	var seen string
	f.Hook = func(c host.Cmd, _ []byte) (host.Response, bool) {
		if c.Name == "tailscale" && len(c.Args) > 0 && c.Args[0] == "up" {
			for _, a := range c.Args {
				if p, ok := strings.CutPrefix(a, "--auth-key=file:"); ok {
					data, _ := os.ReadFile(p)
					seen = strings.TrimSpace(string(data))
					st, _ := os.Stat(p)
					if st.Mode().Perm() != 0o600 {
						t.Errorf("key file mode %v", st.Mode())
					}
				}
			}
			return host.Response{}, true
		}
		return host.Response{}, false
	}
	c := Client{Runner: f, RunDir: dir}
	if err := c.UpWithKey(context.Background(), "care", "tskey-auth-SECRET"); err != nil {
		t.Fatal(err)
	}
	if seen != "tskey-auth-SECRET" {
		t.Fatalf("tailscale read %q", seen)
	}
	for _, call := range f.Calls {
		if strings.Contains(call, "SECRET") {
			t.Fatalf("the key is on a command line: %s", call)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "tailscale-auth-key")); err == nil {
		t.Fatal("the key file is still there")
	}
	if !strings.Contains(f.Calls[0], "--hostname=care --accept-dns=false") {
		t.Fatalf("up %q", f.Calls[0])
	}
}

func TestLoginFlow(t *testing.T) {
	f := host.NewFake()
	f.On("tailscale status --json",
		host.Response{Out: StatusJSON(StateNeedsLogin, "https://login.tailscale.com/a/abc"), Code: 1},
		host.Response{Out: StatusJSON(StateRunning, "")})
	c := Client{Runner: f, Poll: time.Millisecond}
	if err := c.StartLogin(context.Background(), "care"); err != nil {
		t.Fatal(err)
	}
	if got := f.Filter("systemd-run"); len(got) != 1 || !strings.Contains(got[0], "--unit="+UpUnit+" -- tailscale up --hostname=care") ||
		!strings.Contains(got[0], "--timeout=15m0s") {
		t.Fatalf("start login %q", got)
	}
	var urls []string
	st, err := c.WaitFor(context.Background(), time.Second, func(s *Status) {
		if s.AuthURL != "" {
			urls = append(urls, s.AuthURL)
		}
	}, func(s *Status) bool { return s.BackendState == StateRunning })
	if err != nil || st.DNSName != "care.tail1234.ts.net" || len(urls) != 1 {
		t.Fatalf("wait %+v %v %v", st, err, urls)
	}
}

func TestStatusWithoutTailscale(t *testing.T) {
	f := host.NewFake()
	f.On("tailscale version", host.Response{Code: 127})
	st, err := Client{Runner: f}.Status(context.Background())
	if err != nil || st.Installed {
		t.Fatalf("%+v %v", st, err)
	}
}

// serveJSON is "tailscale serve status --json" of lumine (tailscale 1.104): a serve on 443 of the node, and a
// Tailscale Service with its own address. Only the TCP map of the node counts.
const serveJSON = `{"TCP":{"443":{"HTTPS":true}},"Web":{"lumine.orthrus-bass.ts.net:443":{"Handlers":{"/":{"Proxy":"http://10.43.99.23:8080"}}}},
"Services":{"svc:care-box":{"TCP":{"443":{"HTTPS":true}},"Web":{"care-box.orthrus-bass.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:8080"}}}}}}}`

func TestServePorts(t *testing.T) {
	cases := map[string][]int{
		serveJSON:                                {443},
		`{"TCP":{"8443":{},"80":{},"10000":{}}}`: {80, 8443, 10000},
		`{}`:                                     nil,
		``:                                       nil,
		`No serve config`:                        nil,
	}
	for in, want := range cases {
		if got := ParseServePorts([]byte(in)); !equalInts(got, want) {
			t.Errorf("%q: got %v, want %v", in, got, want)
		}
	}
	f := host.NewFake()
	f.On("tailscale serve status --json", host.Response{Out: serveJSON})
	if got := (Client{Runner: f}).ServePorts(context.Background()); !equalInts(got, []int{443}) {
		t.Fatalf("client %v", got)
	}
	f.Set("tailscale serve status --json", host.Response{Code: 1})
	if got := (Client{Runner: f}).ServePorts(context.Background()); got != nil {
		t.Fatalf("a failed command gives no ports, got %v", got)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
