# installer

This directory holds the host installer for Care.

| File | Purpose |
|---|---|
| `install.sh` | Prepare a Raspberry Pi OS or Debian-family host. Install the `care-pi` agent and `care-pi.service`. Run `care-pi bootstrap` (setup mode by default). |
| `uninstall.sh` | Remove Care from a host. This is a development tool. |

Both files are POSIX `sh`. They use no bash features.
`install.sh` is one file on purpose, so that `curl | sh` works.
All shared functions are at the top of the file.
CI runs `shellcheck` on each `*.sh` file in this directory.

The operator guide is `docs/install.md`.
The design is in `plans/05-image-and-installer.md` section 6 and `plans/06-roadmap.md` Phase 1.
The CLI and path contract is the Phase 1 contract: the agent implements the commands that the script calls.

## Run

```sh
curl -fsSL https://raw.githubusercontent.com/rithviknishad/care-pi/main/installer/install.sh | sudo sh -s -- --yes
```

`sh -s --` reads the script from the pipe and passes all later words to the script as flags.
To use environment variables, put them after `sudo`:

```sh
curl -fsSL <URL> | sudo CARE_PI_PRESET=solo sh -s -- --yes
```

The script must run as root.
From a file, it starts itself again with `sudo`.
From a pipe, it cannot do this. It prints the correct command and stops.

## Flags and environment variables

| Flag | Environment variable | Default | Meaning |
|---|---|---|---|
| `--version TAG` | `CARE_PI_VERSION` | `latest` | Agent release tag. |
| `--hostname NAME` | `CARE_PI_HOSTNAME` | `care` | Host name. The mDNS name is `NAME.local`. |
| `--preset NAME` | `CARE_PI_PRESET` | `small-clinic` | Preset for `care-pi bootstrap` (`solo`, `small-clinic`, or `busy-counter`). |
| `--fe-provider NAME` | `CARE_PI_FE_PROVIDER` | `local` | FE provider for `care-pi bootstrap` (`local`, `artifact`, or `none`). `local` builds care_fe on the host. The first build takes about 3 minutes and needs internet. |
| `--unattended` | `CARE_PI_UNATTENDED` | off | Ask no questions. Imply `--yes`. Pass `--unattended` to `care-pi bootstrap`: a full apply now, no console setup (the Phase 1 behaviour). |
| `--yes` | `CARE_PI_YES` | off | Do not ask for confirmation. |
| `--no-firewall` | `CARE_PI_NO_FIREWALL` | off | Do not install the care-pi firewall (`care-pi-firewall.service` and its nftables table). |
| `--no-avahi` | `CARE_PI_NO_AVAHI` | off | Do not install or configure avahi. |
| `--no-apt-hold` | `CARE_PI_NO_APT_HOLD` | off | Do not hold the container packages. |
| `--skip-bootstrap` | `CARE_PI_SKIP_BOOTSTRAP` | off | Prepare the host. Do not run `care-pi bootstrap`. |
| `--binary PATH` | `CARE_PI_BINARY` | none | Install the agent from a local file. Do not download it. |
| `--dry-run` | `CARE_PI_DRY_RUN` | off | Print what the script would change. Change nothing. |
| `--help` | none | none | Print the help text. |
| none | `CARE_PI_DOWNLOAD_BASE` | GitHub releases | URL that holds `care-pi_linux_<arch>` and `SHA256SUMS`. |
| none | `CARE_PI_ALLOW_NO_SYSTEMD` | off | **Test only.** Continue on a host without systemd. |

A flag overrides its environment variable.
A switch variable accepts `1`, `true`, or `yes`.

Exit codes: `0` success, `1` failure, `2` wrong flags.

## What the script does

The script detects the host and then runs 15 steps.
Each step is a function named `step_<name>`.
Each step is idempotent. If nothing changes, it prints `already configured`.

