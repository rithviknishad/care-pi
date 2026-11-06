import { useId, useMemo, useState } from "react";
import type { ProblemError } from "@/api/problem";
import { usePlugSettingsSchema } from "@/api/queries";
import type { Plug, PlugSettingsValues } from "@/api/types";
import { PasswordInput } from "@/components/app/form-field";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { fmt, strings } from "@/lib/strings";
import { LINK_TONE } from "@/components/app/link-tone";
import { cn } from "@/lib/utils";
import {
  buildFields,
  changesOf,
  draftOf,
  groupFields,
  initialDraft,
  serverErrors,
  validateDraft,
  valueText,
  type Draft,
  type DraftValue,
  type FormField,
  type SecretDraft,
} from "./settings-form";

const p = strings.plugs;

function SecretControl({
  id,
  field,
  value,
  onChange,
  invalid,
  describedBy,
}: {
  id: string;
  field: FormField;
  value: SecretDraft;
  onChange: (v: SecretDraft) => void;
  invalid: boolean;
  describedBy: string | undefined;
}) {
  const input = (
    <PasswordInput
      id={id}
      autoComplete="off"
      value={value.text}
      placeholder={field.placeholder ?? undefined}
      onChange={(e) =>
        onChange({ mode: e.target.value === "" && !field.secretSet ? "keep" : "set", text: e.target.value })
      }
      aria-invalid={invalid ? true : undefined}
      aria-describedby={describedBy}
      data-testid={`setting-${field.key}`}
    />
  );
  if (!field.secretSet) return input;
  if (value.mode === "set") {
    return (
      <div className="flex flex-col gap-2">
        {input}
        <Button
          type="button"
          variant="link"
          size="xs"
          className={cn("self-start px-0", LINK_TONE)}
          onClick={() => onChange({ mode: "keep", text: "" })}
        >
          {p.secretKeep}
        </Button>
      </div>
    );
  }
  return (
    <div className="flex min-h-9 flex-wrap items-center gap-2" data-testid={`setting-${field.key}`}>
      {value.mode === "remove" ? (
        <span className="text-sm text-amber-700 dark:text-amber-300">{p.secretRemoved}</span>
      ) : (
        <Badge size="sm" variant="success">
          {p.secretSet}
        </Badge>
      )}
      {value.mode === "remove" ? (
        <Button type="button" variant="outline" size="xs" onClick={() => onChange({ mode: "keep", text: "" })}>
          {p.secretKeep}
        </Button>
      ) : (
        <>
          <Button type="button" variant="outline" size="xs" onClick={() => onChange({ mode: "set", text: "" })}>
            {p.secretChange}
          </Button>
          <Button type="button" variant="outline" size="xs" onClick={() => onChange({ mode: "remove", text: "" })}>
            {p.secretRemove}
          </Button>
        </>
      )}
    </div>
  );
}

