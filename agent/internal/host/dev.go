package host

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Dev answers the commands of care-pi with plausible output and changes nothing on the host.
// "care-pi serve --insecure-dev --fake-host" uses it, so that a console developer can run the agent
// on a laptop without podman and systemd. Each unit is active and each container is healthy.
// A Dev of NewDev also keeps the podman secrets that apply sets and the stop of care.target,
// so a second apply finds no change and a full cycle ends.
type Dev struct{ st *devState }

type devState struct {
	mu      sync.Mutex
	secrets map[string]bool
	stopped bool
}

// NewDev returns a Dev with its own state.
func NewDev() Dev { return Dev{st: &devState{secrets: map[string]bool{}}} }

// record keeps the effect of a command on the state. It reports whether the units are stopped.
func (d Dev) record(c Cmd) (stopped bool, secrets []string) {
	if d.st == nil {
		return false, nil
	}
	d.st.mu.Lock()
	defer d.st.mu.Unlock()
	a := c.Args
	switch {
	case c.Name == "systemctl" && len(a) > 1 && a[0] == "stop" && a[1] == "care.target":
		d.st.stopped = true
	case c.Name == "systemctl" && len(a) > 1 && (a[0] == "start" || a[0] == "restart") && strings.HasPrefix(a[1], "care"):
		d.st.stopped = false
	case c.Name == "podman" && len(a) > 3 && a[0] == "secret" && a[1] == "create":
		d.st.secrets[a[len(a)-2]] = true
	}
	for n := range d.st.secrets {
		secrets = append(secrets, n)
	}
	sort.Strings(secrets)
	return d.st.stopped, secrets
}

// Run implements Runner.
func (d Dev) Run(_ context.Context, c Cmd) ([]byte, error) {
	stopped, secretNames := d.record(c)
	a := c.Args
	switch c.Name {
	case "systemctl":
		if len(a) > 0 && a[0] == "show" {
			var units []string
			for i := 1; i < len(a); i++ {
				if a[i] == "-p" {
					i++
					continue
				}
				units = append(units, a[i])
			}
			var b strings.Builder
			for _, u := range units {
				if stopped && strings.HasPrefix(u, "care") {
					_, _ = fmt.Fprintf(&b, "Id=%s\nActiveState=inactive\nSubState=dead\nLoadState=loaded\nNRestarts=0\n\n", u)
					continue
				}
				_, _ = fmt.Fprintf(&b, "Id=%s\nActiveState=active\nSubState=running\nLoadState=loaded\nNRestarts=0\nMemoryCurrent=104857600\nMemoryPeak=157286400\nMemoryMax=536870912\n\n", u)
			}
			return []byte(b.String()), nil
		}
		return nil, nil
	case "podman":
		switch {
		case len(a) > 1 && a[0] == "secret" && a[1] == "ls":
			return []byte(strings.Join(secretNames, "\n") + "\n"), nil
		case len(a) > 0 && a[0] == "ps":
			return []byte("care-caddy\tUp 1 hour\ncare-db\tUp 1 hour (healthy)\ncare-redis\tUp 1 hour (healthy)\ncare-s3\tUp 1 hour (healthy)\ncare-beat\tUp 1 hour\ncare-worker\tUp 1 hour (healthy)\ncare-api\tUp 1 hour (healthy)\n"), nil
		case len(a) > 0 && a[0] == "version":
			return []byte("5.4.2\n"), nil
		}
		return nil, nil
	case "skopeo":
		ref := a[len(a)-1]
		return []byte(fmt.Sprintf("sha256:%x\n", sha256.Sum256([]byte(ref)))), nil
	case "timedatectl":
		return []byte("yes\n"), nil
	case "journalctl":
		for _, x := range a {
			if x == "json" {
				return []byte(devJournal(time.Now(), 1)), nil
			}
		}
		return []byte(time.Now().UTC().Format(time.RFC3339) + " care-pi dev host: no journal\n"), nil
	}
	return nil, nil
}

func devJournal(t time.Time, n int) string {
	return fmt.Sprintf(`{"__CURSOR":"s=de1;i=%x;b=de2;m=1;t=%x;x=1","__REALTIME_TIMESTAMP":"%d","PRIORITY":"6","MESSAGE":"dev host line %d"}`+"\n",
		n, t.UnixMicro(), t.UnixMicro(), n)
}

// Stream implements Streamer. A follow command gets one line each 2 s.
func (d Dev) Stream(ctx context.Context, c Cmd, fn func([]byte) error) error {
	out, _ := d.Run(ctx, c)
	if err := fn([]byte(strings.TrimSpace(string(out)))); err != nil {
		return err
	}
	follow := false
	for _, a := range c.Args {
		follow = follow || a == "--follow"
	}
	if !follow {
		return nil
	}
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for n := 2; ; n++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-t.C:
			if err := fn([]byte(strings.TrimSpace(devJournal(now, n)))); err != nil {
				return err
			}
		}
	}
}

// DevGit returns the same sha for each ref of each repository.
type DevGit struct{}

// LsRemote implements Git.
func (DevGit) LsRemote(context.Context, string) (map[string]string, error) {
	sha := strings.Repeat("d", 40)
	return map[string]string{"HEAD": sha, "refs/heads/develop": sha, "refs/heads/main": sha, "refs/heads/master": sha}, nil
}