1. Packages: `podman skopeo restic avahi-daemon nftables systemd-timesyncd unattended-upgrades ca-certificates curl uidmap`, and also `catatonit` and `iproute2`. Check podman before the install (the installed version, or else the apt candidate) and after it: 5.4 or newer (`MIN_PODMAN`), quadlet present. Debian 13 (trixie) ships 5.4. Ubuntu 24.04 (podman 4.9) is not supported in this version.
2. Memory cgroup: edit `/boot/firmware/cmdline.txt` on a Pi. Otherwise check the controller.
3. zram swap of at least 90 % of RAM: `rpi-swap` drop-in on Pi OS, `systemd-zram-generator` elsewhere.
4. Host name: change it only for `--hostname` or for the default `raspberrypi`.
5. avahi: `allow-interfaces`, `use-ipv4`, restart, print the announced name and the IPs.
6. Time: `systemd-timesyncd`, `timedatectl set-ntp true`.
7. Journal: `/etc/systemd/journald.conf.d/50-care-pi.conf` (`Storage=persistent`, `SystemMaxUse=200M`), `/var/log/journal` (root:systemd-journal 2755), `systemd-tmpfiles`, restart `systemd-journald`, `journalctl --flush` (S45).
8. Firewall: table `inet care-pi` in `/etc/care-pi/firewall.nft`, loaded by `care-pi-firewall.service` (S44). It accepts 80/tcp, 443/tcp, 8443/tcp (the console, S48), and 5353/udp. No default-drop policy before Phase 7 (S42). The step also removes the setup of an older install.sh (see below).
9. unattended-upgrades: `/etc/apt/apt.conf.d/52care-pi.conf`, timers.
10. `apt-mark hold` for `podman crun netavark aardvark-dns conmon skopeo`.
11. Directories under `/etc/care-pi` and `/var/lib/care-pi`.
12. Agent binary: copy `--binary`, or download and verify the checksum.
13. Agent service: `/etc/systemd/system/care-pi.service` (`care-pi serve`, `Type=notify`, `Restart=always`, `MemoryMax=128M`, `MemorySwapMax=32M`, `ProtectSystem=strict` with `ReadWritePaths`; function `care_pi_unit`). Enable it and start it. Restart it when the unit or the binary changed. The installer owns this unit: `care-pi apply` never writes it. `docs/console-server.md`, section 2, explains each line.
14. NetworkManager dispatcher hook `90-care-pi`. The hook coalesces the events of 5 s into one apply (15 s in the first 3 minutes after the boot: function `debounce_s`, from `/proc/uptime`). The apply waits up to 60 s for a LAN IPv4 address (`--address-wait`). It holds its flock until the apply ends, so its applies never overlap. An event during an apply gives one more apply after it.
15. `care-pi host check`, then `care-pi bootstrap --hostname NAME --preset NAME --fe-provider NAME --yes [--unattended]`. In setup mode (the default), bootstrap prints the claim code in a box, and the script prints the console URLs (`https://<name>:8443/`, `https://<ip>:8443/`). With `--unattended`, the script prints the Care URLs. The last message says `Care is ready` only when an FE build exists. Else it tells how to leave the setup page.

The script writes a log to `/var/log/care-pi-install.log`.
A reboot may be necessary after step 2 or step 3. Then the script prints `REBOOT REQUIRED` and does not run bootstrap.
`docs/install.md` describes each step, the reboot case, and the failure checklist.

## Develop and test

### Private repository

The care-pi repository is private today.
The default download URL returns 404.
Use a local binary or a local web server.

```sh
make build-linux                                    # bin/care-pi_linux_arm64, bin/care-pi_linux_amd64
sudo sh installer/install.sh --binary bin/care-pi_linux_arm64 --yes
```

Or serve the files over HTTP:

```sh
cd bin && sha256sum care-pi_linux_* > SHA256SUMS && python3 -m http.server 8000
sudo CARE_PI_DOWNLOAD_BASE=http://<your-computer>:8000 sh installer/install.sh --yes
```

### Static checks

```sh
shellcheck -S style installer/install.sh installer/uninstall.sh
sh -n installer/install.sh && bash -n installer/install.sh
```

