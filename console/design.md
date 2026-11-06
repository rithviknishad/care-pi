# The console v2 design system (S66)

This file is the specification of the look and the behaviour of the console v2. The pages follow it.
It is for the persons who build or change a page of the console. The rules of `docs/console-ui.md` stay in force
(stable layout, careui, Simplified Technical English); this file adds the v2 layer. When the 2 files do not agree, this file
is correct for the look, and `docs/console-ui.md` is correct for the API and the server.

| Item                                                                     | Where                                                                                     |
| ------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------- |
| Tokens (colours, gradient, shadow, sizes)                                | `src/styles/tokens.css`                                                                   |
| Type (Geist, Geist Mono, the scale)                                      | `src/styles/typography.css`, fonts in `src/styles/fonts/`, licence `public/fonts/OFL.txt` |
| Motion (durations, animations, reduced motion, view transitions, toasts) | `src/styles/motion.css`                                                                   |
| Components                                                               | `src/components/v2/` (import from `@/components/v2/<file>` or `@/components/v2`)          |
| Default texts of the components                                          | `src/components/v2/strings.ts` (`v2Strings`)                                              |
| Illustrations                                                            | `src/components/v2/illustrations/*.svg` (source), copies in `public/illustrations/`       |
| The gallery (each component with sample data, light and dark)            | `npm run dev`, then `http://localhost:5173/__v2`                                          |
| Checks (axe, stability, reduced motion, fonts, contrast)                 | `npm run test:v2` (`src/components/v2/__e2e__/`)                                          |
| Unit tests                                                               | `src/components/v2/__tests__/` (`npm test`)                                               |

The app shell of each page already uses v2 (`src/app/layout/`). The pages keep their v1 content until they move to the
components of this file.

## 1. Who we design for

The operator is a doctor or a clinic assistant who installs the device without help. The operator is not technical,
is busy, and is often not sure that the device works. The console must make the operator calm and sure:

- The operator always knows what happens now, what comes next, and how long it takes.
- The operator never sees a word that only an engineer knows (no "unit", "lock", "digest", "cutover", "ETag").
- When something goes wrong, the console says what happened, that the data is safe (when it is), and what to do.
  It never says that the operator made the mistake.

## 2. Principles

1. **Calm.** Soft surfaces with a hint of green, much space, one accent colour. No red unless something is wrong.
   No loud motion. No sound.
2. **One thing per screen.** A page has one main task and one primary button. Other actions are secondary or in a menu.
3. **Progress is always visible.** A flow shows "Step 3 of 8" and a progress ring. A job shows each step, its time, and
   "Usually about 4 min".
4. **Plain words.** Simplified Technical English (ASD-STE100), short sentences, the words of a clinic (section 11).
5. **Never blame the user.** "We could not reach the backup disk", not "You entered a wrong address".
6. **Stable layout.** Nothing moves when data comes. Each value has a box of its final size before it comes
   (`docs/console-ui.md` section 5, and section 12 here).
7. **Data looks like data.** Ids, hashes, addresses, times, sizes, logs, and keys are in Geist Mono. Words are in Geist.
8. **Motion explains.** Motion shows a change of state (a step starts, a copy is saved, a check passes). It is short, it
   never moves the layout, and it stops for "reduce motion".
9. **For each person.** WCAG 2.1 AA contrast in both themes, full keyboard use, a visible focus ring, 44 px touch targets,
   and phones from 360 px.

## 3. Surfaces: choose the layout first

Each page is one of these surfaces. Choose the surface, then the layout (section 8), then the components.

| Surface                         | Pages                                                                                       | Layout                    | What dominates                                   |
| ------------------------------- | ------------------------------------------------------------------------------------------- | ------------------------- | ------------------------------------------------ |
| Configure (a flow)              | `/setup/*`, `/setup/restore`                                                                | Stage                     | The one task, the progress, "what happens next"  |
| Focus (one task, no navigation) | `/claim`, `/login`, `/recover`, `/certificate` without a session                            | Focus                     | One card on the gradient band                    |
| Monitor (the state)             | `/dashboard`, `/setup/progress`, the Jobs page (S67)                                        | AppShell + Dashboard grid | A compact status hero, fixed-size cards          |
| Operate (actions on things)     | `/backups`, `/restore`, `/updates`, `/plugs`, `/settings`, `/security`, `/support`, `/logs` | AppShell + Panels         | Panels with one action each, "what happens next" |

A hero with a large illustration is correct only on a landing moment (the done page, an empty first state). The dashboard
uses the compact hero: one sentence of state, not marketing.

## 4. Palette

careui stays the base (S66: tuned, not replaced). The primary scale is careui's emerald (`--primary-50` to `--primary-950`).
`src/styles/tokens.css` sets the careui semantic tokens (`--background`, `--card`, `--foreground`, `--muted-foreground`,
`--border`, `--input`, `--muted`, `--accent`, `--sidebar*`) from the v2 tokens below, so each careui component gets the v2
look. The careui high-contrast and colour-blind themes have a higher specificity and still win.

Each colour mix is in `oklab`. A mix in `oklch` turns the hue (7 % red on a green-tinted card gives green).

### 4.1 Surfaces and text

