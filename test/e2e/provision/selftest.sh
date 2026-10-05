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

# load_lib: source lib.sh as on a CI runner. Cases stub commands (kubectl,
# sleep, ...) with functions; a stub for a lib.sh function goes after this.
load_lib() {
  export GITHUB_ACTIONS=true E2E_WORK_ROOT=$tmp/work
  # shellcheck source=test/e2e/provision/lib.sh
  source "$here/lib.sh"
}

# with_lib <command...>: load_lib, then run the command.
with_lib() {
  load_lib
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

# ── apply-row.sh: waits follow progress, not a fixed time (e2e-12) ─
# wait_case <polls before Ready, or "never"> <grow|still> <phase while waiting>
# One poll is 10 s of waiting. "grow" makes the data directory grow by 1 MiB a
# poll, as a download does; "still" leaves it unchanged.
wait_case() {
  stub_ready_after=$1 stub_growth=$2 stub_waiting_phase=$3
  echo 0 > "$tmp/polls"
  kubectl() {
    local polls
    case "$*" in
      *"{.status.phase}"*)
        polls=$(($(cat "$tmp/polls") + 1))
        echo "$polls" > "$tmp/polls"
        if [ "$stub_ready_after" != never ] && [ "$polls" -gt "$stub_ready_after" ]; then
          echo Ready
        else
          echo "$stub_waiting_phase"
        fi ;;
      *"{.status.message}"*) echo "hash mismatch" ;;
    esac
  }
  sleep() { :; }
  load_lib
  data_dir_bytes() {
    if [ "$stub_growth" = grow ]; then echo $(($(cat "$tmp/polls") * 1048576)); else echo 4096; fi
  }
  wait_ready bootartifact ubuntu-26.04-iso
}
# env_wait_case <NAME=value> <wait_case arguments...>
env_wait_case() { export "${1?}"; shift; wait_case "$@"; }
expect pass "a download that keeps growing for 25 min is waited for" "PASS: bootartifact ubuntu-26.04-iso is Ready \(waited 25 min\)" \
  wait_case 150 grow Downloading
expect fail "a download that stops growing fails after 10 min" "no progress for 10 min \(phase 'Downloading'" \
  wait_case never still Downloading
expect fail "a wait that never ends fails at E2E_WAIT_MAX_MINUTES" "is not Ready after 30 min" \
  env_wait_case E2E_WAIT_MAX_MINUTES=30 never grow Downloading
expect fail "three Error phases in a row fail at once" "stays in phase Error: hash mismatch" \
  wait_case never grow Error

# clean_case <E2E_KEEP_DOWNLOADS value>: run clean_data_dir on a scratch copy
# of the chart's data directory layout and list the directories left.
clean_case() {
  local dir=$tmp/data-$1
  mkdir -p "$dir/squid/cache" "$dir/nfs/ubuntu-26.04/casper" "$dir/nfs/.source_ubuntu-26.04" \
    "$dir/nginx/static/artifacts/ubuntu-26.04-iso" "$dir/nginx/static/boot/ubuntu-26.04"
  sudo() { "$@"; }
  E2E_KEEP_DOWNLOADS=$1 with_lib clean_data_dir "$dir"
  (cd "$dir" && find . -mindepth 1 -maxdepth 3 -type d | sort | tr '\n' ' ')
}
expect pass "cleanup keeps only the squid cache by default" "^\./squid \./squid/cache $" clean_case 0
expect pass "E2E_KEEP_DOWNLOADS=1 also keeps the controller's downloads" \
  "^\./nginx \./nginx/static \./nginx/static/artifacts \./squid \./squid/cache $" clean_case 1

# wait-for-resource.sh (Kind and Helm workflows): an unreadable message must
# not end the wait silently.
cat > "$tmp/bin/kubectl" <<'STUB'
#!/bin/sh
case "$*" in
  *"{.status.phase}"*) echo Error ;;
  *"{.status.message}"*) echo "connection refused" >&2; exit 1 ;;
