// Session and security routes of the mock: claim, login, recover, reauth, password, codes, TOTP, sessions.

import { createHash, randomBytes } from "node:crypto";
import { Problem, clearCookie, sendEmpty, sendJson, sessionCookie } from "./http.mjs";
import { OS_PASSWORD, TOTP_CODE, normalizeCode, now, randomCode } from "./state.mjs";
import { recompute, setupState } from "./setup.mjs";

const COMMON = new Set(["password1234", "123456789012", "qwertyuiopas", "passwordpassword"]);

function checkPassword(pw, pointer) {
  if (typeof pw !== "string" || pw.length < 12) {
    throw new Problem("weak-password", "Use 12 characters or more.", {
      errors: [{ pointer, message: "use 12 characters or more", code: "minLength" }],
    });
  }
  if (pw.length > 128) {
    throw new Problem("weak-password", "Use 128 characters or fewer.", {
      errors: [{ pointer, message: "use 128 characters or fewer", code: "maxLength" }],
    });
  }
  if (COMMON.has(pw.toLowerCase())) {
    throw new Problem("weak-password", "The password is in the list of common passwords.", {
      errors: [{ pointer, message: "the password is too common", code: "common-password" }],
    });
  }
}

// 5 failed credentials from one address in 15 minutes lock the address for 15 minutes.
function guard(state, address) {
  const f = state.failures.get(address);
  if (f && f.lockedUntil > Date.now()) {
    const s = Math.ceil((f.lockedUntil - Date.now()) / 1000);
    throw new Problem("locked-out", "5 failed attempts from this address in 15 minutes. Try again later.", {
      retry_after_s: s,
    });
  }
}

function fail(state, address, detail) {
  const f = state.failures.get(address) ?? { count: 0, lockedUntil: 0 };
  f.count++;
  if (f.count >= 5) {
    f.lockedUntil = Date.now() + 15 * 60 * 1000;
    f.count = 0;
  }
  state.failures.set(address, f);
  throw new Problem("bad-credentials", detail);
}

function success(state, address) {
  state.failures.delete(address);
}

export function sessionView(state, s) {
  return {
    ref: s.ref,
    method: s.method,
    created_at: s.created_at,
    authenticated_at: s.authenticated_at,
    reauth_until: new Date(Date.parse(s.authenticated_at) + 5 * 60 * 1000).toISOString().replace(/\.\d{3}Z$/, "Z"),
    idle_expires_at: new Date(Date.now() + 2 * 3600 * 1000).toISOString().replace(/\.\d{3}Z$/, "Z"),
    expires_at: new Date(Date.parse(s.created_at) + 24 * 3600 * 1000).toISOString().replace(/\.\d{3}Z$/, "Z"),
    csrf_token: s.csrf,
    totp_enabled: state.totp.enabled,
    recovery_codes_remaining: state.recoveryCodes.length,
    setup_state: state.setup.state,
  };
}

function newSession(state, ctx, method) {
  const id = randomBytes(32).toString("base64url");
  const ref = createHash("sha256").update(id).digest("hex").slice(0, 16);
  const s = {
    id,
    ref,
    method,
    csrf: randomBytes(32).toString("base64url"),
    created_at: now(),
    authenticated_at: now(),
    last_seen_at: now(),
    address: ctx.address,
    user_agent: String(ctx.req.headers["user-agent"] ?? "").slice(0, 256),
  };
  state.sessions.set(id, s);
  if (state.sessions.size > 10) state.sessions.delete(state.sessions.keys().next().value);
  return s;
}

export function reauthRecent(s) {
  return Date.now() - Date.parse(s.authenticated_at) < 5 * 60 * 1000;
}

function makeCodes(state) {
  state.recoveryCodes = Array.from({ length: 8 }, () => randomCode(2, 5));
  state.recoveryGeneratedAt = now();
  return [...state.recoveryCodes];
}

