# Run the E2E tests

The provision E2E installs a real operating system end to end, once per
row of a matrix. On one host it sets up k3s, installs the isoboot chart,
builds a "site" network with its own DHCP server, and PXE-boots a UEFI
QEMU/KVM guest. The guest installs, reboots by itself, boots its new disk,
and is checked over SSH.

The same scripts, in [`test/e2e/provision/`](../../test/e2e/provision/), run
in CI and on your machine. (`make test-e2e` is a different, smaller test:
the Kind E2E in `test/e2e/`, which does not install an OS.)

## What the matrix covers

The rows are listed once, in
[`test/e2e/provision/rows.json`](../../test/e2e/provision/rows.json):

| Row | Installs | BootConfig | NIC | Guest RAM | Expected |
|---|---|---|---|---|---|
| `alma-10.2` | AlmaLinux 10.2, kickstart | netboot | virtio | 8 GiB | complete |
| `rocky-10.2` | Rocky Linux 10.2, kickstart | netboot | virtio | 8 GiB | complete |
| `debian-13` | Debian 13.7, preseed | netboot, no firmware | RTL8168 | 8 GiB | **must not install** |
| `debian-13-firmware` | Debian 13.7, preseed | netboot with `firmwareRef` | RTL8168 | 8 GiB | complete |
| `ubuntu-24.04` | Ubuntu 24.04.5, autoinstall | iso, over NFS | virtio | 2 GiB | complete |
| `ubuntu-26.04` | Ubuntu 26.04.1, autoinstall | iso, over NFS | virtio | 2 GiB | complete |
| `ubuntu-26.10` | Ubuntu 26.10 beta, autoinstall | iso, over NFS | virtio | 2 GiB | complete |

Each row applies its manifests from [`examples/`](../../examples/) and its
install files from
[`test/e2e/provision/automation/`](../../test/e2e/provision/automation/),
so the examples and those files are what the E2E proves.

A row runs these phases in order:

| Phase | What it does | Fails when |
|---|---|---|
| `host-setup` | Installs packages and helm, opens `/dev/kvm`, creates `/data/isoboot`; for the RTL8168 rows, builds (or reuses) a QEMU with an emulated RTL8168 card. | no `/dev/kvm` |
| `k3s` | Installs a pinned single-node k3s and waits for its network and CoreDNS. | |
| `network` | Bridge `br-pxe` with the host at 192.168.101.1, and a separate DHCP server (dnsmasq in a container) at 192.168.101.2 that hands out .100 to .200 with no boot options. isoboot's proxyDHCP adds those. | |
| `images` | Locally: builds the three images and imports them into k3s. In CI: nothing, the images come from GHCR. | |
| `helm-install` | Stops the host's `rpcbind` if TCP 111 or 2049 is in use and checks that both are then free, installs the chart with `dnsmasq.subnet=192.168.101.0/24` and `squid.log.access=true`, waits for every pod, then restarts every Deployment once to prove an upgrade can replace each pod. | a port is taken, a pod does not become Ready |
| `apply-row` | Applies the row's examples, waits for the BootArtifacts and the BootConfig, makes test-only keys (login key, SSH host keys, password hash, machine ID), and creates the ConfigMap, Secret, Machine, ProvisionAutomation and Provision. | an artifact or BootConfig stays in `Error` (3 checks in a row), makes no progress for 10 minutes, or is not `Ready` after 120 minutes |
| `boot-install` | PXE-boots the guest from a blank 20 GiB disk and follows the install. | see below |
| `verify` | Boots the installed disk and checks it over SSH. | see below |
| `collect-logs` | Gathers every log into the row's log directory. Never fails. | |

`boot-install` checks, in order: a DHCP lease (within 10 minutes); iPXE
fetched the kernel and the initrd with HTTP 200 (within 5 minutes each);
the Provision reaches `InProgress` (within 25 minutes) and `Complete`
(within 45 minutes); the installer reboots by itself (within 15 minutes
after `Complete`; QEMU runs with `-no-reboot`, so it exits). The guest is
never powered off under a running installer. Ubuntu rows also check that:

- the guest kernel's own command line has `netboot=nfs` and
  `nfsroot=192.168.101.1:/<bootconfig>`, and no `url=`, `iso-url=` or
  `toram`;
- nfsd logged a mount of `/<bootconfig>` by a client on the site subnet;
- nginx served `vmlinuz` and `initrd`, and no `.iso`.

`verify` boots the disk and checks, over SSH within 8 minutes: key login;
that sshd offers no password login (it asks the server, so a client setting
cannot make this pass); the host name; the ecdsa, ed25519 and rsa host keys
are the injected ones; `ID` and `VERSION_ID` in `/etc/os-release`; the
injected machine ID; root on the virtio disk; and, for `debian-13-firmware`,
that the `r8169` driver drove the RTL8168.

