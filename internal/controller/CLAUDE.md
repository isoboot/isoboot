# controller/CLAUDE.md

Controller reconciliation logic for isoboot-controller.

## Components

### Controller (controller.go)
Main reconciliation loop that watches Provision CRDs:
- Validates references (Machine, BootTarget, ResponseTemplate, ConfigMaps, Secrets)
- Manages status transitions: Pending → InProgress → Complete/Failed
- Renders templates with merged ConfigMap/Secret data
- Timeout handling for stuck InProgress provisions (30 min default)

### BootSource Downloader (bootsource.go)
Downloads and caches kernel/initrd/firmware files for BootSources:
- Verifies checksums (SHA256) if provided
- Tracks download progress in BootSource status
- Extracts ISO contents for serving

### gRPC Server (grpc.go)
Exposes primitive CRD accessors to isoboot-http:
- `GetMachineByMAC` - Find machine by MAC address
- `GetProvisionsByMachine` - List provisions for a machine
- `UpdateProvisionStatus` - Update provision status directly
- `GetProvision` - Get provision by name
- `GetConfigMaps` - Get merged ConfigMap data
- `GetSecrets` - Get merged Secret data
- `GetResponseTemplate` - Get response template by name
- `GetBootTarget` - Get boot target by name
- `GetBootSource` - Get boot source by name
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

