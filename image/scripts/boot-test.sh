#!/bin/bash
# boot-test.sh IMAGE.img.xz WORKDIR: boot the root file system of a care-pi image with systemd-nspawn in the
# privileged build container, without a network (only loopback), after an Imager-like customisation
# (user-data with "user:" and a yescrypt hash, a host name). It checks the first boot: cloud-init,
# care-firstboot.service, "care-pi bootstrap --from-image", setup mode with Caddy from the pre-pulled
# images (no registry), the console on port 8443, and the Imager-password claim method.
# This is a container boot, not a Pi boot: no Pi kernel, no zram, no rpi-resize. docs/image.md, section 2.4.
# The exit code is 1 when a check fails. Run it in the build container:
#
#   docker run --rm --privileged --platform linux/arm64 -v /dev:/dev -v care-pi-image-work:/work \
#     -v "$PWD:/src:ro" -v "$PWD/image/out:/out:ro" care-pi-image-builder \
#     bash /src/image/scripts/boot-test.sh /out/care-pi-<version>-<solution>.img.xz /work/boottest
set -euo pipefail
img_xz="$1"
work="$2"
password="care-pi-boot-test"

mnt="${work}/mnt"
nspawn_pid=""
cleanup() {
	[ -n "${nspawn_pid}" ] && kill "${nspawn_pid}" 2>/dev/null && sleep 3
	umount "${mnt}/boot/firmware" 2>/dev/null || true
	umount "${mnt}" 2>/dev/null || true
}
trap cleanup EXIT
if ! command -v systemd-nspawn >/dev/null || ! command -v pgrep >/dev/null; then
	apt-get update -qq && apt-get install -y -qq systemd-container procps >/dev/null 2>&1
fi

rm -rf "${work}"
mkdir -p "${mnt}"
xz -T0 -dc "${img_xz}" > "${work}/image.img"
sfdisk -J "${work}/image.img" > "${work}/parts.json"
p() { jq ".partitiontable.partitions[$1].$2" "${work}/parts.json"; }
mount -o "loop,offset=$(($(p 1 start) * 512)),sizelimit=$(($(p 1 size) * 512))" "${work}/image.img" "${mnt}"
mount -o "loop,offset=$(($(p 0 start) * 512)),sizelimit=$(($(p 0 size) * 512))" "${work}/image.img" "${mnt}/boot/firmware"

# What Imager 2.x writes (rpi-imager CustomisationGenerator): user-data with "user:" and a yescrypt hash.
# A fixed, valid yescrypt setting (libxcrypt returns "*0" for a bad salt). Imager uses the cost j9T too.
hash="$(perl -e 'print crypt($ARGV[0], q($y$j9T$F5Jx5fExrKuPp53xLKQ..1$))' "${password}")"
# shellcheck disable=SC2016 # a literal hash prefix
case "${hash}" in
	'$y$'*) ;;
	*) echo "boot-test: crypt(3) gave no yescrypt hash: ${hash}" >&2; exit 1 ;;
esac
cat > "${mnt}/boot/firmware/user-data" <<EOF
#cloud-config
hostname: "clinic"
manage_etc_hosts: true
packages:
- avahi-daemon

apt:
  preserve_sources_list: true
  conf: |
    Acquire {
      Check-Date "false";
    };

timezone: "Asia/Kolkata"
user:
  name: "clinic"
  shell: /bin/bash
  lock_passwd: false
  passwd: "${hash}"
  sudo: null
ssh_pwauth: true
EOF
echo "instance-id: rpi-imager-boot-test-$(date +%s)" > "${mnt}/boot/firmware/meta-data"

# A container has no zram. "care-pi host check" requires zram of 1x RAM: a unit of this test copy shows a
# zram line of the RAM size in /proc/swaps before care-firstboot runs.
mem_kib="$(awk '/^MemTotal:/ { print $2 }' /proc/meminfo)"
printf 'Filename\t\t\t\tType\t\tSize\t\tUsed\t\tPriority\n/dev/zram0                              partition\t%s\t\t0\t\t100\n' "${mem_kib}" > "${mnt}/etc/boot-test-swaps"
cat > "${mnt}/etc/systemd/system/boot-test-zram.service" <<'EOF2'
[Unit]
Description=boot test: a zram line in /proc/swaps (container)
DefaultDependencies=no
Before=care-firstboot.service
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/mount --bind /etc/boot-test-swaps /proc/swaps
[Install]
WantedBy=multi-user.target
EOF2
ln -sf /etc/systemd/system/boot-test-zram.service "${mnt}/etc/systemd/system/multi-user.target.wants/boot-test-zram.service"

echo "== boot (systemd-nspawn, private network)"
# The build container has no systemd: no scope (--keep-unit), no machined (--register=no), and a tmpfs for
# the state of nspawn (it removes it only from a tmpfs).
mkdir -p /run/systemd/nspawn
mountpoint -q /run/systemd/nspawn || mount -t tmpfs tmpfs /run/systemd/nspawn
# cgroup v2 in the build container: move each process out of the root cgroup, enable the controllers for the
# children, and start nspawn in an empty cgroup. Then the container has the memory controller (MemoryMax works).
cg=/sys/fs/cgroup
mkdir -p "${cg}/builder" "${cg}/nspawn"
while read -r pid; do echo "${pid}" > "${cg}/builder/cgroup.procs" 2>/dev/null || true; done < "${cg}/cgroup.procs"
for c in cpu io memory pids; do echo "+${c}" > "${cg}/cgroup.subtree_control" 2>/dev/null || true; done
echo "cgroup controllers for the container: $(cat "${cg}/nspawn/cgroup.controllers")"
(
	echo "${BASHPID}" > "${cg}/nspawn/cgroup.procs"
	exec systemd-nspawn --quiet --keep-unit --register=no --private-network --boot --console=passive -D "${mnt}" --system-call-filter="@keyring bpf" --capability=all \
		--machine=care-boot
) > "${work}/console.log" 2>&1 &
nspawn_pid=$!

