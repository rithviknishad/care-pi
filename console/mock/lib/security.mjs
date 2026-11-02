// Phase 7 security of the mock: the firewall policy and its rollback timer (docs/console-server.md section 13).
// A config write with apply=true that switches security.firewall.policy to drop starts a 90 s timer. The option
// firewall_rollback_s (reset) makes it shorter for a test. When it ends without a confirmation, the policy goes back
// to accept, in the file too.
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
  };
}

export const securityRoutes = [
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
