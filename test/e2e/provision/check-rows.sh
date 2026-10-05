#!/usr/bin/env bash
# Check rows.json, the one list of provision E2E rows, and what it refers to:
# - field names, types and allowed values; unique ids, MACs and hostnames;
# - a row boots either an ISO (iso_artifact, over NFS) or a netboot kernel
#   and initrd; "nfs": true exactly when it boots an ISO, and then the guest
#   has at most 2048 MB RAM, so an ISO loaded into RAM could not fit;
# - the negative row ("expect": "stall") uses the RTL8168 NIC without firmware;
# - every example manifest and automation file exists, the manifests define
#   the row's BootConfig and BootArtifacts, the BootConfig's mode and refs
#   match the row, and an ISO BootConfig's kernelArgs mount NFS
#   (netboot=nfs nfsroot={{.NFSRoot}}) and have no url=, iso-url= or toram.
# Prints every problem and exits 1 if there is any. Needs bash, jq and awk;
# it changes nothing, so the lint workflow runs it on every push.
# Usage: check-rows.sh [rows.json [examples directory]]
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../../.." && pwd)
rows_file=${1:-$here/rows.json}
examples=${2:-$repo/examples}

problems=0
problem() {
  echo "check-rows: $*" >&2
  problems=$((problems + 1))
}

