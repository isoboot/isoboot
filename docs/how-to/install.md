# Install isoboot

This guide installs isoboot with its Helm chart on one Kubernetes node that
sits on the network your machines PXE-boot from. At the end, every isoboot
pod is running and the node answers PXE requests. To install an operating
system on a machine after that, see
[Provision a machine](provision-a-machine.md).

The commands use the release name `isoboot` and the namespace
`isoboot-system`. With the release name `isoboot`, every object the chart
creates is named `isoboot-<component>` (for example
`deployment/isoboot-httpd`).

## What you need

- **A Kubernetes cluster**, version 1.25 or later (the chart's
  `kubeVersion`), with `kubectl` and Helm 3.8 or later (for OCI charts). A
  one-node k3s is enough; CI uses k3s v1.36.5 and Helm v3.22.0.
- **One node for isoboot.** Everything that touches the network or the disk
  runs on this node: you name it with `nodeName`. It can be x86-64 or arm64
  (the images are built for both). The machines you install must be x86-64:
  isoboot serves x86-64 iPXE only (BIOS and UEFI).
- **The PXE subnet.** The node must have an address on the machines'
  network, with a route for exactly that subnet:
  `ip -4 route show 192.168.1.0/24` (with your subnet) must print one.
  dnsmasq takes its interface from that route, and the node's first IPv4
  address on that interface is the address machines boot from.
- **A DHCP server on that subnet that is not isoboot.** It hands out the
  addresses. isoboot's dnsmasq only adds the boot options (proxyDHCP), so
  you do not change your DHCP server.
- **Free ports on the node.** isoboot's pods use the host network and listen
  on the ports below. Nothing else on the node may use them.

  | Port | Protocol | Component | Used for |
  |---|---|---|---|
  | 67 | UDP | dnsmasq | proxyDHCP (boot options) |
  | 69 | UDP | dnsmasq | TFTP (iPXE) |
  | 4011 | UDP | dnsmasq | PXE boot server |
  | 8080 (`nginx.port`) | TCP | nginx | boot files, iPXE scripts, install files, status calls |
  | 3128 (`squid.port`) | TCP | squid | package cache for installers |
  | 111 | TCP | nfsd | port mapper (Ubuntu installs) |
  | 2049 (`nfsd.port`) | TCP | nfsd | NFS and MOUNT (Ubuntu installs) |

  TCP 111 and 2049 are usually taken by `rpcbind` (pulled in by
  `nfs-common`) or a kernel NFS server. Stop them and keep them stopped:

  ```bash
  sudo systemctl disable --now rpcbind.socket rpcbind.service
  sudo systemctl mask rpcbind.socket rpcbind.service
  sudo systemctl disable --now nfs-server   # only if it is installed
  ```

  Check that the ports are free:

  ```bash
  sudo ss -Hulnp '( sport = :67 or sport = :69 or sport = :4011 )'
  sudo ss -Htlnp '( sport = :8080 or sport = :3128 or sport = :111 or sport = :2049 )'
  ```

  Both commands must print nothing. If the node runs a host firewall, allow
  these ports from the PXE subnet.
- **The data directory**, created on the node before you install and owned
  by UID 65532 (the controller and the containers that write there run as
  that user):

  ```bash
  sudo mkdir -p /data/isoboot
  sudo chown 65532:65532 /data/isoboot
  ```

  It holds every download, a boot directory per BootConfig, the squid cache
  (up to `squid.cacheSizeMB`, 8000 MB by default) and, for each Ubuntu
  BootConfig, a full unpacked copy of its ISO. Plan for each ISO twice (the
  ISO and its unpacked tree) plus the cache, and more while an ISO is
  replaced: the old file and tree stay until the new ones are complete.
  Nothing checks free space first.
- **Internet access** from the cluster's pods, to download boot files, and
  from the node, for squid.

## Install the released chart

