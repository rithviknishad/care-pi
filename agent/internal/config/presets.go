package config

import "fmt"

// Sizing is the CPU and memory shape that a preset selects.
type Sizing struct {
	Preset            string
	GunicornWorkers   int
	CeleryConcurrency int
	PGSharedBuffersMB int
}

// Presets maps each named preset to its sizing. Source: plans/01-architecture.md section 8 (SP3).
var Presets = map[string]Sizing{
	"solo":         {Preset: "solo", GunicornWorkers: 1, CeleryConcurrency: 1, PGSharedBuffersMB: 128},
	"small-clinic": {Preset: "small-clinic", GunicornWorkers: 2, CeleryConcurrency: 1, PGSharedBuffersMB: 128},
	"busy-counter": {Preset: "busy-counter", GunicornWorkers: 3, CeleryConcurrency: 2, PGSharedBuffersMB: 256},
}

// PresetNames lists the presets in the order of size, including "custom".
var PresetNames = []string{"solo", "small-clinic", "busy-counter", "custom"}

// Sizing returns the sizing for the configured preset.
func (c *Config) Sizing() Sizing {
	if c.Size.Preset == "custom" {
		return Sizing{
			Preset:            "custom",
			GunicornWorkers:   c.Size.Custom.GunicornWorkers,
			CeleryConcurrency: c.Size.Custom.CeleryConcurrency,
			PGSharedBuffersMB: c.Size.Custom.PGSharedBuffersMB,
		}
	}
	return Presets[c.Size.Preset]
}

// Mem is one MemoryMax / MemorySwapMax pair in MiB.
type Mem struct {
	MaxMB  int
	SwapMB int
}

// Max returns the systemd value for MemoryMax.
func (m Mem) Max() string { return fmt.Sprintf("%dM", m.MaxMB) }

// Swap returns the systemd value for MemorySwapMax.
func (m Mem) Swap() string {
	if m.SwapMB == 0 {
		return "0"
	}
	return fmt.Sprintf("%dM", m.SwapMB)
}

// Fixed memory caps (SP3, SP5). They are the same for each preset.
// care-beat runs migrate at each start: 382 MiB of 384M plus 85 MiB swap on an empty
// database in the Phase 1 exit test, so its cap is 512M.
var (
	MemRedis = Mem{MaxMB: 96}
	MemS3    = Mem{MaxMB: 256}
	MemCaddy = Mem{MaxMB: 128}
	MemBeat  = Mem{MaxMB: 512, SwapMB: 128}
)

// FE builder shape (SP4). The builder is shaped, not capped.
const (
	FEBuildMemoryHigh = "768M"
	FEBuildMemoryMax  = "1700M"
	FEBuildMemorySwap = "1500M"
)

func roundUp(v, step int) int {
	return (v + step - 1) / step * step
}

// MemAPI returns the caps for gunicorn with the given worker count.
// The measured values are 320M, 512M and 768M for 1, 2 and 3 workers.
// Above 3 workers the cap is 64M + 200M per worker, rounded up to 128M.
func MemAPI(workers int) Mem {
	switch {
	case workers <= 1:
		return Mem{MaxMB: 320, SwapMB: 64}
	case workers == 2:
		return Mem{MaxMB: 512, SwapMB: 128}
	case workers == 3:
		return Mem{MaxMB: 768, SwapMB: 128}
	default:
		return Mem{MaxMB: roundUp(64+200*workers, 128), SwapMB: 128}
	}
}

// MemWorker returns the caps for celery with the given concurrency.
// The measured values are 384M and 512M for concurrency 1 and 2.
// Above 2 the cap grows by 192M per child.
func MemWorker(concurrency int) Mem {
	switch {
	case concurrency <= 1:
		return Mem{MaxMB: 384, SwapMB: 128}
	case concurrency == 2:
		return Mem{MaxMB: 512, SwapMB: 128}
	default:
		return Mem{MaxMB: 512 + 192*(concurrency-2), SwapMB: 128}
	}
}

// MemDB returns the caps for Postgres with the given shared_buffers.
// The measured values are 384M up to 192 MB buffers and 512M at 256 MB buffers.
// Above 256 MB the cap is shared_buffers + 256M, rounded up to 128M.
func MemDB(sharedBuffersMB int) Mem {
	switch {
	case sharedBuffersMB <= 192:
		return Mem{MaxMB: 384}
	case sharedBuffersMB <= 256:
		return Mem{MaxMB: 512}
	default:
		return Mem{MaxMB: roundUp(sharedBuffersMB+256, 128)}
	}
}
