#!/usr/bin/env bash
# Phase 9: gather diagnostics into $E2E_WORK_ROOT/<row-id>/logs (serial
# consoles are written there already). Never fails. With --print, also print
# the tail of each file, for CI job logs.
# Usage: collect-logs.sh <row-id> [--print]
set -uo pipefail
# shellcheck source=test/e2e/provision/lib.sh
source "$(dirname "$0")/lib.sh"
set +e
load_row "${1:-}"

screendump final
{
  ip addr show "$BRIDGE"
  bridge link show master "$BRIDGE"
  sudo ip netns exec ns1 ip addr
  sudo iptables -S FORWARD
  sudo iptables -t nat -S POSTROUTING
} > "$LOG_DIR/network.txt" 2>&1
# shellcheck disable=SC2024 # sudo reads; the log file is ours
sudo docker exec "$DHCP_CONTAINER" cat /tmp/dnsmasq.log > "$LOG_DIR/site-dhcp.log" 2>&1
kc get bootartifact,bootconfig,machine,provision,provisionautomation -o yaml > "$LOG_DIR/resources.yaml" 2>&1
kc get pods -o wide > "$LOG_DIR/pods.txt" 2>&1
kc describe pods > "$LOG_DIR/pods-describe.txt" 2>&1
kc get events --sort-by=.lastTimestamp > "$LOG_DIR/events.txt" 2>&1
kubectl -n kube-system get pods -o wide >> "$LOG_DIR/pods.txt" 2>&1
for component in controller httpd nginx dnsmasq squid nfsd; do
  kc logs -l "app.kubernetes.io/component=$component" --all-containers --prefix --tail=-1 \
    > "$LOG_DIR/pod-$component.log" 2>&1
done
nginx_access_log > "$LOG_DIR/nginx-access.log"
squid=$(kc get pod -l app.kubernetes.io/component=squid -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
[ -n "$squid" ] && kc exec "$squid" -- cat /var/log/squid/access.log > "$LOG_DIR/squid-access.log" 2>&1
# shellcheck disable=SC2024
sudo find "$DATA_DIR" -maxdepth 4 \( -type f -o -type l \) -not -path '*/squid/*' > "$LOG_DIR/data-dir.txt" 2>&1
sudo chown -R "$(id -u):$(id -g)" "$LOG_DIR" 2>/dev/null
log "logs for $ROW_ID are in $LOG_DIR"

if [ "${2:-}" = --print ]; then
  for f in "$LOG_DIR"/*; do
    case $f in *.ppm) continue ;; esac
    echo "=================== $(basename "$f") (last 150 lines) ==================="
    tail -n 150 "$f"
  done
fi
exit 0
