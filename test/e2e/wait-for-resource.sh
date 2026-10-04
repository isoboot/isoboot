#!/usr/bin/env bash
# Wait for a custom resource to reach Ready phase, failing fast on repeated errors.
# Usage: wait-for-resource.sh [-n namespace] <resource-type> <resource-name> [max-attempts] [interval-seconds]
set -euo pipefail

NS_FLAG=()
if [ "${1:-}" = "-n" ]; then
  NS_FLAG=(-n "$2")
  shift 2
fi

RESOURCE_TYPE=${1:?usage: wait-for-resource.sh [-n namespace] <resource-type> <resource-name>}
RESOURCE_NAME=${2:?usage: wait-for-resource.sh [-n namespace] <resource-type> <resource-name>}
MAX_ATTEMPTS=${3:-60}
INTERVAL=${4:-10}

error_count=0
for i in $(seq 1 "$MAX_ATTEMPTS"); do
  phase=$(kubectl "${NS_FLAG[@]}" get "$RESOURCE_TYPE" "$RESOURCE_NAME" -o jsonpath='{.status.phase}' 2>/dev/null || true)
  echo "Attempt $i: phase=$phase"
  if [ "$phase" = "Ready" ]; then
    echo "$RESOURCE_TYPE reached Ready phase"
    exit 0
  fi
  if [ "$phase" = "Error" ]; then
    error_count=$((error_count + 1))
    msg=$(kubectl "${NS_FLAG[@]}" get "$RESOURCE_TYPE" "$RESOURCE_NAME" -o jsonpath='{.status.message}')
    echo "$RESOURCE_TYPE is in Error phase (count=$error_count): $msg"
    if [ "$error_count" -ge 3 ]; then
      echo "Failing fast after $error_count consecutive errors"
      kubectl "${NS_FLAG[@]}" get "$RESOURCE_TYPE" "$RESOURCE_NAME" -o yaml
      kubectl -n isoboot-system logs deployment/isoboot-controller-manager
      exit 1
    fi
  else
    error_count=0
  fi
  sleep "$INTERVAL"
done
echo "Timed out waiting for Ready phase"
kubectl "${NS_FLAG[@]}" get "$RESOURCE_TYPE" "$RESOURCE_NAME" -o yaml
kubectl -n isoboot-system logs deployment/isoboot-controller-manager
exit 1
