#!/bin/sh
# care-pi uninstaller. This is a development tool. It removes the units, quadlets,
# hooks, and agent binary that install.sh and care-pi create.
#
# It also removes the care-pi firewall (care-pi-firewall.service and its table).
# It keeps /etc/care-pi and /var/lib/care-pi (the data) unless you use --purge-data.
# It does not undo the host preparation of install.sh: the packages, the kernel
# command line, the zram, avahi, and journald settings, the host name, the apt
# holds, and the unattended-upgrades settings stay in place.
#
# Usage: sudo sh uninstall.sh [--yes] [--purge-data]

set -eu

PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
export PATH

YES=0
PURGE=0
SYSTEMD=0
WAIT_SECONDS=120

log() {
  printf '[care-pi] %s\n' "$*"
}

warn() {
  printf '[care-pi] WARNING: %s\n' "$*" >&2
}

die() {
  printf '[care-pi] ERROR: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Usage: uninstall.sh [options]

Stop Care and remove the units, quadlets, hooks, and agent binary.
This is a development tool. Run it as root.

Options:
  --yes          Do not ask for confirmation.
  --purge-data   Also delete /etc/care-pi and /var/lib/care-pi. This removes the
                 database, the files, the secrets, and the backups on this host.
                 The script asks twice, unless you add --yes.
  --help         Print this text.

The script keeps the packages and the host settings that install.sh made
(kernel command line, zram, avahi, journald, host name, apt holds, upgrades).
It removes the care-pi firewall unit and its nftables table.
EOF
}

# ask PROMPT reads a yes or no answer from the terminal.
ask() {
  if ! ( : </dev/tty ) 2>/dev/null; then
    die "There is no terminal to ask a question. Add --yes to run without questions."
  fi
  printf '[care-pi] %s ' "$1" >/dev/tty
  read -r ask_reply </dev/tty || ask_reply=""
  case $ask_reply in
    y | Y | yes | YES | Yes) return 0 ;;
    *) return 1 ;;
  esac
}

# ask_word PROMPT WORD succeeds when the user types WORD.
ask_word() {
  if ! ( : </dev/tty ) 2>/dev/null; then
    die "There is no terminal to ask a question. Add --yes to run without questions."
  fi
  printf '[care-pi] %s ' "$1" >/dev/tty
  read -r ask_reply </dev/tty || ask_reply=""
  [ "$ask_reply" = "$2" ]
}

ensure_root() {
  if [ "$(id -u)" = 0 ]; then
    return 0
  fi
  if [ -f "$0" ] && command -v sudo >/dev/null 2>&1; then
    log "The script is not root. Start it again with sudo."
    exec sudo sh "$0" "$@"
  fi
  die "Run this script as root, for example: sudo sh uninstall.sh"
}

# active_care_units prints the care-* units that are not inactive.
active_care_units() {
  systemctl list-units --plain --no-legend --state=active,activating,deactivating 'care-*' 2>/dev/null
}

stop_care() {
  if [ "$SYSTEMD" != 1 ]; then
    warn "systemd is not running. The script cannot stop the Care units."
    return 0
  fi
  log "Stop care.target and care-clock.service."
  systemctl stop care.target care-clock.service 2>/dev/null || true
  # The agent stops the containers through care.target. Wait until each care-*
  # unit is inactive. The script never stops the pod unit.
  waited=0
  while [ -n "$(active_care_units)" ] && [ "$waited" -lt "$WAIT_SECONDS" ]; do
    sleep 2
    waited=$((waited + 2))
  done
  if [ -n "$(active_care_units)" ]; then
    warn "These units are still active after $WAIT_SECONDS s:"
    active_care_units >&2
  fi
  log "Disable care-start.service, care.target, and care-clock.service."
  systemctl disable care-start.service care.target care-clock.service 2>/dev/null || true
  for timer in /etc/systemd/system/care-*.timer; do
    if [ -e "$timer" ]; then
      systemctl disable --now "${timer##*/}" 2>/dev/null || true
    fi
  done
}