| Token (Tailwind)                         | Light                               | Dark                               | Use                                             |
| ---------------------------------------- | ----------------------------------- | ---------------------------------- | ----------------------------------------------- |
| `--surface-page` (`bg-surface-page`)     | `oklch(0.982 0.007 166)` mint paper | `oklch(0.17 0.013 170)` deep green | The page                                        |
| `--surface-card` (`bg-surface-card`)     | 2 % primary-500 on white            | `oklch(0.212 0.016 168)`           | Cards, panels, dialogs                          |
| `--surface-tint` (`bg-surface-tint`)     | 5 % primary on the card             | 7 % primary-400 on the card        | A section in a card, hover, the selected choice |
| `--surface-tint-strong`                  | 10 % primary on the card            | 14 % primary-400 on the card       | Icon tiles, chips                               |
| `--surface-sunken` (`bg-surface-sunken`) | `oklch(0.966 0.009 166)`            | `oklch(0.155 0.012 170)`           | Mono blocks, logs                               |
| `--ink` (`text-ink`)                     | `oklch(0.195 0.02 170)`             | `oklch(0.965 0.007 166)`           | Text, titles                                    |
| `--ink-muted` (`text-ink-muted`)         | `oklch(0.4 0.02 168)`               | `oklch(0.86 0.012 166)`            | Descriptions, labels of controls                |
| `--ink-soft` (`text-ink-soft`)           | `oklch(0.47 0.018 168)`             | `oklch(0.77 0.014 166)`            | Labels of data, hints, times                    |
| `--line`, `--line-soft`, `--line-strong` | green-grey lines                    | green-grey lines                   | Borders and dividers                            |
| `--line-input` (careui `--input`)        | `oklch(0.62 0.018 168)`             | `oklch(0.55 0.02 168)`             | Borders of inputs (3:1 or more)                 |
| `--primary-ink` (`text-primary-ink`)     | primary-800                         | primary-300                        | Text in the primary colour ("Step 3 of 8")      |
| `--primary-solid` (`bg-primary-solid`)   | primary-600                         | primary-400                        | Icons, bars, rings, check marks                 |
| `--primary-track`                        | 16 % primary on the card            | 18 % primary-400 on the card       | The track of a bar or a ring                    |
| `--nav-active-bg`, `--nav-active-fg`     | 13 % primary, primary-900           | 15 % primary-400, primary-200      | The active place in the navigation              |

The dark theme is deep green-tinted neutrals, never pure black. The primary button stays careui's (`--primary`:
primary-800 in light, primary-400 in dark).

### 4.2 Semantic tones

Each tone has 4 tokens: `fg` (text), `bg` (surface), `border`, `solid` (icon, dot, bar). Use them through `Tone`
(`ok`, `warn`, `danger`, `info`, `neutral`, `primary`; `src/components/v2/tone.ts`). Colour is never the only signal:
a chip, a card, or a step also says the state in words.

| Tone    | Light fg on bg          | Dark fg on bg           | Solid (light / dark)  |
| ------- | ----------------------- | ----------------------- | --------------------- |
| ok      | green-800 on 9 % green  | green-300 on 13 % green | green-600 / green-400 |
| warn    | amber-900 on 14 % amber | amber-200 on 12 % amber | amber-600 / amber-400 |
| danger  | red-800 on 7 % red      | red-200 on 14 % red     | red-600 / red-400     |
| info    | sky-900 on 9 % sky      | sky-200 on 13 % sky     | sky-600 / sky-400     |
| neutral | ink-muted on 5 % ink    | ink-muted on 7 % ink    | grey-green            |

The info tone is sky, not indigo or violet. The focus ring stays careui's indigo (`--ring`): it is a function, not
decoration.

### 4.3 The gradient

`--gradient-hero` = a soft radial highlight (white 20 %, at the top right) over `linear-gradient(135deg, primary-500,
primary-700)`. Dark: primary-600 to primary-900, highlight 10 %. Utilities: `bg-gradient-hero`, `bg-gradient-rail`,
`bg-scrim`, `bg-scrim-full`, `on-gradient`.

Use the gradient only on:

- the `Hero` (landing, done page, the compact status band of the dashboard),
- the band of the `Focus` layout,
- the header of the `Stage` rail.

Never put body text on the gradient without a scrim. White on primary-500 is 2.5:1. The scrims make each point 4.5:1 or
more: `bg-scrim` (left to right: 60 %, 44 % at 62 %, 0 % of primary-950) under the text of a wide hero, and `bg-scrim-full`
(50 % of primary-950) on phones and under small labels (the pills of the Focus bar). `on-gradient` sets white text
(`text-on-gradient`, `text-on-gradient-muted`: white 90 %) and turns the illustrations into white line art.

`--gradient-rail` is a quiet tint (9 % to 3 % primary, top to bottom) for the Stage rail under its header.

### 4.4 Contrast (measured)

`npm run test:v2` (test "token contrast") resolves each token in the browser and composites the layers on a canvas. Each
text pair must be 4.5:1 or more; each icon, bar, and input border 3:1 or more. Results of the last run:

| Pair                                                                        | Light                                 | Dark                                  |
| --------------------------------------------------------------------------- | ------------------------------------- | ------------------------------------- |
| ink on page / card / tint / tint-strong / sunken                            | 17.36 / 17.92 / 17.24 / 16.55 / 16.55 | 17.29 / 15.91 / 14.31 / 12.64 / 17.66 |
| ink-muted on page / card / tint / tint-strong                               | 8.71 / 8.99 / 8.66 / 8.31             | 12.57 / 11.57 / 10.41 / 9.20          |
| ink-soft on page / card / tint / sunken                                     | 6.47 / 6.68 / 6.43 / 6.17             | 9.25 / 8.51 / 7.65 / 9.45             |
| primary-ink on card / tint / tint-strong                                    | 7.50 / 7.21 / 6.92                    | 11.55 / 10.38 / 9.18                  |
| nav-active-fg on nav-active-bg                                              | 8.27                                  | 11.96                                 |
| ok / warn / danger / info / neutral fg on bg                                | 6.60 / 8.32 / 7.62 / 8.60 / 8.01      | 10.26 / 11.43 / 10.41 / 11.13 / 9.99  |
| ink on danger / warn / ok / info bg                                         | 16.59 / 16.71 / 16.83 / 16.51         | 13.69 / 12.89 / 13.01 / 13.41         |
| primary-foreground on primary (the button)                                  | 7.23                                  | 7.83                                  |
| white on the hero: text start (scrim 60 %) / text end at 60 % (scrim 44 %)  | 7.03 / 5.78                           | 8.53 / 7.58                           |
| white on a phone band (scrim-full): top right / top left                    | 6.26 / 5.82                           | 8.55 / 7.42                           |
| on-gradient-muted (white 90 %): hero start / end / phone right / phone left | 6.04 / 5.00 / 5.44 / 5.03             | 7.23 / 6.47 / 7.26 / 6.33             |
| UI: line-input on card / page                                               | 3.55 / 3.44                           | 3.68 / 4.00                           |
| UI: primary-solid on card / on primary-track                                | 3.60 / 3.16                           | 9.07 / 6.61                           |
| UI: ok / warn / danger / info solid on card                                 | 3.17 / 3.15 / 4.70 / 3.96             | 9.88 / 10.20 / 6.08 / 8.07            |

The ink is `oklch(0.195 …)`, not lighter: careui draws some text as ink at 60 % (an inactive tab), and 0.195 keeps that
text at 4.5:1 on `--muted`.

## 5. Typography

- **Geist** (sans) for headings, body, labels, and buttons. **Geist Mono** for data: ids, hashes, fingerprints, keys,
  addresses, versions, sizes and times in data rows, elapsed clocks, logs, code, and the step counter ("Step 3 of 8").
- The fonts are in `src/styles/fonts/` (latin subset, variable weight 100–900). The build gives them a hash in
  `/assets/`, so they get the long cache of `/assets/*` and pass the "only API, assets, and routes" rule of the e2e tests
  (`docs/console-ui.md` sections 7 and 8). The OFL licence ships as `/fonts/OFL.txt` (`public/fonts/OFL.txt`). Each
  `@font-face` has `font-display: swap`. `index.html` preloads both files. The fallback faces (`Geist Fallback` on
  Arial, `Geist Mono Fallback` on Menlo or Courier New) have the metrics of Geist (`size-adjust`, `ascent-override`,
  `descent-override`), so the swap moves no line.
- Figures are tabular everywhere (`font-variant-numeric: tabular-nums`, a stable width for changing numbers). Data has a
  slashed zero.

| Utility        | Size / line         | Weight, tracking           | Use                                          |
| -------------- | ------------------- | -------------------------- | -------------------------------------------- |
| `type-display` | 36 / 44             | 650, −0.025 em             | The hero of a landing moment (from 640 px)   |
| `type-h1`      | 28 / 36             | 650, −0.02 em              | The title of a page or a step (one per page) |
| `type-h2`      | 22 / 30             | 600, −0.015 em             | A section of a page, the compact hero        |
| `type-h3`      | 18 / 26             | 600, −0.01 em              | The title of a panel or a card               |
| `type-body`    | 16 / 24             | 400                        | Body text, descriptions, step titles         |
| `type-small`   | 14 / 20             | 400                        | Hints, labels of data, "what happens next"   |
| `type-data`    | 13 / 20, Geist Mono | 400, tabular, slashed zero | Data (see above)                             |

Use the `type-*` utilities, not `text-*` sizes of your own. `cn()` (tailwind-merge rules) takes an unknown `text-*` class
for a colour, so `cn("text-h1", "text-ink")` drops the size. `type-*` never collides. The careui components keep their
Tailwind sizes (`text-sm`).

Rules: no text in capitals for emphasis; sentence case for titles and buttons; a line has 75 characters at most
(`max-w-prose`); a title can wrap (`text-balance`), a data value can wrap on a phone and is cut with its full value in
`title` from 640 px.

## 6. Space, radius, and elevation

- **8-point grid.** Use the Tailwind steps 2, 4, 6, 8, 12, 16 (8 to 64 px); 1 and 3 (4 and 12 px) only inside a control.
  Page gutter: `px-gutter` (16 px on a phone, 24 px from 640 px, 32 px from 1024 px). Between panels: 16 or 24 px.
  Inside a panel: 16 px (phone) and 24 px.
- **Radius.** Inputs and buttons 8 px (`rounded-input`, careui `rounded-md`), cards and panels 12 px (`rounded-card`,
  careui `rounded-xl`), stage panels, heroes, and the focus card 16 px (`rounded-stage`), chips round (`rounded-full`).
- **Elevation.** One soft shadow (`shadow-soft`) and the tint. `shadow-lift` only for the focus card and toasts. No heavy
  or coloured shadows. Depth comes from the surface steps (page → card → tint), not from shadows.
