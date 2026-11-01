// Shared style of the static pages. No external assets.

export const SITE_CSS = `:root {
  color-scheme: light dark;
  --fg: #1d2330;
  --muted: #5b6475;
  --bg: #ffffff;
  --card: #f4f6f9;
  --accent: #0b6e4f;
  --warn: #9a3412;
  --border: #d5dae3;
}
@media (prefers-color-scheme: dark) {
  :root { --fg: #e6e9ef; --muted: #a3acbd; --bg: #12151b; --card: #1b2029; --accent: #4cc38a; --warn: #fb923c; --border: #2c3442; }
}
* { box-sizing: border-box; }
body { margin: 0; font: 16px/1.55 system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; color: var(--fg); background: var(--bg); }
main { max-width: 46rem; margin: 0 auto; padding: 1.5rem 1rem 3rem; }
h1 { font-size: 1.6rem; line-height: 1.25; margin: 0.5rem 0 1rem; }
h2 { font-size: 1.15rem; margin: 2rem 0 0.5rem; }
p, li { max-width: 42rem; }
code, .mono { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 0.92em; overflow-wrap: anywhere; }
.card { background: var(--card); border: 1px solid var(--border); border-radius: 10px; padding: 1rem 1.1rem; margin: 1rem 0; }
.muted { color: var(--muted); }
.warn { color: var(--warn); font-weight: 600; }
.ok { color: var(--accent); font-weight: 600; }
.fingerprint { font-size: 1.05rem; letter-spacing: 0.02em; line-height: 1.7; }
dl { display: grid; grid-template-columns: max-content 1fr; gap: 0.35rem 1rem; margin: 0; }
dt { color: var(--muted); }
dd { margin: 0; overflow-wrap: anywhere; }
button { font: inherit; padding: 0.55rem 1rem; border-radius: 8px; border: 1px solid var(--accent); background: var(--accent); color: #fff; cursor: pointer; margin: 0.25rem 0.5rem 0.25rem 0; }
button.secondary { background: transparent; color: var(--accent); }
button:disabled { opacity: 0.5; cursor: default; }
ul.plain { list-style: none; padding: 0; }
ul.plain li { padding: 0.35rem 0; border-bottom: 1px solid var(--border); }
a { color: var(--accent); }
footer { margin-top: 3rem; font-size: 0.9rem; }
[hidden] { display: none !important; }
`;