### Container checks

A container has no systemd. A real run stops with `systemd not running`.
A dry run prints a warning and continues.

```sh
# Dry run: detection and the plan.
docker run --rm -v "$PWD/installer:/src:ro" debian:trixie \
  sh /src/install.sh --dry-run --skip-bootstrap --binary /bin/true

# Smoke test: real apt, file, and directory steps. Run it twice.
# The second run must print "already configured" for each step.
docker run --rm -v "$PWD/installer:/src:ro" -e CARE_PI_ALLOW_NO_SYSTEMD=1 debian:trixie \
  sh -c 'for i in 1 2; do sh /src/install.sh --yes --skip-bootstrap --binary /bin/true --no-firewall; done'
```

`CARE_PI_ALLOW_NO_SYSTEMD=1` is for tests only.
It makes the script skip every `systemctl` call.
Do not use it on a real host.

To test the firewall step, add `--cap-add NET_ADMIN` to the container, install `nftables` first, and remove `--no-firewall`.
Without systemd, the script loads the table with `nft -f` and warns that nothing loads it at boot.

### Choices to know

- The script does not use `eval`, arrays, or `local`. Each function has its own variable prefix.
- The shell parses the whole file before `main` runs. A command cannot read the rest of a piped script.
- `run` wraps the commands that change the host (`apt-get`, `systemctl`, `install`, `nft`). It honors `--dry-run` and copies the output to the log file.
- `write_file` compares the old and the new content. It writes a file only when the content or the mode differs.
- The script never enables, starts, stops, or restarts `nftables.service` (S44). Its `ExecStop` is `nft flush ruleset`, and the postinst of the `nftables` package runs `try-restart` on each upgrade. Both remove the rules of netavark (the container ports) and of Tailscale.
- `care-pi-firewall.service` (`Type=oneshot`, `RemainAfterExit=yes`) runs `nft -f /etc/care-pi/firewall.nft` at start and reload, and `nft delete table inet care-pi` at stop. It orders itself like the `nftables.service` of Debian: `DefaultDependencies=no`, `Wants=network-pre.target`, `Before=network-pre.target network.target shutdown.target`. It is `WantedBy=multi-user.target`.
- `firewall.nft` starts with `table inet care-pi` and `delete table inet care-pi`, then defines the table. Thus `nft -f` replaces the table in one transaction and is idempotent. Without the two lines, a second `nft -f` adds each rule again.
- An older install.sh appended an `include` for `/etc/nftables.d/*.nft` to `/etc/nftables.conf`, wrote `/etc/nftables.d/care-pi.nft`, and enabled `nftables.service`. The firewall step undoes this: it removes the exact lines it added (and puts back `nftables.conf.care-pi.bak` when the rest of the file is the same), removes the old rule file, and disables `nftables.service` when the ruleset has only care-pi, netavark, Tailscale, and the stock empty `inet filter` table. A runtime drop-in with an empty `ExecStop=` marks the service inactive without the flush. In all other cases it keeps the service and prints a note.
- `apt-get update` runs only when a package is absent.

## Uninstall

`uninstall.sh` stops `care.target` and `care-clock.service`. It disables `care-start.service`, `care.target`, and `care-clock.service` with `systemctl`.
It never stops the pod unit.
It runs `systemctl disable --now care-pi-firewall.service` (the stop deletes the table `inet care-pi`).
It removes the quadlets, the unit files, the NetworkManager hook, `/etc/care-pi/firewall.nft`, the files of an older install.sh (`/etc/nftables.d/care-pi.nft` and the `include` line), and the agent binary.
It keeps the avahi and journald settings.
It keeps `/etc/care-pi` and `/var/lib/care-pi` unless you add `--purge-data`.
`--purge-data` also removes the podman secrets `care-pi-*`. The container images stay.
`--purge-data` asks two questions, unless you add `--yes`.

```sh
sudo sh installer/uninstall.sh [--yes] [--purge-data]
```