- **Touch targets** are 44 px on phones (the careui buttons are 48 px high below 768 px).

## 7. Motion

| Token             | Value                                     | Use                                                                |
| ----------------- | ----------------------------------------- | ------------------------------------------------------------------ |
| `--duration-fast` | 150 ms                                    | Hover, press, a fade, the old view of a stage change               |
| `--duration-base` | 250 ms                                    | The enter of a stage or a panel                                    |
| `--duration-slow` | 400 ms                                    | A success (the check mark pops), a progress tick, a bar that fills |
| `ease-out`        | `cubic-bezier(0.23, 1, 0.32, 1)` (careui) | Each animation                                                     |

| Animation                                                  | Where                                                                                    |
| ---------------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| `animate-v2-enter` (fade and 6 px rise)                    | The task panel of the Stage at each step                                                 |
| `animate-v2-pop`                                           | A step that ends while the page is open; the trust check that passes; the explainer dots |
| `animate-v2-spin`                                          | A running step, a ProgressRing without a known end                                       |
| `animate-v2-slide`                                         | The indeterminate bar at the bottom of a running step                                    |
| `animate-v2-breathe`                                       | The dot of a live state chip ("Setup runs")                                              |
| `animate-v2-ping`, `animate-v2-flow`                       | The retention explainer ("now" pulses, new copies flow to the past)                      |
| Transitions of `stroke-dashoffset` and `transform: scaleX` | ProgressRing and MetricBar (progress ticks)                                              |

Rules:

- Write each animation with the variant `motion-ok:` (for example `motion-ok:animate-v2-pop`). It applies only when the
  system has no "reduce motion" setting and careui's `data-reduced-motion="true"` is not on `<html>`. With reduced motion
  nothing moves; careui's skeleton pulse and spinners stop too (`motion.css`).
- Motion uses `opacity` and `transform` only. It never animates `width`, `height`, `top`, or a margin, so it never moves
  the layout.
- A stage change can use the View Transitions API: give `<Stage viewTransition>` and navigate with
  `<Link viewTransition>` (react-router). The task panel has `view-transition-name: v2-stage`. Without it, the panel has
  the CSS enter animation. Use one of the 2, not both.
- No loop without a reason: a loop shows that work runs (spinner, indeterminate bar, a live dot) and stops when it ends.

## 8. Layouts

### 8.1 AppShell (each page with a session)

`src/components/v2/app-shell.tsx`; the console wires it in `src/app/layout/app-shell.tsx`.

- **Top bar**, sticky, 56 px (64 px from 1024 px): the brand, the device name (Geist Mono, `data-testid="device-name"`,
  a skeleton until it comes), the state chip (`StatusChip` with a fixed width of 10 ch, `data-testid="setup-state"`), the
  **banner slot**, and the actions (theme, account).
- **Banner slot:** 40 px high (`--banner-height`, `h-banner`). From 640 px it is in the middle of the top bar and is always
  there, so the actions never move. Below 640 px the compact banner takes the place of the device name and the state
  (2 lines of 16 px). The maintenance banner wins the slot over the clock banner. A banner is a pill in the warn tone.
- **Rail** from 1024 px: 248 px (`w-rail`), sticky, rows of 40 px, the active place in the nav-active tint. The rail
  footer can show the agent version (Geist Mono).
- **Bottom bar** below 1024 px: 64 px plus the safe area. Up to 5 places. With more than 5 items it shows the items with
  `primary: true` (4 at most) and "More", which opens the whole list in a bottom sheet. Both navigations have the name
  "Main"; only one is visible.
- **Minimal mode** (`navMode="minimal"`): no rail and no bottom bar; a Menu button at the left of the top bar opens the
  navigation. The console uses it before the state is `ready`: the setup wizard has the left column (the Stage).
- Content: `max-w-6xl`, centred, `px-gutter`, 24 px (32 px) from the top; on phones it ends above the bottom bar.

### 8.2 Stage (the setup wizard)

`<Stage steps current railTitle railHint illustration title description back primary next>`.

- From 1024 px: 2 columns. Left (320 px, sticky): the rail panel. Its header is the gradient with the illustration, a
  ProgressRing (percent of the steps done), the title of the flow, and a hint ("About 10 min in total"). Below it, the
  list of steps: done (a check in a primary circle; a link back when it has `href`), now (a ring with a dot, on a white
  row), not started (the number). Right: the task panel (16 px radius): "Step 3 of 8" (mono), the title (h1), one
  sentence, the task, and the footer.
- Below 1024 px: the rail is a band (128 px) with the ring and the title, then a segmented bar (one segment for each step)
  and "Step 3 of 8 · Size". The task panel follows.
- The footer: Back at the left (ghost), the primary button at the right, and **what happens next** under it. On a phone
  the primary button is first and full width, Back is last.
- One task in each step. The presets of the easy setup (S65) are `ChoiceCards` with 1 column in the Stage.

### 8.3 Focus (claim, login, recover, the certificate)

`<FocusBackdrop top={<FocusBar …/>}>` and `<FocusCard title description illustration footer width>`; `<Focus>` is both.

- The page is `--surface-page` with a gradient band at the top (288 px, 384 px from 640 px) that fades into the page
  (`mask-b-from-50%`). A faint network drawing (white, 20 %) is on the band from 640 px.
