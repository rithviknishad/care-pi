// Phase 7 security of the mock: the firewall policy and its rollback timer (docs/console-server.md section 13).
// A config write with apply=true that switches security.firewall.policy to drop starts a 90 s timer. The option
// firewall_rollback_s (reset) makes it shorter for a test. When it ends without a confirmation, the policy goes back
// to accept, in the file too.
import { supportHeader } from "./jobs.mjs";
import { sendText } from "./ops.mjs";
import { Problem, sendJson } from "./http.mjs";
import { now } from "./state.mjs";

export function freshSecurity(options = {}) {
  return {
    live: null,
    pendingUntil: null,
    confirmedAt: null,
    rolledBackAt: null,
    reason: null,
    rollbackMs: Number(options.firewall_rollback_s ?? 90) * 1000,
    unitActive: options.firewall_unit !== "inactive",
  };
}

function sec(state) {
  if (!state.sec) state.sec = freshSecurity();
  return state.sec;
}

function tick(state) {
  const s = sec(state);
  if (s.pendingUntil && Date.now() >= Date.parse(s.pendingUntil)) {
    s.pendingUntil = null;
    s.live = "accept";
    s.rolledBackAt = now();
    s.reason = "timeout";
    state.file = { ...state.file, security: { ...state.file.security, firewall: { policy: "accept" } } };
  }
}

export function policyOf(file) {
  return file.security?.firewall?.policy ?? "accept";
}

// onApply runs at a config write with apply=true (lib/system.mjs).
export function onApply(state, before, after) {
  const s = sec(state);
  if (!s.unitActive) return;
  const prev = s.live ?? "accept";
  const next = policyOf(after);
  if (next === "drop" && prev !== "drop") {
    s.pendingUntil = new Date(Date.now() + s.rollbackMs).toISOString();
    s.confirmedAt = null;
    s.rolledBackAt = null;
    s.reason = null;
  }
  if (next === "accept") s.pendingUntil = null;
  if (policyOf(before) !== next || s.live === null) s.live = next;
}

// S64: the check of security.firewall.allow_ports of the agent (agent/internal/config/validate.go): a port that
// care-pi already accepts gets 422 invalid-config. The JSON Schema rules stay with the agent.
export function allowPortsProblem(file) {
  const ssh = file.security?.ssh?.allow ?? true;
  const ts = Boolean(file.tls?.tailscale?.enabled);
  const errors = [];
  (file.security?.firewall?.allow_ports ?? []).forEach((a, i) => {
    const proto = a.proto ?? "tcp";
    let why = null;
    if (proto === "tcp" && [80, 443, 8443].includes(a.port)) why = "Care and the console";
    else if (proto === "udp" && a.port === 5353) why = "mDNS";
    else if (proto === "tcp" && a.port === 22 && ssh) why = "security.ssh.allow";
    else if (proto === "udp" && a.port === 41641 && ts) why = "the Tailscale mode";
    if (why)
      errors.push({
        pointer: `/security/firewall/allow_ports/${i}/port`,
        message: `${a.port}/${proto} is already allowed by care-pi (${why}); remove this entry`,
        code: "not",
      });
  });
  if (!errors.length) return null;
  return new Problem(
    "invalid-config",
    `${errors.length} value(s) do not match the JSON Schema or the rules of config.yaml.`,
    {
      errors,
    },
  );
}

function view(state) {
  tick(state);
  const s = sec(state);
  return {
    policy: policyOf(state.file),
    live_policy: s.live,
    ssh_allowed: state.file.security?.ssh?.allow ?? true,
    unit_active: s.unitActive,
    pending_until: s.pendingUntil,
    confirmed_at: s.confirmedAt,
    rolled_back_at: s.rolledBackAt,
    rollback_reason: s.reason,
    allow_ports: (state.file.security?.firewall?.allow_ports ?? []).map((a) => ({
      port: a.port,
      proto: a.proto ?? "tcp",
      iface: a.iface ?? null,
      comment: a.comment ?? null,
    })),
  };
}

// GET /doctor (Phase 7): a report with one warning; the reset option doctor: "fail" adds a port conflict.
function doctorReport(state) {
  tick(state);
  const checks = [
    {
      id: "ports",
      title: "Ports 80, 443, 8443",
      status: "ok",
      detail: "no other program listens on a port of the pod",
      fix: null,
    },
    { id: "mdns", title: "mDNS name", status: "ok", detail: "care.local", fix: null },
    { id: "clock", title: "Clock", status: "ok", detail: "NTP synchronized", fix: null },
    { id: "memory", title: "Memory", status: "ok", detail: "812 MiB available of 1987 MiB", fix: null },
    { id: "zram", title: "zram swap", status: "ok", detail: "swap 1987 MiB", fix: null },
    { id: "disk-root", title: "Disk /", status: "ok", detail: "48 GiB free of 58 GiB", fix: null },
    { id: "units", title: "Units of Care", status: "ok", detail: "9 units", fix: null },
    { id: "cert-local", title: "Local root certificate", status: "ok", detail: "valid until 2036-10-30", fix: null },
    {
      id: "backup-usb",
      title: "Backup usb (posix)",
      status: "warn",
      detail: "no snapshot yet",
      fix: "sudo care-pi backup run --destination usb",
    },
    { id: "firewall", title: "Firewall", status: "ok", detail: `policy ${sec(state).live ?? "accept"}`, fix: null },
    { id: "reboot", title: "Reboot", status: "ok", detail: "no reboot is required", fix: null },
  ];
  if (state.doctorFail) {
    checks.unshift({
      id: "ports",
      title: "Ports 80, 443, 8443",
      status: "fail",
      detail: "tailscaled on 100.101.102.103:443",
      fix: "Stop the other program on the port (for tailscaled: tailscale serve reset), then: sudo care-pi apply",
    });
    checks.splice(1, 1);
  }
  const rank = { fail: 3, warn: 2, ok: 1, skip: 0 };
  checks.sort((a, b) => rank[b.status] - rank[a.status]);
  const status = checks.reduce((m, c) => (rank[c.status] > rank[m] ? c.status : m), "ok");
  return { generated_at: now(), status, checks };
}

export const securityRoutes = [
  { method: "GET", path: "/api/v1/doctor", handler: ({ res, state }) => sendJson(res, 200, doctorReport(state)) },
  {
    method: "GET",
    path: "/api/v1/doctor/export",
    handler: ({ res, state }) => {
      const r = doctorReport(state);
      const rows = r.checks.map(
        (c) => `[${c.status.toUpperCase().padEnd(4)}] ${c.title}: ${c.detail}${c.fix ? `\n       fix: ${c.fix}` : ""}`,
      );
      sendText(res, `${supportHeader(state, "doctor")}\n\nStatus: ${r.status}\n\n${rows.join("\n")}\n`);
    },
  },
  { method: "GET", path: "/api/v1/security/firewall", handler: ({ res, state }) => sendJson(res, 200, view(state)) },
  {
    method: "POST",
    path: "/api/v1/security/firewall/confirm",
    handler: ({ res, state }) => {
      tick(state);
      const s = sec(state);
      if (!s.pendingUntil) throw new Problem("state-conflict", "No firewall change waits for a confirmation.");
      s.pendingUntil = null;
      s.confirmedAt = now();
      sendJson(res, 200, view(state));
    },
  },
];
