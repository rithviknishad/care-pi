import { CheckIcon, CopyIcon } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { strings } from "@/lib/strings";

// An icon button that copies a value. withLabel shows the label next to the icon.
export function CopyButton({
  value,
  label = strings.common.copy,
  withLabel = false,
}: {
  value: string;
  label?: string;
  withLabel?: boolean;
}) {
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const t = setTimeout(() => setCopied(false), 1500);
    return () => clearTimeout(t);
  }, [copied]);
  return (
    <Button
      type="button"
      variant="outline"
      size={withLabel ? "sm" : "icon-sm"}
      aria-label={withLabel ? undefined : copied ? strings.common.copied : label}
      title={copied ? strings.common.copied : label}
      onClick={() => {
        void navigator.clipboard?.writeText(value).then(() => setCopied(true));
      }}
    >
      {copied ? <CheckIcon data-icon={withLabel ? "inline-start" : undefined} /> : null}
      {copied ? null : <CopyIcon data-icon={withLabel ? "inline-start" : undefined} />}
      {withLabel ? (
        <span className="inline-block min-w-[12ch] text-left">{copied ? strings.common.copied : label}</span>
      ) : null}
    </Button>
  );
}
