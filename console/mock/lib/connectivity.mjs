// The Phase 6 routes of the mock (docs/api/README.md section 15): the public name, Tailscale, Wi-Fi, and GET /tls.
// Reset options (POST /__mock/reset), scenario ready:
//   connectivity: "fresh" (default; a new device with the name-constrained root) | "migrated" (the root changed:
//                 the one-time warning of the certificate page) | "public" (the public name is on, the certificate
//                 is issued) | "tailscale" (public and Tailscale on)
//   wifi_device: false (a device without Wi-Fi)
//   public_fail: "dns" (the DNS service does not answer: the enrol job fails)
//   tailscale_login: "manual" (the login waits until POST /__mock/tailscale-login) | "auto" (default: 3 polls)
//   wifi_fail: "auth" (the network refuses the passphrase)

import { Problem, sendJson } from "./http.mjs";
import { runningHostJob, startJob } from "./jobs.mjs";
import { ADDRESS, FINGERPRINT, FINGERPRINT_COLON, now } from "./state.mjs";

export const ZONE = "care-pi.rithviknishad.dev";
const PREVIOUS = "9a1d6c0e5a2b9e4d7c8f1a0b2c3d4e5f60718293a4b5c6d7e8f9011223344500";
const TS_NAME = "care.tail1234.ts.net";
const TS_IP = "100.101.102.103";
const WIFI_ADDRESS = "192.168.1.23";
const LOGIN_URL = "https://login.tailscale.com/a/1a2b3c4d5e6f";

export function freshConnectivity(options = {}) {
  const scenario = options.connectivity ?? "fresh";
  const c = {
    scenario,
    caChangedAt: scenario === "migrated" ? "2026-11-01T10:00:00Z" : null,
    public: { enabled: false, clinicId: null, acmeCa: null, cert: null, address: null, updatedAt: null },
    tailscale: { enabled: false, installed: false, state: null, loginUrl: null, hostname: null, polls: 0 },
    wifiDevice: options.wifi_device !== false,
    wifi: null,
    publicFail: options.public_fail ?? null,
    tailscaleLogin: options.tailscale_login ?? "auto",
    wifiFail: options.wifi_fail ?? null,
  };
  if (scenario === "public" || scenario === "tailscale") {
    c.public = {
      enabled: true,
      clinicId: "care-k3x9",
      acmeCa: null,
      cert: "issued",
      address: ADDRESS,
      updatedAt: "2026-11-01T10:00:00Z",
    };
  }
  if (scenario === "tailscale") {
    c.tailscale = { enabled: true, installed: true, state: "Running", loginUrl: null, hostname: TS_NAME, polls: 0 };
  }
  return c;
}

function conn(state) {
  state.conn ??= freshConnectivity();
  return state.conn;
}

export function publicHost(state) {
  const p = conn(state).public;
  return p.enabled && p.clinicId ? `${p.clinicId}.${ZONE}` : null;
}

export function tailscaleHost(state) {
  const t = conn(state).tailscale;
  return t.enabled ? t.hostname : null;
}

// The TLS summary of GET /status.
export function tlsSummary(state) {
  return {
    local_ca: { enabled: true, fingerprint_sha256: FINGERPRINT, not_after: "2036-10-30T10:00:00Z" },
    public: { enabled: conn(state).public.enabled, hostname: publicHost(state) },
    tailscale: { enabled: conn(state).tailscale.enabled, hostname: tailscaleHost(state) },
  };
}

