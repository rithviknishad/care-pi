import { useId } from "react";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  NativeSelect,
  NativeSelectOption,
} from "@/components/ui/native-select";
import { Spinner } from "@/components/ui/spinner";
import { useTranslation } from "@/hooks/use-translation";

export function TextField({
  label,
  value,
  onChange,
  description,
  required,
  type = "text",
  inputMode,
  placeholder,
  testId,
  autoComplete = "off",
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  description?: string;
  required?: boolean;
  type?: string;
  inputMode?: React.HTMLAttributes<HTMLInputElement>["inputMode"];
  placeholder?: string;
  testId?: string;
  autoComplete?: string;
}) {
  const id = useId();
  return (
    <Field>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <Input
        id={id}
        type={type}
        inputMode={inputMode}
        value={value}
        required={required}
        placeholder={placeholder}
        autoComplete={autoComplete}
        data-testid={testId}
        onChange={(e) => onChange(e.target.value)}
      />
      {description && <FieldDescription>{description}</FieldDescription>}
    </Field>
  );
}

export function SelectField({
  label,
  value,
  onChange,
  options,
  description,
  testId,
  placeholder,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  options: { value: string; label: string }[];
  description?: string;
  testId?: string;
  placeholder?: string;
}) {
  const id = useId();
  return (
    <Field>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <NativeSelect
        id={id}
        value={value}
        className="w-full"
        data-testid={testId}
        onChange={(e) => onChange(e.target.value)}
      >
        {placeholder !== undefined && (
          <NativeSelectOption value="">{placeholder}</NativeSelectOption>
        )}
        {options.map((o) => (
          <NativeSelectOption key={o.value} value={o.value}>
            {o.label}
          </NativeSelectOption>
        ))}
      </NativeSelect>
      {description && <FieldDescription>{description}</FieldDescription>}
    </Field>
  );
}

export function CheckRow({
  label,
  description,
  checked,
  onChange,
  disabled,
}: {
  label: string;
  description?: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
  disabled?: boolean;
}) {
  const id = useId();
  return (
    <div className="flex items-start gap-3 py-1">
      <Checkbox
        checked={checked}
        disabled={disabled}
        aria-labelledby={`${id}-label`}
        onCheckedChange={(value) => onChange(Boolean(value))}
        className="mt-0.5"
      />
      <div className="flex flex-col">
        <span
          id={`${id}-label`}
          className="cursor-pointer text-sm font-medium"
          onClick={() => !disabled && onChange(!checked)}
        >
          {label}
        </span>
        {description && (
          <span className="text-muted-foreground text-sm">{description}</span>
        )}
      </div>
    </div>
  );
}

export function ApplyButton({
  busy,
  disabled,
  label,
}: {
  busy: boolean;
  disabled?: boolean;
  label?: string;
}) {
  const { t } = useTranslation();
  return (
    <Button type="submit" disabled={busy || disabled} data-testid="apply">
      {busy && <Spinner aria-hidden="true" />}
      {busy ? t("applying") : (label ?? t("apply"))}
    </Button>
  );
}

export interface StepFormProps {
  preview: Record<string, any>;
  facility: string | null;
  busy: boolean;
  onApply: (body: Record<string, unknown>) => void;
}
