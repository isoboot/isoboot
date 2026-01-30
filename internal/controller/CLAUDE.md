# controller/CLAUDE.md

Controller reconciliation logic for isoboot-controller.

## Components

### Controller (controller.go)
Main reconciliation loop that watches Provision and BootMedia CRDs:
- Validates references (Machine, BootTarget, BootMedia, ResponseTemplate, ConfigMaps, Secrets)
- Two-step readiness check: Provision → BootTarget → BootMedia (status must be Complete)
- Manages status transitions: Pending → InProgress → Complete/Failed
- WaitingForBootMedia status when BootMedia is not yet Complete
- Timeout handling for stuck InProgress provisions (30 min default)

### BootMedia Downloader (bootmedia.go)
Downloads and caches files for BootMedia resources. Four download flows:
1. **Direct** (kernel + initrd URLs, no firmware): Downloads to flat `{bmDir}/` layout
2. **Direct + firmware**: Downloads kernel to `{bmDir}/`, initrd to `{bmDir}/no-firmware/`, firmware to temp, concatenates initrd + firmware → `{bmDir}/with-firmware/`
3. **ISO** (ISO URL + extraction paths, no firmware): Downloads ISO to temp, extracts kernel/initrd to flat `{bmDir}/` layout
4. **ISO + firmware**: Same as ISO but with firmware subdirectory layout

Features:
- Verifies checksums (SHA256) if provided
- Tracks download progress per named field in BootMedia status (kernel, initrd, iso, firmware, firmwareInitrd)
- Firmware concatenation: `no-firmware/initrd + firmware.cpio.gz → with-firmware/initrd`

### gRPC Server (grpc.go)
Exposes primitive CRD accessors to isoboot-http:
- `GetMachineByMAC` - Find machine by MAC address
- `GetProvisionsByMachine` - List provisions for a machine
- `UpdateProvisionStatus` - Update provision status directly
- `GetProvision` - Get provision by name
- `GetConfigMaps` - Get merged ConfigMap data
- `GetSecrets` - Get merged Secret data
- `GetResponseTemplate` - Get response template by name
- `GetBootTarget` - Get boot target by name (returns template, bootMediaRef, useFirmware)
- `GetBootMedia` - Get boot media by name (returns kernelFilename, initrdFilename, hasFirmware)
- `GetConfigMapValue` - Get single value from ConfigMap

### SSH Key Derivation (sshkeys.go)
Derives public keys from private SSH host keys in secrets:
- Supports RSA, ECDSA, Ed25519 key types
- Auto-generates `.ssh_host_*_key_pub` template variables

## Validation

### machineId Format
Machine IDs must be exactly 32 lowercase hex characters (systemd machine-id format):
```go
var validMachineId = regexp.MustCompile(`^[0-9a-f]{32}$`)
```
Uppercase hex is rejected - users must provide lowercase.

