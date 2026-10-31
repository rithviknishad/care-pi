// Helpers of the journeys against a real device (playwright.real.config.ts). Read README.md in this directory.
import crypto from "node:crypto";
import fs from "node:fs";
import http from "node:http";
import type net from "node:net";
import path from "node:path";
import tls from "node:tls";
import { expect, type Page, type TestInfo } from "@playwright/test";

const e = process.env;

export const env = {
  baseURL: e.BASE_URL ?? "https://care.local:8443",
  claimCode: e.CLAIM_CODE ?? "",
  consolePassword: e.CONSOLE_PASSWORD ?? "",
  displayName: e.DISPLAY_NAME ?? "Real Test Clinic",
  careUsername: e.CARE_USERNAME ?? "",
  careEmail: e.CARE_EMAIL ?? "",
  carePassword: e.CARE_PASSWORD ?? "",
  caCert: e.CA_CERT ?? "",
  proxy: e.PROXY ?? "",
  shotsDir: e.SHOTS_DIR ?? "test-results/real-shots",
  stateDir: e.STATE_DIR ?? "test-results/real-state",
  // The values that the device must show. Empty: the test does not check the value.
  hostname: e.EXPECT_HOSTNAME ?? "",
  ips: (e.EXPECT_IPS ?? "").split(",").filter(Boolean),
  ram: e.EXPECT_RAM ?? "",
  pageSizeKiB: e.EXPECT_PAGE_KIB ?? "",
  diskKind: e.EXPECT_DISK_KIND ?? "",
  rtc: e.EXPECT_RTC ?? "",
  setupTimeoutMs: Number(e.SETUP_TIMEOUT_MIN ?? 12) * 60_000,
};

export function stateFile(name: string): string {
  return path.resolve(env.stateDir, `${name}.json`);
}

// existingState returns the storage state of an earlier journey, or undefined.
export function existingState(name: string): string | undefined {
  const f = stateFile(name);
  return fs.existsSync(f) ? f : undefined;
}

export async function saveState(page: Page, name: string): Promise<void> {
  fs.mkdirSync(env.stateDir, { recursive: true });
  await page.context().storageState({ path: stateFile(name) });
}

// shot writes <SHOTS_DIR>/<project>-<name>.png (full page).
export async function shot(page: Page, info: TestInfo, name: string): Promise<void> {
  fs.mkdirSync(env.shotsDir, { recursive: true });
  await page.screenshot({ path: path.join(env.shotsDir, `${info.project.name}-${name}.png`), fullPage: true });
}

// note appends a line to <SHOTS_DIR>/<project>-journey.log and prints it.
export function note(info: TestInfo, line: string): void {
  fs.mkdirSync(env.shotsDir, { recursive: true });
  fs.appendFileSync(
    path.join(env.shotsDir, `${info.project.name}-journey.log`),
    `${new Date().toISOString()} ${line}\n`,
  );
  console.log(line);
}

// RFC 4648 base32 (no padding).
function base32(s: string): Buffer {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = 0;
  let value = 0;
  const out: number[] = [];
  for (const c of s.replace(/[\s=]/g, "").toUpperCase()) {
    const i = alphabet.indexOf(c);
    if (i < 0) throw new Error(`not base32: ${c}`);
    value = ((value << 5) | i) & 0xffff;
    bits += 5;
    if (bits >= 8) {
      out.push((value >>> (bits - 8)) & 0xff);
      bits -= 8;
    }
  }
  return Buffer.from(out);
}

// totp is RFC 6238: SHA-1, 6 digits, 30 s.
export function totp(secret: string, at = Date.now()): string {
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(at / 1000 / 30)));
  const mac = crypto.createHmac("sha1", base32(secret)).update(counter).digest();
  const offset = (mac[mac.length - 1] ?? 0) & 0x0f;
  const code = (mac.readUInt32BE(offset) & 0x7fffffff) % 1_000_000;
  return String(code).padStart(6, "0");
}

// nextTotpStep waits for the next 30 s step: the agent refuses a code that it accepted before (no replay).
export async function nextTotpStep(page: Page): Promise<void> {
  await page.waitForTimeout(30_000 - (Date.now() % 30_000) + 1_500);
}

function caX509(): crypto.X509Certificate {
  return new crypto.X509Certificate(fs.readFileSync(env.caCert));
}

// caFingerprint is the SHA-256 fingerprint of CA_CERT as the console shows it: AA:BB:...
export function caFingerprint(): string {
  return caX509().fingerprint256;
}

export function caDer(): Buffer {
  return caX509().raw;
}

// checkServedChain opens a TLS connection to BASE_URL (through PROXY when it is set) and
// verifies the served chain against CA_CERT only, not against the system store.
export async function checkServedChain(): Promise<{ authorized: boolean; error: string; issuer: string }> {
  const u = new URL(env.baseURL);
  const port = Number(u.port || 443);
  const socket = await new Promise<net.Socket | undefined>((resolve, reject) => {
    if (!env.proxy) {
      resolve(undefined);
      return;
    }
    const p = new URL(env.proxy);
    const req = http.request({
      host: p.hostname,
      port: Number(p.port),
      method: "CONNECT",
      path: `${u.hostname}:${port}`,
    });
    req.on("connect", (res, s) => (res.statusCode === 200 ? resolve(s) : reject(new Error(`proxy ${res.statusCode}`))));
    req.on("error", reject);
    req.end();
  });
  return new Promise((resolve, reject) => {
    const c = tls.connect(
      {
        ...(socket ? { socket } : { host: u.hostname, port }),
        servername: /^[\d.]+$/.test(u.hostname) ? undefined : u.hostname,
        ca: fs.readFileSync(env.caCert),
        rejectUnauthorized: false,
      },
      () => {
        const peer = c.getPeerCertificate(true);
        resolve({
          authorized: c.authorized,
          error: c.authorizationError ? String(c.authorizationError) : "",
          issuer: String(peer.issuer?.CN ?? ""),
        });
        c.end();
      },
    );
    c.on("error", reject);
  });
}

