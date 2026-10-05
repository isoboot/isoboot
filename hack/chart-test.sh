#!/usr/bin/env bash
# Lint charts/isoboot and check what it renders: each check below names the
# behaviour it guards. Every check runs; the script exits non-zero if any
# fails. Run it with `make chart-test`, which installs the pinned helm and yq.
# Usage: HELM=<helm> YQ=<yq> hack/chart-test.sh
set -euo pipefail
cd "$(dirname "$0")/.."

HELM=${HELM:-helm}
YQ=${YQ:-yq}
chart=charts/isoboot
release=rel
namespace=isoboot-test
subnet=192.168.101.0/24
required=(--set nodeName=node1 --set "dnsmasq.subnet=$subnet")
# Render for the Kubernetes the E2E runs (helm built with `go install` would
# otherwise assume v1.20, below the chart's kubeVersion).
kube_version=$(sed -n 's/^K3S_VERSION="\(v[0-9.]*\)+.*/\1/p' test/e2e/provision/lib.sh)
[ -n "$kube_version" ] || { echo "FAIL: no K3S_VERSION in test/e2e/provision/lib.sh" >&2; exit 1; }

failures=0
pass() { echo "ok   $*"; }
fail() {
  echo "FAIL $*" >&2
  failures=$((failures + 1))
}

# render [helm template flags]: the chart with the required values.
render() {
  "$HELM" template "$release" "$chart" --namespace "$namespace" --kube-version "$kube_version" \
    "${required[@]}" "$@"
}

# query <rendered> [yq flags] <yq expression>: evaluate the expression on
# every rendered document.
query() {
  local rendered=$1
  shift
  "$YQ" eval --no-doc "$@" - <<<"$rendered"
}

# expect <description> <expected> <actual>
expect() {
  if [ "$2" = "$3" ]; then
    pass "$1"
  else
    fail "$1"
    printf '       expected: %s\n       actual:   %s\n' "$2" "$3" >&2
  fi
}

# expect_render_failure <description> <message substring> [helm template flags]
expect_render_failure() {
  local description=$1 message=$2 output
  shift 2
  if output=$("$HELM" template "$release" "$chart" --namespace "$namespace" \
    --kube-version "$kube_version" "$@" 2>&1); then
    fail "$description (rendered without error)"
  elif [[ $output != *"$message"* ]]; then
    fail "$description (error does not mention '$message': $output)"
  else
    pass "$description"
  fi
}

# container <rendered> <deployment suffix> <container> <field>: one field of a
# container, as JSON on one line.
container() {
  query "$1" -o=json -I=0 "select(.kind == \"Deployment\" and .metadata.name == \"$release-isoboot-$2\")
    | .spec.template.spec.containers[] | select(.name == \"$3\") | .$4"
}

if "$HELM" lint --strict "$chart" "${required[@]}" >/dev/null; then
  pass "helm lint --strict"
else
  "$HELM" lint --strict "$chart" "${required[@]}" >&2 || true
  fail "helm lint --strict"
fi
expect_render_failure "nodeName is required" "nodeName is required" --set "dnsmasq.subnet=$subnet"

default=$(render)
app_version=$("$YQ" eval '.appVersion' "$chart/Chart.yaml")

# One Go image: the controller, httpd and nfsd all run from .Values.image,
# each with its own program; no other isoboot Go image is referenced.
for program in controller-manager:manager:/manager httpd:httpd:/httpd nfsd:nfsd:/nfsd; do
  IFS=: read -r deployment name command <<<"$program"
  expect "$deployment runs $command from the isoboot image" \
    "\"ghcr.io/isoboot/isoboot:$app_version\" [\"$command\"]" \
    "$(container "$default" "$deployment" "$name" image) $(container "$default" "$deployment" "$name" command)"
done
expect "image.tag sets the tag of all three Go programs" \
  "ghcr.io/isoboot/isoboot:1.2.3 ghcr.io/isoboot/isoboot:1.2.3 ghcr.io/isoboot/isoboot:1.2.3" \
  "$(query "$(render --set image.tag=1.2.3)" 'select(.kind == "Deployment")
    | .spec.template.spec.containers[] | select(.command[0] == "/manager" or .command[0] == "/httpd" or .command[0] == "/nfsd")
    | .image' | sort | xargs)"
expect "no isoboot-httpd or isoboot-nfsd image" "" \
  "$(grep -oE 'isoboot-(httpd|nfsd):[^"]*' <<<"$default" || true)"

