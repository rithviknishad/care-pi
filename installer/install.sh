#!/bin/sh
# care-pi installer. It prepares a Raspberry Pi OS or Debian-family host for Care,
# installs the care-pi agent and its service (care-pi.service), and runs "care-pi bootstrap".
# By default bootstrap starts setup mode: Caddy and the console on port 8443. The
# operator claims the device in the console with the printed claim code.
#
#   curl -fsSL <URL> | sudo sh -s -- [options]
#   sudo sh install.sh [options]
#
# Run "sh install.sh --help" for the options.
#
# Shell choice: POSIX sh. Debian and Raspberry Pi OS use dash as sh. The script
# has no bash features and no arrays.
# The script is one file so that "curl | sh" works. The shell parses the whole
# file before "main" runs. This matters because a shell that reads a piped script
# from stdin would lose the rest of the script if a command read stdin.

set -eu

PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
export PATH
export DEBIAN_FRONTEND=noninteractive
export APT_LISTCHANGES_FRONTEND=none
export NEEDRESTART_MODE=a

REPO="rithviknishad/care-pi"
INSTALL_URL="https://raw.githubusercontent.com/$REPO/main/installer/install.sh"
RELEASES_URL="https://github.com/$REPO/releases"

LOG_FILE="/var/log/care-pi-install.log"
BIN_PATH="/usr/local/bin/care-pi"
CMDLINE_FILE="/boot/firmware/cmdline.txt"
CGROUP_CONTROLLERS="/sys/fs/cgroup/cgroup.controllers"
AVAHI_CONF="/etc/avahi/avahi-daemon.conf"
NFT_CONF="/etc/nftables.conf"
# An older install.sh wrote this file and added an include line to NFT_CONF.
NFT_LEGACY_FILE="/etc/nftables.d/care-pi.nft"
FIREWALL_FILE="/etc/care-pi/firewall.nft"
FIREWALL_UNIT="care-pi-firewall.service"
FIREWALL_UNIT_FILE="/etc/systemd/system/$FIREWALL_UNIT"
AGENT_UNIT="care-pi.service"
AGENT_UNIT_FILE="/etc/systemd/system/$AGENT_UNIT"
CONSOLE_PORT=8443
NFT_NOFLUSH_DIR="/run/systemd/system/nftables.service.d"
JOURNALD_DROPIN="/etc/systemd/journald.conf.d/50-care-pi.conf"
DISPATCHER_FILE="/etc/NetworkManager/dispatcher.d/90-care-pi"
ZRAM_GENERATOR_DROPIN_DIR="/etc/systemd/zram-generator.conf.d"
RPI_SWAP_DROPIN_DIR="/etc/rpi/swap.conf.d"
RC_FILE="/run/care-pi-install.$$.rc"

# Container and Podman packages that the script holds. See step_holds.
HOLD_PACKAGES="podman crun netavark aardvark-dns conmon skopeo"
# The oldest podman that care-pi supports. "care-pi host check" checks the same
# version. The .pod quadlets need podman 5.0. care-pi is tested with podman 5.4.2.
MIN_PODMAN=5.4
STEP_TOTAL=15

SELF=$0

# Option defaults. load_env and parse_args change them.
AGENT_VERSION="latest"
VERSION_EXPLICIT=0
HOSTNAME_ARG="care"
HOSTNAME_EXPLICIT=0
PRESET="small-clinic"
FE_PROVIDER="local"
ASSUME_YES=0
UNATTENDED=0
NO_FIREWALL=0
NO_AVAHI=0
NO_APT_HOLD=0
SKIP_BOOTSTRAP=0
BINARY=""
DRY_RUN=0

# Run state.
LOG_READY=0
FINISHED=0
CURRENT_STEP="start"
STEP_NO=0
STEP_CHANGED=0
STEP_SKIP=""
LAST_WRITE=0
REBOOT_NEEDED=0
REBOOT_REASONS=""
APT_UPDATED=0
WORK_DIR=""
AVAHI_SINCE=""
ADVERTISED_NAME=""
EFFECTIVE_HOSTNAME=""
PKG_LIST=""
OS_ID=""
OS_ID_LIKE=""
OS_VERSION=""
OS_PRETTY=""
ARCH=""
SYSTEMD=0
IS_PI=0
HOSTNAME_CHANGED=0
NFT_LEGACY_FOUND=0
AGENT_CHANGED=0

# ---------------------------------------------------------------- output

log_file_line() {
  if [ "$LOG_READY" = 1 ]; then
    printf '%s %s\n' "$(date '+%Y-%m-%dT%H:%M:%S%z')" "$*" >>"$LOG_FILE"
  fi
}

log() {
  printf '[care-pi] %s\n' "$*"
  log_file_line "$*"
}

warn() {
  printf '[care-pi] WARNING: %s\n' "$*" >&2
  log_file_line "WARNING: $*"
}

die() {
  printf '[care-pi] ERROR in step "%s": %s\n' "$CURRENT_STEP" "$*" >&2
  log_file_line "ERROR ($CURRENT_STEP): $*"
  exit 1
}

# In a dry run, report a problem as a warning and go on. In a real run, stop.
die_unless_dry() {
  if [ "$DRY_RUN" = 1 ]; then
    warn "$*"
  else
    die "$*"
  fi
}

plan() {
  log "dry-run: would $*"
}

usage_error() {
  FINISHED=1
  printf 'install.sh: %s\nRun "install.sh --help" for the options.\n' "$*" >&2
  exit 2
}

usage() {
  cat <<EOF
Usage: install.sh [options]

Prepare this host for Care. Install the care-pi agent and care-pi.service.
Run "care-pi bootstrap". By default bootstrap starts setup mode: open the
console at https://<host>:8443/ and enter the printed claim code.
Run the script as root. To run it from a pipe, use sudo and "sh -s --":

  curl -fsSL $INSTALL_URL | sudo sh -s -- [options]

Options:
  --version TAG      Agent release tag. Default: latest.
  --hostname NAME    Host name. The mDNS name is NAME.local. Default: care.
  --preset NAME      Preset for "care-pi bootstrap". Default: small-clinic.
  --fe-provider NAME FE provider for "care-pi bootstrap": local, artifact, or
                     none. Default: local. local builds care_fe on this host.
                     The first build takes about 3 minutes and needs internet.
                     none serves only the setup page.
  --unattended       Ask no questions. Imply --yes. Bootstrap applies everything
                     at once (no console setup), as in Phase 1. Use it for CI
                     tests and prepared hosts.
  --yes              Do not ask for confirmation.
  --no-firewall      Do not install the care-pi firewall (nftables table and
                     care-pi-firewall.service).
  --no-avahi         Do not install or configure avahi (mDNS).
  --no-apt-hold      Do not hold the container packages.
  --skip-bootstrap   Prepare the host, but do not run "care-pi bootstrap".
  --binary PATH      Install the agent from a local file. Do not download it.
  --dry-run          Print what the script would change. Change nothing.
  --help             Print this text.

Each option has an environment variable: CARE_PI_VERSION, CARE_PI_HOSTNAME,
CARE_PI_PRESET, CARE_PI_FE_PROVIDER, CARE_PI_UNATTENDED, CARE_PI_YES,
CARE_PI_NO_FIREWALL, CARE_PI_NO_AVAHI, CARE_PI_NO_APT_HOLD,
CARE_PI_SKIP_BOOTSTRAP, CARE_PI_BINARY, CARE_PI_DRY_RUN. A flag overrides its
variable. Switch variables accept 1, true, or yes.
CARE_PI_DOWNLOAD_BASE sets the URL that holds the agent files. It replaces the
GitHub release URL.

Example:
  curl -fsSL $INSTALL_URL | sudo CARE_PI_PRESET=solo sh -s -- --yes

Log file: $LOG_FILE
EOF
}

# ---------------------------------------------------------------- helpers

is_true() {
  case ${1:-} in
    1 | true | TRUE | yes | YES | on) return 0 ;;
    *) return 1 ;;
  esac
}

# version_ge A B succeeds when version A is the same as or newer than version B.
version_ge() {
  [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -n 1)" = "$2" ]
}

# run COMMAND...: log the command, run it, copy its output to the log file.
# The exit status of the command is the exit status of run.
run() {
  if [ "$DRY_RUN" = 1 ]; then
    plan "run: $*"
    return 0
  fi
  log_file_line "run: $*"
  if [ "$LOG_READY" != 1 ]; then
    "$@" </dev/null
    return
  fi
  {
    run_rc=0
    "$@" 2>&1 </dev/null || run_rc=$?
    echo "$run_rc" >"$RC_FILE"
  } | tee -a "$LOG_FILE"
  run_rc=$(cat "$RC_FILE" 2>/dev/null || echo 1)
  if [ "$run_rc" != 0 ]; then
    log_file_line "exit code $run_rc: $*"
  fi
  return "$run_rc"
}

apt_get() {
  run apt-get -y \
    -o DPkg::Lock::Timeout=300 \
    -o Dpkg::Options::=--force-confdef \
    -o Dpkg::Options::=--force-confold \
    "$@"
}

apt_update_once() {
  if [ "$APT_UPDATED" = 0 ]; then
    if ! apt_get update; then
      die "apt-get update failed. Check the network and the apt sources."
    fi
    APT_UPDATED=1
  fi
}

pkg_installed() {
  dpkg-query -W -f='${Status}' "$1" 2>/dev/null | grep -q ' ok installed$'
}

pkg_held() {
  apt-mark showhold 2>/dev/null | grep -qx "$1"
}

# write_file PATH MODE CONTENT: write CONTENT and a newline to PATH. Do nothing
# when PATH already has this content and mode. Set LAST_WRITE to 1 on a write.
write_file() {
  wf_path=$1
  wf_mode=$2
  wf_new="$wf_path.care-pi.new"
  LAST_WRITE=0
  if [ -f "$wf_path" ] \
    && printf '%s\n' "$3" | cmp -s - "$wf_path" \
    && [ "$(stat -c %a "$wf_path")" = "$wf_mode" ]; then
    return 0
  fi
  LAST_WRITE=1
  STEP_CHANGED=1
  if [ "$DRY_RUN" = 1 ]; then
    plan "write $wf_path (mode $wf_mode)"
    return 0
  fi
  (
    umask 077
    printf '%s\n' "$3" >"$wf_new"
  )
  chmod "$wf_mode" "$wf_new"
  mv -f "$wf_new" "$wf_path"
  log "wrote $wf_path"
}

