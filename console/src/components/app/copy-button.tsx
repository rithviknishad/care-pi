import { CopyButton as V2CopyButton } from "@/components/v2/copy-button";
import { strings } from "@/lib/strings";

// An icon button that copies a value. withLabel shows the label next to the icon.
// It is the v2 CopyButton (console/design.md): the check mark, the toast "Copied", and a fallback without the
// Clipboard API. A report for support (S68) uses the v2 CopyButton with report.
export function CopyButton({
  value,
  label = strings.common.copy,
  withLabel = false,
}: {
  value: string;
  label?: string;
  withLabel?: boolean;
}) {
  return <V2CopyButton text={value} label={label} iconOnly={!withLabel} />;
}