The negative row, `debian-13`, boots the same RTL8168 guest without the
firmware. Its installer can have no network, so for 50 checks 10 seconds
apart the Provision must stay exactly `Pending`, and nginx must never have
served its install files. It skips `verify`.

Every row ends by failing if any container of an isoboot pod restarted, so
an OOM kill the controller recovers from is not missed.

## Run it in CI

The workflow is
[`.github/workflows/test-provision-e2e.yaml`](../../.github/workflows/test-provision-e2e.yaml).
It runs:

| Trigger | Runs |
|---|---|
| Add the label `e2e` to a pull request | yes |
| Push to, or reopen, a pull request that has the label `e2e` | yes; a new push cancels the run in progress |
| Add any other label | no |
| A pull request from a fork | no: its token cannot push the test images. A maintainer runs it with *Run workflow* (`workflow_dispatch`) on a branch of this repository. |
| *Run workflow* in the Actions tab (`gh workflow run test-provision-e2e.yaml --ref <branch>`) | yes |

The `build` job pushes the isoboot, dnsmasq and squid images and the chart
to GHCR as version `0.0.0-sha.<first 7 characters of the commit>`. Then one
job per row (the matrix is read from `rows.json`; one row failing does not
stop the others) runs each phase as a step with
`test/e2e/provision/run.sh <row> <phase>`, against those images
(`E2E_IMAGES=ghcr`). A row job may take up to 100 minutes.

