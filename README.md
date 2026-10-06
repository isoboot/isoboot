# isoboot

isoboot installs operating systems on bare-metal machines over the network,
driven by Kubernetes resources. You describe the boot files, the machine (by
MAC address) and the install files (kickstart, preseed or autoinstall); isoboot
downloads and checks the files, answers the machine's PXE boot, serves the
installer its kernel, initrd and install files, and tracks each install from
`Pending` to `Complete`. A machine with nothing to install boots its local
disk.

Tested end to end on every pull request labelled `e2e`: AlmaLinux 10.2,
Rocky 10.2, Debian 13 with NIC firmware (and a check that it does not
install without it) and Ubuntu 24.04.5, 26.04.1 and 26.10 (installed over
NFS with 2 GiB of RAM).

The documentation is in [docs/](docs/README.md): how to install isoboot
and provision a machine, and a reference for every resource and template.

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

| Kind | What it is |
|---|---|
| BootArtifact | One file to download (`url`) with its `sha256` or `sha512`: a kernel, an initrd, a firmware archive or an ISO. |
| BootConfig | What a machine boots: `spec.netboot` (kernel, initrd, optional firmware) or `spec.iso` (an ISO, unpacked and exported over NFS), and `kernelArgs`, a Go template. |
| Machine | A machine, by the `mac` it PXE-boots with (hyphen-separated). |
| ProvisionAutomation | `files`: the install files (kickstart, preseed, autoinstall), as Go templates. |
| Provision | One install: a Machine, a BootConfig, a ProvisionAutomation, and the ConfigMaps and Secrets the templates read. Phase `Pending`, then `InProgress`, then `Complete`. |

Every field, phase and message is in
[docs/reference/custom-resources.md](docs/reference/custom-resources.md), and
the template variables and functions in
[docs/reference/templates.md](docs/reference/templates.md).

`examples/` has a tested BootConfig with its BootArtifacts for each
supported release, and `test/e2e/provision/automation/` has tested install
files (kickstart, preseed, autoinstall user-data).

## Installing

On one node on the machines' network, with a DHCP server there that is not
isoboot, free ports (UDP 67, 69, 4011; TCP 111, 2049, 3128, 8080) and a data
directory owned by UID 65532:

```bash
helm install isoboot oci://ghcr.io/isoboot/charts/isoboot --version <version> \
  --namespace isoboot-system --create-namespace \
  --set nodeName=<node> --set dnsmasq.subnet=192.168.1.0/24
```

[Install isoboot](docs/how-to/install.md) has the requirements, the values,
health checks, upgrades and uninstalling;
[Provision a machine](docs/how-to/provision-a-machine.md) installs a first
machine.

## Security defaults

- The controller manager and httpd see only the release namespace
  (`--namespace`), with a Role and RoleBinding there instead of cluster-wide
  rights.
- nginx (`/static/`, `/dynamic/`) accepts requests only from the PXE subnet
  (`dnsmasq.subnet`), localhost and the node's own addresses (installers'
  requests relayed by squid come from the node). squid accepts requests only
  from the PXE subnet and localhost.
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
make test      # unit tests (envtest), and the objects and links in docs/
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
PXE-boots, installs, reboots from its disk and is checked over SSH. The rows
are listed once, in `test/e2e/provision/rows.json`. The scripts change the
host for real, so they refuse to run outside CI or a throwaway VM; run them
locally with `hack/e2e-local.sh`. How to run them, watch the guest and read
the logs is in [Run the E2E tests](docs/how-to/run-the-e2e-tests.md).

## License

MIT, see `LICENSE`.
