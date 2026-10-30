import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useId, useState } from "react";
import { Link, Navigate, useNavigate, useSearchParams } from "react-router";
import { api, call } from "@/api/client";
import { asProblem } from "@/api/problem";
import { usePublicStatus, useSession } from "@/api/queries";
import { safeNext, useStoreSession } from "@/app/session";
import { FormField, PasswordInput } from "@/components/app/form-field";
import { ProblemAlert } from "@/components/app/problem-alert";
import { useRetryWait } from "@/hooks/use-retry-wait";
import { TotpInput } from "@/components/app/totp-input";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Spinner } from "@/components/ui/spinner";
import { homeFor } from "@/features/setup/wizard";
import { strings } from "@/lib/strings";

export default function LoginPage() {
  const id = useId();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const next = safeNext(params.get("next"));
  const storeSession = useStoreSession();
  const session = useSession();
  const pub = usePublicStatus(session.data === null);
  const [password, setPassword] = useState("");
  const [totp, setTotp] = useState("");
  const [needTotp, setNeedTotp] = useState(false);

  const login = useMutation({
    mutationFn: (body: { password: string; totp_code?: string }) => call(api.POST("/session/login", { body })),
    onSuccess: (s) => {
      qc.removeQueries({ predicate: (q) => q.queryKey[0] !== "session" });
      storeSession(s);
      navigate(next ?? homeFor(s.setup_state, true), { replace: true });
    },
    onError: (e) => {
      if (asProblem(e)?.is("totp-required")) setNeedTotp(true);
    },
  });
  const wait = useRetryWait(login.error);
  const problem = asProblem(login.error);

  if (session.data) return <Navigate to={next ?? homeFor(session.data.setup_state, true)} replace />;
  if (pub.data?.setup_state === "unclaimed" || problem?.is("not-claimed")) return <Navigate to="/claim" replace />;

  const wrong = problem?.is("bad-credentials") ? (needTotp ? strings.login.badTotp : strings.login.badPassword) : null;
  const showAlert = problem && !problem.is("bad-credentials", "totp-required");

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!password) return;
    login.mutate(needTotp ? { password, totp_code: totp } : { password });
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h1 className="text-xl">{strings.login.title}</h1>
        </CardTitle>
        <CardDescription>{strings.login.intro}</CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={submit} className="flex flex-col gap-2" noValidate>
          <FormField id={`${id}-pw`} label={strings.password.label} error={!needTotp ? wrong : null}>
            <PasswordInput
              id={`${id}-pw`}
              name="password"
              autoComplete="current-password"
              autoFocus
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              aria-invalid={wrong && !needTotp ? true : undefined}
            />
          </FormField>
          {needTotp ? (
            <FormField id={`${id}-totp`} label={strings.login.totpLabel} help={strings.login.totpHelp} error={wrong}>
              <TotpInput id={`${id}-totp`} value={totp} onChange={setTotp} invalid={Boolean(wrong)} autoFocus />
            </FormField>
          ) : null}
          {showAlert ? <ProblemAlert error={login.error} className="mb-2" /> : null}
          <Button type="submit" disabled={login.isPending || wait > 0 || (needTotp && totp.length !== 6)}>
            {login.isPending ? <Spinner data-icon="inline-start" /> : null}
            {strings.login.submit}
          </Button>
        </form>
      </CardContent>
      <CardFooter className="flex flex-col items-start gap-2">
        <Button asChild variant="link" className="px-0">
          <Link to={next ? `/recover?next=${encodeURIComponent(next)}` : "/recover"}>{strings.login.useRecovery}</Link>
        </Button>
        <p className="text-xs text-soft-foreground">{strings.login.resetHint}</p>
      </CardFooter>
    </Card>
  );
}
