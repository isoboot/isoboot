# Install Debian

This page installs Debian 13 (trixie) on one machine with a preseed file.
The machine boots the Debian installer's netboot kernel and initrd, and the
installer reads its answers from isoboot. For network cards whose driver
needs non-free firmware, the BootConfig adds Debian's firmware archive to the
initrd.

The steps are those of [Provision a machine](provision-a-machine.md); this
page gives the Debian parts: the BootConfig, its kernel arguments, the
preseed file and the firmware for network cards that need it. You need
isoboot installed ([Install isoboot](install.md)) in the namespace
`isoboot-system`.

## 1. Apply the BootConfig

```bash
kubectl -n isoboot-system apply -f examples/debian-13.yaml
kubectl -n isoboot-system get bootartifacts,bootconfigs
```

[`examples/debian-13.yaml`](../../examples/debian-13.yaml) holds:

| Resource | What it is |
|---|---|
| BootArtifact `debian-13-kernel` | `https://deb.debian.org/debian/dists/trixie/main/installer-amd64/20250803+deb13u7/images/netboot/debian-installer/amd64/linux` |
| BootArtifact `debian-13-initrd` | `.../20250803+deb13u7/images/netboot/debian-installer/amd64/initrd.gz` |
| BootConfig `debian-13` | `spec.netboot` with the two artifacts, and the kernel arguments below |

### Use the dated installer directory

The URLs name the dated installer build `20250803+deb13u7` (Debian 13.7),
not `current`. The files under `current` change with every point release,
and a BootArtifact whose file changed no longer matches its hash: the next
download (a new node, a wiped `dataDir`) fails with `hash mismatch` and the
artifact stays in `Error`. A dated directory does not change. Take the
hashes from that directory's `images/SHA256SUMS`.

To move to a later point release, change the dated directory in both URLs,
the hashes, and the firmware archive's version (below) together.

### Kernel arguments

```
console=ttyS0,115200 auto=true priority=critical {{if .ProxyURL}}mirror/http/proxy={{.ProxyURL}} {{end}}preseed/url={{.ProvisionAutomationBaseURL}}/preseed.cfg
```

| Argument | What it does |
|---|---|
| `console=ttyS0,115200` | The console on the first serial port, which the E2E reads. Leave it out to follow the installer on the machine's screen. |
| `auto=true` | Delays the locale and keyboard questions until the preseed file has been read, so it can answer them. |
| `priority=critical` | Asks only critical questions; the preseed file answers the rest. |
| `mirror/http/proxy={{.ProxyURL}}` | The installer fetches packages through isoboot's squid cache. httpd fills in `http://<node>:3128`. The `if` drops the argument when there is no proxy. |
| `preseed/url={{.ProvisionAutomationBaseURL}}/preseed.cfg` | The preseed file: the file `preseed.cfg` of the Provision's ProvisionAutomation, served by httpd at `http://<node>:8080/dynamic/automation/<provision>/preseed.cfg`. |

`kernelArgs` must render to one line; the controller renders it with sample
values and sets the BootConfig to `Error` with `invalid kernelArgs: ...`
when it cannot.

## 2. Create the Machine, the settings and the host keys

