import { ThemeProvider } from "next-themes";
import type { ComponentType } from "react";
import { MemoryRouter } from "react-router";
import { TooltipProvider } from "@/components/ui/tooltip";
import { CopyHeaderProvider } from "../copy-provider";
import { Toaster } from "../toast";
import { AppShellExample } from "./app-shell.example";
import { ArrivalContext, useArrivalSwitch } from "./arrival";
import { CertificateExample } from "./certificate.example";
import { ChoiceCardsExample } from "./choice-cards.example";
import { CopyExample } from "./copy.example";
import { EmptyStateExample } from "./empty-state.example";
import { ExplainerExample } from "./explainer.example";
import { FocusExample } from "./focus.example";
import { HeroExample } from "./hero.example";
import { IllustrationExample } from "./illustration.example";
import { KeyValueListExample } from "./key-value-list.example";
import { PanelExample } from "./panel.example";
import { ProblemCardExample } from "./problem-card.example";
import { ProgressRingExample } from "./progress-ring.example";
import { device } from "./sample-data";
import { StageExample } from "./stage.example";
import { StatCardExample } from "./stat-card.example";
import { StatusChipExample } from "./status-chip.example";
import { StepListExample } from "./step-list.example";
import { TimelineExample } from "./timeline.example";
import { ToastExample } from "./toast.example";
import { TokensExample } from "./tokens.example";

const SECTIONS: { id: string; title: string; note: string; Example: ComponentType }[] = [
  {
    id: "tokens",
    title: "Tokens",
    note: "Surfaces, semantic tones, gradients, and the type scale (Geist, Geist Mono).",
    Example: TokensExample,
  },
  {
    id: "illustrations",
    title: "Illustration",
    note: "8 line drawings in 2 colours. They follow the theme.",
    Example: IllustrationExample,
  },
  {
    id: "hero",
    title: "Hero",
    note: "Gradient header with a scrim. lg for landing and done pages, compact for status.",
    Example: HeroExample,
  },
  {
    id: "stat",
    title: "StatCard, MetricBar, DashboardGrid",
    note: "Fixed-height cards. The skeletons have the final size.",
    Example: StatCardExample,
  },
  {
    id: "kv",
    title: "KeyValueList",
    note: "Label above the value on a phone, side by side from 640 px.",
    Example: KeyValueListExample,
  },
  {
    id: "steps",
    title: "StepList",
    note: "Append-only job steps with state, time, the usual duration, and the log.",
    Example: StepListExample,
  },
  {
    id: "explainer",
    title: "Explainer (backups retention)",
    note: "An animated timeline and one plain sentence.",
    Example: ExplainerExample,
  },
  {
    id: "choices",
    title: "ChoiceCards",
    note: "Big radio cards for presets and destinations.",
    Example: ChoiceCardsExample,
  },
  {
    id: "problem",
    title: "ProblemCard",
    note: "Plain title, what to do, Retry, Copy, technical details.",
    Example: ProblemCardExample,
  },
  { id: "empty", title: "EmptyState", note: "Illustration, one sentence, one action.", Example: EmptyStateExample },
  {
    id: "copy",
    title: "CopyButton, MonoBlock",
    note: "Data in Geist Mono. A report copy has the support header.",
    Example: CopyExample,
  },
  { id: "timeline", title: "Timeline", note: "Events in time order.", Example: TimelineExample },
  {
    id: "chips",
    title: "StatusChip",
    note: "A dot and a word. Colour is never the only signal.",
    Example: StatusChipExample,
  },
  {
    id: "ring",
    title: "ProgressRing",
    note: "Known progress, or a turning arc without a known end.",
    Example: ProgressRingExample,
  },
  {
    id: "panel",
    title: "Panel, WhatNext",
    note: "The v2 card, the tinted section, and the sentence under a primary button.",
    Example: PanelExample,
  },
  { id: "toast", title: "Toast", note: "sonner in the v2 look.", Example: ToastExample },
];

const VIEWS = ["components", "shell", "stage", "backups", "focus", "certificate"] as const;
type View = (typeof VIEWS)[number];

function link(params: Record<string, string>) {
  const q = new URLSearchParams(window.location.search);
  for (const [k, v] of Object.entries(params)) q.set(k, v);
  return `?${q.toString()}`;
}

function Toolbar({ view, theme }: { view: View; theme: string }) {
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-line bg-surface-card px-gutter py-2 type-small">
      <span className="font-semibold">care-pi v2</span>
      <nav aria-label="Gallery views" className="flex flex-wrap gap-x-3 gap-y-1">
        {VIEWS.map((v) => (
          <a
            key={v}
            href={link({ view: v })}
            aria-current={v === view ? "page" : undefined}
            className="text-ink-muted underline-offset-4 hover:underline aria-[current=page]:font-semibold aria-[current=page]:text-primary-ink"
          >
            {v}
          </a>
        ))}
      </nav>
      <span className="ml-auto flex gap-3">
        {["light", "dark"].map((t) => (
          <a
            key={t}
            href={link({ theme: t })}
            aria-current={t === theme ? "true" : undefined}
            className="text-ink-muted aria-[current=true]:font-semibold aria-[current=true]:text-primary-ink"
          >
            {t}
          </a>
        ))}
      </span>
    </div>
  );
}

function Components() {
  return (
    <main id="main" className="mx-auto flex max-w-6xl flex-col gap-12 px-gutter py-8">
      <header className="flex flex-col gap-2">
        <h1 className="type-h1">The console v2 design system</h1>
        <p className="max-w-prose type-body text-ink-muted">
          Each component of src/components/v2 with sample data. console/design.md is the spec.
        </p>
      </header>
      {SECTIONS.map(({ id, title, note, Example }) => (
        <section key={id} id={id} aria-labelledby={`${id}-title`} className="flex flex-col gap-4" data-v2-section={id}>
          <div>
            <h2 id={`${id}-title`} className="type-h2">
              {title}
            </h2>
            <p className="type-small text-ink-muted">{note}</p>
          </div>
          <Example />
        </section>
      ))}
    </main>
  );
}

function View({ view }: { view: View }) {
  switch (view) {
    case "shell":
      return <AppShellExample />;
    case "stage":
      return <StageExample step="size" />;
    case "backups":
      return <StageExample step="backups" />;
    case "focus":
      return <FocusExample />;
    case "certificate":
      return <CertificateExample />;
    default:
      return <Components />;
  }
}

export function Gallery() {
  const params = new URLSearchParams(window.location.search);
  const view = (VIEWS as readonly string[]).includes(params.get("view") ?? "")
    ? (params.get("view") as View)
    : "components";
  const theme = params.get("theme") === "dark" ? "dark" : "light";
  const toolbar = params.get("toolbar") !== "0";
  const arrived = useArrivalSwitch(params.get("arrive"));
  return (
    <ThemeProvider
      attribute="class"
      forcedTheme={theme}
      storageKey="care-pi.v2-gallery.theme"
      disableTransitionOnChange
    >
      <TooltipProvider>
        <CopyHeaderProvider value={{ device: device.name, version: device.version }}>
          <ArrivalContext.Provider value={arrived}>
            <MemoryRouter initialEntries={[view === "shell" ? "/dashboard" : "/setup"]}>
              {toolbar ? <Toolbar view={view} theme={theme} /> : null}
              <View view={view} />
            </MemoryRouter>
          </ArrivalContext.Provider>
        </CopyHeaderProvider>
      </TooltipProvider>
      <Toaster />
    </ThemeProvider>
  );
}
