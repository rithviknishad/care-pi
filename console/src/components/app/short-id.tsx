import { CheckIcon, CopyIcon } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { shortIdOf } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";
import { cn } from "@/lib/utils";

// A digest or a sha in monospace, short, with its full value in the title and a small copy button.
// The button is 24 px high, so the row of a DataList keeps its height.
export function ShortId({
  value,
  length = 12,
  label,
  className,
  testId,
}: {
  value: string | null | undefined;
  length?: number;
  label?: string;
  className?: string;
  testId?: string;
}) {
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const t = setTimeout(() => setCopied(false), 1500);
    return () => clearTimeout(t);
  }, [copied]);
  if (!value) return <span className={className}>—</span>;
  const text = copied ? strings.common.copied : fmt(strings.common.copyValue, { value: label ?? value });
  return (
    <span className={cn("inline-flex max-w-full min-w-0 items-center gap-1 align-middle", className)}>
      <code className="min-w-0 truncate font-mono text-xs" title={value} data-testid={testId}>
        {shortIdOf(value, length)}
      </code>
      <Button
        type="button"
        variant="ghost"
        size="icon-xs"
        className="-my-1 size-6 shrink-0 md:size-6"
        aria-label={text}
        title={text}
        onClick={() => {
          void navigator.clipboard?.writeText(value).then(() => setCopied(true));
        }}
      >
        {copied ? <CheckIcon /> : <CopyIcon />}
      </Button>
    </span>
  );
}
