# handlers/CLAUDE.md

HTTP handlers for isoboot-http server.

## Handlers

### BootHandler (boot.go)
- `GET /boot/boot.ipxe` - Initial iPXE script (chains to conditional-boot)
- `GET /boot/conditional-boot?mac=xx-xx-xx-xx-xx-xx` - Returns BootTarget template if Deploy exists, 404 otherwise
- `GET /boot/done?id={machineName}` - Marks Deploy as completed (call from preseed late_command)

Template variables: Host, Port, MachineName, Hostname, Domain, BootTarget

### ISOHandler (iso.go)
- `GET /iso/content/{bootTarget}/{isoFile}/{path...}` - Serves extracted ISO contents
- Firmware merging: If BootTarget has `includeFirmwarePath` and path matches, appends firmware.cpio.gz

### AnswerHandler (answer.go)
- `GET /answer/{machineName}/{filename}` - Serves rendered ResponseTemplate files

## Error Handling

- 400 Bad Request - Missing required parameters
- 404 Not Found - Resource not found (Deploy, Machine, file)
- 502 Bad Gateway - gRPC/controller communication error
- Always set `Content-Length` header (iPXE requirement)

## Machine Name Splitting

`splitHostDomain(name)` splits machine name on first dot:
- `vm-01.lan` → Hostname: `vm-01`, Domain: `lan`
- `web.example.com` → Hostname: `web`, Domain: `example.com`
- `server01` → Hostname: `server01`, Domain: ``