// The Phase 6 members of GET /network.
export function networkExtras(state) {
  const c = conn(state);
  const wifiUp = Boolean(c.wifi);
  const wifi = !c.wifiDevice
    ? null
    : {
        ssid: c.wifi?.ssid ?? null,
        country: c.wifi?.country ?? null,
        failover: c.wifi?.failover ?? false,
        connected: wifiUp,
        device: "wlan0",
        radio: true,
        active_ssid: c.wifi?.ssid ?? null,
        signal: wifiUp ? 67 : null,
        address: wifiUp ? WIFI_ADDRESS : null,
        default_route: wifiUp && !c.wifi.failover,
        profiles: [
          { name: "preconfigured", ssid: "Clinic-Setup", autoconnect: true, active: false, managed: false },
          ...(wifiUp
            ? [{ name: "care-pi-wifi", ssid: c.wifi.ssid, autoconnect: true, active: true, managed: true }]
            : []),
        ],
      };
  const hints = [{ interface: "eth0", mac: "d8:3a:dd:00:11:22", address: ADDRESS }];
  if (wifiUp) hints.push({ interface: "wlan0", mac: "d8:3a:dd:00:11:23", address: WIFI_ADDRESS });
  return {
    default_interface: wifiUp && !c.wifi.failover ? "wlan0" : "eth0",
    dhcp_hints: hints,
    tailscale_address: c.tailscale.state === "Running" ? TS_IP : null,
    wifi,
    addresses: wifiUp ? [ADDRESS, WIFI_ADDRESS] : [ADDRESS],
  };
}

function tlsState(state) {
  const c = conn(state);
  const pub = c.public;
  const host = publicHost(state);
  const ts = c.tailscale;
  return {
    local_ca: {
      enabled: true,
      root: {
        subject: "CN=care-pi care.local Root CA,O=care-pi",
        fingerprint_sha256: FINGERPRINT,
        fingerprint_sha256_colon: FINGERPRINT_COLON,
        not_before: "2026-11-01T10:00:00Z",
        not_after: "2036-10-30T10:00:00Z",
        name_constraints: {
          permitted_dns_domains: [".local", `.${ZONE}`],
          permitted_ip_ranges: ["192.168.1.0/24"],
        },
      },
      hostnames: [`${state.appliedHostname}.local`, ...networkExtras(state).addresses],
      uncovered: [],
      ca_changed_at: c.caChangedAt,
      previous_fingerprint_sha256: c.caChangedAt ? PREVIOUS : null,
      cert_page_url: `https://${ZONE}/cert`,
    },
    public: {
      enabled: pub.enabled,
      clinic_id: pub.clinicId,
      hostname: host,
      acme_ca: pub.acmeCa,
      certificate: host
        ? {
            state: pub.cert,
            not_after: pub.cert === "issued" ? "2027-01-30T10:00:00Z" : null,
            issuer: pub.cert === "issued" ? (pub.acmeCa ? "(STAGING) Ersatz Edamame E1" : "E6") : null,
            staging: Boolean(pub.acmeCa),
          }
        : null,
      dns: {
        address: pub.address,
        updated_at: pub.updatedAt,
        last_error: null,
        last_error_at: null,
        revoked: false,
      },
      primary: pub.cert === "issued",
      health_url: host ? `https://${host}/_care-pi/health` : null,
    },
    tailscale: {
      enabled: ts.enabled,
      hostname: ts.enabled ? ts.hostname : null,
      installed: ts.installed,
      backend_state: ts.installed ? ts.state : null,
      login_url: ts.state === "NeedsLogin" ? ts.loginUrl : null,
      tailnet: ts.state === "Running" ? "clinic@example.com" : null,
      ipv4: ts.state === "Running" ? TS_IP : null,
      https_enabled: ts.state === "Running",
      published: ts.enabled,
    },
  };
}

function busy(state) {
  const running = runningHostJob(state);
  if (running) throw new Problem("job-running", `The job ${running.id} runs.`, { job_id: running.id });
}

function ready(state) {
  if (state.setup.state !== "ready") throw new Problem("setup-incomplete", "Finish the setup first.");
}

function accepted(res, job) {
  sendJson(res, 202, job, { Location: `/api/v1/jobs/${job.id}` });
}

const HINT = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;