Pick a version from the
[tags](https://github.com/isoboot/isoboot/tags). The chart version is the
git tag without its leading `v`: tag `v1.2.3` is chart version `1.2.3`. A
tag's chart is published only after its install test passes, so a tag
whose test failed has images but no chart. This guide describes the chart
on `main`; an older release may have other values, so read its
`CHANGELOG.md` and `values.yaml`.

`nodeName` and `dnsmasq.subnet` are required:

```bash
helm install isoboot oci://ghcr.io/isoboot/charts/isoboot --version <version> \
  --namespace isoboot-system --create-namespace \
  --set nodeName=pxe-node-1 \
  --set dnsmasq.subnet=192.168.1.0/24
```

Replace `<version>` with the release you picked, `pxe-node-1` with the node's
name as `kubectl get nodes` shows it, and `192.168.1.0/24` with the PXE
subnet. Take the files in `examples/` from the same release tag.

For more than a few values, keep them in a file and pass it with `-f`
instead of `--set`; you need the same file again to upgrade:

```yaml
# isoboot-values.yaml
nodeName: pxe-node-1
dnsmasq:
  subnet: 192.168.1.0/24
```

```bash
helm install isoboot oci://ghcr.io/isoboot/charts/isoboot --version <version> \
  --namespace isoboot-system --create-namespace -f isoboot-values.yaml
```

Helm installs the CRDs from the chart's `crds/` directory first, then the
components. After the install, a hook creates the BootArtifact
`isoboot-ipxe`, which downloads iPXE; dnsmasq starts once it is there.

Do not add `--wait` (or `--atomic`, which implies it) to the first
install. Helm then runs the hook only after every pod is ready, but dnsmasq
is not ready until the hook's iPXE is downloaded: the install waits until
it times out and fails. Check the pods yourself instead
([Check that it is healthy](#check-that-it-is-healthy)).

## Install from a checkout

In a checkout, `charts/isoboot/Chart.yaml` has the placeholder version and
appVersion `0.1.0`, and no images are published for it. The image tags
default to the appVersion, so package the chart with the version of a
published release, from that release's tag:

```bash
git checkout v<version>
helm package charts/isoboot --version <version> --app-version <version>
helm install isoboot ./isoboot-<version>.tgz \
  --namespace isoboot-system --create-namespace -f isoboot-values.yaml
```

To run images you built yourself, build all three with the same `IMG`. The
dnsmasq and squid targets append `-dnsmasq` and `-squid` to it, so with a
tag in `IMG` the suffix lands on the tag:

```bash
make docker-build docker-build-dnsmasq docker-build-squid \
  IMG=registry.example.com/isoboot:dev
# builds registry.example.com/isoboot:dev, :dev-dnsmasq and :dev-squid
```

Push them (or import them into the node's container runtime), then point
the chart at them in your values file:

```yaml
# isoboot-values.yaml
nodeName: pxe-node-1
image:
  repository: registry.example.com/isoboot
  tag: dev
dnsmasq:
  subnet: 192.168.1.0/24
  image:
    repository: registry.example.com/isoboot
    tag: dev-dnsmasq
squid:
  image:
    repository: registry.example.com/isoboot
    tag: dev-squid
```

The local E2E run ([Run the E2E tests](run-the-e2e-tests.md)) does it the
other way round: it tags its builds with the chart's default names
(`ghcr.io/isoboot/isoboot`, `-dnsmasq`, `-squid`) and a version, imports
them into k3s, and packages the chart with that version as appVersion, so
no image values are needed.

## Values

Every value, with its default, is in
[`charts/isoboot/values.yaml`](../../charts/isoboot/values.yaml). These are
the ones you set or may want to change.

Required:

| Value | What to set |
|---|---|
| `nodeName` | The node isoboot runs on. Every pod that uses the host network or `dataDir` is pinned to it. Without it the install fails with `nodeName is required`. |
| `dnsmasq.subnet` | The PXE subnet as an IPv4 CIDR, for example `192.168.1.0/24`. dnsmasq serves this subnet; nginx, squid and nfsd answer only it and the node itself (nfsd: unless `nfsd.allowedCIDRs` is set). Without it, or with anything that is not an IPv4 CIDR, the install fails. |

Optional:

| Value | Default | What it changes |
|---|---|---|
| `dataDir` | `/data/isoboot` | The data directory on the node. It must exist and be writable by UID 65532 before you install. |
| `nginx.port` | `8080` | The node port for boot files, install files and status calls. iPXE and the installers are told this port. Keep it at 1024 or above: nginx runs without root. |
| `squid.port` | `3128` | The node port of the package cache. Install files and kernel arguments get it as `{{.ProxyURL}}`. |
| `squid.blockedDestinationCIDRs` | `10.42.0.0/16`, `10.43.0.0/16` | Destinations squid refuses, so PXE clients cannot reach in-cluster services through it. The defaults are k3s's pod and service networks; on another distribution, set your cluster's. |
| `squid.cacheSizeMB` | `8000` | Disk cache size, under `<dataDir>/squid/cache`. |
| `squid.maxObjectSize` | `"1024 MB"` | Largest file squid caches. |
| `squid.cacheMemMB` | `0` | Squid's memory cache (`cache_mem`), in MB. `0` turns it off, and cached objects are served from the disk cache. Squid's own default is 256 MB; raise `squid.resources` with it. |
| `squid.log.access` | `false` | `true` writes squid's access log to `<dataDir>/squid/logs/access.log`. |
| `nfsd.enabled` | `true` | `false` removes nfsd. Ubuntu BootConfigs (ISO mode) then go to `Error`; Debian, AlmaLinux and Rocky do not use nfsd. With it off, TCP 111 and 2049 stay free. |
| `nfsd.port` | `2049` | The NFS and MOUNT port. The port mapper always listens on TCP 111: the installer's NFS client asks only there. |
| `nfsd.allowedCIDRs` | `[]` (the PXE subnet) | Client networks allowed to use NFS, MOUNT and the port mapper. A list you set replaces the PXE subnet, so include it if machines there install Ubuntu. The node's own address is always added (for the kubelet's probes). |
| `nfsd.logLevel` | `info` | `debug`, or `trace` to log every request. |
| `nfsd.goMemLimit` | `200MiB` | Go memory limit. Keep it about 50 MiB under `nfsd.resources.limits.memory`. |
| `dnsmasq.ipxe.url`, `dnsmasq.ipxe.sha512` | iPXE v2.0.0 | The iPXE release to download, and its hash. |
| `*.resources` | see `values.yaml` | CPU and memory of each component (`resources` is the controller's). |

`httpd.port` is the port of httpd's in-cluster Service; it does not touch
the node and does not clash with `nginx.port`.

## What gets deployed

| Deployment | What it does | Network |
|---|---|---|
| `isoboot-controller-manager` | Downloads and checks BootArtifacts, builds each BootConfig's boot directory, unpacks ISOs for nfsd, sets new Provisions to `Pending` | pod network |
| `isoboot-httpd` | Answers iPXE (`/conditional-boot`), serves install files (`/automation/...`) and takes status calls (`/status`), behind nginx | pod network, Service `isoboot-httpd` |
| `isoboot-nginx` | Serves `/static/` (boot files from `dataDir`) and passes `/dynamic/` to httpd | host, TCP 8080 |
| `isoboot-dnsmasq` | proxyDHCP and TFTP: hands out iPXE | host, UDP 67, 69, 4011 |
| `isoboot-squid` | Package cache for installers | host, TCP 3128 |
| `isoboot-nfsd` | Read-only NFSv3 export of each unpacked ISO (only with `nfsd.enabled`) | host, TCP 111, 2049 |

Also: the five CRDs (`bootartifacts`, `bootconfigs`, `machines`,
`provisionautomations`, `provisions` in `isoboot.github.io`), two
ServiceAccounts, a Role and RoleBinding each for the controller and httpd
(they see only the release namespace), the ConfigMaps for nginx and squid,
the metrics Service `isoboot-metrics-service` (HTTPS, port 8443) with its
ClusterRoles, and the BootArtifact `isoboot-ipxe`.

All isoboot resources (BootArtifacts, BootConfigs, Machines,
ProvisionAutomations, Provisions, and the ConfigMaps and Secrets they read)
go in the release namespace. The controller and httpd do not see any other
namespace.

## Check that it is healthy

1. Every pod is `Running` and ready (dnsmasq needs iPXE first, which can
   take a minute or two):

   ```bash
   kubectl -n isoboot-system get pods
   kubectl -n isoboot-system wait pod --all --for=condition=Ready --timeout=300s
   ```

   You get six pods (five with `nfsd.enabled=false`).

2. iPXE is downloaded:

   ```bash
   kubectl -n isoboot-system get bootartifact isoboot-ipxe
   ```

   `PHASE` must be `Ready`.

3. dnsmasq found the PXE subnet's interface and the node's address on it:

   ```bash
   kubectl -n isoboot-system logs deploy/isoboot-dnsmasq -c generate-boot-ipxe
   ```

   It prints `Detected interface=<interface> host_ip=<address>` and the
   generated `boot.ipxe`.

4. nginx serves the boot script and reaches httpd. Run this on the node,
   with the address from step 3:

   ```bash
   curl http://192.168.1.10:8080/static/boot.ipxe
   curl "http://192.168.1.10:8080/dynamic/conditional-boot?mac=02-00-00-00-00-01"
   ```

   The first prints a two-line iPXE script that chains
   `/dynamic/conditional-boot`. The second prints
   `no pending provision for MAC`: httpd answered, and there is nothing to
   install for that MAC. From an address outside the PXE subnet and the
   node, nginx answers `403 Forbidden`.

5. nfsd is serving (when enabled):

   ```bash
   kubectl -n isoboot-system logs deploy/isoboot-nfsd
   ```

   It logs `serving NFS and MOUNT` and `serving port mapper`.

If something is not right (more in [Troubleshoot](troubleshoot.md)):

| What you see | Cause and fix |
|---|---|
| Pods stay `ContainerCreating`; `kubectl describe pod` says the hostPath type check failed | `dataDir` does not exist on the node. Create it (see above). |
| An init container logs `cannot create .../nginx/static/boot` or `.../nfs`, or `isoboot-ipxe` is `Error` with `creating directory: ... permission denied` | `dataDir` is not writable by UID 65532. `sudo chown 65532:65532 /data/isoboot`. |
| `generate-boot-ipxe` logs `FAIL: no interface matches subnet` | The node has no route for exactly `dnsmasq.subnet`. Set the subnet as `ip -4 route` shows it on the node. |
| dnsmasq, nginx, squid or nfsd restart again and again, logging that the address is already in use | Something else on the node uses one of the ports above. Find it with `ss` and stop it. |
| `extract-ipxe` logs `timed out waiting for .../ipxeboot.tar.gz` | The BootArtifact `isoboot-ipxe` is not `Ready`; see its `MESSAGE` with `kubectl -n isoboot-system get bootartifact isoboot-ipxe -o yaml`. The pod restarts and waits again. |

## Upgrade

Helm installs the CRDs in `crds/` on the first install only: it never
upgrades or deletes them. Apply the new release's CRDs yourself first, then
upgrade the release:

```bash
helm pull oci://ghcr.io/isoboot/charts/isoboot --version <new-version> \
  --untar --untardir isoboot-<new-version>
kubectl apply -f isoboot-<new-version>/isoboot/crds/
helm upgrade isoboot oci://ghcr.io/isoboot/charts/isoboot --version <new-version> \
  --namespace isoboot-system -f isoboot-values.yaml
```

From a checkout, apply `charts/isoboot/crds/` of the new tag instead.

Read [`CHANGELOG.md`](../../CHANGELOG.md) before you upgrade: changes marked
**BREAKING** may need your resources or values changed.

The pods on the node (controller, dnsmasq, nginx, squid, nfsd) are stopped
before their replacement starts, so PXE boots, downloads and installs over
NFS stop for a short time. Upgrade when no machine is installing.

## Uninstall

Helm does not delete the CRDs, nor the hook-created BootArtifact
`isoboot-ipxe`, nor anything in `dataDir`. To remove everything:

1. Delete your isoboot resources while the controller still runs: it then
   removes each BootConfig's boot directory and unpacked ISO.

   ```bash
   kubectl -n isoboot-system delete provisions,provisionautomations,machines,bootconfigs --all
   kubectl -n isoboot-system delete bootartifacts --all
   ```

2. Uninstall the release and remove the namespace:

   ```bash
   helm uninstall isoboot --namespace isoboot-system
   kubectl delete namespace isoboot-system
   ```

3. Delete the CRDs. This deletes every isoboot resource left in any
   namespace.

   ```bash
   kubectl delete crd bootartifacts.isoboot.github.io bootconfigs.isoboot.github.io \
     machines.isoboot.github.io provisionautomations.isoboot.github.io \
     provisions.isoboot.github.io
   ```

4. On the node, remove the data directory (downloads, boot files, unpacked
   ISOs, squid cache):

   ```bash
   sudo rm -rf /data/isoboot
   ```

To keep the downloads for a later install, skip step 4: the controller
re-checks each file's hash and reuses it instead of downloading it again.
