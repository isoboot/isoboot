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

# Switch to Kind cluster context
kubectl config use-context "kind-${CLUSTER_NAME}" >/dev/null 2>&1 || {
    echo -e "${RED}Failed to switch to Kind cluster context: kind-${CLUSTER_NAME}${NC}"
    exit 1
}

log_info() {
    echo -e "${YELLOW}[INFO]${NC} $1"
}

log_pass() {
    echo -e "${GREEN}[PASS]${NC} $1"
    ((TESTS_PASSED++))
    ((TOTAL_TESTS++))
}

log_fail() {
    echo -e "${RED}[FAIL]${NC} $1"
    ((TESTS_FAILED++))
    ((TOTAL_TESTS++))
}

cleanup() {
    log_info "Cleaning up test resources..."
    helm uninstall "${RELEASE_NAME}" -n "${NAMESPACE}" 2>/dev/null || true
    kubectl delete namespace "${NAMESPACE}" --ignore-not-found=true --wait=false 2>/dev/null || true
}

# Ensure cleanup on exit
trap cleanup EXIT

# Create test namespace
log_info "Creating test namespace: ${NAMESPACE}"
kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -

echo ""
echo "=========================================="
echo "       Helm E2E Integration Tests"
echo "=========================================="
echo ""

# ============================================
# POSITIVE TESTS
# ============================================

# Test 1: Chart installs successfully
log_info "Test 1: Chart installs successfully"
if helm install "${RELEASE_NAME}" "${CHART_PATH}" -n "${NAMESPACE}" --wait --timeout 120s 2>&1; then
    log_pass "Chart installs successfully"
else
    log_fail "Chart installs successfully"
fi

# Test 2: Deployment creates pod
log_info "Test 2: Deployment creates pod"
if kubectl wait --for=condition=available deployment/"${RELEASE_NAME}-isoboot" -n "${NAMESPACE}" --timeout=60s 2>/dev/null; then
    log_pass "Deployment creates pod"
else
    log_fail "Deployment creates pod"
fi

# Test 3: Pod runs as non-root user
log_info "Test 3: Pod runs as non-root user"
POD_NAME=$(kubectl get pods -n "${NAMESPACE}" -l "app.kubernetes.io/instance=${RELEASE_NAME}" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")
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

# Test 5: Controller responds to health probe
log_info "Test 5: Controller responds to health probe"
if [[ -n "${POD_NAME}" ]]; then
    HEALTH_PORT=$(kubectl get pod "${POD_NAME}" -n "${NAMESPACE}" -o jsonpath='{.spec.containers[0].livenessProbe.httpGet.port}' 2>/dev/null || echo "")
    if [[ "${HEALTH_PORT}" == "health" ]]; then
        log_pass "Controller health probe configured on port 'health'"
    else
        log_fail "Controller health probe configured (got: ${HEALTH_PORT}, expected: health)"
    fi
else
    log_fail "Controller responds to health probe (pod not found)"
fi

# Test 6: Controller responds to ready probe
log_info "Test 6: Controller responds to ready probe"
if [[ -n "${POD_NAME}" ]]; then
    READY_PATH=$(kubectl get pod "${POD_NAME}" -n "${NAMESPACE}" -o jsonpath='{.spec.containers[0].readinessProbe.httpGet.path}' 2>/dev/null || echo "")
    if [[ "${READY_PATH}" == "/readyz" ]]; then
        log_pass "Controller readiness probe configured at /readyz"
    else
        log_fail "Controller readiness probe configured (got: ${READY_PATH}, expected: /readyz)"
    fi
else
    log_fail "Controller responds to ready probe (pod not found)"
fi

# Test 7: ServiceAccount is created
log_info "Test 7: ServiceAccount is created"
if kubectl get serviceaccount "${RELEASE_NAME}-isoboot" -n "${NAMESPACE}" >/dev/null 2>&1; then
    log_pass "ServiceAccount is created"
else
    log_fail "ServiceAccount is created"
fi

# Test 8: Chart upgrades successfully
log_info "Test 8: Chart upgrades successfully"
if helm upgrade "${RELEASE_NAME}" "${CHART_PATH}" -n "${NAMESPACE}" --set controller.baseDir=/var/lib/isoboot-upgraded --wait --timeout 120s 2>&1; then
    log_pass "Chart upgrades successfully"
else
    log_fail "Chart upgrades successfully"
fi

# Test 9: Chart uninstalls cleanly
log_info "Test 9: Chart uninstalls cleanly"
if helm uninstall "${RELEASE_NAME}" -n "${NAMESPACE}" 2>&1; then
    # Verify resources are removed
    sleep 2
    if ! kubectl get deployment "${RELEASE_NAME}-isoboot" -n "${NAMESPACE}" >/dev/null 2>&1; then
        log_pass "Chart uninstalls cleanly"
    else
        log_fail "Chart uninstalls cleanly (deployment still exists)"
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

# Wait for pod to be created and check status
sleep 10
POD_STATUS=$(kubectl get pods -n "${NAMESPACE}" -l "app.kubernetes.io/instance=${RELEASE_NAME}-neg2" -o jsonpath='{.items[0].status.containerStatuses[0].state.waiting.reason}' 2>/dev/null || echo "")
if [[ "${POD_STATUS}" == "ImagePullBackOff" ]] || [[ "${POD_STATUS}" == "ErrImagePull" ]]; then
    log_pass "Pod fails to start with invalid image (${POD_STATUS})"
else
    log_fail "Pod fails to start with invalid image (got: ${POD_STATUS}, expected: ImagePullBackOff or ErrImagePull)"
fi
helm uninstall "${RELEASE_NAME}-neg2" -n "${NAMESPACE}" 2>/dev/null || true

# Test 12: Pod liveness probe is configured for restart
log_info "Test 12: Pod liveness probe is configured for restart"
helm install "${RELEASE_NAME}-neg3" "${CHART_PATH}" -n "${NAMESPACE}" --wait --timeout 120s 2>/dev/null || true
POD_NAME=$(kubectl get pods -n "${NAMESPACE}" -l "app.kubernetes.io/instance=${RELEASE_NAME}-neg3" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")
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
