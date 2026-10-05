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
set -euo pipefail
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
export KUBECONFIG=${KUBECONFIG:-/etc/rancher/k3s/k3s.yaml}

log() { printf '[%s] %s\n' "$(date -u +%H:%M:%S)" "$*"; }
pass() { log "PASS: $*"; }
fail() {
  printf '[%s] FAIL: %s\n' "$(date -u +%H:%M:%S)" "$*" >&2
  exit 1
}

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

# wait_ready <kind> <name>: wait for a custom resource to reach phase Ready.
wait_ready() {
  "$REPO_ROOT/test/e2e/wait-for-resource.sh" -n "$NS" "$1" "$2"
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