// One field of the form: the control of its type (docs/api/README.md section 14.6), the help, the default, and the
// error line. The error line has a fixed minimum height, so a message does not move the fields below it.
function SettingField({
  field,
  value,
  error,
  reset,
  onChange,
  onReset,
}: {
  field: FormField;
  value: DraftValue | undefined;
  error: string | undefined;
  reset: boolean;
  onChange: (v: DraftValue) => void;
  onReset: () => void;
}) {
  const id = `${useId()}-${field.key}`;
  const helpId = `${id}-help`;
  const errorId = `${id}-error`;
  const described =
    [field.help || field.type === "list" || field.type === "secret" ? helpId : null, error ? errorId : null]
      .filter(Boolean)
      .join(" ") || undefined;
  const def = field.type === "secret" ? "" : valueText(field, field.defaultValue);
  const help = [
    field.help,
    field.type === "list" ? p.listHelp : null,
    field.type === "secret" ? p.secretHelp : null,
    def ? fmt(p.defaultValue, { value: def }) : null,
  ]
    .filter(Boolean)
    .join(" ");
  const invalid = Boolean(error);
  const label = (
    <span className="flex flex-wrap items-center gap-2">
      {field.label}
      {field.required ? (
        <Badge size="xs" variant="neutral">
          {strings.common.required}
        </Badge>
      ) : null}
      {field.source === "operator" && !reset ? (
        <Badge size="xs" variant="primary">
          {p.changed}
        </Badge>
      ) : null}
    </span>
  );

  let control: React.ReactNode;
  switch (field.type) {
    case "boolean":
      control = (
        <Switch
          id={id}
          checked={value === true}
          onCheckedChange={(v) => onChange(v)}
          aria-describedby={described}
          data-testid={`setting-${field.key}`}
        />
      );
      break;
    case "secret":
      control = (
        <SecretControl
          id={id}
          field={field}
          value={(value as SecretDraft | undefined) ?? { mode: "keep", text: "" }}
          onChange={onChange}
          invalid={invalid}
          describedBy={described}
        />
      );
      break;
    case "list":
      control = (
        <Textarea
          id={id}
          rows={3}
          value={String(value ?? "")}
          placeholder={field.placeholder ?? undefined}
          spellCheck={false}
          onChange={(e) => onChange(e.target.value)}
          aria-invalid={invalid ? true : undefined}
          aria-describedby={described}
          data-testid={`setting-${field.key}`}
        />
      );
      break;
    case "choices": {
      const selected = Array.isArray(value) ? value : [];
      control = (
        <div
          className="grid gap-2 sm:grid-cols-2"
          role="group"
          aria-describedby={described}
          data-testid={`setting-${field.key}`}
        >
          {field.options.map((o) => (
            <Label key={o.value} className="flex items-center gap-2 font-normal">
              <Checkbox
                checked={selected.includes(o.value)}
                onCheckedChange={(on) => onChange(on ? [...selected, o.value] : selected.filter((v) => v !== o.value))}
              />
              {o.label}
            </Label>
          ))}
        </div>
      );
      break;
    }
    default:
      control = (
        <Input
          id={id}
          type={field.type === "number" ? "number" : field.type === "url" ? "url" : "text"}
          inputMode={field.type === "number" ? "decimal" : field.type === "url" ? "url" : undefined}
          min={field.min ?? undefined}
          max={field.max ?? undefined}
          step={field.type === "number" ? "any" : undefined}
          value={String(value ?? "")}
          placeholder={field.placeholder ?? undefined}
          spellCheck={false}
          autoCapitalize="none"
          onChange={(e) => onChange(e.target.value)}
          aria-invalid={invalid ? true : undefined}
          aria-describedby={described}
          data-testid={`setting-${field.key}`}
        />
      );
  }

  const boolean = field.type === "boolean";
  return (
    <Field data-invalid={invalid ? true : undefined} data-field={field.id}>
      {boolean ? (
        <div className="flex items-center gap-3">
          {control}
          <FieldLabel htmlFor={id}>{label}</FieldLabel>
        </div>
      ) : field.type === "choices" ? (
        <>
          <span className="text-sm font-medium">{label}</span>
          {control}
        </>
      ) : (
        <>
          <FieldLabel htmlFor={id}>{label}</FieldLabel>
          {control}
        </>
      )}
      {help ? <FieldDescription id={helpId}>{help}</FieldDescription> : null}
      {field.source === "operator" && field.type !== "secret" && !reset ? (
        <div>
          <Button type="button" variant="link" size="xs" className={cn("px-0", LINK_TONE)} onClick={onReset}>
            {p.useDefault}
          </Button>
        </div>
      ) : null}
      <div className="min-h-6" aria-live="polite">
        {error ? (
          <FieldError id={errorId} data-testid={`setting-error-${field.key}`}>
            {error}
          </FieldError>
        ) : null}
      </div>
    </Field>
  );
}

