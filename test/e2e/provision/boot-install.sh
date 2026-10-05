#!/usr/bin/env bash
# Phase 7: PXE-boot the VM (UEFI, KVM) and follow the install.
# Checks: a DHCP lease, kernel and initrd fetched by iPXE through nginx, the
# Provision going InProgress then Complete, and the installer rebooting by
# itself (QEMU runs with -no-reboot, so it exits). The VM is never powered off
# under a running installer: anaconda still relabels and syncs after %post.
# NFS rows (BootConfigs in iso mode) also prove the NFS path: the guest's
# kernel command line mounts NFS and has no url= or toram, nfsd logged the
# guest's mount of /<bootconfig>, and nginx served no .iso.
# A row with "expect": "stall" (Debian without NIC firmware) must stay Pending
# and never fetch its automation files: the installer had no network.
# Every row ends by checking that no isoboot pod restarted.
# Usage: boot-install.sh <row-id>
set -euo pipefail
# shellcheck source=test/e2e/provision/lib.sh
source "$(dirname "$0")/lib.sh"
load_row "${1:-}"

bootconfig=$(row bootconfig)
serial=$LOG_DIR/serial-install.log

# The NFS checks follow the BootConfig's real mode, not only the row's flag.
iso_artifact=$(kc get bootconfig "$bootconfig" -o jsonpath='{.spec.iso.artifactRef}')
if [ -n "$iso_artifact" ] && [ "$(row nfs)" != true ]; then
  fail "BootConfig $bootconfig is in iso mode (NFS), but row $ROW_ID does not have \"nfs\": true"
fi
if [ -z "$iso_artifact" ] && [ "$(row nfs)" = true ]; then
  fail "row $ROW_ID has \"nfs\": true, but BootConfig $bootconfig is not in iso mode"
fi

cp "$(ovmf_vars)" "$WORK/ovmf-vars.fd"
rm -f "$WORK/disk.qcow2"
qemu-img create -q -f qcow2 "$WORK/disk.qcow2" 20G
# Bridged VM traffic must not go through k3s's iptables FORWARD rules.
sudo sysctl -qw net.bridge.bridge-nf-call-iptables=0
qemu_start "$serial" pxe

# tail_serial: the end of the install console, for failure messages.
tail_serial() { echo "--- last 40 lines of $serial ---"; tail -n 40 "$serial" 2>/dev/null || true; }

# ── DHCP and PXE ───────────────────────────────────────────────────
vm_ip=""
for i in $(seq 1 120); do
  vm_ip=$(dhcp_ip)
  [ -n "$vm_ip" ] && break
  [ "$i" = 120 ] && { tail_serial; fail "VM ($MAC_COLON) got no DHCP lease within 10 minutes"; }
  sleep 5
done
pass "VM got DHCP lease $vm_ip"

for i in $(seq 1 60); do
  nginx_access_log | grep "GET /static/$bootconfig/$(row kernel_path) " | grep '" 200 ' | grep 'iPXE/' >/dev/null && break
  [ "$i" = 60 ] && { tail_serial; fail "no iPXE HTTP 200 for /static/$bootconfig/$(row kernel_path)"; }
  sleep 5
done
pass "iPXE fetched the kernel"

for i in $(seq 1 60); do
  nginx_access_log | grep "GET /static/$bootconfig/$(row initrd_path) " | grep '" 200 ' | grep 'iPXE/' >/dev/null && break
  [ "$i" = 60 ] && { tail_serial; fail "no iPXE HTTP 200 for /static/$bootconfig/$(row initrd_path)"; }
  sleep 5
done
pass "iPXE fetched the initrd"

# ── Negative row: the install must not complete ────────────────────
if [ "$(row expect)" = stall ]; then
  assert_stalls 50 10
  screendump stall
  tail_serial
  qemu_stop
  assert_no_restarts
  exit 0
fi

# ── Install ────────────────────────────────────────────────────────
# wait_phase <phase...> <minutes>: wait until the Provision is in one of the
# phases; fail early if QEMU exits first (the installer crashed or rebooted).
wait_phase() {
  local minutes=${*: -1} want=("${@:1:$#-1}") phase i p
  for i in $(seq 1 $((minutes * 6))); do
    phase=$(provision_phase)
    for p in "${want[@]}"; do
      [ "$phase" = "$p" ] && return 0
    done
    if ! qemu_running; then
      # The phase may have changed just before the installer rebooted.
      phase=$(provision_phase)
      for p in "${want[@]}"; do
        [ "$phase" = "$p" ] && return 0
      done
      tail_serial
      fail "QEMU exited while the Provision was '$phase', waiting for ${want[*]}"
    fi
    [ $((i % 6)) = 1 ] && log "phase=${phase:-?}, waiting for ${want[*]} ($((i / 6))/$minutes min)"
    sleep 10
  done
  screendump "wait-${want[0]}"
  tail_serial
  fail "Provision is '$phase' after $minutes minutes; expected ${want[*]}"
}

wait_phase InProgress Complete 25
pass "Provision InProgress"
wait_phase Complete 45
pass "Provision Complete"

for i in $(seq 1 90); do
  qemu_running || break
  [ "$i" = 90 ] && { screendump no-reboot; tail_serial; fail "installer did not reboot within 15 minutes after Complete"; }
  sleep 10
done
sudo rm -f "$WORK/qemu.pid" "$WORK/qemu-monitor.sock"
pass "installer finished and rebooted (QEMU exited)"

# ── NFS rows: prove the installer ran from the NFS export ──────────
if [ "$(row nfs)" = true ]; then
  assert_nfs_cmdline "$serial" "$bootconfig"
  kc logs -l app.kubernetes.io/component=nfsd --tail=-1 > "$LOG_DIR/nfsd.log" 2>&1 || true
  # The installer's own DHCP client may get another address than iPXE did,
  # so accept any client on the site subnet (the VM is the only one there).
  grep 'msg=mount ' "$LOG_DIR/nfsd.log" | grep -F 'client=192.168.101.' | grep "export=/$bootconfig\$" >/dev/null \
    || { cat "$LOG_DIR/nfsd.log"; fail "nfsd log has no mount of /$bootconfig by the VM"; }
  pass "nfsd: $(grep 'msg=mount ' "$LOG_DIR/nfsd.log" | grep "export=/$bootconfig\$" | sed -n 1p)"
  nginx_access_log > "$LOG_DIR/nginx-access.log"
  grep -q "GET /static/$bootconfig/vmlinuz " "$LOG_DIR/nginx-access.log" \
    || fail "nginx did not serve /static/$bootconfig/vmlinuz"
  grep -q "GET /static/$bootconfig/initrd " "$LOG_DIR/nginx-access.log" \
    || fail "nginx did not serve /static/$bootconfig/initrd"
  if grep -E '"[A-Z]+ [^ ]*\.iso[ ?]' "$LOG_DIR/nginx-access.log"; then
    fail "nginx served an .iso (above); the installer must not download the ISO"
  fi
  pass "nginx served vmlinuz and initrd and no .iso"
fi

assert_no_restarts
