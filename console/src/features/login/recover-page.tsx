import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useId, useState } from "react";
import { Link, Navigate, useNavigate, useSearchParams } from "react-router";
import { toast } from "sonner";
import { api, call } from "@/api/client";
import { asProblem } from "@/api/problem";
import { useSession } from "@/api/queries";
import { safeNext, useStoreSession } from "@/app/session";
import { FormField, NewPasswordFields } from "@/components/app/form-field";
import { describedBy, validateNewPassword } from "@/lib/form";
import { ProblemAlert } from "@/components/app/problem-alert";
import { useRetryWait } from "@/hooks/use-retry-wait";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import { homeFor } from "@/features/setup/wizard";
import { formatRecoveryCode, isCompleteRecoveryCode } from "@/lib/codes";
import { fmt, strings } from "@/lib/strings";

export default function RecoverPage() {
  const id = useId();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const next = safeNext(params.get("next"));
  const storeSession = useStoreSession();
  const session = useSession();
  const [code, setCode] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [touched, setTouched] = useState(false);

  const recover = useMutation({
    mutationFn: (body: { recovery_code: string; new_password: string }) => call(api.POST("/session/recover", { body })),
    onSuccess: (res) => {
      qc.removeQueries({ predicate: (q) => q.queryKey[0] !== "session" });
      storeSession(res.session);
      if (res.recovery_codes_remaining <= 2) {
        toast.warning(fmt(strings.recover.lowCodes, { count: res.recovery_codes_remaining }), { duration: 10_000 });
      }
      navigate(next ?? homeFor(res.session.setup_state, true), { replace: true });
    },
  });
  const wait = useRetryWait(recover.error);
  const problem = asProblem(recover.error);

  if (session.data && !recover.isSuccess) return <Navigate to={homeFor(session.data.setup_state, true)} replace />;
  if (problem?.is("not-claimed")) return <Navigate to="/claim" replace />;

  const pwErrors = touched ? validateNewPassword(password, confirm) : {};
  const codeError = problem?.is("bad-credentials") ? strings.recover.badCode : null;
  const weak = problem?.is("weak-password")
    ? (problem.fieldError("/new_password") ?? strings.problems["weak-password"])
    : undefined;
  const showAlert = problem && !problem.is("bad-credentials", "weak-password");

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    setTouched(true);
    const errs = validateNewPassword(password, confirm);
    if (errs.password || errs.confirm || !isCompleteRecoveryCode(code)) return;
    recover.mutate({ recovery_code: code, new_password: password });
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h1 className="text-xl">{strings.recover.title}</h1>
        </CardTitle>
        <CardDescription>{strings.recover.intro}</CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={submit} className="flex flex-col gap-2" noValidate>
          <FormField
            id={`${id}-code`}
            label={strings.recover.codeLabel}
            help={strings.recover.codeHelp}
            error={codeError}
          >
            <Input
              id={`${id}-code`}
              name="recovery-code"
              value={code}
              onChange={(e) => setCode(formatRecoveryCode(e.target.value))}
              placeholder="XXXXX-XXXXX"
              autoComplete="one-time-code"
              autoCapitalize="characters"
              spellCheck={false}
              className="tracking-widest uppercase"
              aria-invalid={codeError ? true : undefined}
              aria-describedby={describedBy(`${id}-code`, true, codeError)}
            />
          </FormField>
          <NewPasswordFields
            password={password}
            confirm={confirm}
            onPassword={setPassword}
            onConfirm={setConfirm}
            errors={{ password: pwErrors.password ?? weak, confirm: pwErrors.confirm }}
          />
          {showAlert ? <ProblemAlert error={recover.error} className="mb-2" /> : null}
          <Button type="submit" disabled={recover.isPending || wait > 0 || !isCompleteRecoveryCode(code)}>
            {recover.isPending ? <Spinner data-icon="inline-start" /> : null}
            {strings.recover.submit}
          </Button>
        </form>
      </CardContent>
      <CardFooter>
        <Button asChild variant="link" className="px-0">
          <Link to="/login">{strings.recover.backToLogin}</Link>
        </Button>
      </CardFooter>
    </Card>
  );
}