remove_units() {
  log "Remove the quadlets and the unit files."
  rm -rf /etc/containers/systemd/care*
  rm -f /etc/systemd/system/care.target /etc/systemd/system/care-clock.service /etc/systemd/system/care-start.service
  rm -f /etc/systemd/system/care-*.timer /etc/systemd/system/care-*.service
  rm -rf /etc/systemd/system/care.target.d /etc/systemd/system/care-*.d
  for link in /etc/systemd/system/*.wants/care*; do
    if [ -L "$link" ]; then
      rm -f "$link"
    fi
  done
  if [ "$SYSTEMD" = 1 ]; then
    systemctl daemon-reload
    systemctl reset-failed 'care-*' 2>/dev/null || true
  fi
}

# After the quadlets are gone, a pod can stay behind. Remove it with podman.
remove_pod() {
  if command -v podman >/dev/null 2>&1 && podman pod exists care 2>/dev/null; then
    log "Remove the leftover pod 'care' with podman."
    podman pod rm -f care || warn "podman could not remove the pod 'care'."
  fi
}

# remove_firewall stops care-pi-firewall.service before remove_units deletes its
# file. Its ExecStop deletes only the table inet care-pi. The script never stops
# nftables.service: its stop flushes the whole ruleset.
remove_firewall() {
  log "Remove the care-pi firewall (care-pi-firewall.service and the table inet care-pi)."
  if [ "$SYSTEMD" = 1 ]; then
    systemctl disable --now care-pi-firewall.service 2>/dev/null || true
  fi
  if command -v nft >/dev/null 2>&1 && nft list table inet care-pi >/dev/null 2>&1; then
    nft delete table inet care-pi || warn "nft could not delete the table inet care-pi."
  fi
  rm -f /etc/systemd/system/care-pi-firewall.service /etc/care-pi/firewall.nft
  # An older install.sh wrote /etc/nftables.d/care-pi.nft and added an include
  # line (after a blank line and a comment) to the stock nftables.conf.
  rm -f /etc/nftables.d/care-pi.nft
  if [ -f /etc/nftables.conf ] \
    && grep -qxF '# care-pi: load the rule files in /etc/nftables.d' /etc/nftables.conf; then
    awk '
      { line[NR] = $0 }
      END {
        for (i = 1; i <= NR; i++) {
          if (line[i] == "# care-pi: load the rule files in /etc/nftables.d" && line[i + 1] == "include \"/etc/nftables.d/*.nft\"") {
            drop[i] = 1; drop[i + 1] = 1
            if (i > 1 && line[i - 1] == "") drop[i - 1] = 1
          }
        }
        for (i = 1; i <= NR; i++) if (!drop[i]) print line[i]
      }
    ' /etc/nftables.conf >/etc/nftables.conf.care-pi.new
    chmod "$(stat -c %a /etc/nftables.conf)" /etc/nftables.conf.care-pi.new
    mv -f /etc/nftables.conf.care-pi.new /etc/nftables.conf
    if cmp -s /etc/nftables.conf /etc/nftables.conf.care-pi.bak; then
      rm -f /etc/nftables.conf.care-pi.bak
    fi
  fi
}

remove_hooks() {
  log "Remove the NetworkManager hook."
  rm -f /etc/NetworkManager/dispatcher.d/90-care-pi
}

remove_binary() {
  log "Remove the agent binary."
  rm -f /usr/local/bin/care-pi /usr/local/bin/care-pi.new
}

purge_data() {
  log "Delete /etc/care-pi and /var/lib/care-pi."
  rm -rf /etc/care-pi /var/lib/care-pi
}

main() {
  while [ $# -gt 0 ]; do
    case $1 in
      --yes)
        YES=1
        ;;
      --purge-data)
        PURGE=1
        ;;
      --help | -h)
        usage
        exit 0
        ;;
      *)
        printf 'uninstall.sh: Unknown option: %s\nRun "uninstall.sh --help" for the options.\n' "$1" >&2
        exit 2
        ;;
    esac
    shift
  done
  ensure_root "$@"
  umask 022
  if [ -d /run/systemd/system ]; then
    SYSTEMD=1
  fi

  if [ "$YES" != 1 ]; then
    if [ "$PURGE" = 1 ]; then
      log "WARNING: --purge-data deletes the database, the files, the secrets, and the backups on this host."
    fi
    if ! ask "Stop Care and remove its units and the agent? [y/N]"; then
      die "Stopped by the user. The script changed nothing."
    fi
    if [ "$PURGE" = 1 ]; then
      if ! ask "Delete ALL Care data in /etc/care-pi and /var/lib/care-pi? [y/N]"; then
        die "Stopped by the user. The script changed nothing."
      fi
      if ! ask_word "This cannot be undone. Type DELETE to delete the data:" DELETE; then
        die "Stopped by the user. The script changed nothing."
      fi
    fi
  fi

  stop_care
  remove_firewall
  remove_units
  remove_pod
  remove_hooks
  remove_binary
  if [ "$PURGE" = 1 ]; then
    purge_data
  else
    log "Kept /etc/care-pi and /var/lib/care-pi. Add --purge-data to delete them."
  fi
  log "Care is removed. The packages and the host settings from install.sh stay in place."
}

main "$@"
