#!/usr/bin/env bash
# Run the provision E2E locally, in a throwaway multipass VM, with the same
# scripts CI runs (test/e2e/provision/run.sh).
#
# Usage: hack/e2e-local.sh [options]
#   --row <id>        row from test/e2e/provision/rows.json; repeatable.
#                     Default: every row, one after another.
#   --keep            keep the VM and the last row's state (k3s, isoboot,
#                     the guest) for debugging; inside the VM,
#                     ~/isoboot/test/e2e/provision/run.sh <row> <phase>
#                     re-runs one phase. Default: delete the VM if this run
#                     created it.
#   --reuse           use an existing VM of that name, made earlier by this
#                     script (default: refuse to touch an existing VM). A
#                     reused VM is never deleted.
#   --vm-name <name>  default isoboot-e2e-local
#   --cpus <n>        default 4
#   --memory <size>   default 12G
#   --disk <size>     default 60G
#   --logs <dir>      where logs are copied to (default <repo>/e2e-logs/<time>)
#   -h, --help
#
# Needs an x86-64 Linux host with KVM, nested virtualisation enabled
# (kvm_intel/kvm_amd "nested") and multipass. The VM is Ubuntu 24.04.
#
# What it does: create (or with --reuse, start) the VM and mark it as
# disposable (/etc/isoboot-e2e-vm; the E2E scripts refuse to run on an
# unmarked host), copy this checkout into it, including uncommitted changes but
# not ignored files, then for each row run test/e2e/provision/run.sh with
# E2E_IMAGES=local, which builds the images inside the VM, imports them into
# k3s and installs the chart from the checkout. Rows run one at a time; each
# row starts and ends with a cleanup inside the VM, so a failed row does not
# break the next one. Downloaded BootArtifacts (E2E_KEEP_DOWNLOADS=1), the
# squid cache and the RTL8168 QEMU build are kept in the VM, so later rows and
# --reuse runs do not fetch or build them again. Logs of every row are copied
# out, and the script exits non-zero if any row failed or no row ran.
#
# Sizing (one row at a time): the guest VM needs up to 8 GiB (EL and Debian
# rows; Ubuntu rows 2 GiB), k3s with isoboot about 1.5 GiB, the image builds
# about 2 GiB, which happen before the guest starts: 12 GiB. 4 vCPUs: 2 for
# the guest, the rest for k3s, nginx and nfsd. 60 GiB disk: the kept
# downloads (both Ubuntu ISOs, about 6 GiB), one unpacked ISO tree (about
# 3 GiB), the guest disk (up to 20 GiB, usually 4-6 GiB used), images and
# build cache (about 5 GiB), the squid cache (up to 8 GiB, kept between rows)
# and the RTL8168 QEMU build (about 2 GiB).
set -euo pipefail

vm=isoboot-e2e-local
cpus=4
memory=12G
disk=60G
keep=false
reuse=false
rows=()
repo=$(cd "$(dirname "$0")/.." && pwd)
logs=$repo/e2e-logs/$(date +%Y%m%d-%H%M%S)

# usage: the comment block at the top of this file.
usage() { awk 'NR > 1 && /^#/ { sub(/^# ?/, ""); print; next } NR > 1 { exit }' "$0"; }
die() { echo "e2e-local: $*" >&2; exit 1; }
need_arg() { [ $# -ge 2 ] && [ -n "$2" ] || die "$1 needs a value"; }

while [ $# -gt 0 ]; do
  case $1 in
    --row) need_arg "$@"; rows+=("$2"); shift 2 ;;
    --keep) keep=true; shift ;;
    --reuse) reuse=true; shift ;;
    --vm-name) need_arg "$@"; vm=$2; shift 2 ;;
    --cpus) need_arg "$@"; cpus=$2; shift 2 ;;
    --memory) need_arg "$@"; memory=$2; shift 2 ;;
    --disk) need_arg "$@"; disk=$2; shift 2 ;;
    --logs) need_arg "$@"; logs=$2; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; die "unknown option $1" ;;
  esac
done

# test/e2e/provision/selftest.sh runs this script against a stub multipass
# on a host without KVM.
if [ "${E2E_LOCAL_SKIP_HOST_CHECKS:-}" != 1 ]; then
  [ "$(uname -s)" = Linux ] && [ "$(uname -m)" = x86_64 ] || die "needs an x86-64 Linux host"
  [ -e /dev/kvm ] || die "/dev/kvm is missing: enable KVM"
  nested=$(cat /sys/module/kvm_intel/parameters/nested /sys/module/kvm_amd/parameters/nested 2>/dev/null || true)
  case $nested in
    *Y*|*1*) ;;
    *) die "nested virtualisation is off (kvm_intel/kvm_amd parameter 'nested')" ;;
  esac