leader() { pgrep -P "${nspawn_pid}" 2>/dev/null | head -n 1; }
in_ct() { nsenter -t "$(leader)" -a -- "$@"; }
for _ in $(seq 1 120); do
	sleep 5
	if [ -n "$(leader)" ] && in_ct test -e /var/lib/care-pi/state/firstboot-done 2>/dev/null; then
		break
	fi
	if [ -n "$(leader)" ] && [ "$(in_ct systemctl is-failed care-firstboot.service 2>/dev/null)" = failed ]; then
		break
	fi
done
sleep 10

fails=0
pass() { printf 'PASS  %s\n' "$*"; }
fail() { printf 'FAIL  %s\n' "$*"; fails=$((fails + 1)); }
check() {
	local desc="$1"
	shift
	if "$@" >/dev/null 2>&1; then pass "${desc}"; else fail "${desc}"; fi
}

echo "== journal of care-firstboot"
in_ct journalctl --no-pager -u care-firstboot.service -o cat | tail -40
echo "== results"
check "care-firstboot.service succeeded" test "$(in_ct systemctl show -p Result --value care-firstboot.service)" = success
check "the first-boot marker exists" in_ct test -f /var/lib/care-pi/state/firstboot-done
check "cloud-init is done" bash -c "nsenter -t $(leader) -a -- cloud-init status | grep -q 'status: done'"
check "the host name of user-data (clinic) is set" test "$(in_ct hostname)" = clinic
# shellcheck disable=SC2016 # awk code and a literal hash prefix.
prefix="$(in_ct awk -F: '$1 == "clinic" { print substr($2, 1, 3) }' /etc/shadow || true)"
# shellcheck disable=SC2016
check "the Imager user has a yescrypt hash in /etc/shadow (${prefix:-none})" test "${prefix}" = '$y$'
check "config.yaml has the host name clinic" in_ct grep -q 'hostname: clinic' /etc/care-pi/config.yaml
check "the solution lock of the image is in the update cache" in_ct test -f /var/lib/care-pi/state/update-cache/locks/care-clinic-2026.11.0.lock.yaml
check "the dist snapshot is the cached index" bash -c "nsenter -t $(leader) -a -- grep -q 'file:///var/lib/care-pi-dist/' /var/lib/care-pi/state/update-cache/index.json"
check "care-pi.service is active" test "$(in_ct systemctl is-active care-pi.service)" = active
check "care-caddy.service is active" test "$(in_ct systemctl is-active care-caddy.service)" = active
check "care.target is active (setup mode, no clock wait)" test "$(in_ct systemctl is-active care.target)" = active
check "the setup state is not ready (setup mode)" bash -c "! nsenter -t $(leader) -a -- care-pi setup status --json | grep -q '\"state\": \"ready\"'"
check "no image was pulled" bash -c "! nsenter -t $(leader) -a -- journalctl --no-pager -o cat | grep -qiE 'Trying to pull|pulling image|podman pull'"
status="$(in_ct curl -s --unix-socket /run/care-pi/api.sock -H 'Host: clinic.local' -H 'X-Forwarded-For: 127.0.0.1' http://clinic.local/_care-pi/status || true)"
echo "INFO  status: ${status}"
check "the console offers the claim method os-password first" bash -c "echo '${status}' | jq -e '.claim_methods[0] == \"os-password\"'"
code="$(in_ct curl -s --unix-socket /run/care-pi/api.sock -X POST -H 'Host: clinic.local:8443' -H 'Origin: https://clinic.local:8443' \
	-H 'X-Forwarded-For: 127.0.0.1' -H 'Content-Type: application/json' \
	-d "{\"method\":\"os-password\",\"secret\":\"${password}\",\"new_password\":\"amber-lantern-quietly-folds-9\"}" \
	-o "/run/boot-test-claim.json" -w '%{http_code}' http://clinic.local:8443/api/v1/session/claim || true)"
echo "INFO  claim response: $(in_ct cat /run/boot-test-claim.json 2>/dev/null | head -c 400)"
check "the claim with the Imager password succeeds (HTTP ${code})" bash -c "[ '${code}' = 200 ] || [ '${code}' = 201 ]"
sleep 5
check "the claim wrote the account" in_ct test -f /etc/care-pi/secrets/agent.json
check "care-pi-led-off.service started after the claim (path unit)" test "$(in_ct systemctl is-active care-pi-led-off.service)" = active
in_ct podman ps --format 'INFO  container: {{.Names}} {{.Image}} {{.Status}}'

echo
if [ "${fails}" -eq 0 ]; then
	echo "RESULT: all checks passed"
else
	echo "RESULT: ${fails} check(s) failed"
	exit 1
fi
