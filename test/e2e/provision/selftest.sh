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

# ── verify.sh: password login (e2e-02) and host keys ───────────────
# sshd_case <sshd_config lines...>: start a real sshd with that configuration
# in a throwaway container and run assert_no_password_auth against it there.
sshd_case() {
  tar -C "$repo" -cf - .alpine-version test/e2e/provision/lib.sh \
    | docker run --rm -i "alpine:$(cat "$repo/.alpine-version")" sh -c '
        set -e
        mkdir /repo && tar -xf - -C /repo
        apk add -q --no-cache bash openssh-server openssh-client >/dev/null
        ssh-keygen -A >/dev/null
        adduser -D isoboot && echo isoboot:e2etest | chpasswd 2>/dev/null
        printf "%s\n" "$@" >> /etc/ssh/sshd_config
        /usr/sbin/sshd
        GITHUB_ACTIONS=true bash -c "source /repo/test/e2e/provision/lib.sh && assert_no_password_auth isoboot@127.0.0.1"
      ' sshd-case "$@"
}
expect fail "sshd that accepts passwords is caught" "FAIL: sshd on isoboot@127.0.0.1 offers password login: .*password" \
  sshd_case "PasswordAuthentication yes"
expect fail "sshd that offers keyboard-interactive is caught" "FAIL: .*offers password login: .*keyboard-interactive" \
  sshd_case "PasswordAuthentication no" "KbdInteractiveAuthentication yes"
expect pass "sshd with key login only passes" "PASS: sshd on isoboot@127.0.0.1 offers only publickey$" \
  sshd_case "PasswordAuthentication no" "KbdInteractiveAuthentication no"
expect fail "an unreachable sshd fails instead of passing" "could not read the login methods" \
  with_lib assert_no_password_auth isoboot@127.0.0.1 -p 1

ssh-keygen -q -t rsa -b 2048 -N "" -f "$tmp/host_rsa_key"
host_key_case() {
  stub_keyscan_output=$1
  ssh-keyscan() { [ -z "$stub_keyscan_output" ] || echo "192.0.2.1 $stub_keyscan_output"; }
  with_lib assert_host_key 192.0.2.1 rsa "$tmp/host_rsa_key.pub"
}
expect fail "a missing host key fails with a message" "FAIL: rsa host key: expected SHA256:.*, got none" \
  host_key_case ""
expect pass "the injected host key passes" "" \
  host_key_case "$(cut -d' ' -f1,2 "$tmp/host_rsa_key.pub")"

# ── boot-install.sh: the negative row must stall for the right reason (e2e-03)
# stall_case <phase, or "unreadable"> <nginx access log fixture>
# (Stub variables have a stub_ prefix: a local of the function under test
# with the same name would hide them.)
stall_case() {
  stub_phase=$1 stub_access_log=$2
  kubectl() {
    case "$*" in
      *"get provision"*) [ "$stub_phase" != unreadable ] || return 1; echo "$stub_phase" ;;
      *"get pod"*) echo nginx-0 ;;
      *" exec "*) cat "$stub_access_log" ;;
    esac
  }
  sleep() { :; }
  with_lib assert_stalls 3 10
}
ipxe_line='192.168.101.150 - - [05/Oct/2026:02:41:47 +0000] "GET /static/debian-13/kernel/linux HTTP/1.1" 200 1 "-" "iPXE/2.0.0"'
echo "$ipxe_line" > "$tmp/access-ipxe-only.log"
{ echo "$ipxe_line"
  echo '192.168.101.151 - - [05/Oct/2026:02:44:02 +0000] "GET /dynamic/automation/qemu-vm1-provision/preseed.cfg HTTP/1.1" 200 1 "-" "Wget"'
} > "$tmp/access-preseed.log"
: > "$tmp/access-empty.log"
expect fail "stall row: a Provision that went InProgress fails" "Provision is 'InProgress' at check 1/3" \
  stall_case InProgress "$tmp/access-ipxe-only.log"
expect fail "stall row: an unreadable phase fails" "Provision is '<unreadable>'" \
  stall_case unreadable "$tmp/access-ipxe-only.log"
expect fail "stall row: an installer that fetched preseed.cfg fails" "fetched its automation files" \
  stall_case Pending "$tmp/access-preseed.log"
expect fail "stall row: an unreadable access log fails" "could not read the nginx access log" \
  stall_case Pending "$tmp/access-empty.log"
expect pass "stall row: Pending throughout and no automation fetch passes" "PASS: Provision stayed Pending" \
  stall_case Pending "$tmp/access-ipxe-only.log"

