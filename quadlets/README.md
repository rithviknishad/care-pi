# quadlets

This directory holds the templates that the agent renders.
`embed.go` embeds `templates/` with `go:embed`. The renderer is `agent/internal/render`.
The templates use Go `text/template`. The data is `render.Model`.

| Template | Output (host path) |
|---|---|
| `care.pod.tmpl` | `/etc/containers/systemd/care.pod`. It publishes 80, 443, and 8443 (the console, S48) on each LAN address (ethernet and Wi-Fi). It has `Wants=` and `After=network-online.target`, because it publishes on the LAN addresses. Phase 6, Tailscale mode: it also publishes on the address of `tailscale0`, has `After=tailscaled.service`, and its `[Service]` has `ExecStartPre=-care-pi network wait-address --address <IP> --timeout 30s` (before the `pod create` of quadlet). |
| `care-caddy.container.tmpl` | `/etc/containers/systemd/care-caddy.container`. It mounts the directory `/run/care-pi` (the agent socket `api.sock`; `ExecStartPre` creates the directory) and has `Wants=` and `After=care-pi.service`. Caddy starts without the agent: the agent routes then give `503`. Phase 6: it mounts `/var/lib/care-pi/ca` read-only at `/etc/care-pi-ca` (the root of the agent), the podman secret `care-pi-acme-dns` as a file (`type=mount`, public mode only), and `/run/tailscale` (Tailscale mode only). |
| `care-db.container.tmpl` | `/etc/containers/systemd/care-db.container` |
| `care-redis.container.tmpl` | `/etc/containers/systemd/care-redis.container` |
| `care-s3.container.tmpl` | `/etc/containers/systemd/care-s3.container` |
| `care-beat.container.tmpl` | `/etc/containers/systemd/care-beat.container` |
| `care-worker.container.tmpl` | `/etc/containers/systemd/care-worker.container` |
| `care-api.container.tmpl` | `/etc/containers/systemd/care-api.container` |
| `care-fe-build.container.tmpl` | `/etc/containers/systemd/care-fe-build.container` (only `fe.provider: local`) |
| `care.target.tmpl` | `/etc/systemd/system/care.target` (no `[Install]` section) |
| `care-clock.service.tmpl` | `/etc/systemd/system/care-clock.service` |
| `care-start.service.tmpl` | `/etc/systemd/system/care-start.service`. At boot, it runs `systemctl start --no-block care.target`. The agent enables this unit, not `care.target`, so the boot does not wait for the clock gate or the health checks. It does not wait for `network-online.target` either (the pod does). |
| `Caddyfile.tmpl` | `/etc/care-pi/rendered/caddy/Caddyfile`. Port 443: Care, `/_care-pi/*` to the agent socket, `/console*` → `308` to port 8443. Port 8443: each request to the agent socket (`docs/console-server.md`, section 3). Phase 6: the `pki` block (the root of the agent), the snippets `(care)`, `(console)`, and `(tls_public)`, one site block for each mode on 443 and on 8443, HSTS only on the public and Tailscale sites, and the port 80 redirects (`docs/apply.md`, "Caddy"). |
| `status-setup.html.tmpl`, `status-maintenance.html.tmpl`, `status-clock.html.tmpl` | `/etc/care-pi/rendered/caddy/status/*.html`. The setup page links to the console on port 8443 and offers the root CA. |
| `backend.env.tmpl` | `/etc/care-pi/rendered/care/backend.env` |
| `pgbackrest.conf.tmpl` | `/etc/care-pi/rendered/db/pgbackrest.conf` |
| `frontend.env.tmpl`, `fe-build.sh.tmpl` | `/etc/care-pi/rendered/fe/` (only `fe.provider: local`) |
| `care-backup.target.tmpl` | `/etc/systemd/system/care-backup.target` (Phase 3). `WantedBy=multi-user.target`. It wants each backup timer and each automount unit of a backup disk (no timer with `backups.paused`). |
| `care-backup.service.tmpl` | `/etc/systemd/system/care-backup-{db-full,db-incr,verify,drill}.service` and the templates `care-backup-{db,files}@.service` (Phase 3). `Type=oneshot`, `care-pi backup … --from-timer`, `MemoryMax=512M`, `Nice=10`, `IOSchedulingClass=idle`, `GOMAXPROCS=2`. |
| `care-backup.timer.tmpl` | `/etc/systemd/system/care-backup-*.timer` (Phase 3). `PartOf=care-backup.target`. A calendar timer has `Persistent=true`; a destination timer has `OnActiveSec=1min` and `OnUnitActiveSec=<interval_min>min`. |
| `care-backup-disk.mount.tmpl`, `care-backup-disk.automount.tmpl` | `/etc/systemd/system/var-lib-care\x2dpi-disks-<label>.{mount,automount}` (Phase 3). A backup disk with `nofail`; the automount unit unmounts it after 10 idle minutes. |
| `backup-ssh_config.tmpl`, `backup-known_hosts.tmpl` | `/etc/care-pi/rendered/backup/{ssh_config,known_hosts}` (Phase 3). One `Host care-dest-<id>` for each SFTP destination with the pinned host key, `StrictHostKeyChecking yes`, and `BatchMode yes`. |
| `additional-plugs.json.tmpl` (Phase 4) | `/etc/care-pi/rendered/care/additional-plugs.json`: `ADDITIONAL_PLUGS` with each secret replaced by `"<secret>"`. The podman secret `care-additional-plugs` has the real value. |
| `care-update-check.timer.tmpl`, `care-update-check.service.tmpl` (Phase 4) | `/etc/systemd/system/care-update-check.{timer,service}`: each day at a random time, `care-pi update check --scheduled`. |
| `care-update-apply.timer.tmpl`, `care-update-apply.service.tmpl` (Phase 4) | `/etc/systemd/system/care-update-apply.{timer,service}`: one `OnCalendar=` for each day of the window, `Persistent=true`, `care-pi update apply --scheduled`. |
| `care-build.build.tmpl` (Phase 4) | `/etc/containers/systemd/care-build-<solution>.build`: the local backend build (`docs/solutions.md` section 13.1). Quadlet of podman 5.4 has no `BuildArg=`, so the template uses `PodmanArgs=--build-arg=BASE=…`. The service has `MemoryHigh=640M` and `MemoryMax=768M`. |
| `care-plug-build.container.tmpl`, `plug-build.sh.tmpl` (Phase 4, experimental) | `/etc/containers/systemd/care-plug-build.container` and `/etc/care-pi/rendered/plugs/plug-build.sh`: the local FE plug build. |
| `care-dns-update.timer.tmpl`, `care-dns-update.service.tmpl` (Phase 6) | `/etc/systemd/system/care-dns-update.{timer,service}` (public mode only): `care-pi tls public update-address --scheduled` 2 minutes after the boot, then each 5 minutes. |
| `firewall-tailscale.conf.tmpl` (Phase 6) | `/etc/systemd/system/care-pi-firewall.service.d/50-care-pi-tailscale.conf` (Tailscale mode only): `ExecStartPost=` and `ExecReload=` add `udp dport 41641 accept` to the table `inet care-pi`. |
| `care-secrets.tmpl`, `status-style.tmpl` | Shared blocks (`{{define}}`). No output. |

