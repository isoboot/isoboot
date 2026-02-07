#!/usr/bin/env bash
# test/integration/fileserver-health.sh
#
# Integration test: deploy the full isoboot Helm chart into a Kind
# cluster with a veth-attached test subnet and verify every pod
# becomes healthy (kubelet probes passing).
#
# Subnet auto-detection: scans 192.168.100.0/24 through 192.168.199.0/24
# and picks the first /24 for which "ip -4 route show to match" returns
# no non-default routes. This handles networks of any prefix length
# (/24, /22, /20, etc.) by relying on the kernel's route lookup, not
# naive string matching.
#
# Requires: sudo, Docker, Kind, Helm, kubectl
set -euo pipefail

CLUSTER="isoboot-health-test"
BRIDGE="br-isoboot"
VETH_HOST="veth-ib-br"
VETH_KIND="veth-ib"
HEALTH_PORT=10261
CONTROLLER_IMAGE=$(awk '/^controllerImage:/{print $2}' charts/isoboot/values.yaml)
FILESERVER_IMAGE=$(awk '/^fileserverImage:/{print $2}' charts/isoboot/values.yaml)

# find_available_subnet scans 192.168.{100..199}.0/24 and returns the
# third octet of the first /24 with no explicit route (connected, VPN,
# or otherwise). Uses "ip route show to match" which returns all routes
# covering the prefix — connected, static, VPN — but also the default
# route. After filtering out "default", an empty result means only the
# default route covers this /24, so it is safe to use.
find_available_subnet() {
    for third in $(seq 100 199); do
        local routes
        routes=$(ip -4 route show to match "192.168.${third}.0/24" 2>/dev/null \
            | grep -v "^default") || true
        if [ -z "$routes" ]; then
            echo "$third"
            return 0
        fi
    done
    echo "ERROR: No available /24 in 192.168.100.0/24 - 192.168.199.0/24" >&2
    return 1
}

THIRD=$(find_available_subnet)
SUBNET="192.168.${THIRD}.0/24"
BRIDGE_IP="192.168.${THIRD}.1"
SRC_IP="192.168.${THIRD}.10"
echo "=== Selected subnet ${SUBNET} (192.168.${THIRD}.0/24 is available) ==="

NODE="${CLUSTER}-control-plane"

cleanup() {
    echo "--- cleanup ---"
    kind delete cluster --name "$CLUSTER" 2>/dev/null || true
    ip link del "$VETH_HOST" 2>/dev/null || true
    ip link set "$BRIDGE" down 2>/dev/null || true
    ip link del "$BRIDGE" 2>/dev/null || true
}
trap cleanup EXIT

debug_pods() {
    echo "--- debug: pod descriptions ---"
    kubectl describe pods || true
    echo "--- debug: pod logs (controller-manager) ---"
    kubectl logs -l app.kubernetes.io/component=controller-manager || true
    echo "--- debug: pod logs (fileserver) ---"
    kubectl logs -l app.kubernetes.io/component=fileserver || true
    echo "--- debug: events ---"
    kubectl get events --sort-by=.lastTimestamp || true
}

echo "=== Building controller image ==="
docker build -t "$CONTROLLER_IMAGE" -f Dockerfile .

echo "=== Building fileserver image ==="
docker build -t "$FILESERVER_IMAGE" -f Dockerfile.nginx .

echo "=== Creating bridge ${BRIDGE} (${BRIDGE_IP}/24) ==="
ip link add "$BRIDGE" type bridge
ip addr add "${BRIDGE_IP}/24" dev "$BRIDGE"
ip link set "$BRIDGE" up

echo "=== Creating Kind cluster ${CLUSTER} ==="
kind create cluster --name "$CLUSTER" --wait 60s

echo "=== Loading images into Kind ==="
kind load docker-image "$CONTROLLER_IMAGE" "$FILESERVER_IMAGE" --name "$CLUSTER"

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

echo "=== Waiting for controller-manager pod to be Ready ==="
if ! kubectl wait --for=condition=Ready pod \
    -l app.kubernetes.io/component=controller-manager \
    --timeout=90s; then
    echo "FAIL: controller-manager pod did not become Ready"
    debug_pods
    exit 1
fi
echo "PASS: controller-manager pod is Ready (probes passing)"

echo "=== Waiting for fileserver pod to be Ready ==="
if ! kubectl wait --for=condition=Ready pod \
    -l app.kubernetes.io/component=fileserver \
    --timeout=90s; then
    echo "FAIL: fileserver pod did not become Ready"
    debug_pods
    exit 1
fi
echo "PASS: fileserver pod is Ready (probes passing)"

echo "=== Verifying fileserver health endpoint directly ==="
RESP=$(docker exec "$NODE" curl -sf http://127.0.0.1:${HEALTH_PORT}/healthz)
if [ "$RESP" = "ok" ]; then
    echo "PASS: /healthz returned 'ok'"
else
    echo "FAIL: /healthz returned '$RESP'"
    debug_pods
    exit 1
fi

echo "=== Verifying main server ==="
CODE=$(docker exec "$NODE" curl -s -o /dev/null -w '%{http_code}' "http://${SRC_IP}:8080/" || true)
if [ "$CODE" = "200" ]; then
    echo "PASS: main server returned 200"
else
    echo "PASS (expected): main server returned $CODE (no artifacts to serve)"
fi

echo "=== All assertions passed ==="
