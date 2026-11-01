// Static pages and their assets.

import type { Env } from "../env";
import { SITE_CSS } from "../pages/assets";
import { CERT_HTML } from "../pages/cert";
import { CERT_JS } from "../pages/cert-client";
import { HOME_JS, homeHtml } from "../pages/home";

// The pages load only same-origin scripts and styles. /cert needs no connect-src: it sends nothing.
const CSP_HOME =
  "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; " +
  "base-uri 'none'; form-action 'none'; frame-ancestors 'none'";
const CSP_CERT =
  "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'none'; " +
  "base-uri 'none'; form-action 'none'; frame-ancestors 'none'";

function page(body: string, csp: string): Response {
  return new Response(body, {
    headers: {
      "content-type": "text/html; charset=utf-8",
      "content-security-policy": csp,
      "cache-control": "public, max-age=300",
      "x-frame-options": "DENY",
    },
  });
}

function asset(body: string, type: string): Response {
  return new Response(body, { headers: { "content-type": type, "cache-control": "public, max-age=300" } });
}

export function home(env: Env): Response {
  return page(homeHtml(env.BASE_DOMAIN), CSP_HOME);
}

export function certPage(): Response {
  return page(CERT_HTML, CSP_CERT);
}

export const STATIC_ASSETS: Record<string, () => Response> = {
  "/static/site.css": () => asset(SITE_CSS, "text/css; charset=utf-8"),
  "/static/home.js": () => asset(HOME_JS, "text/javascript; charset=utf-8"),
  "/static/cert.js": () => asset(CERT_JS, "text/javascript; charset=utf-8"),
  "/robots.txt": () => asset("User-agent: *\nDisallow: /lookup\nDisallow: /v1/\n", "text/plain; charset=utf-8"),
};
