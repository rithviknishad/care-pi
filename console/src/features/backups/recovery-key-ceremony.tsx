import { useMutation } from "@tanstack/react-query";
import { CheckCircle2Icon, DownloadIcon, EyeIcon, KeyRoundIcon, PrinterIcon } from "lucide-react";
import { useId, useState } from "react";
import { api, call, callFull } from "@/api/client";
import { asProblem } from "@/api/problem";
import type { RecoveryKey, RecoveryKeyStatus } from "@/api/types";
import { FormField } from "@/components/app/form-field";
import { ProblemAlert } from "@/components/app/problem-alert";
import { QrCode } from "@/components/app/qr-code";
import { isReauthCancelled, useReauth } from "@/components/app/reauth-context";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import { downloadText } from "@/lib/download";
import { formatDateTime } from "@/lib/format";
import { expandWord } from "@/lib/recovery-key";
import { fmt, strings } from "@/lib/strings";
import { useShowKey } from "./use-backups";

const k = strings.keyCeremony;
type Which = "active" | "pending";

function shown(error: unknown): unknown {
  return error && !isReauthCancelled(error) ? error : null;
}

// The 24 words in a numbered grid (down each column, as the .txt file of the agent), the QR code, Print, and the
// .txt file. Print prints only this sheet (the class print-area, src/styles/console.css).
export function KeySheet({ recoveryKey, clinic }: { recoveryKey: RecoveryKey; clinic: string }) {
  const { withReauth } = useReauth();
  const download = useMutation({
    mutationFn: () =>
      withReauth(async () => {
        const { data, response } = await callFull(
          api.GET("/backups/recovery-key/download", {
            params: { query: { key: recoveryKey.key } },
            parseAs: "text",
            headers: { Accept: "text/plain" },
          }),
        );
        const name = /filename="?([^";]+)"?/.exec(response.headers.get("Content-Disposition") ?? "")?.[1];
        downloadText(name ?? recoveryKey.file_name, data);
      }),
  });
  return (
    <div className="flex flex-col gap-4">
      <div className="print-area flex flex-col gap-4" data-testid="key-sheet">
        <div className="hidden flex-col gap-1 print:flex">
          <h2 className="text-lg font-semibold">{k.printTitle}</h2>
          <p className="text-sm">{fmt(k.printClinic, { name: clinic })}</p>
        </div>
        <p className="text-xs text-soft-foreground">
          {fmt(k.keyId, { id: recoveryKey.key_id })} · {formatDateTime(recoveryKey.created_at).slice(0, 10)}
        </p>
        <div className="grid gap-4 md:grid-cols-[minmax(0,1fr)_11rem]">
          <ol
            className="grid grid-flow-col grid-cols-2 grid-rows-12 gap-x-4 gap-y-1.5 rounded-lg border bg-soft-background p-4 font-mono text-base sm:grid-cols-3 sm:grid-rows-8"
            aria-label={k.words}
            data-testid="key-words"
          >
            {recoveryKey.words.map((word, i) => (
              <li key={i} className="flex min-w-0 gap-2" data-position={i + 1}>
                <span className="w-[2ch] shrink-0 text-right text-soft-foreground">{i + 1}</span>
                <span className="font-semibold tracking-wide select-all">{word}</span>
              </li>
            ))}
          </ol>
          <figure className="flex flex-col items-center gap-2">
            <QrCode value={recoveryKey.qr_payload} className="w-40" label={k.qr} />
            <figcaption className="text-center text-xs text-soft-foreground">{k.qr}</figcaption>
          </figure>
        </div>
        <p className="hidden text-sm print:block">{k.printKeep}</p>
      </div>
      <p className="text-sm text-soft-foreground print:hidden">{k.save}</p>
      <div className="flex flex-wrap gap-2 print:hidden">
        <Button type="button" variant="outline" onClick={() => window.print()}>
          <PrinterIcon data-icon="inline-start" />
          {strings.common.print}
        </Button>
        <Button type="button" variant="outline" onClick={() => download.mutate()} disabled={download.isPending}>
          {download.isPending ? <Spinner data-icon="inline-start" /> : <DownloadIcon data-icon="inline-start" />}
          {k.downloadTxt}
        </Button>
      </div>
      {shown(download.error) ? <ProblemAlert error={download.error} /> : null}
      <p className="text-xs text-soft-foreground print:hidden">{k.hidden}</p>
    </div>
  );
}

