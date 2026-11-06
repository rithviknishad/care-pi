import { useId, type ReactNode } from "react";
import { cn } from "@/lib/utils";
import { CopyButton } from "./copy-button";

export interface MonoBlockProps {
  /** The data. It is also the text of the copy. */
  text: string;
  /** A label above the block (for example "Fingerprint (SHA-256)"). */
  label?: ReactNode;
  /** A fixed height of N lines; longer text scrolls inside the block. Without it, the block grows. */
  lines?: number;
  /** pre: keep the lines and scroll to the side (logs). wrap: break anywhere (hashes, keys). */
  mode?: "pre" | "wrap";
  /** false: no copy button. "report": the copy has the support header (S68). */
  copy?: boolean | "report";
  /** The title of a report copy. */
  copyTitle?: string;
  className?: string;
  testId?: string;
}

// Data in Geist Mono with a Copy button. The button is in the corner (absolute), so it moves no text.
export function MonoBlock({
  text,
  label,
  lines,
  mode = "wrap",
  copy = true,
  copyTitle,
  className,
  testId,
}: MonoBlockProps) {
  const id = useId();
  const fixed = lines !== undefined;
  return (
    <div className={cn("flex min-w-0 flex-col gap-1.5", className)}>
      {label ? (
        <span id={id} className="type-small font-medium text-ink-muted">
          {label}
        </span>
      ) : null}
      <div className="relative min-w-0">
        <pre
          aria-labelledby={label ? id : undefined}
          tabIndex={fixed ? 0 : undefined}
          data-testid={testId}
          style={fixed ? { height: `calc(${lines} * 1.25rem + 1.5rem)` } : undefined}
          className={cn(
            "rounded-input border border-line bg-surface-sunken px-3 py-3 type-data text-ink outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
            copy ? "pr-12" : null,
            mode === "pre" ? "overflow-x-auto whitespace-pre" : "break-all whitespace-pre-wrap",
            fixed ? "overflow-y-auto" : null,
          )}
        >
          {text}
        </pre>
        {copy ? (
          <CopyButton
            text={text}
            report={copy === "report"}
            title={copyTitle}
            iconOnly
            size="xs"
            variant="ghost"
            className="absolute top-1.5 right-1.5 bg-surface-sunken/80"
          />
        ) : null}
      </div>
    </div>
  );
}
