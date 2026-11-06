import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";

const VIEWS = ["components", "shell", "stage", "backups", "focus", "certificate"];
const THEMES = ["light", "dark"];

async function open(page: Page, view: string, theme: string, arrive = "now") {
  await page.goto(`/__v2?view=${view}&theme=${theme}&arrive=${arrive}`);
  await page.locator("#main").waitFor();
  await page.evaluate(() => document.fonts.ready);
}

// 1. axe: no serious or critical violation of WCAG 2.1 A and AA, in each view and each theme.
for (const view of VIEWS) {
  for (const theme of THEMES) {
    test(`axe: ${view} (${theme})`, async ({ page }) => {
      await open(page, view, theme);
      await page.waitForTimeout(600);
      const result = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
      const bad = result.violations.filter((v) => v.impact === "serious" || v.impact === "critical");
      const report = bad.map((v) => `${v.id} (${v.impact}): ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`);
      console.log(`axe ${view} ${theme}: ${result.violations.length} violations, ${bad.length} serious or critical`);
      expect(report).toEqual([]);
    });
  }
}

// 2. Stability: the data comes (window.__v2Arrive) and no box moves or changes its size; CLS stays below 0.01.
async function boxes(page: Page, selector: string) {
  return page.$$eval(selector, (els) =>
    els.map((el) => {
      const r = el.getBoundingClientRect();
      return [
        el.getAttribute("data-v2-section") ?? el.getAttribute("data-testid") ?? el.tagName,
        Math.round(r.x),
        Math.round(r.y + window.scrollY),
        Math.round(r.width),
        Math.round(r.height),
      ];
    }),
  );
}

for (const [view, selector] of [
  [
    "components",
    "[data-v2-section], [data-testid=kv-panel], [data-testid=stat-example] section, [data-testid=steps] li, [data-testid=hero-compact]",
  ],
  ["shell", "main section, header, aside, [data-testid=device-name], [data-testid=shell-hero]"],
] as const) {
  test(`stability: ${view} keeps each box when the data comes`, async ({ page }) => {
    await page.addInitScript(() => {
      (window as unknown as { __cls: number }).__cls = 0;
      new PerformanceObserver((list) => {
        for (const e of list.getEntries() as unknown as { hadRecentInput: boolean; value: number }[]) {
          if (!e.hadRecentInput) (window as unknown as { __cls: number }).__cls += e.value;
        }
      }).observe({ type: "layout-shift", buffered: true });
    });
    await open(page, view, "light", "manual");
    await page.waitForTimeout(400);
    const before = await boxes(page, selector);
    expect(before.length).toBeGreaterThan(3);
    await page.evaluate(() => window.__v2Arrive?.());
    await page.waitForTimeout(800);
    const after = await boxes(page, selector);
    expect(after).toEqual(before);
    const cls = await page.evaluate(() => (window as unknown as { __cls: number }).__cls);
    console.log(`CLS ${view}: ${cls.toFixed(4)}`);
    expect(cls).toBeLessThan(0.01);
  });
}

// 3. Reduced motion: no animation runs. Without the setting, the same page has animations (the check is real).
test("reduced motion stops each animation", async ({ page }) => {
  const list = () =>
    page.evaluate(() =>
      document
        .getAnimations()
        .filter((a) => a.playState === "running")
        .map((a) => {
          const el = (a.effect as KeyframeEffect | null)?.target;
          const name = (a as CSSAnimation).animationName ?? (a as CSSTransition).transitionProperty;
          return `${name} on ${el?.tagName ?? "?"}.${(el?.getAttribute("class") ?? "").slice(0, 60)}`;
        }),
    );
  const running = async () => {
    const names = await list();
    if (names.length) console.log(names.join("\n"));
    return names.length;
  };
  await open(page, "components", "light");
  await page.waitForTimeout(300);
  const normal = (await list()).length;
  expect(normal).toBeGreaterThan(0);
  await page.emulateMedia({ reducedMotion: "reduce" });
  await open(page, "components", "light");
  await page.waitForTimeout(300);
  const reduced = await running();
  console.log(`running animations: ${normal} normal, ${reduced} with reduced motion`);
  expect(reduced).toBe(0);
  // careui's own switch (data-reduced-motion="true" on <html>, set at the start) has the same effect.
  await page.emulateMedia({ reducedMotion: "no-preference" });
  await page.addInitScript(() => {
    // The init script runs before the parser makes <html>: set the attribute when the element comes.
    new MutationObserver((_, observer) => {
      if (!document.documentElement) return;
      document.documentElement.setAttribute("data-reduced-motion", "true");
      observer.disconnect();
    }).observe(document, { childList: true });
  });
  await open(page, "components", "light");
  await page.waitForTimeout(300);
  expect(await running()).toBe(0);
});

