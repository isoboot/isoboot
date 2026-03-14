#!/usr/bin/env bash
# Wait for a BootConfig to reach Ready phase, failing fast on repeated errors.
# Usage: wait-for-bootconfig.sh [-n namespace] <bootconfig-name> [max-attempts] [interval-seconds]
set -euo pipefail

NS_FLAG=()
if [ "${1:-}" = "-n" ]; then
  NS_FLAG=(-n "$2")
  shift 2
fi

CONFIG=${1:?usage: wait-for-bootconfig.sh [-n namespace] <bootconfig-name>}
MAX_ATTEMPTS=${2:-60}
INTERVAL=${3:-10}

error_count=0
for i in $(seq 1 "$MAX_ATTEMPTS"); do
  phase=$(kubectl "${NS_FLAG[@]}" get bootconfig "$CONFIG" -o jsonpath='{.status.phase}' 2>/dev/null || true)
  echo "Attempt $i: phase=$phase"
  if [ "$phase" = "Ready" ]; then
    echo "BootConfig reached Ready phase"
    exit 0
  fi
  if [ "$phase" = "Error" ]; then
    error_count=$((error_count + 1))
    msg=$(kubectl "${NS_FLAG[@]}" get bootconfig "$CONFIG" -o jsonpath='{.status.message}')
    echo "BootConfig is in Error phase (count=$error_count): $msg"
    if [ "$error_count" -ge 3 ]; then
      echo "Failing fast after $error_count consecutive errors"
      kubectl "${NS_FLAG[@]}" get bootconfig "$CONFIG" -o yaml
      kubectl -n isoboot-system logs deployment/isoboot-controller-manager
      exit 1
    fi
  else
    error_count=0
  fi
  sleep "$INTERVAL"
done
echo "Timed out waiting for Ready phase"
kubectl "${NS_FLAG[@]}" get bootconfig "$CONFIG" -o yaml
kubectl -n isoboot-system logs deployment/isoboot-controller-manager
exit 1