# ── Fields and values, in jq ──────────────────────────────────────
# shellcheck disable=SC2016 # a jq program, not shell
field_problems=$(jq -r '
  def known: ["id", "name", "manifests", "bootconfig", "kernel_artifact",
    "initrd_artifact", "firmware_artifact", "iso_artifact", "kernel_path",
    "initrd_path", "automation", "hostname", "os_id", "version_id", "mac",
    "nic", "ram_mb", "expect", "nfs", "verify_rtl_firmware"];
  def required_strings: ["id", "name", "bootconfig", "kernel_path",
    "initrd_path", "hostname", "os_id", "version_id", "mac", "nic", "expect"];
  def optional_strings: ["kernel_artifact", "initrd_artifact",
    "firmware_artifact", "iso_artifact"];
  def text: type == "string" and length > 0;
  def duplicates(f): [.[] | objects | f | strings] | group_by(.) | map(select(length > 1) | .[0]) | .[];

  if type != "array" or length == 0 then "the file must be a non-empty JSON array of rows"
  else
    (to_entries[] | .key as $index | .value as $row
      | (if ($row | type) == "object" and ($row.id | text) then $row.id else "row \($index)" end) as $id
      | if ($row | type) != "object" then "\($id): not a JSON object"
        else $row | (
          (keys[] | select(. as $key | known | index($key) | not) | "\($id): unknown field \"\(.)\""),
          (required_strings[] as $f | select($row[$f] | text | not) | "\($id): \"\($f)\" must be a non-empty string"),
          (optional_strings[] as $f | select(has($f) and ($row[$f] | text | not)) | "\($id): \"\($f)\" must be a non-empty string when present"),
          (select(.id | text) | select(.id | test("^[a-z0-9][a-z0-9.-]*$") | not) | "\($id): id must be lower-case letters, digits, dots and hyphens"),
          (select(.mac | text) | select(.mac | test("^([0-9a-f]{2}-){5}[0-9a-f]{2}$") | not) | "\($id): mac \"\(.mac)\" must be lower-case hex pairs joined by hyphens"),
          (select(.nic | text) | select(.nic | IN("virtio", "rtl8168") | not) | "\($id): nic \"\(.nic)\" must be virtio or rtl8168"),
          (select(.expect | text) | select(.expect | IN("complete", "stall") | not) | "\($id): expect \"\(.expect)\" must be complete or stall"),
          (select((.ram_mb | type) != "number" or .ram_mb != (.ram_mb | floor) or .ram_mb <= 0) | "\($id): ram_mb must be a positive whole number"),
          (select((.manifests | type) != "array" or (.manifests | length) == 0 or any(.manifests[]; text | not)) | "\($id): manifests must be a non-empty list of example names"),
          (select((.automation | type) != "object" or (.automation | length) == 0 or any(.automation[]; text | not)) | "\($id): automation must map file names to files in automation/"),
          (("nfs", "verify_rtl_firmware") as $f | select(has($f) and ($row[$f] | type) != "boolean") | "\($id): \"\($f)\" must be true or false"),
          (if has("iso_artifact") then
             (select(has("kernel_artifact") or has("initrd_artifact") or has("firmware_artifact")) | "\($id): an ISO row has no kernel, initrd or firmware artifact"),
             (select(.nfs != true) | "\($id): a row with iso_artifact boots over NFS and needs \"nfs\": true"),
             (select((.ram_mb | type) == "number" and .ram_mb > 2048) | "\($id): an NFS row must have ram_mb <= 2048 (has \(.ram_mb)), so a guest that loaded the ISO into RAM could not install")
           else
             (select((has("kernel_artifact") and has("initrd_artifact")) | not) | "\($id): a netboot row needs kernel_artifact and initrd_artifact (or iso_artifact for an ISO row)"),
             (select(.nfs == true) | "\($id): \"nfs\": true needs an ISO row (iso_artifact)")
           end),
          (select(.expect == "stall") | (
             (select(.nic != "rtl8168") | "\($id): the stall row proves the missing NIC firmware, so it needs nic rtl8168"),
             (select(has("firmware_artifact")) | "\($id): the stall row must not have firmware_artifact"),
             (select(.verify_rtl_firmware == true) | "\($id): the stall row installs nothing to verify"))),
          (select(.verify_rtl_firmware == true and (.nic != "rtl8168" or (has("firmware_artifact") | not))) | "\($id): verify_rtl_firmware needs nic rtl8168 and firmware_artifact")
        ) end),
    (duplicates(.id) | "duplicate id \"\(.)\""),
    (duplicates(.mac) | "duplicate mac \"\(.)\""),
    (duplicates(.hostname) | "duplicate hostname \"\(.)\"")
  end' "$rows_file") || { problem "$rows_file is not valid JSON"; exit 1; }
if [ -n "$field_problems" ]; then
  while IFS= read -r line; do problem "$line"; done <<<"$field_problems"
  echo "check-rows: $problems problem(s) in $rows_file" >&2
  exit 1
fi

# ── The files each row refers to ──────────────────────────────────
# manifest_objects <file>: one line per object in a multi-document example
# manifest: kind, name, BootConfig mode (iso or netboot), iso.artifactRef,
# netboot.kernelRef, initrdRef, firmwareRef and kernelArgs, separated by the
# ASCII unit separator (not a tab: read would merge empty tab-separated fields).
# It reads the plain layout examples/ uses; an object it cannot read shows up
# as missing, never as present.
manifest_objects() {
  awk -v US=$'\037' '
    function flush() {
      if (kind != "") print kind US name US mode US iso US kernel US initrd US firmware US args
      kind = name = mode = iso = kernel = initrd = firmware = args = section = ""
    }
    function value(line) { sub(/^[^:]*:[ ]*/, "", line); gsub(/^"|"$/, "", line); return line }
    /^---/ { flush(); next }
    /^kind:/ { kind = value($0) }
    /^metadata:/ { section = "metadata" }
    /^spec:/ { section = "spec" }
    section == "metadata" && /^  name:/ { name = value($0) }
    section == "spec" && /^  iso:/ { mode = "iso" }
    section == "spec" && /^  netboot:/ { mode = "netboot" }
    section == "spec" && /^    artifactRef:/ { iso = value($0) }
    section == "spec" && /^    kernelRef:/ { kernel = value($0) }
    section == "spec" && /^    initrdRef:/ { initrd = value($0) }
    section == "spec" && /^    firmwareRef:/ { firmware = value($0) }
    section == "spec" && /^  kernelArgs:/ { args = value($0) }
    END { flush() }
  ' "$1"
}

field() { jq -r --arg id "$1" --arg f "$2" '.[] | select(.id == $id) | .[$f] // empty' "$rows_file"; }

for id in $(jq -r '.[].id' "$rows_file"); do
  objects=""
  for manifest in $(jq -r --arg id "$id" '.[] | select(.id == $id) | .manifests[]' "$rows_file"); do
    file=$examples/$manifest.yaml
    if [ -f "$file" ]; then
      objects+=$(manifest_objects "$file")$'\n'
    else
      problem "$id: manifest $examples/$manifest.yaml does not exist"
    fi
  done
  for source in $(jq -r --arg id "$id" '.[] | select(.id == $id) | .automation[]' "$rows_file"); do
    [ -f "$here/automation/$source" ] || problem "$id: automation file test/e2e/provision/automation/$source does not exist"
  done

  for kind_field in iso_artifact kernel_artifact initrd_artifact firmware_artifact; do
    name=$(field "$id" "$kind_field")
    [ -z "$name" ] || awk -F$'\037' -v n="$name" '$1 == "BootArtifact" && $2 == n { found = 1 } END { exit !found }' <<<"$objects" \
      || problem "$id: the row's manifests define no BootArtifact \"$name\" ($kind_field)"
  done

  bootconfig=$(field "$id" bootconfig)
  line=$(awk -F$'\037' -v n="$bootconfig" '$1 == "BootConfig" && $2 == n' <<<"$objects")
  if [ -z "$line" ]; then
    problem "$id: the row's manifests define no BootConfig \"$bootconfig\""
    continue
  fi
  IFS=$'\037' read -r _ _ mode iso_ref kernel_ref initrd_ref firmware_ref kernel_args <<<"$line"
  if [ -n "$(field "$id" iso_artifact)" ]; then
    [ "$mode" = iso ] || problem "$id: BootConfig $bootconfig is not in iso mode, but the row has iso_artifact"
    [ "$iso_ref" = "$(field "$id" iso_artifact)" ] \
      || problem "$id: BootConfig $bootconfig uses ISO \"$iso_ref\", the row says \"$(field "$id" iso_artifact)\""
    case " $kernel_args " in
      *" netboot=nfs "*) ;;
      *) problem "$id: BootConfig $bootconfig kernelArgs must have netboot=nfs" ;;
    esac
    case " $kernel_args " in
      *" nfsroot={{.NFSRoot}} "*) ;;
      *) problem "$id: BootConfig $bootconfig kernelArgs must have nfsroot={{.NFSRoot}}" ;;
    esac
    case " $kernel_args " in
      *" url="* | *" iso-url="* | *" toram "* | *" toram="*)
        problem "$id: BootConfig $bootconfig kernelArgs would copy the ISO into RAM (url=, iso-url= or toram)" ;;
    esac
  else
    [ "$mode" = netboot ] || problem "$id: BootConfig $bootconfig is not in netboot mode, but the row has no iso_artifact"
    [ "$kernel_ref" = "$(field "$id" kernel_artifact)" ] \
      || problem "$id: BootConfig $bootconfig uses kernel \"$kernel_ref\", the row says \"$(field "$id" kernel_artifact)\""
    [ "$initrd_ref" = "$(field "$id" initrd_artifact)" ] \
      || problem "$id: BootConfig $bootconfig uses initrd \"$initrd_ref\", the row says \"$(field "$id" initrd_artifact)\""
    [ "$firmware_ref" = "$(field "$id" firmware_artifact)" ] \
      || problem "$id: BootConfig $bootconfig uses firmware \"${firmware_ref:-<none>}\", the row says \"$(field "$id" firmware_artifact)\""
  fi
done

if [ "$problems" -gt 0 ]; then
  echo "check-rows: $problems problem(s) in $rows_file" >&2
  exit 1
fi
echo "check-rows: $(jq length "$rows_file") rows in $rows_file are consistent"