# backup_once FILE: copy FILE to FILE.care-pi.bak if no backup exists yet.
backup_once() {
  bo_bak="$1.care-pi.bak"
  if [ -f "$1" ] && [ ! -e "$bo_bak" ]; then
    if [ "$DRY_RUN" = 1 ]; then
      plan "copy $1 to $bo_bak"
    else
      cp "$1" "$bo_bak"
      log "saved a copy of $1 as $bo_bak"
    fi
  fi
}

# ensure_dir PATH MODE OWNER:GROUP (numeric ids)
ensure_dir() {
  ed_have=""
  if [ -d "$1" ]; then
    ed_have=$(stat -c '%a %u:%g' "$1")
  fi
  if [ "$ed_have" = "$2 $3" ]; then
    return 0
  fi
  STEP_CHANGED=1
  run install -d -m "$2" -o "${3%%:*}" -g "${3##*:}" "$1"
}

# ensure_service UNIT: enable the unit and start it.
ensure_service() {
  if [ "$SYSTEMD" != 1 ]; then
    if [ "$DRY_RUN" = 1 ]; then
      STEP_CHANGED=1
      plan "enable and start $1"
    else
      warn "systemd is not running. The script cannot enable $1."
    fi
    return 0
  fi
  if ! systemctl is-enabled --quiet "$1" 2>/dev/null; then
    STEP_CHANGED=1
    run systemctl enable "$1"
  fi
  if ! systemctl is-active --quiet "$1"; then
    STEP_CHANGED=1
    run systemctl start "$1"
  fi
}

need_reboot() {
  REBOOT_NEEDED=1
  REBOOT_REASONS="${REBOOT_REASONS}${REBOOT_REASONS:+, }$1"
  warn "A reboot is required: $1."
}

memory_cgroup_active() {
  grep -qw memory "$CGROUP_CONTROLLERS" 2>/dev/null
}

# lan_ipv4s prints one global IPv4 address per line. It skips container,
# VPN, and loopback interfaces.
lan_ipv4s() {
  if ! command -v ip >/dev/null 2>&1; then
    return 0
  fi
  ip -4 -o addr show scope global 2>/dev/null | while read -r _ if_name _ if_addr _; do
    case $if_name in
      lo | podman* | veth* | tailscale* | docker* | br-* | cni* | virbr*) continue ;;
    esac
    printf '%s\n' "${if_addr%%/*}"
  done
}

# advertised_name prints the mDNS name that avahi announces, for example
# care.local. The name can differ from the host name after a name conflict.
advertised_name() {
  if [ "$SYSTEMD" != 1 ] || ! command -v journalctl >/dev/null 2>&1; then
    return 0
  fi
  if [ -n "$AVAHI_SINCE" ]; then
    set -- --since "$AVAHI_SINCE"
  else
    set -- -b
  fi
  journalctl -u avahi-daemon.service --no-pager -o cat "$@" 2>/dev/null | awk '
    /Host name is / {
      n = $0
      sub(/.*Host name is /, "", n)
      sub(/ .*/, "", n)
      sub(/\.$/, "", n)
      name = n
    }
    /Host name conflict, retrying with / {
      n = $0
      sub(/.*retrying with /, "", n)
      sub(/ .*/, "", n)
      name = n ".local"
    }
    END { if (name != "") print name }
  '
}

# ini_render FILE SECTION KEY VALUE prints FILE with KEY=VALUE set in [SECTION].
ini_render() {
  awk -v sec="$2" -v key="$3" -v val="$4" '
    { line[NR] = $0 }
    END {
      insec = 0; start = 0; active = 0; commented = 0
      for (i = 1; i <= NR; i++) {
        if (line[i] ~ /^[ \t]*\[/) {
          insec = (line[i] ~ ("^[ \t]*\\[" sec "\\][ \t]*$"))
          if (insec && !start) start = i
          continue
        }
        if (!insec) continue
        if (line[i] ~ ("^[ \t]*" key "[ \t]*=")) {
          if (!active) active = i; else drop[i] = 1
        } else if (!commented && line[i] ~ ("^[ \t]*[#;][ \t]*" key "[ \t]*=")) {
          commented = i
        }
      }
      target = active ? active : (commented ? commented : start)
      for (i = 1; i <= NR; i++) {
        if (drop[i]) continue
        if (i == active) { print key "=" val; continue }
        print line[i]
        if (!active && i == target) print key "=" val
      }
      if (!active && !start) { print "[" sec "]"; print key "=" val }
    }
  ' "$1"
}

# ask PROMPT reads a yes or no answer from the terminal. It also works when the
# script comes from a pipe.
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

# ---------------------------------------------------------------- options

load_env() {
  if [ -n "${CARE_PI_VERSION:-}" ]; then
    AGENT_VERSION=$CARE_PI_VERSION
    VERSION_EXPLICIT=1
  fi
  if [ -n "${CARE_PI_HOSTNAME:-}" ]; then
    HOSTNAME_ARG=$CARE_PI_HOSTNAME
    HOSTNAME_EXPLICIT=1
  fi
  if [ -n "${CARE_PI_PRESET:-}" ]; then
    PRESET=$CARE_PI_PRESET
  fi
  if [ -n "${CARE_PI_FE_PROVIDER:-}" ]; then
    FE_PROVIDER=$CARE_PI_FE_PROVIDER
  fi
  if [ -n "${CARE_PI_BINARY:-}" ]; then
    BINARY=$CARE_PI_BINARY
  fi
  if is_true "${CARE_PI_UNATTENDED:-}"; then UNATTENDED=1; fi
  if is_true "${CARE_PI_YES:-}"; then ASSUME_YES=1; fi
  if is_true "${CARE_PI_NO_FIREWALL:-}"; then NO_FIREWALL=1; fi
  if is_true "${CARE_PI_NO_AVAHI:-}"; then NO_AVAHI=1; fi
  if is_true "${CARE_PI_NO_APT_HOLD:-}"; then NO_APT_HOLD=1; fi
  if is_true "${CARE_PI_SKIP_BOOTSTRAP:-}"; then SKIP_BOOTSTRAP=1; fi
  if is_true "${CARE_PI_DRY_RUN:-}"; then DRY_RUN=1; fi
}

# take_value reads the value of an option. It uses the globals that parse_args
# sets: opt, has_val, val. It sets OPT_VALUE and OPT_SHIFT.
take_value() {
  if [ "$has_val" = 1 ]; then
    OPT_VALUE=$val
    OPT_SHIFT=1
  else
    if [ $# -lt 2 ]; then
      usage_error "The option $opt needs a value."
    fi
    OPT_VALUE=$2
    OPT_SHIFT=2
  fi
}

parse_args() {
  while [ $# -gt 0 ]; do
    has_val=0
    opt=$1
    val=""
    case $1 in
      --*=*)
        has_val=1
        opt=${1%%=*}
        val=${1#*=}
        ;;
    esac
    case $opt in
      --version)
        take_value "$@"
        AGENT_VERSION=$OPT_VALUE
        VERSION_EXPLICIT=1
        shift "$OPT_SHIFT"
        ;;
      --hostname)
        take_value "$@"
        HOSTNAME_ARG=$OPT_VALUE
        HOSTNAME_EXPLICIT=1
        shift "$OPT_SHIFT"
        ;;
      --preset)
        take_value "$@"
        PRESET=$OPT_VALUE
        shift "$OPT_SHIFT"
        ;;
      --fe-provider)
        take_value "$@"
        FE_PROVIDER=$OPT_VALUE
        shift "$OPT_SHIFT"
        ;;
      --binary)
        take_value "$@"
        BINARY=$OPT_VALUE
        shift "$OPT_SHIFT"
        ;;
      --unattended)
        UNATTENDED=1
        ASSUME_YES=1
        shift
        ;;
      --yes)
        ASSUME_YES=1
        shift
        ;;
      --no-firewall)
        NO_FIREWALL=1
        shift
        ;;
      --no-avahi)
        NO_AVAHI=1
        shift
        ;;
      --no-apt-hold)
        NO_APT_HOLD=1
        shift
        ;;
      --skip-bootstrap)
        SKIP_BOOTSTRAP=1
        shift
        ;;
      --dry-run)
        DRY_RUN=1
        shift
        ;;
      --help | -h)
        usage
        FINISHED=1
        exit 0
        ;;
      *)
        usage_error "Unknown option: $1"
        ;;
    esac
  done
  if [ "$UNATTENDED" = 1 ]; then
    ASSUME_YES=1
  fi
  validate_options
}

validate_options() {
  case $HOSTNAME_ARG in
    "" | -* | *- | *[!a-z0-9-]*)
      usage_error "Invalid host name: $HOSTNAME_ARG. Use 1 to 63 lowercase letters, digits, or hyphens. Do not add .local."
      ;;
  esac
  if [ "${#HOSTNAME_ARG}" -gt 63 ]; then
    usage_error "Invalid host name: it has more than 63 characters."
  fi
  case $PRESET in
    "" | -* | *[!a-z0-9-]*)
      usage_error "Invalid preset: $PRESET. Use lowercase letters, digits, or hyphens."
      ;;
  esac
  case $FE_PROVIDER in
    local | artifact | none) ;;
    *)
      usage_error "Invalid FE provider: $FE_PROVIDER. Use local, artifact, or none."
      ;;
  esac
  case $AGENT_VERSION in
    "" | *[!A-Za-z0-9._+-]*)
      usage_error "Invalid version: $AGENT_VERSION."
      ;;
  esac
  if [ -n "$BINARY" ] && [ "$VERSION_EXPLICIT" = 1 ]; then
    usage_error "Use either --binary or --version. Do not use both."
  fi
  if [ -n "$BINARY" ] && [ ! -f "$BINARY" ]; then
    usage_error "The file $BINARY does not exist."
  fi
}

