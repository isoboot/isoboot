# Templates and the status callback

isoboot renders two kinds of Go templates
([`text/template`](https://pkg.go.dev/text/template)) and takes one callback
from the installer:

| What | Rendered or called | Rendered by |
|---|---|---|
| `BootConfig.spec.kernelArgs` | Each time a machine asks `/dynamic/conditional-boot` | httpd, into the iPXE `kernel` line |
| `ProvisionAutomation.spec.files` | Each time an installer fetches `/dynamic/automation/<provision>/<file>` | httpd, as the file body |
| Status callback | The installer posts to `/dynamic/status` | httpd moves the Provision to `InProgress` or `Complete` |

Both templates are plain `text/template`: nothing is escaped, and only the
functions listed below exist. There is no Sprig, so `b64enc`, `default`,
`quote`, `indent` and the like are not available.

The fields that hold the templates are described in
[Custom resources](custom-resources.md). The examples on this page use a
release installed as in [Install isoboot](../how-to/install.md)
(`helm install isoboot ... --namespace isoboot-system`), a node whose address
on the PXE subnet is `192.168.101.2`, and the chart's default ports (nginx
8080, squid 3128).

## Where the URLs come from

httpd builds every URL from the address the machine used to reach nginx. The
chart's `boot.ipxe` chains to `http://<node IPv4 on the PXE subnet>:8080/dynamic/conditional-boot`,
and nginx passes that host and its own port on to httpd (`X-Forwarded-Host`,
`X-Forwarded-Port`). So, with the chart, every URL starts with
`http://192.168.101.2:8080`.

## BootConfig.spec.kernelArgs

### Variables

| Variable | Expands to | Empty when |
|---|---|---|
| `{{.ProvisionAutomationBaseURL}}` | `http://192.168.101.2:8080/dynamic/automation/<provision name>`, with no trailing `/` | Never |
| `{{.UpdatePhaseURL}}` | `http://192.168.101.2:8080/dynamic/status` | Never |
| `{{.ProvisionName}}` | The Provision's `metadata.name` | Never |
| `{{.ProxyURL}}` | `http://192.168.101.2:3128`: squid on the same node address, port `squid.port` | httpd has no `PROXY_PORT`. The chart always sets it to `squid.port`, so with the chart it is never empty. The examples still write `{{if .ProxyURL}}...{{end}}`. |
| `{{.NFSRoot}}` | `192.168.101.2:/<bootconfig name>`, the value for casper's `nfsroot=` | The BootConfig is in `netboot` mode |

Any other name, for example `{{.ISOURL}}` (removed) or a typo, is an error.
Only the `text/template` built-ins work here (`if`, `with`, `and`, `or`,
`not`, `eq`, `printf`, ...). The `required` function of install files does
not exist in kernel arguments.

### What the machine receives

httpd renders `kernelArgs` and appends it to the `kernel` line of the iPXE
script. An empty `kernelArgs` gives a `kernel` line without arguments. For the
BootConfig in [`examples/alma-10.2.yaml`](../../examples/alma-10.2.yaml) and a
Provision named `web-server-01-install`, the script is:

```
#!ipxe
kernel /static/alma-10.2/kernel/vmlinuz ip=dhcp inst.proxy=http://192.168.101.2:3128 inst.repo=http://repo.almalinux.org/almalinux/10.2/BaseOS/x86_64/os inst.ks=http://192.168.101.2:8080/dynamic/automation/web-server-01-install/ks.cfg
initrd /static/alma-10.2/initrd/initrd.img
boot
```

In `iso` mode the paths are `/static/<bootconfig>/vmlinuz` and
`/static/<bootconfig>/initrd`. For
[`examples/ubuntu-26.04.yaml`](../../examples/ubuntu-26.04.yaml):

```
#!ipxe
kernel /static/ubuntu-26.04/vmlinuz console=ttyS0,115200 ip=dhcp netboot=nfs nfsroot=192.168.101.2:/ubuntu-26.04 fsck.mode=skip autoinstall ds=nocloud;s=http://192.168.101.2:8080/dynamic/automation/web-server-01-install/ ---
initrd /static/ubuntu-26.04/initrd
boot
```

### One line only

The rendered result must be one line. Leading and trailing white space,
including a final line break, is dropped first. Any line break left inside
(`\n` or `\r`) is an error, because every further line of the iPXE script
would be another command.

| `kernelArgs` in YAML | Result |
|---|---|
| `kernelArgs: "ip=dhcp inst.ks={{.ProvisionAutomationBaseURL}}/ks.cfg"` | One line. Works. |
| `kernelArgs: >` followed by the arguments on several lines with the same indentation | The lines are joined with spaces, and the final line break is dropped. Works. |
| `kernelArgs: \|` (literal block) with more than one line | Line breaks stay. Error. |
| `kernelArgs: >` with one line indented more than the others | YAML keeps the line breaks around that line. Error. |

### Validation by the controller

The BootConfig controller renders `kernelArgs` with sample values before it
does anything else, on every reconcile. If that fails, the BootConfig goes
to `Error`, nothing is built, and the controller checks again every 10
seconds. Fix the template and the BootConfig goes on to `Ready`.

| Cause | `status.message` |
|---|---|
| Template does not parse, for example `{{.ProxyURL` | `invalid kernelArgs: parsing kernel args template: template: kernelArgs:1: unclosed action` |
| Unknown variable, for example `{{.Hostname}}` | `invalid kernelArgs: executing kernel args template: template: kernelArgs:1:2: executing "kernelArgs" at <.Hostname>: can't evaluate field Hostname in type kernelargs.Data` |
| Unknown function, for example `{{required .ProxyURL}}` | `invalid kernelArgs: parsing kernel args template: template: kernelArgs:1: function "required" not defined` |
| A line break inside the result | `invalid kernelArgs: kernel args must be a single line` |

```bash
kubectl -n isoboot-system get bootconfig alma-10.2 \
  -o jsonpath='{.status.phase}{"\n"}{.status.message}{"\n"}'
```

The sample values are those in the variables table with the address
`192.0.2.1`, the provision name `sample` and the BootConfig name `sample`.
Every variable has a value, `NFSRoot` included, so the check does not catch:

- `{{.NFSRoot}}` in a `netboot` BootConfig. It passes and renders empty at
  boot.
- A branch that only runs when a value is empty, such as
  `{{if not .ProxyURL}}...{{end}}`. A line break or an unknown variable
  inside it fails only at boot.

### Errors at boot

A BootConfig that is not `Ready` (an invalid template included) is never
rendered: `/dynamic/conditional-boot` answers 404 `boot config not ready` and
the machine boots its local disk. The other answers that send the machine
to its local disk are:

| Cause | Answer |
|---|---|
| No `mac` parameter, or not in the form `aa-bb-cc-dd-ee-ff` | 400 `missing required parameter: mac` or `invalid mac address format` |
| No Machine with this MAC, or no `Pending` Provision for it | 404 `no pending provision for MAC` |
| Two Machines with this MAC, or two `Pending` Provisions for the Machine | 409 `multiple machines with MAC ...` or `multiple pending provisions for MAC ...` |

Errors while rendering for a `Ready` BootConfig:

| Cause | Answer | httpd log |
|---|---|---|
| The template fails to render with the real values | 500 `internal error` | `kernel args template failed` with the error |
| `iso` mode, and the machine reached nginx by a host name or an IPv6 address | 500 `nfsroot needs an IPv4 address` | `nfsroot needs an IPv4 address, but the machine reached isoboot through another host` |

The chart's `boot.ipxe` chains with `|| exit`, so on any error iPXE exits and
the firmware moves on to its next boot device. Read the reason with
`kubectl -n isoboot-system logs deploy/isoboot-httpd`.

### IPv4-only nfsroot

The installer's NFS client (klibc `nfsmount`) takes an IPv4 literal only.
httpd therefore refuses to answer an `iso`-mode BootConfig unless the
machine reached nginx by an IPv4 address. An IPv4-mapped IPv6 address
(`::ffff:192.168.101.2`) is refused too. The check runs whenever
`kernelArgs` is not empty, even if it does not use `{{.NFSRoot}}`. With the
chart this never happens: `boot.ipxe` always uses the node's IPv4 address.

### iPXE rules for the rendered line

isoboot does not quote or escape anything: the rendered text goes into the
iPXE script as it is. iPXE (the chart installs v2.0.0) then reads the line
like this:

| In the rendered line | What iPXE does |
|---|---|
| Spaces and tabs | Split the line into words, which are joined again with one space each. Runs of spaces become one. |
| `"` and `'` | Nothing: iPXE has no quoting. The characters reach the kernel unchanged, so `foo="a b"` still works for Linux, but `foo="a  b"` arrives as `foo="a b"`. |
| `${name}` | Replaced by the iPXE setting `name`, or by nothing if there is no such setting. There is no way to escape it. This can be useful: `${net0/mac:hexhyp}` gives the boot NIC's MAC address. |
| A word that starts with `#` | The rest of the line is a comment and is dropped. |
| A word that is exactly `\|\|`, `&&` or `;` | Ends the `kernel` command. The rest runs as another command. |
| `;` inside a word, as in `ds=nocloud;s=...` | Kept. |
| A word that starts with `-`, such as `---` | Kept. iPXE stops reading its own options at the kernel path. |

To put a literal `{{` into the arguments, write `{{"{{"}}`.

### Example

This is [`examples/alma-10.2.yaml`](../../examples/alma-10.2.yaml), which the
E2E installs on every run. The kernel arguments use two of the variables
(`ProxyURL` and `ProvisionAutomationBaseURL`) and add `inst.proxy=` only when
squid is on:

```yaml
apiVersion: isoboot.github.io/v1alpha1
kind: BootArtifact
metadata:
  name: alma-10.2-kernel
  labels:
    app.kubernetes.io/name: isoboot
spec:
  url: https://repo.almalinux.org/almalinux/10.2/BaseOS/x86_64/os/images/pxeboot/vmlinuz
  sha256: "90ee8394588e78eed5136fa484a2b49c6a9e70960356b046c1b90a5a6f449f7d"
---
apiVersion: isoboot.github.io/v1alpha1
kind: BootArtifact
metadata:
  name: alma-10.2-initrd
  labels:
    app.kubernetes.io/name: isoboot
spec:
  url: https://repo.almalinux.org/almalinux/10.2/BaseOS/x86_64/os/images/pxeboot/initrd.img
  sha256: "e1b5b7f627a0d2982981521e3dd550a8f51e40d08b3e9318763d2c7ce273b3a9"
---
apiVersion: isoboot.github.io/v1alpha1
kind: BootConfig
metadata:
  name: alma-10.2
  labels:
    app.kubernetes.io/name: isoboot
spec:
  netboot:
    kernelRef: alma-10.2-kernel
    initrdRef: alma-10.2-initrd
  kernelArgs: "ip=dhcp {{if .ProxyURL}}inst.proxy={{.ProxyURL}} {{end}}inst.repo=http://repo.almalinux.org/almalinux/10.2/BaseOS/x86_64/os inst.ks={{.ProvisionAutomationBaseURL}}/ks.cfg"
```

The script it gives is shown under [What the machine receives](#what-the-machine-receives).
Without a proxy URL the `inst.proxy=` word and its trailing space disappear:
`ip=dhcp inst.repo=http://repo.almalinux.org/... inst.ks=...`.

## ProvisionAutomation files

### URL layout

| URL | Serves |
|---|---|
| `http://192.168.101.2:8080/dynamic/automation/<provision>/<file>` | The key `<file>` of `spec.files` in the Provision's ProvisionAutomation, rendered for the Provision `<provision>` |

`{{.ProvisionAutomationBaseURL}}` in kernel arguments is this URL without
`/<file>`. File names in `spec.files` must match
`^[A-Za-z0-9][-A-Za-z0-9_.]*$` (one path component; the API server rejects
others), so a file is always one level below the base URL.

### When files are served

Files can hold Secrets, and the endpoint has no authentication, so httpd
serves them only while the install runs:

| Provision `status.phase` | `GET` answer |
|---|---|
| `Pending` | 200, the rendered file. This includes the time before the machine boots. |
| `InProgress` | 200, the rendered file |
| `Complete`, `Failed`, `ConfigError`, `WaitingForBootSource`, or no phase yet | 404 |

httpd reads the ProvisionAutomation, ConfigMaps and Secrets from the API
server on each request, so a change is used by the next fetch, even during
an install.

Every answer to a `GET` on this endpoint, errors included, carries
`Cache-Control: no-store`, so neither squid
nor any other cache keeps a rendered file. nginx answers 403 to clients
outside the PXE subnet, the node itself and localhost.

### Data

| Field | Value |
|---|---|
| `.ConfigMaps` | A map of every key in `data` of the ConfigMaps listed in the Provision's `spec.configMaps`, merged in list order: a later ConfigMap's key replaces an earlier one's. `binaryData` is not included. |
| `.Secrets` | The same for `data` of the Secrets in `spec.secrets`. Values are the decoded value, not base64. |
| `.ProvisionName` | The Provision's `metadata.name` |
| `.UpdatePhaseURL` | `http://192.168.101.2:8080/dynamic/status` |
| `.ProxyURL` | `http://192.168.101.2:3128`, the same value as in kernel arguments (empty when httpd has no `PROXY_PORT`) |

`ProvisionAutomationBaseURL` and `NFSRoot` exist only in kernel arguments.
In a file they are an error.

The ConfigMaps and Secrets must be in the release namespace (httpd reads only
that namespace).

### Functions

| Function | Use | Result |
|---|---|---|
| `required` | `{{ required .ConfigMaps "default_user.password" }}` | The value. A missing key fails the render. |
| `text/template` built-ins | `and`, `or`, `not`, `len`, `index`, `slice`, `print`, `printf`, `println`, `eq`, `ne`, `lt`, `le`, `gt`, `ge`, `html`, `js`, `urlquery`, `call` | As in Go |

### Reading a key, and what a missing key does

| Template | Key present | Key missing |
|---|---|---|
| `{{ required .ConfigMaps "default_user.username" }}` | The value | Render fails: 500 |
| `{{ index .ConfigMaps "default_user.username" }}` | The value | Empty string, no error |
| `{{ .ConfigMaps.hostname }}` | The value | Render fails: 500. Works only for keys made of letters, digits and `_` that do not start with a digit: in `.ConfigMaps.default_user.username` each dot is another lookup. |
| `{{ with index .ConfigMaps "machine_id" }}{{ . }}{{ else }}...{{ end }}` | The value (an empty value takes the `else` branch too) | The `else` branch: use this for optional keys |

Use `required` for anything the machine cannot do without: with `index`, a
misspelt key quietly installs an empty password hash or host key.

### No escaping

Values go into the file exactly as stored. A value with a quote, a line
break or a YAML-special character can break the file around it. The E2E
keeps PEM host keys a second time, base64-encoded, under their own Secret
keys (`ssh_host_ed25519_key_b64`) and decodes them on the machine:

```yaml
late-commands:
  - printf '%s' '{{ required .Secrets "ssh_host_ed25519_key_b64" }}' | base64 -d > /target/etc/ssh/ssh_host_ed25519_key
```

There is no `b64enc` function, so the encoding has to happen when the Secret
is created.

### Errors

Nothing checks a ProvisionAutomation ahead of time: a broken template shows
up only when an installer fetches the file.

| Cause | Answer | httpd log |
|---|---|---|
| The provision name in the URL is not a valid name | 400 `invalid provision name` | |
| No such Provision, or it is not `Pending` or `InProgress` | 404 `not found` | `automation file not served` with the reason |
| No such ProvisionAutomation, or no such file in it | 404 `not found` | same |
| A ConfigMap or Secret listed in the Provision does not exist | 404 `not found` | same |
| The template does not parse (an unknown function such as `b64enc`) or fails to render (`required` on a missing key, `.ConfigMaps.key` on a missing key, an unknown field) | 500 `internal error` | `automation render failed` with the error |
| httpd is not running, or does not answer within 5 seconds | 502 or 504 from nginx | nginx's, not httpd's |

To see a file as the installer will, fetch it from the node while the
Provision is `Pending`:

```bash
curl -i http://192.168.101.2:8080/dynamic/automation/web-server-01-install/user-data
```

### Ubuntu: the NoCloud seed

Ubuntu's installer reads its answer file through cloud-init's NoCloud data
source. The kernel argument `ds=nocloud;s={{.ProvisionAutomationBaseURL}}/`
gives it the seed URL. cloud-init appends file names to that URL, so it must
end in `/` (`{{.ProvisionAutomationBaseURL}}` has none of its own). It then
fetches:

| File name in `spec.files` | Content |
|---|---|
| `meta-data` | Required. The E2E serves `instance-id: <hostname>`. An empty file also works. |
| `user-data` | `#cloud-config` with an `autoinstall:` section |

The E2E serves only these two files. What the `autoinstall:` section must
contain, screen by screen, is in
[the Ubuntu answer file study](../ubuntu-autoinstall-journey.md).

### Example

[Provision a machine](../how-to/provision-a-machine.md) installs Ubuntu
26.04 with the ProvisionAutomation `ubuntu-autoinstall` (its
[step 3](../how-to/provision-a-machine.md#3-write-the-install-files)), the
ConfigMaps `default-user` and `web-server-01`, and the Provision
`web-server-01-install`. While that Provision is `Pending`,
`http://192.168.101.2:8080/dynamic/automation/web-server-01-install/user-data`
answers (the hash and key are yours):

```yaml
#cloud-config
autoinstall:
  version: 1
  # No network: section. The installer runs from an NFS root over the
  # boot NIC; reconfiguring that NIC can freeze the install.
  proxy: http://192.168.101.2:3128
  identity:
    hostname: web-server-01
    username: webadmin
    password: "$6$...your hash..."
  ssh:
    install-server: true
    allow-pw: false
    authorized-keys:
      - "ssh-ed25519 AAAA...your key... you@workstation"
  storage:
    layout:
      name: direct
  early-commands:
    - curl -X POST -d 'provisionName=web-server-01-install&phase=InProgress' http://192.168.101.2:8080/dynamic/status
  late-commands:
    - curl -X POST -d 'provisionName=web-server-01-install&phase=Complete' http://192.168.101.2:8080/dynamic/status
```

and `.../meta-data` answers `instance-id: web-server-01`.

The ProvisionAutomation has no machine-specific text, so other machines can
share it: each Provision brings its own ConfigMap and Secret. The installed
system keeps `proxy:` in its apt configuration (see the README's
[known limitations](../../README.md#known-limitations)). Tested install
files for every supported release, kickstart and preseed included, are in
[`test/e2e/provision/automation/`](../../test/e2e/provision/automation/).

## The status callback

### Request

```
POST http://192.168.101.2:8080/dynamic/status
```

| Parameter | Value |
|---|---|
| `provisionName` | The Provision's name. In templates: `{{.ProvisionName}}`. |
| `phase` | `InProgress` or `Complete` |

Send the parameters as a form body (`application/x-www-form-urlencoded`, as
`curl -d` and `wget --post-data` do) or in the query string. Only `POST` is
accepted; any other method gets 405. The call may go through squid: squid
allows nginx's port, and nginx accepts the node's own addresses.

### Allowed transitions

| Provision is | `phase=` | Result |
|---|---|---|
| `Pending` | `InProgress` | 200 `OK`. `status.message` is `Installation in progress`. |
| `InProgress` | `Complete` | 200 `OK`. `status.message` is `Installation complete`. |
| anything else | either | 409 |

Each successful call also sets `status.lastUpdated`. The same call twice
gets 409 the second time, so an installer that retries should ignore the
answer (`|| true`).

The controller sets a new Provision to `Pending` within a second or so of
its creation; until then it has no phase, and both the PXE boot and its
files answer 404. Once it is `InProgress`,
`/dynamic/conditional-boot` no longer sends the machine to the installer:
a reboot in the middle of the install boots the local disk. Once it is
`Complete`, its files answer 404 as well. To install the machine again,
delete the Provision and create it again
([Reinstall a machine](../how-to/provision-a-machine.md#reinstall-a-machine)).

### Answers

| Cause | Answer |
|---|---|
| Success | 200 `OK` |
| No `provisionName` | 400 `missing required parameter: provisionName` |
| `provisionName` is not a valid name | 400 `invalid provision name` |
| No `phase`, or any value but `InProgress` and `Complete` | 400 `invalid phase: must be InProgress or Complete` |
| No such Provision | 404 `provision not found` |
| Transition not allowed | 409 `invalid phase transition: cannot transition from Pending to Complete` (with the real phases) |
| The Provision changed during the update | 409 with the Kubernetes conflict message. Send the call again. |
| Anything else | 500 `internal error`, details in the httpd log |
| A client outside the PXE subnet, the node and localhost | 403 from nginx |

There is no authentication: any client nginx accepts can move any Provision
it can name. The phase only ever moves forward.

### Examples

From an Ubuntu autoinstall `user-data` (see the example above) or a
kickstart `%pre`/`%post` section:

```bash
curl -X POST -d "provisionName={{.ProvisionName}}&phase=InProgress" {{.UpdatePhaseURL}}
```

From a Debian preseed, with `wget` as the E2E preseed does:

```
d-i preseed/early_command string wget -q -O /dev/null --post-data="provisionName={{.ProvisionName}}&phase=InProgress" "{{.UpdatePhaseURL}}" || true
```

By hand, from the node:

```bash
curl -X POST -d 'provisionName=web-server-01-install&phase=InProgress' \
  http://192.168.101.2:8080/dynamic/status
kubectl -n isoboot-system get provision web-server-01-install
```