esac
STUB
chmod +x "$tmp/bin/kubectl"
expect fail "wait-for-resource.sh reports an Error phase whose message it cannot read" "is in Error phase \(count=2\)" \
  "$repo/test/e2e/wait-for-resource.sh" -n isoboot-system bootartifact x 2 0
rm "$tmp/bin/kubectl"

# ── hack/e2e-local.sh against a stub multipass (e2e-07) ────────────
# The stub logs every call to $tmp/multipass-calls. STUB_VM=absent|marked|
# unmarked is the VM before the run, STUB_LAUNCH=fail makes the launch fail,
# STUB_ROWS=fail|empty|list is what jq in the VM answers.
cat > "$tmp/bin/multipass" <<STUB
#!/bin/sh
echo "\$*" >> "$tmp/multipass-calls"
case "\$1" in
  info) [ "\$STUB_VM" != absent ] ;;
  launch) [ "\$STUB_LAUNCH" != fail ] ;;
  start|delete) exit 0 ;;
  exec)
    case "\$*" in
      *"jq -r"*)
        case "\$STUB_ROWS" in
          fail) echo "jq: error: syntax error" >&2; exit 1 ;;
          empty) exit 0 ;;
          *) echo alma-10.2; echo ubuntu-26.04 ;;
        esac ;;
      *"test -e /etc/isoboot-e2e-vm"*) [ "\$STUB_VM" = marked ] ;;
      *"tar -xzf -"*) cat >/dev/null ;;
      *"cd /tmp/isoboot-e2e/"*) exit 1 ;;
      *) exit 0 ;;
    esac ;;
esac
STUB
chmod +x "$tmp/bin/multipass"
# local_case <vm before> <launch: ok|fail> <rows: fail|empty|list> <e2e-local.sh options...>
local_case() {
  : > "$tmp/multipass-calls"
  STUB_VM=$1 STUB_LAUNCH=$2 STUB_ROWS=$3 E2E_LOCAL_SKIP_HOST_CHECKS=1 \
    "$repo/hack/e2e-local.sh" --logs "$tmp/e2e-logs" "${@:4}" || return
  echo "multipass calls:"
  cat "$tmp/multipass-calls"
}
calls_have() { grep -qE -- "$1" "$tmp/multipass-calls"; }
calls_lack() { ! calls_have "$1"; }

expect fail "local: rows.json that jq cannot read fails the run" "could not read the rows" \
  local_case absent ok fail
expect pass "local: ... and the VM it created is deleted" "" calls_have "^delete --purge isoboot-e2e-local$"
expect fail "local: zero rows fails the run" "no rows to run" \
  local_case absent ok empty
expect pass "local: all rows run by default" "exec isoboot-e2e-local -- env .*/run.sh ubuntu-26.04" \
  local_case absent ok list
expect pass "local: --keep keeps the row's state and the VM" "env E2E_IMAGES=local E2E_KEEP_DOWNLOADS=1 E2E_KEEP=1 .*run.sh alma-10.2" \
  local_case absent ok list --keep --row alma-10.2
expect pass "local: ... and does not delete the VM" "" calls_lack "^delete"
expect pass "local: a new VM is marked as disposable" "" calls_have "exec isoboot-e2e-local -- sudo touch /etc/isoboot-e2e-vm"
expect pass "local: --reuse runs on a marked VM" "run.sh alma-10.2" \
  local_case marked ok list --reuse --row alma-10.2
expect pass "local: ... and never deletes it" "" calls_lack "^delete"
expect fail "local: --reuse refuses a VM without the marker" "has no /etc/isoboot-e2e-vm" \
  local_case unmarked ok list --reuse --row alma-10.2
expect pass "local: ... and does not run or delete anything there" "" calls_lack "run.sh|^delete"
expect fail "local: a failed launch fails the run" "" \
  local_case absent fail list --row alma-10.2