# chart-01: a single pod pinned to one node with self anti-affinity or host
# ports can only be replaced after the old one stops; RollingUpdate deadlocks.
expect "every pinned Deployment uses the Recreate strategy" \
  "controller-manager=Recreate dnsmasq=Recreate nfsd=Recreate nginx=Recreate squid=Recreate" \
  "$(query "$default" 'select(.kind == "Deployment" and .spec.template.spec.affinity.podAntiAffinity != null)
    | (.metadata.name | sub("^rel-isoboot-", "")) + "=" + (.spec.strategy.type // "RollingUpdate")' | sort | xargs)"

# chart-04: only the controller serves metrics on 8443; the Service must not
# pick the other isoboot pods (most of them on the host network) as endpoints.
metrics_selector=$(query "$default" -o=json -I=0 \
  'select(.kind == "Service" and .metadata.name == "rel-isoboot-metrics-service") | .spec.selector')
expect "metrics Service selects only the controller pod" "controller-manager" \
  "$(SELECTOR=$metrics_selector query "$default" 'select(.kind == "Deployment")
    | select(.spec.template.metadata.labels as $labels
      | [env(SELECTOR) | to_entries | .[] | $labels[.key] == .value] | all)
    | .metadata.name | sub("^rel-isoboot-", "")' | xargs)"

# chart-06: nginx copies its ConfigMap once at pod start, so a changed
# ConfigMap (e.g. httpd.port) must change the pod template to roll the pod.
nginx_checksum() {
  query "$1" 'select(.kind == "Deployment" and .metadata.name == "rel-isoboot-nginx")
    | .spec.template.metadata.annotations["checksum/nginx-config"]'
}
checksum_default=$(nginx_checksum "$default")
checksum_changed=$(nginx_checksum "$(render --set httpd.port=9090)")
if [ "$checksum_default" != null ] && [ "$checksum_default" != "$checksum_changed" ]; then
  pass "nginx pod template changes when its ConfigMap changes"
else
  fail "nginx pod template changes when its ConfigMap changes ($checksum_default / $checksum_changed)"
fi

# chart-03 / namespaced controller: httpd faces the PXE LAN and the controller
# unpacks downloaded ISOs; neither may read Secrets, ConfigMaps or isoboot
# resources outside the release namespace.
expect "controller watches only the release namespace" '"--namespace=isoboot-test"' \
  "$(container "$default" controller-manager manager 'args[] | select(test("^--namespace"))')"
expect "httpd queries only the release namespace" '"--namespace=isoboot-test"' \
  "$(container "$default" httpd httpd 'args[] | select(test("^--namespace"))')"
expect "no ClusterRole grants access to namespaced resources" "" \
  "$(query "$default" 'select(.kind == "ClusterRole") | .metadata.name as $role | .rules[]
    | select(.resources != null and ([.resources[] | test("^(tokenreviews|subjectaccessreviews)$") | not] | any))
    | $role + ": " + (.resources | join(","))')"
expect "controller and httpd roles are Roles in the release namespace" \
  "Role/rel-isoboot-httpd-role@isoboot-test Role/rel-isoboot-manager-role@isoboot-test" \
  "$(query "$default" 'select(.kind == "Role" or .kind == "ClusterRole") | select(.metadata.name | test("-(manager|httpd)-role$"))
    | .kind + "/" + .metadata.name + "@" + (.metadata.namespace // "cluster")' | sort | xargs)"
expect "their bindings are RoleBindings to those Roles" \
  "RoleBinding/rel-isoboot-httpd-rolebinding@isoboot-test->Role/rel-isoboot-httpd-role RoleBinding/rel-isoboot-manager-rolebinding@isoboot-test->Role/rel-isoboot-manager-role" \
  "$(query "$default" 'select(.kind == "RoleBinding" or .kind == "ClusterRoleBinding") | select(.metadata.name | test("-(manager|httpd)-rolebinding$"))
    | .kind + "/" + .metadata.name + "@" + (.metadata.namespace // "cluster") + "->" + .roleRef.kind + "/" + .roleRef.name' | sort | xargs)"
# ci-04: the controller's rules are controller-gen's, not a hand-kept copy.
expect "controller Role has exactly the rules controller-gen generated" \
  "$("$YQ" eval -o=json -I=0 '.rules' config/rbac/role.yaml)" \
  "$(query "$default" -o=json -I=0 'select(.kind == "Role" and .metadata.name == "rel-isoboot-manager-role") | .rules')"

if [ "$failures" -gt 0 ]; then
  echo "$failures chart check(s) failed" >&2
  exit 1
fi
echo "All chart checks passed"
