# 07 — First contact: how an operator's browser comes to trust a brand-new Pi

Status: **DECIDED (S36)** — see §6. The analysis below is kept in full so the choice stays
explainable. Items marked *(verify: SP7)* are browser/OS behaviours to confirm on real devices in
the Phase 0 spike rather than assert. `care-pi.dev` in this document is the placeholder written
before the domain was chosen; read it as **`care-pi.rithviknishad.dev`** (S38).

---

## 1. What "first contact" has to achieve

The image is one public file; nothing is printed; the Pi has no screen. The first time an operator
types `care.local`, three distinct things need to happen, and they are often conflated:

| Goal | Direction | What goes wrong if it is skipped |
|---|---|---|
| **G1 Pi → operator** | The browser must learn *this* Pi's identity (its root CA, or a publicly valid name) | Operator installs an attacker's root CA, or types the first password into an attacker's page |
| **G2 operator → Pi** | The Pi must learn that the person claiming it is the owner | Anyone on the LAN can claim an unclaimed box and become its operator |
| **G3 channel** | The first password / recovery key must not cross the wire in a readable form | Passive sniffing on the LAN |

Most products solve only G3 (by using TLS) and *call* it security. G1 is the hard one, because a
browser offers no way to verify a never-seen certificate without an out-of-band channel.

### 1.1 The adversary

An attacker present **on the clinic LAN during the setup window** who can spoof mDNS (`care.local`),
ARP, or DHCP, and therefore terminate connections meant for the Pi. Outside the setup window this
adversary is also what the Care auth wall and TLS protect against; inside it, nothing does yet.

Realistically: a hostile guest on the clinic Wi-Fi, a compromised laptop, or a malicious IoT device.
Probability low, but the worst-case consequence is unusually high — see 1.2.

### 1.2 Severity ladder (why the local-CA design raises the stakes)

1. Attacker learns the operator password → operator resets it; attacker needs to be back on the LAN
   to exploit it. Bad, recoverable.
2. Attacker claims the box first → visible immediately (operator cannot log in); recover by factory
   reset. Annoying, recoverable.
3. **Attacker gets the operator to install the attacker's root CA** → every HTTPS site on that
   device (bank, email) can be silently intercepted whenever the attacker is on-path, for years.
   Invisible, and the operator has no reason to ever suspect it.

Level 3 exists *only because* we ask the operator to install a root CA. That has two consequences
for the design regardless of the first-contact flow chosen:

- **The CA we generate must be name-constrained** (§4.1), so that even a *leaked* care-pi CA is
  harmless to the rest of the web. The attacker's fake CA would not be constrained, but a
  constrained genuine CA at least removes level-3 risk from every future compromise of the Pi.
- **Public-cert and Tailscale modes avoid level 3 entirely** because no CA is installed. That is a
  strong argument for making public-cert the default whenever the Pi has internet (§3.8).

---

## 2. Browser facts that constrain every option

These are the walls we design around. Each is either well-established or flagged for the spike.

| # | Fact | Consequence |
|---|---|---|
| F1 | Page JavaScript cannot read the TLS certificate of its own connection. | No in-page "compare this fingerprint" is trustworthy — the page itself came over the channel in question. |
| F2 | `fetch`/`WebSocket`/`import()` cannot bypass a certificate error; only top-level navigation offers "Proceed anyway". | A *trusted* hosted page (`https://care-pi.dev`) cannot talk to an untrusted `https://care.local`. |
| F3 | Mixed content: an HTTPS page cannot `fetch` `http://care.local`. | The same hosted page cannot use a plaintext endpoint on the Pi either. Private-network exemptions do not relax this. |
| F4 | Code served over an untrusted channel is untrusted code. | Any clever in-browser protocol (PAKE, pinning) run from a page served by the Pi before trust exists is theatre: the attacker simply serves different JavaScript. Only code from a trusted origin can run such a protocol. |
| F5 | Chrome, Safari and Firefox now try `https://` first for typed hostnames and fall back to `http://` when the HTTPS attempt fails. Whether a *certificate error* counts as "failed" (silent fallback) or shows the interstitial differs by browser and version. *(verify: SP7)* Chrome 154 (Oct 2026) makes "Always use secure connections" default for public sites but exempts private-network hosts. *(verify: SP7)* | We cannot control which entry point a given device hits: some will land on `http://care.local`, some on the `https://` interstitial. **Both entry points must work no matter which option below is chosen.** |
| F6 | On an origin whose certificate error was bypassed, Chrome refuses to register a service worker and treats some APIs as unavailable; Firefox stores a permanent exception; Safari stores a per-site exception. *(verify: SP7, per device)* | "Just click through every time" is not a viable permanent mode for Care (PWA/service worker, camera, WebAuthn). Installing the CA is effectively mandatory in local-CA mode. |
| F7 | HSTS on a host with an untrusted certificate removes the "Proceed" button entirely. | **Never send `Strict-Transport-Security` in local-CA mode.** Send it only for public-cert and Tailscale hostnames. |
| F8 | Installing a root CA is a different ceremony on every OS (§4.3). iOS needs two separate Settings screens; Android warns loudly; Firefox desktop trusts the OS store by default, Firefox Android does not. | The per-OS instructions page is a product feature, not an afterthought. It is also the single best reason to prefer public-cert mode. |
| F9 | A `cloud-init` `user-data` file on the FAT boot partition is read once, at first boot. On Pi OS trixie this is how Raspberry Pi Imager applies its "OS customisation" (hostname, user, password, Wi-Fi) — confirmed on lumine (`ds=nocloud;i=rpi-imager-…`). | There is exactly one operator-controlled secret that reaches the Pi before any network contact: **the password typed into Imager.** It is the natural G2 credential (§4.2). The boot partition is also a bidirectional physical channel (§3.4). |

