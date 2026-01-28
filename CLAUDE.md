# CLAUDE.md

Go code repository for isoboot controller and HTTP server.

## Project Context

This repo works alongside `isoboot-chart` (Helm chart with CRDs). Together they provide PXE boot infrastructure on Kubernetes.

**Workflow**: Machine PXE boots → dnsmasq responds → iPXE loads → fetches boot script from isoboot-http → installer runs → fetches answer files from /answer/{provisionName}/{filename} → completes via /boot/done?id={machineName}

## Git Conventions

- **Never force push** - use squash merge at PR merge time
- PRs required for main branch

## Package Structure

```
cmd/
├── isoboot-controller/   # Kubernetes controller binary
└── isoboot-http/         # HTTP server binary

internal/
├── config/              # Configuration and hot-reload
├── controller/          # Controller reconciliation logic
├── controllerclient/    # gRPC client for HTTP→controller communication
├── handlers/            # HTTP handlers (boot, iso, answer)
├── iso/                 # ISO extraction utilities
└── k8s/                 # Kubernetes client and CR types

api/
├── controllerpb/        # Generated protobuf code
└── proto/               # Proto definitions
```

## Key Patterns

### gRPC Communication
- Controller exposes gRPC on port 8081
- HTTP server connects as client
- Used for: GetPendingBoot, MarkBootStarted, GetRenderedTemplate, etc.

### Template Rendering
Templates use Go `text/template` with `missingkey=error`:
```go
tmpl, err := template.New("").Option("missingkey=error").Parse(content)
if err != nil {
    return err
}
```

Custom template functions (Helm/sprig-style):
- `b64enc` - base64 encode a string: `{{ .Password | b64enc }}`
- `hasKey` - check if key exists in map: `{{ if hasKey . "ssh_host_ed25519_key_pub" }}...{{ end }}`

Available variables in ResponseTemplate (preseed/answer files):
- `.Host` - HTTP server host IP
- `.Port` - HTTP server port
- `.Hostname` - machine reference from Provision
- `.Target` - boot target reference from Provision
- `.MAC` - machine MAC address (dash-separated, lowercase)
- `.MachineId` - systemd machine-id from Provision (use `hasKey` to check if set)
- `.key` - values merged from referenced ConfigMaps and Secrets (flat namespace)
- `.ssh_host_*_key_pub` - auto-derived public keys for SSH host keys in secrets

Available variables in BootTarget (iPXE scripts):
- `.Host` - HTTP server host IP
- `.Port` - HTTP server port
- `.MachineName` - full machine name (e.g., "vm-01.lan")
- `.Hostname` - first part before dot (e.g., "vm-01")
- `.Domain` - everything after first dot (e.g., "lan")
- `.BootTarget` - BootTarget resource name
- `.ProvisionName` - Provision resource name (use for answer file URLs)

### Error Handling in HTTP Handlers
- Return 502 Bad Gateway for gRPC/transport errors
- Return 404 Not Found only for "resource not found" errors
- Always set Content-Length header

## Commands

```bash
# Run tests
go test ./...

# Build binaries
go build ./cmd/isoboot-controller
go build ./cmd/isoboot-http

# Generate protobuf (if proto files change)
protoc --go_out=. --go-grpc_out=. api/proto/controller.proto
```

## Testing Guidelines

- Unit tests alongside code: `foo_test.go`
- Use `httptest.NewRecorder()` for HTTP handler tests
- Mock external dependencies (k8s client, gRPC client)

## Before Committing

- Run `go test ./...`
- Keep commits focused on single logical changes