// The 3 words at the confirm positions. A word of the list or its first 4 letters (BIP-39) is valid.
function ConfirmWords({
  which,
  positions,
  onConfirmed,
  onShowAgain,
  showing,
}: {
  which: Which;
  positions: number[];
  onConfirmed: (status: RecoveryKeyStatus) => void;
  onShowAgain: () => void;
  showing: boolean;
}) {
  const id = useId();
  const [words, setWords] = useState<string[]>(() => positions.map(() => ""));
  const [local, setLocal] = useState<(string | null)[]>(() => positions.map(() => null));
  const confirm = useMutation({
    mutationFn: (expanded: string[]) =>
      call(
        api.POST("/backups/recovery-key/confirm", {
          body: { key: which, words: positions.map((index, i) => ({ index, word: expanded[i] ?? "" })) },
        }),
      ),
    onSuccess: onConfirmed,
  });
  const mismatch = asProblem(confirm.error)?.is("recovery-key-mismatch") ? asProblem(confirm.error) : null;
  const serverError = (i: number) =>
    mismatch?.problem.errors?.some((e) => e.pointer === `/words/${i}/word`)
      ? fmt(k.wrongWord, { n: positions[i] ?? "" })
      : null;
  // Not a <form>: the step backups of the wizard is a form, and a form must not contain a form.
  const submit = () => {
    if (confirm.isPending || words.some((w) => w.length < 3)) return;
    const expanded = words.map((w) => expandWord(w));
    setLocal(expanded.map((w) => (w ? null : k.notInList)));
    if (expanded.every((w): w is string => w !== null)) confirm.mutate(expanded);
  };
  return (
    <div role="group" aria-labelledby={`${id}-title`} className="flex flex-col gap-3" data-testid="key-confirm">
      <div>
        <h3 id={`${id}-title`} className="font-semibold">
          {k.confirmTitle}
        </h3>
        <p className="text-sm text-soft-foreground">{k.confirmIntro}</p>
      </div>
      <div className="grid gap-x-4 sm:grid-cols-3">
        {positions.map((n, i) => {
          const error = local[i] ?? serverError(i);
          return (
            <FormField key={n} id={`${id}-${n}`} label={fmt(k.word, { n })} error={error}>
              <Input
                id={`${id}-${n}`}
                value={words[i] ?? ""}
                onChange={(e) => {
                  const next = [...words];
                  next[i] = e.target.value.toLowerCase().replace(/[^a-z]/g, "");
                  setWords(next);
                  setLocal((l) => l.map((v, j) => (j === i ? null : v)));
                }}
                onKeyDown={(e) => {
                  if (e.key !== "Enter") return;
                  e.preventDefault();
                  submit();
                }}
                autoComplete="off"
                autoCapitalize="none"
                spellCheck={false}
                className="font-mono"
                aria-invalid={error ? true : undefined}
                data-position={n}
              />
            </FormField>
          );
        })}
      </div>
      {confirm.error && !mismatch ? <ProblemAlert error={confirm.error} /> : null}
      <div className="flex flex-wrap gap-2">
        <Button
          type="button"
          onClick={submit}
          disabled={confirm.isPending || words.some((w) => w.length < 3)}
          data-testid="key-confirm-submit"
        >
          {confirm.isPending ? <Spinner data-icon="inline-start" /> : null}
          {k.confirm}
        </Button>
        <Button type="button" variant="outline" onClick={onShowAgain} disabled={showing}>
          {showing ? <Spinner data-icon="inline-start" /> : <EyeIcon data-icon="inline-start" />}
          {k.showAgain}
        </Button>
      </div>
    </div>
  );
}

