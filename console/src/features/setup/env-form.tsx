import { PlusIcon, Trash2Icon } from "lucide-react";
import { useId } from "react";
import type { FeProvider } from "@/api/types";
import { FormField } from "@/components/app/form-field";
import { Button } from "@/components/ui/button";
import { FieldError } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { fmt, strings } from "@/lib/strings";
import { BACKEND_DEFAULTS, FRONTEND_DEFAULTS, type EnvDefault } from "./env-defaults";
import { newRow, type EnvErrors, type EnvModel, type EnvRow } from "./env-logic";

function Defaults({ items }: { items: EnvDefault[] }) {
  return (
    <details className="text-sm">
      <summary className="cursor-pointer py-1 text-soft-foreground">{strings.env.defaults}</summary>
      <p className="mt-1 text-xs text-soft-foreground">{strings.env.defaultsHelp}</p>
      <dl className="mt-2 grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] gap-x-4 rounded-md border bg-soft-background p-3 text-xs">
        {items.map((d) => (
          <div key={d.name} className="contents">
            <dt className="truncate leading-6" title={d.name}>
              {d.name}
            </dt>
            <dd className="truncate leading-6 text-soft-foreground" title={`${d.value} (${d.source})`}>
              {d.value}
            </dd>
          </div>
        ))}
      </dl>
    </details>
  );
}

function KeyValueEditor({
  part,
  legend,
  help,
  rows,
  onRows,
  errors,
  defaults,
}: {
  part: "backend" | "frontend";
  legend: string;
  help?: string;
  rows: EnvRow[];
  onRows: (rows: EnvRow[]) => void;
  errors: EnvErrors;
  defaults: EnvDefault[];
}) {
  const id = useId();
  const update = (key: number, patch: Partial<EnvRow>) =>
    onRows(rows.map((r) => (r.key === key ? { ...r, ...patch } : r)));
  return (
    <fieldset className="flex flex-col gap-3">
      <legend className="mb-1 font-semibold">{legend}</legend>
      {help ? <p className="-mt-2 text-xs text-soft-foreground">{help}</p> : null}
      {rows.length === 0 ? <p className="text-sm text-soft-foreground">{strings.env.empty}</p> : null}
      <ul className="flex flex-col gap-2">
        {rows.map((row) => {
          const nameError = errors[`${part}.${row.key}.name`];
          const valueError = errors[`${part}.${row.key}.value`];
          return (
            <li
              key={row.key}
              className="grid grid-cols-[minmax(0,1fr)_auto] gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)_auto]"
            >
              <div className="flex flex-col gap-1">
                <Label htmlFor={`${id}-${row.key}-n`} className="sr-only">
                  {strings.env.name}
                </Label>
                <Input
                  id={`${id}-${row.key}-n`}
                  value={row.name}
                  placeholder={strings.env.name}
                  autoCapitalize="characters"
                  spellCheck={false}
                  onChange={(e) => update(row.key, { name: e.target.value })}
                  aria-invalid={nameError ? true : undefined}
                />
                <div className="min-h-6">{nameError ? <FieldError>{nameError}</FieldError> : null}</div>
              </div>
              <div className="col-span-2 row-start-2 flex flex-col gap-1 sm:col-span-1 sm:row-start-auto">
                <Label htmlFor={`${id}-${row.key}-v`} className="sr-only">
                  {strings.env.value}
                </Label>
                <Input
                  id={`${id}-${row.key}-v`}
                  value={row.value}
                  placeholder={strings.env.value}
                  spellCheck={false}
                  onChange={(e) => update(row.key, { value: e.target.value })}
                  aria-invalid={valueError ? true : undefined}
                />
                <div className="min-h-6">{valueError ? <FieldError>{valueError}</FieldError> : null}</div>
              </div>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="col-start-2 row-start-1 sm:col-start-auto sm:row-start-auto"
                aria-label={fmt(strings.env.remove, { name: row.name || strings.env.name })}
                onClick={() => onRows(rows.filter((r) => r.key !== row.key))}
              >
                <Trash2Icon />
              </Button>
            </li>
          );
        })}
      </ul>
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="self-start"
        onClick={() => onRows([...rows, newRow()])}
      >
        <PlusIcon data-icon="inline-start" />
        {strings.env.add}
      </Button>
      <Defaults items={defaults} />
    </fieldset>
  );
}

const PROVIDERS: FeProvider[] = ["local", "artifact", "none"];

export function EnvForm({
  model,
  onChange,
  errors,
}: {
  model: EnvModel;
  onChange: (m: EnvModel) => void;
  errors: EnvErrors;
}) {
  const id = useId();
  return (
    <div className="flex flex-col gap-8">
      <KeyValueEditor
        part="backend"
        legend={strings.env.backend}
        rows={model.backend}
        onRows={(backend) => onChange({ ...model, backend })}
        errors={errors}
        defaults={BACKEND_DEFAULTS}
      />
      <KeyValueEditor
        part="frontend"
        legend={strings.env.frontend}
        help={strings.env.frontendHelp}
        rows={model.frontend}
        onRows={(frontend) => onChange({ ...model, frontend })}
        errors={errors}
        defaults={FRONTEND_DEFAULTS}
      />
      <fieldset className="flex flex-col gap-3">
        <legend className="mb-1 font-semibold">{strings.env.feProvider}</legend>
        <RadioGroup value={model.provider} onValueChange={(v) => onChange({ ...model, provider: v as FeProvider })}>
          {PROVIDERS.map((p) => (
            <Label key={p} htmlFor={`${id}-${p}`} className="flex items-start gap-3 font-normal">
              <RadioGroupItem id={`${id}-${p}`} value={p} />
              <span>
                <span className="font-semibold">{p}</span> — {strings.env.providers[p]}
              </span>
            </Label>
          ))}
        </RadioGroup>
        {model.provider === "artifact" ? (
          <div className="flex flex-col gap-1">
            <FormField
              id={`${id}-url`}
              label={strings.env.artifactUrl}
              help={strings.env.artifactUrlHelp}
              error={errors.artifactUrl}
            >
              <Input
                id={`${id}-url`}
                value={model.artifactUrl}
                inputMode="url"
                spellCheck={false}
                onChange={(e) => onChange({ ...model, artifactUrl: e.target.value })}
                aria-invalid={errors.artifactUrl ? true : undefined}
              />
            </FormField>
            <FormField
              id={`${id}-sha`}
              label={strings.env.artifactSha}
              help={strings.env.artifactShaHelp}
              error={errors.artifactSha}
            >
              <Input
                id={`${id}-sha`}
                value={model.artifactSha}
                spellCheck={false}
                onChange={(e) => onChange({ ...model, artifactSha: e.target.value })}
                aria-invalid={errors.artifactSha ? true : undefined}
              />
            </FormField>
          </div>
        ) : null}
      </fieldset>
    </div>
  );
}
