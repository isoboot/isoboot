#!/usr/bin/env bash
# One step of the Ubuntu autoinstall journey (docs/ubuntu-autoinstall-journey.md):
# boot a fresh VM through the real isoboot path (PXE, iPXE, kernel and initrd
# over HTTP, root over NFS) with a given answer file, follow the installer
# until it stops, and keep 4K screenshots and the serial console.
#
# Runs where the provision E2E runs (an E2E VM made by hack/e2e-local.sh, or
# a CI runner), after its phases up to helm-install and an applied Ubuntu
# BootConfig, e.g. inside the VM:
#   ~/isoboot/test/e2e/provision/run.sh ubuntu-26.04 apply-row
#
# Usage:
#   autoinstall-journey.sh install <out-dir> [options]
#     Boot from the network onto a new blank 20 GiB disk and wait until QEMU
#     exits (the installer finished and rebooted; QEMU runs with -no-reboot),
#     the screen stays the same for --stable-minutes (a prompt or an error),
#     or --minutes pass. Then save the last screen as final.png and stop.
#       --bootconfig <name>    BootConfig to boot (default ubuntu-26.04)
#       --kernel-args <args>   set the BootConfig's spec.kernelArgs first
#                              (template variables as in the examples)
#       --user-data <file>     the answer file, served as user-data
#       --meta-data <file>     served as meta-data
#                              (a file left out is not served: HTTP 404)
#       --minutes <n>          give up after n minutes (default 45)
#       --stable-minutes <n>   default 3
#       --press <key>          when the screen has stopped changing, press
#                              this key (QEMU sendkey name, e.g. ret) and
#                              follow again; repeatable. The screen before
#                              each press is kept as before-press-<i>.png.
#   autoinstall-journey.sh login <out-dir> --user <name> --password <pw>
#                          [--command <cmd>]... [--ssh-command <cmd>]...
#     Boot the disk that "install" left in <out-dir>, wait for the console
#     login prompt, log in by typing on the VM's keyboard (QEMU sendkey) and
#     run each command (after "clear"), with a screenshot after each. Then
#     log in over SSH with the same password (password authentication only)
#     and run each --ssh-command; the output goes to ssh-<i>.txt.
#
# Every step writes to <out-dir>: shot-<seconds>.png (a screenshot every
# 20 s while the screen changes), final.png, serial.log, outcome.txt,
# kernel-args.txt, the served files and the nginx log lines of this boot.
# Screens are 3840x2160 (lib.sh qemu_start); watch live through VNC on
# 127.0.0.1:5900 + display number (E2E_VNC_DISPLAY, default :0).
#
# The VM is the hardware of row ubuntu-26.04 (virtio NIC, 2048 MiB, UEFI) with
# its own MAC, Machine "journey" and Provision "journey-provision", so it does
# not clash with the E2E's resources. The E2E's own VM is stopped first: both
# use the same tap device.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=test/e2e/provision/lib.sh
source "$here/../test/e2e/provision/lib.sh"

usage() { awk 'NR > 1 && /^#/ { sub(/^# ?/, ""); print; next } NR > 1 { exit }' "$0"; }

cmd=${1:-}
out=${2:-}
case $cmd in
  install | login) [ -n "$out" ] || { usage >&2; exit 2; } ;;
  -h | --help) usage; exit 0 ;;
  *) usage >&2; exit 2 ;;
esac
shift 2

bootconfig=ubuntu-26.04
kernel_args=""
user_data=""
meta_data=""
minutes=45
stable_minutes=3
user=""
password=""
commands=()
ssh_commands=()
presses=()
while [ $# -gt 0 ]; do
  [ $# -ge 2 ] || fail "$1 needs a value"
  case $1 in
    --bootconfig) bootconfig=$2 ;;
    --kernel-args) kernel_args=$2 ;;
    --user-data) user_data=$2 ;;
    --meta-data) meta_data=$2 ;;
    --minutes) minutes=$2 ;;
    --stable-minutes) stable_minutes=$2 ;;
    --user) user=$2 ;;
    --password) password=$2 ;;
    --command) commands+=("$2") ;;
    --ssh-command) ssh_commands+=("$2") ;;
    --press) presses+=("$2") ;;
    *) usage >&2; fail "unknown option $1" ;;
  esac
  shift 2
done