function putPublic({ res, state, body }) {
  ready(state);
  if (typeof body.enabled !== "boolean") throw new Problem("invalid-request", "The member enabled is required.");
  const c = conn(state);
  const hint = body.hostname_hint ?? state.file.identity.hostname ?? "care";
  if (body.enabled && !HINT.test(hint)) {
    throw new Problem("invalid-request", "1 value is not valid.", {
      errors: [{ pointer: "/hostname_hint", message: "use a-z, 0-9, and a dash inside", code: "pattern" }],
    });
  }
  if (!body.enabled && !c.public.enabled) throw new Problem("state-conflict", "The public name is off.");
  busy(state);
  const id = `${hint}-k3x9`;
  const steps = body.enabled
    ? [
        [
          "dns-register",
          `dns-register: register ${hint} with the DNS service`,
          { lines: [`the DNS service gave the name ${id}.${ZONE}`] },
        ],
        ["dns-address", "dns-address: send the LAN address to the DNS service"],
        ["write-config", "write-config: tls.public"],
        ["ensure-dirs", "ensure-dirs"],
        ["write-files", "write-files: 6 file(s)"],
        ["set-secrets", "set-secrets: care-pi-acme-dns"],
        ["cutover", "cutover: care.target (backend plugs changed)"],
        ["enable-target", "enable-target: care-start.service care-dns-update.timer"],
        ["wait-certificate", `wait-certificate: wait for the certificate of ${id}.${ZONE}`],
        ["reload-caddy", "reload-caddy: care-caddy.service"],
      ]
    : [
        ["write-config", "write-config: tls.public off"],
        ["write-files", "write-files: 5 file(s)"],
        ["disable-units", "disable-units: care-dns-update.timer"],
        ["cutover", "cutover: care.target (backend plugs changed)"],
        ["dns-delete", `dns-delete: delete the registration ${c.public.clinicId}`],
      ];
  const failDns = body.enabled && c.publicFail === "dns";
  const job = startJob(state, {
    kind: "tls-public-enrol",
    steps: failDns
      ? [
          [
            "dns-register",
            `dns-register: register ${hint} with the DNS service`,
            {
              fail: "the DNS service does not answer: dial tcp: lookup care-pi.rithviknishad.dev: no such host",
              problem: "dns-service-unreachable",
            },
          ],
        ]
      : steps,
    onStep: (s, step, phase) => {
      if (phase !== "done") return;
      if (step.action === "write-config" && body.enabled) {
        c.public = {
          enabled: true,
          clinicId: id,
          acmeCa: body.acme_ca || null,
          cert: "pending",
          address: ADDRESS,
          updatedAt: now(),
        };
        s.file.tls.public = { enabled: true, clinic_id: id, ...(body.acme_ca ? { acme_ca: body.acme_ca } : {}) };
      }
      if (step.action === "wait-certificate") c.public.cert = "issued";
      if (step.action === "write-config" && !body.enabled) {
        c.public = { enabled: false, clinicId: null, acmeCa: null, cert: null, address: null, updatedAt: null };
        s.file.tls.public = { enabled: false, clinic_id: null };
      }
    },
    result: () => ({
      kind: "tls-public-enrol",
      enabled: body.enabled,
      hostname: body.enabled ? `${id}.${ZONE}` : null,
      certificate: body.enabled ? "issued" : "none",
    }),
  });
  accepted(res, job);
}

