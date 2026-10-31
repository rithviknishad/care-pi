// Package units formats sizes for the texts of the agent.
package units

import (
	"math"
	"strconv"
	"strings"
)

var byteUnits = []string{"B", "KiB", "MiB", "GiB", "TiB"}

// Bytes formats a size with binary units and 1 decimal, without ".0": 128 GB gives "119.2 GiB".
// It is the rule of formatBytes of the console (console/src/lib/format.ts), so the hardware facts and
// the host checks of the setup show the same size.
func Bytes(n int64) string {
	if n < 1024 {
		return strconv.FormatInt(n, 10) + " B"
	}
	v := float64(n)
	u := 0
	for v >= 1024 && u < len(byteUnits)-1 {
		v /= 1024
		u++
	}
	r := math.Round(v*10) / 10
	if r >= 1024 && u < len(byteUnits)-1 {
		r = math.Round(v/1024*10) / 10
		u++
	}
	return strings.TrimSuffix(strconv.FormatFloat(r, 'f', 1, 64), ".0") + " " + byteUnits[u]
}