# ── boot-install.sh: the guest's real kernel command line (e2e-04) ─
# The first line is from a green CI run of the ubuntu-26.04 row.
cmdline='[    0.000000] Command line: vmlinuz console=ttyS0,115200 ip=dhcp netboot=nfs nfsroot=192.168.101.1:/ubuntu-26.04 fsck.mode=skip autoinstall ds=nocloud;s=http://192.168.101.1:8080/dynamic/automation/qemu-vm1-provision/ ---'
printf '%s\r\n' "boot noise" "$cmdline" > "$tmp/serial-nfs.log"
printf '%s\n' "${cmdline/ ---/ iso-url=http:\/\/x\/y.iso ---}" > "$tmp/serial-iso-url.log"
printf '%s\n' "${cmdline/ ip=dhcp/ ip=dhcp url=http:\/\/x\/y.iso}" > "$tmp/serial-url.log"
printf '%s\n' "${cmdline/ ---/ toram ---}" > "$tmp/serial-toram.log"
printf '%s\n' "${cmdline/ netboot=nfs/}" > "$tmp/serial-no-netboot.log"
echo "no kernel messages" > "$tmp/serial-empty.log"
expect pass "NFS command line from CI passes" "PASS: kernel command line" \
  with_lib assert_nfs_cmdline "$tmp/serial-nfs.log" ubuntu-26.04
expect fail "NFS command line: iso-url= fails" "would copy the ISO into RAM" \
  with_lib assert_nfs_cmdline "$tmp/serial-iso-url.log" ubuntu-26.04
expect fail "NFS command line: url= fails" "would copy the ISO into RAM" \
  with_lib assert_nfs_cmdline "$tmp/serial-url.log" ubuntu-26.04
expect fail "NFS command line: toram fails" "would copy the ISO into RAM" \
  with_lib assert_nfs_cmdline "$tmp/serial-toram.log" ubuntu-26.04
expect fail "NFS command line: no netboot=nfs fails" "has no netboot=nfs" \
  with_lib assert_nfs_cmdline "$tmp/serial-no-netboot.log" ubuntu-26.04
expect fail "NFS command line: another export fails" "has no nfsroot=192.168.101.1:/ubuntu-26.10" \
  with_lib assert_nfs_cmdline "$tmp/serial-nfs.log" ubuntu-26.10
expect fail "NFS command line: no command line fails" "no kernel command line" \
  with_lib assert_nfs_cmdline "$tmp/serial-empty.log" ubuntu-26.04

# ── Pod restarts during a row (e2e-06) ─────────────────────────────
# restarts_case <pod list JSON file, or "unreadable">
restarts_case() {
  stub_pods=$1
  kubectl() { [ "$stub_pods" != unreadable ] || return 1; cat "$stub_pods"; }
  with_lib assert_no_restarts
}
jq -n '{items: [
  {metadata: {name: "isoboot-controller-manager-1"}, status: {containerStatuses: [
    {name: "manager", restartCount: 2, lastState: {terminated: {reason: "OOMKilled", exitCode: 137}}}]}},
  {metadata: {name: "isoboot-nfsd-1"}, status: {containerStatuses: [{name: "nfsd", restartCount: 0, lastState: {}}]}}]}' \
  > "$tmp/pods-oom.json"
jq -n '{items: [
  {metadata: {name: "isoboot-nfsd-1"}, status: {
    initContainerStatuses: [{name: "nfs-dir", restartCount: 1, lastState: {terminated: {reason: "Error"}}}],
    containerStatuses: [{name: "nfsd", restartCount: 0, lastState: {}}]}}]}' > "$tmp/pods-init.json"
jq '.items[].status.containerStatuses[].restartCount = 0' "$tmp/pods-oom.json" > "$tmp/pods-ok.json"
expect fail "an OOM-killed controller fails the row" "isoboot-controller-manager-1/manager restarts=2 last=OOMKilled" \
  restarts_case "$tmp/pods-oom.json"
expect fail "a restarted init container fails the row" "isoboot-nfsd-1/nfs-dir restarts=1 last=Error" \
  restarts_case "$tmp/pods-init.json"
expect fail "an unreadable pod list fails the row" "could not list the isoboot pods" \
  restarts_case unreadable
expect pass "pods without restarts pass" "PASS: no isoboot pod restarted" \
  restarts_case "$tmp/pods-ok.json"

echo
if [ "$failures" -gt 0 ]; then
  echo "selftest: $failures check(s) failed"
  exit 1
fi
echo "selftest: all checks passed"
