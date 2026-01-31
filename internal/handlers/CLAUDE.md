# handlers/CLAUDE.md

HTTP handlers for isoboot-http server.

## Handlers

### BootHandler (boot.go)
- `GET /boot/conditional-boot?mac=xx-xx-xx-xx-xx-xx` - Returns BootTarget template if Provision exists, 404 otherwise
- `GET /boot/done?mac={mac}` - Marks Provision as completed (call from preseed late_command with `{{ .MAC }}`)

Template variables: Host, Port, MachineName, Hostname, Domain, BootTarget, BootMedia, UseFirmware, ProvisionName, KernelFilename, InitrdFilename, HasFirmware

Port detection: `portFromRequest(r)` reads `X-Forwarded-Port` header (set by nginx), falls back to port from `Host` header, defaults to "80".

### AnswerHandler (answer.go)
- `GET /answer/{provisionName}/{filename}` - Serves rendered ResponseTemplate files
- Uses direct O(1) lookup by provision name (not hostname search)

## Error Handling

- 400 Bad Request - Missing required parameters
- 404 Not Found - Resource not found (Provision, Machine, file)
- 502 Bad Gateway - gRPC/controller communication error
- Always set `Content-Length` header (iPXE requirement)

## Machine Name Splitting

`splitHostDomain(name)` splits machine name on first dot:
- `vm-01.lan` → Hostname: `vm-01`, Domain: `lan`
- `web.example.com` → Hostname: `web`, Domain: `example.com`
- `server01` → Hostname: `server01`, Domain: ``
