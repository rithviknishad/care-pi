// The semantic tones of v2 (console/design.md, "Palette"). Each tone has a text colour (fg), a surface (bg),
// a border, and a solid colour for icons, dots, and bars. Each pair has 4.5:1 or more in both themes.

export type Tone = "ok" | "warn" | "danger" | "info" | "neutral" | "primary";

export const toneText: Record<Tone, string> = {
  ok: "text-ok-fg",
  warn: "text-warn-fg",
  danger: "text-danger-fg",
  info: "text-info-fg",
  neutral: "text-neutral-fg",
  primary: "text-primary-ink",
};

export const toneSurface: Record<Tone, string> = {
  ok: "bg-ok-bg text-ok-fg border-ok-border",
  warn: "bg-warn-bg text-warn-fg border-warn-border",
  danger: "bg-danger-bg text-danger-fg border-danger-border",
  info: "bg-info-bg text-info-fg border-info-border",
  neutral: "bg-neutral-bg text-neutral-fg border-neutral-border",
  primary: "bg-surface-tint-strong text-primary-ink border-primary-solid/30",
};

export const toneSolid: Record<Tone, string> = {
  ok: "bg-ok-solid",
  warn: "bg-warn-solid",
  danger: "bg-danger-solid",
  info: "bg-info-solid",
  neutral: "bg-neutral-solid",
  primary: "bg-primary-solid",
};

export const toneStroke: Record<Tone, string> = {
  ok: "text-ok-solid",
  warn: "text-warn-solid",
  danger: "text-danger-solid",
  info: "text-info-solid",
  neutral: "text-neutral-solid",
  primary: "text-primary-solid",
};

// The tone of a meter: amber from warnAt (80 %), red from dangerAt (95 %).
export function meterTone(fraction: number, warnAt = 0.8, dangerAt = 0.95): "ok" | "warn" | "danger" {
  if (fraction >= dangerAt) return "danger";
  if (fraction >= warnAt) return "warn";
  return "ok";
}
