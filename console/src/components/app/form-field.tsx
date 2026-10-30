import { EyeIcon, EyeOffIcon } from "lucide-react";
import { useId, useState, type ReactNode } from "react";
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field";
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from "@/components/ui/input-group";
import { PASSWORD_MAX, passwordStrength } from "@/lib/codes";
import { describedBy } from "@/lib/form";
import { strings } from "@/lib/strings";
import { cn } from "@/lib/utils";

// A label, a control, a help text, and an error line. The error line has a fixed
// minimum height, so a message does not move the fields below it.
export function FormField({
  id,
  label,
  help,
  error,
  children,
  className,
}: {
  id: string;
  label: ReactNode;
  help?: ReactNode;
  error?: string | null | false;
  children: ReactNode;
  className?: string;
}) {
  return (
    <Field data-invalid={error ? true : undefined} className={className}>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      {children}
      {help ? <FieldDescription id={`${id}-help`}>{help}</FieldDescription> : null}
      <div className="min-h-6" aria-live="polite">
        {error ? <FieldError id={`${id}-error`}>{error}</FieldError> : null}
      </div>
    </Field>
  );
}

export function PasswordInput({ className, ...props }: Omit<React.ComponentProps<"input">, "type">) {
  const [show, setShow] = useState(false);
  return (
    <InputGroup className={className}>
      <InputGroupInput type={show ? "text" : "password"} spellCheck={false} autoCapitalize="none" {...props} />
      <InputGroupAddon align="inline-end">
        <InputGroupButton
          size="icon-xs"
          aria-label={show ? strings.common.hidePassword : strings.common.showPassword}
          aria-pressed={show}
          onClick={() => setShow((v) => !v)}
        >
          {show ? <EyeOffIcon /> : <EyeIcon />}
        </InputGroupButton>
      </InputGroupAddon>
    </InputGroup>
  );
}

const BAR_COLORS = ["bg-muted", "bg-red-500", "bg-amber-500", "bg-primary-500", "bg-primary-700"];

function StrengthMeter({ password }: { password: string }) {
  const score = passwordStrength(password);
  return (
    <div className="flex items-center gap-3" aria-live="polite">
      <div className="grid flex-1 grid-cols-4 gap-1" aria-hidden="true">
        {[1, 2, 3, 4].map((i) => (
          <div key={i} className={cn("h-1.5 rounded-full", i <= score ? BAR_COLORS[score] : "bg-muted")} />
        ))}
      </div>
      <span className="w-[10ch] text-right text-xs text-soft-foreground">
        <span className="sr-only">{strings.password.strengthLabel}: </span>
        {strings.password.strength[score]}
      </span>
    </div>
  );
}

// The new password and the confirmation, with a strength hint (a hint only: the agent applies the rules).
export function NewPasswordFields({
  password,
  confirm,
  onPassword,
  onConfirm,
  errors,
  label = strings.password.newLabel,
  help = strings.password.help,
  autoComplete = "new-password",
}: {
  password: string;
  confirm: string;
  onPassword: (v: string) => void;
  onConfirm: (v: string) => void;
  errors: { password?: string | undefined; confirm?: string | undefined };
  label?: string;
  help?: string;
  autoComplete?: string;
}) {
  const id = useId();
  return (
    <>
      <FormField id={`${id}-pw`} label={label} help={help} error={errors.password}>
        <PasswordInput
          id={`${id}-pw`}
          name="new-password"
          autoComplete={autoComplete}
          value={password}
          maxLength={PASSWORD_MAX}
          onChange={(e) => onPassword(e.target.value)}
          aria-invalid={errors.password ? true : undefined}
          aria-describedby={describedBy(`${id}-pw`, true, errors.password)}
        />
        <StrengthMeter password={password} />
      </FormField>
      <FormField id={`${id}-confirm`} label={strings.password.confirmLabel} error={errors.confirm}>
        <PasswordInput
          id={`${id}-confirm`}
          name="confirm-password"
          autoComplete={autoComplete}
          value={confirm}
          maxLength={PASSWORD_MAX}
          onChange={(e) => onConfirm(e.target.value)}
          aria-invalid={errors.confirm ? true : undefined}
          aria-describedby={describedBy(`${id}-confirm`, false, errors.confirm)}
        />
      </FormField>
    </>
  );
}
