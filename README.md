# isoboot

A Kubernetes controller that manages PXE boot artifacts.

## Development

### Scaffold

Project was scaffolded with kubebuilder using a flat API group:

```bash
kubebuilder init --domain isoboot.github.io --repo github.com/isoboot/isoboot
# APIs created without --group so apiVersion is isoboot.github.io/v1alpha1
kubebuilder create api --version v1alpha1 --kind BootArtifact --controller --resource
kubebuilder create api --version v1alpha1 --kind BootConfig --controller --resource
```

### CRDs

**BootArtifact** — a single downloadable file (kernel, initrd, or firmware) with URL and hash verification.

**BootConfig** — groups BootArtifacts into a servable PXE boot directory. If firmware is present, creates `no-firmware/` and `with-firmware/` subdirectories.

In `iso` mode (Ubuntu live-server) the controller unpacks the whole ISO into `<dataDir>/nfs/<bootconfig>/`, the `nfsd` component exports it read-only over NFSv3, and only the kernel and initrd go over HTTP. Kernel arguments use `netboot=nfs nfsroot={{.NFSRoot}}`, so the installer never downloads the ISO into RAM (see `examples/ubuntu-26.04.yaml`). nfsd runs on the host network and needs TCP 111 and 2049 free on the node (no `rpcbind` or kernel NFS server).

### Provision E2E

`test/e2e/provision/` installs a real OS end to end: k3s, the chart, a site DHCP server on a bridge, and a UEFI QEMU/KVM guest that PXE-boots, installs, reboots from disk and is checked over SSH. The rows (AlmaLinux 10.2, Rocky 10.2, Debian 13 with and without firmware, Ubuntu 26.04.1 and 26.10 over NFS at 2 GiB) are listed once in `test/e2e/provision/rows.json`; the installer files are in `test/e2e/provision/automation/`. CI (`.github/workflows/test-provision-e2e.yaml`, on PRs labelled `e2e`) runs one phase script per step.

To run the same scripts locally on an x86-64 Linux host with KVM, nested virtualisation and multipass:

```bash
hack/e2e-local.sh                       # all rows, one after another, in a new VM
hack/e2e-local.sh --row ubuntu-26.04 --keep
hack/e2e-local.sh --reuse --row debian-13-firmware
```

It creates the multipass VM `isoboot-e2e-local` (4 CPUs, 12G, 60G), copies the checkout in (uncommitted changes included), builds the images inside it, and prints where the logs are (`e2e-logs/<time>/<row>/`). See the script header for all options.