# ensure_root re-runs the script with sudo when the user is not root. This works
# only when the script is a file. A script from a pipe cannot run again.
ensure_root() {
  if [ "$(id -u)" = 0 ]; then
    return 0
  fi
  case $SELF in
    sh | -sh | dash | -dash | bash | -bash | */sh | */dash | */bash)
      piped=1
      ;;
    *)
      piped=0
      ;;
  esac
  if [ "$piped" = 1 ]; then
    printf '[care-pi] ERROR: The script must run as root, and it cannot start sudo from a pipe.\n' >&2
    printf '[care-pi] Run this command:\n  curl -fsSL %s | sudo sh -s -- [options]\n' "$INSTALL_URL" >&2
    FINISHED=1
    exit 1
  fi
  if ! command -v sudo >/dev/null 2>&1; then
    printf '[care-pi] ERROR: The script must run as root. sudo is not installed.\n' >&2
    printf '[care-pi] Become root (for example with "su -"), then run the script again.\n' >&2
    FINISHED=1
    exit 1
  fi
  printf '[care-pi] The script is not root. Start it again with sudo.\n'
  set --
  if [ "$VERSION_EXPLICIT" = 1 ]; then set -- "$@" --version "$AGENT_VERSION"; fi
  if [ "$HOSTNAME_EXPLICIT" = 1 ]; then set -- "$@" --hostname "$HOSTNAME_ARG"; fi
  set -- "$@" --preset "$PRESET" --fe-provider "$FE_PROVIDER"
  if [ -n "$BINARY" ]; then set -- "$@" --binary "$BINARY"; fi
  if [ "$UNATTENDED" = 1 ]; then set -- "$@" --unattended; fi
  if [ "$ASSUME_YES" = 1 ]; then set -- "$@" --yes; fi
  if [ "$NO_FIREWALL" = 1 ]; then set -- "$@" --no-firewall; fi
  if [ "$NO_AVAHI" = 1 ]; then set -- "$@" --no-avahi; fi
  if [ "$NO_APT_HOLD" = 1 ]; then set -- "$@" --no-apt-hold; fi
  if [ "$SKIP_BOOTSTRAP" = 1 ]; then set -- "$@" --skip-bootstrap; fi
  if [ "$DRY_RUN" = 1 ]; then set -- "$@" --dry-run; fi
  FINISHED=1
  exec sudo env \
    "CARE_PI_DOWNLOAD_BASE=${CARE_PI_DOWNLOAD_BASE:-}" \
    "CARE_PI_ALLOW_NO_SYSTEMD=${CARE_PI_ALLOW_NO_SYSTEMD:-}" \
    sh "$SELF" "$@"
}

setup_logging() {
  if [ "$DRY_RUN" = 1 ]; then
    return 0
  fi
  if ( : >>"$LOG_FILE" ) 2>/dev/null; then
    LOG_READY=1
    chmod 0640 "$LOG_FILE"
    log_file_line "---- install.sh starts ----"
  else
    warn "Cannot write the log file $LOG_FILE. The script goes on without it."
  fi
}

# ---------------------------------------------------------------- detection

detect_os() {
  if [ ! -r /etc/os-release ]; then
    die "Cannot read /etc/os-release. This host is not supported."
  fi
  # shellcheck source=/dev/null
  OS_ID=$(. /etc/os-release && printf '%s' "${ID:-}")
  # shellcheck source=/dev/null
  OS_ID_LIKE=$(. /etc/os-release && printf '%s' "${ID_LIKE:-}")
  # shellcheck source=/dev/null
  OS_VERSION=$(. /etc/os-release && printf '%s' "${VERSION_ID:-}")
  # shellcheck source=/dev/null
  OS_PRETTY=$(. /etc/os-release && printf '%s' "${PRETTY_NAME:-$OS_ID}")

  case $OS_ID in
    fedora | rhel | centos | rocky | almalinux)
      die "$OS_PRETTY is not supported in this version. This version supports Debian-family hosts only. Fedora support is planned (plans/06-roadmap.md, Phase 7)."
      ;;
    debian | raspbian)
      if [ -n "$OS_VERSION" ] && ! version_ge "$OS_VERSION" 12; then
        die "$OS_PRETTY is too old. Use Raspberry Pi OS (trixie) or Debian 13."
      fi
      if [ -z "$OS_VERSION" ]; then
        warn "The Debian version is not known (Debian testing or unstable?). The script goes on, and the podman check decides."
      elif version_ge "$OS_VERSION" 14; then
        warn "Debian $OS_VERSION is newer than the tested version 13."
      fi
      ;;
    ubuntu)
      if ! version_ge "$OS_VERSION" 25.04; then
        die "Ubuntu $OS_VERSION is not supported in this version. Care needs podman 5.4 or newer. Ubuntu 24.04 ships podman 4.9, and Ubuntu 22.04 ships podman 3.4. Debian 13 (trixie) ships podman 5.4. Use Raspberry Pi OS (trixie) or Debian 13."
      fi
      warn "$OS_PRETTY is not tested. The podman check decides."
      ;;
    *)
      case " $OS_ID_LIKE " in
        *" debian "* | *" ubuntu "*)
          warn "$OS_PRETTY is a Debian-family derivative. It is not tested. The podman check decides."
          ;;
        *)
          die "$OS_PRETTY is not supported. Supported hosts: Raspberry Pi OS (trixie) and Debian 13 (trixie). Care needs podman 5.4 or newer."
          ;;
      esac
      ;;
  esac
  if ! command -v apt-get >/dev/null 2>&1 || ! command -v dpkg >/dev/null 2>&1; then
    die "apt-get and dpkg are required. This host is not Debian-family."
  fi
}

detect_arch() {
  case $(uname -m) in
    aarch64 | arm64) ARCH="arm64" ;;
    x86_64 | amd64) ARCH="amd64" ;;
    *)
      die "The CPU architecture $(uname -m) is not supported. Use a 64-bit Raspberry Pi OS (arm64) or an amd64 host."
      ;;
  esac
  if [ "$(dpkg --print-architecture)" != "$ARCH" ]; then
    die "The kernel is $ARCH, but the user space is $(dpkg --print-architecture). Install a 64-bit operating system."
  fi
}

detect_systemd() {
  if [ -d /run/systemd/system ]; then
    SYSTEMD=1
  elif [ "$DRY_RUN" = 1 ]; then
    warn "systemd not running. The dry run goes on and prints the plan. A real run stops here."
  elif is_true "${CARE_PI_ALLOW_NO_SYSTEMD:-}"; then
    warn "systemd not running. CARE_PI_ALLOW_NO_SYSTEMD is set (test only). The script skips all systemctl calls."
  else
    die "systemd not running. Care needs systemd as PID 1. Use a host with systemd, not a plain container."
  fi
}

detect_cgroup() {
  if [ ! -r "$CGROUP_CONTROLLERS" ]; then
    die_unless_dry "cgroup v2 is not active ($CGROUP_CONTROLLERS does not exist). Care needs cgroup v2."
  fi
}

detect_pi() {
  if [ -r /proc/device-tree/model ] && grep -aq 'Raspberry Pi' /proc/device-tree/model; then
    IS_PI=1
  elif [ -f "$CMDLINE_FILE" ]; then
    IS_PI=1
  fi
}

detect_host() {
  CURRENT_STEP="detect the host"
  log "Detect the host."
  detect_os
  detect_arch
  detect_systemd
  detect_cgroup
  detect_pi
  EFFECTIVE_HOSTNAME=$(cat /proc/sys/kernel/hostname)
  if [ "$IS_PI" = 1 ]; then
    pi_text="yes"
  else
    pi_text="no"
  fi
  log "Host: $OS_PRETTY, $ARCH, Raspberry Pi: $pi_text, systemd: $SYSTEMD"
}

other_ntp_installed() {
  for ont_pkg in chrony ntpsec ntp openntpd; do
    if pkg_installed "$ont_pkg"; then
      printf '%s' "$ont_pkg"
      return 0
    fi
  done
  return 1
}

build_package_list() {
  PKG_LIST="podman skopeo restic nftables unattended-upgrades ca-certificates curl uidmap catatonit iproute2"
  if [ "$NO_AVAHI" != 1 ]; then
    PKG_LIST="$PKG_LIST avahi-daemon"
  fi
  if ! other_ntp_installed >/dev/null; then
    PKG_LIST="$PKG_LIST systemd-timesyncd"
  fi
}

confirm_plan() {
  if [ "$ASSUME_YES" = 1 ] || [ "$DRY_RUN" = 1 ]; then
    return 0
  fi
  CURRENT_STEP="confirm the plan"
  log "The script will change this host:"
  log "  - install packages: $PKG_LIST"
  log "  - set kernel, swap, mDNS, time, journal, firewall, and update settings"
  log "  - create /etc/care-pi and /var/lib/care-pi"
  if [ -n "$BINARY" ]; then
    log "  - install the agent from $BINARY"
  else
    log "  - download the agent ($AGENT_VERSION) and verify its checksum"
  fi
  log "  - install and start care-pi.service (the console on port $CONSOLE_PORT)"
  if [ "$UNATTENDED" = 1 ]; then
    log "  - run care-pi bootstrap --unattended (host name $HOSTNAME_ARG, preset $PRESET, FE provider $FE_PROVIDER)"
  else
    log "  - run care-pi bootstrap in setup mode (host name $HOSTNAME_ARG); print the claim code"
  fi
  if ! ask "Continue? [y/N]"; then
    die "Stopped by the user. The script changed nothing."
  fi
}

# ---------------------------------------------------------------- steps

step() {
  STEP_NO=$((STEP_NO + 1))
  CURRENT_STEP=$1
  STEP_CHANGED=0
  STEP_SKIP=""
  log "[$STEP_NO/$STEP_TOTAL] $1"
  "$2"
  if [ -n "$STEP_SKIP" ]; then
    log "  skipped: $STEP_SKIP"
  elif [ "$STEP_CHANGED" = 1 ]; then
    if [ "$DRY_RUN" = 1 ]; then
      log "  changes planned"
    else
      log "  done"
    fi
  else
    log "  already configured"
  fi
}

# --- 1. packages

podman_too_old() {
  die "podman $1 ($2) is too old. Care needs podman $MIN_PODMAN or newer. Debian 13 (trixie) and Raspberry Pi OS (trixie) ship podman 5.4. Ubuntu 24.04 ships podman 4.9 and is not supported in this version. This script adds no third-party repositories (plans/06-roadmap.md, Phase 7)."
}

podman_version() {
  podman --version 2>/dev/null | sed -n 's/^podman version \([0-9][0-9.]*\).*/\1/p'
}

