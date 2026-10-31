import { useMutation } from "@tanstack/react-query";
import { useId, useState } from "react";
import { api, call } from "@/api/client";
import { useConfig } from "@/api/queries";
import type {
  Config,
  ConfigChangeResult,
  Destination,
  DestinationCredentials,
  DestinationState,
  Job,
} from "@/api/types";
import { FormField, PasswordInput } from "@/components/app/form-field";
import { ProblemAlert } from "@/components/app/problem-alert";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { Plan } from "@/features/settings/config-change";
import { fmt, strings } from "@/lib/strings";
import { DestinationForm } from "./destination-form";
import type { Choice } from "./destination-meta";
import { DestinationChoices, DestinationSchedule, type ScheduleValue } from "./destination-parts";

const p = strings.backupsPage;

type Secret = { id: string; credentials: DestinationCredentials } | null;

// The destinations of config.yaml (view=file): the list that PATCH /config replaces as a whole (RFC 7396).
function fileDestinations(config: Config | undefined): Destination[] {
  return (config?.backups?.destinations ?? []) as Destination[];
}

// The change of backups.destinations (docs/api/README.md section 13.5): PATCH /config with If-Match and the full list,
// first as a dry run (the plan), then with apply. An S3 destination needs its secret before the apply: PATCH without
// apply, PUT /backups/destinations/{id}/secrets, then POST /apply.
function useDestinationsChange(onJob: (job: Job) => void) {
  const file = useConfig("file");
  const etag = file.data?.etag ?? "";
  const patch = (list: Destination[], query: { dry_run?: boolean; apply?: boolean }) =>
    call(
      api.PATCH("/config", {
        params: { header: { "If-Match": etag }, query },
        body: { backups: { destinations: list } } as unknown as Record<string, never>,
        headers: { "Content-Type": "application/merge-patch+json" },
      }),
    );
  const preview = useMutation({ mutationFn: (list: Destination[]) => patch(list, { dry_run: true }) });
  const apply = useMutation({
    mutationFn: async ({ list, secret }: { list: Destination[]; secret: Secret }): Promise<Job | null> => {
      if (!secret) return (await patch(list, { apply: true })).job;
      await patch(list, {});
      await call(
        api.PUT("/backups/destinations/{destination_id}/secrets", {
          params: { path: { destination_id: secret.id } },
          body: secret.credentials,
        }),
      );
      return call(api.POST("/apply", { body: { update: false, reason: `backup destination ${secret.id}` } }));
    },
    onSuccess: (job) => {
      if (job) onJob(job);
    },
  });
  return { file, preview, apply, list: fileDestinations(file.data?.config) };
}

function PlanStep({
  plan,
  busy,
  error,
  onBack,
  onApply,
}: {
  plan: ConfigChangeResult | undefined;
  busy: boolean;
  error: unknown;
  onBack: () => void;
  onApply: () => void;
}) {
  return (
    <div className="flex flex-col gap-3">
      {plan ? <Plan plan={plan.plan} /> : <Skeleton className="h-32" />}
      {error ? <ProblemAlert error={error} /> : null}
      <div className="flex flex-wrap gap-2 border-t pt-4">
        <Button type="button" variant="outline" onClick={onBack} disabled={busy}>
          {strings.common.back}
        </Button>
        <Button type="button" onClick={onApply} disabled={busy || !plan} data-testid="dest-apply">
          {busy ? <Spinner data-icon="inline-start" /> : null}
          {p.applyChange}
        </Button>
      </div>
    </div>
  );
}

