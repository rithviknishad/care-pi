import { useMutation } from "@tanstack/react-query";
import { ShieldCheckIcon, WifiOffIcon } from "lucide-react";
import { useId, useState } from "react";
import { api, call } from "@/api/client";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Switch } from "@/components/ui/switch";
import { strings } from "@/lib/strings";
import { StepActions, type StepProps } from "../step-frame";
import { loadOnline, saveOnline } from "../wizard";

const t = strings.tlsStep;

// The certificate step (S66): one sentence and one choice. The device always makes its own certificate (the local CA
// serves the LAN addresses and works offline, S36). "Keep Care working offline" off: the done page and the review
// remind the operator to add a public name in Settings › Connectivity after the install (it needs the internet and
// its own enrolment job, so the setup step does not take it).
export function TlsStep({ onDone, onBack, next }: StepProps) {
  const id = useId();
  const [offline, setOffline] = useState(() => !loadOnline());
  const save = useMutation({
    mutationFn: () =>
      call(
        api.PUT("/setup/steps/tls", {
          body: {
            local_ca: { enabled: true },
            public: { enabled: false, clinic_id: null },
            tailscale: { enabled: false },
          },
        }),
      ),
    onSuccess: (s) => {
      saveOnline(!offline);
      onDone(s);
    },
  });
  return (
    <form
      className="flex flex-col gap-6"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <div className="flex items-start gap-4 rounded-card border border-line-soft bg-surface-tint p-4 sm:p-5">
        <span className="grid size-11 shrink-0 place-items-center rounded-lg bg-surface-tint-strong text-primary-solid">
          <ShieldCheckIcon className="size-5" aria-hidden="true" />
        </span>
        <p className="type-body text-ink" data-testid="tls-sentence">
          {t.sentence}
        </p>
      </div>
      <div className="flex items-start gap-4 rounded-card border border-line p-4 sm:p-5">
        <span className="grid size-11 shrink-0 place-items-center rounded-lg bg-surface-tint-strong text-primary-solid">
          <WifiOffIcon className="size-5" aria-hidden="true" />
        </span>
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <label htmlFor={`${id}-offline`} className="type-body font-semibold text-ink">
            {t.offline}
          </label>
          <p id={`${id}-help`} className="min-h-10 type-small text-ink-muted" aria-live="polite">
            {offline ? t.offlineHelp : t.onlineHelp}
          </p>
        </div>
        <Switch
          id={`${id}-offline`}
          checked={offline}
          onCheckedChange={setOffline}
          aria-describedby={`${id}-help`}
          data-testid="tls-offline"
        />
      </div>
      {save.error ? <ProblemAlert error={save.error} /> : null}
      <StepActions onBack={onBack} busy={save.isPending} next={next} />
    </form>
  );
}
