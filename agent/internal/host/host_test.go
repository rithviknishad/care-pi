package host

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestParseShow(t *testing.T) {
	out := "Id=care-api.service\nActiveState=active\n\nId=care-db.service\nActiveState=inactive\n"
	got := ParseShow([]byte(out), 3)
	if len(got) != 3 || got[0]["ActiveState"] != "active" || got[1]["Id"] != "care-db.service" || len(got[2]) != 0 {
		t.Fatalf("unexpected: %v", got)
	}
}

func TestParseHealth(t *testing.T) {
	out := "care-api\tUp 5 minutes (healthy)\ncare-db\tUp 6 minutes (starting)\ncare-s3\tUp 6 minutes\ncare-beat\tExited (1) 2 seconds ago\n"
	want := map[string]string{"care-api": "healthy", "care-db": "starting", "care-s3": "", "care-beat": "stopped"}
	if got := ParseHealth([]byte(out)); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func pkt(s string) string {
	return strings.ToLower(strings.Repeat("0", 4-len(itoa16(len(s)+4)))+itoa16(len(s)+4)) + s
}

func itoa16(n int) string {
	const hex = "0123456789abcdef"
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{hex[n%16]}, b...)
		n /= 16
	}
	return string(b)
}

func TestParseRefAdvertisement(t *testing.T) {
	sha1 := strings.Repeat("a", 40)
	sha2 := strings.Repeat("b", 40)
	sha3 := strings.Repeat("c", 40)
	body := pkt("# service=git-upload-pack\n") + "0000" +
		pkt(sha1+" HEAD\x00multi_ack symref=HEAD:refs/heads/develop\n") +
		pkt(sha1+" refs/heads/develop\n") +
		pkt(sha2+" refs/tags/v25.2.0\n") +
		pkt(sha3+" refs/tags/v25.2.0^{}\n") + "0000"
	refs, err := ParseRefAdvertisement(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{"develop": sha1, "v25.2.0": sha3, "HEAD": sha1, sha2: sha2}
	for ref, want := range cases {
		if got, ok := ResolveRef(refs, ref); !ok || got != want {
			t.Errorf("ResolveRef(%s) = %s %v, want %s", ref, got, ok, want)
		}
	}
	if _, ok := ResolveRef(refs, "main"); ok {
		t.Error("main must not resolve")
	}
}

func TestFakeScript(t *testing.T) {
	f := NewFake().On("a b", Response{Out: "1"}, Response{Out: "2"})
	h := New(f)
	ctx := context.Background()
	for _, want := range []string{"1", "2", "2"} {
		out, _ := h.Runner.Run(ctx, Cmd{Name: "a", Args: []string{"b"}})
		if string(out) != want {
			t.Fatalf("got %q, want %q", out, want)
		}
	}
	f.On("podman image exists x", Response{Code: 1})
	if ok, err := h.Podman.ImageExists(ctx, "x"); ok || err != nil {
		t.Fatalf("ImageExists = %v %v", ok, err)
	}
	if ok, err := h.Podman.ImageExists(ctx, "y"); !ok || err != nil {
		t.Fatalf("ImageExists = %v %v", ok, err)
	}
	f.On("timedatectl show --property NTPSynchronized --value", Response{Out: "yes\n"})
	if ok, _ := h.Clock.NTPSynchronized(ctx); !ok {
		t.Fatal("want synchronized")
	}
}

func TestResetFailedIgnoresNotLoaded(t *testing.T) {
	const line = "systemctl reset-failed care-pod.service care-api.service"
	notLoaded := "Failed to reset failed state of unit care-pod.service: Unit care-pod.service not loaded.\n" +
		"Failed to reset failed state of unit care-api.service: Unit care-api.service not loaded.\n"
	f := NewFake().On(line, Response{Err: &ExitError{Cmd: line, Code: 1, Stderr: notLoaded}})
	s := ExecSystemd{R: f}
	if err := s.ResetFailed(context.Background(), "care-pod.service", "care-api.service"); err != nil {
		t.Fatalf("not loaded units must not fail: %v", err)
	}

	other := notLoaded + "Failed to reset failed state of unit care-db.service: Access denied\n"
	f = NewFake().On(line, Response{Err: &ExitError{Cmd: line, Code: 1, Stderr: other}})
	s = ExecSystemd{R: f}
	if err := s.ResetFailed(context.Background(), "care-pod.service", "care-api.service"); err == nil {
		t.Fatal("an error other than not loaded must fail")
	}

	f = NewFake().On(line, Response{Code: 1})
	s = ExecSystemd{R: f}
	if err := s.ResetFailed(context.Background(), "care-pod.service", "care-api.service"); err == nil {
		t.Fatal("an exit error without stderr must fail")
	}
}

func TestDevKeepsSecretsAndStops(t *testing.T) {
	ctx := context.Background()
	h := New(NewDev())
	if err := h.Podman.SecretSet(ctx, "care-pi-jwks-base64", []byte("x")); err != nil {
		t.Fatal(err)
	}
	names, err := h.Podman.SecretNames(ctx)
	if err != nil || !names["care-pi-jwks-base64"] || len(names) != 1 {
		t.Fatalf("secrets %v %v", names, err)
	}
	state := func() string {
		p, err := h.Systemd.Show(ctx, []string{"care-api.service"}, "ActiveState")
		if err != nil {
			t.Fatal(err)
		}
		return p[0]["ActiveState"]
	}
	if state() != "active" {
		t.Fatal("a unit is active at the start")
	}
	_ = h.Systemd.Stop(ctx, "care.target")
	if state() != "inactive" {
		t.Fatal("the stop of care.target stops each unit")
	}
	_ = h.Systemd.Start(ctx, "care.target")
	if state() != "active" {
		t.Fatal("the start of care.target starts each unit")
	}
	if n, _ := New(Dev{}).Podman.SecretNames(ctx); len(n) != 0 {
		t.Fatalf("a Dev without state has no secrets: %v", n)
	}
}

func TestTransientWrap(t *testing.T) {
	sandbox := Transient{HostMountNS: func() bool { return false }}
	host := Transient{HostMountNS: func() bool { return true }}
	env := []string{"DJANGO_SUPERUSER_PASSWORD=x"}
	cases := []struct {
		name string
		t    Transient
		in   Cmd
		want string
	}{
		{"pull", sandbox, Cmd{Name: "podman", Args: []string{"pull", "redis:8"}},
			"systemd-run --quiet --wait --pipe --collect --description=care-pi image pull -- podman pull redis:8"},
		// The sandbox of care-pi.service: podman exec runs in the mount namespace of PID 1 (wave 4c on lumine).
		{"exec in the sandbox", sandbox, Cmd{Name: "podman", Args: []string{"exec", "--user", "postgres", "care-db", "pg_isready"}, Env: env},
			"nsenter --target 1 --mount -- podman exec --user postgres care-db pg_isready"},
		{"exec in the host namespace", host, Cmd{Name: "podman", Args: []string{"exec", "care-caddy", "caddy", "reload"}},
			"podman exec care-caddy caddy reload"},
		{"other podman command", sandbox, Cmd{Name: "podman", Args: []string{"secret", "ls"}}, "podman secret ls"},
		{"not podman", sandbox, Cmd{Name: "systemctl", Args: []string{"start", "care.target"}}, "systemctl start care.target"},
	}
	for _, c := range cases {
		got := c.t.Wrap(c.in)
		if got.String() != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got.String(), c.want)
		}
		if !reflect.DeepEqual(got.Env, c.in.Env) {
			t.Errorf("%s: the environment changed: %v", c.name, got.Env)
		}
	}
	if !sameMountNS() {
		t.Log("the test process is not in the mount namespace of PID 1")
	}
}
