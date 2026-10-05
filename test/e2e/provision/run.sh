#!/usr/bin/env bash
# Provision E2E for one row of rows.json.
#
#   run.sh <row-id>             all phases in order, then collect logs and
#                               clean up (set E2E_KEEP=1 to leave it running)
#   run.sh <row-id> <phase> [args]  only that phase (CI runs one per step)
#
# Phases: host-setup k3s network images helm-install apply-row boot-install
# verify collect-logs (collect-logs takes --print as an extra argument).
# Needs an x86-64 Ubuntu host with KVM, passwordless sudo and internet access.
# The host is changed for real (packages, k3s, a bridge, iptables, /data/isoboot),
# so the scripts run only on a GitHub Actions runner, in a VM made by
# hack/e2e-local.sh (marker /etc/isoboot-e2e-vm) or with E2E_ALLOW_THIS_HOST=1.
# Environment: see lib.sh (E2E_IMAGES, E2E_VERSION, E2E_WORK_ROOT, E2E_QEMU_CACHE).
set -euo pipefail
dir=$(cd "$(dirname "$0")" && pwd)
phases=(host-setup k3s network images helm-install apply-row boot-install verify)

row_id=${1:-}
if [ -z "$row_id" ] || [ "$row_id" = -h ] || [ "$row_id" = --help ]; then
  sed -n '2,13p' "$0" | sed 's/^# \{0,1\}//'
  echo "Rows: $(jq -r '[.[].id] | join(" ")' "$dir/rows.json")"
  exit 2
fi
shift

if [ $# -gt 0 ]; then
  phase=$1
  shift
  case " ${phases[*]} collect-logs " in
    *" $phase "*) exec "$dir/$phase.sh" "$row_id" "$@" ;;
    *) echo "FAIL: unknown phase '$phase' (phases: ${phases[*]} collect-logs)" >&2; exit 2 ;;
  esac
fi

# shellcheck source=test/e2e/provision/lib.sh
source "$dir/lib.sh"
load_row "$row_id"
"$dir/cleanup.sh"
rm -rf "$WORK"
mkdir -p "$LOG_DIR"

status=0
started=$(date +%s)
for phase in "${phases[@]}"; do
  log "===== $ROW_ID: $phase ====="
  if ! "$dir/$phase.sh" "$ROW_ID" 2>&1 | tee -a "$LOG_DIR/run.log"; then
    status=1
    log "===== $ROW_ID: $phase FAILED ====="
    break
  fi
done
"$dir/collect-logs.sh" "$ROW_ID" >/dev/null
[ "${E2E_KEEP:-0}" = 1 ] || "$dir/cleanup.sh"
elapsed=$(( $(date +%s) - started ))
if [ "$status" = 0 ]; then
  log "RESULT $ROW_ID: PASS in $((elapsed / 60)) min; logs in $LOG_DIR"
else
  log "RESULT $ROW_ID: FAIL in $((elapsed / 60)) min (phase $phase); logs in $LOG_DIR"
fi
exit "$status"
