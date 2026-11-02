# Troubleshooting

Start with the health checks. They read the host and change nothing.

- Console: **Support > Health checks**, then **Run again** after a fix.
- SSH: `sudo care-pi doctor` (a table with a fix hint for each problem), or `sudo care-pi doctor --json`.
  The exit code is 1 when a check failed.
- API: `GET /api/v1/doctor` (a console session).

The failed checks come first. `docs/console-server.md`, section 16, lists each check and its limits.

## Common problems

| Symptom | Check | Cause and fix |
|---|---|---|
| `https://care.local/` does not open, but `https://<IP address>/` opens | `mdns` | Another device on the network has the name `care`, so avahi announces `care-2.local`. Use the announced name or the IP address, or give the device another name: set the OS host name and `identity.hostname` to the same value (`docs/config.md`), then run `sudo care-pi apply`. |
| Port 443 does not answer, or answers with another certificate | `ports` | Another program listens on 443 or 8443 of the LAN address or of all addresses, for example `tailscale serve`. Run `sudo ss -ltnp 'sport = :443'` and stop that program. Care publishes its ports on explicit addresses. |
| The browser warns about the certificate on each device | `cert-local` | The root certificate of the device is not installed on the client, or the local CA was rotated. Open **Certificate** and install it again (`docs/connectivity.md`, section 2). |
| The public name stops working | `cert-public` | The public certificate expires in less than 14 days and the renewal fails. Open **Connectivity** and read the last renewal error. |
| A banner says that a backup is late | `backup-<id>` | The last snapshot of the destination is older than `backups.alert_after_hours`. Open **Backups**, run **Back up now**, and read the log of the destination (a full USB disk, a changed SFTP host key, an S3 key without rights). |
| Care is slow, or containers restart | `memory`, `zram` | Low available memory, or no zram swap. Check `sudo care-pi doctor` and `systemctl status 'care-*'`. A Pi with 2 GB needs zram swap of 1x RAM (the installer sets it). |
| The clock is wrong after a power cut | `clock` | The Pi has no real-time clock battery and no NTP server is reachable. Care takes the time of the browser at the claim. Check the network, then `timedatectl`. |
| A unit of Care failed | `units` | Run `sudo care-pi logs <unit>` or `journalctl -u <unit>`. `sudo care-pi apply` starts the units again. |
| The memory limits do nothing | `host-*` | The memory cgroup controller is off (`cgroup_disable=memory` in `/boot/firmware/cmdline.txt`). The installer fixes it; reboot after the install. |
| The console asks to confirm the firewall | `firewall` | The policy `drop` waits for its confirmation. Open a new SSH session first, then confirm on the **Security** page or with `sudo care-pi firewall confirm`. Without it, the policy goes back to `accept` after 90 s (`docs/console-server.md`, section 13). |

## Logs

| What | Command |
|---|---|
| The agent | `sudo care-pi logs agent` or `journalctl -u care-pi.service` |
| A container | `sudo care-pi logs api` (also `worker`, `beat`, `db`, `redis`, `caddy`, `s3`) |
| The privileged helper | `journalctl -u care-pi-helper` (each request and each refusal) |
| A job | the job page of the console, or `sudo care-pi jobs show JOB_ID` |

## The support bundle

When you ask for help, attach a support bundle: **Support > Make a support bundle**, or
`sudo care-pi support-bundle`. The bundle has the health checks (`doctor.json`), the rendered files, the firewall
ruleset, the listening sockets, `podman ps`, the Tailscale status, the memory peaks of the units, and the recent logs.
The agent removes the secrets: each value of a secret file is `<redacted>`, the environment files have no secret
values, and the Tailscale status has no keys and no peers. Read the bundle before you send it.