function putTailscale({ res, state, body }) {
  ready(state);
  if (typeof body.enabled !== "boolean") throw new Problem("invalid-request", "The member enabled is required.");
  const c = conn(state);
  const key = body.auth_key ?? null;
  if (key !== null && !String(key).startsWith("tskey-")) {
    throw new Problem("invalid-request", "1 value is not valid.", {
      errors: [{ pointer: "/auth_key", message: "an auth key of Tailscale starts with tskey-", code: "pattern" }],
    });
  }
  if (!body.enabled && !c.tailscale.enabled) throw new Problem("state-conflict", "Tailscale is off.");
  busy(state);
  const ts = c.tailscale;
  if (!body.enabled) {
    const job = startJob(state, {
      kind: "tls-tailscale-enrol",
      steps: [
        ["write-config", "write-config: tls.tailscale off"],
        ["write-files", "write-files: 3 file(s)"],
        ["reload-firewall", "reload-firewall: care-pi-firewall.service"],
        ["tailscale-down", "tailscale-down: disconnect from the tailnet"],
        ["tailscale-logout", "tailscale-logout: remove the device from the tailnet"],
      ],
      onStep: (s, step, phase) => {
        if (phase === "done" && step.action === "write-config") {
          c.tailscale = { ...ts, enabled: false, hostname: null };
          s.file.tls.tailscale = { enabled: false };
        }
        if (phase === "done" && step.action === "tailscale-logout") c.tailscale.state = "NeedsLogin";
      },
      result: () => ({ kind: "tls-tailscale-enrol", enabled: false, hostname: null, tailnet: null }),
    });
    accepted(res, job);
    return;
  }
  // The login step waits: with a key it ends at once; without one it waits for the login in the browser.
  const loginWait = key ? 1 : c.tailscaleLogin === "manual" ? 400 : 6;
  const job = startJob(state, {
    kind: "tls-tailscale-enrol",
    cancellable: true,
    steps: [
      [
        "tailscale-install",
        "tailscale-install: install tailscale from the repository of Tailscale",
        { lines: ["installed tailscale"] },
      ],
      ["tailscale-up", "tailscale-up: connect to the tailnet"],
      [
        "tailscale-login",
        "tailscale-login: wait for the login",
        {
          lines: Array.from({ length: loginWait }, (_, i) =>
            i === 0 && !key
              ? "open the login URL in Settings > Connectivity, or run: care-pi tls tailscale status"
              : "",
          ).filter(Boolean),
          waitUntil: () => c.tailscale.state === "Running",
          maxTicks: loginWait,
        },
      ],
      ["write-config", "write-config: tls.tailscale"],
      ["write-files", "write-files: 4 file(s)"],
      ["reload-firewall", "reload-firewall: care-pi-firewall.service"],
      ["full-cycle", "full-cycle: care.target (pod changed)"],
    ],
    onStep: (s, step, phase) => {
      if (step.action === "tailscale-install" && phase === "done") ts.installed = true;
      if (step.action === "tailscale-up" && phase === "done") {
        if (key) ts.state = "Running";
        else {
          ts.state = "NeedsLogin";
          ts.loginUrl = LOGIN_URL;
        }
      }
      if (step.action === "tailscale-login" && phase === "done") {
        ts.state = "Running";
        ts.loginUrl = null;
      }
      if (step.action === "write-config" && phase === "done") {
        ts.enabled = true;
        ts.hostname = TS_NAME;
        s.file.tls.tailscale = { enabled: true, hostname: TS_NAME };
      }
    },
    result: () => ({ kind: "tls-tailscale-enrol", enabled: true, hostname: TS_NAME, tailnet: "clinic@example.com" }),
  });
  accepted(res, job);
}

const SSID = /^.{1,32}$/u;

