# Connectivity

This document tells how devices get to Care on a care-pi host (Phase 6): the three TLS modes, the name-constrained
local CA, the first contact in each mode, the names that Care serves, and Wi-Fi with the failover.
The API contract is `docs/api/README.md` section 15 and `docs/api/openapi.yaml` (tags `tls` and `network`).
The config keys are in `docs/config.md`, sections `tls` and `network`. The decisions are in `plans/00-decisions.md`
(S12, S14 to S16, S30, S35 to S38, P10, P11, O4, O7, O17) and in `plans/07-first-contact-trust.md`.
The DNS/ACME service is `docs/dns-service.md` and `services/dns/API.md`.

## 1. The three modes

| Mode | Name | Certificate | When it works | HSTS | Config |
|---|---|---|---|---|---|
| Local CA (always on) | `<hostname>.local` and each LAN IPv4 address | The root CA of this device (name-constrained) | Always, also with no internet. Each device must install the root CA one time. | No | `tls.local_ca` |
| Public name | `<id>.care-pi.rithviknishad.dev` | Let's Encrypt (DNS-01 through the DNS/ACME service) | On the clinic LAN. The device and the clinic devices need the internet for DNS. | Yes | `tls.public` |
| Tailscale | `<host>.<tailnet>.ts.net` | Let's Encrypt, through `tailscaled` | From each device in the tailnet: mobile data, home, the LAN. | Yes | `tls.tailscale` |

The modes can run together (P10). The local CA mode stays on when the public name is on (S36.3): it is the
fallback when the internet is down. The console (port 8443) is on each name of each mode.

```text
                      ┌───────────────── care-caddy ─────────────────┐
LAN laptop ──► https://sunrise-k3x9.care-pi.rithviknishad.dev ─► Let's Encrypt cert (DNS-01, acmedns)   HSTS
LAN laptop ──► https://care.local, https://192.168.1.10       ─► leaf of the local CA (name-constrained) no HSTS
phone (4G) ──► https://care.tail1234.ts.net (100.x address)   ─► cert of tailscaled (get_certificate)   HSTS
                      └─────────────── :80 :443 :8443 ────────────────┘
```

## 2. The local CA

### 2.1 The root of the agent

The agent makes the root CA (`07` section 4.1). Caddy does not make it.

- Key: ECDSA P-256, in `/var/lib/care-pi/ca/root.key` (0600). Certificate: `/var/lib/care-pi/ca/root.crt`.
- Validity: 10 years. Subject `CN=care-pi <primary name> Root CA, O=care-pi` (for example
  `care-pi care.local Root CA`). Path length 1.
- The Caddyfile gives the root to the `local` CA of Caddy (`pki { ca local { root { cert …; key … } } }`).
  Caddy makes its intermediate (`care-pi - ECC Intermediate`) and the leaf certificates below the root.
- care-caddy reads the directory read-only (`/etc/care-pi-ca`). The backups and the disaster recovery keep it
  (`docs/backups.md`).

### 2.2 The name constraints

The root has a critical X.509 Name Constraints extension. The root can only sign these names (permitted subtrees):

| Kind | Permitted | Why |
|---|---|---|
| DNS | `.local` | `care.local`, `care-2.local`, and each other mDNS name of the device. |
| DNS | `.care-pi.rithviknishad.dev` | The subtree of the public names (S38). |
| DNS | each other local name | Only when a local name is not below `.local`. |
| IP | the `/24` of each LAN IPv4 address | For example `192.168.1.0/24` for `192.168.1.10`. |

A leaf for each other name (for example `evil.example`, or a bank) fails the check in the browser.
So a stolen root key cannot attack the other sites of a clinic device. This is the reason for S36.

The `/24` is a trade-off. A `/32` for each address is stricter, but a new DHCP address then needs a new root, and each
device must install the new root. A `/24` keeps the root valid when the router gives another address in the same network.
The risk is small: the root can sign a leaf for the other addresses of the clinic LAN, which are private addresses.
A move to another network (for example `10.0.0.0/24`) puts the new address outside the constraints.
`GET /tls` then shows it in `local_ca.uncovered`, and the console tells the operator.
Use the name (`care.local`, or the public name) until the root changes.
A new root comes with `POST /tls/local-ca/rotate` (Phase 7). Each device must install the new root.

The root does not permit the Tailscale name or the Tailscale address. The local CA never signs them: the Tailscale site
uses the certificate of `tailscaled`. So a change of the Tailscale mode never needs a new root.
The root needs 1 or more LAN IPv4 addresses: an empty IP list permits each address. Without an address, `apply` waits
and makes the root in a later apply.

