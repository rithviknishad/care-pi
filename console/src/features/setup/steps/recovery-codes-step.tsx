import { useMutation } from "@tanstack/react-query";
import { useId, useState } from "react";
import { api, call } from "@/api/client";
import { asProblem } from "@/api/problem";
import type { RecoveryCodes } from "@/api/types";
import { FormField } from "@/components/app/form-field";
import { ProblemAlert } from "@/components/app/problem-alert";
import { useReauth, isReauthCancelled } from "@/components/app/reauth-context";
import { RecoveryCodesPanel } from "@/components/app/recovery-codes";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { formatRecoveryCode, isCompleteRecoveryCode } from "@/lib/codes";
import { strings } from "@/lib/strings";
import { StepActions, StepIntro, type StepProps } from "../step-frame";

export function RecoveryCodesStep({ onDone, onBack }: StepProps) {
  const id = useId();
  const { withReauth } = useReauth();
  const [code, setCode] = useState("");
  const [fresh, setFresh] = useState<RecoveryCodes | null>(null);

  const confirm = useMutation({
    mutationFn: (confirm_code: string) => call(api.PUT("/setup/steps/recovery-codes", { body: { confirm_code } })),
    onSuccess: onDone,
  });
  const regenerate = useMutation({
    mutationFn: () => withReauth(() => call(api.POST("/security/recovery-codes"))),
    onSuccess: (codes) => setFresh(codes),
  });

  if (fresh) {
    return (
      <RecoveryCodesPanel
        codes={fresh.codes}
        generatedAt={fresh.generated_at}
        intro={strings.codes.regenerated}
        onContinue={() => {
          setFresh(null);
          setCode("");
          confirm.reset();
        }}
      />
    );
  }

  const wrong = asProblem(confirm.error)?.is("invalid-request") ? strings.recoveryStep.wrong : null;
  const regenError = regenerate.error && !isReauthCancelled(regenerate.error) ? regenerate.error : null;

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        if (isCompleteRecoveryCode(code)) confirm.mutate(code);
      }}
      noValidate
    >
      <StepIntro>{strings.recoveryStep.intro}</StepIntro>
      <FormField id={`${id}-code`} label={strings.recoveryStep.label} help={strings.recover.codeHelp} error={wrong}>
        <Input
          id={`${id}-code`}
          name="confirm-code"
          value={code}
          onChange={(e) => setCode(formatRecoveryCode(e.target.value))}
          placeholder="XXXXX-XXXXX"
          autoComplete="off"
          autoCapitalize="characters"
          spellCheck={false}
          className="max-w-xs tracking-widest uppercase"
          aria-invalid={wrong ? true : undefined}
        />
      </FormField>
      {confirm.error && !wrong ? <ProblemAlert error={confirm.error} /> : null}
      <details className="text-sm">
        <summary className="cursor-pointer">{strings.recoveryStep.lost}</summary>
        <div className="mt-3 flex flex-col items-start gap-3">
          <p className="text-soft-foreground">{strings.recoveryStep.lostIntro}</p>
          <Button type="button" variant="outline" onClick={() => regenerate.mutate()} disabled={regenerate.isPending}>
            {strings.recoveryStep.makeNew}
          </Button>
          {regenError ? <ProblemAlert error={regenError} /> : null}
        </div>
      </details>
      <StepActions onBack={onBack} busy={confirm.isPending} disabled={!isCompleteRecoveryCode(code)} />
    </form>
  );
}
