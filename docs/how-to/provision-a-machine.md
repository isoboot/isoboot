# Provision a machine

This guide installs Ubuntu 26.04 LTS on one machine, `web-server-01`, from
start to finish: the boot files, the machine, the install files, the
install itself, and what to do afterwards. The other releases work the same
way and differ only in steps 1 and 3; their pages are listed under
[Other releases](#other-releases).

You need isoboot installed and healthy ([Install isoboot](install.md)), in
the namespace `isoboot-system`. Every resource below goes in that
namespace: the controller and httpd see no other.

The machine must:

- be x86-64, with network boot (PXE) enabled, BIOS or UEFI;
- have network boot **before** its local disk in the boot order, or a
  one-time boot menu you can pick the network from. After the install,
  isoboot sends it on to its disk;
- be on the PXE subnet;
- have at least 2 GiB of RAM for Ubuntu (CI installs Ubuntu with 2 GiB, and
  AlmaLinux, Rocky and Debian with 8 GiB).

The install erases the disk it installs on.

## The resources

| Resource | Name in this guide | What it is |
|---|---|---|
| BootArtifact | `ubuntu-26.04-iso` | One file to download, with its hash. Here, the Ubuntu ISO. |
| BootConfig | `ubuntu-26.04` | What a machine boots: kernel, initrd and kernel arguments. Here, built from the ISO, which nfsd exports. |
| Machine | `web-server-01` | One machine, by its MAC address. |
| ProvisionAutomation | `ubuntu-autoinstall` | The install files, as templates. One can serve many machines. |
| ConfigMap, Secret | `default-user`, `web-server-01`, `web-server-01-host-keys` | The values the templates fill in: the login user (shared by all machines), this machine's host name and machine ID, and its SSH host keys. |
| Provision | `web-server-01-install` | One install: this machine, with this BootConfig and these install files. Its phase tracks the install. |

Every field is described in [Custom resources](../reference/custom-resources.md).

## 1. Apply the boot files and wait until they are Ready

Take the examples from the same release tag as your chart. They have no
namespace, so give it with `-n`:

```bash
kubectl -n isoboot-system apply -f examples/ubuntu-26.04.yaml
```

The controller downloads the ISO, checks its sha256, unpacks it to
`<dataDir>/nfs/ubuntu-26.04/` and copies its kernel and initrd to the boot
directory. Wait for both resources:

```bash
kubectl -n isoboot-system wait bootartifact/ubuntu-26.04-iso \
  --for=jsonpath='{.status.phase}'=Ready --timeout=60m
kubectl -n isoboot-system wait bootconfig/ubuntu-26.04 \
  --for=jsonpath='{.status.phase}'=Ready --timeout=30m
```

To follow them, or to see why one is not Ready:

```bash
kubectl -n isoboot-system get bootartifacts,bootconfigs
kubectl -n isoboot-system get bootconfig ubuntu-26.04 -o jsonpath='{.status.message}{"\n"}'
```

| Phase and message | What it means |
|---|---|
| BootArtifact `Downloading` | The download is running. The controller downloads one file at a time. |
| BootArtifact `Error`, `hash mismatch: expected ... got ...` | The file is not the one the hash names. Fix `url` or the hash. |
| BootArtifact `Error`, `download failed: ...` | The controller tries again after 10 s, then twice as long each time, up to 320 s, until the cause is fixed ([BootArtifact lifecycle](../reference/custom-resources.md#bootartifact-lifecycle)); a corrected URL or hash is tried at once. A download must finish within 30 minutes, so a multi-GB ISO needs a fast enough link. |
| BootConfig `Pending`, `waiting for iso artifact "ubuntu-26.04-iso" to be Ready` | Normal while the ISO downloads. |
| BootConfig `Error`, `extracting iso tree: ...` | Unpacking failed (for example, a full disk). Retried after 10 s, doubling up to 30 minutes. |
| BootConfig `Error`, `iso mode needs the controller's --nfs-dir` | The chart was installed with `nfsd.enabled=false`. Ubuntu needs nfsd. |
| BootConfig `Error`, `invalid kernelArgs: ...` | The `kernelArgs` template does not render, or renders to more than one line. |
| BootConfig `Ready` | Machines can boot it. |

A machine that PXE-boots before its BootConfig is `Ready` is not sent to
the installer: it boots its local disk, and httpd logs why.

## 2. Create the Machine

```yaml
# web-server-01-machine.yaml
apiVersion: isoboot.github.io/v1alpha1
kind: Machine
metadata:
  name: web-server-01
  namespace: isoboot-system
spec:
  mac: 52-54-00-12-34-56   # the machine's MAC address, with hyphens
```

```bash
kubectl apply -f web-server-01-machine.yaml
```

Write the MAC with hyphens, not colons: `52-54-00-12-34-56`. Upper or lower
case does not matter. It must be the MAC iPXE asks with: the one of its
first network interface (`net0`), which is normally the one the machine
boots from. On a machine with several network interfaces, the httpd log
shows the MAC iPXE used (step 6).

Each MAC must belong to one Machine only. With two, httpd answers
`409 Conflict` and the machine boots its disk.

## 3. Write the install files

A ProvisionAutomation holds the installer's files as Go templates. Ubuntu's
BootConfig points the installer at
`{{.ProvisionAutomationBaseURL}}/` (`ds=nocloud;s=...` in its kernel
arguments), where it fetches `user-data` and `meta-data`. So those are the
two file names here.

This is the smallest answer file that installs Ubuntu unattended, plus the
squid proxy and the two status calls isoboot needs. What each entry does is
in [Install Ubuntu Server](ubuntu.md#the-answer-file).

```yaml
# ubuntu-autoinstall.yaml
apiVersion: isoboot.github.io/v1alpha1
kind: ProvisionAutomation
metadata:
  name: ubuntu-autoinstall
  namespace: isoboot-system
spec:
  files:
    user-data: |
      #cloud-config
      autoinstall:
        version: 1
        # No network: section. The installer runs from an NFS root over the
        # boot NIC; reconfiguring that NIC can freeze the install.
        {{if .ProxyURL}}proxy: {{.ProxyURL}}{{end}}
        identity:
          hostname: {{ required .ConfigMaps "hostname" }}
          username: {{ required .ConfigMaps "default_user.username" }}
          password: "{{ required .ConfigMaps "default_user.password" }}"
        ssh:
          install-server: true
          allow-pw: false
          authorized-keys:
            - "{{ required .ConfigMaps "default_user.ssh_public_key" }}"
        storage:
          layout:
            name: direct
        early-commands:
          - curl -X POST -d 'provisionName={{.ProvisionName}}&phase=InProgress' {{.UpdatePhaseURL}}
        late-commands:
          - curl -X POST -d 'provisionName={{.ProvisionName}}&phase=Complete' {{.UpdatePhaseURL}}
    meta-data: |
      instance-id: {{ required .ConfigMaps "hostname" }}
```

```bash
kubectl apply -f ubuntu-autoinstall.yaml
```

The templates read the values with `{{ required .ConfigMaps "key" }}`: a
missing key fails the render (the installer gets 500), so a misspelt key
cannot install an empty user name. Every variable and function is in
[Templates](../reference/templates.md).

The installer must report its progress itself, with two calls to
`{{.UpdatePhaseURL}}`: `phase=InProgress` when it starts and
`phase=Complete` when it is done. The Provision moves only forward,
`Pending` to `InProgress` to `Complete`; a call out of that order gets
`409 Conflict` and changes nothing
([status callback](../reference/templates.md#the-status-callback)). The
file calls `curl` without `-f`, so a refused call does not stop the install.

## 4. Create the settings and host keys

The install files of every page in this guide read the same keys, the ones
the [E2E tests](run-the-e2e-tests.md) use:

| Object | Key | Value |
|---|---|---|
| ConfigMap `default-user` (shared by all machines) | `default_user.username` | The login user. |
| | `default_user.password` | The user's password as a SHA-512 crypt hash, not the password. |
| | `default_user.ssh_public_key` | The public key that may log in over SSH. |
| ConfigMap `web-server-01` (this machine) | `hostname` | The host name. |
| | `machine_id` | `/etc/machine-id` of the installed system (32 hex digits). |
| Secret `web-server-01-host-keys` (this machine) | `ssh_host_ecdsa_key`, `ssh_host_ed25519_key`, `ssh_host_rsa_key` | The SSH host keys, as files. |
| | `ssh_host_ecdsa_key_b64`, `ssh_host_ed25519_key_b64`, `ssh_host_rsa_key_b64` | The same keys in base64 on one line, for install files that cannot hold a multi-line value. |

The shared ConfigMap. `openssl passwd -6` asks for the password and prints
its hash:

```bash
password_hash=$(openssl passwd -6) &&
  kubectl -n isoboot-system create configmap default-user \
    --from-literal=default_user.username=webadmin \
    --from-literal="default_user.password=$password_hash" \
    --from-literal="default_user.ssh_public_key=$(cat ~/.ssh/id_ed25519.pub)"
```

`-6` needs OpenSSL 1.1.1 or later. The `openssl` that comes with macOS
(LibreSSL) does not have it: it prints its usage, and the `&&` stops the
ConfigMap from being created with an empty hash. Use a Linux machine, or
OpenSSL from Homebrew. The hash sits in a ConfigMap, as in the E2E; to keep
it out of ConfigMaps, put it in a Secret and read it with `.Secrets`
instead.

This machine's ConfigMap:

```bash
kubectl -n isoboot-system create configmap web-server-01 \
  --from-literal=hostname=web-server-01 \
  --from-literal=machine_id="$(openssl rand -hex 16)"
```

Its SSH host keys, made beforehand so that you know their fingerprints
before the machine is installed:

```bash
mkdir web-server-01-keys
ssh-keygen -q -t ecdsa -N "" -f web-server-01-keys/ssh_host_ecdsa_key
ssh-keygen -q -t ed25519 -N "" -f web-server-01-keys/ssh_host_ed25519_key
ssh-keygen -q -t rsa -b 4096 -N "" -f web-server-01-keys/ssh_host_rsa_key
kubectl -n isoboot-system create secret generic web-server-01-host-keys \
  --from-file=web-server-01-keys/ssh_host_ecdsa_key \
  --from-file=web-server-01-keys/ssh_host_ed25519_key \
  --from-file=web-server-01-keys/ssh_host_rsa_key \
  --from-literal="ssh_host_ecdsa_key_b64=$(base64 < web-server-01-keys/ssh_host_ecdsa_key | tr -d '\n')" \
  --from-literal="ssh_host_ed25519_key_b64=$(base64 < web-server-01-keys/ssh_host_ed25519_key | tr -d '\n')" \
  --from-literal="ssh_host_rsa_key_b64=$(base64 < web-server-01-keys/ssh_host_rsa_key | tr -d '\n')"
```

The answer file of step 3 does not read `machine_id` or the host keys; the
installed system makes its own. The kickstart and preseed files, and the
Ubuntu file with host keys, read them. Create them anyway: a ConfigMap or
Secret that the Provision lists must exist, or every install file answers
404.

httpd reads these when the installer fetches a file, so you can change them
until then. When a Provision lists several ConfigMaps (or Secrets), their
keys are merged in the order listed, and a later one wins. That is what
lets `default-user` be shared and `web-server-01` hold the per-machine
values.

## 5. Create the Provision

```yaml
# web-server-01-install.yaml
apiVersion: isoboot.github.io/v1alpha1
kind: Provision
metadata:
  name: web-server-01-install
  namespace: isoboot-system
spec:
  machineRef: web-server-01
  bootConfigRef: ubuntu-26.04
  provisionAutomationRef: ubuntu-autoinstall
  configMaps:
  - default-user
  - web-server-01
  secrets:
  - web-server-01-host-keys
```

```bash
kubectl apply -f web-server-01-install.yaml
kubectl -n isoboot-system get provision web-server-01-install
```

A new Provision has no phase for a moment; the controller sets it to
`Pending` within a second or so. From then on, the next time the machine
PXE-boots, it installs. Nothing checks the references when you create the
Provision: a misspelt name shows up only when the machine boots (see
[When it does not work](#when-it-does-not-work)).

## 6. PXE-boot the machine and watch the install

Power the machine on, or restart it, and let it boot from the network
(pick the network in its boot menu if the disk comes first). Then:

1. The site's DHCP server gives the machine an address, and isoboot's
   dnsmasq adds the boot options. The machine loads iPXE over TFTP.
2. iPXE fetches `/static/boot.ipxe` from nginx. That script chains
   `/dynamic/conditional-boot?mac=<its MAC>`, which nginx passes to httpd.
   httpd finds `web-server-01`, its `Pending` Provision and the `Ready`
   BootConfig, and answers with the kernel, the initrd and the rendered
   kernel arguments.
3. The kernel and initrd come from nginx. The installer mounts the ISO tree
   from nfsd and fetches `user-data` and `meta-data`.
4. `early-commands` reports `InProgress`; `late-commands` reports
   `Complete`. The installer then restarts the machine.

Watch the Provision:

```bash
kubectl -n isoboot-system get provision web-server-01-install -w
```

The `PHASE` column goes from `Pending` to `InProgress` to `Complete`.
`kubectl -n isoboot-system get provision web-server-01-install -o yaml`
also shows `status.message` and `status.lastUpdated`.

Each step leaves a trace in a log:

| Component | Command | What to look for |
|---|---|---|
| dnsmasq | `kubectl -n isoboot-system logs -f deploy/isoboot-dnsmasq` | The machine's PXE request (its MAC, with colons) and the iPXE file sent over TFTP. Nothing here: the machine is not on the PXE subnet, or not booting from the network. |
| httpd | `kubectl -n isoboot-system logs -f deploy/isoboot-httpd` | `conditional-boot request mac=...` when the installer is sent; `no pending provision mac=...` or `not booting the installer` when not; `provision status updated ... phase=InProgress` and `phase=Complete`. |
| nginx | `kubectl -n isoboot-system exec deploy/isoboot-nginx -c nginx -- tail -f /var/run/access.log` | Every request with its status code: `/static/boot.ipxe`, `/dynamic/conditional-boot?mac=...`, `/static/ubuntu-26.04/vmlinuz` and `initrd`, `/dynamic/automation/web-server-01-install/user-data`, `POST /dynamic/status`. Requests that went through squid show the node's address as the client. For Ubuntu, a dozen `404`s for `network-config` and `vendor-data` are normal: cloud-init asks for them for about 20 seconds and goes on without them (httpd logs them as `automation file not served`). |
| nfsd | `kubectl -n isoboot-system logs -f deploy/isoboot-nfsd` | `msg=mount client=<machine address>:<port> export=/ubuntu-26.04` when the installer mounts the ISO tree. |
| controller | `kubectl -n isoboot-system logs -f deploy/isoboot-controller-manager` | Downloads, hash checks and ISO unpacking (step 1). |

## 7. When the install is Complete

`Complete` means the installer ran its last command and reported it.
isoboot does not look at the installed system. From then on:

- The install files of this Provision are no longer served (httpd answers
  404), so the password hash and host keys cannot be read from the network
  any more.
- When the machine PXE-boots again, httpd finds no `Pending` Provision and
  answers 404. iPXE gives up and the firmware boots the next device: the
  disk you just installed. You do not need to change the boot order.

Log in with your key:

```bash
ssh webadmin@<address of web-server-01>
```

Keep the Provision as the record of the install, or delete it.

## Reinstall a machine

isoboot never moves a `Complete` Provision back to `Pending`. To install
the machine again, delete the Provision and create it again, then PXE-boot
the machine:

```bash
kubectl -n isoboot-system delete provision web-server-01-install
kubectl apply -f web-server-01-install.yaml
```

Do the same after a failed install that left the Provision `InProgress`:
httpd only sends a machine to the installer while its Provision is
`Pending`. Make sure the ConfigMaps and the Secret it names still exist. A
Machine may have only one `Pending` Provision: with two, httpd answers
`409 Conflict` and the machine boots its disk.

## Stop a pending install

Delete the Provision before the machine boots:

```bash
kubectl -n isoboot-system delete provision web-server-01-install
```

The next PXE boot finds nothing to install and the machine boots its disk.
A Provision does not expire: if you leave it `Pending`, the machine
installs whenever it next boots from the network.

Once the installer runs, deleting the Provision does not stop it. Its
install files and status calls get 404 from then on, but an installer that
already has its files carries on. Power the machine off instead, then
delete the Provision.

## When it does not work

The most common cases are below; [Troubleshoot](troubleshoot.md) has the
rest.

| What you see | Cause and fix |
|---|---|
| The machine boots its disk; httpd logs `no pending provision mac=...` | No Machine has that MAC, its Provision is not `Pending`, or the Provision's `machineRef` names another Machine. Compare the logged MAC with the Machine's `spec.mac`, and `machineRef` with the Machine's name. |
| httpd logs `not booting the installer ... boot config not ready` | The BootConfig is not `Ready` (step 1). |
| httpd logs `duplicate match` | Two Machines with the same MAC, or two `Pending` Provisions for one Machine. Delete one. |
| httpd logs `boot directive lookup failed` | Something the Provision points to is missing, usually its `bootConfigRef`. The logged error names it. |
| The installer cannot get its files; httpd logs `automation file not served` | The file name is not in the ProvisionAutomation, or the ProvisionAutomation, a ConfigMap or a Secret does not exist, or the Provision is no longer `Pending` or `InProgress`. The `reason` says which. |
| httpd logs `automation render failed` | The template does not render: a syntax error, or a `required` key that is missing (`missing key "..."`). The installer gets 500. |
| The install runs but the Provision stays `Pending` | The `InProgress` call did not arrive, so the `Complete` call got `409` too. Look for `POST /dynamic/status` in the nginx access log and its status code. While it is `Pending`, every network boot installs again: delete the Provision before the installer restarts the machine. |
| The Provision stays `InProgress` | The installer stopped before its last command, or the machine restarted during the install (it then boots its disk, since only a `Pending` Provision is sent to the installer). Look at the machine's console, then [reinstall](#reinstall-a-machine). |
| Kernel messages, or the whole Debian installer, show on the serial port and not on the screen | The Debian and Ubuntu examples put `console=ttyS0,115200` in `kernelArgs`, because CI watches its virtual machines on the serial port. The kernel's last `console=` is its main console. To watch on a monitor, add `console=tty0` right after `console=ttyS0,115200` in your BootConfig's `kernelArgs`. |

## Other releases

CI boots every example in `examples/` in a virtual machine and installs it
to the end, except `debian-13`: CI boots that one only on a network card it
has no firmware for, to check that the install stops. For another release,
change step 1 (the example and the names to wait for) and step 3 (the
install files, from the release's page), and set the Provision's
`bootConfigRef` and `provisionAutomationRef`. Steps 2, 4 and 5 stay the
same.

| Release | Apply | Wait for (BootArtifacts, then BootConfig) | Install files | Page |
|---|---|---|---|---|
| AlmaLinux 10.2 | `examples/alma-10.2.yaml` | `alma-10.2-kernel`, `alma-10.2-initrd`, then `alma-10.2` | `ks.cfg` | [Rocky Linux and AlmaLinux](rocky-and-alma.md) |
| Rocky 10.2 | `examples/rocky-10.2.yaml` | `rocky-10.2-kernel`, `rocky-10.2-initrd`, then `rocky-10.2` | `ks.cfg` | [Rocky Linux and AlmaLinux](rocky-and-alma.md) |
| Debian 13.7 | `examples/debian-13.yaml` | `debian-13-kernel`, `debian-13-initrd`, then `debian-13` | `preseed.cfg` | [Debian](debian.md) |
| Debian 13.7 with NIC firmware | `examples/debian-13.yaml`, then `examples/debian-13-firmware.yaml` | the above and `debian-13-firmware`, then `debian-13-firmware` | `preseed.cfg` | [Debian](debian.md#network-cards-that-need-firmware) |
| Ubuntu 24.04.5 LTS | `examples/ubuntu-24.04.yaml` | `ubuntu-24.04-iso`, then `ubuntu-24.04` | `user-data`, `meta-data` | [Ubuntu](ubuntu.md) |
| Ubuntu 26.04.1 LTS | `examples/ubuntu-26.04.yaml` | `ubuntu-26.04-iso`, then `ubuntu-26.04` | `user-data`, `meta-data` | [Ubuntu](ubuntu.md) |
| Ubuntu 26.10 (beta) | `examples/ubuntu-26.10.yaml` | `ubuntu-26.10-iso`, then `ubuntu-26.10` | `user-data`, `meta-data` | [Ubuntu](ubuntu.md) |

The install file names are the ones the BootConfig's `kernelArgs` ask for
(`inst.ks=.../ks.cfg`, `preseed/url=.../preseed.cfg`, `ds=nocloud;s=.../`).
For all three Ubuntu releases, use the ProvisionAutomation from step 3 and
set `bootConfigRef` to the release's BootConfig.