`GET /tls` shows the constraints in `local_ca.root.name_constraints` (`permitted_dns_domains`, `permitted_ip_ranges`).
`care-pi tls show` shows them too.

### 2.3 The migration from the root of Caddy

A device of Phase 1 to 5 has the root of Caddy (no name constraints). The first `apply` of Phase 6 does a one-time switch:

1. The plan has the action `ensure-ca`. The step makes the new root in `/var/lib/care-pi/ca/`.
2. The step writes `state/ca.json`: the new fingerprint, the fingerprint of the old root, `changed_at`, and the constraints.
3. The step deletes the old root, the old intermediate, and the local leaf certificates from the data of Caddy.
4. The plan restarts care-caddy. Caddy makes a new intermediate and new leaf certificates below the new root.

Each clinic device must install the new root one time. Until then, the browser shows a warning.
`GET /tls` has `local_ca.ca_changed_at` and `local_ca.previous_fingerprint_sha256`. The console shows a warning one time with
the new fingerprint on the Certificate page. The operator can close the warning. The browser keeps this choice for
the fingerprint.

A new device makes the new root in its first apply. It has no warning.

### 2.4 The certificate page

The Certificate page of the console shows the fingerprint, the name constraints, and the install steps for each OS.
Its QR code opens `https://care-pi.rithviknishad.dev/cert#<root DER, base64url>` (`07` section 4.4). The page of the
DNS service reads the fragment in the browser: the fragment never goes to the server. With no internet, use the
other QR code: it has only the fingerprint, to compare on the device.

## 3. The public name

### 3.1 How it works

1. The agent registers with the DNS/ACME service (`services/dns/API.md`). The service gives an id (for example
   `sunrise-k3x9`) and the credentials of acme-dns for `_acme-challenge.sunrise-k3x9.care-pi.rithviknishad.dev`.
2. The agent sends the LAN address. The service sets the `A` record of the name to the LAN address.
   The public address of the clinic is not necessary. The name only works on the clinic LAN.
3. Caddy gets the certificate with DNS-01. The `acmedns` module writes the TXT record through the service.
   The challenge is TXT-direct: no CNAME. Caddy uses `1.1.1.1` and `8.8.8.8` to check the TXT record.
4. The site of the name has HSTS (`max-age=31536000`). The sites of the local CA have no HSTS.

The agent signs each request with an Ed25519 key (`/etc/care-pi/secrets/dns_client.key`, 0600; `services/dns/API.md`
section 3). The credentials of acme-dns are in `/etc/care-pi/secrets/acme_dns.json` (0600). Caddy reads them as the
podman secret `care-pi-acme-dns` (a file in `/run/secrets/`). They are never in `config.yaml`.
The service is `https://care-pi.rithviknishad.dev`. For a test of another deployment, set `CARE_PI_DNS_URL` in a drop-in
of `care-pi.service` and of `care-dns-update.service`.

### 3.2 Turn it on

Console: Settings > Connectivity > Public name. CLI: `care-pi tls public enable [--hostname-hint NAME] [--acme-ca URL]`.
API: `PUT /tls/public {"enabled": true, "hostname_hint": "sunrise"}`. The job is `tls-public-enrol`:

| Step | What it does |
|---|---|
| `dns-register` | Register (one try: the nonce is for one use). A registration that exists is used again. |
| `dns-address` | Send the LAN address. |
| `write-config` | Set `tls.public.enabled`, `tls.public.clinic_id`, and `tls.public.acme_ca`. |
| apply steps | Render the public site, set the secret, enable `care-dns-update.timer`, cycle the pod. The plug `care_multihost_files_be` turns on (a cutover). |
| `wait-certificate` | Wait up to 5 minutes for the certificate in the data of Caddy. |
| apply steps | When the certificate is there: the redirect of port 80 goes to the public name. |

When the certificate is not there after 5 minutes, the job succeeds with `certificate: pending`. Caddy tries again.
The timer runs `care-pi tls public update-address`, and it applies again when the certificate comes.

### 3.3 The address updates

`care-dns-update.timer` runs `care-pi tls public update-address --scheduled` 2 minutes after the boot, then each 5 minutes.
The command sends the address only when it changed, or when the last send is 6 hours old (`services/dns/API.md` section 5).
An apply after an address change (the NetworkManager hook) also sends the new address (plan action `update-dns`).
`state/tls-public.json` records the last address and the time. `GET /tls` shows them in `public.dns`.

