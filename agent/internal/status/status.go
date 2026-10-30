// Package status collects the state of the stack for "care-pi status".
package status

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/lock"
	"github.com/rithviknishad/care-pi/agent/internal/netinfo"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/render"
)

// Unit is the state of one unit.
type Unit struct {
	Unit          string `json:"unit"`
	ActiveState   string `json:"active_state"`
	SubState      string `json:"sub_state"`
	Health        string `json:"health,omitempty"`
	MemoryCurrent uint64 `json:"memory_current_bytes,omitempty"`
	MemoryPeak    uint64 `json:"memory_peak_bytes,omitempty"`
	MemoryMax     uint64 `json:"memory_max_bytes,omitempty"`
	Restarts      int    `json:"restarts"`
}

// Image is one locked image.
type Image struct {
	Name string `json:"name"`
	Ref  string `json:"ref"`
	Pull string `json:"pull"`
}

// Report is the output of "care-pi status".
type Report struct {
	Hostnames []string `json:"hostnames"`
	// Addresses are the LAN IPv4 addresses of the interfaces now.
	Addresses []string `json:"addresses"`
	// Published are the addresses in the rendered care.pod. They differ from Addresses
	// after a DHCP change until the next apply.
	Published []string   `json:"published"`
	Units     []Unit     `json:"units"`
	Images    []Image    `json:"images,omitempty"`
	CareFESHA string     `json:"care_fe_sha,omitempty"`
	FEBuildID string     `json:"fe_build_id,omitempty"`
	Locked    string     `json:"locked_at,omitempty"`
	Degraded  bool       `json:"degraded"`
	Problems  []string   `json:"problems,omitempty"`
	Mode      []string   `json:"mode,omitempty"`
	Lock      *lock.Lock `json:"-"`
}

// Units lists the units that status shows.
func Units() []string {
	return append([]string{"care.target", "care-clock.service"}, render.Units...)
}

// Collect reads the state of the host.
func Collect(ctx context.Context, h *host.Host, p paths.Paths, hostnames []string, ips netinfo.Source) (*Report, error) {
	r := &Report{Hostnames: hostnames}
	if addrs, err := netinfo.LANIPv4(ips); err == nil {
		r.Addresses = addrs
	} else {
		r.Problems = append(r.Problems, "cannot read the network interfaces: "+err.Error())
	}
	r.Published = publishedIPs(p)

	units := Units()
	props, err := h.Systemd.Show(ctx, units, "Id", "ActiveState", "SubState", "MemoryCurrent", "MemoryPeak", "MemoryMax", "NRestarts")
	if err != nil {
		return nil, fmt.Errorf("cannot read the unit states: %w", err)
	}
	health, err := h.Podman.Health(ctx, "care")
	if err != nil {
		health = map[string]string{}
		r.Problems = append(r.Problems, "cannot read the container health: "+err.Error())
	}
	for i, u := range units {
		pr := props[i]
		st := Unit{
			Unit:          u,
			ActiveState:   pr["ActiveState"],
			SubState:      pr["SubState"],
			MemoryCurrent: parseBytes(pr["MemoryCurrent"]),
			MemoryPeak:    parseBytes(pr["MemoryPeak"]),
			MemoryMax:     parseBytes(pr["MemoryMax"]),
		}
		st.Restarts, _ = strconv.Atoi(pr["NRestarts"])
		st.Health = health[strings.TrimSuffix(u, ".service")]
		if st.ActiveState != "active" {
			r.Degraded = true
			r.Problems = append(r.Problems, fmt.Sprintf("%s is %s", u, orUnknown(st.ActiveState)))
		}
		if st.Health == "unhealthy" {
			r.Degraded = true
			r.Problems = append(r.Problems, u+" is unhealthy")
		}
		r.Units = append(r.Units, st)
	}
	if lk, err := lock.Read(p.Abs(paths.LockFile)); err == nil && lk != nil {
		r.Lock = lk
		r.Locked = lk.GeneratedAt
		for _, n := range []string{"care", "postgres", "caddy", "redis", "s3", "node"} {
			if img, ok := lk.Images[n]; ok {
				r.Images = append(r.Images, Image{Name: n, Ref: img.Ref, Pull: img.Pull()})
			}
		}
		if g, ok := lk.Git["care_fe"]; ok {
			r.CareFESHA = g.SHA
		}
		r.FEBuildID = lk.FE.BuildID
	} else if err != nil {
		r.Problems = append(r.Problems, err.Error())
	}
	for _, m := range []string{paths.ModeClockWait, paths.ModeMaintenance} {
		if _, err := os.Stat(p.Abs(paths.ModeDir + "/" + m)); err == nil {
			r.Mode = append(r.Mode, m)
		}
	}
	return r, nil
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func parseBytes(s string) uint64 {
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil || v == ^uint64(0) {
		return 0
	}
	return v
}

// publishedIPs reads the PublishPort=<ip>:443:443 lines of the rendered care.pod.
func publishedIPs(p paths.Paths) []string {
	data, err := os.ReadFile(p.Abs(paths.QuadletDir + "/care.pod"))
	if err != nil {
		return nil
	}
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		v, ok := strings.CutPrefix(line, "PublishPort=")
		if !ok || !strings.HasSuffix(v, ":443:443") {
			continue
		}
		out = append(out, strings.TrimSuffix(v, ":443:443"))
	}
	return out
}

