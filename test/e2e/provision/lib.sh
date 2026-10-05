# shellcheck shell=bash
# Shared settings and helpers for the provision E2E phase scripts.
# Source it from a phase script; it is not meant to be run.
#
# Environment:
#   E2E_IMAGES     local (default): build the images here and import them into
#                  k3s; ghcr: use the images and chart pushed to GHCR (CI).
#   E2E_VERSION    image tag and chart version. Required for ghcr; defaults to
#                  0.0.0-local for local.
#   E2E_WORK_ROOT  scratch root (default /tmp/isoboot-e2e); a row uses
#                  $E2E_WORK_ROOT/<row-id>, its logs go to .../<row-id>/logs.
#   E2E_QEMU_CACHE where the custom RTL8168 QEMU build is kept (default
#                  ~/qemu-cache; CI caches this directory).
#   E2E_KEEP_DOWNLOADS=1  cleanup.sh keeps the downloaded artifacts in
#                  $DATA_DIR/artifacts, so later rows and runs on the same host
#                  do not fetch them again (hack/e2e-local.sh sets it; CI
#                  downloads afresh).
#   E2E_NO_PROGRESS_MINUTES, E2E_WAIT_MAX_MINUTES  limits of wait_ready.
#   E2E_ALLOW_THIS_HOST=1  run on a host that is neither a GitHub Actions
#                  runner nor a VM made by hack/e2e-local.sh (see below).
#
# The phase scripts change the host for real: they install packages and k3s,
# uninstall k3s, delete /data/isoboot, add a bridge and iptables rules, stop
# rpcbind and open /dev/kvm to everyone. So they refuse to run unless the host
# is known to be disposable: a GitHub Actions runner (GITHUB_ACTIONS=true), a
# VM that hack/e2e-local.sh created (it writes the marker file
# /etc/isoboot-e2e-vm), or a host the caller names as disposable with
# E2E_ALLOW_THIS_HOST=1.
set -euo pipefail

log() { printf '[%s] %s\n' "$(date -u +%H:%M:%S)" "$*"; }
pass() { log "PASS: $*"; }
fail() {
  printf '[%s] FAIL: %s\n' "$(date -u +%H:%M:%S)" "$*" >&2
  exit 1
}

E2E_HOST_MARKER=/etc/isoboot-e2e-vm
if [ "${GITHUB_ACTIONS:-}" != true ] && [ ! -e "$E2E_HOST_MARKER" ] \
    && [ "${E2E_ALLOW_THIS_HOST:-}" != 1 ]; then
  fail "refusing to change this host: it is not a GitHub Actions runner and has no $E2E_HOST_MARKER (hack/e2e-local.sh creates it in its VM). Run hack/e2e-local.sh, or set E2E_ALLOW_THIS_HOST=1 only on a throwaway machine."
fi

# Constants used by the phase scripts that source this file.
# shellcheck disable=SC2034
{
E2E_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "$E2E_DIR/../../.." && pwd)
ROWS_FILE=$E2E_DIR/rows.json

NS=isoboot-system
RELEASE=isoboot
SUBNET=192.168.101.0/24
HOST_IP=192.168.101.1
DHCP_IP=192.168.101.2
BRIDGE=br-pxe
TAP=tap-vm
DHCP_CONTAINER=dhcp-srv
DATA_DIR=/data/isoboot
IMAGE=ghcr.io/isoboot/isoboot
PROVISION="qemu-vm1-provision"

# Tool versions, pinned so every run installs the same ones; bump on purpose.
# Keep KUBECTL_VERSION in .devcontainer/post-install.sh on the same minor.
K3S_VERSION="v1.36.5+k3s1"
HELM_VERSION="v3.22.0"
}

E2E_IMAGES=${E2E_IMAGES:-local}
E2E_WORK_ROOT=${E2E_WORK_ROOT:-/tmp/isoboot-e2e}
E2E_QEMU_CACHE=${E2E_QEMU_CACHE:-$HOME/qemu-cache}
case $E2E_IMAGES in
  local) E2E_VERSION=${E2E_VERSION:-0.0.0-local} ;;
  ghcr) E2E_VERSION=${E2E_VERSION:-} ;;
  *) echo "FAIL: E2E_IMAGES must be local or ghcr, got '$E2E_IMAGES'" >&2; exit 1 ;;
esac
# shellcheck disable=SC2034
ALPINE_VERSION=$(cat "$REPO_ROOT/.alpine-version")
# Always the k3s that k3s.sh installs, never a cluster from the caller's
# environment.
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml

# load_row <row-id>: select the row and set ROW_ID, WORK, LOG_DIR, MAC, MAC_COLON.
load_row() {
  ROW_ID=${1:-}
  [ -n "$ROW_ID" ] || fail "usage: $(basename "$0") <row-id> (ids: $(jq -r '[.[].id] | join(", ")' "$ROWS_FILE"))"
  jq -e --arg id "$ROW_ID" 'any(.[]; .id == $id)' "$ROWS_FILE" >/dev/null \
    || fail "unknown row '$ROW_ID' (ids: $(jq -r '[.[].id] | join(", ")' "$ROWS_FILE"))"
  WORK=$E2E_WORK_ROOT/$ROW_ID
  LOG_DIR=$WORK/logs
  mkdir -p "$WORK" "$LOG_DIR"
  MAC=$(row mac)
  MAC_COLON=${MAC//-/:}
}

# row <field>: a scalar field of the current row; empty when absent or false.
row() {
  jq -r --arg id "$ROW_ID" --arg f "$1" \
    '.[] | select(.id == $id) | .[$f] // empty | if type == "array" or type == "object" then tojson else . end' \
    "$ROWS_FILE"
}

kc() { kubectl -n "$NS" "$@"; }

# wait_pods <component> <timeout-seconds>: wait for the pod to exist, then Ready.
wait_pods() {
  local component=$1 timeout=$2
  for _ in $(seq 1 90); do
    [ -n "$(kc get pod -l "app.kubernetes.io/component=$component" -o name 2>/dev/null)" ] && break
    sleep 2
  done
  kc wait --for=condition=Ready pod -l "app.kubernetes.io/component=$component" --timeout="${timeout}s"
}

# data_dir_bytes: the bytes under $DATA_DIR, without the squid cache.
# Downloads and ISO unpacks grow it, so it shows the controller at work.
data_dir_bytes() {
  local bytes
  bytes=$(sudo du -sb --exclude=squid "$DATA_DIR" 2>/dev/null | cut -f1 || true)
  echo "${bytes:-0}"
}

# wait_ready <kind> <name>: wait for a custom resource to reach phase Ready
# for as long as there is progress: its phase changes or the bytes under
# $DATA_DIR change (a multi-GB ISO can take long on a slow link). Fails after
# E2E_NO_PROGRESS_MINUTES (default 10) without progress, after
# E2E_WAIT_MAX_MINUTES (default 120) in all, or on 3 Error phases in a row.
wait_ready() {
  local kind=$1 name=$2 phase bytes message last_phase=none last_bytes=none
  local waited=0 idle=0 errors=0
  local idle_limit=$((${E2E_NO_PROGRESS_MINUTES:-10} * 60))
  local total_limit=$((${E2E_WAIT_MAX_MINUTES:-120} * 60))
  while :; do
    phase=$(kc get "$kind" "$name" -o jsonpath='{.status.phase}' 2>/dev/null || true)
    if [ "$phase" = Ready ]; then
      pass "$kind $name is Ready (waited $((waited / 60)) min)"
      return 0
    fi
    if [ "$phase" = Error ]; then
      errors=$((errors + 1))
      message=$(kc get "$kind" "$name" -o jsonpath='{.status.message}' 2>/dev/null || true)
      log "$kind $name is in phase Error ($errors in a row): $message"
      [ "$errors" -lt 3 ] || { wait_ready_dump "$kind" "$name"; fail "$kind $name stays in phase Error: $message"; }
    else
      errors=0
    fi
    bytes=$(data_dir_bytes)
    if [ "$phase" != "$last_phase" ] || [ "$bytes" != "$last_bytes" ]; then
      idle=0
    fi
    if [ "$idle" -ge "$idle_limit" ]; then
      wait_ready_dump "$kind" "$name"
      fail "$kind $name: no progress for $((idle / 60)) min (phase '${phase:-<none>}', $((bytes / 1048576)) MiB in $DATA_DIR)"
    fi
    if [ "$waited" -ge "$total_limit" ]; then
      wait_ready_dump "$kind" "$name"
      fail "$kind $name is not Ready after $((waited / 60)) min (phase '${phase:-<none>}')"
    fi
    [ $((waited % 60)) = 0 ] \
      && log "$kind $name: phase ${phase:-<none>}, $((bytes / 1048576)) MiB in $DATA_DIR, waited $((waited / 60)) min"
    last_phase=$phase
    last_bytes=$bytes
    sleep 10
    waited=$((waited + 10))
    idle=$((idle + 10))
  done
}

wait_ready_dump() {
  kc get "$1" "$2" -o yaml || true
  kc logs -l app.kubernetes.io/component=controller --tail=50 || true
}

# clean_data_dir <dir>: empty the data directory but keep the squid cache and,
# with E2E_KEEP_DOWNLOADS=1 (local runs), the downloaded artifacts: the
# controller checks their hash and reuses them instead of downloading again.
clean_data_dir() {
  local kept=(! -name squid)
  [ "${E2E_KEEP_DOWNLOADS:-0}" = 1 ] && kept+=(! -name artifacts)
  [ -d "$1" ] || return 0
  sudo find "$1" -mindepth 1 -maxdepth 1 "${kept[@]}" -exec rm -rf {} +
}

provision_phase() {
  kc get provision "$PROVISION" -o jsonpath='{.status.phase}' 2>/dev/null || true
}

nginx_access_log() {
  local pod
  pod=$(kc get pod -l app.kubernetes.io/component=nginx -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
  [ -n "$pod" ] || return 0
  kc exec "$pod" -- cat /var/run/access.log 2>/dev/null || true
}

# dhcp_ip: the address the site DHCP server last acknowledged for the VM's MAC.
dhcp_ip() {
  sudo docker exec "$DHCP_CONTAINER" grep -a DHCPACK /tmp/dnsmasq.log 2>/dev/null \
    | grep -i "$MAC_COLON" | tail -1 | grep -oE '192\.168\.101\.[0-9]+' | head -1 || true
}

qemu_running() {
  [ -f "$WORK/qemu.pid" ] && sudo kill -0 "$(sudo cat "$WORK/qemu.pid")" 2>/dev/null
}

qemu_monitor() {
  [ -S "$WORK/qemu-monitor.sock" ] || return 0
  printf '%s\n' "$1" | sudo socat - "UNIX-CONNECT:$WORK/qemu-monitor.sock" >/dev/null 2>&1 || true
}

# qemu_stop: quit the VM (power cut) and wait for the process to go.
qemu_stop() {
  qemu_monitor quit
  for _ in $(seq 1 15); do
    qemu_running || break
    sleep 1
  done
  if qemu_running; then sudo kill "$(sudo cat "$WORK/qemu.pid")" 2>/dev/null || true; fi
  sudo rm -f "$WORK/qemu.pid" "$WORK/qemu-monitor.sock"
}

screendump() {
  qemu_running || return 0
  qemu_monitor "screendump $LOG_DIR/screen-$1.ppm"
  sleep 1
  sudo chmod 644 "$LOG_DIR/screen-$1.ppm" 2>/dev/null || true
}

ovmf_code() {
  local f
  for f in /usr/share/OVMF/OVMF_CODE_4M.fd /usr/share/OVMF/OVMF_CODE.fd; do
    [ -f "$f" ] && { echo "$f"; return 0; }
  done
  fail "OVMF firmware not found (package ovmf)"
}

ovmf_vars() {
  local f
  for f in /usr/share/OVMF/OVMF_VARS_4M.fd /usr/share/OVMF/OVMF_VARS.fd; do
    [ -f "$f" ] && { echo "$f"; return 0; }
  done
  fail "OVMF variable store not found (package ovmf)"
}

# qemu_start <serial-log> <boot-from: pxe|disk>: start the row's VM in the background.
# The PXE (install) boot uses -no-reboot, so QEMU exits when the installer reboots.
qemu_start() {
  local serial=$1 boot=$2 qemu nic_dev disk_dev extra=()
  case $(row nic) in
    rtl8168)
      qemu=/usr/local/bin/qemu-system-x86_64
      nic_dev="rtl8168,netdev=pxe0,romfile=efi-rtl8168.rom,mac=$MAC_COLON"
      ;;
    virtio)
      qemu=/usr/bin/qemu-system-x86_64
      nic_dev="virtio-net-pci,netdev=pxe0,mac=$MAC_COLON"
      ;;
    *) fail "row $ROW_ID: unknown nic '$(row nic)'" ;;
  esac
  disk_dev="virtio-blk-pci,drive=disk0"
  if [ "$boot" = pxe ]; then
    nic_dev="$nic_dev,bootindex=1"
    extra=(-boot n -no-reboot)
  else
    disk_dev="$disk_dev,bootindex=1"
  fi
  sudo ip link set "$TAP" master "$BRIDGE"
  sudo ip link set "$TAP" up
  sudo "$qemu" \
    -enable-kvm -cpu host -smp 2 -m "$(row ram_mb)" \
    -drive "if=pflash,format=raw,readonly=on,file=$(ovmf_code)" \
    -drive "if=pflash,format=raw,file=$WORK/ovmf-vars.fd" \
    -drive "file=$WORK/disk.qcow2,format=qcow2,if=none,id=disk0" \
    -device "$disk_dev" \
    -netdev "tap,id=pxe0,ifname=$TAP,script=no,downscript=no" \
    -device "$nic_dev" \
    "${extra[@]}" \
    -display none \
    -serial "file:$serial" \
    -monitor "unix:$WORK/qemu-monitor.sock,server,nowait" \
    -daemonize -pidfile "$WORK/qemu.pid"
  sudo chmod 644 "$serial"
  log "QEMU started ($(row nic) NIC, $(row ram_mb) MB, boot from $boot, serial $serial)"
}

