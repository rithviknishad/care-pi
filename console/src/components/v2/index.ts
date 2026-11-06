// The v2 design system of the console (decision S66). console/design.md is the spec; /__v2 (dev server only)
// shows each component with sample data.
export { AppShell, type AppShellProps, type ShellNavItem } from "./app-shell";
export { ChoiceCards, type Choice, type ChoiceCardsProps } from "./choice-cards";
export { buildCopyText, copyHeaderLines, writeClipboard, type CopyHeader } from "./copy";
export { CopyButton, type CopyButtonProps } from "./copy-button";
export { useCopyHeader } from "./copy-context";
export { CopyHeaderProvider } from "./copy-provider";
export { DashboardGrid } from "./dashboard-grid";
export { aboutMinutes, clock } from "./duration";
export { EmptyState, type EmptyStateProps } from "./empty-state";
export { Explainer, type ExplainerProps } from "./explainer";
export { Focus, FocusBackdrop, FocusBar, FocusCard, type FocusCardProps } from "./focus";
export { Hero, type HeroProps } from "./hero";
export { Illustration, type IllustrationProps } from "./illustration";
export { illustrationNames, illustrations, type IllustrationName } from "./illustrations";
export { InlineSkeleton } from "./inline-skeleton";
export { KeyValueList, type KeyValueItem, type KeyValueListProps } from "./key-value-list";
export { MetricBar, type MetricBarProps } from "./metric-bar";
export { MonoBlock, type MonoBlockProps } from "./mono-block";
export { Panel, WhatNext, type PanelProps } from "./panel";
export { ProblemCard, type ProblemCardProps } from "./problem-card";
export { ProgressRing, type ProgressRingProps } from "./progress-ring";
export { everyText, retentionSummary, retentionZones, type RetentionPolicy, type RetentionZone } from "./retention";
export { Stage, type StageProps, type StageStep } from "./stage";
export { StatCard, type StatCardProps } from "./stat-card";
export { StatusChip, type StatusChipProps } from "./status-chip";
export {
  StepList,
  StepProgress,
  type StepItem,
  type StepListProps,
  type StepProgressProps,
  type StepState,
} from "./step-list";
export { v2Strings } from "./strings";
export { ThemeToggle } from "./theme-toggle";
export { Timeline, type TimelineItem } from "./timeline";
export { Toaster } from "./toast";
export { meterTone, type Tone } from "./tone";
export { useNow } from "./use-now";