---

## 3. The options

For each: the flow, what it authenticates (G1/G2/G3), failure modes, UX cost, implementation cost.

### 3.1 Option A — plaintext bootstrap page over HTTP ("TOFU-plain") — *your current lean*

**Flow.** Port 80 serves a small page while the box is unclaimed: "Welcome to care-pi. Step 1 —
install the security certificate for this device: [Download] (instructions auto-selected for
iPhone/Android/Windows/macOS). Step 2 — [Continue to https://care.local]". After the operator
completes setup, port 80 becomes a bare `301`. Re-enabling the page for N minutes from the console
is possible for enrolling additional devices (but see §4.4 for better ways).

**Authenticates.** G1: no. G2: only if combined with the Imager-password claim (§4.2). G3: yes,
once the user is on HTTPS; the CA file itself travels in plaintext.

**Failure modes.**
- Active attacker swaps the CA file in transit (does not even need to spoof mDNS — ARP suffices).
  Result: severity level 3.
- Because the attacker controls the page, they can also "helpfully" link to `https://evil.local`.
- Zero continuity: nothing stored on the client remembers the first contact, so a second TOFU on the
  next device is just as blind.

**UX.** The cleanest possible first screen: no scary interstitial, explicit numbered steps, device
detection. The scary part moves to the OS's own certificate-install dialogs, which exist in every
option.

**Implementation.** Trivial: a Caddy site block on `:80` toggled by agent state.

**Note on F5.** Many devices will *still* arrive at the `https://` interstitial (typed
`https://`, bookmarks, QR codes, browsers that do not fall back). So Option B's path has to exist
anyway; Option A only adds a friendlier door beside it. The decision is therefore narrower than it
looks: **"does port 80 serve a bootstrap page while unclaimed, or only a 301?"**

### 3.2 Option B — HTTPS only, one-time interstitial ("TOFU-TLS")

**Flow.** Port 80 is a `301`. First visit shows the browser's certificate warning; the operator
clicks Advanced → Proceed (iOS: Show details → visit this website). The console then shows the same
"install the certificate" page as Option A, served over TLS, plus the CA fingerprint.

**Authenticates.** G1: no (still TOFU). G2: with §4.2. G3: yes, from the first byte.

**Compared with A.**
- The attacker must terminate TLS rather than merely rewrite plaintext. Same ARP/mDNS skill set,
  marginally more effort; **not** a meaningful security gain on its own.
- Continuity: Firefox and Safari store the accepted certificate; if an attacker appears later with a
  different certificate the user gets a *new* warning. Chrome's bypass memory is short.
  *(verify: SP7)*. Weak but non-zero.
- No plaintext path exists at any time, so there is no "forgot to turn it off" window and nothing
  to re-enable for new devices.
- Downside: the interstitial is frightening and its wording ("attackers might be trying to steal
  your information") is exactly the opposite of what we want a doctor to read at minute one. It is
  also hidden behind "Advanced" in Chrome, and the number of taps on iOS is four.

**Implementation.** Nothing beyond Caddy defaults.

### 3.3 Option C — physical channel: USB stick export ("plug a stick in")

**Flow.** While unclaimed (and later, on demand from the console), if a FAT-formatted USB stick is
inserted into the Pi, the agent writes `care-pi-certificate.crt`, `care-pi-certificate.mobileconfig`
(iOS/macOS profile), a `README.html` with per-OS instructions, and `fingerprint.txt`, then blinks
the ACT LED three times. The operator moves the stick to the laptop/phone (phones: via a USB-C stick
or the laptop) and installs the certificate from the file.

**Authenticates.** G1: **yes** — the file came from the device physically in the operator's hands,
not from the network. G2: with §4.2. G3: yes.

**Failure modes.** Operator has no stick; stick is NTFS/exFAT (handle exFAT too); phone-only
operators. A malicious stick is out of scope (physical attack, S19/S25).

**UX.** Simple to explain ("any USB stick, 10 seconds, three blinks"). Still ends in the OS
certificate dialogs. Good fit for the local IT person persona; weaker for the phone-only doctor.

**Implementation.** Small: a udev rule → agent job (mount read-only check, write files, unmount,
LED). Pi 4/5 both have a controllable ACT LED.

### 3.4 Option D — boot-partition hand-off ("secure flashing")

Two directions, both using the fact that the operator *flashed the disk* and so has physical access
to its FAT boot partition from the very laptop that will do the setup.

**D1 — pre-seed (laptop → Pi).** A trusted hosted page (`https://care-pi.dev/prepare`, static,
client-side only) or the `care-pi` CLI generates:
1. a `user-data` fragment (cloud-init) with hostname, operator password hash, TLS mode, time zone,
   optional Wi-Fi, **and a 32-byte CA seed**;
2. the matching root certificate (derived deterministically from the seed in the browser), offered
   for download/AirDrop/e-mail *before the Pi ever boots*.

The operator drops `user-data` onto the freshly flashed boot partition (Windows/macOS both mount it
as a drive) and boots. The agent derives the same CA from the seed, deletes the seed from the FAT
partition, and the certificate the operator already holds is valid for the Pi. First contact is
fully authenticated (G1, G2 via the password hash in the same file, G3).

Cost: one more step at flash time (download a file, copy it to the drive) that non-technical
operators may skip. If skipped, everything falls back to A/B. It is the cheapest *authenticated*
path to build: the hosted page is a few hundred lines; the agent already reads `user-data`.

Caveat: the seed is a private key sitting on an unencrypted FAT partition until first boot
(minutes, and only readable with physical access). Acceptable under S19.

**D2 — read-back (Pi → laptop).** On first boot the agent writes the root certificate (public) to
`/boot/firmware/care-pi/`. The operator can power off, plug the SSD into the laptop, and copy it.
Authenticated but clumsy (power cycle, NVMe HATs are not pluggable). Worth having only as a
documented fallback since it costs nothing.

### 3.5 Option E — show the fingerprint on a screen (HDMI / serial)

**Flow.** `getty` on `tty1` is replaced while unclaimed by a status screen: hostname, IP, CA
fingerprint, and a QR code (rendered in text) of the certificate's SHA-256. The operator compares
the browser-shown fingerprint against the screen, or scans the QR with a phone.

**Authenticates.** G1: yes, *if* a monitor is attached. Pi 4/5 need a micro-HDMI adapter most
clinics do not own; Pi 5 is often installed headless in a cupboard.

**Verdict.** Cheap to add (we want a status tty anyway for IP display), unreliable as the primary
path. The phone-QR variant is valuable only when the QR's source is trusted, which is the whole
point: a QR shown by the *untrusted web page* proves nothing — that is the flaw in the earlier
"QR on a second device" idea unless the QR comes from a screen, a sticker, or an already-trusted
device (§4.4).

### 3.6 Option F — cloud-assisted enrolment (relay + PAKE on the Imager password)

**Flow.** The Pi opens an outbound HTTPS connection to a relay we operate (`enrol.care-pi.dev`)
and registers under an opaque id. The operator opens the *hosted* page (trusted via public PKI) and
enters the password they typed into Imager. Browser and Pi run a PAKE (CPace/OPAQUE) through the
relay; the password never leaves either end; a LAN attacker cannot participate; the relay learns
nothing. Over the resulting authenticated channel the Pi sends its root certificate (or, in
public-cert mode, its `<id>.care-pi.dev` name), which the hosted page offers for install/bookmark.

**Authenticates.** G1 and G2 mutually and strongly, G3 yes. This is the only option that is both
authenticated and screen-/stick-/file-free.

**Costs.** Needs internet on both sides at setup; needs us to run a relay (tiny, stateless, could be
the same service as the DNS/ACME one); PAKE implementation in Go and TypeScript (libraries exist;
still cryptography we own). Discovery ("which Pi is mine?") needs the id → the relay can show
devices registered from the same public IP in the last hour, which is a UX aid but *not* a security
control (CGNAT, and the LAN attacker shares the IP).

**Verdict.** The right design for a later phase; too much to carry in v1. It becomes cheap once the
DNS/ACME service exists, because 80 % of it is the same service.

### 3.7 Option G — Tailscale as the trust channel

In Tailscale mode the Pi obtains a *public* certificate for `<name>.<tailnet>.ts.net` via
`tailscale cert`, and MagicDNS resolves it. Devices logged into the tailnet reach the Pi with a
green lock and no CA. Trust moves to the tailnet login (Google/Microsoft/GitHub identity provider).
Headless `tailscale up` needs an auth key, which the wizard must collect — over the still-untrusted
first contact (so G, like public-cert, *follows* A/B rather than replacing them for the very first
page), unless pre-seeded via D1.

For LAN devices without the Tailscale app, nothing changes; they still need local-CA or public-cert.

### 3.8 Option H — public certificate via DNS-01 (our DNS/ACME service)

**Flow.** With internet at first boot, the Pi registers `<id>.care-pi.dev → <LAN IP>` with our
DNS service and obtains a Let's Encrypt certificate by DNS-01 (acme-dns style: the Pi holds a
per-zone credential, the service answers the `_acme-challenge` TXT). Typically < 60 s. From then on
every device on the LAN gets a green lock at `https://<id>.care-pi.dev` with **no certificate
installation at all**. `http://care.local` becomes a `301` to that name.

**First-contact residue.** G1 is reduced to "is this *my* `<id>`?" — an on-path attacker can
redirect `care.local` to their own legitimately-certified `<other-id>.care-pi.dev`. Mitigations:
derive `<id>` from the Imager hostname plus a short suffix and show it in the wizard; have the
operator bookmark it; use the same-public-IP listing on our site as a visibility aid. Not airtight,
but the attacker's prize drops from "root CA on the device" to "one phished setup password", i.e.
severity level 1.

**Caveats (O5).** Needs internet DNS on every client; a multi-day uplink outage breaks name
resolution (TTL ≈ 1 day helps; IP fallback then hits an interstitial); some routers' DNS-rebinding
protection refuses public names that resolve to private IPs (the wizard can detect this by having
the browser fetch `https://<id>.care-pi.dev/ping` and advise); Let's Encrypt's 50-certificates/week
per registered domain is shared by all clinics until `care-pi.dev` (or a subdomain of it) is on the
Public Suffix List — apply for PSL early.

**Verdict.** The single largest UX win available: it deletes §4.3 from the operator's life. It
should be the **recommended default when the Pi has internet**; local-CA stays the offline default.

---

## 4. Cross-cutting controls (apply whichever options you pick)

### 4.1 Name-constrained root CA

The agent — not Caddy — generates the root key and certificate with the X.509 *Name Constraints*
extension (`permittedDNSDomains`: the configured hostnames, `.local`; `permittedIPRanges`: the Pi's
current LAN address/32; everything else excluded) and hands them to Caddy's `pki` app as the local
CA's root (`pki › certificate_authorities › local › root {cert,key}`). Caddy mints its intermediate
and 12-hour leaves under it as usual. Constraints are honoured by Chrome's verifier (desktop,
Android, ChromeOS), NSS/Firefox, Windows CryptoAPI and Apple's verifier *(verify Apple on current
iOS: SP7)*. Effect: a leaked or stolen care-pi CA can only sign for `.local` names — severity level
3 is off the table for every compromise *after* first contact.

