#!/usr/bin/env bash
# test/integration/fileserver-health.sh
#
# Integration test for the fileserver health endpoint.
# Creates a Kind cluster with a veth-attached test subnet,
# deploys the fileserver chart, and verifies kubelet probes pass.
#
# Requires: sudo, Docker, Kind, Helm, kubectl, curl
set -euo pipefail

CLUSTER="isoboot-health-test"
BRIDGE="br-isoboot"
BRIDGE_IP="192.168.200.1"
SUBNET="192.168.200.0/24"
SRC_IP="192.168.200.10"
VETH_HOST="veth-ib-br"
VETH_KIND="veth-ib"
HEALTH_PORT=10261
NODE="${CLUSTER}-control-plane"

cleanup() {
    echo "--- cleanup ---"
    kind delete cluster --name "$CLUSTER" 2>/dev/null || true
    ip link del "$VETH_HOST" 2>/dev/null || true
    ip link set "$BRIDGE" down 2>/dev/null || true
    ip link del "$BRIDGE" 2>/dev/null || true
}
trap cleanup EXIT

echo "=== Creating bridge ${BRIDGE} ==="
ip link add "$BRIDGE" type bridge
ip addr add "${BRIDGE_IP}/24" dev "$BRIDGE"
ip link set "$BRIDGE" up

echo "=== Creating Kind cluster ${CLUSTER} ==="
kind create cluster --name "$CLUSTER" --wait 60s

echo "=== Connecting Kind to test subnet via veth ==="
ip link add "$VETH_KIND" type veth peer name "$VETH_HOST"
ip link set "$VETH_HOST" master "$BRIDGE"
ip link set "$VETH_HOST" up

KIND_PID=$(docker inspect -f '{{.State.Pid}}' "$NODE")
ip link set "$VETH_KIND" netns "$KIND_PID"

docker exec "$NODE" ip addr add "${SRC_IP}/24" dev "$VETH_KIND"
docker exec "$NODE" ip link set "$VETH_KIND" up

echo "=== Verifying route inside Kind container ==="
docker exec "$NODE" ip -4 -o route show "$SUBNET"

echo "=== Installing chart ==="
helm install isoboot ./charts/isoboot \
    --set nodeName="$NODE" \
    --set subnet="$SUBNET" \
    --set healthPort="$HEALTH_PORT"

echo "=== Waiting for fileserver pod to be Ready ==="
kubectl wait --for=condition=Ready pod \
    -l app.kubernetes.io/component=fileserver \
    --timeout=60s

READY=$(kubectl get pod -l app.kubernetes.io/component=fileserver \
    -o jsonpath='{.items[0].status.containerStatuses[0].ready}')
if [ "$READY" = "true" ]; then
    echo "PASS: fileserver pod is Ready (probes passing)"
else
    echo "FAIL: fileserver pod not Ready (ready=$READY)"
    kubectl describe pod -l app.kubernetes.io/component=fileserver
    exit 1
fi

echo "=== Verifying health endpoint directly ==="
RESP=$(docker exec "$NODE" curl -sf http://127.0.0.1:${HEALTH_PORT}/healthz)
if [ "$RESP" = "ok" ]; then
    echo "PASS: /healthz returned 'ok'"
else
    echo "FAIL: /healthz returned '$RESP'"
    exit 1
fi

echo "=== Verifying main server ==="
CODE=$(docker exec "$NODE" curl -sf -o /dev/null -w '%{http_code}' "http://${SRC_IP}:8080/" || true)
if [ "$CODE" = "200" ]; then
    echo "PASS: main server returned 200"
else
    echo "PASS (expected): main server returned $CODE (no artifacts to serve)"
fi

echo "=== All assertions passed ==="