Create the Machine `web-server-01` and the ConfigMaps `default-user` and
`web-server-01` and the Secret `web-server-01-host-keys` as in
[Provision a machine](provision-a-machine.md), steps
[2](provision-a-machine.md#2-create-the-machine) and
[4](provision-a-machine.md#4-create-the-settings-and-host-keys). The
preseed below reads their keys. A preseed command must be one logical
line, so it reads the host keys in base64 (the `_b64` keys) and decodes
them on the machine.

## 3. Create the preseed file and the Provision

Save this as `web-server-01-debian.yaml` and apply it with
`kubectl -n isoboot-system apply -f web-server-01-debian.yaml`.
The preseed is the one the E2E installs Debian with
([`test/e2e/provision/automation/preseed.cfg`](../../test/e2e/provision/automation/preseed.cfg)),
with the host name and user read from the ConfigMaps, the late command split
over lines, and without the serial console and machine-id the test VM
sets.

```yaml
apiVersion: isoboot.github.io/v1alpha1
kind: ProvisionAutomation
metadata:
  name: debian-preseed
spec:
  files:
    preseed.cfg: |
      d-i debian-installer/locale string en_US.UTF-8
      d-i keyboard-configuration/xkb-keymap select us
      d-i time/zone string America/Los_Angeles
      d-i clock-setup/utc boolean true
      d-i netcfg/choose_interface select auto
      d-i netcfg/get_hostname string {{ required .ConfigMaps "hostname" }}
      d-i netcfg/get_domain string local
      d-i mirror/country string manual
      d-i mirror/http/hostname string deb.debian.org
      d-i mirror/http/directory string /debian
      d-i passwd/root-login boolean false
      d-i passwd/user-fullname string Administrator
      d-i passwd/username string {{ required .ConfigMaps "default_user.username" }}
      d-i passwd/user-password-crypted password {{ required .ConfigMaps "default_user.password" }}
      d-i partman-auto/method string regular
      d-i partman-auto/disk string /dev/vda
      d-i partman-auto/choose_recipe select atomic
      d-i partman-partitioning/confirm_write_new_label boolean true
      d-i partman/choose_partition select finish
      d-i partman/confirm boolean true
      d-i partman/confirm_nooverwrite boolean true
      tasksel tasksel/first multiselect standard, ssh-server
      d-i pkgsel/include string openssh-server curl
      d-i grub-installer/only_debian boolean true
      d-i grub-installer/bootdev string /dev/vda
      d-i preseed/early_command string wget -q -O /dev/null --post-data="provisionName={{.ProvisionName}}&phase=InProgress" "{{.UpdatePhaseURL}}" || true
      d-i preseed/late_command string \
        in-target mkdir -p /home/{{ required .ConfigMaps "default_user.username" }}/.ssh && \
        echo '{{ required .ConfigMaps "default_user.ssh_public_key" }}' > /target/home/{{ required .ConfigMaps "default_user.username" }}/.ssh/authorized_keys && \
        in-target chmod 700 /home/{{ required .ConfigMaps "default_user.username" }}/.ssh && \
        chmod 600 /target/home/{{ required .ConfigMaps "default_user.username" }}/.ssh/authorized_keys && \
        in-target chown -R {{ required .ConfigMaps "default_user.username" }}:{{ required .ConfigMaps "default_user.username" }} /home/{{ required .ConfigMaps "default_user.username" }}/.ssh && \
        echo '{{ required .Secrets "ssh_host_ecdsa_key_b64" }}' | base64 -d > /target/etc/ssh/ssh_host_ecdsa_key && \
        chmod 600 /target/etc/ssh/ssh_host_ecdsa_key && \
        in-target ssh-keygen -y -f /etc/ssh/ssh_host_ecdsa_key > /target/etc/ssh/ssh_host_ecdsa_key.pub && \
        chmod 644 /target/etc/ssh/ssh_host_ecdsa_key.pub && \
        echo '{{ required .Secrets "ssh_host_ed25519_key_b64" }}' | base64 -d > /target/etc/ssh/ssh_host_ed25519_key && \
        chmod 600 /target/etc/ssh/ssh_host_ed25519_key && \
        in-target ssh-keygen -y -f /etc/ssh/ssh_host_ed25519_key > /target/etc/ssh/ssh_host_ed25519_key.pub && \
        chmod 644 /target/etc/ssh/ssh_host_ed25519_key.pub && \
        echo '{{ required .Secrets "ssh_host_rsa_key_b64" }}' | base64 -d > /target/etc/ssh/ssh_host_rsa_key && \
        chmod 600 /target/etc/ssh/ssh_host_rsa_key && \
        in-target ssh-keygen -y -f /etc/ssh/ssh_host_rsa_key > /target/etc/ssh/ssh_host_rsa_key.pub && \
        chmod 644 /target/etc/ssh/ssh_host_rsa_key.pub && \
        in-target sed -i 's/^#PasswordAuthentication yes/PasswordAuthentication no/' /etc/ssh/sshd_config && \
        in-target sed -i 's/^PasswordAuthentication yes/PasswordAuthentication no/' /etc/ssh/sshd_config && \
        echo '{{ required .ConfigMaps "hostname" }}' > /target/etc/hostname && \
        wget -q -O /dev/null --post-data="provisionName={{.ProvisionName}}&phase=Complete" "{{.UpdatePhaseURL}}"
      d-i finish-install/reboot_in_progress note
---
apiVersion: isoboot.github.io/v1alpha1
kind: Provision
metadata:
  name: web-server-01-install
spec:
  machineRef: web-server-01
  bootConfigRef: debian-13           # or debian-13-firmware, below
  provisionAutomationRef: debian-preseed
  configMaps:
  - default-user
  - web-server-01
  secrets:
  - web-server-01-host-keys
```

Notes on the file:

- `/dev/vda` is the E2E's virtio disk. Set the machine's install disk in
  `partman-auto/disk` and `grub-installer/bootdev`, for example `/dev/sda`
  or `/dev/nvme0n1`. The disk is erased.
- `mirror/http/hostname` and `mirror/http/directory` are fetched over plain
  HTTP, through squid when the kernel line sets `mirror/http/proxy`.
- `{{ required .ConfigMaps "key" }}` stops the render when the key is
  missing; the installer then gets a 500 instead of a file with an empty
  value. Keys with dots must be read with `index` or `required`.
- The E2E's file also writes a fixed `/etc/machine-id` and sends the
  installed system's console to the serial port.

## 4. Boot the machine

Power it on and watch the Provision:

```bash
kubectl -n isoboot-system get provisions -w
```

| Step | Phase |
|---|---|
| iPXE loads `/static/debian-13/kernel/linux` and `/static/debian-13/initrd/initrd.gz`. | `Pending` |
| The installer brings up the network, fetches `preseed.cfg` and runs `preseed/early_command`, which posts `InProgress`. | `InProgress` |
| The late command writes the SSH keys and host name, then posts `Complete`; the installer reboots. | `Complete` |
| The machine PXE-boots again; with no `Pending` Provision httpd answers 404 and the firmware boots the disk. | `Complete` |

`InProgress` is accepted only from `Pending`, and `Complete` only from
`InProgress` (otherwise 409). The early command ends in `|| true`, so a
refused call does not stop the installer. The late command is one chain of
`&&`: if a step before the last one fails, `Complete` is never sent and the
Provision stays `InProgress`.

Install files are served only while the Provision is `Pending` or
`InProgress`; afterwards `preseed.cfg` answers 404.

To install the machine again, delete the Provision and apply the file
again ([Reinstall a machine](provision-a-machine.md#reinstall-a-machine)).

## The mirror proxy stays in the installed system

`mirror/http/proxy` is also written into the installed system's apt
configuration. If the installed machine cannot reach squid later (it is on
the node, and serves only the PXE subnet), `apt` fails until the proxy is
removed. Remove it in the late command, or afterwards, if that is the case.

## Network cards that need firmware

Debian's netboot initrd has no non-free firmware. A network card whose
driver needs firmware, for example a Realtek RTL8168, does not come up in
the installer: the installer never gets a network and never fetches its preseed
file, so the install does not start and the Provision stays `Pending`.

For these machines, use the BootConfig with the firmware archive:

```bash
kubectl -n isoboot-system apply -f examples/debian-13.yaml -f examples/debian-13-firmware.yaml
```

[`examples/debian-13-firmware.yaml`](../../examples/debian-13-firmware.yaml)
adds:

| Resource | What it is |
|---|---|
| BootArtifact `debian-13-firmware` | `https://cdimage.debian.org/cdimage/firmware/trixie/13.7.0/firmware.cpio.gz`, the firmware bundle for the same point release |
| BootConfig `debian-13-firmware` | the same kernel, initrd and kernel arguments as `debian-13`, plus `firmwareRef: debian-13-firmware` |

With `firmwareRef` set, the controller writes the initrd followed by the
firmware archive into one file and serves it as the initrd
(`/static/debian-13-firmware/initrd/initrd.gz`). The kernel unpacks both
archives, so the installer finds the firmware and loads it for the card. The
combined file is written again when the initrd or the firmware archive is
newer than it.

Point the Provision's `bootConfigRef` at `debian-13-firmware`. The firmware
archive's version (`13.7.0`) goes with the installer build (13.7); change
them together.

### The test that proves it

The E2E boots a QEMU machine with an emulated RTL8168 card twice, with the
same preseed file:

| Row | BootConfig | Expected | Checked |
|---|---|---|---|
| `debian-13` | `debian-13` (no firmware) | the install never starts | iPXE loads the kernel and initrd; then, for 50 checks 10 seconds apart (about 8 minutes), the Provision stays exactly `Pending`, and the nginx access log has no request for `/dynamic/automation/<provision>/...`: without firmware the installer had no network |
| `debian-13-firmware` | `debian-13-firmware` | a complete install | `Complete`, then the installed system is checked over SSH, and the `r8169` driver shows up on the serial console |

The first row would pass trivially if the card worked without firmware, so
the pair shows both that the firmware is needed and that `firmwareRef`
delivers it. See [Run the E2E tests](run-the-e2e-tests.md).

If something does not work, see [Troubleshoot](troubleshoot.md).
