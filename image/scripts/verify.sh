#!/bin/bash
# verify.sh IMAGE.img.xz LOCK.json WORKDIR: check the file tree of a built care-pi image without a boot
# (docs/image.md, "Check an image"). It runs in the build container (privileged, linux/arm64): it
# unpacks the image into WORKDIR, mounts the partitions with loop devices, and checks the files, the
# units, the packages, the pre-pulled images (podman --root), and the pre-seeded solution.
# The exit code is 1 when a check fails.
set -euo pipefail
img_xz="$1"
lock="$2"
work="$3"

fails=0
pass() { printf 'PASS  %s\n' "$*"; }
fail() { printf 'FAIL  %s\n' "$*"; fails=$((fails + 1)); }
check() {
	local desc="$1"
	shift
	if "$@" >/dev/null 2>&1; then pass "${desc}"; else fail "${desc}"; fi
}
info() { printf 'INFO  %s\n' "$*"; }

mnt="${work}/mnt"
cleanup() {
	umount "${mnt}/boot/firmware" 2>/dev/null || true
	umount "${mnt}" 2>/dev/null || true
	rm -f "${work}/image.img"
}
trap cleanup EXIT
rm -rf "${work}"
mkdir -p "${mnt}" "${work}/run"
xz -T0 -dc "${img_xz}" > "${work}/image.img"
info "image: $(basename "${img_xz}"), $(stat -c %s "${work}/image.img") bytes extracted"

# The partitions. mount -o offset needs no partition scan of the loop device.
sfdisk -J "${work}/image.img" > "${work}/parts.json"
p1_start=$(jq '.partitiontable.partitions[0].start' "${work}/parts.json")
p1_size=$(jq '.partitiontable.partitions[0].size' "${work}/parts.json")
p2_start=$(jq '.partitiontable.partitions[1].start' "${work}/parts.json")
p2_size=$(jq '.partitiontable.partitions[1].size' "${work}/parts.json")
info "partitions: boot $((p1_size * 512 / 1048576)) MiB (type $(jq -r '.partitiontable.partitions[0].type' "${work}/parts.json")), root $((p2_size * 512 / 1048576)) MiB"
mount -o "loop,offset=$((p2_start * 512)),sizelimit=$((p2_size * 512))" "${work}/image.img" "${mnt}"
mount -o "loop,offset=$((p1_start * 512)),sizelimit=$((p1_size * 512))" "${work}/image.img" "${mnt}/boot/firmware"

# ---------------------------------------------------------------- boot partition
boot="${mnt}/boot/firmware"
cmdline="$(cat "${boot}/cmdline.txt")"
info "cmdline.txt: ${cmdline}"
check "cmdline.txt has one line" test "$(grep -c . "${boot}/cmdline.txt")" = 1
check "cmdline.txt has cgroup_enable=memory cgroup_memory=1" grep -q 'cgroup_enable=memory cgroup_memory=1' "${boot}/cmdline.txt"
check "cmdline.txt has no cgroup_disable=memory" bash -c "! grep -q 'cgroup_disable=memory' '${boot}/cmdline.txt'"
for f in user-data meta-data network-config; do
	check "boot partition has the cloud-init seed ${f} (Imager writes it)" test -f "${boot}/${f}"
done
check "boot partition has kernel8.img (Pi 4)" test -f "${boot}/kernel8.img"
check "boot partition has kernel_2712.img (Pi 5, 16 KB pages)" test -f "${boot}/kernel_2712.img"

# ---------------------------------------------------------------- root file system
check "care-pi version runs" "${mnt}/usr/local/bin/care-pi" version
info "care-pi version: $("${mnt}/usr/local/bin/care-pi" version | head -n 1)"
check "host name is care" test "$(cat "${mnt}/etc/hostname")" = care
info "os-release: $(sed -n 's/^PRETTY_NAME="\(.*\)"$/\1/p' "${mnt}/etc/os-release")"
info "rpi-issue: $(head -n 1 "${mnt}/etc/rpi-issue" 2>/dev/null || echo none)"
for u in care-firstboot.service care-pi.service care-pi-firewall.service care-pi-led.service care-pi-led.path \
	avahi-daemon.service systemd-timesyncd.service apt-daily.timer apt-daily-upgrade.timer NetworkManager.service \
	cloud-init-main.service cloud-final.service; do
	state="$(systemctl --root="${mnt}" is-enabled "${u}" 2>/dev/null || true)"
	if [ "${state}" = enabled ]; then pass "unit ${u} is enabled"; else fail "unit ${u} is enabled (got: ${state:-none})"; fi
done
for p in podman crun netavark aardvark-dns conmon skopeo restic nftables avahi-daemon unattended-upgrades cloud-init rpi-cloud-init-mods rpi-swap; do
	v="$(dpkg-query --admindir="${mnt}/var/lib/dpkg" -W -f='${Version}' "${p}" 2>/dev/null || true)"
	if [ -n "${v}" ]; then pass "package ${p} ${v}"; else fail "package ${p} is installed"; fi
