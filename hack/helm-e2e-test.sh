#!/usr/bin/env bash
# Helm E2E Integration Tests for isoboot chart
# Runs against a Kind cluster to validate chart deployment behavior.
#
# Usage: ./hack/helm-e2e-test.sh <cluster-name>
#
# Prerequisites:
# - Kind cluster running (created by 'make helm-test-e2e')
# - kubectl configured for the Kind cluster
# - helm installed

set -euo pipefail

CLUSTER_NAME="${1:-isoboot-helm-e2e}"
NAMESPACE="isoboot-test"
RELEASE_NAME="isoboot-test"
CHART_PATH="charts/isoboot"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m' # No Color

# Test counters
TESTS_PASSED=0
TESTS_FAILED=0
TOTAL_TESTS=0

# Preflight checks
command -v docker >/dev/null 2>&1 || {
    echo -e "${RED}docker is required but not found. This script uses 'docker exec' to configure Kind nodes.${NC}"
    exit 1
}
command -v kubectl >/dev/null 2>&1 || {
    echo -e "${RED}kubectl is required but not found.${NC}"
    exit 1
}
command -v helm >/dev/null 2>&1 || {
    echo -e "${RED}helm is required but not found.${NC}"
    exit 1
}

# Save original kubectl context and switch to Kind cluster
ORIGINAL_CONTEXT=$(kubectl config current-context 2>/dev/null || echo "")
kubectl config use-context "kind-${CLUSTER_NAME}" >/dev/null 2>&1 || {
    echo -e "${RED}Failed to switch to Kind cluster context: kind-${CLUSTER_NAME}${NC}"
    exit 1
}

log_info() {
    echo -e "${YELLOW}[INFO]${NC} $1"
}

log_pass() {
    echo -e "${GREEN}[PASS]${NC} $1"
    TESTS_PASSED=$((TESTS_PASSED + 1))
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
}

log_fail() {
    echo -e "${RED}[FAIL]${NC} $1"
    TESTS_FAILED=$((TESTS_FAILED + 1))
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
}

cleanup() {
    log_info "Cleaning up test resources..."
    helm uninstall "${RELEASE_NAME}" -n "${NAMESPACE}" 2>/dev/null || true
    kubectl delete namespace "${NAMESPACE}" --ignore-not-found=true --wait=false 2>/dev/null || true
    # Restore original kubectl context
    if [[ -n "${ORIGINAL_CONTEXT}" ]]; then
        kubectl config use-context "${ORIGINAL_CONTEXT}" >/dev/null 2>&1 || true
    fi
}

# Ensure cleanup on exit
trap cleanup EXIT

# Create test namespace
log_info "Creating test namespace: ${NAMESPACE}"
kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -

# Pre-create hostPath directory on Kind nodes with correct ownership
# Required because storage.hostPath.type defaults to "Directory" (fails fast if not pre-created)
# and the controller runs as non-root UID 65532.
log_info "Pre-creating hostPath directory on Kind nodes..."
for node in $(kubectl get nodes -o jsonpath='{.items[*].metadata.name}'); do
    docker exec "${node}" mkdir -p /var/lib/isoboot
    docker exec "${node}" chown 65532:65532 /var/lib/isoboot
done

echo ""
echo "=========================================="
echo "       Helm E2E Integration Tests"
echo "=========================================="
echo ""

# ============================================
# POSITIVE TESTS
# ============================================

# Test 1: Chart installs successfully
# Note: Using --wait=false because controller image may not exist yet.
# This tests chart structure, not controller functionality.
log_info "Test 1: Chart installs successfully"
if helm install "${RELEASE_NAME}" "${CHART_PATH}" -n "${NAMESPACE}" --wait=false --timeout 120s 2>&1; then
    log_pass "Chart installs successfully"
else
    log_fail "Chart installs successfully"
fi

# Test 2: Deployment is created
log_info "Test 2: Deployment is created"
if kubectl get deployment "${RELEASE_NAME}" -n "${NAMESPACE}" >/dev/null 2>&1; then
    log_pass "Deployment is created"
else
    log_fail "Deployment is created"
fi

