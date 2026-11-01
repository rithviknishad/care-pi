package wifi

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rithviknishad/care-pi/agent/internal/host"
)

func TestParseScan(t *testing.T) {
	out := []byte(` :Sunrise-Staff:72:WPA2:6
 :Sunrise-Staff:40:WPA2:36
*:Clinic\:Guest:55:WPA1 WPA2:11
 ::80:WPA2:1
 :Open Cafe:30:--:1
`)
	got := ParseScan(out)
	if len(got) != 3 {
		t.Fatalf("networks %+v", got)
	}
	if got[0].SSID != "Sunrise-Staff" || got[0].Signal != 72 || got[0].Channel != 6 {
		t.Errorf("strongest first, one entry for each SSID: %+v", got[0])
	}
	if got[1].SSID != "Clinic:Guest" || !got[1].InUse || got[1].Security != "WPA1 WPA2" {
		t.Errorf("escaped colon and in use: %+v", got[1])
	}
	if got[2].Security != "" {
		t.Errorf("open network: %+v", got[2])
	}
}

func TestKeyfile(t *testing.T) {
	k := string(Keyfile(Spec{SSID: "Sunrise-Staff", PSK: ` pass\word#1`, Failover: true}, "wlan0"))
	for _, want := range []string{"id=care-pi-wifi\n", "interface-name=wlan0\n", "autoconnect-priority=-10\n", "ssid=Sunrise-Staff\n",
		"key-mgmt=wpa-psk\n", `psk=\spass\\word#1` + "\n", "[ipv4]\nmethod=auto\nroute-metric=600\n", "[ipv6]\naddr-gen-mode=default\nmethod=auto\nroute-metric=600\n"} {
		if !strings.Contains(k, want) {
			t.Errorf("keyfile lacks %q:\n%s", want, k)
		}
	}
	k = string(Keyfile(Spec{SSID: "Café; 2", PSK: "12345678"}, ""))
	if !strings.Contains(k, "ssid=67;97;102;195;169;59;32;50;\n") || !strings.Contains(k, "route-metric=50\n") || strings.Contains(k, "interface-name") {
		t.Errorf("byte list SSID and primary metric:\n%s", k)
	}
}

func TestValidate(t *testing.T) {
	ok := Spec{SSID: "x", PSK: "12345678", Country: "IN"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, s := range []Spec{{SSID: "", PSK: "12345678"}, {SSID: "x", PSK: "short"}, {SSID: "x", PSK: "12345678", Country: "in"},
		{SSID: strings.Repeat("a", 33), PSK: "12345678"}, {SSID: "x", PSK: "1234567\n8"}} {
		if s.Validate() == nil {
			t.Errorf("valid: %+v", s)
		}
	}
}

func TestApplyKeepsThePSKOffTheCommandLine(t *testing.T) {
	f := host.NewFake()
	f.On("nmcli -t -f DEVICE,TYPE device status", host.Response{Out: "eth0:ethernet\nwlan0:wifi\nlo:loopback\n"})
	f.On("sh -c command -v raspi-config", host.Response{Out: "/usr/bin/raspi-config"})
	var keyfile []byte
	f.Hook = func(c host.Cmd, stdin []byte) (host.Response, bool) {
		if c.Name == "systemd-run" && strings.Contains(strings.Join(c.Args, " "), "care-pi-wifi-keyfile") {
			keyfile = stdin
			return host.Response{}, true
		}
		return host.Response{}, false
	}
	var steps []string
	err := Client{Runner: f}.Apply(context.Background(), Spec{SSID: "Sunrise-Staff", PSK: "s3cret-passphrase", Country: "IN", Failover: true},
		func(name string) func(error) {
			steps = append(steps, strings.SplitN(name, ":", 2)[0])
			return func(error) {}
		})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(steps, ",") != "wifi-radio,wifi-profile,wifi-connect" {
		t.Fatalf("steps %v", steps)
	}
	if !strings.Contains(string(keyfile), "psk=s3cret-passphrase") {
		t.Fatalf("the keyfile goes through stdin: %q", keyfile)
	}
	for _, c := range f.Calls {
		if strings.Contains(c, "s3cret") {
			t.Fatalf("the PSK is on a command line: %s", c)
		}
	}
	want := []string{"nmcli radio wifi on", "nmcli -t -f DEVICE,TYPE device status", "sh -c command -v raspi-config",
		"systemd-run --wait --collect --pipe --quiet --unit=care-pi-wifi-country -- raspi-config nonint do_wifi_country IN"}
	for i, w := range want {
		if f.Calls[i] != w {
			t.Fatalf("call %d %q, want %q", i, f.Calls[i], w)
		}
	}
	if got := f.Filter("nmcli connection load", "nmcli --wait 45 connection up"); len(got) != 2 ||
		got[0] != "nmcli connection load "+KeyfilePath || got[1] != "nmcli --wait 45 connection up care-pi-wifi" {
		t.Fatalf("load and up: %q", got)
	}
}

func TestApplyWrongPassphrase(t *testing.T) {
	f := host.NewFake()
	f.On("nmcli -t -f DEVICE,TYPE device status", host.Response{Out: "wlan0:wifi\n"})
	f.On("sh -c command -v raspi-config", host.Response{Code: 1})
	f.On("nmcli --wait 45 connection up care-pi-wifi", host.Response{Out: "Error: Connection activation failed: Secrets were required, but not provided.", Code: 4})
	err := Client{Runner: f}.Apply(context.Background(), Spec{SSID: "x", PSK: "12345678", Country: "IN"}, nil)
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("want ErrAuth: %v", err)
	}
	if len(f.Filter("iw reg set IN")) != 1 {
		t.Fatal("a host without raspi-config uses iw")
	}
}

