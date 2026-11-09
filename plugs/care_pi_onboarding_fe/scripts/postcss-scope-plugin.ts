import type { Plugin } from "vite";

/**
 * Scopes this MFE's stylesheet to `.care-pi-onboarding-fe-container` so it can
 * be injected into the care_fe host document without touching host styles.
 *
 * WHY THIS IS A POST-PROCESSING PASS AND NOT `@import` NESTING
 * -----------------------------------------------------------
 * The obvious approach is to nest the import in the CSS source:
 *
 *     .care-pi-onboarding-fe-container { @import "tailwindcss"; ... }
 *
 * Tailwind prefixes every selector it generates with the parent, which is
 * correct for utilities and preflight but *silently fatal* for the theme layer.
 * `tailwindcss/index.css` declares the default palette inside `@theme default`,
 * which compiles to `:root, :host`. Prefixing turns that into:
 *
 *     .care-pi-onboarding-fe-container :root,
 *     .care-pi-onboarding-fe-container :host { --color-white:#fff; ... }
 *
 * `:root` *is* `<html>`, so a descendant combinator asks for an `<html>` nested
 * inside a `<div>` — that matches zero elements in any document, forever.
 * `:host` only matches inside a shadow root, so it matches zero too. Every one
 * of the ~147 base theme variables is therefore dead, `--card: var(--color-white)`
 * resolves to nothing, becomes invalid at computed-value time, and the component
 * renders with no background. Tailwind exposes no knob to retarget the `:root`
 * that `@theme` emits, so the rewrite has to happen after generation.
 *
 * THE TWO RULES
 * -------------
 *   1. `:root` / `:host`  ->  REPLACED by the container class (not prefixed).
 *      This lands the full palette and every careui semantic token *on* the
 *      plugin's root element, where they inherit down through the subtree.
 *   2. Every other selector  ->  PREFIXED with the container class, which is
 *      what the nested-import approach already did correctly.
 *
 * WHAT IS DELIBERATELY LEFT ALONE
 * -------------------------------
 * `@keyframes` / `@property` / `@font-face` children are not selectors —
 * their "selectors" are keyframe offsets (`from`, `50%`) or nothing at all.
 * Prefixing them produces invalid CSS that browsers drop silently. `@property`
 * in particular registers globally *by name*; it is intentionally shared with
 * the host (both repos are on Tailwind v4, so the `--tw-*` registrations are
 * byte-identical and idempotent).
 */

const CONTAINER = ".care-pi-onboarding-fe-container";

/**
 * At-rules whose children are not selectors and must pass through untouched.
 *
 * `@layer` is deliberately NOT in this set: Tailwind emits every rule inside
 * `@layer theme/base/utilities`, so treating it as opaque skips the entire
 * stylesheet. Bare `@layer a, b;` statements have no child rules, so
 * `walkRules` never descends into them anyway.
 */
const OPAQUE_AT_RULES = new Set([
  "keyframes",
  "-webkit-keyframes",
  "property",
  "font-face",
  "counter-style",
  "font-feature-values",
  "font-palette-values",
]);

/**
 * Matches the document-root selectors as whole tokens.
 *
 * `html` and `body` are included alongside `:root` / `:host` because Tailwind's
 * preflight styles them directly (`html, :host { line-height … }`). Prefixing
 * would produce `.container html`, which matches nothing; leaving them bare
 * would restyle the care_fe host document. Collapsing them onto the container
 * keeps preflight's intent inside the plugin boundary.
 */
const ROOT_LIKE = /^(?::root|:host(?:\([^)]*\))?|html|body)$/;

/**
 * Theme-mode selectors (`.dark`, `.high-contrast`, `[data-theme=…]`, `:not(…)`).
 * careui writes them bare because it expects the mode class on `<html>`. The host
 * sets it on an ANCESTOR of the plugin root, so prefixing them gives a descendant
 * selector that never matches. Emit both the ancestor form and the compound form:
 *
 *     .dark  ->  .dark .care-pi-onboarding-fe-container, .care-pi-onboarding-fe-container.dark
 */
const THEME_STATE =
  /^(?:\.dark|\.high-contrast|\[data-theme=[^\]]*\]|:not\([^)]*\))+$/;

function scopeSelector(selector: string): string {
  const parts = selector
    .split(",")
    .map((part) => {
      const trimmed = part.trim();
      if (!trimmed) return trimmed;

      // Rule 1: `:root` / `:host` become the container itself.
      if (ROOT_LIKE.test(trimmed)) return CONTAINER;

      // Rule 1b: theme-mode selectors scope onto the ancestor side.
      if (THEME_STATE.test(trimmed)) {
        return `${trimmed} ${CONTAINER}, ${CONTAINER}${trimmed}`;
      }

      // Already scoped (e.g. the retargeted @layer base rule in index.css).
      if (trimmed === CONTAINER || trimmed.startsWith(`${CONTAINER} `)) {
        return trimmed;
      }

      // `:root.dark` / `:host.foo` -> compound on the container, not a descendant.
      const compound = trimmed.match(/^(?::root|:host(?:\([^)]*\))?)(\S+)$/);
      if (compound) return `${CONTAINER}${compound[1]}`;

      // Rule 2: everything else is prefixed.
      return `${CONTAINER} ${trimmed}`;
    })
    .filter(Boolean);

  // `html, :host` and `:root, :host` both collapse onto the container, which
  // would otherwise emit the same selector twice in one rule.
  return [...new Set(parts)].join(", ");
}

/**
 * PostCSS plugin. Declared with `postcssPlugin` + `Once` so it runs a single
 * walk after Tailwind has generated the full stylesheet.
 */
const scopePlugin = {
  postcssPlugin: "care-pi-onboarding-scope",
  Once(root: any) {
    root.walkRules((rule: any) => {
      // Skip rules nested inside @keyframes and friends.
      let parent = rule.parent;
      while (parent) {
        if (
          parent.type === "atrule" &&
          OPAQUE_AT_RULES.has(String(parent.name).toLowerCase())
        ) {
          return;
        }
        parent = parent.parent;
      }
      rule.selector = scopeSelector(rule.selector);
    });
  },
};

export default scopePlugin;

/**
 * Vite plugin wrapper: applies the scoping to the built CSS bundle.
 *
 * This runs in `generateBundle` rather than as a PostCSS plugin in
 * `css.postcss`, because `@tailwindcss/vite` generates its stylesheet in a
 * later internal step than the per-file PostCSS pipeline — a plugin registered
 * there never sees the generated theme/utility layers, only the authored source.
 */
export function scopeTailwindOutput(): Plugin {
  return {
    name: "care-pi-onboarding-scope-css",
    enforce: "post",
    async generateBundle(_options, bundle) {
      const postcss = (await import("postcss")).default;
      for (const [fileName, asset] of Object.entries(bundle)) {
        if (!fileName.endsWith(".css") || asset.type !== "asset") continue;
        const source = String(asset.source);
        const result = await postcss([scopePlugin]).process(source, {
          from: fileName,
        });
        asset.source = result.css;
      }
    },
  };
}
