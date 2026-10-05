#!/usr/bin/env bash
# Tear down what the phases set up, so the next row starts clean on the same
# host: the VM, k3s, the site network and the data directory (the squid cache
# under it is kept). Logs under $E2E_WORK_ROOT stay. CI runners are thrown
# away instead. Usage: cleanup.sh
set -uo pipefail
# shellcheck source=test/e2e/provision/lib.sh
source "$(dirname "$0")/lib.sh"
set +e

for pidfile in "$E2E_WORK_ROOT"/*/qemu.pid; do
  [ -f "$pidfile" ] && sudo kill "$(sudo cat "$pidfile")" 2>/dev/null
  sudo rm -f "$pidfile" "$(dirname "$pidfile")/qemu-monitor.sock"
done
sudo ip link del "$TAP" 2>/dev/null
[ -x /usr/local/bin/k3s-uninstall.sh ] && sudo /usr/local/bin/k3s-uninstall.sh >/dev/null 2>&1
sudo docker rm -f "$DHCP_CONTAINER" >/dev/null 2>&1
sudo rm -f /var/run/netns/ns1
sudo ip link del veth-ns1-br 2>/dev/null
sudo ip link del "$BRIDGE" 2>/dev/null
sudo iptables -t nat -D POSTROUTING -s "$SUBNET" ! -o "$BRIDGE" -j MASQUERADE 2>/dev/null
sudo iptables -D FORWARD -i "$BRIDGE" -j ACCEPT 2>/dev/null
sudo iptables -D FORWARD -o "$BRIDGE" -j ACCEPT 2>/dev/null
sudo sysctl -qw net.bridge.bridge-nf-call-iptables=1 2>/dev/null
if [ -d "$DATA_DIR" ]; then
  sudo find "$DATA_DIR" -mindepth 1 -maxdepth 1 ! -name squid -exec rm -rf {} +
fi
log "cleaned up"
exit 0
