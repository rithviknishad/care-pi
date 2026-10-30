import { useMutation } from "@tanstack/react-query";
import { InfoIcon } from "lucide-react";
import { useId, useState } from "react";
import { api, call } from "@/api/client";
import { asProblem } from "@/api/problem";
import { FormField, NewPasswordFields } from "@/components/app/form-field";
import { describedBy, validateNewPassword } from "@/lib/form";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Input } from "@/components/ui/input";
import { CARE_USERNAME_RE } from "@/lib/schema";
import { strings } from "@/lib/strings";
import { StepActions, StepIntro, type StepProps } from "../step-frame";

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

export function CareAdminStep({ setup, onDone, onBack }: StepProps) {
  const id = useId();
  const draft = setup.draft.care_admin;
  const step = setup.steps.find((s) => s.id === "care-admin");
  const passwordNeeded = step?.errors.some((e) => e.code === "password-needed") ?? false;
  const [username, setUsername] = useState(draft.username ?? "");
  const [email, setEmail] = useState(draft.email ?? "");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [touched, setTouched] = useState(false);
  const save = useMutation({
    mutationFn: () => call(api.PUT("/setup/steps/care-admin", { body: { username, email, password } })),
    onSuccess: onDone,
  });
  const problem = asProblem(save.error);
  const local = {
    username:
      CARE_USERNAME_RE.test(username) && !/[._-]{2}/.test(username) ? undefined : strings.careAdmin.usernameInvalid,
    email: EMAIL_RE.test(email) ? undefined : strings.careAdmin.emailInvalid,
    ...validateNewPassword(password, confirm),
  };
  const errors: Partial<typeof local> = touched ? local : {};
  const server = {
    username: problem?.is("care-user-exists") ? strings.problems["care-user-exists"] : problem?.fieldError("/username"),
    email: problem?.fieldError("/email"),
    password: problem?.is("weak-password")
      ? (problem.fieldError("/password") ?? strings.problems["weak-password"])
      : undefined,
  };
  const showAlert = problem && !server.username && !server.email && !server.password;

  return (
    <form
      className="flex flex-col gap-2"
      noValidate
      onSubmit={(e) => {
        e.preventDefault();
        setTouched(true);
        if (Object.values(local).every((v) => !v)) save.mutate();
      }}
    >
      <StepIntro>{strings.careAdmin.intro}</StepIntro>
      {passwordNeeded ? (
        <Alert variant="warning" className="my-2">
          <InfoIcon />
          <AlertDescription>{strings.careAdmin.passwordNeeded}</AlertDescription>
        </Alert>
      ) : draft.password_set ? (
        <p className="my-2 text-xs text-soft-foreground">{strings.careAdmin.passwordSet}</p>
      ) : null}
      <FormField
        id={`${id}-user`}
        label={strings.careAdmin.username}
        help={strings.careAdmin.usernameHelp}
        error={errors.username ?? server.username}
      >
        <Input
          id={`${id}-user`}
          name="username"
          value={username}
          maxLength={16}
          autoComplete="off"
          autoCapitalize="none"
          spellCheck={false}
          className="max-w-xs"
          onChange={(e) => setUsername(e.target.value.toLowerCase())}
          aria-invalid={errors.username || server.username ? true : undefined}
          aria-describedby={describedBy(`${id}-user`, true, errors.username ?? server.username)}
        />
      </FormField>
      <FormField id={`${id}-email`} label={strings.careAdmin.email} error={errors.email ?? server.email}>
        <Input
          id={`${id}-email`}
          name="email"
          type="email"
          value={email}
          maxLength={254}
          autoComplete="email"
          onChange={(e) => setEmail(e.target.value.trim())}
          aria-invalid={errors.email || server.email ? true : undefined}
        />
      </FormField>
      <NewPasswordFields
        password={password}
        confirm={confirm}
        onPassword={setPassword}
        onConfirm={setConfirm}
        errors={{ password: errors.password ?? server.password, confirm: errors.confirm }}
        label={strings.careAdmin.password}
        help={strings.careAdmin.passwordHelp}
      />
      {showAlert ? <ProblemAlert error={save.error} /> : null}
      <StepActions onBack={onBack} busy={save.isPending} />
    </form>
  );
}