- The bar on the band: the brand pill (logo, "care-pi", the host in mono) at the left and the theme toggle at the right.
  Each text on the band is on a `bg-scrim-full` pill.
- One card (16 px radius, `shadow-lift`), centred, 480 px wide (`width="md"`: claim, login, recover) or 768 px
  (`width="lg"`: the certificate). The text in the card is left-aligned: only the card is in the centre.
- `src/app/layout/public-layout.tsx` uses the backdrop now. A page of the router brings its own card; other content
  (a problem, "not found") gets the card surface. The certificate page (`wide`) has the band only behind the bar until it
  moves to `FocusCard width="lg"`; then set `band="tall"`.

### 8.4 Dashboard grid (Monitor)

`<DashboardGrid>`: 1 column on a phone, 2 from 640 px, 4 from 1280 px, gap 16 px. Put fixed-height cards in it
(`StatCard`: 128 px). A wide card spans with `sm:col-span-2`. Below the grid: panels in 2 columns from 1024 px
(`grid gap-6 lg:grid-cols-2`). The order of a Monitor page: compact Hero (the state in one sentence) → the 4 stat cards
→ panels (services with MetricBars, this device with a KeyValueList) → the timeline of today.

## 9. Components

Each component is small, typed, and has no text literal: its texts are props, with defaults from `v2Strings`. Each has an
example in `src/components/v2/__examples__/` (shown at `/__v2`) and tests in `src/components/v2/__tests__/`.

| Component                                         | File                                   | What it is                                                                                                        | Stability and access                                                                                                                                |
| ------------------------------------------------- | -------------------------------------- | ----------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------- |
| `AppShell`                                        | `app-shell.tsx`                        | Top bar, rail, bottom bar, banner slot, minimal mode                                                              | Fixed slots; skip link; one visible "Main" navigation                                                                                               |
| `Stage`                                           | `stage.tsx`                            | The wizard layout (8.2)                                                                                           | Fixed rail; `aria-current="step"`; the ring is a `progressbar`                                                                                      |
| `Focus`, `FocusBackdrop`, `FocusCard`, `FocusBar` | `focus.tsx`                            | The focus layout (8.3)                                                                                            | The card has the `main` landmark                                                                                                                    |
| `Hero`                                            | `hero.tsx`                             | The gradient header: eyebrow (mono), title, one sentence, actions, illustration                                   | `lg` min 208/224 px, `compact` min 128 px; the description has 2 lines reserved; skeletons in the same boxes                                        |
| `Panel`, `WhatNext`                               | `panel.tsx`                            | The v2 card (title h2/h3, description, action, footer; `variant="tint"`), and the sentence under a primary button | —                                                                                                                                                   |
| `StatCard`                                        | `stat-card.tsx`                        | One value: label, icon, value (mono), unit, hint or trend (tone)                                                  | 128 px high; skeleton rows of the same size                                                                                                         |
| `MetricBar`                                       | `metric-bar.tsx`                       | Used against a limit (memory against the cap). Amber from 80 %, red from 95 %                                     | `role="meter"` with the text value; the fill is `scaleX`                                                                                            |
| `DashboardGrid`                                   | `dashboard-grid.tsx`                   | The grid of 8.4                                                                                                   | —                                                                                                                                                   |
| `KeyValueList`                                    | `key-value-list.tsx`                   | Labels and values; `mono`, `copy`, `loading` per row                                                              | Label above value below 640 px; rows 64 px / 48 px; `dl`                                                                                            |
| `MonoBlock`                                       | `mono-block.tsx`                       | Data in Geist Mono with Copy in the corner; `mode="pre"` (logs) or `"wrap"` (hashes); `lines` for a fixed height  | The copy button is absolute: it moves no text; a fixed-height block can take focus (scroll by keyboard)                                             |
| `CopyButton`                                      | `copy-button.tsx`                      | Copies a value, or a report with the S68 header (device, agent version, time, page); toast "Copied"               | Both labels share one grid cell: the width never changes                                                                                            |
| `CopyHeaderProvider`                              | `copy-provider.tsx`                    | The header of the reports; the app shell gives the device name                                                    | Set `version` when the page knows the agent version                                                                                                 |
| `ProblemCard`                                     | `problem-card.tsx`                     | A problem: plain title, detail, "what you can do" steps, Retry, Copy (report), "Technical details" disclosure     | `role="alert"` with `announce`; takes the place of the failed content                                                                               |
| `EmptyState`                                      | `empty-state.tsx`                      | Illustration, title, one sentence, one action                                                                     | min 288 px high                                                                                                                                     |
| `ChoiceCards`                                     | `choice-cards.tsx`                     | Big radio cards: icon or illustration, title, one-line fit, mono meta, "Recommended" on the top edge              | Native radios (arrow keys); the selected border is a ring: no size change                                                                           |
| `Explainer`                                       | `explainer.tsx`, `retention.ts`        | The backups retention explainer: SVG timeline, legend, one sentence                                               | One drawing for phones and one for desktops (dots keep their size); fixed heights                                                                   |
| `StepList`                                        | `step-list.tsx`                        | Job steps: state icon, title, "Usually about 4 min", elapsed clock (mono), log disclosure with a report Copy      | Append only; a row that can have 2 lines reserves them (`detail: null`); `reserve` rows before the steps come; the bar of a running row is absolute |
| `StepProgress`                                    | `step-list.tsx`                        | "Step 3 of 12" and a bar of the planned step count; without it "Step 3" and an indeterminate bar                  | `role="progressbar"`; full only when the job succeeded; the line is reserved                                                                        |
| `ProgressRing`                                    | `progress-ring.tsx`                    | A ring with a value (0–1) or without a known end (`null`); `onGradient`                                           | `role="progressbar"`                                                                                                                                |
| `StatusChip`                                      | `status-chip.tsx`                      | A dot and a word in a tone; `live` breathes; `width` in ch                                                        | The label says the state (not only the colour)                                                                                                      |
| `Timeline`                                        | `timeline.tsx`                         | Events in time order: time (mono), a dot or an icon in a tone, title, description                                 | Time above the title on a phone, a column from 640 px                                                                                               |
| `Illustration`                                    | `illustration.tsx`, `illustrations.ts` | An inline SVG by name; `label` makes it an image                                                                  | 5:4 box of a fixed width: reserves its space                                                                                                        |
| `Toaster`                                         | `toast.tsx`                            | sonner in the v2 look (below the top bar)                                                                         | `motion.css` gives the careui Toaster the same look                                                                                                 |
| `ThemeToggle`                                     | `theme-toggle.tsx`                     | System, light, dark                                                                                               | —                                                                                                                                                   |
| `InlineSkeleton`                                  | `inline-skeleton.tsx`                  | A skeleton for a heading or a paragraph (a `<p>` cannot hold the careui div)                                      | —                                                                                                                                                   |

