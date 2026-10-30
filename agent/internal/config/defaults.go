package config

// Default values. docs/config.md lists the same values.
const (
	DefaultHostname          = "care"
	DefaultDisplayName       = "CARE"
	DefaultSolution          = "care-clinic"
	DefaultTrack             = "stable"
	DefaultPreset            = "small-clinic"
	DefaultClockPolicy       = "ntp"
	DefaultWALArchiveTimeout = 5
	DefaultFEProvider        = "local"
	DefaultUploadMaxBodyMB   = 200
)

// Default returns a config with every default set.
// Load decodes the YAML document on top of this value.
func Default() *Config {
	return &Config{
		Version: 1,
		Identity: Identity{
			DisplayName: DefaultDisplayName,
			Hostname:    DefaultHostname,
		},
		Solution: Solution{
			Name:  DefaultSolution,
			Track: DefaultTrack,
		},
		Size: Size{Preset: DefaultPreset},
		TLS: TLS{
			LocalCA: TLSLocalCA{Enabled: true},
		},
		Clock: Clock{Policy: DefaultClockPolicy},
		Backups: Backups{
			WALArchiveTimeoutMin: DefaultWALArchiveTimeout,
			Schedule: BackupSchedule{
				DBFull: "Sun 01:00",
				DBIncr: "daily 02:00",
				Files:  "daily 02:30",
				Verify: "Sat 03:00",
			},
			Retention: BackupRetention{
				DBFull:          2,
				ArchiveDays:     14,
				FilesKeepDaily:  14,
				FilesKeepWeekly: 8,
			},
		},
		Updates: Updates{
			Auto: false,
			Window: UpdateWindow{
				Days:  []string{"Sun"},
				Start: "02:00",
				End:   "05:00",
			},
			OSSecurity: true,
		},
		FE:      FE{Provider: DefaultFEProvider},
		Uploads: Uploads{MaxBodyMB: DefaultUploadMaxBodyMB},
	}
}