// The recovery key ceremony (docs/api/README.md section 13.4): make the key, show the 24 words and the QR code,
// Print and the .txt file, "I saved the recovery key", then type 3 words again. which "pending" is the new key of
// a rotation. The words stay in the memory of this component only: they go away when the operator leaves the page.
export function RecoveryKeyCeremony({
  status,
  which = "active",
  clinic,
  onChange,
}: {
  status: RecoveryKeyStatus;
  which?: Which;
  clinic: string;
  onChange: (status: RecoveryKeyStatus) => void;
}) {
  const [key, setKey] = useState<RecoveryKey | null>(null);
  const [phase, setPhase] = useState<"show" | "confirm">("show");
  const [saved, setSaved] = useState(false);
  const target =
    which === "pending"
      ? status.pending
        ? {
            key_id: status.pending.key_id,
            confirmed_at: status.pending.confirmed_at,
            positions: status.pending.confirm_positions,
          }
        : null
      : status.state === "none"
        ? null
        : { key_id: status.key_id, confirmed_at: status.confirmed_at, positions: status.confirm_positions };

  const make = useMutation({
    mutationFn: () => call(api.POST("/backups/recovery-key")),
    onSuccess: (created) => {
      setKey(created.key);
      setPhase("show");
      setSaved(false);
      onChange(created.status);
    },
  });
  const showAgain = useShowKey(which, (shownKey) => {
    setKey(shownKey);
    setPhase("show");
    setSaved(false);
  });

  if (!target) {
    return (
      <section className="flex flex-col items-start gap-3" data-testid="key-ceremony" data-phase="none">
        <p className="text-sm text-soft-foreground">{which === "pending" ? k.pendingIntro : k.intro}</p>
        <Button type="button" onClick={() => make.mutate()} disabled={make.isPending} data-testid="key-make">
          {make.isPending ? <Spinner data-icon="inline-start" /> : <KeyRoundIcon data-icon="inline-start" />}
          {k.make}
        </Button>
        {make.error ? <ProblemAlert error={make.error} /> : null}
      </section>
    );
  }

  if (target.confirmed_at) {
    return (
      <Alert variant="success" data-testid="key-ceremony" data-phase="confirmed">
        <CheckCircle2Icon aria-hidden="true" />
        <AlertTitle>{k.confirmed}</AlertTitle>
        <AlertDescription>
          {fmt(k.confirmedAt, { time: formatDateTime(target.confirmed_at), id: target.key_id ?? "" })}
        </AlertDescription>
      </Alert>
    );
  }

  if (key && key.key_id === target.key_id && phase === "show") {
    return (
      <section className="flex flex-col gap-4" data-testid="key-ceremony" data-phase="show">
        {which === "pending" ? <p className="text-sm font-semibold">{k.pending}</p> : null}
        <KeySheet recoveryKey={key} clinic={clinic} />
        <label className="flex items-start gap-3 text-sm print:hidden">
          <Checkbox checked={saved} onCheckedChange={(v) => setSaved(v === true)} name="key-saved" />
          <span>{k.saved}</span>
        </label>
        <div className="print:hidden">
          <Button type="button" disabled={!saved} onClick={() => setPhase("confirm")} data-testid="key-saved-continue">
            {strings.common.continue}
          </Button>
        </div>
      </section>
    );
  }

  return (
    <section className="flex flex-col gap-3" data-testid="key-ceremony" data-phase="confirm">
      <ConfirmWords
        key={target.key_id ?? ""}
        which={which}
        positions={target.positions ?? []}
        showing={showAgain.isPending}
        onShowAgain={() => {
          if (key && key.key_id === target.key_id) {
            setPhase("show");
            setSaved(false);
          } else showAgain.mutate();
        }}
        onConfirmed={(next) => {
          setKey(null);
          onChange(next);
        }}
      />
      {shown(showAgain.error) ? <ProblemAlert error={showAgain.error} /> : null}
    </section>
  );
}