# Stop before the install when apt offers only an old podman.
check_podman_candidate() {
  pc_ver=$(apt-cache policy podman 2>/dev/null | sed -n 's/^ *Candidate: \([0-9][0-9.]*\).*/\1/p')
  if [ -z "$pc_ver" ]; then
    die "The apt sources have no podman package."
  fi
  if ! version_ge "$pc_ver" "$MIN_PODMAN"; then
    podman_too_old "$pc_ver" "the version in the apt sources"
  fi
}

# Stop before the install when the installed podman is too old.
# The script installs only the missing packages, so it does not update podman.
check_podman_installed() {
  if ! command -v podman >/dev/null 2>&1; then
    return 0
  fi
  ci_ver=$(podman_version)
  if [ -n "$ci_ver" ] && ! version_ge "$ci_ver" "$MIN_PODMAN"; then
    podman_too_old "$ci_ver" "the installed version"
  fi
}

check_podman() {
  if ! command -v podman >/dev/null 2>&1; then
    if [ "$DRY_RUN" = 1 ]; then
      log "dry-run: the podman version check runs after the install."
      return 0
    fi
    die "podman is not installed."
  fi
  cp_ver=$(podman_version)
  if [ -z "$cp_ver" ]; then
    die "Cannot read the podman version from 'podman --version'."
  fi
  if ! version_ge "$cp_ver" "$MIN_PODMAN"; then
    podman_too_old "$cp_ver" "the installed version"
  fi
  if [ ! -x /usr/libexec/podman/quadlet ] && [ ! -x /usr/lib/podman/quadlet ]; then
    die "podman $cp_ver has no quadlet. Looked for /usr/libexec/podman/quadlet and /usr/lib/podman/quadlet."
  fi
  log "podman $cp_ver with quadlet is ready."
}

step_packages() {
  check_podman_installed
  set --
  for sp_pkg in $PKG_LIST; do
    if ! pkg_installed "$sp_pkg"; then
      set -- "$@" "$sp_pkg"
    fi
  done
  if [ $# -gt 0 ]; then
    STEP_CHANGED=1
    log "Install: $*"
    apt_update_once
    if [ "$DRY_RUN" != 1 ] && ! pkg_installed podman; then
      check_podman_candidate
    fi
    apt_get install "$@" || die "apt-get install failed. Read the output above."
  fi
  check_podman
}

# --- 2. memory cgroup

# cmdline_with_memory_cgroup LINE prints LINE with cgroup_disable=memory removed
# and with "cgroup_enable=memory cgroup_memory=1" present once.
cmdline_with_memory_cgroup() {
  cm_out=""
  cm_enable=0
  cm_memory=0
  set -f
  for cm_tok in $1; do
    case $cm_tok in
      cgroup_disable=*)
        cm_rest=""
        cm_oldifs=$IFS
        IFS=,
        # shellcheck disable=SC2086
        for cm_item in ${cm_tok#cgroup_disable=}; do
          if [ "$cm_item" != memory ]; then
            cm_rest="${cm_rest:+$cm_rest,}$cm_item"
          fi
        done
        IFS=$cm_oldifs
        if [ -n "$cm_rest" ]; then
          cm_out="$cm_out cgroup_disable=$cm_rest"
        fi
        ;;
      cgroup_enable=memory)
        if [ "$cm_enable" = 0 ]; then
          cm_out="$cm_out $cm_tok"
          cm_enable=1
        fi
        ;;
      cgroup_memory=*)
        if [ "$cm_tok" = cgroup_memory=1 ] && [ "$cm_memory" = 0 ]; then
          cm_out="$cm_out $cm_tok"
          cm_memory=1
        fi
        ;;
      *)
        cm_out="$cm_out $cm_tok"
        ;;
    esac
  done
  set +f
  if [ "$cm_enable" = 0 ]; then
    cm_out="$cm_out cgroup_enable=memory"
  fi
  if [ "$cm_memory" = 0 ]; then
    cm_out="$cm_out cgroup_memory=1"
  fi
  printf '%s' "${cm_out# }"
}

step_memory_cgroup() {
  if [ "$IS_PI" = 1 ] && [ ! -f "$CMDLINE_FILE" ]; then
    warn "$CMDLINE_FILE not found. The script checks the cgroup controller only."
  fi
  if [ "$IS_PI" != 1 ] || [ ! -f "$CMDLINE_FILE" ]; then
    if memory_cgroup_active; then
      log "The memory cgroup controller is active."
    else
      die_unless_dry "The memory cgroup controller is not active. Add cgroup_enable=memory to the kernel command line, then reboot. Without it, Care cannot enforce memory limits."
    fi
    return 0
  fi

  if [ "$(grep -c . "$CMDLINE_FILE")" -gt 1 ]; then
    die "$CMDLINE_FILE has more than one line. The script does not edit it. Put cgroup_enable=memory cgroup_memory=1 on the one kernel line, remove cgroup_disable=memory, and run the script again."
  fi
  sm_old=$(head -n 1 "$CMDLINE_FILE" | tr -d '\r')
  if [ -z "$sm_old" ]; then
    die "$CMDLINE_FILE is empty. The script does not edit it."
  fi
  sm_new=$(cmdline_with_memory_cgroup "$sm_old")
  if [ "$sm_old" != "$sm_new" ]; then
    STEP_CHANGED=1
    backup_once "$CMDLINE_FILE"
    if [ "$DRY_RUN" = 1 ]; then
      plan "set $CMDLINE_FILE to: $sm_new"
    else
      printf '%s\n' "$sm_new" >"$CMDLINE_FILE.care-pi.new"
      mv -f "$CMDLINE_FILE.care-pi.new" "$CMDLINE_FILE"
      sync
      log "Updated $CMDLINE_FILE."
    fi
  fi
  if memory_cgroup_active; then
    log "The memory cgroup controller is active."
  elif [ "$DRY_RUN" = 1 ]; then
    log "dry-run: the memory cgroup controller is not active. A reboot is needed after a real run."
    REBOOT_NEEDED=1
    REBOOT_REASONS="${REBOOT_REASONS}${REBOOT_REASONS:+, }memory cgroup"
  else
    need_reboot "the memory cgroup controller starts at the next boot"
  fi
}

# --- 3. zram

zram_swap_kib() {
  awk '$1 ~ /^\/dev\/zram/ { s += $3 } END { print s + 0 }' /proc/swaps
}

mem_total_kib() {
  awk '/^MemTotal:/ { print $2 }' /proc/meminfo
}

zram_ok() {
  [ "$(($(zram_swap_kib) * 100))" -ge "$(($(mem_total_kib) * 90))" ]
}

# The rpi-swap package (Raspberry Pi OS trixie) reads /etc/rpi/swap.conf.d/*.conf.
# Source: swap.conf(5) of rpi-swap 1.2.4. The default zram size is 1x RAM with a
# limit of 2048 MiB. The drop-in sets a high limit, so a Pi with more than 2 GB gets
# 1x RAM too. Do not use "MaxSizeMiB=" (empty): rpi-systemd-config drops an empty
# value, and rpi-desired-zram-size then uses the 2048 MiB limit (wave 3 on lumine).
# rpi-swap reads its settings at boot only. A change needs a reboot.
rpi_swap_conf() {
  cat <<'EOF'
# Managed by care-pi install.sh.
# The Care front-end build needs zram swap of 1x RAM. RamMultiplier=1 sets the size.
# MaxSizeMiB replaces the default limit of 2048 MiB with a limit above any Pi RAM.
[Main]
Mechanism=zram+file

[Zram]
RamMultiplier=1
MaxSizeMiB=65536
EOF
}

zram_generator_conf() {
  cat <<'EOF'
# Managed by care-pi install.sh.
# The Care front-end build needs zram swap of 1x RAM.
[zram0]
zram-size = ram
compression-algorithm = zstd
EOF
}

zram_with_rpi_swap() {
  ensure_dir "$RPI_SWAP_DROPIN_DIR" 755 0:0
  write_file "$RPI_SWAP_DROPIN_DIR/50-care-pi.conf" 644 "$(rpi_swap_conf)"
}

zram_with_generator() {
  if ! pkg_installed systemd-zram-generator; then
    STEP_CHANGED=1
    apt_update_once
    apt_get install systemd-zram-generator || die "Cannot install systemd-zram-generator."
  fi
  # A drop-in overrides the main file. Ubuntu ships a main file with a default [zram0].
  ensure_dir "$ZRAM_GENERATOR_DROPIN_DIR" 755 0:0
  write_file "$ZRAM_GENERATOR_DROPIN_DIR/50-care-pi.conf" 644 "$(zram_generator_conf)"
  if [ "$LAST_WRITE" = 1 ] && { [ "$SYSTEMD" = 1 ] || [ "$DRY_RUN" = 1 ]; }; then
    run systemctl daemon-reload
    if ! run systemctl start systemd-zram-setup@zram0.service; then
      warn "systemd-zram-setup@zram0.service did not start."
    fi
    if ! run systemctl start dev-zram0.swap; then
      warn "dev-zram0.swap did not start."
    fi
  fi
}

step_zram() {
  if zram_ok; then
    log "zram swap is active: $(($(zram_swap_kib) / 1024)) MiB."
    return 0
  fi
  if [ "$IS_PI" = 1 ] && pkg_installed rpi-swap; then
    zram_with_rpi_swap
  else
    zram_with_generator
  fi
  if [ "$DRY_RUN" = 1 ]; then
    return 0
  fi
  if zram_ok; then
    log "zram swap is active: $(($(zram_swap_kib) / 1024)) MiB."
  elif [ "$SYSTEMD" != 1 ]; then
    warn "zram swap is not active. There is no systemd, so the script cannot start it."
  elif [ "$STEP_CHANGED" = 1 ]; then
    need_reboot "zram swap starts at the next boot"
  else
    die "zram swap is not active, but its configuration exists. Run 'systemctl status systemd-zram-setup@zram0.service' and 'dmesg | grep -i zram'. Care needs zram swap of 1x RAM."
  fi
}

# --- 4. host name

hosts_with_name() {
  awk -v name="$1" '
    $1 == "127.0.1.1" { if (!done) { print "127.0.1.1\t" name; done = 1 } next }
    { print }
    END { if (!done) print "127.0.1.1\t" name }
  ' /etc/hosts
}