When a step fails, the *Print logs* step prints the last 150 lines of every
log file into the job log. The logs are always uploaded as the artifact
`e2e-logs-<row>` (see [Read the logs](#read-the-logs)). The squid cache and
the RTL8168 QEMU build are kept in the Actions cache between runs.

## Run it locally

[`hack/e2e-local.sh`](../../hack/e2e-local.sh) runs the same rows in a
throwaway multipass VM:

```bash
hack/e2e-local.sh                                  # every row, in a new VM, deleted afterwards
hack/e2e-local.sh --row ubuntu-26.04               # one row
hack/e2e-local.sh --row debian-13 --row debian-13-firmware
hack/e2e-local.sh --row ubuntu-26.04 --keep        # keep the VM and the row's state
hack/e2e-local.sh --reuse --row debian-13-firmware # run again in the kept VM
```

### Host requirements

- An x86-64 Linux host with KVM, and nested virtualisation turned on
  (`/sys/module/kvm_intel/parameters/nested` or
  `/sys/module/kvm_amd/parameters/nested` is `Y` or `1`): the guest is a
  KVM machine inside the multipass VM.
- multipass and git.
- Internet access: the VM downloads packages, base images and every
  BootArtifact.

The script checks these first and stops with a message if one is missing.

### What it does

1. Creates the VM `isoboot-e2e-local` (Ubuntu 24.04), or with `--reuse`
   starts the existing one, and marks a VM it created as disposable
   (`/etc/isoboot-e2e-vm`).
2. Copies this checkout into the VM as `~/isoboot`: tracked and untracked
   files, uncommitted changes included, ignored files left out.
3. For each row, one after another, runs `test/e2e/provision/run.sh <row>`
   with `E2E_IMAGES=local`: it builds the images inside the VM, imports them
   into k3s, and installs the chart from the checkout. Each row cleans up
   before it starts, so a failed row does not break the next one.
4. Copies each row's logs out, prints `PASS` or `FAIL` per row, and exits
   non-zero if a row failed or no row ran.
5. Deletes the VM, unless `--keep` was given or the VM was not created by
   this run.

Downloaded BootArtifacts, the squid cache and the RTL8168 QEMU build stay in
the VM, so later rows, and later runs with `--reuse`, do not fetch or build
them again. Waits for downloads follow their progress, so a slow link makes
a row slower. The controller still cuts off any one download after 30
minutes and starts it again from the first byte, so on a link slower than
about 15 Mbit/s an Ubuntu ISO never finishes
([BootArtifact lifecycle](../reference/custom-resources.md#bootartifact-lifecycle)).

### Options

| Option | Default | What it does |
|---|---|---|
| `--row <id>` | every row | Run this row; repeat for more. |
| `--keep` | off | Keep the VM and the last row's state (k3s, isoboot, the guest) for debugging. |
| `--reuse` | off | Use an existing VM of that name, made earlier by this script. Without it the script refuses to touch an existing VM. A reused VM is never deleted. |
| `--vm-name <name>` | `isoboot-e2e-local` | The VM's name. |
| `--cpus <n>` | 4 | VM CPUs. |
| `--memory <size>` | 12G | VM memory. |
| `--disk <size>` | 60G | VM disk. |
| `--logs <dir>` | `<repo>/e2e-logs/<time>` | Where the logs are copied to. |

### VM sizing

One row runs at a time:

| Resource | Needed for |
|---|---|
| 12 GiB memory | the guest (8 GiB for the EL and Debian rows, 2 GiB for Ubuntu), k3s with isoboot (about 1.5 GiB), and the image builds (about 2 GiB, before the guest starts) |
| 4 CPUs | 2 for the guest, the rest for k3s, nginx and nfsd |
| 60 GiB disk | the kept downloads (both Ubuntu ISOs, about 6 GiB), one unpacked ISO tree (about 3 GiB), the guest's disk (up to 20 GiB, usually 4 to 6 GiB used), images and build cache (about 5 GiB), the squid cache (up to 8 GiB) and the RTL8168 QEMU build (about 2 GiB) |

### The host guard

The phase scripts change the host they run on for real: they install
packages and k3s, uninstall k3s, delete `/data/isoboot` (all but the squid
cache), add a bridge, a DHCP container and iptables rules, stop `rpcbind`
and open `/dev/kvm` to every user. So they refuse to run unless the host is
known to be disposable:

- a GitHub Actions runner (`GITHUB_ACTIONS=true`);
- a VM with the marker file `/etc/isoboot-e2e-vm`, which `hack/e2e-local.sh`
  writes into each VM it creates; or
- `E2E_ALLOW_THIS_HOST=1`, set by you, on a machine you are willing to throw
  away.

With `--reuse`, `hack/e2e-local.sh` also refuses a VM without the marker.
The scripts always use k3s's own kubeconfig
(`/etc/rancher/k3s/k3s.yaml`), never the cluster in your environment.

To run the phases directly on a throwaway x86-64 Ubuntu machine with KVM,
passwordless sudo and internet access:

```bash
E2E_ALLOW_THIS_HOST=1 test/e2e/provision/run.sh ubuntu-26.04
```

### Keep the state and re-run a phase

After `--keep`, the VM still runs k3s, isoboot and the last row's
resources. Inside it:

```bash
multipass shell isoboot-e2e-local
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
kubectl -n isoboot-system get bootconfigs,provisions
~/isoboot/test/e2e/provision/run.sh ubuntu-26.04 verify   # one phase
```

`run.sh <row> <phase>` runs that phase alone, as CI does; the phases are
`host-setup`, `k3s`, `network`, `images`, `helm-install`, `apply-row`,
`boot-install`, `verify` and `collect-logs`. `run.sh <row>` without a phase
cleans up first and runs everything.

To install the guest again, the Provision must be `Pending`; after a run it
is `Complete`, and phases only move forward. The guest from `verify` (or a
failed `boot-install`) is still running and holds the tap device and the
VNC port, so stop it first. Then delete the Provision, apply the row and
boot again:

```bash
sudo kill "$(sudo cat /tmp/isoboot-e2e/ubuntu-26.04/qemu.pid)"
sudo rm -f /tmp/isoboot-e2e/ubuntu-26.04/qemu.pid /tmp/isoboot-e2e/ubuntu-26.04/qemu-monitor.sock
kubectl -n isoboot-system delete provision qemu-vm1-provision
~/isoboot/test/e2e/provision/run.sh ubuntu-26.04 apply-row
~/isoboot/test/e2e/provision/run.sh ubuntu-26.04 boot-install
```

To change code and test it again, run `hack/e2e-local.sh --reuse --row <row>`
from the host: it copies the checkout in again and rebuilds the images.

### Environment variables

Set these when you run `run.sh` yourself (`hack/e2e-local.sh` sets
`E2E_IMAGES=local`, `E2E_KEEP_DOWNLOADS=1` and, with `--keep`, `E2E_KEEP=1`
for you):

| Variable | Default | What it does |
|---|---|---|
| `E2E_IMAGES` | `local` | `local` builds the images on the host; `ghcr` uses images and chart from GHCR (CI). |
| `E2E_VERSION` | `0.0.0-local` | Image tag and chart version; required with `ghcr`. |
| `E2E_KEEP` | unset | `1` leaves everything running after `run.sh <row>`. |
| `E2E_KEEP_DOWNLOADS` | unset | `1` keeps the downloaded artifacts during cleanup; the controller checks their hashes and reuses them. |
| `E2E_WORK_ROOT` | `/tmp/isoboot-e2e` | Scratch directory; a row uses `<root>/<row>/`, its logs `<root>/<row>/logs/`. |
| `E2E_QEMU_CACHE` | `~/qemu-cache` | Where the RTL8168 QEMU build is kept. |
| `E2E_NO_PROGRESS_MINUTES` | 10 | How long a wait for a BootArtifact or BootConfig may see no progress. |
| `E2E_WAIT_MAX_MINUTES` | 120 | How long such a wait may take in all. |
| `E2E_VNC_DISPLAY` | `:0` | The guest's VNC display (port 5900 + display number). |
| `E2E_ALLOW_THIS_HOST` | unset | See [the host guard](#the-host-guard). |

## Watch the guest

The guest has a 3840x2160 screen and no window. QEMU serves it over VNC on
`127.0.0.1:5900`, inside the VM (or host) that runs the row. From your
workstation, tunnel to it through the host that runs the VM:

```bash
multipass info isoboot-e2e-local        # on the host: the VM's IPv4 address
ssh -J you@host -L 5900:127.0.0.1:5900 ubuntu@<vm-ip>
```

then point a VNC viewer at `localhost:5900`. The VM's `ubuntu` user needs
your public key in `~/.ssh/authorized_keys`; multipass installs only its
own. `E2E_VNC_DISPLAY=:1` moves the server to port 5901; it takes effect
when you run `run.sh` inside the VM yourself, since `hack/e2e-local.sh` does
not pass it on.

Without VNC, the screenshots in the logs show the screen when a wait failed.

## Read the logs

Locally the logs are copied to `e2e-logs/<time>/<row>/` (inside the VM they
are in `/tmp/isoboot-e2e/<row>/logs/`); in CI download the artifact
`e2e-logs-<row>` from the run's summary page.

| File | What it holds |
|---|---|
| `run.log` | The whole output of every phase (local runs; in CI it is the job log). |
| `serial-install.log` | The guest's serial console during the install: the kernel command line, the installer's messages. |
| `serial-disk.log` | The serial console of the first boot from disk (`verify`). |
| `screen-final.png` | The guest's screen at the end. |
| `screen-<what>.png` | The screen when a wait failed: `screen-wait-InProgress.png`, `screen-wait-Complete.png`, `screen-no-reboot.png`, `screen-disk-boot.png`; `screen-stall.png` for the negative row. |
| `resources.yaml` | Every BootArtifact, BootConfig, Machine, Provision and ProvisionAutomation, with status. |
| `pod-<component>.log` | The logs of `controller`, `httpd`, `nginx`, `dnsmasq`, `squid` and `nfsd`. |
| `nginx-access.log` | Every HTTP request the guest made. |
| `squid-access.log` | Every package download through squid (`TCP_MISS` / `TCP_HIT`). |
| `nfsd.log` | nfsd's log (Ubuntu rows), with the guest's `portmap getport` and `mount`. |
| `site-dhcp.log` | The site DHCP server's log: the leases. |
| `pods.txt`, `pods-describe.txt`, `events.txt` | Pods (with restarts) and Kubernetes events. |
| `network.txt` | The bridge, its ports, the DHCP namespace and the iptables rules. |
| `data-dir.txt` | The files under `/data/isoboot` (without the squid cache). |

Where to start:

| Failed | Look at |
|---|---|
| `apply-row` | `resources.yaml` (the BootArtifact's or BootConfig's message), `pod-controller.log` |
| no DHCP lease | `site-dhcp.log`, `network.txt` |
| iPXE did not fetch the kernel | `pod-dnsmasq.log`, `nginx-access.log`, `pod-httpd.log` |
| not `InProgress` / not `Complete` | `serial-install.log`, the `screen-*.png`, `nginx-access.log` (were the install files and `/dynamic/status` requested?) |
| an Ubuntu NFS check | `serial-install.log` (`Command line:`), `nfsd.log` |
| `verify` | `serial-disk.log`, `screen-disk-boot.png` |
| a pod restarted | `pods.txt`, `pods-describe.txt` |

The symptoms are explained in [Troubleshoot](troubleshoot.md).

## Test the harness

Two checks run on every push, in the lint workflow, without KVM:

- [`test/e2e/provision/check-rows.sh`](../../test/e2e/provision/check-rows.sh)
  checks `rows.json` and what it refers to: field names and values; unique
  ids, MACs and host names; that every example and install file exists and
  defines the row's BootConfig and BootArtifacts; that an Ubuntu (ISO) row
  has `"nfs": true`, at most 2048 MB of RAM, and kernel arguments with
  `netboot=nfs nfsroot={{.NFSRoot}}` and without `url=`, `iso-url=` or
  `toram`; and that the negative row uses the RTL8168 card without firmware.
  It needs bash, jq and awk.
- [`test/e2e/provision/selftest.sh`](../../test/e2e/provision/selftest.sh)
  tests the harness itself (the host guard, the checks, the local runner)
  against stub commands. It needs bash 4 or later, jq, GNU coreutils and
  docker.

Run both after changing `rows.json` or the scripts.
