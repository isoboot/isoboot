# Install Rocky Linux or AlmaLinux

This page installs Rocky Linux 10.2 or AlmaLinux 10.2 on one machine with a
kickstart file. Both boot the netboot kernel and initrd from the release's
BaseOS `pxeboot` directory and install packages from the release's BaseOS
repository. The two releases differ only in their URLs; everything below
works for both.

The steps are those of [Provision a machine](provision-a-machine.md); this
page gives the Rocky and AlmaLinux parts: the BootConfig, its kernel
arguments and the kickstart. You need isoboot installed
([Install isoboot](install.md)) in the namespace `isoboot-system`.

## 1. Apply the BootConfig

```bash
kubectl -n isoboot-system apply -f examples/rocky-10.2.yaml   # or examples/alma-10.2.yaml
kubectl -n isoboot-system get bootartifacts,bootconfigs
```

[`examples/rocky-10.2.yaml`](../../examples/rocky-10.2.yaml) holds two
BootArtifacts and one BootConfig:

| Resource | What it is |
|---|---|
| BootArtifact `rocky-10.2-kernel` | `https://download.rockylinux.org/pub/rocky/10.2/BaseOS/x86_64/os/images/pxeboot/vmlinuz` with its sha256 |
| BootArtifact `rocky-10.2-initrd` | `.../images/pxeboot/initrd.img` with its sha256 |
| BootConfig `rocky-10.2` | `spec.netboot.kernelRef` and `initrdRef` name the two artifacts; `kernelArgs` below |

The controller downloads each file, checks its hash and sets the artifact to
`Ready`. When both are `Ready` the BootConfig becomes `Ready` and nginx
serves `/static/rocky-10.2/kernel/vmlinuz` and
`/static/rocky-10.2/initrd/initrd.img`. A machine is sent to the installer
only while its BootConfig is `Ready`.

The kernel arguments:

```
ip=dhcp {{if .ProxyURL}}inst.proxy={{.ProxyURL}} {{end}}inst.repo=http://download.rockylinux.org/pub/rocky/10.2/BaseOS/x86_64/os inst.ks={{.ProvisionAutomationBaseURL}}/ks.cfg
```

| Argument | What it does |
|---|---|
| `ip=dhcp` | The installer's initrd brings up the network with DHCP. |
| `inst.proxy={{.ProxyURL}}` | Anaconda fetches the repository through isoboot's squid cache. httpd fills in `http://<node>:3128` (the node address the machine used, and `squid.port`). The `if` drops the argument when there is no proxy. |
| `inst.repo=http://...` | The installation source: the same release's BaseOS repository. It is `http://`, not `https://`, so that squid can cache the packages; squid only tunnels HTTPS. |
| `inst.ks={{.ProvisionAutomationBaseURL}}/ks.cfg` | The kickstart file: the file `ks.cfg` of the Provision's ProvisionAutomation, served by httpd at `http://<node>:8080/dynamic/automation/<provision>/ks.cfg`. |

Keep the kernel, the initrd and `inst.repo` on the same release: anaconda's
initrd and the installer image it loads from the repository must match.

AlmaLinux uses `https://repo.almalinux.org/almalinux/10.2/BaseOS/x86_64/os/images/pxeboot/`
for the artifacts and `inst.repo=http://repo.almalinux.org/almalinux/10.2/BaseOS/x86_64/os`
(see [`examples/alma-10.2.yaml`](../../examples/alma-10.2.yaml)).

BootArtifact URLs must start with `https://`. `kernelArgs` must render to
one line; the controller renders it with sample values and sets the
BootConfig to `Error` with `invalid kernelArgs: ...` when it cannot.

## 2. Create the Machine, the settings and the host keys