func mib(b uint64) string {
	if b == 0 {
		return "-"
	}
	return fmt.Sprintf("%dM", b/(1024*1024))
}

// WriteText prints the report for an operator.
func (r *Report) WriteText(w io.Writer) {
	state := "ok"
	if r.Degraded {
		state = "degraded"
	}
	_, _ = fmt.Fprintf(w, "state: %s\n", state)
	_, _ = fmt.Fprintf(w, "names: %s\n", strings.Join(r.Hostnames, " "))
	_, _ = fmt.Fprintf(w, "addresses now: %s\n", joinOr(r.Addresses, "(none)"))
	_, _ = fmt.Fprintf(w, "addresses published: %s\n", joinOr(r.Published, "(none)"))
	if !equalStrings(r.Addresses, r.Published) {
		_, _ = fmt.Fprintln(w, "note: the addresses changed; run \"care-pi apply\" to publish the new addresses")
	}
	if len(r.Mode) > 0 {
		_, _ = fmt.Fprintf(w, "status page: %s\n", strings.Join(r.Mode, " "))
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "%-20s %-10s %-12s %-10s %8s %8s %8s %s\n", "UNIT", "ACTIVE", "SUB", "HEALTH", "MEM", "PEAK", "MAX", "RESTARTS")
	for _, u := range r.Units {
		_, _ = fmt.Fprintf(w, "%-20s %-10s %-12s %-10s %8s %8s %8s %d\n",
			u.Unit, orUnknown(u.ActiveState), u.SubState, orDash(u.Health), mib(u.MemoryCurrent), mib(u.MemoryPeak), mib(u.MemoryMax), u.Restarts)
	}
	if len(r.Images) > 0 {
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintf(w, "lock (%s):\n", orDash(r.Locked))
		for _, i := range r.Images {
			_, _ = fmt.Fprintf(w, "  %-9s %s\n", i.Name, i.Pull)
		}
		if r.CareFESHA != "" {
			_, _ = fmt.Fprintf(w, "  %-9s %s (build %s)\n", "care_fe", r.CareFESHA, orDash(r.FEBuildID))
		}
	}
	if len(r.Problems) > 0 {
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, "problems:")
		for _, p := range r.Problems {
			_, _ = fmt.Fprintln(w, "  -", p)
		}
	}
}

// WriteJSON prints the report as JSON.
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

func joinOr(s []string, empty string) string {
	if len(s) == 0 {
		return empty
	}
	return strings.Join(s, " ")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func equalStrings(a, b []string) bool {
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
