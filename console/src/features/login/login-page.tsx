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
import { FocusCard } from "@/components/v2/focus";
import { publicJobId } from "@/app/public-job";
import { Spinner } from "@/components/ui/spinner";
import { clearDrHandoff, readDrHandoff } from "@/features/setup/dr-handoff";
import { homeFor } from "@/features/setup/wizard";
import { fmt, strings } from "@/lib/strings";
import { LINK_TONE } from "@/components/app/link-tone";
import { cn } from "@/lib/utils";

export default function LoginPage() {
  const id = useId();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  // After the restore of a new device, the console account of the old device is back (docs/api/README.md 13.9.3).
  // The page of the restore does not exist any more: the login opens the start page.
  const [restored] = useState(readDrHandoff);
  const next = restored ? null : safeNext(params.get("next"));
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
      clearDrHandoff();
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

  // S67: a job runs (the setup finish, an update): the progress is one tap away.
  const busy = publicJobId(pub.data);
  const wrong = problem?.is("bad-credentials") ? (needTotp ? strings.login.badTotp : strings.login.badPassword) : null;
  const showAlert = problem && !problem.is("bad-credentials", "totp-required");

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!password) return;
    login.mutate(needTotp ? { password, totp_code: totp } : { password });
  };

  return (
    <FocusCard
      illustration="pi-box"
      title={strings.login.title}
      description={strings.login.intro}
      footer={
        <div className="flex flex-col items-start gap-1">
          <Button asChild variant="link" className={cn("h-auto px-0", LINK_TONE)}>
            <Link to={next ? `/recover?next=${encodeURIComponent(next)}` : "/recover"}>
              {strings.login.useRecovery}
            </Link>
          </Button>
          <p className="type-small text-ink-soft">{strings.login.resetHint}</p>
        </div>
      }
    >
      {restored ? (
        <div
          className="flex flex-col gap-1 rounded-card border border-ok-border bg-ok-bg p-4 type-body text-ok-fg"
          data-testid="dr-restored"
        >
          <p>{fmt(strings.dr.restored, { name: restored.name || pub.data?.display_name || "" })}</p>
          {restored.consoleUrl && new URL(restored.consoleUrl).host !== window.location.host ? (
            <p>
              <a href={restored.consoleUrl} className="underline underline-offset-4">
                {fmt(strings.dr.restoredMoved, { url: restored.consoleUrl })}
              </a>
            </p>
          ) : null}
        </div>
      ) : null}
      {busy ? (
        <Link
          to={`/jobs/${encodeURIComponent(busy)}`}
          className="flex items-center gap-3 rounded-card border border-warn-border bg-warn-bg p-4 type-body text-warn-fg outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
          data-testid="login-busy"
        >
          <Spinner className="size-4 shrink-0" />
          <span className="min-w-0 flex-1">{strings.login.busy}</span>
        </Link>
      ) : null}
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
        <Button
          type="submit"
          size="lg"
          className="w-full"
          disabled={login.isPending || wait > 0 || (needTotp && totp.length !== 6)}
        >
          {login.isPending ? <Spinner data-icon="inline-start" /> : null}
          {strings.login.submit}
        </Button>
      </form>
    </FocusCard>
  );
}