# sshd_auth_methods <user@host> [ssh options...]: the login methods sshd
# offers, read from its answer to the "none" method (ssh -v ends its debug
# lines with CR LF). Empty if unreachable.
sshd_auth_methods() {
  local destination=$1
  shift
  ssh -v "$@" -o BatchMode=yes -o PreferredAuthentications=none \
    -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 \
    "$destination" true 2>&1 | tr -d '\r' \
    | sed -n 's/.*Authentications that can continue: //p' | head -1 || true
}

# assert_no_password_auth <user@host> [ssh options...]: fail unless sshd
# refuses passwords. It asks the server which methods it offers instead of
# trying a password, so a client-side setting cannot make it pass.
# keyboard-interactive counts as password login (PAM asks for the password).
assert_no_password_auth() {
  local methods
  methods=$(sshd_auth_methods "$@")
  [ -n "$methods" ] || fail "could not read the login methods sshd on $1 offers"
  case ",$methods," in
    *,password,* | *,keyboard-interactive,*) fail "sshd on $1 offers password login: $methods" ;;
  esac
  pass "sshd on $1 offers only $methods"
}

# assert_host_key <host> <type> <expected public key file>: fail unless the
# host's SSH host key of that type is the expected one.
assert_host_key() {
  local host=$1 type=$2 expected actual
  expected=$(ssh-keygen -lf "$3" | awk '{print $2}')
  actual=$(ssh-keyscan -t "$type" "$host" 2>/dev/null | ssh-keygen -lf - 2>/dev/null | awk '{print $2}' || true)
  [ "$expected" = "$actual" ] || fail "$type host key: expected $expected, got ${actual:-none}"
}

