import type { Config, FirewallState } from "@/api/types";

/** The seconds until the rollback of the policy drop, or null when no timer runs. */
export function secondsLeft(fw: Pick<FirewallState, "pending_until"> | undefined, now: number): number | null {
  if (!fw?.pending_until) return null;
  return Math.max(0, Math.ceil((Date.parse(fw.pending_until) - now) / 1000));
}

/** One entry of security.firewall.allow_ports of config.yaml (S64). */
export type AllowPort = NonNullable<NonNullable<NonNullable<Config["security"]>["firewall"]>["allow_ports"]>[number];

export const MAX_ALLOW_PORTS = 32;
export const COMMENT_MAX = 60;
const IFACE = /^[A-Za-z0-9][A-Za-z0-9._-]{0,14}$/;

/** The protocol of an entry: tcp when the key is not set. */
export const protoOf = (a: Pick<AllowPort, "proto">) => a.proto ?? "tcp";

/** The key of an entry: port, protocol, and interface ("" for each interface). */
export const allowPortKey = (a: AllowPort) => `${a.port}/${protoOf(a)}/${a.iface ?? ""}`;

/**
 * Why the table of care-pi already accepts proto to port, or null. The agent refuses such an entry
 * (agent/internal/config/validate.go, builtinPort); the console checks it first for a clear message.
 */
export function builtinPort(
  port: number,
  proto: "tcp" | "udp",
  ctx: { ssh: boolean; tailscale: boolean },
): "care" | "mdns" | "ssh" | "tailscale" | null {
  if (proto === "tcp" && (port === 80 || port === 443 || port === 8443)) return "care";
  if (proto === "udp" && port === 5353) return "mdns";
  if (proto === "tcp" && port === 22 && ctx.ssh) return "ssh";
  if (proto === "udp" && port === 41641 && ctx.tailscale) return "tailscale";
  return null;
}

export type AllowDraft = { port: string; proto: "tcp" | "udp"; iface: string; comment: string };
export type BuiltinReason = NonNullable<ReturnType<typeof builtinPort>>;
export type DraftErrors = {
  port?: "range" | "builtin" | "duplicate";
  iface?: "pattern";
  comment?: "length";
  list?: "full";
  builtin?: BuiltinReason;
};

/** Checks a new entry against the list. It returns the entry, or the errors (builtin: the reason of builtinPort). */
export function checkDraft(
  d: AllowDraft,
  list: AllowPort[],
  ctx: { ssh: boolean; tailscale: boolean },
): { entry: AllowPort; errors?: undefined } | { entry?: undefined; errors: DraftErrors } {
  const errors: DraftErrors = {};
  const text = d.port.trim();
  const port = /^\d+$/.test(text) ? Number(text) : NaN;
  const iface = d.iface.trim();
  const comment = d.comment.trim();
  if (!Number.isInteger(port) || port < 1 || port > 65535) errors.port = "range";
  if (iface && !IFACE.test(iface)) errors.iface = "pattern";
  if (comment.length > COMMENT_MAX) errors.comment = "length";
  if (list.length >= MAX_ALLOW_PORTS) errors.list = "full";
  const entry: AllowPort = { port, proto: d.proto, ...(iface ? { iface } : {}), ...(comment ? { comment } : {}) };
  if (!errors.port) {
    const why = builtinPort(port, d.proto, ctx);
    if (why) {
      errors.port = "builtin";
      errors.builtin = why;
    } else if (list.some((a) => allowPortKey(a) === allowPortKey(entry))) errors.port = "duplicate";
  }
  return Object.keys(errors).length ? { errors } : { entry };
}