Trade-off: adding a new hostname later may require a new root (re-install on devices) unless the
constraint set was generous (`.local`, `.care-pi.dev` subtree, the Pi's IP). Choose generous.

### 4.2 Claim credential = the Imager password (G2)

Pi OS requires a user to be created at first boot; with our Imager repository entry the
customisation dialog is where that happens, so **a password the operator chose always exists** on
the box (hash in `/etc/shadow`). The setup wizard's first screen asks for *that* password before
anything else (verified via `unix_chkpwd`, root-only, rate-limited, 5 attempts then 15-minute
lockout). The box cannot be claimed by someone who did not flash it. Cost: one field. Also doubles
as the obvious seed for the operator account password.

If an operator flashes without customisation (possible with third-party tools), the agent generates
a random user password and the only claim paths are C (stick), D2 or SSH console — documented.

### 4.3 The certificate-install ceremony (unavoidable in local-CA mode)

| Device | Steps after download | Notes |
|---|---|---|
| iPhone/iPad | Safari download → Settings → "Profile Downloaded" → Install (passcode) → **then** Settings → General → About → Certificate Trust Settings → enable | Two unrelated screens; the #1 support ticket in every product that does this. A `.mobileconfig` with the root inside removes the first half but not the trust toggle. |
| Android | Settings → Security → Encryption & credentials → Install a certificate → CA certificate → "Install anyway" | Path differs per vendor skin. Chrome trusts user CAs; Firefox Android needs `about:config` → `security.enterprise_roots.enabled`. |
| Windows | Double-click → Install Certificate → Current User → "Place all certificates in…" → Trusted Root Certification Authorities → security warning → Yes | Edge/Chrome use it immediately; Firefox since ESR 68 trusts the OS store by default. |
| macOS | Double-click → Keychain Access → login keychain → open cert → Trust → "Always Trust" → password | Safari/Chrome immediate; Firefox via `enterprise_roots` (default on). |
| Linux desktop | Distro-specific (`update-ca-certificates` vs NSS `certutil` per browser) | Operators on Linux can follow a text page. |
| ChromeOS | Settings → Privacy → Manage certificates → Authorities → Import | |

The console must detect the OS/browser and show only the relevant steps with screenshots, verify
success (after the toggle, `https://care.local/console/ok` loads without error → show a green tick),
and offer the `.mobileconfig` on Apple devices.

### 4.4 Enrolling the second, third… device from an already-trusted one

Once *one* device trusts the Pi, that device is an authenticated channel for the next:

- **Screen-to-camera QR.** The console shows a QR encoding `https://care-pi.dev/cert#<base64 DER>`.
  The new phone scans it; the hosted page (trusted origin, static) reads the certificate from the
  URL fragment — which never reaches our server — and offers it for install. Authenticated by the
  screen the operator is looking at. Needs internet on the phone only; without internet the QR can
  instead encode the fingerprint for manual comparison against the interstitial's certificate
  viewer.
- **Share sheet / AirDrop / e-mail** of the `.crt`/`.mobileconfig` from the trusted device.
- Both make "re-open the plaintext page" (Option A for additional devices) unnecessary.

### 4.5 Housekeeping

- Setup window is visible: ACT LED heartbeat pattern while unclaimed; console shows "first set up
  from 192.168.1.23 (Chrome on macOS) at …" afterwards, so a hijacked claim is at least noticed.
- Port 80 is `301`-only after claim in every option.
- No HSTS in local-CA mode (F7). HSTS + preload-eligible headers in public-cert/Tailscale modes.
- The root CA key never leaves the Pi except via D1 seed (operator-initiated) and backups
  (encrypted). Rotation is an explicit console action with a "re-install on all devices" warning.

---

## 5. Comparison

| Option | G1 Pi→op | G2 op→Pi | Plaintext ever? | Needs internet | Needs our service | Extra operator steps | Build cost | Severity if attacked at first contact |
|---|---|---|---|---|---|---|---|---|
| A TOFU-plain | ✗ | with 4.2 | yes (CA file) | no | no | 0 | tiny | 3 |
| B TOFU-TLS interstitial | ✗ (weak continuity) | with 4.2 | no | no | no | 0 (+ scary click) | none | 3 |
| C USB stick | ✓ | with 4.2 | no | no | no | 1 physical | small | 1 |
| D1 pre-seed `user-data` | ✓ | ✓ | no | laptop only, once | static page | 1 file copy at flash time | small | 1 (none, really) |
| D2 boot-partition read-back | ✓ | with 4.2 | no | no | no | power-cycle + re-plug | trivial | 1 |
| E HDMI fingerprint | ✓ if screen | with 4.2 | no | no | no | monitor + adapter | small | 1 |
| F relay + PAKE | ✓✓ | ✓✓ | no | yes, both ends | relay | 0 | large | 1 |
| G Tailscale | ✓ (for tailnet devices) | tailnet login | no | yes | no (SaaS) | app install + login | medium | 1 |
| H public cert DNS-01 | mostly (id confusion) | with 4.2 | no | yes (+ client DNS) | DNS/ACME | 0 | medium (service) | 1 |

"Severity" refers to §1.2. Note the sharp line: everything except A and B keeps a first-contact
attacker at level 1.

---

## 6. Recommendation — **DECIDED (S36)**

Your call: **public-cert mode is primary**; the Pi must still work fully offline; one image, not two.
Resulting layered design:

1. **Public-cert mode (H) is the default** whenever the Pi has internet at first contact. First
   contact there is: `http://care.local` → `301` → `https://<id>.care-pi.rithviknishad.dev`
   (green lock, no CA). The residual "is this my `<id>`?" risk is handled by the Imager-password
   claim (§4.2) and by showing the id prominently for bookmarking.
2. **Offline = local-CA mode** (chosen automatically when no internet is detected, or pre-seeded
   via `user-data` — D1 — at flash time). First contact there is the TLS interstitial (B); **no
   plaintext bootstrap page** (A is dropped, so "HTTPS only" stays literal). Authenticated paths:
   USB-stick export (C) and `user-data` pre-seed (D1) in v1; status tty (E) comes with the IP
   display; D2 documented.
3. **Both modes may run together**: public name primary, `care.local` + local CA as the
   offline fallback, offered in the wizard as "keep Care working when the internet is down". The
   CA install for that fallback is optional and done from an already-trusted device (§4.4 QR).
4. **Non-negotiables regardless of mode**: name-constrained CA (§4.1), Imager-password claim
   (§4.2), no HSTS on local-CA hostnames (F7), port 80 = `301` only after claim.
5. **F (relay + PAKE) deferred** until the DNS/ACME service exists; it then becomes cheap.
6. Tailscale (G) stays strictly opt-in (S37).

Defaults taken for the remaining sub-questions unless you object: **Q-c** constraint set =
`.local`, the Pi's LAN IP(s), `*.care-pi.rithviknishad.dev`, plus the Tailscale name when enabled;
**Q-d** the Imager repository entry requires OS customisation (random-password fallback for other
flashing tools, documented).

### Original recommendation (kept for the record)

A layered default, with the decision reduced to its real core:

1. **Public-cert mode (H) is the recommended path whenever the Pi has internet at setup.** No CA,
   no §4.3, green lock on every device. local-CA remains the offline default. This is a change of
   emphasis from P10 (which had local-CA as the universal default) — please confirm.
2. **Both plaintext (A) and interstitial (B) doors exist** while the box is unclaimed, because F5
   means we do not control which one a device arrives at. Port 80 serves the bootstrap page only
   until claim, then `301`. After claim, new devices are enrolled via §4.4, never via plaintext.
   → *If you would rather have zero plaintext ever, say so and A is dropped; the cost is the scary
   interstitial on the very first device only.*
3. **Imager-password claim (4.2) and name-constrained CA (4.1) are non-negotiable** in my view;
   they are cheap and they are what keeps an A/B TOFU from being level-3 in practice.
4. **USB-stick export (C) and `user-data` pre-seed (D1) ship in v1** as the documented
   "secure setup" for local IT persons; E (status tty with fingerprint) comes free with the IP
   display we want anyway; D2 is documentation only.
5. **F (relay + PAKE) is deferred** to the phase where the DNS/ACME service exists.
6. Tailscale (G) stays as designed in P10/P11: opt-in, after claim.

### Questions back to you

- Q-a: Accept layered default above? In particular (1) public-cert as recommended-when-online, and
  (2) keeping *both* A and B doors pre-claim.
- Q-b: Should the bootstrap page (A) also serve the certificate file over plaintext, or only
  instructions + a link to the HTTPS console (which then serves the file after the interstitial)?
  The former is the full TOFU-plain you described; the latter keeps the CA bytes off plaintext at
  the cost of one interstitial.
- Q-c: `.local` + the Pi's IP + `*.care-pi.dev` as the name-constraint set — or stricter
  (exact hostnames only, accepting re-install on rename)?
- Q-d: For the Imager repository entry, do we *require* OS customisation (so the claim password
  always exists), accepting that users of other flashing tools get the random-password fallback?