done
holds="$(chroot "${mnt}" apt-mark showhold | tr '\n' ' ')"
check "apt holds podman crun netavark aardvark-dns conmon skopeo (${holds})" bash -c "for p in podman crun netavark aardvark-dns conmon skopeo; do echo ' ${holds} ' | grep -q \" \$p \" || exit 1; done"
for f in etc/rpi/swap.conf.d/50-care-pi.conf etc/systemd/journald.conf.d/50-care-pi.conf etc/care-pi/firewall.nft \
	etc/apt/apt.conf.d/52care-pi.conf etc/sysctl.d/50-care-pi.conf etc/systemd/system/care-pi.service etc/systemd/system/care-firstboot.service \
	usr/local/lib/care-pi/care-firstboot usr/local/lib/care-pi/care-pi-led; do
	check "file /${f}" test -f "${mnt}/${f}"
done
check "NetworkManager hook /etc/NetworkManager/dispatcher.d/90-care-pi is executable" test -x "${mnt}/etc/NetworkManager/dispatcher.d/90-care-pi"
check "avahi allow-interfaces is set" grep -q '^allow-interfaces=' "${mnt}/etc/avahi/avahi-daemon.conf"
check "/var/log/journal is 2755" test "$(stat -c %a "${mnt}/var/log/journal")" = 2755

# Nothing device-specific is in the image: the first boot makes the secrets, the config, and the claim code.
check "no config.yaml" test ! -e "${mnt}/etc/care-pi/config.yaml"
check "no lock.yaml" test ! -e "${mnt}/etc/care-pi/lock.yaml"
check "no secrets" test -z "$(ls -A "${mnt}/etc/care-pi/secrets")"
check "no firstboot-done marker" test ! -e "${mnt}/var/lib/care-pi/state/firstboot-done"
check "no SSH host keys" bash -c "! ls '${mnt}'/etc/ssh/ssh_host_*_key >/dev/null 2>&1"
check "machine-id is uninitialized" grep -qx uninitialized "${mnt}/etc/machine-id"

# ---------------------------------------------------------------- the pre-seeded solution
check "/etc/care-pi/image-release.json" jq -e .version "${mnt}/etc/care-pi/image-release.json"
info "image-release.json: $(jq -c . "${mnt}/etc/care-pi/image-release.json")"
dist="${mnt}/var/lib/care-pi-dist"
check "dist snapshot index.json is valid" "${mnt}/usr/local/bin/care-pi" solution validate "${dist}/index/index.json"
check "dist snapshot dist_base is file:///var/lib/care-pi-dist/" test "$(jq -r .dist_base "${dist}/index/index.json")" = file:///var/lib/care-pi-dist/
while IFS=$'\t' read -r url sum; do
	check "dist snapshot ${url} has the SHA-256 of the index" test "$(sha256sum "${dist}/${url}" | cut -d' ' -f1)" = "${sum}"
done < <(jq -r '.solutions[].locks[] | [.url, .sha256] | @tsv' "${dist}/index/index.json")
build_id="$(jq -r .fe.build_id "${lock}")"
check "FE build ${build_id} is pre-seeded (fe/${build_id}/index.html)" test -f "${mnt}/var/lib/care-pi/fe/${build_id}/index.html"
check "fe/current is absent (setup mode serves the setup page)" test ! -e "${mnt}/var/lib/care-pi/fe/current"
check "FE bundle ${build_id} is in the dist snapshot" test -f "${dist}/$(jq -r .fe.url "${lock}")"
while IFS=$'\t' read -r slug sha entry; do
	check "FE plug bundle ${slug} ${sha:0:12} is pre-seeded" test -f "${mnt}/var/lib/care-pi/plugs/${slug}/${sha:0:12}/${entry}"
done < <(jq -r '.plugs // {} | to_entries[] | .value.frontend // empty | select(.source == "built") | [.slug, .sha, .entry] | @tsv' "${lock}")

# ---------------------------------------------------------------- the pre-pulled images
store="${mnt}/var/lib/containers/storage"
pod() { podman --root "${store}" --runroot "${work}/run" --storage-driver overlay "$@"; }
pod images --format '{{.Repository}}:{{.Tag}} {{.Size}} {{.Digest}}' | sed 's/^/INFO  image: /'
while IFS= read -r ref; do
	check "image ${ref} exists (the reference of the units)" pod image exists "${ref}"
	arch="$(pod image inspect --format '{{.Architecture}}' "${ref}" 2>/dev/null || true)"
	if [ "${arch}" = arm64 ]; then pass "image ${ref%%@*} is arm64"; else fail "image ${ref%%@*} is arm64 (got: ${arch:-none})"; fi
done < <(jq -r '(.solution_image | .image + "@" + .digest), (.images[] | (.ref | sub(":[^:/]+$"; "")) + "@" + .digest)' "${lock}")
info "size of /var/lib/containers: $(du -sh "${mnt}/var/lib/containers" | cut -f1)"
info "size of /var/lib/care-pi and /var/lib/care-pi-dist: $(du -sh "${mnt}/var/lib/care-pi" | cut -f1), $(du -sh "${dist}" | cut -f1)"
info "used on the root file system: $(df -h --output=used,size "${mnt}" | tail -n 1)"

echo
if [ "${fails}" -eq 0 ]; then
	echo "RESULT: all checks passed"
else
	echo "RESULT: ${fails} check(s) failed"
	exit 1
fi