step_hostname() {
  sh_current=$(cat /etc/hostname 2>/dev/null || cat /proc/sys/kernel/hostname)
  sh_target=$sh_current
  if [ "$HOSTNAME_EXPLICIT" = 1 ]; then
    sh_target=$HOSTNAME_ARG
  elif [ "$sh_current" = raspberrypi ] && [ "$NO_AVAHI" != 1 ]; then
    sh_target=$HOSTNAME_ARG
  fi
  EFFECTIVE_HOSTNAME=$sh_target
  if [ "$sh_target" = "$sh_current" ]; then
    log "Keep the host name '$sh_current'."
    return 0
  fi
  if [ "$SYSTEMD" != 1 ] && [ "$DRY_RUN" != 1 ]; then
    warn "systemd is not running. The script cannot change the host name."
    EFFECTIVE_HOSTNAME=$sh_current
    return 0
  fi
  STEP_CHANGED=1
  HOSTNAME_CHANGED=1
  run hostnamectl set-hostname "$sh_target"
  if [ -f /etc/hosts ]; then
    write_file /etc/hosts 644 "$(hosts_with_name "$sh_target")"
  fi
  log "Host name: '$sh_current' -> '$sh_target'."
}

# --- 5. avahi

# avahi_interfaces prints the interface list for allow-interfaces. It always
# has eth0 and wlan0. It adds other physical interfaces (such as enp3s0 on a PC).
avahi_interfaces() {
  ai_list="eth0 wlan0"
  for ai_dev in /sys/class/net/*; do
    if [ ! -e "$ai_dev/device" ]; then
      continue
    fi
    ai_name=${ai_dev##*/}
    case " $ai_list " in
      *" $ai_name "*) continue ;;
    esac
    ai_list="$ai_list $ai_name"
  done
  printf '%s' "$ai_list" | tr ' ' ','
}

report_network() {
  rn_name=$(advertised_name)
  ADVERTISED_NAME=$rn_name
  if [ -n "$rn_name" ]; then
    log "mDNS name: $rn_name"
    if [ "$rn_name" != "$EFFECTIVE_HOSTNAME.local" ]; then
      warn "avahi announces '$rn_name', not '$EFFECTIVE_HOSTNAME.local'. Another device on the LAN has the same name. Use the announced name or the IP address."
    fi
  fi
  rn_ips=$(lan_ipv4s | tr '\n' ' ')
  log "LAN IPv4 addresses: ${rn_ips:-none found}"
}

step_avahi() {
  if [ "$NO_AVAHI" = 1 ]; then
    STEP_SKIP="--no-avahi"
    return 0
  fi
  if [ ! -f "$AVAHI_CONF" ]; then
    if [ "$DRY_RUN" = 1 ]; then
      STEP_CHANGED=1
      plan "set allow-interfaces=$(avahi_interfaces) and use-ipv4=yes in $AVAHI_CONF after the package install"
      plan "enable and restart avahi-daemon"
      return 0
    fi
    die "$AVAHI_CONF not found. Is avahi-daemon installed?"
  fi
  sa_mode=$(stat -c %a "$AVAHI_CONF")
  sa_text=$(ini_render "$AVAHI_CONF" server allow-interfaces "$(avahi_interfaces)")
  sa_text=$(printf '%s\n' "$sa_text" | ini_render - server use-ipv4 yes)
  write_file "$AVAHI_CONF" "$sa_mode" "$sa_text"
  sa_changed=$LAST_WRITE
  if [ "$DRY_RUN" = 1 ]; then
    plan "enable avahi-daemon, and restart it after a change"
    return 0
  fi
  if [ "$SYSTEMD" != 1 ]; then
    warn "systemd is not running. The script cannot restart avahi-daemon."
    return 0
  fi
  ensure_service avahi-daemon.service
  if [ "$sa_changed" = 1 ] || [ "$HOSTNAME_CHANGED" = 1 ]; then
    STEP_CHANGED=1
    AVAHI_SINCE=$(date '+%Y-%m-%d %H:%M:%S')
    run systemctl restart avahi-daemon.service
  fi
  sa_wait=0
  while command -v journalctl >/dev/null 2>&1 && [ -z "$(advertised_name)" ] && [ "$sa_wait" -lt 15 ]; do
    sleep 1
    sa_wait=$((sa_wait + 1))
  done
  report_network
}

# --- 6. time

step_time() {
  if sn_other=$(other_ntp_installed); then
    STEP_SKIP="$sn_other is installed and keeps the clock in sync"
    return 0
  fi
  ensure_service systemd-timesyncd.service
  if [ "$SYSTEMD" = 1 ]; then
    if [ "$(timedatectl show -p NTP --value 2>/dev/null)" != yes ]; then
      STEP_CHANGED=1
      run timedatectl set-ntp true
    fi
  elif [ "$DRY_RUN" = 1 ]; then
    STEP_CHANGED=1
    plan "run: timedatectl set-ntp true"
  fi
}

# --- 7. journal

journald_conf() {
  cat <<'EOF'
# Managed by care-pi install.sh. Keep the journal on disk with a size limit, so
# that "care-pi logs" also shows the previous boots. Raspberry Pi OS keeps the
# journal in RAM only (Storage=volatile in
# /usr/lib/systemd/journald.conf.d/40-rpi-volatile-storage.conf).
[Journal]
Storage=persistent
SystemMaxUse=200M
EOF
}

journald_installed() {
  [ -x /usr/lib/systemd/systemd-journald ] || [ -x /lib/systemd/systemd-journald ]
}

# journal_on_disk succeeds when journald writes the system journal to /var/log/journal.
journal_on_disk() {
  jd_id=$(cat /etc/machine-id 2>/dev/null || true)
  [ -n "$jd_id" ] && [ -f "/var/log/journal/$jd_id/system.journal" ]
}

step_journal() {
  if ! journald_installed; then
    if [ "$DRY_RUN" = 1 ]; then
      STEP_CHANGED=1
      plan "write $JOURNALD_DROPIN (Storage=persistent, SystemMaxUse=200M) and create /var/log/journal when systemd-journald exists"
      return 0
    fi
    STEP_SKIP="systemd-journald is not installed"
    return 0
  fi
  ensure_dir /etc/systemd/journald.conf.d 755 0:0
  write_file "$JOURNALD_DROPIN" 644 "$(journald_conf)"
  sj_changed=$LAST_WRITE
  sj_have=""
  if [ -d /var/log/journal ]; then
    sj_have=$(stat -c '%a %U:%G' /var/log/journal)
  fi
  if [ "$sj_have" != "2755 root:systemd-journal" ]; then
    STEP_CHANGED=1
    sj_changed=1
    run install -d -m 2755 -o root -g systemd-journal /var/log/journal
  fi
  if [ "$SYSTEMD" != 1 ]; then
    if [ "$DRY_RUN" = 1 ]; then
      STEP_CHANGED=1
      plan "run: systemd-tmpfiles --create --prefix /var/log/journal"
      plan "run: systemctl restart systemd-journald.service"
    else
      warn "systemd is not running. The script cannot restart systemd-journald."
    fi
    return 0
  fi
  if [ "$sj_changed" = 1 ] || ! journal_on_disk; then
    STEP_CHANGED=1
    run systemd-tmpfiles --create --prefix /var/log/journal
    run systemctl restart systemd-journald.service
    # Move the journal of this boot from /run/log/journal to the disk.
    run journalctl --flush
  fi
  if [ "$DRY_RUN" != 1 ]; then
    log "Journal: $(journalctl --disk-usage 2>/dev/null || echo 'size unknown')"
  fi
}

# --- 8. firewall

sshd_active() {
  [ "$SYSTEMD" = 1 ] || return 1
  for sa_unit in ssh.service sshd.service ssh.socket; do
    if systemctl is-active --quiet "$sa_unit" 2>/dev/null; then
      return 0
    fi
  done
  return 1
}

nft_rules() {
  nr_ssh=""
  if [ "$1" = 1 ]; then
    nr_ssh="
    tcp dport 22 accept"
  fi
  cat <<EOF
# Managed by care-pi install.sh. Do not edit. $FIREWALL_UNIT loads this file.
# The chain accepts the Care ports (80, 443) and the console (8443, S48). It drops nothing. The policy is
# "accept". The default-drop policy comes in Phase 7 (hardening). Allow DHCP
# (udp 68) before that change.
# The file changes only the table inet care-pi. It does not touch the tables of
# podman (netavark) or Tailscale.
# The first two commands make "nft -f" idempotent: create the table when it is
# missing, then delete it. The last block creates it again with the rules.
table inet care-pi
delete table inet care-pi

table inet care-pi {
  chain input {
    type filter hook input priority filter; policy accept;
    iifname "lo" accept
    ct state established,related accept
    ip protocol icmp accept
    meta l4proto ipv6-icmp accept$nr_ssh
    tcp dport { 80, 443, $CONSOLE_PORT } accept
    udp dport 5353 accept
  }
}
EOF
}

# firewall_unit prints the unit that loads FIREWALL_FILE. It uses the order of
# the nftables.service of Debian: before network-pre.target (systemd.special(7)).
# Unlike nftables.service, its stop deletes only the table inet care-pi. It
# never runs "nft flush ruleset".
firewall_unit() {
  cat <<EOF
# Managed by care-pi install.sh. Do not edit.
# Load the care-pi firewall table. This unit changes only the table inet care-pi.
# It does not flush the ruleset, so the rules of podman (netavark) and Tailscale stay.
[Unit]
Description=care-pi firewall (nftables table inet care-pi)
Documentation=https://github.com/rithviknishad/care-pi
DefaultDependencies=no
Wants=network-pre.target
Before=network-pre.target network.target shutdown.target
Conflicts=shutdown.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/sbin/nft -f $FIREWALL_FILE
ExecReload=/usr/sbin/nft -f $FIREWALL_FILE
ExecStop=/usr/sbin/nft delete table inet care-pi

[Install]
WantedBy=multi-user.target
EOF
}

# The stock nftables.conf of Debian and Ubuntu. nft_conf_is_default compares a
# file with this text, ignoring comments and spacing.
nft_default_conf() {
  cat <<'EOF'
#!/usr/sbin/nft -f

flush ruleset

table inet filter {
	chain input {
		type filter hook input priority filter;
	}
	chain forward {
		type filter hook forward priority filter;
	}
	chain output {
		type filter hook output priority filter;
	}
}
EOF
}

nft_normalize() {
  sed -e 's/#.*//' -e 's/priority 0;/priority filter;/' \
    -e 's/[[:space:]][[:space:]]*/ /g' -e 's/^ //' -e 's/ $//' | grep -v '^$'
}

