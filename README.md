# isoboot

isoboot installs operating systems on bare-metal machines over the network,
driven by Kubernetes resources. You describe the boot files, the machine (by
MAC address) and the install files (kickstart, preseed or autoinstall); isoboot
downloads and checks the files, answers the machine's PXE boot, serves the
installer its kernel, initrd and install files, and tracks each install from
`Pending` to `Complete`. A machine with nothing to install boots its local
disk.

Tested end to end on every pull request labelled `e2e`: AlmaLinux 10.2,
Rocky 10.2, Debian 13 (with and without NIC firmware) and Ubuntu 24.04.5,
26.04.1 and 26.10 (installed over NFS with 2 GiB of RAM).

## How a machine is installed

1. The machine PXE-boots. Your DHCP server gives it an address; isoboot's
   dnsmasq adds the boot options as a proxyDHCP server and serves iPXE over
   TFTP. iPXE asks `GET /dynamic/conditional-boot?mac=<mac>`.
2. httpd finds the `Machine` with that MAC and its `Pending` `Provision`. If
   there is one and its `BootConfig` is `Ready`, it answers an iPXE script
   that loads the BootConfig's kernel and initrd from `/static/` with the
   rendered kernel arguments. Otherwise it answers 404 (and logs why), and the
   machine boots its local disk.
3. The installer fetches its install files from
   `/dynamic/automation/<provision>/<file>`: the `ProvisionAutomation`'s files,
   rendered as Go templates with the Provision's ConfigMaps and Secrets. It
   reports `InProgress` and `Complete` to `/dynamic/status`.
4. Debian, AlmaLinux and Rocky install from their netboot kernel and initrd.
   Ubuntu live-server has no netboot kernel, so its `BootConfig` uses the ISO:
   the controller unpacks the whole ISO, `nfsd` exports the tree read-only over
   NFSv3, and casper mounts it (`netboot=nfs nfsroot={{.NFSRoot}}`). Nothing is
   copied into RAM, so 2 GiB is enough. Never add `url=`, `iso-url=` or
   `toram`: they download the ISO into RAM.
5. Package downloads during the install can go through the squid cache
   (`{{.ProxyURL}}`).

What an Ubuntu answer file (autoinstall `user-data`) must contain, screen by
screen with 4K screenshots of every boot, is in
[docs/ubuntu-autoinstall-journey.md](docs/ubuntu-autoinstall-journey.md).

## Components

All run on one node (`nodeName`) and keep their data under `dataDir` on that
node.

| Component | What it does | Network |
|---|---|---|
| controller manager (`/manager`) | Reconciles the resources: downloads and checks BootArtifacts, builds each BootConfig's boot directory, unpacks ISOs for NFS | cluster |
| httpd (`/httpd`) | `/conditional-boot`, `/automation`, `/status` behind nginx | cluster |
| nfsd (`/nfsd`) | Our own read-only NFSv3 and MOUNT server plus a port mapper, for Ubuntu installs only. Runs as UID 0 with only `NET_BIND_SERVICE` | host: TCP 111, 2049 |
| nginx (upstream `nginx-unprivileged`) | `/static/` (boot files) and `/dynamic/` (to httpd) | host: TCP 8080 |
| dnsmasq | proxyDHCP and TFTP for iPXE | host: UDP 67, 69, 4011 |
| squid | Package cache for installers | host: TCP 3128 |

The three Go programs ship in one image, `ghcr.io/isoboot/isoboot`, and the
chart starts each with its own command. dnsmasq and squid have their own
images (`isoboot-dnsmasq`, `isoboot-squid`).

## Resources

All are namespaced, in the group `isoboot.github.io/v1alpha1`, and must be
created in the namespace the chart is installed in.

- **BootArtifact**: one file to download (`url`) with its `sha256` or
  `sha512`. Phases `Downloading`, `Ready`, `Error`. Stored at
  `<dataDir>/nginx/static/artifacts/<name>/<file>`.
