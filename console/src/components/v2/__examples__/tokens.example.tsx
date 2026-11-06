import { cn } from "@/lib/utils";

const SURFACES = [
  ["surface-page", "bg-surface-page"],
  ["surface-card", "bg-surface-card"],
  ["surface-tint", "bg-surface-tint"],
  ["surface-tint-strong", "bg-surface-tint-strong"],
  ["surface-sunken", "bg-surface-sunken"],
];

const TONES = ["ok", "warn", "danger", "info", "neutral"] as const;
const TONE_CLASS: Record<(typeof TONES)[number], string> = {
  ok: "bg-ok-bg text-ok-fg border-ok-border",
  warn: "bg-warn-bg text-warn-fg border-warn-border",
  danger: "bg-danger-bg text-danger-fg border-danger-border",
  info: "bg-info-bg text-info-fg border-info-border",
  neutral: "bg-neutral-bg text-neutral-fg border-neutral-border",
};
const SOLID: Record<(typeof TONES)[number], string> = {
  ok: "bg-ok-solid",
  warn: "bg-warn-solid",
  danger: "bg-danger-solid",
  info: "bg-info-solid",
  neutral: "bg-neutral-solid",
};

const TYPE = [
  ["type-display", "Display 36/44", "Care is ready"],
  ["type-h1", "H1 28/36", "Set up your Care box"],
  ["type-h2", "H2 22/30", "Backups"],
  ["type-h3", "H3 18/26", "Where should backups go?"],
  ["type-body", "Body 16/24", "We save a copy of the clinic data every 15 minutes."],
  ["type-small", "Small 14/20", "This takes about 4 min. You can close this page."],
  ["type-data", "Data: Geist Mono 13/20", "sha256:77fe743e51a6 · 192.168.1.20 · 03:20"],
];

export function TokensExample() {
  return (
    <div className="flex flex-col gap-8">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-5">
        {SURFACES.map(([name, cls]) => (
          <div key={name} className={cn("flex h-24 flex-col justify-end rounded-card border border-line p-3", cls)}>
            <span className="type-data text-ink">{name}</span>
            <span className="type-small text-ink-muted">ink-muted</span>
          </div>
        ))}
      </div>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-5">
        {TONES.map((t) => (
          <div key={t} className={cn("flex h-20 items-center gap-3 rounded-card border p-3", TONE_CLASS[t])}>
            <span className={cn("size-3 rounded-full", SOLID[t])} />
            <span className="type-data">{t}</span>
          </div>
        ))}
      </div>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-[2fr_1fr]">
        <div className="relative isolate flex h-28 items-end overflow-hidden rounded-stage bg-gradient-hero p-4 on-gradient">
          <div aria-hidden="true" className="absolute inset-0 -z-10 bg-scrim" />
          <span className="type-data">gradient-hero + scrim</span>
        </div>
        <div className="flex h-28 items-end rounded-stage border border-line bg-gradient-rail p-4">
          <span className="type-data text-ink">gradient-rail</span>
        </div>
      </div>
      <div className="flex flex-col gap-4">
        {TYPE.map(([cls, name, sample]) => (
          <div key={cls} className="grid gap-1 sm:grid-cols-[12rem_minmax(0,1fr)] sm:items-baseline">
            <span className="type-data text-ink-soft">{name}</span>
            <span className={cn(cls, "text-ink")}>{sample}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