# nft_conf_is_default FILE succeeds when FILE is the stock nftables.conf.
nft_conf_is_default() {
  [ "$(nft_normalize <"$1")" = "$(nft_default_conf | nft_normalize)" ]
}

# The two lines that an older install.sh added to the end of NFT_CONF, after a
# blank line.
NFT_LEGACY_COMMENT='# care-pi: load the rule files in /etc/nftables.d'
NFT_LEGACY_INCLUDE='include "/etc/nftables.d/*.nft"'

nft_conf_has_legacy_include() {
  [ -f "$NFT_CONF" ] && grep -qxF "$NFT_LEGACY_COMMENT" "$NFT_CONF" \
    && grep -qxF "$NFT_LEGACY_INCLUDE" "$NFT_CONF"
}

# nft_conf_without_legacy prints NFT_CONF without the comment line, the include
# line that follows it, and the blank line before them. It keeps all other lines.
nft_conf_without_legacy() {
  awk -v c="$NFT_LEGACY_COMMENT" -v inc="$NFT_LEGACY_INCLUDE" '
    { line[NR] = $0 }
    END {
      for (i = 1; i <= NR; i++) {
        if (line[i] == c && line[i + 1] == inc) {
          drop[i] = 1; drop[i + 1] = 1
          if (i > 1 && line[i - 1] == "") drop[i - 1] = 1
        }
      }
      for (i = 1; i <= NR; i++) if (!drop[i]) print line[i]
    }
  ' "$NFT_CONF"
}

# nft_legacy_cleanup removes what an older install.sh did: the include line in
# NFT_CONF and the rule file in /etc/nftables.d. When NFT_CONF is then the same as
# its backup, the script puts the backup back.
nft_legacy_cleanup() {
  NFT_LEGACY_FOUND=0
  if nft_conf_has_legacy_include; then
    NFT_LEGACY_FOUND=1
    STEP_CHANGED=1
    nl_bak="$NFT_CONF.care-pi.bak"
    nl_text=$(nft_conf_without_legacy)
    if [ -f "$nl_bak" ] && printf '%s\n' "$nl_text" | cmp -s - "$nl_bak"; then
      if [ "$DRY_RUN" = 1 ]; then
        plan "remove the care-pi include from $NFT_CONF: put back $nl_bak"
      else
        nl_mode=$(stat -c %a "$NFT_CONF")
        cp "$nl_bak" "$NFT_CONF.care-pi.new"
        chmod "$nl_mode" "$NFT_CONF.care-pi.new"
        mv -f "$NFT_CONF.care-pi.new" "$NFT_CONF"
        rm -f "$nl_bak"
        log "removed the care-pi include from $NFT_CONF: put back $nl_bak"
      fi
    else
      write_file "$NFT_CONF" "$(stat -c %a "$NFT_CONF")" "$nl_text"
      if [ -f "$nl_bak" ]; then
        log "$NFT_CONF has other changes after its backup. The script removed only its include line and kept $nl_bak."
      fi
    fi
  fi
  if [ -f "$NFT_LEGACY_FILE" ] && grep -q '^# Managed by care-pi install.sh' "$NFT_LEGACY_FILE"; then
    NFT_LEGACY_FOUND=1
    STEP_CHANGED=1
    if [ "$DRY_RUN" = 1 ]; then
      plan "remove $NFT_LEGACY_FILE"
    else
      rm -f "$NFT_LEGACY_FILE"
      log "removed $NFT_LEGACY_FILE"
      # The older script created the directory. Remove it when it is empty and
      # nothing includes it.
      if ! grep -q '/etc/nftables\.d/' "$NFT_CONF" 2>/dev/null; then
        rmdir /etc/nftables.d 2>/dev/null || true
      fi
    fi
  fi
}

# nft_table_is_tailscale TEXT succeeds for a table of Tailscale: only ts-* chains
# and the base chains of iptables-nft, and a ts-* chain or the Tailscale fwmark
# mask (0xff0000).
nft_table_is_tailscale() {
  printf '%s\n' "$1" | awk '
    $1 == "chain" && $2 !~ /^(INPUT|FORWARD|OUTPUT|PREROUTING|POSTROUTING|ts-.*)$/ { other = 1 }
    /ts-|0xff0000|0x00ff0000/ { ts = 1 }
    END { exit !(ts && !other) }
  '
}

# nft_table_is_stock TEXT succeeds for the table inet filter of the stock
# nftables.conf: the chains input, forward, and output, and no rules.
nft_table_is_stock() {
  printf '%s\n' "$1" | awk '
    /^[ \t]*(#|$)/ { next }
    $1 == "table" || $1 == "}" || $1 == "type" { next }
    $1 == "chain" { if ($2 !~ /^(input|forward|output)$/) other = 1; next }
    { rules++ }
    END { exit !(!other && rules == 0) }
  '
}

# nft_foreign_tables prints each table of the live ruleset that does not belong
# to care-pi, netavark, Tailscale, or the stock nftables.conf.
nft_foreign_tables() {
  nft list tables 2>/dev/null | while read -r _ ft_family ft_name; do
    case "$ft_family $ft_name" in
      "inet care-pi" | "inet netavark") continue ;;
    esac
    ft_text=$(nft list table "$ft_family" "$ft_name" 2>/dev/null) || continue
    if nft_table_is_tailscale "$ft_text"; then
      continue
    fi
    if [ "$ft_family $ft_name" = "inet filter" ] && nft_table_is_stock "$ft_text"; then
      continue
    fi
    printf '%s %s\n' "$ft_family" "$ft_name"
  done
}

# nft_service_stop_without_flush marks nftables.service inactive. A runtime
# drop-in clears its ExecStop ("nft flush ruleset") for the stop, so the live
# rules stay.
nft_service_stop_without_flush() {
  if ! systemctl is-active --quiet nftables.service; then
    return 0
  fi
  if [ "$DRY_RUN" = 1 ]; then
    plan "stop nftables.service with an empty ExecStop (runtime drop-in), so that the ruleset stays"
    return 0
  fi
  install -d -m 755 "$NFT_NOFLUSH_DIR"
  printf '%s\n' '# care-pi install.sh: stop nftables.service without "nft flush ruleset".' \
    '[Service]' 'ExecStop=' >"$NFT_NOFLUSH_DIR/50-care-pi-no-flush.conf"
  run systemctl daemon-reload
  if [ -z "$(systemctl show -p ExecStop --value nftables.service 2>/dev/null)" ]; then
    run systemctl stop nftables.service
  else
    warn "Cannot clear the ExecStop of nftables.service. It stays active until the next reboot. Do not stop or restart it: it flushes the ruleset."
  fi
  rm -f "$NFT_NOFLUSH_DIR/50-care-pi-no-flush.conf"
  rmdir "$NFT_NOFLUSH_DIR" 2>/dev/null || true
  run systemctl daemon-reload
}

# nft_service_enabled_by_care_pi succeeds when the last enable or disable of
# nftables.service in the install log is an enable (by an older install.sh).
nft_service_enabled_by_care_pi() {
  grep -E 'run: systemctl (enable|disable) nftables\.service' "$LOG_FILE" 2>/dev/null \
    | tail -n 1 | grep -q 'enable nftables'
}

# nft_service_retire disables nftables.service when an older install.sh enabled
# it. Its stop and restart run "nft flush ruleset". That removes the rules of
# podman and Tailscale. An nftables package upgrade restarts it.
nft_service_retire() {
  if [ "$SYSTEMD" != 1 ]; then
    return 0
  fi
  if ! systemctl is-enabled --quiet nftables.service 2>/dev/null; then
    return 0
  fi
  if [ "$NFT_LEGACY_FOUND" != 1 ] && ! nft_service_enabled_by_care_pi; then
    log "Note: nftables.service is enabled, but care-pi did not enable it. The script keeps it. Its stop runs the ExecStop of /etc/nftables.conf (often 'flush ruleset'), which also removes the container rules."
    return 0
  fi
  if [ -f "$NFT_CONF" ] && [ "$DRY_RUN" != 1 ] && ! nft_conf_is_default "$NFT_CONF"; then
    log "Note: $NFT_CONF has rules of its own. The script keeps nftables.service enabled. To disable it: sudo systemctl disable nftables.service (do not stop it: the stop flushes the ruleset)."
    return 0
  fi
  nr_other=$(nft_foreign_tables | tr '\n' ',' | sed 's/,$//; s/,/, /g')
  if [ -n "$nr_other" ]; then
    log "Note: the ruleset has tables of other software ($nr_other). The script keeps nftables.service enabled. To disable it: sudo systemctl disable nftables.service (do not stop it: the stop flushes the ruleset)."
    return 0
  fi
  STEP_CHANGED=1
  run systemctl disable nftables.service
  nft_service_stop_without_flush
  log "Disabled nftables.service. $FIREWALL_UNIT loads the care-pi table."
}

