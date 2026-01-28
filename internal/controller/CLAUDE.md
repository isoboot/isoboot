# controller/CLAUDE.md

Controller reconciliation logic for isoboot-controller.

## Components

### Controller (controller.go)
Main reconciliation loop that watches Provision CRDs:
- Validates references (Machine, BootTarget, ResponseTemplate, ConfigMaps, Secrets)
- Manages status transitions: Pending → InProgress → Complete/Failed
- Renders templates with merged ConfigMap/Secret data
- Timeout handling for stuck InProgress provisions (30 min default)

### DiskImage Downloader (diskimage.go)
Downloads and caches ISO/firmware files for BootTargets:
- Verifies checksums (SHA256/SHA512) if provided
- Tracks download progress in DiskImage status
- Extracts ISO contents for serving

### gRPC Server (grpc.go)
Exposes controller functions to isoboot-http:
- `GetPendingBoot` - Find pending provision by MAC
- `MarkBootStarted` - Transition to InProgress
- `MarkBootCompleted` - Transition to Complete
- `GetRenderedTemplate` - Render answer file for provision

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

## Known Issues / Tech Debt

### DiskImage Download Timeout (HIGH)
**File**: diskimage.go:108, :190, :219

The `downloadRequestTimeout` (15 min) applies to the entire DiskImage operation. If ISO download takes close to 15 minutes, firmware download inherits near-expired context and may fail even with a valid URL.

**Potential fixes**:
- Per-file timeouts (new context per download)
- Longer overall timeout with per-file sub-timeouts

### HEAD Request Error Handling (MEDIUM)
**File**: diskimage.go:266, :280

Any 4xx/5xx response to HEAD request causes immediate failure. Some mirrors return 403/405 for HEAD but allow GET.

**Potential fixes**:
- Treat HEAD errors as "no size available" and continue to GET
- Special-case 405 Method Not Allowed