export const authRoutes = [
  {
    method: "GET",
    path: "/api/v1/session",
    handler: ({ res, state, session }) => sendJson(res, 200, sessionView(state, session)),
  },
  {
    method: "DELETE",
    path: "/api/v1/session",
    handler: ({ res, state, session }) => {
      state.sessions.delete(session.id);
      sendEmpty(res, 204, { "Set-Cookie": clearCookie() });
    },
  },
  {
    method: "POST",
    path: "/api/v1/session/claim",
    public: true,
    handler: (ctx) => {
      const { state, body, res } = ctx;
      if (state.claimed) throw new Problem("already-claimed", "Log in with the console password.");
      guard(state, ctx.address);
      if (!state.claimMethods.includes(body.method)) {
        throw new Problem("invalid-request", "The device does not accept this claim method.", {
          errors: [{ pointer: "/method", message: "not accepted", code: "enum" }],
        });
      }
      const ok =
        body.method === "claim-code"
          ? normalizeCode(body.secret) === normalizeCode(state.claimCode)
          : body.secret === OS_PASSWORD;
      if (!ok)
        fail(
          state,
          ctx.address,
          body.method === "claim-code" ? "The claim code is not correct." : "The password is not correct.",
        );
      checkPassword(body.new_password, "/new_password");
      success(state, ctx.address);
      state.claimed = true;
      state.claimMethods = [];
      state.password = body.new_password;
      state.passwordSetAt = now();
      state.claimRecord = {
        claimed_at: now(),
        method: body.method,
        address: ctx.address,
        user_agent: String(ctx.req.headers["user-agent"] ?? "").slice(0, 256),
      };
      state.setup.claimedAt = now();
      state.setup.state = "claimed";
      state.setup.steps.account = { status: "done", updated_at: now(), errors: [], warnings: [] };
      recompute(state);
      const codes = makeCodes(state);
      const s = newSession(state, ctx, "claim");
      sendJson(
        res,
        201,
        { session: sessionView(state, s), recovery_codes: codes },
        { "Set-Cookie": sessionCookie(s.id) },
      );
    },
  },
  {
    method: "POST",
    path: "/api/v1/session/login",
    public: true,
    handler: (ctx) => {
      const { state, body, res } = ctx;
      if (!state.claimed) throw new Problem("not-claimed", "Claim the device first.");
      guard(state, ctx.address);
      if (typeof body.password !== "string" || body.password.length === 0) {
        throw new Problem("invalid-request", "The password is necessary.", {
          errors: [{ pointer: "/password", message: "required", code: "required" }],
        });
      }
      if (body.password !== state.password) fail(state, ctx.address, "The password is not correct.");
      if (state.totp.enabled && !body.totp_code)
        throw new Problem("totp-required", "TOTP is on. Send the password and the 6-digit code.");
      if (state.totp.enabled && body.totp_code !== TOTP_CODE) fail(state, ctx.address, "The TOTP code is not correct.");
      success(state, ctx.address);
      const s = newSession(state, ctx, "password");
      sendJson(res, 200, sessionView(state, s), { "Set-Cookie": sessionCookie(s.id) });
    },
  },
  {
    method: "POST",
    path: "/api/v1/session/recover",
    public: true,
    handler: (ctx) => {
      const { state, body, res } = ctx;
      if (!state.claimed) throw new Problem("not-claimed", "Claim the device first.");
      guard(state, ctx.address);
      const i = state.recoveryCodes.findIndex((c) => normalizeCode(c) === normalizeCode(body.recovery_code ?? ""));
      if (i < 0) fail(state, ctx.address, "The recovery code is not correct.");
      checkPassword(body.new_password, "/new_password");
      success(state, ctx.address);
      state.recoveryCodes.splice(i, 1);
      state.password = body.new_password;
      state.passwordSetAt = now();
      state.totp = { enabled: false, enrolledAt: null, pending: null };
      state.sessions.clear();
      const s = newSession(state, ctx, "recovery-code");
      sendJson(
        res,
        200,
        { session: sessionView(state, s), recovery_codes_remaining: state.recoveryCodes.length },
        { "Set-Cookie": sessionCookie(s.id) },
      );
    },
  },
  {
    method: "POST",
    path: "/api/v1/session/reauth",
    handler: (ctx) => {
      const { state, body, res, session } = ctx;
      guard(state, ctx.address);
      if (body.password !== state.password) fail(state, ctx.address, "The password is not correct.");
      success(state, ctx.address);
      session.authenticated_at = now();
      sendJson(res, 200, sessionView(state, session));
    },
  },
  {
    method: "GET",
    path: "/api/v1/security",
    handler: ({ res, state }) => sendJson(res, 200, securityState(state)),
  },
  {
    method: "PUT",
    path: "/api/v1/security/password",
    handler: (ctx) => {
      const { state, body, res, session } = ctx;
      guard(state, ctx.address);
      if (body.current_password !== state.password) fail(state, ctx.address, "The current password is not correct.");
      checkPassword(body.new_password, "/new_password");
      if (body.new_password === state.password) {
        throw new Problem("weak-password", "Use a password that is different from the current password.", {
          errors: [{ pointer: "/new_password", message: "same as the current password", code: "reused-password" }],
        });
      }
      state.password = body.new_password;
      state.passwordSetAt = now();
      for (const id of [...state.sessions.keys()]) if (id !== session.id) state.sessions.delete(id);
      state.sessions.delete(session.id);
      const s = newSession(state, ctx, session.method);
      sendJson(res, 200, sessionView(state, s), { "Set-Cookie": sessionCookie(s.id) });
    },
  },
  {
    method: "POST",
    path: "/api/v1/security/recovery-codes",
    reauth: true,
    handler: ({ res, state }) =>
      sendJson(res, 200, { codes: makeCodes(state), generated_at: state.recoveryGeneratedAt }),
  },
  {
    method: "POST",
    path: "/api/v1/security/totp",
    reauth: true,
    handler: ({ res, state }) => {
      if (state.totp.enabled) throw new Problem("state-conflict", "TOTP is on. Turn it off first.");
      const secret = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP";
      state.totp.pending = { secret, expires: Date.now() + 10 * 60 * 1000 };
      sendJson(res, 200, {
        secret,
        otpauth_uri: `otpauth://totp/care-pi:${state.appliedHostname}.local?secret=${secret}&issuer=care-pi&algorithm=SHA1&digits=6&period=30`,
        algorithm: "SHA1",
        digits: 6,
        period_s: 30,
        expires_at: new Date(state.totp.pending.expires).toISOString().replace(/\.\d{3}Z$/, "Z"),
      });
    },
  },
  {
    method: "DELETE",
    path: "/api/v1/security/totp",
    reauth: true,
    handler: ({ res, state }) => {
      if (!state.totp.enabled) throw new Problem("state-conflict", "TOTP is off.");
      state.totp = { enabled: false, enrolledAt: null, pending: null };
      sendEmpty(res, 204);
    },
  },
  {
    method: "POST",
    path: "/api/v1/security/totp/verify",
    handler: (ctx) => {
      const { state, body, res } = ctx;
      if (state.totp.enabled || !state.totp.pending || state.totp.pending.expires < Date.now()) {
        throw new Problem("state-conflict", "Start the TOTP enrolment first.");
      }
      if (body.code !== TOTP_CODE) {
        throw new Problem("invalid-request", "The code is not correct.", {
          errors: [{ pointer: "/code", message: "not correct", code: "totp" }],
        });
      }
      state.totp = { enabled: true, enrolledAt: now(), pending: null };
      sendJson(res, 200, securityState(state));
    },
  },
  {
    method: "GET",
    path: "/api/v1/security/sessions",
    handler: ({ res, state, session }) =>
      sendJson(res, 200, {
        items: [...state.sessions.values()].reverse().map((s) => ({
          ref: s.ref,
          current: s.id === session.id,
          method: s.method,
          created_at: s.created_at,
          last_seen_at: s.last_seen_at,
          address: s.address,
          user_agent: s.user_agent,
        })),
      }),
  },
  {
    method: "DELETE",
    path: "/api/v1/security/sessions/:ref",
    handler: ({ res, state, params }) => {
      const entry = [...state.sessions.values()].find((s) => s.ref === params.ref);
      if (!entry) throw new Problem("not-found", `No session has the reference ${params.ref}.`);
      state.sessions.delete(entry.id);
      sendEmpty(res, 204);
    },
  },
];

export function securityState(state) {
  return {
    password: { set_at: state.passwordSetAt },
    recovery_codes: { remaining: state.recoveryCodes.length, generated_at: state.recoveryGeneratedAt },
    totp: { enabled: state.totp.enabled, enrolled_at: state.totp.enrolledAt },
    lockout: { locked_until: null, consecutive_failures: 0 },
    claim: state.claimRecord,
    ssh: null,
  };
}

export { setupState };