The client tries 3 times (5 s, then 10 s; it obeys `Retry-After`). The next timer run tries again.
When the service tells that the id is revoked, `GET /tls` shows `public.dns.revoked: true`. Turn the public name off,
then on again: the device gets a new name.

### 3.4 Let's Encrypt limits

Let's Encrypt allows 50 new certificates each week for each registered domain. The names of each clinic are below
`care-pi.rithviknishad.dev`, so all clinics share this limit until the domain is on the Public Suffix List.
A renewal does not count. For a test, use the staging directory first:

```sh
care-pi tls public enable --hostname-hint test --acme-ca https://acme-staging-v02.api.letsencrypt.org/directory
```

Browsers do not trust a staging certificate. When the test is good, turn the public name off and on again without
`--acme-ca` (or `care-pi config set tls.public.acme_ca=null --apply`).

### 3.5 Turn it off

CLI: `care-pi tls public disable`. API: `PUT /tls/public {"enabled": false}`. The job sets `tls.public.enabled` to
`false`, applies (the site, the timer, and the secret go; the canonical host goes back to the local name; this is a
cutover), then deletes the registration. The service never gives the same id again.

### 3.6 Other commands

- `care-pi tls public status [--json]`: the name, the certificate, the last address update.
- `care-pi tls public update-address`: send the address when it changed (the timer adds `--scheduled`).
- `care-pi tls public rotate [--new-key]`: get new acme-dns credentials from the service (`--new-key`: also a new
  Ed25519 key). CLI only.

## 4. Tailscale

### 4.1 How it works

Tailscale is strictly opt-in (S37). The installer never installs it. The enrol job installs it:

1. A transient unit `care-pi-tailscale-install` (`systemd-run`) writes the apt key
   (`/usr/share/keyrings/tailscale-archive-keyring.gpg`) and the source (`/etc/apt/sources.list.d/tailscale.list`)
   of `pkgs.tailscale.com`, runs `apt-get install tailscale`, and enables `tailscaled`. The OS update uses the same
   pattern.
2. `tailscale up --hostname=<hostname> --accept-dns=false --accept-routes=false --reset` connects. With an auth key, the key goes in a file
   in `/run/care-pi/` (0600), and the agent deletes the file after the step. Without a key, `tailscale up` gives a login
   link. `GET /tls` shows the link (`tailscale.login_url`); the console shows it with a QR code. The link is never in a
   job log. The job waits up to 15 minutes for the login.
3. The job reads the MagicDNS name and the address from `tailscale status --json` (`Self.DNSName`,
   `Self.TailscaleIPs`), and writes `tls.tailscale.hostname`.
4. The apply renders the Tailscale site. Caddy gets the certificate from `tailscaled`
   (`tls { get_certificate tailscale }`, through `/run/tailscale/tailscaled.sock`). The site has HSTS.

The tailnet must have HTTPS certificates on (admin console of Tailscale > DNS > HTTPS Certificates) and MagicDNS on.
Without them, the job stops with `tailscale-https-disabled`.

### 4.2 The pod and the firewall

The pod publishes 80, 443, and 8443 on the address of `tailscale0` too. The LAN address list (avahi, `default_sni`)
does not have it. At the boot, `tailscale0` can come after the pod: the pod waits up to 30 s for the address
(`ExecStartPre=-care-pi network wait-address`), and starts without it when it does not come.

The apply writes the drop-in `/etc/systemd/system/care-pi-firewall.service.d/50-care-pi-tailscale.conf`.
It adds `udp dport 41641 accept` to the table `inet care-pi` after the unit of the installer loads the table.
The plan action `reload-firewall` reloads the unit. With the Tailscale mode off, the apply deletes the drop-in and
reloads the unit again.

### 4.3 Turn it on and off

Console: Settings > Connectivity > Tailscale. CLI: `care-pi tls tailscale enable [--auth-key-stdin]`.
API: `PUT /tls/tailscale {"enabled": true, "auth_key": "tskey-…"}` (needs a recent password check).
The job is `tls-tailscale-enrol` (steps `tailscale-install`, `tailscale-up`, `tailscale-login`, `write-config`, apply steps).

Off: `care-pi tls tailscale disable`, or `PUT /tls/tailscale {"enabled": false}`. The job sets the config, applies
(the site and the drop-in go), runs `tailscale down` and `tailscale logout`. The package stays installed and
`tailscaled` stays enabled, so a new enable is fast. To remove it: `apt-get purge tailscale`.