func TestApplyNoDevice(t *testing.T) {
	f := host.NewFake()
	f.On("nmcli -t -f DEVICE,TYPE device status", host.Response{Out: "eth0:ethernet\n"})
	if err := (Client{Runner: f}).Apply(context.Background(), Spec{SSID: "x", PSK: "12345678"}, nil); !errors.Is(err, ErrNoDevice) {
		t.Fatalf("want ErrNoDevice: %v", err)
	}
}

func TestVerifyRoutes(t *testing.T) {
	both := []Route{{Dev: "eth0", Metric: 100}, {Dev: "wlan0", Metric: 600}}
	if err := VerifyRoutes(both, "wlan0", true); err != nil {
		t.Fatal(err)
	}
	if err := VerifyRoutes([]Route{{Dev: "wlan0", Metric: 50}, {Dev: "eth0", Metric: 100}}, "wlan0", true); err == nil {
		t.Fatal("Wi-Fi wins over ethernet with failover on")
	}
	if err := VerifyRoutes([]Route{{Dev: "wlan0", Metric: 600}}, "wlan0", true); err != nil {
		t.Fatalf("cable out: %v", err)
	}
	if err := VerifyRoutes([]Route{{Dev: "eth0", Metric: 100}}, "wlan0", true); err == nil {
		t.Fatal("no Wi-Fi route")
	}
}

func TestLinkStateAndProfiles(t *testing.T) {
	f := host.NewFake()
	f.On("nmcli radio wifi", host.Response{Out: "enabled\n"})
	f.On("nmcli -t -f DEVICE,TYPE device status", host.Response{Out: "eth0:ethernet\nwlan0:wifi\n"})
	f.On("nmcli -t -f GENERAL.STATE,GENERAL.CONNECTION,IP4.ADDRESS device show wlan0",
		host.Response{Out: "GENERAL.STATE:100 (connected)\nGENERAL.CONNECTION:care-pi-wifi\nIP4.ADDRESS[1]:192.168.1.23/24\n"})
	f.On("nmcli -t -f IN-USE,SSID,SIGNAL device wifi list --rescan no", host.Response{Out: " :Other:20\n*:Sunrise-Staff:67\n"})
	f.On("ip -json route show default", host.Response{Out: `[{"dst":"default","gateway":"192.168.1.1","dev":"eth0","metric":100},{"dst":"default","gateway":"192.168.1.1","dev":"wlan0","metric":600}]`})
	l, err := Client{Runner: f}.LinkState(context.Background())
	if err != nil || !l.Radio || !l.Connected || *l.SSID != "Sunrise-Staff" || *l.Signal != 67 || *l.Address != "192.168.1.23" || l.DefaultRoute {
		t.Fatalf("link %+v %v", l, err)
	}
	f.On("nmcli -t -f NAME,TYPE,AUTOCONNECT,ACTIVE connection show",
		host.Response{Out: "Wired connection 1:802-3-ethernet:yes:yes\npreconfigured:802-11-wireless:yes:no\ncare-pi-wifi:802-11-wireless:yes:yes\n"})
	f.On("nmcli -t -g 802-11-wireless.ssid connection show preconfigured", host.Response{Out: "Home\n"})
	ps, err := Client{Runner: f}.Profiles(context.Background())
	if err != nil || len(ps) != 2 || ps[0].SSID != "Home" || ps[0].Managed || !ps[1].Managed || !ps[1].Active {
		t.Fatalf("profiles %+v %v", ps, err)
	}
}