The v2 components are built on careui: `Button`, `Sheet`, `DropdownMenu`, `Skeleton`, `Spinner`, `Tabs`, `Field`,
`Input`, `InputGroup`. No new careui item was necessary: the registry has no step list, ring, or retention diagram, and
the v2 look comes from the tuned tokens. Before you make a new component, look in careui first
(`docs/console-ui.md` section 3.1).

### 9.1 Replace the v1 parts

| v1 (`src/components/app/`, `features/*`) | v2                                                                                                              |
| ---------------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| `PageHeader`                             | A `Hero` (`compact`) on Monitor pages; `PageHeader` keeps the v2 type (`type-h1`) on Operate pages              |
| `SectionCard`                            | `Panel` (`SectionCard` already has the v2 card look)                                                            |
| `ProblemAlert`                           | `ProblemCard`; the text of the problem type is the title; `detail`, `type`, `status`, and ids go to `technical` |
| `CopyButton` (app)                       | It is the v2 `CopyButton` now; use `report` for logs, jobs, problems, and doctor results (S68)                  |
| `ShortId`                                | `KeyValueList` with `mono` and `copy`; a short hash only inside "Technical details"                             |
| `job-progress.tsx`                       | `StepList`, with `expectedSeconds` from the step text and `log` from the job events                             |
| The wizard progress bar and step frame   | `Stage`                                                                                                         |
| The size step questions                  | `ChoiceCards` with the 3 presets (S65)                                                                          |
| The backup destination radios            | `ChoiceCards`, and `Explainer` for the schedule                                                                 |

## 10. Illustrations

- 8 drawings, viewBox 200 × 160, flat line art in 2 colours: the primary (`--illo-primary`) and a neutral ink
  (`--illo-ink`), with a soft primary fill (`--illo-fill`) and the paper colour (`--illo-paper`) to hide lines behind a
  shape. Strokes 2.5 px (details 2 px), round caps and joins. Each file is 12 KB or less (now 1–2 KB).
- Each SVG has a `<style>` block with the classes `illo-i` (ink stroke), `illo-p` (primary stroke), `illo-f` (soft fill),
  `illo-w` (paper fill), `illo-d` (primary fill), `illo-k` (ink fill). The classes read the `--illo-*` tokens, with light
  colours as the fallback for a plain page. In the dark theme the tokens change; in `on-gradient` the drawing is white.
- No other colour, no gradient, no text, no people, no brand logos.

| Name                 | Subject                                                | Use                                                    |
| -------------------- | ------------------------------------------------------ | ------------------------------------------------------ |
| `pi-box`             | The Care box with a heart-beat line and a status light | Claim, dashboard hero, setup rail                      |
| `shield-certificate` | A certificate with a seal and a shield with a check    | The certificate ceremony, security                     |
| `backup-vault`       | A vault with copies behind it and a "copy again" badge | Backups, restore, empty states of backups              |
| `update-arrow`       | An up arrow in a progress ring                         | Updates                                                |
| `clinic`             | A small clinic with a cross sign and a tree            | The done page, the size step, "Care is ready"          |
| `phone-qr`           | A phone with a QR code, a scan line, and a check       | Install on a phone, the second device                  |
| `clock`              | A clock with a 15-minute sector                        | The clock banner page, the schedule, the update window |
| `network`            | The box, a laptop, a phone, and Wi-Fi                  | Connectivity, the focus band                           |

To add one: draw it in `src/components/v2/illustrations/<name>.svg` with the same `<style>` block, add it to
`illustrations.ts`, and copy it to `public/illustrations/` (`cp src/components/v2/illustrations/*.svg public/illustrations/`).
`__tests__/illustrations.test.ts` checks the size, the tokens, and the copy.

## 11. Copy

- **Simplified Technical English** (ASD-STE100): active voice, present tense, short sentences, one instruction in each
  sentence, numerals ("8 recovery codes", "4 min").