// 4. Fonts: both files are preloaded, each face has font-display: swap (text shows at once in the metric-matched
// fallback: no FOIT), and Geist and Geist Mono are ready soon after the start.
test("fonts load with swap and without a block period", async ({ page }) => {
  await page.goto("/__v2?view=components&theme=light&arrive=now");
  const preloads = await page.$$eval("link[rel=preload][as=font]", (ls) =>
    ls.map((l) => new URL((l as HTMLLinkElement).href).pathname),
  );
  expect(preloads).toEqual([
    "/src/styles/fonts/geist-latin-wght.woff2",
    "/src/styles/fonts/geist-mono-latin-wght.woff2",
  ]);
  const faces = await page.evaluate(async () => {
    await document.fonts.ready;
    return [...document.fonts].map((f) => ({
      family: f.family.replace(/"/g, ""),
      display: f.display,
      status: f.status,
    }));
  });
  for (const family of ["Geist", "Geist Mono"]) {
    const face = faces.find((f) => f.family === family);
    expect(face?.display).toBe("swap");
    expect(face?.status).toBe("loaded");
  }
  expect(faces.every((f) => f.display === "swap" || f.family.endsWith("Fallback"))).toBe(true);
  const timing = await page.evaluate(() => {
    const fcp = performance.getEntriesByName("first-contentful-paint")[0]?.startTime ?? -1;
    const fonts = performance
      .getEntriesByType("resource")
      .filter((e) => e.name.endsWith(".woff2"))
      .map((e) => (e as PerformanceResourceTiming).responseEnd);
    return { fcp, fontsEnd: Math.max(...fonts) };
  });
  console.log(`first contentful paint ${timing.fcp.toFixed(0)} ms, fonts loaded at ${timing.fontsEnd.toFixed(0)} ms`);
  expect(timing.fcp).toBeGreaterThan(0);
  expect(
    await page.evaluate(() => document.fonts.check('16px "Geist"') && document.fonts.check('13px "Geist Mono"')),
  ).toBe(true);
});

// 5. The contrast of the token pairs (WCAG 2.1: 4.5:1 for text, 3:1 for icons, bars, and input borders).
// The colours come from the real CSS: the browser resolves each token, and a canvas composites the layers.
const TEXT: [string, string[]][] = [
  ["--ink", ["--surface-page", "--surface-card", "--surface-tint", "--surface-tint-strong", "--surface-sunken"]],
  ["--ink-muted", ["--surface-page", "--surface-card", "--surface-tint", "--surface-tint-strong", "--surface-sunken"]],
  ["--ink-soft", ["--surface-page", "--surface-card", "--surface-tint", "--surface-sunken"]],
  ["--primary-ink", ["--surface-card", "--surface-tint", "--surface-tint-strong"]],
  ["--nav-active-fg", ["--nav-active-bg"]],
  ["--ok-fg", ["--ok-bg"]],
  ["--warn-fg", ["--warn-bg"]],
  ["--danger-fg", ["--danger-bg"]],
  ["--info-fg", ["--info-bg"]],
  ["--neutral-fg", ["--neutral-bg"]],
  ["--ink", ["--danger-bg", "--warn-bg", "--ok-bg", "--info-bg"]],
  ["--primary-foreground", ["--primary"]],
];
const UI: [string, string[]][] = [
  ["--line-input", ["--surface-card", "--surface-page"]],
  ["--primary-solid", ["--surface-card", "--primary-track"]],
  ["--ok-solid", ["--surface-card"]],
  ["--warn-solid", ["--surface-card"]],
  ["--danger-solid", ["--surface-card"]],
  ["--info-solid", ["--surface-card"]],
];

for (const theme of THEMES) {
  test(`token contrast (${theme})`, async ({ page }) => {
    await open(page, "components", theme);
    const rows = await page.evaluate(
      ({ text, ui }) => {
        const canvas = document.createElement("canvas");
        canvas.width = canvas.height = 1;
        const ctx = canvas.getContext("2d", { willReadFrequently: true })!;
        const probe = document.createElement("div");
        document.body.appendChild(probe);
        const resolve = (v: string) => {
          probe.style.color = v.startsWith("--") ? `var(${v})` : v;
          return getComputedStyle(probe).color;
        };
        // Paints the layers (bottom first) and reads the sRGB pixel.
        const paint = (layers: string[]) => {
          ctx.clearRect(0, 0, 1, 1);
          for (const l of layers) {
            ctx.fillStyle = resolve(l);
            ctx.fillRect(0, 0, 1, 1);
          }
          return [...ctx.getImageData(0, 0, 1, 1).data.slice(0, 3)];
        };
        const lum = (rgb: number[]) => {
          const [r, g, b] = rgb.map((c) => {
            const s = c / 255;
            return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
          });
          return 0.2126 * r + 0.7152 * g + 0.0722 * b;
        };
        const ratio = (fg: number[], bg: number[]) => {
          const [a, b] = [lum(fg), lum(bg)].sort((x, y) => y - x);
          return (a + 0.05) / (b + 0.05);
        };
        const out: { kind: string; fg: string; bg: string; ratio: number }[] = [];
        for (const [kind, pairs] of [
          ["text", text],
          ["ui", ui],
        ] as const) {
          for (const [fg, bgs] of pairs) {
            for (const bg of bgs) {
              const base = paint(["white", bg]);
              const top = paint(["white", bg, fg]);
              out.push({ kind, fg, bg, ratio: ratio(top, base) });
            }
          }
        }
        // White text on the hero gradient with its scrim, at the brightest points (console/design.md, "Gradient").
        const root = getComputedStyle(document.documentElement);
        const p = (n: number) => root.getPropertyValue(`--primary-${n}`).trim();
        const dark = document.documentElement.classList.contains("dark");
        const [g0, g1] = dark ? [p(600), p(900)] : [p(500), p(700)];
        const hi = dark ? 0.1 : 0.2;
        const at = (t: number) => `color-mix(in srgb, ${g1} ${t * 100}%, ${g0})`;
        const scrim = (a: number) => `color-mix(in srgb, ${p(950)} ${a * 100}%, transparent)`;
        const points: [string, string[]][] = [
          ["hero text start (scrim 60 %)", [at(0), scrim(0.6)]],
          ["hero text end, 60 % (scrim 44 %)", [at(0.3), "rgb(255 255 255 / 0.06)", scrim(0.44)]],
          ["phone band, top right (scrim-full)", [at(0.5), `rgb(255 255 255 / ${hi})`, "var(--gradient-scrim-full)"]],
          ["phone band, top left (scrim-full)", [at(0), "var(--gradient-scrim-full)"]],
        ];
        for (const [name, layers] of points) {
          const bg = paint(["white", ...layers]);
          out.push({ kind: "text", fg: "white (on-gradient)", bg: name, ratio: ratio([255, 255, 255], bg) });
          const muted = paint(["white", ...layers, "rgb(255 255 255 / 0.9)"]);
          out.push({ kind: "text", fg: "on-gradient-muted", bg: name, ratio: ratio(muted, bg) });
        }
        probe.remove();
        return out;
      },
      { text: TEXT, ui: UI },
    );
    console.log(
      `contrast (${theme}):\n${rows.map((r) => `  ${r.kind.padEnd(4)} ${r.fg} on ${r.bg}: ${r.ratio.toFixed(2)}`).join("\n")}`,
    );
    const failing = rows.filter((r) => r.ratio < (r.kind === "text" ? 4.5 : 3));
    expect(failing.map((r) => `${r.fg} on ${r.bg}: ${r.ratio.toFixed(2)}`)).toEqual([]);
  });
}