load_row ubuntu-26.04
MAC=02-00-00-ab-cd-f1
MAC_COLON=${MAC//-/:}
PROVISION=journey-provision
mkdir -p "$out"
WORK=$(cd "$out" && pwd)
LOG_DIR=$WORK

# stop_e2e_vms: stop any QEMU the E2E phases left running (same tap device).
stop_e2e_vms() {
  local pidfile
  for pidfile in "$E2E_WORK_ROOT"/*/qemu.pid; do
    [ -f "$pidfile" ] || continue
    sudo kill "$(sudo cat "$pidfile")" 2>/dev/null || true
    sudo rm -f "$pidfile" "$(dirname "$pidfile")/qemu-monitor.sock"
  done
}

# shot <name>: screendump to <out>/<name>.png.
shot() {
  qemu_running || return 0
  qemu_monitor "screendump $WORK/$1.png -f png"
  sleep 1
  sudo chmod 644 "$WORK/$1.png" 2>/dev/null || true
}

# follow <max-minutes> <stable-minutes> [prefix]: screenshot every 20 s
# (<prefix>shot-<seconds>.png) until QEMU exits or the screen stops
# changing; sets outcome. A blinking cursor makes
# two images alternate, so "unchanged" means at most two different images in
# the last stable-minutes.
follow() {
  local max=$(( $1 * 60 )) window=$(( $2 * 3 )) prefix=${3:-} start=$SECONDS t=0 name
  local sums=() last="" distinct
  outcome="timeout"
  while [ "$t" -lt "$max" ]; do
    if ! qemu_running; then outcome="qemu-exited"; break; fi
    name=${prefix}shot-$(printf %04d "$t")
    shot "$name"
    if [ -f "$WORK/$name.png" ]; then
      last=$WORK/$name.png
      sums+=("$(md5sum < "$last" | cut -d' ' -f1)")
      if [ "${#sums[@]}" -ge "$window" ]; then
        distinct=$(printf '%s\n' "${sums[@]: -$window}" | sort -u | wc -l)
        if [ "$distinct" -le 2 ]; then outcome="screen-unchanged"; break; fi
      fi
    fi
    sleep 19
    t=$((SECONDS - start))
  done
  if qemu_running; then
    shot final
  elif [ -n "$last" ]; then
    cp "$last" "$WORK/final.png"
  fi
  elapsed=$((SECONDS - start))
}

# type_text <text>: type on the VM's keyboard (US layout).
type_text() {
  local text=$1 i c k
  for ((i = 0; i < ${#text}; i++)); do
    c=${text:i:1}
    case $c in
      [a-z0-9]) k=$c ;;
      [A-Z]) k="shift-${c,,}" ;;
      ' ') k=spc ;;
      '|') k=shift-backslash ;;
      '-') k=minus ;;
      '_') k=shift-minus ;;
      '.') k='dot' ;;
      '/') k=slash ;;
      ';') k=semicolon ;;
      ':') k=shift-semicolon ;;
      '=') k=equal ;;
      '*') k=shift-8 ;;
      "'") k=apostrophe ;;
      '"') k=shift-apostrophe ;;
      '>') k=shift-dot ;;
      '<') k=shift-comma ;;
      ',') k=comma ;;
      '$') k=shift-4 ;;
      '&') k=shift-7 ;;
      *) fail "type_text: no key for '$c'" ;;
    esac
    qemu_monitor "sendkey $k"
  done
}

enter() { qemu_monitor "sendkey ret"; }

provision_resources() {
  local files='{}' name src
  for name in user-data meta-data; do
    src=$user_data
    [ "$name" = meta-data ] && src=$meta_data
    [ -n "$src" ] || continue
    cp "$src" "$WORK/served-$name"
    files=$(jq --arg n "$name" --rawfile body "$src" '. + {($n): $body}' <<<"$files")
  done
  # A ProvisionAutomation needs at least one file; with none to serve, give
  # it one nobody asks for.
  [ "$files" != '{}' ] || files='{"unused":""}'
  kc delete provision "$PROVISION" --ignore-not-found --wait=true >/dev/null
  jq -n --arg mac "$MAC" --arg bc "$bootconfig" --argjson files "$files" --arg prov "$PROVISION" '
  {apiVersion: "v1", kind: "List", items: [
    {apiVersion: "isoboot.github.io/v1alpha1", kind: "Machine",
     metadata: {name: "journey"}, spec: {mac: $mac}},
    {apiVersion: "isoboot.github.io/v1alpha1", kind: "ProvisionAutomation",
     metadata: {name: "journey-automation"}, spec: {files: $files}},
    {apiVersion: "isoboot.github.io/v1alpha1", kind: "Provision",
     metadata: {name: $prov},
     spec: {machineRef: "journey", bootConfigRef: $bc,
            provisionAutomationRef: "journey-automation"}}
  ]}' | kc apply -f - >/dev/null
  for i in $(seq 1 30); do
    [ "$(provision_phase)" = Pending ] && return 0
    [ "$i" = 30 ] && fail "Provision $PROVISION did not reach phase Pending"
    sleep 2
  done
}

do_install() {
  local args
  [ "$(kc get bootconfig "$bootconfig" -o jsonpath='{.status.phase}')" = Ready ] \
    || fail "BootConfig $bootconfig is not Ready"
  if [ -n "$kernel_args" ]; then
    kc patch bootconfig "$bootconfig" --type=merge \
      -p "$(jq -n --arg a "$kernel_args" '{spec: {kernelArgs: $a}}')" >/dev/null
  fi
  args=$(kc get bootconfig "$bootconfig" -o jsonpath='{.spec.kernelArgs}')
  printf '%s\n' "$args" > "$WORK/kernel-args.txt"
  provision_resources
  stop_e2e_vms
  qemu_stop
  rm -f "$WORK"/*shot-*.png "$WORK"/before-press-*.png "$WORK/final.png"
  cp "$(ovmf_vars)" "$WORK/ovmf-vars.fd"
  rm -f "$WORK/disk.qcow2"
  qemu-img create -q -f qcow2 "$WORK/disk.qcow2" 20G
  sudo sysctl -qw net.bridge.bridge-nf-call-iptables=0
  local seen
  seen=$(nginx_access_log | wc -l)
  local t0=$SECONDS
  qemu_start "$WORK/serial.log" pxe
  follow "$minutes" "$stable_minutes"
  local i=0 key
  for key in "${presses[@]}"; do
    [ "$outcome" = screen-unchanged ] || break
    i=$((i + 1))
    mv "$WORK/final.png" "$WORK/before-press-$i.png"
    log "screen unchanged; pressing $key"
    qemu_monitor "sendkey $key"
    follow "$minutes" "$stable_minutes" "press$i-"
  done
  elapsed=$((SECONDS - t0))
  qemu_stop
  # This boot's requests: the access log lines written since it started.
  nginx_access_log | tail -n +"$((seen + 1))" > "$WORK/nginx-access.log"
  printf 'outcome=%s\nseconds=%s\nbootconfig=%s\nkernel_args=%s\n' \
    "$outcome" "$elapsed" "$bootconfig" "$args" > "$WORK/outcome.txt"
  log "install step done: $outcome after ${elapsed}s; $WORK/final.png"
}

do_login() {
  local i n=0 c
  [ -f "$WORK/disk.qcow2" ] || fail "no disk in $WORK: run install first"
  [ -n "$user" ] && [ -n "$password" ] || fail "login needs --user and --password"
  # The disk boots on its own; no Pending Provision may send it to the installer.
  kc delete provision "$PROVISION" --ignore-not-found --wait=true >/dev/null
  stop_e2e_vms
  qemu_stop
  rm -f "$WORK"/login-*.png
  qemu_start "$WORK/serial-disk.log" disk
  # First boot: cloud-init creates the user, then prints over the console.
  local saved=$WORK
  follow 15 2
  [ "$outcome" = screen-unchanged ] || fail "installed system: screen still changing after 15 min ($outcome)"
  mv "$saved/final.png" "$saved/login-0-boot.png"
  rm -f "$saved"/shot-*.png
  # An empty name redraws the prompt below the boot messages.
  enter
  sleep 3
  shot login-1-prompt
  type_text "$user"
  enter
  sleep 3
  type_text "$password"
  enter
  sleep 8
  shot login-2-shell
  for c in "${commands[@]}"; do
    n=$((n + 1))
    type_text "clear"
    enter
    sleep 1
    type_text "$c"
    enter
    sleep 5
    shot "login-$((n + 2))-command"
    printf '%s\n' "$c" > "$WORK/login-$((n + 2))-command.txt"
  done
  for i in 1 2; do qemu_monitor "sendkey ctrl-d"; sleep "$i"; done
  [ "${#ssh_commands[@]}" -eq 0 ] || ssh_login
  qemu_stop
  log "login step done: $WORK/login-*.png"
}

# ssh_login: log in over SSH as $user with $password (password
# authentication only; ssh reads the password from an askpass helper) and
# run each --ssh-command.
ssh_login() {
  local ip="" i n=0 c
  for i in $(seq 1 60); do
    ip=$(dhcp_ip)
    [ -n "$ip" ] && break
    sleep 5
  done
  [ -n "$ip" ] || fail "no DHCP lease for $MAC_COLON"
  # shellcheck disable=SC2016 # $JOURNEY_PASSWORD expands when ssh runs it
  printf '#!/bin/sh\nprintf "%%s\\n" "$JOURNEY_PASSWORD"\n' > "$WORK/askpass"
  chmod 700 "$WORK/askpass"
  local opts=(-o StrictHostKeyChecking=no -o "UserKnownHostsFile=$WORK/known_hosts"
    -o ConnectTimeout=10 -o PubkeyAuthentication=no
    -o PreferredAuthentications=password -o NumberOfPasswordPrompts=1)
  sshd_auth_methods "$user@$ip" > "$WORK/ssh-auth-methods.txt"
  for c in "${ssh_commands[@]}"; do
    n=$((n + 1))
    printf '$ ssh %s@%s %s\n' "$user" "$ip" "$c" > "$WORK/ssh-$n.txt"
    JOURNEY_PASSWORD=$password SSH_ASKPASS=$WORK/askpass SSH_ASKPASS_REQUIRE=force \
      setsid -w ssh "${opts[@]}" "$user@$ip" "$c" < /dev/null >> "$WORK/ssh-$n.txt" 2>&1 \
      || echo "(exit status $?)" >> "$WORK/ssh-$n.txt"
  done
  rm -f "$WORK/askpass"
  log "ssh login done: $WORK/ssh-*.txt"
}

case $cmd in
  install) do_install ;;
  login) do_login ;;
esac