# assert_stalls <checks> <seconds between checks>: the negative row's proof.
# The installer has no firmware for its NIC, so it never gets a network: the
# Provision must stay exactly Pending throughout (an unreadable phase fails
# too), and nginx must never have served the Provision's automation files,
# which are the installer's first fetch once it has a network.
assert_stalls() {
  local checks=$1 interval=$2 phase access i
  for i in $(seq 1 "$checks"); do
    phase=$(provision_phase)
    [ "$phase" = Pending ] \
      || fail "Provision is '${phase:-<unreadable>}' at check $i/$checks; it must stay Pending (no NIC firmware, so no network)"
    [ $((i % 6)) = 1 ] && log "phase=$phase (check $i/$checks)"
    sleep "$interval"
  done
  access=$(nginx_access_log)
  [ -n "$access" ] || fail "could not read the nginx access log"
  if grep -F "GET /dynamic/automation/$PROVISION/" <<<"$access"; then
    fail "the installer fetched its automation files (above): it had a network without NIC firmware"
  fi
  pass "Provision stayed Pending and the installer fetched nothing: no network without NIC firmware"
}

# assert_nfs_cmdline <serial log> <bootconfig>: the guest kernel's own command
# line (printed on the serial console) mounts the tree over NFS and has
# nothing that copies the ISO into RAM (url=, iso-url=, toram).
assert_nfs_cmdline() {
  local serial=$1 bootconfig=$2 cmdline
  cmdline=$(grep -a -m1 'Command line:' "$serial" | tr -d '\r') \
    || fail "no kernel command line in $serial"
  cmdline=" ${cmdline#*Command line: } "
  case $cmdline in
    *" netboot=nfs "*) ;;
    *) fail "kernel command line has no netboot=nfs:$cmdline" ;;
  esac
  case $cmdline in
    *" nfsroot=$HOST_IP:/$bootconfig "*) ;;
    *) fail "kernel command line has no nfsroot=$HOST_IP:/$bootconfig:$cmdline" ;;
  esac
  case $cmdline in
    *" url="* | *" iso-url="* | *" toram "* | *" toram="*)
      fail "kernel command line would copy the ISO into RAM:$cmdline" ;;
  esac
  pass "kernel command line: netboot=nfs nfsroot=$HOST_IP:/$bootconfig, no url=, iso-url= or toram"
}

# assert_no_restarts: fail if any container of an isoboot pod has restarted
# (an OOM kill the controller recovers from would otherwise go unnoticed).
assert_no_restarts() {
  local restarts
  restarts=$(kc get pods -o json | jq -r '.items[] | .metadata.name as $pod
      | ((.status.initContainerStatuses // []) + (.status.containerStatuses // []))[]
      | select(.restartCount > 0)
      | "\($pod)/\(.name) restarts=\(.restartCount) last=\(.lastState.terminated.reason // "?")"') \
    || fail "could not list the isoboot pods"
  [ -z "$restarts" ] || fail "isoboot pods restarted: $restarts"
  pass "no isoboot pod restarted"
}
