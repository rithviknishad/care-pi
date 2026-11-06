import { describe, expect, it } from "vitest";
import { illustrationNames, illustrations } from "../illustrations";

// The copies for plain pages (public/illustrations). Vite reads them as text (no Node API in the app tsconfig).
const copies = import.meta.glob<string>("/public/illustrations/*.svg", {
  query: "?raw",
  import: "default",
  eager: true,
});

// console/design.md, "Illustrations": 8 SVGs, 12 KB or less each, 2 colours through the --illo-* tokens.
describe("illustrations", () => {
  it.each(illustrationNames)("%s is small, uses the tokens, and has the same copy in public/", (name) => {
    const svg = illustrations[name];
    expect(new TextEncoder().encode(svg).length).toBeLessThanOrEqual(12 * 1024);
    expect(svg).toMatch(/^<svg [^>]*viewBox="0 0 200 160"/);
    expect(svg).toContain("var(--illo-primary");
    expect(svg).toContain("var(--illo-ink");
    // No colour other than the tokens: no hex outside the var() fallbacks.
    expect(svg.replace(/var\(--illo-[a-z]+,#[0-9a-f]{6}\)/g, "")).not.toMatch(/#[0-9a-f]{3,6}\b/i);
    expect(copies[`/public/illustrations/${name}.svg`]).toBe(svg);
  });
});
