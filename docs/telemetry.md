# Telemetry (opt-in)

Phase 7 (O12, S29, `01` section 13). care-pi sends metrics only when the operator turns telemetry on, and only to the
URL of the operator. There is no hosted side: the receiver is any Prometheus remote-write endpoint (VictoriaMetrics,
Prometheus with `--web.enable-remote-write-receiver`, Grafana Cloud, Mimir). With `telemetry.enabled: false` (the
default), the device runs no telemetry container, writes no metrics file, and sends nothing.

## 1. What runs

| Unit | Image | Role | Cap |
|---|---|---|---|
| `care-node-exporter.service` | `quay.io/prometheus/node-exporter` (40 MB, arm64) | The metrics of the host from `/proc` and `/sys` (CPU, memory, load, file systems, disks, network, pressure, temperature), and the metrics of the agent from a text file. It listens on `127.0.0.1:9100` of the host only. | `MemoryMax=64M` |
| `care-telemetry.service` | `docker.io/victoriametrics/vmagent` (44 MB, arm64) | It scrapes node_exporter each `interval_s` and sends the samples with remote write. It keeps a queue of at most 64 MB on disk (`/var/lib/care-pi/telemetry/vmagent`) when the receiver does not answer. It listens on `127.0.0.1:8429` only. | `MemoryMax=96M` |

Both have `Network=host`, a read-only root file system, and `PartOf=care.target`. The `.image` quadlets
(`care-telemetry.image`, `care-node-exporter.image`) pull the images in their own units before the containers start.
The lock pins the images by digest, as the other images.

Grafana Alloy does the same in one container, but its arm64 image is 641 MB (S56).

**The metrics of the agent.** While telemetry is on, `care-pi serve` writes
`/var/lib/care-pi/telemetry/textfile/care_pi.prom` each 30 s (atomically); node_exporter reads it with its text file
collector. The same metrics are at `GET /api/v1/metrics` (session) and `care-pi metrics`:

| Metric | Labels | Meaning |
|---|---|---|
| `care_pi_info` | `version`, `solution`, `track`, `setup_state` | Always 1. |
| `care_pi_state` | `state` | 1 for the state of the device (`ok`, `degraded`, `starting`, `maintenance`, `clock-wait`, `setup`). |
| `care_pi_unit_active`, `care_pi_unit_memory_bytes`, `care_pi_unit_memory_peak_bytes`, `care_pi_unit_memory_max_bytes`, `care_pi_unit_restarts_total` | `unit` | The units of Care (the cgroups of `care.target`). |
| `care_pi_host_memory_*`, `care_pi_host_swap_*`, `care_pi_host_load`, `care_pi_host_uptime_seconds`, `care_pi_host_cpus` | `window` (load) | The host. |
| `care_pi_disk_total_bytes`, `care_pi_disk_free_bytes` | `mount` | The file systems of care-pi. |
| `care_pi_clock_synchronized`, `care_pi_clock_trusted` | | The clock (S52). |
| `care_pi_backup_local_last_{full,incr,wal}_timestamp_seconds`, `care_pi_backup_last_snapshot_timestamp_seconds`, `care_pi_backup_failing`, `care_pi_backup_alert_after_seconds` | `destination`, `type` | The backups. |
| `care_pi_certificate_expiry_timestamp_seconds` | `kind`, `name` | The local root and the certificate of the public name. |
| `care_pi_update_available`, `care_pi_os_reboot_required` | | Updates. |
| `care_pi_firewall_policy` | `policy` | The loaded firewall policy. |

**Labels.** vmagent sets `instance=<identity.hostname>` on each sample and the external label
`care_pi_version=<agent version>`.

## 2. Turn it on

```sh
sudo care-pi config set telemetry.enabled=true \
  telemetry.remote_write_url=https://metrics.example.org/api/v1/write \
  telemetry.username=sunrise-clinic
printf '%s\n' 'the password' | sudo care-pi telemetry set-password   # runs the apply
```

The console has no form for telemetry yet: `PATCH /config` sets the keys; the password is only on the CLI
(`/etc/care-pi/secrets/telemetry_password`, 0600, the podman secret `care-pi-telemetry-password`, mounted for vmagent
as a file). Use a receiver user that can only write.

| Key | Default | Meaning |
|---|---|---|
| `telemetry.enabled` | `false` | Run the two units. |
| `telemetry.remote_write_url` | none (required when enabled) | `http(s)://…`. Use https: the samples have the host name and the units. |
| `telemetry.username` | none | The basic auth user. |
| `telemetry.interval_s` | `60` | The scrape interval (15 to 3600). |

The apply action `ensure-telemetry` starts, restarts, or stops only these units; Care keeps running. Off:
`sudo care-pi config set telemetry.enabled=false --apply` stops them, deletes their files, and the agent deletes its text
file. The queue directory stays (delete `/var/lib/care-pi/telemetry` by hand).

## 3. What the receiver gets

Only metrics: no log, no patient data, no file name, no IP address of a client. The host name, the solution, the agent
version, the unit names, the backup destination ids, and the public host name are labels.

## 4. Test

`quadlets/scripts/test-telemetry.sh` runs node_exporter and vmagent in Docker with the files of the golden case
`hardened`, and VictoriaMetrics with basic auth as the receiver. It checks `care_pi_unit_active` (the text file) and
`node_load1` at the receiver with `instance=care` and `care_pi_version`, and a 401 with a wrong password. Measured on a
Mac (arm64): node_exporter 32 MiB, vmagent 35 MiB. `quadlets/scripts/quadlet-dryrun.sh hardened` checks the quadlets
(12 units) with podman 5.4.2.