step_firewall() {
  if [ "$NO_FIREWALL" = 1 ]; then
    STEP_SKIP="--no-firewall"
    return 0
  fi
  if [ "$SYSTEMD" = 1 ]; then
    for sf_unit in ufw.service firewalld.service; do
      if systemctl is-active --quiet "$sf_unit" 2>/dev/null; then
        STEP_SKIP="$sf_unit manages the firewall. Allow 80/tcp, 443/tcp, $CONSOLE_PORT/tcp, and 5353/udp there"
        return 0
      fi
    done
  fi
  sf_ssh=0
  if sshd_active; then
    sf_ssh=1
  fi
  sf_rules=$(nft_rules "$sf_ssh")

  # Check the rules before the script writes any file.
  if [ "$DRY_RUN" = 1 ]; then
    plan "check the rules with: nft -c -f"
  else
    sf_check="/run/care-pi-nft.$$.check"
    printf '%s\n' "$sf_rules" >"$sf_check"
    if ! run nft -c -f "$sf_check"; then
      rm -f "$sf_check"
      die "nft rejected the rules. The script changed no firewall file. Use --no-firewall to skip this step."
    fi
    rm -f "$sf_check"
  fi

  ensure_dir /etc/care-pi 755 0:0
  write_file "$FIREWALL_FILE" 644 "$sf_rules"
  sf_rules_written=$LAST_WRITE

  if [ "$SYSTEMD" != 1 ]; then
    if [ "$DRY_RUN" = 1 ]; then
      plan "write $FIREWALL_UNIT_FILE, then enable and start $FIREWALL_UNIT"
    else
      warn "systemd is not running. The script loads the table now, but nothing loads it at boot."
      if [ "$sf_rules_written" = 1 ] || ! nft list table inet care-pi >/dev/null 2>&1; then
        run nft -f "$FIREWALL_FILE"
      fi
    fi
  else
    write_file "$FIREWALL_UNIT_FILE" 644 "$(firewall_unit)"
    if [ "$LAST_WRITE" = 1 ]; then
      run systemctl daemon-reload
    fi
    if ! systemctl is-enabled --quiet "$FIREWALL_UNIT" 2>/dev/null; then
      STEP_CHANGED=1
      run systemctl enable "$FIREWALL_UNIT"
    fi
    # The start (or reload) replaces the table in one nft transaction. The rules
    # of podman and Tailscale stay.
    if ! systemctl is-active --quiet "$FIREWALL_UNIT"; then
      STEP_CHANGED=1
      run systemctl start "$FIREWALL_UNIT" || die "$FIREWALL_UNIT failed. Run: systemctl status $FIREWALL_UNIT"
    elif [ "$sf_rules_written" = 1 ] || ! nft list table inet care-pi >/dev/null 2>&1; then
      STEP_CHANGED=1
      run systemctl reload "$FIREWALL_UNIT" || die "$FIREWALL_UNIT failed to reload. Run: systemctl status $FIREWALL_UNIT"
    fi
  fi

  if [ -f "$NFT_CONF" ] && ! nft_conf_is_default "$NFT_CONF" && ! nft_conf_has_legacy_include; then
    log "Note: $NFT_CONF has rules of its own. If they drop packets, also allow 80/tcp, 443/tcp, $CONSOLE_PORT/tcp, and 5353/udp there."
  fi
  nft_legacy_cleanup
  nft_service_retire
}

# --- 9. unattended-upgrades

unattended_conf() {
  cat <<'EOF'
// Managed by care-pi install.sh.
// Install security updates automatically. Never reboot by itself: Care reboots
// inside the update window that the operator sets.
Unattended-Upgrade::Automatic-Reboot "false";
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
EOF
}

step_upgrades() {
  write_file /etc/apt/apt.conf.d/52care-pi.conf 644 "$(unattended_conf)"
  if pkg_installed unattended-upgrades; then
    if ! apt-config dump | grep -i 'Unattended-Upgrade::\(Origins-Pattern\|Allowed-Origins\)' | grep -qi security; then
      warn "The unattended-upgrades origins do not list the security pocket. Check /etc/apt/apt.conf.d/50unattended-upgrades."
    fi
  fi
  ensure_service apt-daily.timer
  ensure_service apt-daily-upgrade.timer
}

# --- 10. apt holds

step_holds() {
  if [ "$NO_APT_HOLD" = 1 ]; then
    STEP_SKIP="--no-apt-hold"
    return 0
  fi
  set --
  for sh_pkg in $HOLD_PACKAGES; do
    if { pkg_installed "$sh_pkg" || [ "$DRY_RUN" = 1 ]; } && ! pkg_held "$sh_pkg"; then
      set -- "$@" "$sh_pkg"
    fi
  done
  if [ $# -gt 0 ]; then
    STEP_CHANGED=1
    run apt-mark hold "$@"
  fi
  log "Held packages: $HOLD_PACKAGES. A Care release updates them on purpose. The command 'care-pi update --os' (future) lifts the holds."
}

# --- 11. directories

step_dirs() {
  while read -r sd_path sd_mode sd_owner; do
    ensure_dir "$sd_path" "$sd_mode" "$sd_owner"
  done <<EOF
/etc/care-pi 755 0:0
/etc/care-pi/secrets 700 0:0
/etc/care-pi/rendered 700 0:0
/var/lib/care-pi 755 0:0
/var/lib/care-pi/pg 700 999:999
/var/lib/care-pi/s3 700 0:0
/var/lib/care-pi/backup 755 0:0
/var/lib/care-pi/backup/pg 700 999:999
/var/lib/care-pi/fe 755 0:0
/var/lib/care-pi/plugs 755 0:0
/var/lib/care-pi/build 755 0:0
/var/lib/care-pi/state 700 0:0
EOF
}

# --- 12. agent binary

agent_base_url() {
  if [ -n "${CARE_PI_DOWNLOAD_BASE:-}" ]; then
    printf '%s' "${CARE_PI_DOWNLOAD_BASE%/}"
  elif [ "$AGENT_VERSION" = latest ]; then
    printf '%s' "$RELEASES_URL/latest/download"
  else
    printf '%s' "$RELEASES_URL/download/$AGENT_VERSION"
  fi
}

fetch() {
  if ! run curl -fsSL --retry 3 --connect-timeout 15 -o "$2" "$1"; then
    die "Download failed: $1. The care-pi repository is private today, so the default URL returns 404. Use --binary PATH, or set CARE_PI_DOWNLOAD_BASE to a URL that serves the files."
  fi
}

install_binary() {
  STEP_CHANGED=1
  if [ "$DRY_RUN" = 1 ]; then
    plan "install $1 as $BIN_PATH (mode 755)"
    return 0
  fi
  run install -m 0755 "$1" "$BIN_PATH.new"
  run mv -f "$BIN_PATH.new" "$BIN_PATH"
  AGENT_CHANGED=1
  log "Installed $BIN_PATH."
}

agent_from_file() {
  if [ ! -f "$BINARY" ]; then
    die "The file $BINARY does not exist."
  fi
  if [ -f "$BIN_PATH" ] && cmp -s "$BINARY" "$BIN_PATH" && [ "$(stat -c %a "$BIN_PATH")" = 755 ]; then
    return 0
  fi
  install_binary "$BINARY"
}

agent_from_release() {
  if [ -x "$BIN_PATH" ] && [ "$VERSION_EXPLICIT" = 0 ] && [ -z "${CARE_PI_DOWNLOAD_BASE:-}" ]; then
    log "The agent is already installed. Add --version TAG to change it."
    return 0
  fi
  ar_base=$(agent_base_url)
  ar_asset="care-pi_linux_$ARCH"
  if [ "$DRY_RUN" = 1 ]; then
    plan "download $ar_base/SHA256SUMS and $ar_base/$ar_asset"
    plan "check the sha256 sum, then install $BIN_PATH"
    STEP_CHANGED=1
    return 0
  fi
  WORK_DIR="/var/lib/care-pi/state/install.$$"
  install -d -m 700 "$WORK_DIR"
  fetch "$ar_base/SHA256SUMS" "$WORK_DIR/SHA256SUMS"
  ar_want=$(awk -v a="$ar_asset" '{ f = $2; sub(/^\*/, "", f); sub(/.*\//, "", f); if (f == a) { print $1; exit } }' "$WORK_DIR/SHA256SUMS")
  if [ -z "$ar_want" ]; then
    die "SHA256SUMS has no line for $ar_asset."
  fi
  if [ -f "$BIN_PATH" ] && [ "$(sha256sum "$BIN_PATH" | awk '{ print $1 }')" = "$ar_want" ]; then
    return 0
  fi
  fetch "$ar_base/$ar_asset" "$WORK_DIR/$ar_asset"
  printf '%s  %s\n' "$ar_want" "$ar_asset" >"$WORK_DIR/check.sha256"
  if ! ar_check=$(cd "$WORK_DIR" && sha256sum -c check.sha256 2>&1); then
    die "The checksum of $ar_asset does not match SHA256SUMS: $ar_check"
  fi
  log "$ar_check"
  install_binary "$WORK_DIR/$ar_asset"
}

step_agent() {
  if [ -n "$BINARY" ]; then
    agent_from_file
  else
    agent_from_release
  fi
  if [ "$DRY_RUN" = 1 ]; then
    plan "run: $BIN_PATH version"
    return 0
  fi
  if ! sa_out=$("$BIN_PATH" version 2>&1 </dev/null); then
    die "'$BIN_PATH version' failed: $sa_out"
  fi
  log "Agent: ${sa_out:-(no version output)}"
}

# --- 13. agent service

# care_pi_unit prints care-pi.service. The installer owns the unit, not "care-pi apply":
# an apply must never rewrite and restart the unit of the process that runs the apply.
# The unit and the binary change together, at an install.
care_pi_unit() {
  cat <<EOF
# Managed by care-pi install.sh. Do not edit.
# The care-pi agent: the console API and the console SPA on the unix socket
# /run/care-pi/api.sock ("care-pi serve"). Caddy sends port $CONSOLE_PORT and
# /_care-pi/* of port 443 to the socket.
# The agent does not wait for network-online.target: it listens on a unix socket and
# reads the LAN addresses at each request. Thus multi-user.target does not wait for
# the network. care-caddy.service starts after this unit (Wants= and After=).
[Unit]
Description=care-pi agent (console API on /run/care-pi/api.sock)
Documentation=https://github.com/rithviknishad/care-pi
After=network.target

[Service]
Type=notify
# The sandbox needs these directories at the start of the service.
ExecStartPre=+/bin/mkdir -p /etc/care-pi /var/lib/care-pi /run/care-pi /etc/containers/systemd /var/lib/containers
ExecStart=$BIN_PATH serve
Restart=always
RestartSec=2
TimeoutStopSec=30
# Memory cap of 01 section 8. "podman pull" runs in a transient unit (systemd-run),
# so the image pulls of an apply do not count here. The other children (skopeo,
# podman exec, podman ps) count here: 64M was too small beside the argon2id
# hashes of logins (wave 4c). "care-pi serve" gives the Go GC half of MemoryMax.
MemoryMax=128M
MemorySwapMax=32M
OOMScoreAdjust=-100
# Hardening (04 section 2). NoNewPrivileges is not possible: the agent runs podman
# and systemctl. ProtectControlGroups and SystemCallFilter wait for Phase 7: podman
# exec writes the cgroup of the container and needs more system calls.
ProtectSystem=strict
# /run: podman writes /run/containers, /run/libpod, and /run/crun.
# /etc/hosts: the setup finish writes the 127.0.1.1 line of a new host name (hostnamectl writes /etc/hostname).
ReadWritePaths=/etc/care-pi /var/lib/care-pi /run /etc/containers/systemd /etc/systemd/system /var/lib/containers -/var/cache/containers -/etc/hosts
ProtectHome=read-only
PrivateTmp=yes
ProtectKernelTunables=yes
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK

[Install]
WantedBy=multi-user.target
EOF
}

step_agent_service() {
  if [ "$SYSTEMD" != 1 ]; then
    if [ "$DRY_RUN" = 1 ]; then
      plan "write $AGENT_UNIT_FILE, then enable and start $AGENT_UNIT"
      STEP_CHANGED=1
    else
      STEP_SKIP="systemd is not running. The console needs $AGENT_UNIT"
    fi
    return 0
  fi
  write_file "$AGENT_UNIT_FILE" 644 "$(care_pi_unit)"
  as_written=$LAST_WRITE
  if [ "$as_written" = 1 ]; then
    run systemctl daemon-reload
  fi
  if ! systemctl is-enabled --quiet "$AGENT_UNIT" 2>/dev/null; then
    STEP_CHANGED=1
    run systemctl enable "$AGENT_UNIT"
  fi
  if [ "$REBOOT_NEEDED" = 1 ]; then
    log "$AGENT_UNIT starts after the reboot."
    return 0
  fi
  if systemctl is-active --quiet "$AGENT_UNIT"; then
    if [ "$as_written" = 1 ] || [ "$AGENT_CHANGED" = 1 ]; then
      STEP_CHANGED=1
      run systemctl restart "$AGENT_UNIT" || warn "$AGENT_UNIT did not restart. Run: systemctl status $AGENT_UNIT"
    fi
  else
    STEP_CHANGED=1
    run systemctl start "$AGENT_UNIT" || warn "$AGENT_UNIT did not start. Run: systemctl status $AGENT_UNIT"
  fi
}

# --- 14. NetworkManager dispatcher hook

nm_hook() {
  cat <<'EOF'
#!/bin/sh
# Managed by care-pi install.sh. NetworkManager runs this script with two
# arguments: the interface and the action. On an address change, it runs
# "care-pi apply" so that the Caddyfile gets the new LAN address.
PENDING="/run/care-pi-address-change.pending"
LOCK="/run/care-pi-address-change.lock"
CARE_PI="/usr/local/bin/care-pi"

if [ "${1:-}" = "--run" ]; then
  # One job waits at a time. An event that finds a waiting job ends at once:
  # the apply of the waiting job also sees its change.
  exec 8>"$PENDING"
  flock -n 8 || exit 0
  # Wait for the apply of an earlier job. Hold LOCK until this apply ends.
  exec 9>"$LOCK"
  flock 9
  # Debounce: wait 5 s for more events of the same change.
  sleep 5
  # From now on, a new event starts one more job after this apply.
  exec 8>&-
  # care-pi does not get fd 9, so its child processes cannot keep the lock.
  "$CARE_PI" apply --reason address-change 9>&-
  exit $?
fi

case "${2:-}" in
  up | down | dhcp4-change | connectivity-change) ;;
  *) exit 0 ;;
