import { DownloadIcon, PrinterIcon } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { CopyButton } from "@/components/app/copy-button";
import { downloadText } from "@/lib/download";
import { recoveryCodesText } from "@/lib/codes";
import { strings } from "@/lib/strings";

// Shows the recovery codes one time, with Print, Download (.txt), and the check "I saved these codes".
export function RecoveryCodesPanel({
  codes,
  generatedAt,
  onContinue,
  intro = strings.codes.intro,
}: {
  codes: string[];
  generatedAt: string;
  onContinue: () => void;
  intro?: string;
}) {
  const [saved, setSaved] = useState(false);
  const text = recoveryCodesText(codes, window.location.host, generatedAt);
  return (
    <div className="flex flex-col gap-5">
      <div className="print-area flex flex-col gap-4">
        <h2 className="hidden text-lg font-semibold print:block">{strings.codes.printTitle}</h2>
        <p className="text-sm text-soft-foreground">{intro}</p>
        <ol
          className="grid grid-cols-1 gap-x-6 gap-y-2 rounded-lg border bg-soft-background p-4 text-base tabular-nums min-[420px]:grid-cols-2"
          data-testid="recovery-codes"
        >
          {codes.map((code, i) => (
            <li key={code} className="flex gap-3">
              <span className="w-[2ch] text-right text-soft-foreground">{i + 1}.</span>
              <span className="font-semibold tracking-wider select-all">{code}</span>
            </li>
          ))}
        </ol>
        <p className="hidden text-sm print:block">{window.location.host}</p>
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
        <CopyButton value={codes.join("\n")} />
      </div>
      <label className="flex items-start gap-3 text-sm print:hidden">
        <Checkbox checked={saved} onCheckedChange={(v) => setSaved(v === true)} name="saved" />
        <span>{strings.codes.saved}</span>
      </label>
      <div className="print:hidden">
        <Button type="button" disabled={!saved} onClick={onContinue}>
          {strings.codes.continue}
        </Button>
      </div>
    </div>
  );
}