// "Add a destination": the 3 choices, the form with the test, the plan, and the apply job.
export function AddDestinationDialog({
  open,
  onOpenChange,
  walTimeoutMin,
  onJob,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  walTimeoutMin: number | undefined;
  onJob: (job: Job) => void;
}) {
  const change = useDestinationsChange((job) => {
    onOpenChange(false);
    onJob(job);
  });
  const [choice, setChoice] = useState<Choice | null>(null);
  const [staged, setStaged] = useState<{ list: Destination[]; secret: Secret } | null>(null);
  const close = (next: boolean) => {
    if (!next) {
      setChoice(null);
      setStaged(null);
      change.preview.reset();
      change.apply.reset();
    }
    onOpenChange(next);
  };
  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className="sm:max-w-2xl" innerClassName="max-h-[85dvh] overflow-y-auto" data-testid="add-dialog">
        <DialogHeader>
          <DialogTitle>{p.add}</DialogTitle>
          <DialogDescription>{strings.backups.destIntro}</DialogDescription>
        </DialogHeader>
        {change.file.error ? <ProblemAlert error={change.file.error} /> : null}
        {staged ? (
          <PlanStep
            plan={change.preview.data}
            busy={change.apply.isPending || change.preview.isPending}
            error={change.preview.error ?? change.apply.error}
            onBack={() => {
              setStaged(null);
              change.preview.reset();
            }}
            onApply={() => change.apply.mutate(staged)}
          />
        ) : (
          <div className="flex flex-col gap-4">
            <DestinationChoices value={choice} onChange={setChoice} />
            {choice && change.file.data ? (
              <DestinationForm
                key={choice}
                choice={choice}
                existingIds={change.list.map((d) => d.id)}
                takenLabels={change.list.flatMap((d) => (d.disk_label ? [d.disk_label] : []))}
                walTimeoutMin={walTimeoutMin}
                onCancel={() => setChoice(null)}
                addLabel={p.saveChange}
                onAdd={(d, credentials) => {
                  const list = [...change.list, d];
                  const next = { list, secret: credentials ? { id: d.id, credentials } : null };
                  setStaged(next);
                  change.preview.mutate(list);
                }}
              />
            ) : null}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

// "Change": the interval and the retention of a destination, and new S3 keys. To change the place, add a new
// destination and remove this one.
export function EditDestinationDialog({
  destination,
  onClose,
  walTimeoutMin,
  onJob,
}: {
  destination: DestinationState | null;
  onClose: () => void;
  walTimeoutMin: number | undefined;
  onJob: (job: Job) => void;
}) {
  const id = useId();
  const change = useDestinationsChange((job) => {
    onClose();
    onJob(job);
  });
  const current = destination ? change.list.find((d) => d.id === destination.id) : undefined;
  const [schedule, setSchedule] = useState<ScheduleValue | null>(null);
  const [keys, setKeys] = useState({ access: "", secret: "" });
  const [staged, setStaged] = useState<{ list: Destination[]; secret: Secret } | null>(null);
  const value: ScheduleValue = schedule ?? { interval_min: current?.interval_min, retention: current?.retention };
  const close = () => {
    setSchedule(null);
    setKeys({ access: "", secret: "" });
    setStaged(null);
    change.preview.reset();
    change.apply.reset();
    onClose();
  };
  const submit = () => {
    if (!current) return;
    const { interval_min: _i, retention: _r, ...rest } = current;
    void _i;
    void _r;
    const next: Destination = {
      ...rest,
      ...(value.interval_min ? { interval_min: value.interval_min } : {}),
      ...(value.retention ? { retention: value.retention } : {}),
    };
    const list = change.list.map((d) => (d.id === current.id ? next : d));
    const secret =
      current.type === "s3" && keys.access && keys.secret
        ? { id: current.id, credentials: { access_key_id: keys.access, secret_access_key: keys.secret } }
        : null;
    setStaged({ list, secret });
    change.preview.mutate(list);
  };
  return (
    <Dialog open={destination !== null} onOpenChange={(open) => (open ? undefined : close())}>
      <DialogContent className="sm:max-w-xl" innerClassName="max-h-[85dvh] overflow-y-auto" data-testid="edit-dialog">
        <DialogHeader>
          <DialogTitle>{fmt(p.editTitle, { name: destination?.id ?? "" })}</DialogTitle>
          <DialogDescription>{p.editHelp}</DialogDescription>
        </DialogHeader>
        {change.file.error ? <ProblemAlert error={change.file.error} /> : null}
        {!current ? (
          <Skeleton className="h-40" />
        ) : staged ? (
          <PlanStep
            plan={change.preview.data}
            busy={change.apply.isPending || change.preview.isPending}
            error={change.preview.error ?? change.apply.error}
            onBack={() => setStaged(null)}
            onApply={() => change.apply.mutate(staged)}
          />
        ) : (
          <div className="flex flex-col gap-4">
            <DestinationSchedule value={value} onChange={setSchedule} walTimeoutMin={walTimeoutMin} />
            {current.type === "s3" ? (
              <fieldset className="grid gap-x-4 sm:grid-cols-2">
                <legend className="mb-2 text-sm font-semibold">{p.newKeys}</legend>
                <FormField id={`${id}-access`} label={strings.dest.s3.accessKey}>
                  <Input
                    id={`${id}-access`}
                    value={keys.access}
                    onChange={(e) => setKeys({ ...keys, access: e.target.value.trim() })}
                    autoComplete="off"
                  />
                </FormField>
                <FormField id={`${id}-secret`} label={strings.dest.s3.secretKey}>
                  <PasswordInput
                    id={`${id}-secret`}
                    value={keys.secret}
                    onChange={(e) => setKeys({ ...keys, secret: e.target.value })}
                    autoComplete="off"
                  />
                </FormField>
              </fieldset>
            ) : null}
            <div className="flex flex-wrap gap-2 border-t pt-4">
              <Button type="button" variant="outline" onClick={close}>
                {strings.common.cancel}
              </Button>
              <Button
                type="button"
                onClick={submit}
                disabled={Boolean(keys.access) !== Boolean(keys.secret)}
                data-testid="edit-preview"
              >
                {p.saveChange}
              </Button>
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

// "Remove": PATCH /config without the destination, then the apply. The data at the destination stays.
export function RemoveDestinationDialog({
  destination,
  onClose,
  onJob,
}: {
  destination: DestinationState | null;
  onClose: () => void;
  onJob: (job: Job) => void;
}) {
  const change = useDestinationsChange((job) => {
    onClose();
    onJob(job);
  });
  const name = destination?.id ?? "";
  return (
    <AlertDialog open={destination !== null} onOpenChange={(open) => (open ? undefined : onClose())}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{fmt(p.removeTitle, { name })}</AlertDialogTitle>
          <AlertDialogDescription>{fmt(p.removeText, { name })}</AlertDialogDescription>
        </AlertDialogHeader>
        {change.apply.error ? <ProblemAlert error={change.apply.error} /> : null}
        <AlertDialogFooter>
          <AlertDialogCancel>{strings.common.cancel}</AlertDialogCancel>
          <Button
            type="button"
            variant="destructive-solid"
            disabled={!change.file.data || change.apply.isPending}
            onClick={() => change.apply.mutate({ list: change.list.filter((d) => d.id !== name), secret: null })}
            data-testid="remove-confirm"
          >
            {change.apply.isPending ? <Spinner data-icon="inline-start" /> : null}
            {p.remove}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
