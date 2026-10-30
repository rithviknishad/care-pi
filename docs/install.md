# Install Care on a host

This page tells you how to run `installer/install.sh`.
The script prepares a Raspberry Pi OS or Debian-family host.
It installs the `care-pi` agent and its service `care-pi.service`, and runs `care-pi bootstrap`.
By default, bootstrap starts setup mode: Caddy serves a setup page at `https://care.local` and the console at
`https://care.local:8443/`. The script prints a claim code. Open the console, enter the claim code, and complete
the setup. The setup finish installs Care.
With `--unattended`, bootstrap installs Care at once, without the console (the Phase 1 behaviour).
Then bootstrap builds care_fe on the host (`--fe-provider local`). The first build takes about 3 minutes on a Pi 5
and needs internet.

Related pages: `installer/README.md` (developer notes), `plans/05-image-and-installer.md` (design).

## Before you start

You need:

- A Raspberry Pi 4 or 5 with 2 GB of RAM or more, or another supported host.
- An ethernet cable. Wi-Fi setup comes in a later version.
- A user that can run `sudo`.
- Access to the internet for `apt`, for the agent download, and for the first care_fe build.

Supported hosts:

| Host | Status |
|---|---|
| Raspberry Pi OS Lite, trixie, 64-bit | Supported. This is the target. It ships podman 5.4. |
| Debian 13 (trixie), amd64 or arm64 | Supported. It ships podman 5.4. |
| Ubuntu 24.04 and older | Not supported in this version. Ubuntu 24.04 ships podman 4.9. The script stops before it changes the host. |
| Ubuntu 25.04 or newer | Not tested. The podman check decides. |
| Debian 12 | Stops at the podman check, before the script installs a package. Podman 4.3.1 is too old. |
| Fedora and Red Hat family | Not supported in this version. |
| 32-bit systems | Refused. |

The script needs systemd as PID 1, cgroup v2, and podman 5.4 or newer with quadlet.
The agent requires the same podman version (`care-pi host check`).
The script checks the podman version before it installs a package.
The script adds no third-party apt repository.

## Run the script

The script is one file. Run it from a pipe:

```sh
curl -fsSL https://raw.githubusercontent.com/rithviknishad/care-pi/main/installer/install.sh | sudo sh -s -- --yes
```