function putWifi({ res, state, body }) {
  ready(state);
  const c = conn(state);
  const errors = [];
  if (typeof body.ssid !== "string" || !SSID.test(body.ssid))
    errors.push({ pointer: "/ssid", message: "use 1 to 32 characters", code: "maxLength" });
  if (typeof body.psk !== "string" || body.psk.length < 8 || body.psk.length > 63)
    errors.push({ pointer: "/psk", message: "use 8 to 63 characters", code: "minLength" });
  if (body.country != null && !/^[A-Z]{2}$/.test(body.country))
    errors.push({ pointer: "/country", message: "use 2 capital letters", code: "pattern" });
  if (typeof body.failover !== "boolean")
    errors.push({ pointer: "/failover", message: "the member failover is required", code: "required" });
  if (errors.length) throw new Problem("invalid-request", `${errors.length} value(s) are not valid.`, { errors });
  if (!c.wifiDevice) throw new Problem("wifi-no-device", "This device has no Wi-Fi interface.");
  busy(state);
  const auth = c.wifiFail === "auth";
  const job = startJob(state, {
    kind: "network-wifi",
    steps: [
      ["wifi-radio", "wifi-radio: nmcli radio wifi on"],
      ["wifi-profile", "wifi-profile: write /etc/NetworkManager/system-connections/care-pi-wifi.nmconnection"],
      [
        "wifi-connect",
        "wifi-connect: nmcli connection up care-pi-wifi",
        auth
          ? {
              fail: `Check the passphrase of ${body.ssid}. the network refused the passphrase: Error: Connection activation failed: Secrets were required, but not provided.`,
              problem: "wifi-auth-failed",
            }
          : {},
      ],
      ["wifi-verify", "wifi-verify: check the routes"],
      ["write-config", "write-config: network.wifi"],
      ["write-files", "write-files: 2 file(s)"],
      ["full-cycle", "full-cycle: care.target (pod changed)"],
    ],
    onStep: (s, step, phase) => {
      if (phase === "done" && step.action === "write-config") {
        c.wifi = { ssid: body.ssid, country: body.country ?? null, failover: body.failover };
        s.file.network = { wifi: { ssid: body.ssid, failover: body.failover, country: body.country ?? null } };
      }
    },
    result: () => ({ kind: "network-wifi", ssid: body.ssid, connected: true, address: WIFI_ADDRESS }),
  });
  accepted(res, job);
}

function deleteWifi({ res, state }) {
  ready(state);
  busy(state);
  const c = conn(state);
  const job = startJob(state, {
    kind: "network-wifi",
    steps: [
      ["wifi-remove", "wifi-remove: delete the connection care-pi-wifi"],
      ["write-config", "write-config: network.wifi off"],
      ["write-files", "write-files: 1 file(s)"],
    ],
    onStep: (s, step, phase) => {
      if (phase === "done" && step.action === "write-config") {
        c.wifi = null;
        delete s.file.network;
      }
    },
    result: () => ({ kind: "network-wifi", ssid: null, connected: false, address: null }),
  });
  accepted(res, job);
}

function scanWifi({ res, state }) {
  const c = conn(state);
  if (!c.wifiDevice) throw new Problem("wifi-no-device", "This device has no Wi-Fi interface.");
  sendJson(res, 200, {
    device: "wlan0",
    networks: [
      { ssid: "Sunrise-Staff", signal: 72, security: "WPA2", channel: 6, in_use: Boolean(c.wifi) },
      { ssid: "Sunrise-Guest", signal: 64, security: "WPA2", channel: 11, in_use: false },
      { ssid: "Pharmacy 2G", signal: 31, security: "WPA1 WPA2", channel: 1, in_use: false },
      { ssid: "Open Cafe", signal: 18, security: "", channel: 1, in_use: false },
    ],
  });
}

export const connectivityRoutes = [
  { method: "GET", path: "/api/v1/tls", handler: ({ res, state }) => sendJson(res, 200, tlsState(state)) },
  { method: "PUT", path: "/api/v1/tls/public", handler: putPublic },
  { method: "PUT", path: "/api/v1/tls/tailscale", reauth: true, handler: putTailscale },
  { method: "PUT", path: "/api/v1/network/wifi", handler: putWifi },
  { method: "DELETE", path: "/api/v1/network/wifi", handler: deleteWifi },
  { method: "GET", path: "/api/v1/network/wifi/scan", handler: scanWifi },
];

// POST /__mock/tailscale-login: the operator logged in with the login URL (tailscale_login: "manual").
export function tailscaleLogin(state) {
  const ts = conn(state).tailscale;
  if (ts.state === "NeedsLogin") ts.state = "Running";
  return { backend_state: ts.state };
}