Setup mode (`apply --phase bootstrap`, `docs/apply.md`) renders only `care.pod`, `care-caddy.container`,
`care.target`, `care-clock.service`, `care-start.service`, the Caddyfile, and the status pages
(`render.Model.Bootstrap`). It renders no update timers. The golden tree is `small-clinic-bootstrap`.
`care-pi.service` is not a template: the installer writes it (`installer/install.sh`, `care_pi_unit`).

Rules for the templates:

- The first line of each output is the header comment
  "Generated by care-pi from /etc/care-pi/config.yaml. Do not edit."
- Each `.container` has `RunInit=true`, `Pod=care.pod`, `PartOf=care.target`, `Restart=always`,
  `WantedBy=care.target`, `MemoryMax=`, and `MemorySwapMax=`. The FE builder is the exception
  for `Pod=`, `Restart=`, and `WantedBy=`.
- Mount directories from `/etc/care-pi/rendered/<unit>/`, never single files.
- Use only quadlet keys that podman 5.4 knows.
- A container that needs more than 10 s to stop sets `StopTimeout=` in `[Container]`, and a longer
  `TimeoutStopSec=` in `[Service]`. The `ExecStop` of quadlet (`podman rm -f`) uses `StopTimeout`,
  then it sends SIGKILL. `care-db` has `StopTimeout=110` and `TimeoutStopSec=120`.
- A container whose process exits with 143 (128 + SIGTERM) at a stop sets `SuccessExitStatus=143` in `[Service]`:
  `care-api` (gunicorn), `care-worker`, and `care-beat` (celery). Else systemd records a failure at each stop.
- Never put a secret into a template. Use `Secret=` with a podman secret name.

- The maintenance page (`status-maintenance.html.tmpl`) reads `GET /_care-pi/status` each 5 s and shows the sentence of
  the step of the update (`render.MaintenanceSteps`, the same sentences as the console: `docs/console-ui.md` section 9). It opens Care again when the site is `care`. It works without JavaScript too (a slow meta refresh).

The backup units are plain systemd units, not quadlets. `docs/backups.md`, section 4, describes them.
`systemd` does not accept the calendar text `daily HH:MM`; the renderer writes `*-*-* HH:MM` for it.

After a change, update the goldens: `go test ./agent/internal/render -update`.
Phase 4 files: `custom-build` holds the `.build` unit and the FE plug builder. Check them with quadlet of podman 5.4:
`/usr/libexec/podman/quadlet -dryrun` (the `.build` unit becomes `care-build-<solution>-build.service`).
Phase 6 files: `public` and `public-tailscale`. Quadlet of podman 5.4.2 accepts the `[Service]` section of `care.pod`
(the `ExecStartPre` of the wait comes before the `pod create` of quadlet), and `Secret=…,type=mount,mode=0400`.
`caddy validate` of the public and Tailscale sites needs the image of `images/caddy` (the modules `acmedns` and `tailscale`).
`agent/README.md` describes how to check the output with the quadlet generator and Caddy.