The repository is private today. This URL returns 404 until the repository is public.
See [Develop and test](#develop-and-test).

The `sh -s --` words do this:

- `-s` tells `sh` to read the script from the pipe.
- `--` ends the options of `sh`. All flags after `--` go to the script.

To set an option, add a flag after `--`:

```sh
curl -fsSL <URL> | sudo sh -s -- --preset solo --hostname clinic --yes
```

To set an option with an environment variable, put the variable after `sudo`:

```sh
curl -fsSL <URL> | sudo CARE_PI_PRESET=solo sh -s -- --yes
```

You can also save the script and run it from a file:

```sh
sudo sh install.sh --yes
```

If you start the script from a file without root, it starts itself again with `sudo`.
If you pipe the script into a shell without root, it cannot do this.
It prints the correct command and stops.

Without `--yes`, the script shows its plan and asks one question.
It reads the answer from the terminal, also when you use a pipe.
If there is no terminal, it stops and asks you to add `--yes`.

## Options

| Flag | Environment variable | Default | Meaning |
|---|---|---|---|
| `--version TAG` | `CARE_PI_VERSION` | `latest` | Agent release tag. |
| `--hostname NAME` | `CARE_PI_HOSTNAME` | `care` | Host name. The mDNS name is `NAME.local`. |
| `--preset NAME` | `CARE_PI_PRESET` | `small-clinic` | Preset for `care-pi bootstrap` (`solo`, `small-clinic`, or `busy-counter`). |
| `--fe-provider NAME` | `CARE_PI_FE_PROVIDER` | `local` | `fe.provider` for `care-pi bootstrap` (`local`, `artifact`, or `none`). `local` builds care_fe on the host. `none` serves only the setup page. See `docs/fe.md`. |
| `--unattended` | `CARE_PI_UNATTENDED` | off | Ask no questions. Imply `--yes`. Run `care-pi bootstrap --unattended`: install Care at once, without the console setup (the Phase 1 behaviour). Use it for CI tests and prepared hosts. |
| `--yes` | `CARE_PI_YES` | off | Do not ask for confirmation. |
| `--no-firewall` | `CARE_PI_NO_FIREWALL` | off | Do not install the care-pi firewall (`care-pi-firewall.service` and its nftables table). |
| `--no-avahi` | `CARE_PI_NO_AVAHI` | off | Do not install or configure avahi. |
| `--no-apt-hold` | `CARE_PI_NO_APT_HOLD` | off | Do not hold the container packages. |
| `--skip-bootstrap` | `CARE_PI_SKIP_BOOTSTRAP` | off | Prepare the host. Do not run `care-pi bootstrap`. |
| `--binary PATH` | `CARE_PI_BINARY` | none | Install the agent from a local file. Do not download it. |
| `--dry-run` | `CARE_PI_DRY_RUN` | off | Print what the script would change. Change nothing. |
| `--help` | none | none | Print the help text. |
| none | `CARE_PI_DOWNLOAD_BASE` | GitHub releases | URL that holds `care-pi_linux_<arch>` and `SHA256SUMS`. |

A flag overrides its environment variable.
A switch variable accepts `1`, `true`, or `yes`.
The host name must be 1 to 63 lowercase letters, digits, or hyphens. Do not add `.local`.

The script has one more environment variable for tests: `CARE_PI_ALLOW_NO_SYSTEMD=1`.
Use it only in a test container. See [Develop and test](#develop-and-test).

## What the script does

The script runs these steps in order.
Each step checks the host first.
If the host is already correct, the step prints `already configured` and changes nothing.

Before step 1, the script detects the host.
It reads `/etc/os-release`, the CPU architecture, systemd, cgroup v2, and the Raspberry Pi model.
It stops if the host is not supported.

| # | Step | What changes |
|---|---|---|
| 1 | Install packages | First, the script checks the podman version: the installed version, or else the version in the apt sources. It must be 5.4 or newer. Then `apt` installs `podman`, `skopeo`, `restic`, `avahi-daemon`, `nftables`, `systemd-timesyncd`, `unattended-upgrades`, `ca-certificates`, `curl`, `uidmap`, `catatonit`, and `iproute2`. The script checks podman again: version 5.4 or newer, and quadlet present. |
| 2 | Memory cgroup | On a Raspberry Pi, the script edits `/boot/firmware/cmdline.txt`. It removes `cgroup_disable=memory` and adds `cgroup_enable=memory cgroup_memory=1`. It saves the original as `cmdline.txt.care-pi.bak` once. On other hosts, the script only checks that the memory controller exists. |
| 3 | zram swap | The script makes sure zram swap is at least 90 % of RAM. On Raspberry Pi OS with `rpi-swap`, it writes `/etc/rpi/swap.conf.d/50-care-pi.conf`. On other hosts, it installs `systemd-zram-generator` and writes `/etc/systemd/zram-generator.conf.d/50-care-pi.conf` with `zram-size = ram`. |
| 4 | Host name | The script sets the host name to `--hostname` when you give the flag. It also sets the name when the host name is `raspberrypi` and avahi is on. In all other cases, it keeps the host name. It never renames the host silently. |
| 5 | avahi | The script sets `allow-interfaces=eth0,wlan0` and `use-ipv4=yes` in `/etc/avahi/avahi-daemon.conf`. It restarts avahi after a change. It reads the mDNS name that avahi announces from the journal. It prints that name and the LAN IPv4 addresses. |
| 6 | Time | The script enables `systemd-timesyncd` and runs `timedatectl set-ntp true`. If another NTP service is installed, the script keeps it. |
| 7 | Journal | The script writes `/etc/systemd/journald.conf.d/50-care-pi.conf` with `Storage=persistent` and `SystemMaxUse=200M`. It creates `/var/log/journal` (root:systemd-journal, mode 2755), runs `systemd-tmpfiles --create --prefix /var/log/journal`, restarts `systemd-journald`, and runs `journalctl --flush`. Raspberry Pi OS keeps the journal in RAM by default. Now `care-pi logs` and `journalctl -b -1` also show the previous boots. |
| 8 | Firewall | The script writes `/etc/care-pi/firewall.nft` and `/etc/systemd/system/care-pi-firewall.service`, then enables and starts the unit. See [Firewall](#firewall). |
| 9 | Unattended upgrades | The script writes `/etc/apt/apt.conf.d/52care-pi.conf` with `Unattended-Upgrade::Automatic-Reboot "false"`. It enables the apt timers. |
| 10 | apt holds | The script runs `apt-mark hold` on `podman crun netavark aardvark-dns conmon skopeo`. A Care release updates these packages on purpose. The future command `care-pi update --os` lifts the holds. |
| 11 | Directories | The script creates `/etc/care-pi` and `/var/lib/care-pi` with the owners and modes from the Phase 1 contract. |
| 12 | Agent | The script installs `/usr/local/bin/care-pi`. See [Agent download](#agent-download). |
| 13 | Agent service | The script writes `/etc/systemd/system/care-pi.service` (`care-pi serve`: the console API on `/run/care-pi/api.sock`), enables it, and starts it. It restarts the unit when the unit or the binary changed. After a reboot request, the unit starts at the next boot. `docs/console-server.md` describes the unit. |
| 14 | NetworkManager hook | If NetworkManager is present, the script installs `/etc/NetworkManager/dispatcher.d/90-care-pi`. After an address change, it waits 5 s and runs `care-pi apply --reason address-change`. The events of those 5 s give one apply. An event during an apply gives one more apply after it. Two applies of the hook never overlap. |
| 15 | Host check and bootstrap | The script runs `care-pi host check`. Then it runs `care-pi bootstrap --hostname NAME --preset NAME --fe-provider NAME --yes` (with `--unattended` when the script has it). On a new host, bootstrap starts setup mode and prints the claim code. With `--unattended` and `--fe-provider local`, bootstrap also makes the first care_fe build. A host with an earlier install (a `lock.yaml`) always gets the full apply. |

The script writes a log to `/var/log/care-pi-install.log`.
It prefixes its own lines with `[care-pi]`.
If a step fails, the script prints the name of the step.

### Firewall

The script writes the table `inet care-pi` to `/etc/care-pi/firewall.nft`.
The unit `care-pi-firewall.service` loads the file at boot, before the network starts (S44).
Its input chain accepts these packets:

- loopback traffic,
- established and related connections,
- ICMP and ICMPv6,
- 22/tcp, only when sshd is active,
- 80/tcp and 443/tcp (Care),
- 8443/tcp (the console, S48),
- 5353/udp.

The chain policy is `accept`. The chain drops nothing in Phase 1.
Phase 7 changes the policy to `drop`.
The script does not touch the chains of podman.

The unit changes only the table `inet care-pi`:

- Start and reload: `nft -f /etc/care-pi/firewall.nft`. The file deletes the table and creates it again in one transaction, so a second load gives the same table.
- Stop: `nft delete table inet care-pi`. It never runs `nft flush ruleset`.

The script does not use `nftables.service`. The stop of `nftables.service` runs `nft flush ruleset`,
and an upgrade of the `nftables` package restarts it. Both remove the rules of podman (the container ports)
and of Tailscale. unattended-upgrades can install such an upgrade.

The script does not change a firewall that you configured:

- It does not change `/etc/nftables.conf`, except to remove the lines of an older install.sh (see below).
  If that file has rules of its own, the script prints a note:
  if they drop packets, also allow 80/tcp, 443/tcp, 8443/tcp, and 5353/udp there.
- If `ufw` or `firewalld` is active, the script skips the step.

The script checks the rules with `nft -c -f` before it writes a file.

An older install.sh used `/etc/nftables.d/care-pi.nft`, added an `include` line to `/etc/nftables.conf`,
and enabled `nftables.service`. The firewall step removes this setup:

- It removes the `include` line and its comment. If the rest of the file is the same as
  `nftables.conf.care-pi.bak`, it puts the backup back. Else it keeps the backup.
- It removes `/etc/nftables.d/care-pi.nft`.
- It disables `nftables.service` only when care-pi enabled it, `/etc/nftables.conf` is the stock file,
  and the live ruleset has no table of other software
  (only `inet care-pi`, `inet netavark`, the Tailscale tables, and the empty `inet filter` of the stock file).
  It does not stop the service with its own `ExecStop`, so the ruleset stays. Else it keeps the service and prints a note.

### Agent download

With `--binary PATH`, the script copies that file to `/usr/local/bin/care-pi`.

Without `--binary`, the script downloads two files from the release:

- `care-pi_linux_arm64` or `care-pi_linux_amd64`,
- `SHA256SUMS`.

The script compares the `sha256` sum of the binary with the line for `care-pi_linux_<arch>` in `SHA256SUMS`.
If the sums differ, the script stops.
It installs the binary with a copy and a rename, so the file is never half written.
Then it runs `care-pi version`.

The release URL is `https://github.com/rithviknishad/care-pi/releases/download/<version>/`.
For `latest`, the URL is `https://github.com/rithviknishad/care-pi/releases/latest/download/`.
`CARE_PI_DOWNLOAD_BASE` replaces this URL.

If the agent is already installed and you give no `--version`, the script keeps it.
To change the agent, add `--version TAG`.

## When the script asks for a reboot

On a Raspberry Pi, a new kernel command line needs a reboot.
The `rpi-swap` settings also need a reboot.
The script cannot apply them while the system runs.

In this case, the script:

1. makes all other changes,
2. runs `care-pi host check` and prints its output (some checks fail before the reboot),
3. prints `REBOOT REQUIRED` with the reason,
4. does not run `care-pi bootstrap`.

Do these steps:

```sh
sudo reboot
# after the Pi is up again:
sudo care-pi bootstrap --hostname care --preset small-clinic --fe-provider local --yes
```

The script prints the exact bootstrap command with your host name, preset, and FE provider.
You can also run the installer again after the reboot. It makes no changes and runs bootstrap.

If the memory controller is already active, no reboot is needed.

## After the install

### Setup mode (the default)

When bootstrap ends, it prints the claim code in a box, with the console URL for the mDNS name and for each
LAN IPv4 address:

```text
+------------------------------------------------------+
|  Claim code: 4F7K-Q2MX-9PDA                          |
|                                                      |
|  Open the console and enter the claim code:          |
|    https://care.local:8443/                          |
|    https://192.168.1.10:8443/                        |
|                                                      |
|  Print the code again: sudo care-pi auth claim-code  |
+------------------------------------------------------+
```

1. On a device in the same network, open `https://care.local/`. The browser shows a certificate warning one time.
   The device uses its own local certificate authority. Accept the warning.
2. The setup page offers the root certificate and its SHA-256 fingerprint. Install the certificate on the device.
   Then the console on port 8443 shows no second warning.
3. Open the console link of the setup page (`https://care.local:8443/`). Enter the claim code and a console password
   (12 to 128 characters). Write down the 8 recovery codes.
4. Complete the steps of the setup. The finish installs Care and creates the first Care superuser.

Anyone who can read the claim code is root on the host, so the code adds no new risk.
To print the code again, run `sudo care-pi auth claim-code`. To make a new code, add `--rotate`.
To complete the setup on the host without a browser, see `docs/console-server.md`, "Setup on the host".

### Unattended install

With `--unattended`, the script prints:

- `Care is ready` when an FE build exists. Else it tells you that Care shows the setup page.
  This occurs with `--fe-provider none`, or when `config.yaml` existed with `fe.provider: none`.
  To serve Care, set `fe.provider` to `local` or `artifact` in `/etc/care-pi/config.yaml`.
  Then run `sudo care-pi apply`.
- the mDNS name that avahi announces, for example `https://care.local`,
- one `https://<ip>` address for each LAN IPv4 address.

Another device on your LAN can have the name `care.local` already.
Then avahi renames this host to `care-2.local`.
The script prints the real name, so use the printed name or the IP address.

The first visit shows a certificate warning.
Care uses its own local certificate authority until you enable a public certificate.
Accept the warning once, then install the certificate on each device.

Create the first Care user:

```sh
sudo care-pi care create-superuser --username NAME --email EMAIL
```

The console is at `https://<host>:8443/`. To claim it, print the claim code: `sudo care-pi auth claim-code`.

## Run the script again

The script is safe to run again.
A second run changes nothing and prints `already configured` for each step.
Use a second run to repair a host after you fix the cause of an error.

## Dry run

```sh
sudo sh install.sh --dry-run --skip-bootstrap
```

A dry run prints each change as `dry-run: would ...`.
It changes nothing and writes no log file.
On a host without systemd, a dry run prints a warning and continues.
A real run stops there.

## Develop and test

The care-pi repository is private today.
The default download URL returns 404 until the repository is public.
Use one of these two methods.

**Method 1: a local binary.**
Build the binary on your computer. Copy it and the script to the host. Install the agent from the file:

```sh
make build-linux
scp bin/care-pi_linux_arm64 installer/install.sh pi@care.local:
ssh -t pi@care.local sudo sh install.sh --binary ./care-pi_linux_arm64 --yes
```

**Method 2: a local web server.**
Serve the two release files over HTTP:

```sh
cd bin
sha256sum care-pi_linux_arm64 care-pi_linux_amd64 > SHA256SUMS
python3 -m http.server 8000
```

On the host, set the base URL:

```sh
sudo CARE_PI_DOWNLOAD_BASE=http://<your-computer>:8000 sh install.sh --yes
```

With method 2, the script verifies the checksum of the binary. With method 1, it does not.

To test the script on a host without systemd, for example a container, set `CARE_PI_ALLOW_NO_SYSTEMD=1`.
The script then skips every `systemctl` call and only warns.
Do not use this variable on a real host.
Example:

```sh
docker run --rm -v "$PWD/installer:/src:ro" debian:trixie \
  sh /src/install.sh --dry-run --skip-bootstrap --binary /bin/true
```

## Uninstall

`installer/uninstall.sh` is a development tool.

```sh
sudo sh installer/uninstall.sh            # ask before each action
sudo sh installer/uninstall.sh --yes      # ask nothing
sudo sh installer/uninstall.sh --purge-data
```

The script:

1. runs `systemctl disable --now care-pi.service` (the console agent) and removes its socket,
2. stops `care.target` and `care-clock.service`, and disables `care-start.service`, `care.target`, and `care-clock.service` with `systemctl`,
3. waits until every `care-*` unit is inactive,
4. runs `systemctl disable --now care-pi-firewall.service`. The stop deletes the table `inet care-pi`,
5. removes the quadlets (`/etc/containers/systemd/care*`), the unit files (also `care-pi.service`), the NetworkManager hook, `/etc/care-pi/firewall.nft`, the files of an older install.sh, and the agent binary,
6. removes a leftover pod `care` with `podman pod rm -f`.

The script never stops the pod unit with `systemctl`.

The script keeps `/etc/care-pi` and `/var/lib/care-pi`.
With `--purge-data`, it deletes them.
The data includes the database, the files, the secrets, and the backups on this host.
The script asks two questions before it does this, unless you add `--yes`.

The script does not undo the host preparation.
The packages, the kernel command line, the zram, avahi, and journald settings, the host name, the apt holds, and the unattended-upgrades settings stay in place.

## If the script fails

Read the last lines of the output.
They name the step that failed.
The full output is in `/var/log/care-pi-install.log`.
Fix the cause and run the script again.

**The script stops at the podman check.**
Run `podman --version`.
You need version 5.4 or newer. Debian 13 (trixie) ships podman 5.4.
Debian 12, Ubuntu 24.04, and Ubuntu 22.04 have older versions.
Ubuntu 24.04 is not supported in this version.
Use Raspberry Pi OS (trixie) or Debian 13.

**The memory cgroup is not active after the reboot.**
Run `cat /boot/firmware/cmdline.txt`.
The file must be one line.
It must contain `cgroup_enable=memory cgroup_memory=1`.
It must not contain `cgroup_disable=memory`.
The original file is `/boot/firmware/cmdline.txt.care-pi.bak`.
After the reboot, `cat /sys/fs/cgroup/cgroup.controllers` must list `memory`.
Without this, Care cannot enforce memory limits.
`care-pi host check` uses only `cgroup.controllers`.
On a Pi, the firmware adds `cgroup_disable=memory` to `/proc/cmdline`, also when `cmdline.txt` does not have it.
The later `cgroup_enable=memory` wins. `host check` shows this as a detail, not as a failure.

**cgroup v2 is not active.**
Run `stat -fc %T /sys/fs/cgroup`. The output must be `cgroup2fs`.

**zram swap is not active.**
Run `swapon --show`. The list must show `/dev/zram0` with a size of about 1× RAM.
On Raspberry Pi OS, read `/etc/rpi/swap.conf.d/50-care-pi.conf` and reboot.
On other hosts, run `systemctl status systemd-zram-setup@zram0.service` and `dmesg | grep -i zram`.
The Care front-end build needs this swap on a 2 GB Pi.

**The mDNS name is wrong, or `care.local` does not resolve.**
Another device can use the name `care.local`.
Run `journalctl -u avahi-daemon | grep -i 'host name'`.
Use the name that the script printed, or use the IP address.
To pick another name, run the script with `--hostname NAME`.
Do not test the name with `avahi-resolve` on the host itself. The result is not reliable.
Test the name from another device.

**Port 443 is in use.**
Run `sudo ss -ltnp 'sport = :443'`.
`tailscale serve` binds `<tailnet-ip>:443`. Stop it with `tailscale serve reset`.
Care publishes ports 80, 443, and 8443 on the LAN addresses only.

**The console gives `503` with "care-pi.service does not answer".**
Caddy runs, but the agent does not. Run `sudo systemctl status care-pi.service` and `sudo care-pi logs agent`.
Start it with `sudo systemctl start care-pi.service`. If the unit file is not there, run the script again.

**The download fails.**
The repository is private today. Use `--binary` or `CARE_PI_DOWNLOAD_BASE`.
If the checksum fails, download the files again.

**The firewall step fails.**
Run `nft -c -f /etc/care-pi/firewall.nft` and `systemctl status care-pi-firewall.service`.
To skip the firewall, run the script with `--no-firewall`.
Do not enable, stop, or restart `nftables.service` while Care runs.
Its stop runs `nft flush ruleset`, and the stock `nftables.conf` starts with `flush ruleset`.
Both remove the container network rules. Then Care is not reachable on 80 and 443 until the next full cycle
(`sudo systemctl stop care.target`, wait until each `care-*` unit is inactive, then `sudo care-pi apply`).
To load the care-pi table again, run `sudo systemctl reload care-pi-firewall.service`.

**`apt` is locked.**
Another `apt` process runs. The script waits up to 5 minutes. Run the script again later.
To see the held packages, run `apt-mark showhold`.

**bootstrap fails at `ensure-fe`.**
With `--fe-provider local`, bootstrap builds care_fe. The build needs internet.
The stack runs, and Caddy shows the setup page.
Read the "Troubleshooting" table in `docs/fe.md`. Fix the cause, then run `sudo care-pi apply`.

**The host check fails.**
Run `sudo care-pi host check`.
It lists each prerequisite that the host does not meet.