expect pass "local: ... and removes the half-made VM" "" calls_have "^delete --purge isoboot-e2e-local$"
rm "$tmp/bin/multipass"

# ── Pinned and checked build sources (e2e-08) ──────────────────────
# Stubs for build-qemu.sh and k3s.sh: curl saves "tampered" (its -o file is
# the second argument in both scripts), git clones an empty directory whose
# HEAD is $STUB_GIT_HEAD, and the build tools only log that they ran.
mkdir -p "$tmp/build-bin"
cat > "$tmp/build-bin/curl" <<'STUB'
#!/bin/sh
echo tampered > "$2"
STUB
cat > "$tmp/build-bin/git" <<STUB
#!/bin/sh
echo "git \$*" >> "$tmp/build-calls"
case "\$*" in
  *" clone "*) for last; do :; done; mkdir -p "\$last" ;;
  *"rev-parse HEAD"*) echo "\$STUB_GIT_HEAD" ;;
esac
STUB
for tool in apt-get make tar ninja sh; do
  printf '#!/bin/sh\necho "%s $*" >> "%s"\n' "$tool" "$tmp/build-calls" > "$tmp/build-bin/$tool"
done
printf '#!/bin/sh\necho "sudo $*" >> "%s"\n' "$tmp/build-calls" > "$tmp/build-bin/sudo-ok"
chmod +x "$tmp/build-bin"/*
# build_case <real|pass> <git HEAD>: run build-qemu.sh with the stubs; with
# "pass", sha256sum is stubbed to accept the tarball so the iPXE pin is reached.
build_case() {
  : > "$tmp/build-calls"
  mkdir -p "$tmp/build-bin-$1"
  cp "$tmp/build-bin"/* "$tmp/build-bin-$1/"
  mv "$tmp/build-bin-$1/sudo-ok" "$tmp/build-bin-$1/sudo"
  [ "$1" = real ] || printf '#!/bin/sh\ncat >/dev/null\n' > "$tmp/build-bin-$1/sha256sum"
  chmod +x "$tmp/build-bin-$1"/*
  PATH="$tmp/build-bin-$1:$PATH" STUB_GIT_HEAD=$2 "$repo/test/qemu/build-qemu.sh"
}
ipxe_commit=$(sed -n 's/^IPXE_COMMIT=//p' "$repo/test/qemu/build-qemu.sh")
expect fail "build-qemu.sh rejects a QEMU tarball with the wrong checksum" "checksum mismatch for qemu-8.2.2.tar.xz" \
  build_case real "$ipxe_commit"
expect pass "... before it clones, installs or builds anything" "" \
  bash -c "! grep -E '^(git|sudo|make|tar|ninja) ' '$tmp/build-calls'"
expect fail "build-qemu.sh rejects an iPXE tag that moved to another commit" "iPXE v2.0.0 is commit 0123abc, expected $ipxe_commit" \
  build_case pass 0123abc
expect pass "... before it installs or builds anything" "" \
  bash -c "! grep -E '^(sudo|make|tar|ninja) ' '$tmp/build-calls'"

# k3s_case: run the k3s phase with no k3s installed and a tampered installer.
k3s_case() {
  : > "$tmp/build-calls"
  PATH="$tmp/build-bin:$PATH" GITHUB_ACTIONS=true E2E_WORK_ROOT=$tmp/work "$here/k3s.sh" alma-10.2
}
if command -v k3s >/dev/null; then
  echo "skip - k3s installer checksum: k3s is installed here, so k3s.sh does not download it"
else
  expect fail "k3s.sh rejects an install script with the wrong checksum" "checksum mismatch for the k3s v1.36.5\+k3s1 install script" \
    k3s_case
  expect pass "... and never runs it" "" bash -c "! grep '^sh ' '$tmp/build-calls'"
fi

# ── check-rows.sh (contract 8, e2e-09) ─────────────────────────────
# rows_case <jq edit of rows.json> [sed edit of examples/ubuntu-26.04.yaml]
rows_case() {
  local dir
  dir=$(mktemp -d "$tmp/rows.XXXX")
  cp -R "$repo/examples" "$dir/examples"
  jq "$1" "$here/rows.json" > "$dir/rows.json"
  [ -z "${2:-}" ] || sed -i "$2" "$dir/examples/ubuntu-26.04.yaml"
  "$here/check-rows.sh" "$dir/rows.json" "$dir/examples"
}
ubuntu='(.[] | select(.id == "ubuntu-26.04"))'
expect pass "rows: the real rows.json passes" "6 rows .* are consistent" "$here/check-rows.sh"
expect fail "rows: an NFS row without \"nfs\" fails" 'ubuntu-26.04: a row with iso_artifact boots over NFS and needs "nfs": true' \
  rows_case "$ubuntu |= del(.nfs)"
expect fail "rows: \"nfs\": \"yes\" fails" '"nfs" must be true or false' \
  rows_case "$ubuntu.nfs = \"yes\""
expect fail "rows: an NFS row with 8192 MB fails" "must have ram_mb <= 2048 \(has 8192\)" \
  rows_case "$ubuntu.ram_mb = 8192"
expect fail "rows: a misspelt expect fails" 'expect "stal" must be complete or stall' \
  rows_case '(.[] | select(.id == "debian-13")).expect = "stal"'
expect fail "rows: a stall row on virtio fails" "the stall row proves the missing NIC firmware" \
  rows_case '(.[] | select(.id == "debian-13")).nic = "virtio"'
expect fail "rows: a misspelt field fails" 'unknown field "nsf"' \
  rows_case "$ubuntu.nsf = true"
expect fail "rows: a duplicate MAC fails" 'duplicate mac "02-00-00-ab-cd-04"' \
  rows_case '(.[] | select(.id == "rocky-10.2")).mac = "02-00-00-ab-cd-04"'
expect fail "rows: a MAC with colons fails" "must be lower-case hex pairs joined by hyphens" \
  rows_case "$ubuntu.mac = \"02:00:00:ab:cd:09\""
expect fail "rows: a missing manifest fails" "examples/ubuntu-27.04.yaml does not exist" \
  rows_case "$ubuntu.manifests = [\"ubuntu-27.04\"]"
expect fail "rows: a missing automation file fails" "automation/user-data-27 does not exist" \
  rows_case "$ubuntu.automation[\"user-data\"] = \"user-data-27\""
expect fail "rows: a BootConfig the manifests do not define fails" 'define no BootConfig "ubuntu-26.4"' \
  rows_case "$ubuntu.bootconfig = \"ubuntu-26.4\""
expect fail "rows: a netboot row whose kernel differs from the BootConfig's fails" 'uses kernel "alma-10.2-kernel", the row says "rocky-10.2-kernel"' \
  rows_case '(.[] | select(.id == "alma-10.2")).kernel_artifact = "rocky-10.2-kernel" | (.[] | select(.id == "alma-10.2")).manifests += ["rocky-10.2"]'
expect fail "rows: kernelArgs with url= fail" "would copy the ISO into RAM" \
  rows_case . 's| fsck.mode=skip| url=http://example.org/u.iso fsck.mode=skip|'
expect fail "rows: kernelArgs without netboot=nfs fail" "kernelArgs must have netboot=nfs" \
  rows_case . 's| netboot=nfs||'
expect fail "rows: invalid JSON fails" "is not valid JSON" \
  bash -c "echo '[{' > '$tmp/broken.json'; '$here/check-rows.sh' '$tmp/broken.json'"

echo
if [ "$failures" -gt 0 ]; then
  echo "selftest: $failures check(s) failed"
  exit 1
fi
echo "selftest: all checks passed"
