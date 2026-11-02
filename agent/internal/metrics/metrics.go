// Package metrics writes the Prometheus text exposition format (version 0.0.4) without a dependency: the metrics of
// the agent for GET /api/v1/metrics and for the text file of node_exporter (Phase 7, docs/telemetry.md).
package metrics

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ContentType is the media type of the text format.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

var nameRE = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)

type sample struct {
	labels []string
	value  float64
}

type family struct {
	name, help, typ string
	samples         []sample
}

// Set is an ordered set of metric families.
type Set struct {
	fams  []*family
	index map[string]*family
}

// New returns an empty Set.
func New() *Set { return &Set{index: map[string]*family{}} }

func (s *Set) add(typ, name, help string, v float64, labels []string) {
	if !nameRE.MatchString(name) || len(labels)%2 != 0 {
		panic("metrics: bad metric " + name)
	}
	f, ok := s.index[name]
	if !ok {
		f = &family{name: name, help: help, typ: typ}
		s.index[name] = f
		s.fams = append(s.fams, f)
	}
	f.samples = append(f.samples, sample{labels: labels, value: v})
}

// Gauge adds a sample of a gauge. labels are name, value pairs.
func (s *Set) Gauge(name, help string, v float64, labels ...string) {
	s.add("gauge", name, help, v, labels)
}

// Counter adds a sample of a counter.
func (s *Set) Counter(name, help string, v float64, labels ...string) {
	s.add("counter", name, help, v, labels)
}

// Bool adds a gauge of 1 or 0.
func (s *Set) Bool(name, help string, b bool, labels ...string) {
	v := 0.0
	if b {
		v = 1
	}
	s.Gauge(name, help, v, labels...)
}

var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
var helpEscaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`)

func formatValue(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	}
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// WriteTo writes the exposition.
func (s *Set) WriteTo(w io.Writer) (int64, error) {
	var b bytes.Buffer
	for _, f := range s.fams {
		_, _ = fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", f.name, helpEscaper.Replace(f.help), f.name, f.typ)
		for _, sm := range f.samples {
			b.WriteString(f.name)
			if len(sm.labels) > 0 {
				b.WriteByte('{')
				for i := 0; i < len(sm.labels); i += 2 {
					if i > 0 {
						b.WriteByte(',')
					}
					_, _ = fmt.Fprintf(&b, `%s="%s"`, sm.labels[i], labelEscaper.Replace(sm.labels[i+1]))
				}
				b.WriteByte('}')
			}
			b.WriteByte(' ')
			b.WriteString(formatValue(sm.value))
			b.WriteByte('\n')
		}
	}
	n, err := w.Write(b.Bytes())
	return int64(n), err
}

// Bytes returns the exposition.
func (s *Set) Bytes() []byte {
	var b bytes.Buffer
	_, _ = s.WriteTo(&b)
	return b.Bytes()
}

// WriteFile writes the exposition atomically (the text file collector of node_exporter reads *.prom files and must
// never see a half file).
func (s *Set) WriteFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, s.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
