import { useMutation, useQueryClient } from "@tanstack/react-query";
import { AlertTriangleIcon, InfoIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { useState } from "react";
import { api, call } from "@/api/client";
import { qk } from "@/api/queries";
import type { Destination, DestinationCredentials, RecoveryKeyStatus } from "@/api/types";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { formatRpo, intervalText, rpoMinutes } from "@/features/backups/backups-format";
import { DestinationForm } from "@/features/backups/destination-form";
import { TYPE_ICONS, destinationPlace, type Choice } from "@/features/backups/destination-meta";
import { DestinationChoices } from "@/features/backups/destination-parts";
import { RecoveryKeyCeremony } from "@/features/backups/recovery-key-ceremony";
import { fmt, strings } from "@/lib/strings";
import { StepActions, StepIntro, type StepProps } from "../step-frame";

const s = strings.backups;

const NO_KEY: RecoveryKeyStatus = {
  state: "none",
  key_id: null,
  created_at: null,
  confirmed_at: null,
  confirm_positions: null,
  pending: null,
  local_repository: "none",
  rotation_job_id: null,
};

function DestinationRow({
  d,
  onRemove,
  walTimeoutMin,
}: {
  d: Destination;
  onRemove: () => void;
  walTimeoutMin: number;
}) {
  const Icon = TYPE_ICONS[d.type] ?? InfoIcon;
  return (
    <li className="flex items-center gap-3 rounded-lg border p-3" data-testid="dest-row" data-id={d.id}>
      <Icon className="size-5 shrink-0 text-primary" aria-hidden="true" />
      <div className="min-w-0 flex-1">
        <p className="truncate font-semibold">
          {d.id} <span className="font-normal text-soft-foreground">· {strings.dest.types[d.type]}</span>
        </p>
        <p className="truncate text-xs text-soft-foreground" title={destinationPlace(d)}>
          {destinationPlace(d)}
        </p>
        <p className="text-xs text-soft-foreground">
          {fmt(s.schedule, {
            interval: intervalText(d.interval_min ?? 15).toLowerCase(),
            rpo: formatRpo(rpoMinutes(d.interval_min, walTimeoutMin)),
          })}
        </p>
      </div>
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        onClick={onRemove}
        aria-label={fmt(s.remove, { name: d.id })}
      >
        <Trash2Icon />
      </Button>
    </li>
  );
}

// The step backups (docs/api/README.md sections 13.4 and 13.5): the recovery key ceremony first, then 1 or more
// destinations. PUT /setup/steps/backups writes the destinations and the S3 keys. A dev build can skip the step.
export function BackupsStep({ setup, onDone, onBack }: StepProps) {
  const qc = useQueryClient();
  const step = setup.steps.find((x) => x.id === "backups");
  const draft = setup.draft.backups;
  const canSkip = Boolean(step?.skippable && setup.dev_build);
  const [keyStatus, setKeyStatus] = useState<RecoveryKeyStatus>(draft?.recovery_key ?? NO_KEY);
  const [destinations, setDestinations] = useState<Destination[]>(draft?.destinations ?? []);
  const [credentials, setCredentials] = useState<Record<string, DestinationCredentials>>({});
  const [choice, setChoice] = useState<Choice | null>(null);
  const [choosing, setChoosing] = useState(false);
  const walTimeoutMin = 5;
  const keyConfirmed = keyStatus.state === "confirmed";

  const save = useMutation({
    mutationFn: () =>
      call(api.PUT("/setup/steps/backups", { body: { destinations, credentials, recovery_key_words: null } })),
    onSuccess: onDone,
  });
  const skip = useMutation({
    mutationFn: () => call(api.POST("/setup/steps/{step}/skip", { params: { path: { step: "backups" } } })),
    onSuccess: onDone,
  });

  const missing = destinations.filter(
    (d) => d.type === "s3" && !credentials[d.id] && draft?.credentials[d.id] !== "present",
  );
  const showChoices = keyConfirmed && choice === null && (destinations.length === 0 || choosing);

  return (
    <form
      className="flex flex-col gap-6"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <StepIntro>{s.intro}</StepIntro>
      {setup.dev_build ? (
        <Alert variant="info">
          <InfoIcon />
          <AlertDescription>{s.devBuild}</AlertDescription>
        </Alert>
      ) : null}
      {keyStatus.state === "unconfirmed" ? (
        <Alert variant="warning" data-testid="key-reminder">
          <AlertTriangleIcon />
          <AlertDescription>{strings.keyCeremony.reminder}</AlertDescription>
        </Alert>
      ) : null}

      <section className="flex flex-col gap-3" aria-labelledby="backups-key">
        <h3 id="backups-key" className="font-semibold">
          {s.keyTitle}
        </h3>
        <RecoveryKeyCeremony
          status={keyStatus}
          clinic={setup.draft.identity.display_name}
          onChange={(next) => {
            setKeyStatus(next);
            void qc.invalidateQueries({ queryKey: qk.setup });
          }}
        />
      </section>

      <section className="flex flex-col gap-3" aria-labelledby="backups-dest" data-testid="backups-destinations">
        <h3 id="backups-dest" className="font-semibold">
          {s.destTitle}
        </h3>
        <p className="text-sm text-soft-foreground">{keyConfirmed ? s.destIntro : s.keyFirst}</p>
        {destinations.length > 0 ? (
          <ul className="flex flex-col gap-2">
            {destinations.map((d) => (
              <DestinationRow
                key={d.id}
                d={d}
                walTimeoutMin={walTimeoutMin}
                onRemove={() => {
                  setDestinations(destinations.filter((x) => x.id !== d.id));
                  setCredentials(Object.fromEntries(Object.entries(credentials).filter(([id]) => id !== d.id)));
                }}
              />
            ))}
          </ul>
        ) : null}
        {showChoices ? <DestinationChoices value={choice} onChange={setChoice} /> : null}
        {choice ? (
          <DestinationForm
            key={choice}
            choice={choice}
            existingIds={destinations.map((d) => d.id)}
            takenLabels={destinations.flatMap((d) => (d.disk_label ? [d.disk_label] : []))}
            walTimeoutMin={walTimeoutMin}
            onCancel={() => setChoice(null)}
            onAdd={(d, c) => {
              setDestinations([...destinations, d]);
              if (c) setCredentials({ ...credentials, [d.id]: c });
              setChoice(null);
              setChoosing(false);
            }}
          />
        ) : null}
        {keyConfirmed && destinations.length > 0 && choice === null && !choosing ? (
          <div>
            <Button type="button" variant="outline" onClick={() => setChoosing(true)} data-testid="dest-add-another">
              <PlusIcon data-icon="inline-start" />
              {s.addAnother}
            </Button>
          </div>
        ) : null}
        {missing.map((d) => (
          <Alert key={d.id} variant="warning">
            <AlertTriangleIcon />
            <AlertDescription>{fmt(s.missingSecret, { name: d.id })}</AlertDescription>
          </Alert>
        ))}
      </section>

      {save.error ? <ProblemAlert error={save.error} /> : null}
      {skip.error ? <ProblemAlert error={skip.error} /> : null}
      <StepActions
        onBack={onBack}
        busy={save.isPending || skip.isPending}
        disabled={!keyConfirmed || destinations.length === 0 || missing.length > 0 || choice !== null}
        extra={
          canSkip ? (
            <Button type="button" variant="ghost" onClick={() => skip.mutate()} disabled={skip.isPending}>
              {strings.common.skip}
            </Button>
          ) : null
        }
      />
    </form>
  );
}