fi
command -v multipass >/dev/null || die "multipass is not installed"
command -v git >/dev/null || die "git is not installed"

vm_exec() { multipass exec "$vm" -- "$@"; }

# Only a VM this run created is ever deleted, and only without --keep. The
# trap is set before the launch, so a launch that fails half-way is removed too.
created=false
# shellcheck disable=SC2329 # run by the EXIT trap
finish() {
  local rc=$?
  if $created && ! $keep; then
    echo "== Deleting VM $vm"
    multipass delete --purge "$vm" || true
  else
    echo "== Leaving VM $vm (multipass shell $vm; checkout in ~/isoboot)"
  fi
  exit "$rc"
}
trap finish EXIT

if multipass info "$vm" >/dev/null 2>&1; then
  $reuse || die "VM $vm already exists; pass --reuse to use it, or --vm-name for another"
  echo "== Reusing VM $vm"
  multipass start "$vm"
else
  echo "== Creating VM $vm (Ubuntu 24.04, $cpus CPUs, $memory RAM, $disk disk)"
  launch_log=$(mktemp)
  if multipass launch 24.04 --name "$vm" --cpus "$cpus" --memory "$memory" --disk "$disk" 2>&1 | tee "$launch_log"; then
    created=true
  else
    # A VM half-made by this launch is ours to remove. One that already
    # existed (multipass info can fail transiently) is not.
    grep -q 'already exists' "$launch_log" || created=true
    rm -f "$launch_log"
    die "multipass launch of $vm failed"
  fi
  rm -f "$launch_log"
fi

vm_exec cloud-init status --wait >/dev/null || true
vm_exec test -e /dev/kvm || die "no /dev/kvm inside the VM: nested virtualisation is not reaching it"
# The E2E scripts run only on a host marked as disposable (see lib.sh).
if $created; then
  vm_exec sudo touch /etc/isoboot-e2e-vm
else
  vm_exec test -e /etc/isoboot-e2e-vm \
    || die "VM $vm has no /etc/isoboot-e2e-vm, so it was not made by this script and the E2E would change it for real. If it is a throwaway E2E VM, mark it: multipass exec $vm -- sudo touch /etc/isoboot-e2e-vm"
fi

echo "== Copying $repo (tracked and untracked files, without ignored ones) into $vm:~/isoboot"
(
  cd "$repo"
  git ls-files -z --cached --others --exclude-standard \
    | while IFS= read -r -d '' f; do [ -e "$f" ] && printf '%s\0' "$f"; done \
    | tar --null -T - -czf -
) | vm_exec bash -c 'rm -rf ~/isoboot && mkdir ~/isoboot && tar -xzf - -C ~/isoboot'

vm_exec sudo apt-get install -y -qq -o DPkg::Lock::Timeout=300 jq >/dev/null
if [ ${#rows[@]} -eq 0 ]; then
  # Not "mapfile < <(...)": a failure there (say a syntax error in rows.json)
  # would be ignored and leave no rows.
  row_ids=$(vm_exec jq -r '.[].id' /home/ubuntu/isoboot/test/e2e/provision/rows.json) \
    || die "could not read the rows from test/e2e/provision/rows.json"
  mapfile -t rows <<<"$row_ids"
fi
[ -n "${rows[*]}" ] || die "no rows to run"

# --keep leaves the last row's state in place (each row cleans up first).
row_env=(E2E_IMAGES=local E2E_KEEP_DOWNLOADS=1)
$keep && row_env+=(E2E_KEEP=1)

mkdir -p "$logs"
results=()
failed=0
for row in "${rows[@]}"; do
  echo "== Row $row"
  rc=0
  vm_exec env "${row_env[@]}" /home/ubuntu/isoboot/test/e2e/provision/run.sh "$row" || rc=$?
  mkdir -p "$logs/$row"
  vm_exec bash -c "cd /tmp/isoboot-e2e/$row/logs 2>/dev/null && tar -czf - ." \
    | tar -xzf - -C "$logs/$row" || echo "e2e-local: could not copy the logs of $row" >&2
  if [ "$rc" = 0 ]; then
    results+=("PASS  $row")
  else
    results+=("FAIL  $row")
    failed=1
  fi
done

echo "== Results"
printf '  %s\n' "${results[@]}"
echo "== Logs: $logs (one directory per row; run.log has the full output)"
exit "$failed"