export async function pageWidth(page: Page): Promise<{ scroll: number; client: number }> {
  return page.evaluate(() => {
    const el = document.scrollingElement ?? document.documentElement;
    return { scroll: el.scrollWidth, client: el.clientWidth };
  });
}

// cardBoxes gives [x, y, width, height] of each card of the main area, in page coordinates.
export async function cardBoxes(page: Page): Promise<number[][]> {
  return page.locator("main [data-slot=card]").evaluateAll((els) =>
    els.map((el) => {
      const r = el.getBoundingClientRect();
      return [r.x, r.y + window.scrollY, r.width, r.height].map((v) => Math.round(v));
    }),
  );
}

// followSetupJob follows the progress page of the setup job until the hand-off page opens. It records the step
// list each second: the list must only grow at the end (append only). It writes the timeline to
// <SHOTS_DIR>/<project>-progress-timeline[-<tag>].json and a screenshot for each new step.
export async function followSetupJob(page: Page, info: TestInfo, finishAt: number, tag = ""): Promise<number> {
  await expect(page).toHaveURL(/\/setup\/progress$/);
  const timeline: { t: number; steps: { name: string; state: string; hint: string }[] }[] = [];
  const hints = new Set<string>();
  let previous: string[] = [];
  let shots = 0;
  const deadline = finishAt + env.setupTimeoutMs;
  while (Date.now() < deadline) {
    if (/\/setup\/done$/.test(page.url())) break;
    if ((await page.getByTestId("setup-failed").count()) > 0) break;
    const steps = await page.getByTestId("job-step").evaluateAll((els) =>
      els.map((el) => ({
        name: el.querySelector("p")?.textContent ?? "",
        state: el.getAttribute("data-state") ?? "",
        hint: el.querySelectorAll("p")[1]?.textContent ?? "",
      })),
    );
    const names = steps.map((s) => s.name);
    expect(names.slice(0, previous.length), "the step list only appends").toEqual(previous);
    for (const s of steps) if (s.hint.startsWith("Expected:")) hints.add(`${s.name} | ${s.hint}`);
    const last = timeline[timeline.length - 1];
    if (!last || JSON.stringify(last.steps) !== JSON.stringify(steps)) {
      timeline.push({ t: Math.round((Date.now() - finishAt) / 1000), steps });
      if (names.length !== previous.length && shots < 20) {
        shots++;
        await shot(page, info, `13-progress${tag}-${String(shots).padStart(2, "0")}`);
      }
    }
    previous = names;
    await page.waitForTimeout(1000);
  }
  fs.writeFileSync(
    path.join(env.shotsDir, `${info.project.name}-progress-timeline${tag}.json`),
    JSON.stringify(timeline, null, 1),
  );
  const seconds = Math.round((Date.now() - finishAt) / 1000);
  note(info, `progress: ${timeline.length} changes, ${previous.length} steps, hints: ${[...hints].join("; ")}`);
  if ((await page.getByTestId("setup-failed").count()) > 0) {
    await shot(page, info, `13-progress${tag}-failed`);
    throw new Error(`setup failed: ${await page.getByTestId("setup-failed").textContent()}`);
  }
  await expect(page).toHaveURL(/\/setup\/done$/);
  note(info, `finish -> ready (hand-off page): ${seconds} s`);
  expect(hints.size, "the running steps show the expected duration").toBeGreaterThan(0);
  return seconds;
}

// checkHandoffAndDashboard checks the hand-off card, then opens the dashboard: each unit is active.
export async function checkHandoffAndDashboard(page: Page, info: TestInfo): Promise<void> {
  const handoff = page.getByTestId("handoff");
  await expect(page.getByTestId("care-url")).toBeVisible();
  const careUrl = (await page.getByTestId("care-url").textContent()) ?? "";
  note(info, `hand-off URL: ${careUrl}`);
  if (env.hostname) expect(careUrl).toMatch(new RegExp(`^https://${env.hostname}\\.local/`));
  await expect(handoff.getByRole("img", { name: /QR code/ })).toBeVisible();
  await expect(page.getByRole("link", { name: "Install the certificate on this device" })).toBeVisible();
  await expect(handoff).toContainText(env.careUsername);
  await shot(page, info, "14-done");

  await page.getByRole("link", { name: "Go to the dashboard" }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  await expect(page.getByTestId("status-state")).toBeVisible();
  const rows = page.getByTestId("units").locator("tbody tr");
  await expect.poll(() => rows.count()).toBeGreaterThan(5);
  const unitText = await rows.allTextContents();
  note(
    info,
    `dashboard state: ${await page.getByTestId("status-state").textContent()}; units: ${unitText.join(" | ")}`,
  );
  for (const r of unitText) expect(r).toMatch(/active\//);
  for (const r of unitText) expect(r).not.toMatch(/inactive|failed/);
  await shot(page, info, "15-dashboard");
}
