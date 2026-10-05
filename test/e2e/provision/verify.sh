#!/usr/bin/env bash
# Phase 8: boot the installed disk and check the result over SSH: key login,
# no password login, hostname, the injected SSH host keys, OS identity,
# machine-id, root on the virtio disk and, for the firmware row, the r8169
# driver. Rows with "expect": "stall" have nothing to verify.
# Usage: verify.sh <row-id>
set -euo pipefail
# shellcheck source=test/e2e/provision/lib.sh
source "$(dirname "$0")/lib.sh"
load_row "${1:-}"

if [ "$(row expect)" = stall ]; then
  log "row $ROW_ID is expected not to install; nothing to verify"
  exit 0
fi

keys=$WORK/keys
serial=$LOG_DIR/serial-disk.log
ssh_opts=(-i "$keys/login" -o StrictHostKeyChecking=no -o "UserKnownHostsFile=$WORK/known_hosts"
  -o ConnectTimeout=5 -o BatchMode=yes)

qemu_running && qemu_stop
qemu_start "$serial" disk

vm_ip=""
for i in $(seq 1 96); do
  ip=$(dhcp_ip)
  if [ -n "$ip" ] && ssh "${ssh_opts[@]}" "isoboot@$ip" true 2>/dev/null; then
    vm_ip=$ip
    break
  fi
  if [ "$i" = 96 ]; then
    screendump disk-boot
    echo "--- last 60 lines of $serial ---"
    tail -n 60 "$serial" || true
    fail "installed system not reachable over SSH within 8 minutes (last lease: ${ip:-none})"
  fi
  sleep 5
done
echo "$vm_ip" > "$WORK/vm-ip"
# shellcheck disable=SC2029 # the commands are meant to run on the VM
on_vm() { ssh "${ssh_opts[@]}" "isoboot@$vm_ip" "$@"; }

# (a) key login
on_vm true || fail "SSH key login failed"
pass "SSH key login as isoboot@$vm_ip"

# (b) password login is refused
assert_no_password_auth "isoboot@$vm_ip"

# (c) hostname
actual=$(on_vm hostname)
[ "$actual" = "$(row hostname)" ] || fail "hostname: expected $(row hostname), got $actual"
pass "hostname $actual"

# (d) the injected SSH host keys
assert_host_key "$vm_ip" ecdsa "$keys/ssh_host_ecdsa_key.pub"
assert_host_key "$vm_ip" ed25519 "$keys/ssh_host_ed25519_key.pub"
assert_host_key "$vm_ip" rsa "$keys/ssh_host_rsa_key.pub"
pass "ecdsa, ed25519 and rsa host keys match the injected ones"

# (e) OS identity
# shellcheck disable=SC2016
os_id=$(on_vm '. /etc/os-release && echo "$ID"')
# shellcheck disable=SC2016
os_version=$(on_vm '. /etc/os-release && echo "$VERSION_ID"')
[ "$os_id" = "$(row os_id)" ] || fail "os-release ID: expected $(row os_id), got $os_id"
[ "$os_version" = "$(row version_id)" ] || fail "os-release VERSION_ID: expected $(row version_id), got $os_version"
pass "OS $os_id $os_version"

# (f) machine-id, 32 hex digits and a newline
mid=$(on_vm 'head -c 32 /etc/machine-id')
mid_len=$(on_vm 'wc -c < /etc/machine-id')
[ "$mid" = "$(cat "$keys/machine-id")" ] || fail "machine-id: expected $(cat "$keys/machine-id"), got $mid"
[ "$mid_len" = 33 ] || fail "/etc/machine-id should be 33 bytes, got $mid_len"
pass "machine-id $mid"

# (g) root on the virtio disk
root_dev=$(on_vm 'findmnt -n -o SOURCE /')
case $root_dev in
  /dev/vda*) pass "root on $root_dev" ;;
  *) fail "root is not on the virtio disk: $root_dev" ;;
esac

# (h) firmware row: the r8169 driver drove the RTL8168
if [ "$(row verify_rtl_firmware)" = true ]; then
  grep -qi r8169 "$serial" "$LOG_DIR/serial-install.log" \
    || fail "r8169 driver did not detect the RTL8168 device"
  pass "r8169 detected the RTL8168 device"
fi

assert_no_restarts
pass "all checks for $ROW_ID"
