import { CheckIcon, CopyIcon } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { buildCopyText, writeClipboard } from "./copy";
import { useCopyHeader } from "./copy-context";
import { v2Strings } from "./strings";

export interface CopyButtonProps {
  /** The text to copy. A function is called at the click (for a long log). */
  text: string | (() => string);
  /** A report copy (S68) adds the header: device, agent version, time, page. Use it for logs, jobs, problems. */
  report?: boolean;
  /** The first line of the body of a report copy, for example the title of the problem. */
  title?: string;
  label?: string;
  /** Only the icon; the label is the accessible name. */
  iconOnly?: boolean;
  size?: "xs" | "sm" | "default";
  variant?: "outline" | "ghost" | "tertiary";
  className?: string;
  onCopied?: (text: string) => void;
}

// Copies a value or a report. The label keeps its width ("Copy" and "Copied"), so the button does not move.
export function CopyButton({
  text,
  report = false,
  title,
  label = v2Strings.copy,
  iconOnly = false,
  size = "sm",
  variant = "outline",
  className,
  onCopied,
}: CopyButtonProps) {
  const header = useCopyHeader();
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const t = setTimeout(() => setCopied(false), 1600);
    return () => clearTimeout(t);
  }, [copied]);

  const onClick = async () => {
    const body = typeof text === "function" ? text() : text;
    const value = report
      ? buildCopyText(body, { ...header, page: header?.page ?? window.location.pathname }, title)
      : body;
    if (await writeClipboard(value)) {
      setCopied(true);
      toast.success(v2Strings.copied, report ? { description: v2Strings.copiedToast } : undefined);
      onCopied?.(value);
    } else {
      toast.error(v2Strings.copyFailed);
    }
  };

  const Icon = copied ? CheckIcon : CopyIcon;
  const iconSize = size === "xs" ? "icon-xs" : size === "default" ? "icon" : "icon-sm";
  return (
    <Button
      type="button"
      variant={variant}
      size={iconOnly ? iconSize : size}
      className={cn("no-underline", className)}
      aria-label={iconOnly ? (copied ? v2Strings.copied : label) : undefined}
      title={iconOnly ? (copied ? v2Strings.copied : label) : undefined}
      data-copied={copied || undefined}
      onClick={() => void onClick()}
    >
      <Icon data-icon={iconOnly ? undefined : "inline-start"} className={cn(copied && "text-ok-solid")} />
      {iconOnly ? null : (
        // Both labels share one grid cell: the button has the width of the longer one, in each state.
        <span className="grid text-left *:col-start-1 *:row-start-1">
          <span className={cn(copied && "invisible")} aria-hidden={copied || undefined}>
            {label}
          </span>
          <span className={cn(!copied && "invisible")} aria-hidden={!copied || undefined}>
            {v2Strings.copied}
          </span>
        </span>
      )}
    </Button>
  );
}