# Wait for pod to be created (may take a moment after deployment)
log_info "Waiting for pod to be created..."
POD_NAME=""
for i in $(seq 1 30); do
    POD_NAME=$(kubectl get pods -n "${NAMESPACE}" -l "app.kubernetes.io/instance=${RELEASE_NAME}" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")
    if [[ -n "${POD_NAME}" ]]; then
        break
    fi
    sleep 2
done

# Test 3: Pod runs as non-root user
log_info "Test 3: Pod runs as non-root user"
if [[ -n "${POD_NAME}" ]]; then
    RUN_AS_USER=$(kubectl get pod "${POD_NAME}" -n "${NAMESPACE}" -o jsonpath='{.spec.containers[0].securityContext.runAsUser}' 2>/dev/null || echo "")
    if [[ "${RUN_AS_USER}" == "65532" ]]; then
        log_pass "Pod runs as non-root user (UID 65532)"
    else
        log_fail "Pod runs as non-root user (got: ${RUN_AS_USER}, expected: 65532)"
    fi
else
    log_fail "Pod runs as non-root user (pod not found)"
fi

# Test 4: hostPath volume is mounted
log_info "Test 4: hostPath volume is mounted"
if [[ -n "${POD_NAME}" ]]; then
    VOLUME_PATH=$(kubectl get pod "${POD_NAME}" -n "${NAMESPACE}" -o jsonpath='{.spec.volumes[?(@.name=="boot-data")].hostPath.path}' 2>/dev/null || echo "")
    if [[ "${VOLUME_PATH}" == "/var/lib/isoboot" ]]; then
        log_pass "hostPath volume is mounted at /var/lib/isoboot"
    else
        log_fail "hostPath volume is mounted (got: ${VOLUME_PATH}, expected: /var/lib/isoboot)"
    fi
else
    log_fail "hostPath volume is mounted (pod not found)"
fi

# Test 5: Health probe is configured
log_info "Test 5: Health probe is configured"
if [[ -n "${POD_NAME}" ]]; then
    HEALTH_PORT=$(kubectl get pod "${POD_NAME}" -n "${NAMESPACE}" -o jsonpath='{.spec.containers[0].livenessProbe.httpGet.port}' 2>/dev/null || echo "")
    if [[ "${HEALTH_PORT}" == "health" ]]; then
        log_pass "Controller health probe configured on port 'health'"
    else
        log_fail "Controller health probe configured (got: ${HEALTH_PORT}, expected: health)"
    fi
else
    log_fail "Health probe is configured (pod not found)"
fi

# Test 6: Ready probe is configured
log_info "Test 6: Ready probe is configured"
if [[ -n "${POD_NAME}" ]]; then
    READY_PATH=$(kubectl get pod "${POD_NAME}" -n "${NAMESPACE}" -o jsonpath='{.spec.containers[0].readinessProbe.httpGet.path}' 2>/dev/null || echo "")
    if [[ "${READY_PATH}" == "/readyz" ]]; then
        log_pass "Controller readiness probe configured at /readyz"
    else
        log_fail "Controller readiness probe configured (got: ${READY_PATH}, expected: /readyz)"
    fi
else
    log_fail "Ready probe is configured (pod not found)"
fi

# Test 7: ServiceAccount is created
log_info "Test 7: ServiceAccount is created"
if kubectl get serviceaccount "${RELEASE_NAME}" -n "${NAMESPACE}" >/dev/null 2>&1; then
    log_pass "ServiceAccount is created"
else
    log_fail "ServiceAccount is created"
fi

# Test 8: Chart upgrades successfully
# Pin storage.hostPath.path to keep using the pre-created directory while changing baseDir
# Note: Using --wait=false because controller image may not exist yet.
log_info "Test 8: Chart upgrades successfully"
if helm upgrade "${RELEASE_NAME}" "${CHART_PATH}" -n "${NAMESPACE}" --set controller.baseDir=/var/lib/isoboot-upgraded --set storage.hostPath.path=/var/lib/isoboot --timeout 120s 2>&1; then
    log_pass "Chart upgrades successfully"
else
    log_fail "Chart upgrades successfully"
fi

# Test 9: Chart uninstalls cleanly
log_info "Test 9: Chart uninstalls cleanly"
if helm uninstall "${RELEASE_NAME}" -n "${NAMESPACE}" 2>&1; then
    # Wait for deployment to be deleted (async deletion can take time)
    if kubectl wait --for=delete deployment/"${RELEASE_NAME}" -n "${NAMESPACE}" --timeout=60s 2>/dev/null; then
        log_pass "Chart uninstalls cleanly"
    elif ! kubectl get deployment "${RELEASE_NAME}" -n "${NAMESPACE}" >/dev/null 2>&1; then
        # Deployment already gone
        log_pass "Chart uninstalls cleanly"
    else
        log_fail "Chart uninstalls cleanly (deployment still exists after 60s)"
    fi
else
    log_fail "Chart uninstalls cleanly"
fi

# ============================================
# NEGATIVE TESTS
# ============================================

# Test 10: Install fails without serviceaccount name when create=false
log_info "Test 10: Install fails without serviceaccount name when create=false"
INSTALL_OUTPUT=$(helm install "${RELEASE_NAME}-neg1" "${CHART_PATH}" -n "${NAMESPACE}" \
    --set serviceAccount.create=false \
    --set serviceAccount.name="" \
    --wait --timeout 30s 2>&1 || true)
if echo "${INSTALL_OUTPUT}" | grep -q "serviceAccount.name is required"; then
    log_pass "Install fails without serviceaccount name when create=false"
    helm uninstall "${RELEASE_NAME}-neg1" -n "${NAMESPACE}" 2>/dev/null || true
else
    log_fail "Install fails without serviceaccount name when create=false (should have failed with validation error)"
    helm uninstall "${RELEASE_NAME}-neg1" -n "${NAMESPACE}" 2>/dev/null || true
fi

# Test 11: Pod fails to start with invalid image
log_info "Test 11: Pod fails to start with invalid image"
helm install "${RELEASE_NAME}-neg2" "${CHART_PATH}" -n "${NAMESPACE}" \
    --set image.repository=invalid-registry.example.com/nonexistent \
    --set image.tag=v999.999.999 \
    --timeout 30s 2>/dev/null || true

# Poll for pod to be created and enter image pull error state (up to 60s)
POD_STATUS=""
for i in $(seq 1 12); do
    POD_STATUS=$(kubectl get pods -n "${NAMESPACE}" -l "app.kubernetes.io/instance=${RELEASE_NAME}-neg2" -o jsonpath='{.items[0].status.containerStatuses[0].state.waiting.reason}' 2>/dev/null || echo "")
    if [[ "${POD_STATUS}" == "ImagePullBackOff" ]] || [[ "${POD_STATUS}" == "ErrImagePull" ]]; then
        break
    fi
    sleep 5
done

if [[ "${POD_STATUS}" == "ImagePullBackOff" ]] || [[ "${POD_STATUS}" == "ErrImagePull" ]]; then
    log_pass "Pod fails to start with invalid image (${POD_STATUS})"
else
    log_fail "Pod fails to start with invalid image (got: ${POD_STATUS}, expected: ImagePullBackOff or ErrImagePull)"
fi
helm uninstall "${RELEASE_NAME}-neg2" -n "${NAMESPACE}" 2>/dev/null || true

# Test 12: Pod liveness probe is configured for restart
# Note: Not using --wait since controller image may not exist; just check rendered probe config
log_info "Test 12: Pod liveness probe is configured for restart"
helm install "${RELEASE_NAME}-neg3" "${CHART_PATH}" -n "${NAMESPACE}" 2>/dev/null || true
# Wait for pod to be created
POD_NAME=""
for i in $(seq 1 30); do
    POD_NAME=$(kubectl get pods -n "${NAMESPACE}" -l "app.kubernetes.io/instance=${RELEASE_NAME}-neg3" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")
    if [[ -n "${POD_NAME}" ]]; then
        break
    fi
    sleep 2
done
if [[ -n "${POD_NAME}" ]]; then
    FAILURE_THRESHOLD=$(kubectl get pod "${POD_NAME}" -n "${NAMESPACE}" -o jsonpath='{.spec.containers[0].livenessProbe.failureThreshold}' 2>/dev/null || echo "")
    if [[ "${FAILURE_THRESHOLD}" == "3" ]]; then
        log_pass "Pod liveness probe configured with failureThreshold=3 for restart"
    else
        log_fail "Pod liveness probe configured (got failureThreshold: ${FAILURE_THRESHOLD}, expected: 3)"
    fi
else
    log_fail "Pod liveness probe is configured for restart (pod not found)"
fi
helm uninstall "${RELEASE_NAME}-neg3" -n "${NAMESPACE}" 2>/dev/null || true

# ============================================
# SUMMARY
# ============================================

echo ""
echo "=========================================="
echo "              Test Summary"
echo "=========================================="
echo -e "Total:  ${TOTAL_TESTS}"
echo -e "Passed: ${GREEN}${TESTS_PASSED}${NC}"
echo -e "Failed: ${RED}${TESTS_FAILED}${NC}"
echo "=========================================="
echo ""

if [[ ${TESTS_FAILED} -gt 0 ]]; then
    echo -e "${RED}Some tests failed!${NC}"
    exit 1
else
    echo -e "${GREEN}All tests passed!${NC}"
    exit 0
fi