- **BootConfig**: what a machine boots, with `kernelArgs` (a Go template;
  an invalid template sets the BootConfig to `Error`).
  - `spec.netboot`: `kernelRef`, `initrdRef` and optional `firmwareRef`
    (BootArtifact names). The boot directory is
    `<dataDir>/nginx/static/boot/<name>/kernel/<file>` and `initrd/<file>`;
    with firmware, the initrd is the initrd and the firmware archive
    concatenated (how Debian's installer finds non-free firmware).
  - `spec.iso`: `artifactRef` (an ISO BootArtifact), `kernelPath` and
    `initrdPath` inside the ISO. The tree is unpacked to
    `<dataDir>/nfs/<name>/` and exported as `/<name>`; `vmlinuz` and `initrd`
    are copied to `<dataDir>/nginx/static/boot/<name>/`.
- **Machine**: a `mac`, hyphen-separated (for example `02-00-00-ab-cd-01`;
  compared without regard to case).
- **ProvisionAutomation**: `files`, a map of file name to template.
- **Provision**: one install: `machineRef`, `bootConfigRef`,
  `provisionAutomationRef`, and optional `configMaps` and `secrets` whose keys
  the templates read (`{{ index .ConfigMaps "key" }}`). Phases `Pending`,
  `InProgress`, `Complete` (and `Failed`, `ConfigError`, `WaitingForBootSource`).

Template variables: kernel arguments get `{{.ProvisionAutomationBaseURL}}`,
`{{.ProvisionName}}`, `{{.UpdatePhaseURL}}`, `{{.ProxyURL}}` and, in ISO mode,
`{{.NFSRoot}}`; install files get `{{.ProvisionName}}`, `{{.UpdatePhaseURL}}`,
`{{.ProxyURL}}`, `.ConfigMaps` and `.Secrets`.

`examples/` has a tested BootConfig with its BootArtifacts for each
supported release, and `test/e2e/provision/automation/` has tested install
files (kickstart, preseed, autoinstall user-data).

## Installing

On the node that will serve PXE:

- The node must be on the machines' network (the PXE subnet), with a DHCP
  server there that is not isoboot.
- Nothing else may listen on UDP 67, 69 and 4011, or TCP 111, 2049, 3128
  and 8080. Stop and disable `rpcbind` and any kernel NFS server, for example
  `sudo systemctl disable --now rpcbind.socket rpcbind.service nfs-server`.
- Create the data directory, owned by UID 65532:
  `sudo mkdir -p /data/isoboot && sudo chown 65532:65532 /data/isoboot`.
  Leave room for the downloads and, for each ISO-mode BootConfig, a full copy
  of the unpacked ISO.

Then install the chart. `nodeName` and `dnsmasq.subnet` are required:

```bash
helm install isoboot oci://ghcr.io/isoboot/charts/isoboot --version <version> \
  --namespace isoboot-system --create-namespace \
  --set nodeName=<node> --set dnsmasq.subnet=192.168.1.0/24
```

The CRDs are in the chart's `crds/` directory: Helm installs them first and
never upgrades or deletes them. On upgrade, apply `charts/isoboot/crds/` with
kubectl first. See `charts/isoboot/values.yaml` for every value.

Then apply an example and create the Machine, ProvisionAutomation and
Provision in the same namespace. `test/e2e/provision/apply-row.sh` does
exactly that for the E2E and is a working reference.

## Security defaults

- The controller manager and httpd see only the release namespace
  (`--namespace`), with a Role and RoleBinding there instead of cluster-wide
  rights.
- nginx (`/static/`, `/dynamic/`) and squid accept requests only from the PXE
  subnet (`dnsmasq.subnet`) and localhost.
- nfsd accepts NFS, MOUNT and port-mapper connections only from
  `nfsd.allowedCIDRs` (default: the PXE subnet). Everything it exports is
  read-only. It allows 8 NFS connections per client address (512 in all) and
  4 port-mapper connections per address (1024 in all); machines behind one
  NAT address share those. A request may be at most 8 KiB and a READ returns
  at most 1 MiB, with 32 MiB of READ data in flight across all clients.
- squid refuses loopback (127.0.0.0/8), link-local addresses,
  `squid.blockedDestinationCIDRs` (default: the k3s pod and service
  networks), ports other than 80, 443 and nginx's port, and CONNECT except to
  443. The node's other addresses stay reachable on those ports.
- `/automation` serves a Provision's files only while it is `Pending` or
  `InProgress` (404 afterwards), since they can contain rendered Secrets.
  `/automation` and `/conditional-boot` answers are sent with
  `Cache-Control: no-store`.
- Every download is checked against the hash in its BootArtifact.

## Known limitations

- Changing a BootConfig's ISO while installs from it are running breaks them:
  the tree under `nfs/<name>/` is replaced.
- Each ISO-mode BootConfig keeps its own unpacked copy of its ISO, even when
  two use the same ISO, and nothing checks free space first.
- Ubuntu's autoinstall `proxy:` (and Debian's preseed mirror proxy) is left
  in the installed system's apt configuration. Remove it in a late command if
  the installed machine cannot reach squid.