### 4.4 Use it

Install the Tailscale app on the phone or the laptop, and log in to the tailnet of the clinic.
Open `https://<host>.<tailnet>.ts.net`. There is no certificate warning and no root CA install.
The device of the doctor needs Tailscale on. A key of an auth key or a login expires (the default is 180 days):
turn off the key expiry of the care-pi device in the admin console of Tailscale.

## 5. The names, the canonical host, and multihost

The order of the names is: the public name, the local names (`<hostname>.local`), the Tailscale name.
The first name is the canonical host:

- `CURRENT_DOMAIN` and `BUCKET_EXTERNAL_ENDPOINT` (`https://<canonical host>`; one value, `01` section 5).
- The `meta.url` of each plug (`https://<canonical host>/plugs/<slug>/<sha12>/<entry>`).
- `default_sni`: the primary LAN address keeps it, so a client without SNI gets the local site.

`REACT_PUBLIC_URL` does not change. `DJANGO_ALLOWED_HOSTS` has `*.care-pi.rithviknishad.dev` (solution default) and each
name. `CSRF_TRUSTED_ORIGINS` has `https://<name>` for each name. The Origin check of the console accepts
`https://<name>:8443` for each name that Caddy serves.

With more than 1 name, the plug `care_multihost_files_be` turns on (`auto: when_multihost`, O7). It signs the file
links for the host of the request, so a file link works from each name. A change of the name set is a cutover.
Caddy sends `Access-Control-Allow-Origin: *` on `/plugs/*`, so the plug bundles load from each name.

## 6. The first contact

| Mode | The operator opens | What happens |
|---|---|---|
| Local CA only | `http://care.local` | `308` to `https://care.local`. A warning until the device installs the root CA (Certificate page). |
| Public name, certificate issued | `http://care.local` | `301` to `https://<id>.care-pi.rithviknishad.dev`. No warning. Bookmark this name. |
| Public name, certificate pending | `http://care.local` | `308` to `https://care.local` (`07` section 6.1). |
| Public name, no internet | `https://care.local` | The local CA site. The public name does not resolve. |
| Tailscale | `https://<host>.<tailnet>.ts.net` | From a device in the tailnet. No warning. |
| Any | `http://192.168.1.10` | `308` to `https://192.168.1.10` (the same host). |

## 7. Wi-Fi and the failover

### 7.1 The approach

care-pi adds Wi-Fi as a secondary NetworkManager connection `care-pi-wifi` beside the ethernet profile (S35).
The ethernet profile does not change.

| `failover` | Route metric | Autoconnect priority | Effect |
|---|---|---|---|
| `true` | 600 | -10 | Ethernet (metric 100) carries the traffic when the cable is in. Wi-Fi carries it when the cable is out. |
| `false` | 50 | 0 | Wi-Fi is the main network. Ethernet is the backup. |

The bond of the gist (an `active-backup` bond of `eth0` and `wlan0`) is rejected. The bond changes the eth0 profile, and
`brcmfmac` (the Wi-Fi driver of the Pi) does not let the bond change the MAC address: the bond needs `fail_over_mac`,
and a wrong value stops the Wi-Fi link (`01` section 12, SP11). The secondary connection has none of these risks.
The cost: each link has its own address, and a failover changes the address of the device.

### 7.2 The addresses

With Wi-Fi up, the LAN address list has 2 addresses. The pod publishes 80, 443, and 8443 on each. `default_sni` and
avahi use the primary address (the interface of the default route). The first time Wi-Fi comes up, the apply cycles the
pod (Care stops for about 2 minutes). A later failover only removes an address: the apply does not cycle the pod
(`state/pod-live.json`), so the failover is seamless. Reserve each address in the router (DHCP reservation):
`GET /network` shows the MAC address and the address of each interface (`dhcp_hints`).

### 7.3 Set it

Console: Settings > Connectivity > Wi-Fi (scan, choose the network, passphrase, country, failover).
CLI: `care-pi network wifi scan`, then `care-pi network wifi set --ssid NAME --psk-stdin [--country IN] [--failover]`.
API: `GET /network/wifi/scan`, then `PUT /network/wifi`. The job is `network-wifi`:

| Step | What it does |
|---|---|
| `wifi-radio` | `nmcli radio wifi on`; set the country (`raspi-config nonint do_wifi_country`, else `iw reg set`). |
| `wifi-profile` | Write the keyfile `/etc/NetworkManager/system-connections/care-pi-wifi.nmconnection` (0600 root) through a transient unit, then `nmcli connection load`. |
| `wifi-connect` | `nmcli --wait 45 connection up care-pi-wifi`. A wrong passphrase gives `wifi-auth-failed`. |
| `wifi-verify` | Read `ip route`: ethernet has the lower metric with the failover on. |
| `write-config` | Set `network.wifi`, then apply (the pod publishes on the new address). |

The passphrase goes from the request to `secrets/wifi.json` (0600) and to the keyfile of NetworkManager. It is never
in a command line, `config.yaml`, or a log. Remove: `care-pi network wifi remove`, or `DELETE /network/wifi`.

The Wi-Fi settings of the Raspberry Pi Imager are NetworkManager profiles too (for example `preconfigured`).
`GET /network` shows them in `wifi.profiles`. care-pi does not change them.

### 7.4 Status

`GET /network` and `care-pi network wifi status` show the interfaces, the interface of the default route
(`default_interface`), the SSID, the signal, the Wi-Fi address, and the DHCP reservation advice.

## 8. The files

| File | Owner | Content |
|---|---|---|
| `/var/lib/care-pi/ca/root.key`, `root.crt` | agent | The name-constrained root CA. |
| `/var/lib/care-pi/state/ca.json` | agent | The fingerprint, the old fingerprint, `changed_at`, the constraints. |
| `/etc/care-pi/secrets/dns_client.key` | agent | The Ed25519 key of the DNS client (PKCS #8, 0600). |
| `/etc/care-pi/secrets/acme_dns.json` | agent | The credentials of acme-dns (0600); podman secret `care-pi-acme-dns`. |
| `/var/lib/care-pi/state/tls-public.json` | agent | The id, the name, the last address and the time of its update. |
| `/var/lib/care-pi/state/tls-tailscale.json` | agent | The last state of the Tailscale job. |
| `/var/lib/care-pi/state/wifi.json` | agent | The last state of the Wi-Fi job. |
| `/etc/care-pi/secrets/wifi.json` | agent | The SSID and the passphrase (0600). |
| `/var/lib/care-pi/state/pod-live.json` | agent | The published addresses of the pod that runs. |
| `/etc/systemd/system/care-dns-update.{timer,service}` | apply | The address updates (public mode only). |
| `/etc/systemd/system/care-pi-firewall.service.d/50-care-pi-tailscale.conf` | apply | UDP 41641 (Tailscale mode only). |

## 9. Troubleshooting

| Symptom | Cause | Do this |
|---|---|---|
| The public name does not resolve on the LAN, but resolves on mobile data. | The router or the DNS server blocks DNS answers with private addresses (DNS rebinding protection). | The console checks this: it loads `https://<name>/_care-pi/health` from the browser. Allow `care-pi.rithviknishad.dev` in the rebinding protection of the router, or use another DNS server (for example `1.1.1.1`). Use `care.local` until then. |
| `GET /tls` shows `public.certificate.state: pending` for more than 1 hour. | DNS-01 fails, or a Let's Encrypt limit. | `care-pi logs caddy`. Check the internet of the device. Check `public.dns`. |
| `public.dns.revoked` is `true`. | The service revoked the id. | Turn the public name off, then on. The device gets a new name. |
| `care.local` opens another device. | 2 devices announce the same mDNS name. avahi renames the second device (`care-2.local`). | Give each device a different `identity.hostname`. |
| The console warns: an address is outside the name constraints. | The device moved to another network. | Use the name. Rotate the local CA (Phase 7: `POST /tls/local-ca/rotate`). |
| All devices show a warning after the update to Phase 6. | The one-time CA switch (section 2.3). | Install the new root on each device. Compare the fingerprint. |
| `tailscale-https-disabled` | The tailnet has no HTTPS certificates. | Turn on MagicDNS and HTTPS Certificates in the admin console of Tailscale. Then turn Tailscale on again. |
| `tailscale-login-required` | The login link expired, or nobody logged in within 15 minutes. | Turn Tailscale on again, or use an auth key. |
| The Tailscale name stops working after months. | The key of the device expired. | Turn off the key expiry of the device in the admin console of Tailscale. |
| `wifi-auth-failed` | The passphrase is wrong. | Type the passphrase again. |
| `wifi-no-device` | The device has no Wi-Fi (or the radio is blocked: `rfkill`). | Use ethernet. |
| Care is slow to start at the boot with Tailscale on. | `tailscale0` is not up; the pod waits up to 30 s. | No action. Care starts without the Tailscale address, and the next apply adds it. |
