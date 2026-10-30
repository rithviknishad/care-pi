import { useMutation } from "@tanstack/react-query";
import { CheckCircle2Icon, CircleIcon } from "lucide-react";
import { api, call } from "@/api/client";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Badge } from "@/components/ui/badge";
import { fmt, strings } from "@/lib/strings";
import { cn } from "@/lib/utils";
import { StepActions, StepIntro, type StepProps } from "../step-frame";

function Mode({ title, help, selected, phase }: { title: string; help: string; selected: boolean; phase?: number }) {
  return (
    <li
      className={cn(
        "flex gap-3 rounded-lg border p-4",
        selected ? "border-primary-600 bg-primary-50 dark:bg-primary-400/10" : "text-soft-foreground",
      )}
      aria-disabled={phase ? true : undefined}
    >
      {selected ? (
        <CheckCircle2Icon className="mt-0.5 size-5 shrink-0 text-primary" aria-hidden="true" />
      ) : (
        <CircleIcon className="mt-0.5 size-5 shrink-0" aria-hidden="true" />
      )}
      <div className="flex min-w-0 flex-col gap-1">
        <p className="flex flex-wrap items-center gap-2 font-semibold">
          {title}
          {phase ? (
            <Badge variant="neutral" size="sm">
              {fmt(strings.tls.phase, { phase })}
            </Badge>
          ) : null}
        </p>
        <p className="text-sm">{help}</p>
        {phase ? <p className="text-xs">{strings.tls.later}</p> : null}
      </div>
    </li>
  );
}

// Phase 2 accepts only the local CA mode. The other modes show as not available (x-phase 6).
export function TlsStep({ onDone, onBack }: StepProps) {
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
    onSuccess: onDone,
  });
  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <StepIntro>{strings.tls.intro}</StepIntro>
      <ul className="flex flex-col gap-3">
        <Mode title={strings.tls.localCa} help={strings.tls.localCaHelp} selected />
        <Mode title={strings.tls.publicCert} help={strings.tls.publicCertHelp} selected={false} phase={6} />
        <Mode title={strings.tls.tailscale} help={strings.tls.tailscaleHelp} selected={false} phase={6} />
      </ul>
      {save.error ? <ProblemAlert error={save.error} /> : null}
      <StepActions onBack={onBack} busy={save.isPending} submitLabel={strings.tls.useLocal} />
    </form>
  );
}