esac
case "${1:-}" in
  lo | veth* | podman* | docker* | br-* | cni* | virbr* | tailscale* | tun* | tap*) exit 0 ;;
esac
[ -f /etc/care-pi/config.yaml ] || exit 0
[ -x "$CARE_PI" ] || exit 0
command -v systemd-run >/dev/null 2>&1 || exit 0

# NetworkManager stops the dispatcher after a short time. A transient unit keeps
# the debounced job alive.
exec systemd-run --no-block --quiet --collect \
  --description="care-pi address change" "$0" --run
EOF
}

step_nm_hook() {
  if [ ! -d /etc/NetworkManager/dispatcher.d ]; then
    STEP_SKIP="NetworkManager is not installed. Run 'care-pi apply' after each LAN address change"
    return 0
  fi
  write_file "$DISPATCHER_FILE" 755 "$(nm_hook)"
}

# --- 15. host check and bootstrap

# bootstrap_args prints the arguments of "care-pi bootstrap".
bootstrap_args() {
  ba="bootstrap --hostname $EFFECTIVE_HOSTNAME --preset $PRESET --fe-provider $FE_PROVIDER --yes"
  if [ "$UNATTENDED" = 1 ]; then
    ba="$ba --unattended"
  fi
  printf '%s' "$ba"
}

print_follow_up() {
  log "Next steps:"
  if [ "$REBOOT_NEEDED" = 1 ]; then
    log "  1. Reboot:  sudo reboot"
    log "  2. Bootstrap Care:  sudo care-pi $(bootstrap_args)"
  else
    log "  Bootstrap Care:  sudo care-pi $(bootstrap_args)"
  fi
}

print_urls() {
  pu_name=$ADVERTISED_NAME
  if [ -z "$pu_name" ] && [ "$NO_AVAHI" != 1 ]; then
    pu_name=$(advertised_name)
  fi
  if [ -n "$pu_name" ]; then
    log "  https://$pu_name"
  fi
  lan_ipv4s | while read -r pu_ip; do
    log "  https://$pu_ip"
  done
}

print_console() {
  log "Setup mode: Care starts when you finish the setup in the console."
  log "Open the console on a device in this network and enter the claim code above:"
  pc_name=$ADVERTISED_NAME
  if [ -z "$pc_name" ] && [ "$NO_AVAHI" != 1 ]; then
    pc_name=$(advertised_name)
  fi
  if [ -n "$pc_name" ]; then
    log "  https://$pc_name:$CONSOLE_PORT/"
  fi
  lan_ipv4s | while read -r pc_ip; do
    log "  https://$pc_ip:$CONSOLE_PORT/"
  done
  log "The first visit shows a certificate warning. The device uses its own local certificate authority. Accept the warning one time. Then install the root certificate (the setup page offers it)."
  log "Print the claim code again:  sudo care-pi auth claim-code"
  log "Finish without the console:  docs/console-server.md, \"Setup on the host\""
}

# setup_state prints the state of the setup machine ("ready" after the setup).
setup_state() {
  "$BIN_PATH" setup status --json 2>/dev/null | sed -n 's/^  "state": "\(.*\)",$/\1/p'
}

print_done() {
  if [ "$UNATTENDED" != 1 ] && [ "$(setup_state)" != ready ]; then
    print_console
    return 0
  fi
  if [ -f /var/lib/care-pi/fe/current/index.html ]; then
    log "Care is ready. Open one of these addresses:"
  else
    log "Care runs, but it shows the setup page: no FE build exists."
    log "To serve Care, set fe.provider to local or artifact in /etc/care-pi/config.yaml. Then run: sudo care-pi apply"
    log "Care is at these addresses:"
  fi
  print_urls
  log "The first visit shows a certificate warning. Care uses its own local certificate authority until you enable a public certificate. Accept the warning once, then install the certificate on each device."
  log "Create the first Care user:"
  log "  sudo care-pi care create-superuser --username NAME --email EMAIL"
  log "The console is at https://<host>:$CONSOLE_PORT/. To claim it, print the claim code: sudo care-pi auth claim-code"
}

step_finish() {
  if [ "$DRY_RUN" = 1 ]; then
    plan "run: $BIN_PATH host check"
  elif ! run "$BIN_PATH" host check; then
    if [ "$REBOOT_NEEDED" = 1 ]; then
      log "The host check reports problems. This is expected before the reboot."
    else
      die "The host check found unmet prerequisites. Read the output above."
    fi
  fi

  if [ "$REBOOT_NEEDED" = 1 ]; then
    log "REBOOT REQUIRED ($REBOOT_REASONS). The script does not run 'care-pi bootstrap' before the reboot."
    print_follow_up
    STEP_SKIP="reboot first"
    return 0
  fi
  if [ "$SKIP_BOOTSTRAP" = 1 ]; then
    print_follow_up
    STEP_SKIP="--skip-bootstrap"
    return 0
  fi
  STEP_CHANGED=1
  if [ "$DRY_RUN" = 1 ]; then
    plan "run: $BIN_PATH $(bootstrap_args)"
    return 0
  fi
  if [ "$UNATTENDED" = 1 ] && [ "$FE_PROVIDER" = local ]; then
    log "Bootstrap builds care_fe on this host (FE provider local). The first build takes about 3 minutes on a Pi 5 and needs internet."
  fi
  # The arguments have no spaces, so the word splitting is intentional.
  # shellcheck disable=SC2046
  if ! run "$BIN_PATH" $(bootstrap_args); then
    die "care-pi bootstrap failed. Read the output above. After you fix the cause, run: sudo care-pi $(bootstrap_args)"
  fi
  print_done
}

# ---------------------------------------------------------------- main

cleanup() {
  cl_rc=$?
  trap - EXIT
  if [ -n "$WORK_DIR" ] && [ -d "$WORK_DIR" ]; then
    rm -rf "$WORK_DIR"
  fi
  rm -f "$RC_FILE"
  if [ "$cl_rc" -ne 0 ] && [ "$FINISHED" != 1 ]; then
    printf '[care-pi] The install stopped in step "%s" (exit code %s).\n' "$CURRENT_STEP" "$cl_rc" >&2
    if [ "$LOG_READY" = 1 ]; then
      printf '[care-pi] Read %s for details. It is safe to run the script again.\n' "$LOG_FILE" >&2
    fi
  fi
}

main() {
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  load_env
  parse_args "$@"
  umask 022
  ensure_root
  setup_logging
  if [ "$DRY_RUN" = 1 ]; then
    log "Dry run: the script changes nothing."
  fi
  detect_host
  build_package_list
  confirm_plan

  step "Install packages" step_packages
  step "Set the memory cgroup" step_memory_cgroup
  step "Set up zram swap" step_zram
  step "Set the host name" step_hostname
  step "Set up avahi (mDNS)" step_avahi
  step "Set up time sync" step_time
  step "Keep the journal on disk" step_journal
  step "Set up the firewall" step_firewall
  step "Set up unattended upgrades" step_upgrades
  step "Hold the container packages" step_holds
  step "Create the directories" step_dirs
  step "Install the agent" step_agent
  step "Install the agent service" step_agent_service
  step "Install the NetworkManager hook" step_nm_hook
  step "Check the host and bootstrap" step_finish

  FINISHED=1
  if [ "$DRY_RUN" = 1 ]; then
    log "Dry run complete. The script changed nothing."
  elif [ "$REBOOT_NEEDED" = 1 ]; then
    log "Install script complete. REBOOT REQUIRED: run 'sudo reboot', then the bootstrap command above. Log file: $LOG_FILE"
  else
    log "Install script complete. Log file: $LOG_FILE"
  fi
}

main "$@"