- **Plain words.** The words of a clinic: "copy" (not "snapshot"), "the device" or "the Care box" (not "the host"),
  "service" (not "unit"), "update" (not "lock"), "the certificate of this device" (not "the root CA"). Never in the
  main text: hex, sha, digest, ETag, cutover, checkpoint, systemd, podman. They go in "Technical details" only.
- **What happens next.** Under each primary button, one sentence (`WhatNext`): "Next, you save 8 recovery codes.",
  "Care stops for about 4 min. Staff see a maintenance page."
- **Durations in minutes.** "About 4 min", "less than 1 min" (`aboutMinutes()`); an elapsed clock in mono (`03:20`). Never
  seconds above 1 minute in a sentence.
- **Never blame.** "We could not …", "The disk did not answer", not "You …" or "Invalid …". Say first if the data is safe.
- **Buttons say the action** in sentence case: "Claim the device", "Check the trust", "Install now". Not "OK", "Submit".
- **Texts live in `src/lib/strings.ts`.** The default texts of the v2 components are in `src/components/v2/strings.ts`
  (`v2Strings`); `strings.ts` can import them as `strings.v2`. A page gives its texts as props.

| Do                                                                                 | Don't                                |
| ---------------------------------------------------------------------------------- | ------------------------------------ |
| "We save a copy every 15 minutes."                                                 | "Backup interval: 15m (restic)"      |
| "We could not reach the backup disk. Your data is safe on this device."            | "Error 502: destination-unreachable" |
| "Usually about 4 min"                                                              | "ETA 220 s"                          |
| "Turn on care.local here: Settings › General › About › Certificate Trust Settings" | "Enable full trust for the root CA"  |
| "Copied. You can paste it into a message to support."                              | "Copied to clipboard!"               |

## 12. States

| State   | Rule                                                                                                                                      | Component                                                                                               |
| ------- | ----------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------- |
| Empty   | The illustration, one sentence (why it is empty and what the action does), one action                                                     | `EmptyState`                                                                                            |
| Loading | A skeleton of the final size in the final box: the same rows, heights, and widths. Never a spinner in place of content                    | `loading` of `StatCard`, `KeyValueList`, `MetricBar`, `Hero`; `reserve` of `StepList`; `InlineSkeleton` |
| Running | Progress in steps and time: "Step 3 of 8", the running step, "Usually about 4 min", the elapsed clock                                     | `StepList`, `ProgressRing`, `StatusChip live`                                                           |
| Error   | A problem card in the place of the failed content (above it if the content stays): title, detail, steps, Retry, Copy, "Technical details" | `ProblemCard`                                                                                           |
| Success | A check (it pops once), one sentence, and the next action                                                                                 | `StepList` (done), the trust check, `Hero` on the done page                                             |

## 13. Do and don't

| Do                                                                                          | Don't                                                                |
| ------------------------------------------------------------------------------------------- | -------------------------------------------------------------------- |
| Choose the surface first (section 3)                                                        | Put a big hero and 3 feature cards on a Monitor page                 |
| One primary button per screen, with `WhatNext`                                              | 2 primary buttons side by side                                       |
| `type-*` utilities, Geist Mono for data                                                     | Mono for prose, or `text-h1` in `cn()`                               |
| Tokens (`bg-surface-tint`, `text-ink-muted`, `border-line`)                                 | Fixed colours (`bg-gray-100`, `#fff`); a colour without a dark value |
| The gradient only on a hero, the focus band, the stage rail, always with a scrim under text | Gradient behind body text, gradient buttons, gradient borders        |
| Skeletons of the final size; fixed slots for banners and chips                              | A layout that grows when data comes; a spinner in place of a card    |
| `motion-ok:` animations of opacity and transform                                            | Animations of size or position; loops without a reason               |
| A word with each colour (chip, step, card)                                                  | Colour as the only signal                                            |
| Plain words, minutes, "what happens next"                                                   | Hashes, internal names, seconds, blame                               |
| Native controls through careui (radios, buttons, details)                                   | `div` buttons, custom focus that hides the ring                      |
| Illustrations from the set of 8                                                             | Emoji, stock photos, a new colour in a drawing                       |
| Group with the tint and a title                                                             | A coloured left border on cards                                      |

## 14. Verification

| Check                                                                                                                                    | Command                                                          | Last result                                                                                                                          |
| ---------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| Types, lint, unit tests, build                                                                                                           | `npm run typecheck && npm run lint && npm test && npm run build` | Pass (all: 29 files, 186 tests; v2: 7 files, 39 tests)                                                                               |
| axe (WCAG 2.1 A and AA) on each gallery view (components, shell, stage, backups, focus, certificate), light and dark, 1280 px and 390 px | `npm run test:v2`                                                | 0 violations (24 runs)                                                                                                               |
| Stability: the data comes (`window.__v2Arrive()`), each box keeps its position and size; CLS                                             | `npm run test:v2`                                                | Boxes equal; CLS 0.0000 (components and shell, 1280 px and 390 px)                                                                   |
| Reduced motion                                                                                                                           | `npm run test:v2`                                                | 53 running animations normally (33 on a phone), 0 with "reduce motion", 0 with `data-reduced-motion="true"`                          |
| Fonts                                                                                                                                    | `npm run test:v2`                                                | Both preloaded, `font-display: swap`, loaded 8–16 ms after the start, first paint 330–350 ms (text in the fallback at once: no FOIT) |
| Token contrast                                                                                                                           | `npm run test:v2`                                                | Each pair passes (section 4.4)                                                                                                       |
| The e2e tests of the pages with the v2 shell                                                                                             | `npm run test:e2e`                                               | 170 passed, 1 skipped (desktop, iPhone 13 in WebKit, 360 px)                                                                         |

