import { DownloadIcon, PrinterIcon } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { CopyButton } from "@/components/v2/copy-button";
import { WhatNext } from "@/components/v2/panel";
import { formatDateTime } from "@/lib/format";
import { downloadText } from "@/lib/download";
import { recoveryCodesText } from "@/lib/codes";
import { strings } from "@/lib/strings";

// Shows the recovery codes one time, with Print, Download (.txt), and the check "I saved these codes".
export function RecoveryCodesPanel({
  codes,
  generatedAt,
  onContinue,
  intro = strings.codes.intro,
  next,
}: {
  codes: string[];
  generatedAt: string;
  onContinue: () => void;
  intro?: string;
  /** "What happens next" under Continue. */
  next?: string;
}) {
  const [saved, setSaved] = useState(false);
  const text = recoveryCodesText(codes, window.location.host, generatedAt);
  return (
    <div className="flex flex-col gap-5">
      <p className="type-body text-ink-muted print:hidden">{intro}</p>
      {/* A card to print: the title, the 8 codes, the device, and the date. */}
      <div className="print-area flex flex-col gap-3 rounded-card border-2 border-dashed border-line-strong bg-surface-sunken p-4 sm:p-5">
        <div className="flex items-baseline justify-between gap-3">
          <h2 className="type-h3 text-ink">{strings.codes.printTitle}</h2>
          <span className="type-data text-ink-soft">{window.location.hostname}</span>
        </div>
        <ol className="grid grid-cols-1 gap-x-6 gap-y-2 type-data min-[420px]:grid-cols-2" data-testid="recovery-codes">
          {codes.map((code, i) => (
            <li key={code} className="flex gap-3 text-base leading-7">
              <span className="w-[2ch] text-right text-ink-soft">{i + 1}.</span>
              <span className="font-semibold tracking-wider text-ink select-all">{code}</span>
            </li>
          ))}
        </ol>
        <p className="type-data text-ink-soft">{formatDateTime(generatedAt)}</p>
      </div>
      <div className="flex flex-wrap gap-2 print:hidden">
        <Button type="button" variant="outline" onClick={() => window.print()}>
          <PrinterIcon data-icon="inline-start" />
          {strings.common.print}
        </Button>
        <Button type="button" variant="outline" onClick={() => downloadText(strings.codes.downloadFile, text)}>
          <DownloadIcon data-icon="inline-start" />
          {strings.codes.downloadTxt}
        </Button>
        <CopyButton text={codes.join("\n")} />
      </div>
      <label className="flex min-h-11 items-center gap-3 rounded-card border border-line bg-surface-tint px-4 py-3 type-body text-ink print:hidden">
        <Checkbox checked={saved} onCheckedChange={(v) => setSaved(v === true)} name="saved" />
        <span>{strings.codes.saved}</span>
      </label>
      <div className="flex flex-col gap-2 print:hidden">
        <Button type="button" size="lg" className="w-full" disabled={!saved} onClick={onContinue}>
          {strings.codes.continue}
        </Button>
        {next ? <WhatNext>{next}</WhatNext> : null}
      </div>
    </div>
  );
}
