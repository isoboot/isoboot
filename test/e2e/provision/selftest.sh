#!/usr/bin/env bash
# Self-test of the provision E2E harness. Runs the phase scripts' checks and
# hack/e2e-local.sh against stub commands and fixtures, so it needs no KVM,
# k3s or VM and never changes the host: a stub sudo on PATH refuses every
# command. Needs bash 4+, jq, GNU coreutils and docker (the sshd cases run a
# throwaway sshd in a container).
# Usage: selftest.sh
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../../.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# Stub commands. sudo only records what it was asked to do, so nothing here can
# touch the host even if a check under test goes wrong.
mkdir -p "$tmp/bin"
cat > "$tmp/bin/sudo" <<EOF
#!/bin/sh
echo "\$*" >> "$tmp/sudo-calls"
exit 1
EOF
chmod +x "$tmp/bin/sudo"
export PATH="$tmp/bin:$PATH"

failures=0
# expect <pass|fail> <name> <extended regex the output must match, or ''> <command...>
# Runs the command in a subshell and compares its exit status and output.
expect() {
  local want=$1 name=$2 pattern=$3 out rc=0 got=pass
  shift 3
  out=$("$@" 2>&1) || rc=$?
  [ "$rc" = 0 ] || got=fail
  if [ "$got" = "$want" ] && { [ -z "$pattern" ] || grep -qE -- "$pattern" <<<"$out"; }; then
    echo "ok - $name"
  else
    echo "not ok - $name: expected $want${pattern:+ matching /$pattern/}, got $got (exit $rc); output:"
    tail -n 20 <<<"$out" | sed 's/^/    /'
    failures=$((failures + 1))
  fi
}

# with_lib <command...>: source lib.sh as on a CI runner, then run the command.
# Cases define their stubs (kubectl, sleep, ...) as functions after sourcing.
with_lib() {
  export GITHUB_ACTIONS=true E2E_WORK_ROOT=$tmp/work
  # shellcheck source=test/e2e/provision/lib.sh
  source "$here/lib.sh"
  "$@"
}

# ── Host guard and KUBECONFIG (contract 7) ─────────────────────────
if [ -e /etc/isoboot-e2e-vm ]; then
  echo "skip - host guard refusal: this host has /etc/isoboot-e2e-vm, so the guard rightly allows it"
else
  : > "$tmp/sudo-calls"
  expect fail "cleanup.sh refuses an unmarked host" "refusing to change this host" \
    env -u GITHUB_ACTIONS -u E2E_ALLOW_THIS_HOST E2E_WORK_ROOT="$tmp/work" "$here/cleanup.sh"
  expect pass "cleanup.sh ran no sudo command on the refused host" "" test ! -s "$tmp/sudo-calls"
  expect fail "run.sh refuses an unmarked host" "refusing to change this host" \
    env -u GITHUB_ACTIONS -u E2E_ALLOW_THIS_HOST E2E_WORK_ROOT="$tmp/work" "$here/run.sh" alma-10.2
  expect fail "GITHUB_ACTIONS must be exactly true" "refusing to change this host" \
    env -u E2E_ALLOW_THIS_HOST GITHUB_ACTIONS=1 bash -c "source '$here/lib.sh'"
fi
expect pass "a GitHub Actions runner is allowed" "^allowed$" \
  env -u E2E_ALLOW_THIS_HOST GITHUB_ACTIONS=true bash -c "source '$here/lib.sh'; echo allowed"
expect pass "E2E_ALLOW_THIS_HOST=1 allows any host" "^allowed$" \
  env -u GITHUB_ACTIONS E2E_ALLOW_THIS_HOST=1 bash -c "source '$here/lib.sh'; echo allowed"
expect pass "KUBECONFIG is always the k3s one" "^/etc/rancher/k3s/k3s.yaml$" \
  env GITHUB_ACTIONS=true KUBECONFIG=/tmp/some-other-cluster bash -c "source '$here/lib.sh'; echo \"\$KUBECONFIG\""

echo
if [ "$failures" -gt 0 ]; then
  echo "selftest: $failures check(s) failed"
  exit 1
fi
echo "selftest: all checks passed"