The gallery takes `?view=components|shell|stage|backups|focus|certificate`, `?theme=light|dark`, `?arrive=manual|now`
(the stability check), and `?toolbar=0`.

### 14.1 Screenshots

In the session files of wave 12 (`~/.copilot/session-state/75a6d114-62bd-43b2-a5f7-506bda506d01/files/v2-design-shots/`):

| View                              | Desktop 1280 px                                                                                          | Phone 390 px                                                        |
| --------------------------------- | -------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------- |
| Components                        | `desktop-light-components.png`, `desktop-dark-components.png`                                            | `phone-light-components.png`, `phone-dark-components.png`           |
| AppShell with the dashboard       | `desktop-light-shell-dashboard.png`, `desktop-dark-shell-dashboard.png`                                  | `phone-light-shell-dashboard.png`, `phone-dark-shell-dashboard.png` |
| Banner slot (maintenance)         | `desktop-light-shell-banner.png`                                                                         | `phone-light-shell-banner.png`                                      |
| Stage: size presets               | `desktop-light-stage-size.png`, `desktop-dark-stage-size.png`                                            | `phone-light-stage-size.png`, `phone-dark-stage-size.png`           |
| Stage: backups with the explainer | `desktop-light-stage-backups.png`                                                                        | `phone-light-stage-backups.png`                                     |
| Focus: claim                      | `desktop-light-focus-claim.png`, `desktop-dark-focus-claim.png`                                          | `phone-light-focus-claim.png`, `phone-dark-focus-claim.png`         |
| Certificate ceremony              | `desktop-light-certificate.png`, `desktop-dark-certificate.png`, `desktop-light-certificate-trusted.png` | `phone-light-certificate.png`, `phone-dark-certificate.png`         |
| The app with the v2 shell (mock)  | `app-desktop-login.png`, `app-desktop-dashboard.png`                                                     | `app-phone-login.png`, `app-phone-dashboard.png`                    |

## 15. Page recipes

- **Claim, login, recover** (Focus `md`): the `pi-box` illustration, the title, one sentence, the fields (careui `Field`),
  the primary button full width, `WhatNext`, and the help in the footer. A problem is a `ProblemCard` above the button.
- **Certificate ceremony** (Focus `lg`; `plans/07-first-contact-trust.md` §4.3; the scariest moment): the
  `shield-certificate` illustration and "This is normal and safe" first. The OS tabs, with the detected OS marked
  "This device". Numbered steps (4 at most), each with the exact path of taps as chips
  (`Settings › General › About › Certificate Trust Settings`) and the step that people miss named. The download button with
  `WhatNext`. "Check the trust" in a tinted panel: a ring while it checks, a check that pops and "This browser trusts the
  device" when it passes, a warn `ProblemCard` with the missed step when it fails. "Install on a phone" with the
  `phone-qr` drawing and the QR code. The fingerprint only in "Check the fingerprint (for experts)". Example:
  `__examples__/certificate.example.tsx`.
- **Setup wizard** (Stage): one step per screen; the size step is `ChoiceCards` of the 3 presets (S65) with
  "Recommended" on the preset that fits the RAM; the backups step is `ChoiceCards` of the destinations and the `Explainer`;
  each step has `WhatNext`.
- **Setup progress** (Monitor): a compact `Hero` ("Setup runs. You can close this page."), the `StepList` (with
  `reserve` from the step count, `expectedSeconds`, logs), and on a failure a `ProblemCard` with Retry and the link to the
  log, above the list.
- **Done**: a `Hero lg` with the `clinic` drawing ("Care is ready"), the address of Care with Copy, the QR for phones,
  and the next action "Install the certificate on this device".
- **Dashboard**: 8.4. The services are `MetricBar`s in a panel (names of the services, not unit names); the facts are a
  `KeyValueList`; the day is a `Timeline`.
- **Backups**: a compact `Hero` with the last copy; the destinations as panels; the schedule as the `Explainer`; an
  `EmptyState` with `backup-vault` before the first destination.
- **Updates, plugs, settings**: panels; each change says its effect in `WhatNext` before it runs; the job is a `StepList`.
- **Logs, jobs (S67), support**: `MonoBlock mode="pre"` with a fixed `lines` height and `copy="report"`; a job list is a
  `Timeline`.

## 16. Notes for the pages

- The shell changed for each page: the bottom bar (phones), the minimal mode during setup, a 64 px top bar from 1024 px,
  and the Focus backdrop of the public pages. The test ids `device-name` and `setup-state`, the banners
  (`maintenance-banner`, `clock-banner`), the "Main" navigation, and the account menu are the same.
- The body font is Geist now (it was Geist Mono). Existing texts in `font-mono` stay mono.
- Copy reports need the agent version: give it to `CopyHeaderProvider` (the shell gives the device name only).
- A Playwright check of a page can use the same stability method as `__e2e__/v2.spec.ts`: hold the data, record the
  boxes, release, compare, and add the `layout-shift` entries.