Create the Machine `web-server-01` and the ConfigMaps `default-user` and
`web-server-01` and the Secret `web-server-01-host-keys` as in
[Provision a machine](provision-a-machine.md), steps
[2](provision-a-machine.md#2-create-the-machine) and
[4](provision-a-machine.md#4-create-the-settings-and-host-keys). The
kickstart below reads their keys. `machine_id` and the host keys are
optional here: the kickstart writes each one only if it is there, and the
installed system makes its own otherwise.

## 3. Create the kickstart and the Provision

Save this as `web-server-01-rocky.yaml` and apply it with
`kubectl -n isoboot-system apply -f web-server-01-rocky.yaml`.
The kickstart is the one the E2E installs Rocky and AlmaLinux with
([`test/e2e/provision/automation/kickstart.cfg`](../../test/e2e/provision/automation/kickstart.cfg)),
with the host name read from the ConfigMap and without the serial console
the test VM needs.

```yaml
apiVersion: isoboot.github.io/v1alpha1
kind: ProvisionAutomation
metadata:
  name: rocky-and-alma-kickstart
spec:
  files:
    ks.cfg: |
      lang en_US.UTF-8
      keyboard us
      timezone America/Los_Angeles --utc
      autopart --type=plain
      clearpart --all --initlabel
      zerombr
      bootloader --location=mbr
      user --name={{ required .ConfigMaps "default_user.username" }} --groups=wheel --iscrypted --password={{ required .ConfigMaps "default_user.password" }}
      {{- if index .ConfigMaps "default_user.ssh_public_key" }}
      sshkey --username={{ index .ConfigMaps "default_user.username" }} "{{ index .ConfigMaps "default_user.ssh_public_key" }}"
      {{- end }}
      network --bootproto=dhcp --onboot=yes --hostname={{ required .ConfigMaps "hostname" }}
      eula --agreed
      reboot

      %packages
      @core
      %end

      %pre --log=/var/log/kickstart-pre.log
      curl -X POST -d "provisionName={{.ProvisionName}}&phase=InProgress" {{.UpdatePhaseURL}}
      %end

      %post --log=/var/log/kickstart-post.log
      {{- if index .Secrets "ssh_host_ecdsa_key" }}
      printf '%s' '{{ index .Secrets "ssh_host_ecdsa_key" }}' > /etc/ssh/ssh_host_ecdsa_key
      chmod 600 /etc/ssh/ssh_host_ecdsa_key
      restorecon /etc/ssh/ssh_host_ecdsa_key
      ssh-keygen -y -f /etc/ssh/ssh_host_ecdsa_key > /etc/ssh/ssh_host_ecdsa_key.pub
      chmod 644 /etc/ssh/ssh_host_ecdsa_key.pub
      restorecon /etc/ssh/ssh_host_ecdsa_key.pub
      {{- end }}
      {{- if index .Secrets "ssh_host_ed25519_key" }}
      printf '%s' '{{ index .Secrets "ssh_host_ed25519_key" }}' > /etc/ssh/ssh_host_ed25519_key
      chmod 600 /etc/ssh/ssh_host_ed25519_key
      restorecon /etc/ssh/ssh_host_ed25519_key
      ssh-keygen -y -f /etc/ssh/ssh_host_ed25519_key > /etc/ssh/ssh_host_ed25519_key.pub
      chmod 644 /etc/ssh/ssh_host_ed25519_key.pub
      restorecon /etc/ssh/ssh_host_ed25519_key.pub
      {{- end }}
      {{- if index .Secrets "ssh_host_rsa_key" }}
      printf '%s' '{{ index .Secrets "ssh_host_rsa_key" }}' > /etc/ssh/ssh_host_rsa_key
      chmod 600 /etc/ssh/ssh_host_rsa_key
      restorecon /etc/ssh/ssh_host_rsa_key
      ssh-keygen -y -f /etc/ssh/ssh_host_rsa_key > /etc/ssh/ssh_host_rsa_key.pub
      chmod 644 /etc/ssh/ssh_host_rsa_key.pub
      restorecon /etc/ssh/ssh_host_rsa_key.pub
      {{- end }}
      sed -i 's/^#\?PasswordAuthentication.*/PasswordAuthentication no/' /etc/ssh/sshd_config
      sed -i '/\/boot\/efi/s/defaults/defaults,nofail/' /etc/fstab
      {{- if index .ConfigMaps "machine_id" }}
      printf '%s\n' '{{ index .ConfigMaps "machine_id" }}' > /etc/machine-id
      chmod 444 /etc/machine-id
      restorecon /etc/machine-id
      {{- end }}
      # SELinux stays enforcing. Relabel the whole system on first boot, and
      # relabel /etc early on every boot and resolv.conf whenever it changes.
      touch /.autorelabel
      printf '%s\n' '[Unit]' 'Description=restorecon /etc (SELinux label-race workaround)' 'DefaultDependencies=no' 'After=local-fs.target' 'Before=sysinit.target' '[Service]' 'Type=oneshot' 'ExecStart=/sbin/restorecon -R /etc' 'RemainAfterExit=yes' '[Install]' 'WantedBy=sysinit.target' > /etc/systemd/system/fix-etc-labels.service
      systemctl enable fix-etc-labels.service
      printf '%s\n' '[Unit]' 'DefaultDependencies=no' 'Before=sysinit.target' '[Path]' 'PathModified=/etc/resolv.conf' 'Unit=fix-resolv.service' '[Install]' 'WantedBy=sysinit.target' > /etc/systemd/system/fix-resolv.path
      printf '%s\n' '[Unit]' 'Description=restorecon resolv.conf' '[Service]' 'Type=oneshot' 'ExecStart=/sbin/restorecon /etc/resolv.conf' > /etc/systemd/system/fix-resolv.service
      systemctl enable fix-resolv.path
      curl -X POST -d "provisionName={{.ProvisionName}}&phase=Complete" {{.UpdatePhaseURL}}
      %end
---
apiVersion: isoboot.github.io/v1alpha1
kind: Provision
metadata:
  name: web-server-01-install
spec:
  machineRef: web-server-01
  bootConfigRef: rocky-10.2          # or alma-10.2
  provisionAutomationRef: rocky-and-alma-kickstart
  configMaps:
  - default-user
  - web-server-01
  secrets:
  - web-server-01-host-keys
```

`clearpart --all` and `zerombr` erase **every** disk the installer sees. Add
`ignoredisk --only-use=<disk>` to keep other disks.

The file is a Go template. `{{ required .ConfigMaps "key" }}` stops the
render when the key is missing, and the installer gets a 500 instead of a
kickstart with an empty user name. `{{ index .ConfigMaps "key" }}` gives an
empty string for a missing key, which the `{{- if }}` blocks use for the
optional values ([reading a key](../reference/templates.md#reading-a-key-and-what-a-missing-key-does)).

The controller sets the new Provision to `Pending` within a second or so:

```bash
kubectl -n isoboot-system get provisions
```

## 4. Boot the machine

Power it on. What happens, and where you see it:

| Step | Where you see it |
|---|---|
| The site DHCP server gives an address; isoboot's dnsmasq adds the PXE options and serves iPXE over TFTP. | `kubectl -n isoboot-system logs deploy/isoboot-dnsmasq` |
| iPXE asks `/dynamic/conditional-boot?mac=...`; httpd finds the `Pending` Provision and answers a script that loads the kernel and initrd. | httpd log `conditional-boot request`, nginx access log |
| Anaconda fetches `ks.cfg`. | nginx access log: `GET /dynamic/automation/web-server-01-install/ks.cfg` |
| `%pre` posts `InProgress`. | `kubectl -n isoboot-system get provisions` shows `InProgress` |
| Packages come from `inst.repo` through squid. | squid access log (with `squid.log.access=true`) |
| `%post` writes the keys and posts `Complete`; the installer reboots. | phase `Complete` |
| The machine PXE-boots again. There is no `Pending` Provision now, so httpd answers 404 and logs `no pending provision`; iPXE exits and the firmware boots the disk. | httpd log |

Watch the phase with `kubectl -n isoboot-system get provisions -w`. The E2E
installs both releases in a VM with 8 GiB of RAM.

## Status callbacks

`%pre` posts `phase=InProgress` and `%post` posts `phase=Complete` to
`{{.UpdatePhaseURL}}`. `Complete` is accepted only from `InProgress`, so
`Complete` without an earlier `InProgress` is refused with 409; every
answer is in [the status callback](../reference/templates.md#the-status-callback).
The kickstart's `curl` calls do not use `-f`, so a refused call does not
stop the install.

`%pre` runs before the disks are touched, `%post` after the packages are
installed: a Provision left in `InProgress` means the install started but
did not reach the end of `%post`.

Install files are served only while the Provision is `Pending` or
`InProgress`. Once it is `Complete`, `ks.cfg` answers 404, so the password
hash and host keys in it can no longer be read.

## SELinux

SELinux stays enforcing. The `%post` section labels everything it writes
(`restorecon`) and adds three safeguards that the E2E needed:

- `touch /.autorelabel` relabels the whole system on the first boot. In CI,
  anaconda's own relabel was sometimes incomplete under load; a mislabelled
  file such as `/etc/ld.so.cache` dropped the first boot into emergency
  mode, with no SSH.
- `fix-etc-labels.service` runs `restorecon -R /etc` early on every boot.
- `fix-resolv.path` relabels `/etc/resolv.conf` each time it is written
  (NetworkManager rewrites it while the network comes up, after the early
  relabel has run).

Do not switch SELinux to permissive to get past such a boot: relabel instead.

The `nofail` added to the `/boot/efi` line of `/etc/fstab` is also from the
E2E, where AlmaLinux needed it.

## Install the machine again

Delete the Provision and create it again
([Reinstall a machine](provision-a-machine.md#reinstall-a-machine)):

```bash
kubectl -n isoboot-system delete provision web-server-01-install
kubectl -n isoboot-system apply -f web-server-01-rocky.yaml
```

## Use a serial console

The E2E installs a VM whose console is its first serial port. To see the
installed system's console there too, append it to the kernel command line
in the kickstart:

```
bootloader --location=mbr --append="console=ttyS0,115200"
```

If something does not work, see [Troubleshoot](troubleshoot.md).