// The settings form of a plug, made from GET /plugs/{id}/settings-schema and Plug.settings (the values).
// "Show the effect" sends only the changed keys with PUT /plugs/{id}?dry_run=true. 422 plug-settings-invalid
// shows each error at its field (errors[].pointer /settings/<part>/<key>).
export function PlugSettingsForm({
  plug,
  plugOn,
  enable,
  problem,
  pending,
  disabled,
  onPreview,
}: {
  plug: Plug;
  plugOn: boolean;
  enable: boolean;
  problem: ProblemError | null;
  pending: boolean;
  disabled: boolean;
  onPreview: (settings: PlugSettingsValues | null) => void;
}) {
  const schema = usePlugSettingsSchema(plug.id, plug.settings.length > 0);
  const fields = useMemo(() => buildFields(plug.settings, schema.data), [plug.settings, schema.data]);
  const [draft, setDraft] = useState<Draft>(() => initialDraft(buildFields(plug.settings, null)));
  const [resets, setResets] = useState<Set<string>>(() => new Set());
  const [edited, setEdited] = useState<Set<string>>(() => new Set());
  const [touched, setTouched] = useState(false);
  const local = validateDraft(fields, draft, plugOn);
  const server = serverErrors(fields, problem?.is("plug-settings-invalid") ? problem.problem : null);
  const errors: Record<string, string> = {};
  for (const [k, v] of Object.entries(server)) if (!edited.has(k)) errors[k] = v;
  if (touched) Object.assign(errors, local);
  const changes = changesOf(fields, draft, resets);
  const { basic, advanced } = groupFields(fields);

  const set = (f: FormField, v: DraftValue) => {
    setDraft((d) => ({ ...d, [f.id]: v }));
    setEdited((e) => new Set(e).add(f.id));
    if (resets.has(f.id)) {
      const next = new Set(resets);
      next.delete(f.id);
      setResets(next);
    }
  };
  const reset = (f: FormField) => {
    setDraft((d) => ({ ...d, [f.id]: draftOf(f, f.defaultValue) }));
    setResets((r) => new Set(r).add(f.id));
  };
  const render = (f: FormField) => (
    <SettingField
      key={f.id}
      field={f}
      value={draft[f.id] ?? draftOf(f)}
      error={errors[f.id]}
      reset={resets.has(f.id)}
      onChange={(v) => set(f, v)}
      onReset={() => reset(f)}
    />
  );

  if (fields.length === 0) return <p className="text-sm text-soft-foreground">{p.noSettings}</p>;
  return (
    <form
      className="flex flex-col gap-1"
      noValidate
      onSubmit={(e) => {
        e.preventDefault();
        setTouched(true);
        setEdited(new Set());
        if (Object.keys(local).length > 0) return;
        onPreview(changes);
      }}
      data-testid={`plug-settings-${plug.id}`}
    >
      <fieldset disabled={disabled} className="flex flex-col gap-1 disabled:opacity-60">
        {basic.map(render)}
        {advanced.length > 0 ? (
          <Collapsible className="rounded-lg border px-3 py-1">
            <CollapsibleTrigger asChild>
              <Button type="button" variant="ghost" size="sm" className="-mx-2 w-[calc(100%+1rem)] justify-start">
                {p.advanced}
              </Button>
            </CollapsibleTrigger>
            <CollapsibleContent className="flex flex-col gap-1 pt-2">{advanced.map(render)}</CollapsibleContent>
          </Collapsible>
        ) : null}
      </fieldset>
      <div className="flex flex-wrap gap-2 pt-2">
        <Button
          type="submit"
          disabled={disabled || pending || (!changes && !enable)}
          data-testid={`plug-settings-preview-${plug.id}`}
        >
          {pending ? <Spinner data-icon="inline-start" /> : null}
          {enable ? p.turnOnWithSettings : p.preview}
        </Button>
        <Button
          type="button"
          variant="outline"
          disabled={disabled || (!changes && !touched)}
          onClick={() => {
            setDraft(initialDraft(fields));
            setResets(new Set());
            setEdited(new Set());
            setTouched(false);
          }}
        >
          {p.undo}
        </Button>
      </div>
    </form>
  );
}