- `config/` (kustomize) duplicates the chart and is only used by the Kind
  E2E tests; the chart is the supported way to install.
- In a checkout, `charts/isoboot/Chart.yaml` carries a placeholder version
  and appVersion `0.1.0`, for which no image is published. Install a released
  chart from `oci://ghcr.io/isoboot/charts/isoboot`, or package the checkout
  with `helm package charts/isoboot --version X --app-version X` for a
  published version X.

## Development

```bash
make test      # unit tests (envtest)
make lint      # golangci-lint
make manifests # regenerate CRDs and RBAC from the Go types; also copies the
               # CRDs to charts/isoboot/crds/
```

`make docker-build IMG=<image>` builds the one Go image. The CRDs
(`config/crd/bases/`, `charts/isoboot/crds/`), `config/rbac/role.yaml` and
`zz_generated.deepcopy.go` are generated: change the Go types and run
`make manifests generate`. `AGENTS.md` has the project layout and conventions.

### Tests

| Test | Where | When |
|---|---|---|
| Unit tests | `make test` | every push |
| Lint, `test/e2e/provision/check-rows.sh`, `test/e2e/provision/selftest.sh` | `.github/workflows/lint.yml` | every push |
| Kind E2E | `test/e2e/`, `make test-e2e` | every push |
| Provision E2E | `test/e2e/provision/`, `.github/workflows/test-provision-e2e.yaml` | PRs labelled `e2e`, or manual |

### Provision E2E

`test/e2e/provision/` installs a real OS end to end on one host: k3s, the
chart, a site DHCP server on a bridge, and a UEFI QEMU/KVM guest that
PXE-boots, installs, reboots from its disk and is checked over SSH (key
login only, hostname, injected host keys, OS version, machine-id). The rows
are listed once, in `test/e2e/provision/rows.json`; `check-rows.sh` validates
that file and what it refers to. The negative Debian row boots a Realtek
RTL8168 NIC without its firmware and must stay `Pending` without ever fetching
its preseed. The Ubuntu rows also check the guest's kernel command line and
nfsd's mount log. Every row fails if an isoboot pod restarted.

`run.sh <row>` runs every phase (`host-setup`, `k3s`, `network`, `images`,
`helm-install`, `apply-row`, `boot-install`, `verify`, `collect-logs`);
`run.sh <row> <phase>` runs one, which is what CI does per step. The scripts
change the host for real (they uninstall k3s, delete `/data/isoboot`, add a
bridge and iptables rules), so they refuse to run unless `GITHUB_ACTIONS=true`,
the host has the marker `/etc/isoboot-e2e-vm`, or `E2E_ALLOW_THIS_HOST=1` is
set, and they always use k3s's own kubeconfig.

To run them locally, on an x86-64 Linux host with KVM, nested
virtualisation and multipass:

```bash
hack/e2e-local.sh                           # every row, in a new VM, deleted afterwards
hack/e2e-local.sh --row ubuntu-26.04 --keep # keep the VM and the row's state for debugging
hack/e2e-local.sh --reuse --row debian-13-firmware
```

It creates the multipass VM `isoboot-e2e-local` (4 CPUs, 12G, 60G) and marks
it as disposable, copies the checkout in (uncommitted changes included),
builds the images inside it and runs the rows one at a time. Downloads, the
squid cache and the RTL8168 QEMU build stay in the VM, so later rows and
`--reuse` runs do not fetch them again; a reused VM is never deleted. Waits
for downloads follow their progress, so a slow link only makes a row slower.
Logs go to `e2e-logs/<time>/<row>/`. See the script's header for all options.

The guest has a 3840x2160 screen. The row saves it as PNG in its logs
(`screen-final.png`, and `screen-<what>.png` when a wait fails), and a VNC
server on `127.0.0.1:5900` inside the VM (`E2E_VNC_DISPLAY`, default `:0`)
shows it live: from your workstation run
`ssh -J you@host -L 5900:127.0.0.1:5900 ubuntu@<vm-ip>` (`multipass info
isoboot-e2e-local` shows the address; the VM's `ubuntu` user needs your
public key), then point a VNC viewer at `localhost:5900`.

`test/e2e/provision/selftest.sh` tests the harness itself (the host guard, the
checks and the local runner) against stub commands; it needs bash, jq and
docker but no KVM.

## License

MIT, see `LICENSE`.
