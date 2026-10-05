#!/usr/bin/env bash
# Phase 6: the row's resources. Applies the example manifests, waits for the
# BootArtifacts and the BootConfig, generates test-only keys (login key, SSH
# host keys, password hash, machine-id; they live in $WORK/keys, never in
# examples/) and creates the ConfigMap, Secret, Machine, ProvisionAutomation
# (from test/e2e/provision/automation/) and Provision.
# Usage: apply-row.sh <row-id>
set -euo pipefail
# shellcheck source=test/e2e/provision/lib.sh
source "$(dirname "$0")/lib.sh"
load_row "${1:-}"

bootconfig=$(row bootconfig)
hostname=$(row hostname)

for manifest in $(jq -r --arg id "$ROW_ID" '.[] | select(.id == $id) | .manifests[]' "$ROWS_FILE"); do
  kc apply -f "$REPO_ROOT/examples/$manifest.yaml"
done

if [ -n "$(row iso_artifact)" ]; then
  wait_ready bootartifact "$(row iso_artifact)"
else
  wait_ready bootartifact "$(row kernel_artifact)"
  wait_ready bootartifact "$(row initrd_artifact)"
fi
if [ -n "$(row firmware_artifact)" ]; then
  wait_ready bootartifact "$(row firmware_artifact)"
fi
wait_ready bootconfig "$bootconfig"

keys=$WORK/keys
rm -rf "$keys"
mkdir -p "$keys"
ssh-keygen -q -t ed25519 -N "" -C e2e -f "$keys/login"
ssh-keygen -q -t ecdsa -N "" -f "$keys/ssh_host_ecdsa_key"
ssh-keygen -q -t ed25519 -N "" -f "$keys/ssh_host_ed25519_key"
ssh-keygen -q -t rsa -b 4096 -N "" -f "$keys/ssh_host_rsa_key"
openssl passwd -6 "e2etest" > "$keys/password"
openssl rand -hex 16 > "$keys/machine-id"

kc create configmap default-user \
  --from-literal="default_user.username=isoboot" \
  --from-literal="default_user.password=$(cat "$keys/password")" \
  --from-literal="default_user.ssh_public_key=$(cat "$keys/login.pub")" \
  --from-literal="machine_id=$(cat "$keys/machine-id")" \
  --dry-run=client -o yaml | kc apply -f -
kc create secret generic host-keys \
  --from-file=ssh_host_ecdsa_key="$keys/ssh_host_ecdsa_key" \
  --from-file=ssh_host_ed25519_key="$keys/ssh_host_ed25519_key" \
  --from-file=ssh_host_rsa_key="$keys/ssh_host_rsa_key" \
  --from-literal="ssh_host_ecdsa_key_b64=$(base64 -w0 < "$keys/ssh_host_ecdsa_key")" \
  --from-literal="ssh_host_ed25519_key_b64=$(base64 -w0 < "$keys/ssh_host_ed25519_key")" \
  --from-literal="ssh_host_rsa_key_b64=$(base64 -w0 < "$keys/ssh_host_rsa_key")" \
  --dry-run=client -o yaml | kc apply -f -

# ProvisionAutomation files: name in the resource -> template in automation/,
# with @HOSTNAME@ replaced by the row's hostname.
files='{}'
for name in $(jq -r --arg id "$ROW_ID" '.[] | select(.id == $id) | .automation | keys[]' "$ROWS_FILE"); do
  src=$(jq -r --arg id "$ROW_ID" --arg n "$name" '.[] | select(.id == $id) | .automation[$n]' "$ROWS_FILE")
  sed "s/@HOSTNAME@/$hostname/g" "$E2E_DIR/automation/$src" > "$WORK/automation-$name"
  files=$(jq --arg n "$name" --rawfile body "$WORK/automation-$name" '. + {($n): $body}' <<<"$files")
done

jq -n --arg mac "$MAC" --arg bc "$bootconfig" --argjson files "$files" '
{apiVersion: "v1", kind: "List", items: [
  {apiVersion: "isoboot.github.io/v1alpha1", kind: "Machine",
   metadata: {name: "qemu-vm1"}, spec: {mac: $mac}},
  {apiVersion: "isoboot.github.io/v1alpha1", kind: "ProvisionAutomation",
   metadata: {name: "qemu-vm1-automation"}, spec: {files: $files}},
  {apiVersion: "isoboot.github.io/v1alpha1", kind: "Provision",
   metadata: {name: "qemu-vm1-provision"},
   spec: {machineRef: "qemu-vm1", bootConfigRef: $bc,
          provisionAutomationRef: "qemu-vm1-automation",
          configMaps: ["default-user"], secrets: ["host-keys"]}}
]}' | kc apply -f -

for i in $(seq 1 30); do
  [ "$(provision_phase)" = Pending ] && break
  [ "$i" = 30 ] && fail "Provision did not reach phase Pending (got '$(provision_phase)')"
  sleep 2
done
pass "row $ROW_ID resources applied; Provision is Pending"
