import { useMutation, useQueryClient } from "@tanstack/react-query";
import { KeyRoundIcon, ShieldCheckIcon, ShieldOffIcon } from "lucide-react";
import { useId, useState } from "react";
import { toast } from "sonner";
import { api, call } from "@/api/client";
import { asProblem } from "@/api/problem";
import { qk, storeSession, useSecurity, useSessions } from "@/api/queries";
import type { RecoveryCodes, TotpEnrolment } from "@/api/types";
import { CopyButton } from "@/components/v2/copy-button";
import { FormField, NewPasswordFields, PasswordInput } from "@/components/app/form-field";
import { validateNewPassword } from "@/lib/form";
import { PageHeader } from "@/components/app/page";
import { KeyValueList } from "@/components/v2/key-value-list";
import { Panel } from "@/components/v2/panel";
import { StatusChip } from "@/components/v2/status-chip";
import { ProblemAlert } from "@/components/app/problem-alert";
import { useRetryWait } from "@/hooks/use-retry-wait";
import { QrCode } from "@/components/app/qr-code";
import { isReauthCancelled, useReauth } from "@/components/app/reauth-context";
import { RecoveryCodesPanel } from "@/components/app/recovery-codes";
import { TotpInput } from "@/components/app/totp-input";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { formatDateTime, formatRelative } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";
import { FirewallCard } from "./firewall-card";

const sc = strings.security;

function errorOf(e: unknown): unknown {
  return e && !isReauthCancelled(e) ? e : null;
}

function PasswordCard() {
  const id = useId();
  const qc = useQueryClient();
  const security = useSecurity();
  const [current, setCurrent] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [touched, setTouched] = useState(false);
  const change = useMutation({
    mutationFn: () =>
      call(api.PUT("/security/password", { body: { current_password: current, new_password: password } })),
    onSuccess: (session) => {
      storeSession(qc, session);
      setCurrent("");
      setPassword("");
      setConfirm("");
      setTouched(false);
      toast.success(sc.passwordChanged);
      void qc.invalidateQueries({ queryKey: qk.security });
      void qc.invalidateQueries({ queryKey: qk.sessions });
    },
  });
  const wait = useRetryWait(change.error);
  const problem = asProblem(change.error);
  const errors = touched ? validateNewPassword(password, confirm) : {};
  const wrongCurrent = problem?.is("bad-credentials") ? strings.login.badPassword : null;
  const weak = problem?.is("weak-password")
    ? (problem.fieldError("/new_password") ?? strings.problems["weak-password"])
    : undefined;
  return (
    <Panel
      title={sc.password}
      description={
        <span className="block min-h-5">
          {security.data ? fmt(sc.passwordSet, { time: formatRelative(security.data.password.set_at) }) : ""}
        </span>
      }
    >
      <form
        className="flex max-w-md flex-col gap-1"
        noValidate
        onSubmit={(e) => {
          e.preventDefault();
          setTouched(true);
          const v = validateNewPassword(password, confirm);
          if (!v.password && !v.confirm && current) change.mutate();
        }}
      >
        <FormField id={`${id}-current`} label={strings.password.currentLabel} error={wrongCurrent}>
          <PasswordInput
            id={`${id}-current`}
            autoComplete="current-password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
          />
        </FormField>
        <NewPasswordFields
          password={password}
          confirm={confirm}
          onPassword={setPassword}
          onConfirm={setConfirm}
          errors={{ password: errors.password ?? weak, confirm: errors.confirm }}
        />
        {problem && !problem.is("bad-credentials", "weak-password") ? <ProblemAlert error={change.error} /> : null}
        <Button type="submit" className="self-start" disabled={change.isPending || wait > 0}>
          {change.isPending ? <Spinner data-icon="inline-start" /> : null}
          {sc.changePassword}
        </Button>
      </form>
    </Panel>
  );
}

function CodesCard() {
  const qc = useQueryClient();
  const { withReauth } = useReauth();
  const security = useSecurity();
  const [codes, setCodes] = useState<RecoveryCodes | null>(null);
  const regenerate = useMutation({
    mutationFn: () => withReauth(() => call(api.POST("/security/recovery-codes"))),
    onSuccess: (c) => {
      setCodes(c);
      void qc.invalidateQueries({ queryKey: qk.security });
    },
  });
  return (
    <Panel
      title={sc.codes}
      description={
        <span className="block min-h-5">
          {security.data ? fmt(sc.codesRemaining, { count: security.data.recovery_codes.remaining }) : ""}
        </span>
      }
    >
      {codes ? (
        <RecoveryCodesPanel
          codes={codes.codes}
          generatedAt={codes.generated_at}
          intro={strings.codes.regenerated}
          onContinue={() => setCodes(null)}
        />
      ) : (
        <>
          <p className="type-small text-ink-muted">{sc.regenerateHelp}</p>
          <Button
            type="button"
            variant="outline"
            className="self-start"
            onClick={() => regenerate.mutate()}
            disabled={regenerate.isPending}
          >
            <KeyRoundIcon data-icon="inline-start" />
            {sc.regenerate}
          </Button>
          {errorOf(regenerate.error) ? <ProblemAlert error={regenerate.error} /> : null}
        </>
      )}
    </Panel>
  );
}

function TotpCard() {
  const id = useId();
  const qc = useQueryClient();
  const { withReauth } = useReauth();
  const security = useSecurity();
  const [enrolment, setEnrolment] = useState<TotpEnrolment | null>(null);
  const [code, setCode] = useState("");
  const enabled = security.data?.totp.enabled ?? false;
  const start = useMutation({
    mutationFn: () => withReauth(() => call(api.POST("/security/totp"))),
    onSuccess: (e) => {
      setEnrolment(e);
      setCode("");
    },
  });
  const verify = useMutation({
    mutationFn: () => call(api.POST("/security/totp/verify", { body: { code } })),
    onSuccess: (state) => {
      qc.setQueryData(qk.security, state);
      setEnrolment(null);
      toast.success(sc.totpEnabled);
    },
  });
  const disable = useMutation({
    mutationFn: () => withReauth(() => call(api.DELETE("/security/totp"))),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.security });
      toast.success(sc.totpDisabled);
    },
  });
  const wrong = asProblem(verify.error)?.is("invalid-request") ? sc.totpWrong : null;
  return (
    <Panel
      title={sc.totp}
      description={
        <span className="block min-h-5">
          {security.data
            ? enabled
              ? fmt(sc.totpOn, { time: formatDateTime(security.data.totp.enrolled_at) })
              : sc.totpOff
            : ""}
        </span>
      }
    >
      {enrolment ? (
        <form
          className="grid gap-4 rounded-card border border-line-soft bg-surface-tint p-4 sm:grid-cols-[12rem_minmax(0,1fr)] sm:p-5"
          onSubmit={(e) => {
            e.preventDefault();
            if (code.length === 6) verify.mutate();
          }}
        >
          <QrCode value={enrolment.otpauth_uri} className="w-48 border border-line p-1" label={sc.totpScan} />
          <div className="flex min-w-0 flex-col gap-3">
            <p className="type-body text-ink">{sc.totpScan}</p>
            <div className="flex min-w-0 items-center gap-2">
              <span className="type-small text-ink-soft">{sc.totpKey}</span>
              <code className="truncate type-data tracking-wider text-ink">
                {enrolment.secret.match(/.{1,4}/g)?.join(" ")}
              </code>
              <CopyButton text={enrolment.secret} iconOnly variant="ghost" />
            </div>
            <p className="type-small text-ink-soft">
              {fmt(sc.totpExpires, { time: formatDateTime(enrolment.expires_at) })}
            </p>
            <FormField id={`${id}-code`} label={sc.totpCode} error={wrong}>
              <TotpInput id={`${id}-code`} value={code} onChange={setCode} invalid={Boolean(wrong)} />
            </FormField>
            {verify.error && !wrong ? <ProblemAlert error={verify.error} /> : null}
            <div className="flex gap-2">
              <Button type="button" variant="outline" onClick={() => setEnrolment(null)}>
                {strings.common.cancel}
              </Button>
              <Button type="submit" disabled={code.length !== 6 || verify.isPending}>
                {sc.totpVerify}
              </Button>
            </div>
          </div>
        </form>
      ) : enabled ? (
        <Button
          type="button"
          variant="destructive"
          className="self-start"
          onClick={() => disable.mutate()}
          disabled={disable.isPending}
        >
          <ShieldOffIcon data-icon="inline-start" />
          {sc.totpDisable}
        </Button>
      ) : (
        <Button
          type="button"
          variant="outline"
          className="self-start"
          onClick={() => start.mutate()}
          disabled={start.isPending || !security.data}
        >
          <ShieldCheckIcon data-icon="inline-start" />
          {sc.totpEnable}
        </Button>
      )}
      {errorOf(start.error) ? <ProblemAlert error={start.error} /> : null}
      {errorOf(disable.error) ? <ProblemAlert error={disable.error} /> : null}
    </Panel>
  );
}

function SessionsCard() {
  const qc = useQueryClient();
  const sessions = useSessions();
  const revoke = useMutation({
    mutationFn: (ref: string) =>
      call(api.DELETE("/security/sessions/{session_ref}", { params: { path: { session_ref: ref } } })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: qk.sessions }),
  });
  return (
    <Panel title={sc.sessions}>
      {sessions.isError ? <ProblemAlert error={sessions.error} /> : null}
      {revoke.error ? <ProblemAlert error={revoke.error} /> : null}
      <ul className="flex flex-col divide-y divide-line-soft" data-testid="sessions">
        {sessions.data
          ? sessions.data.items.map((s) => (
              <li
                key={s.ref}
                className="grid min-h-16 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-1 py-3"
                data-current={s.current || undefined}
              >
                <div className="flex min-w-0 flex-col">
                  <span className="flex min-w-0 flex-wrap items-baseline gap-x-2">
                    <span className="type-data text-ink">{s.address}</span>
                    <span className="type-small text-ink-muted">{sc.methods[s.method] ?? s.method}</span>
                  </span>
                  <span className="truncate type-small text-ink-soft" title={s.user_agent}>
                    {s.user_agent}
                  </span>
                  <span className="type-small text-ink-soft">
                    {fmt(sc.sessionTimes, {
                      created: formatDateTime(s.created_at),
                      seen: formatRelative(s.last_seen_at),
                    })}
                  </span>
                </div>
                {s.current ? (
                  <StatusChip tone="ok" size="sm">
                    {sc.current}
                  </StatusChip>
                ) : (
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => revoke.mutate(s.ref)}
                    disabled={revoke.isPending}
                  >
                    {sc.revoke}
                  </Button>
                )}
              </li>
            ))
          : [0, 1].map((i) => (
              <li key={i} className="flex min-h-16 flex-col justify-center gap-2 py-3" aria-hidden="true">
                <Skeleton className="h-4 w-40" />
                <Skeleton className="h-3 w-64 max-w-full" />
              </li>
            ))}
      </ul>
    </Panel>
  );
}

function ClaimCard() {
  const security = useSecurity();
  const s = security.data;
  return (
    <Panel title={sc.claim}>
      <KeyValueList
        loading={!s}
        items={[
          {
            id: "claim",
            label: sc.claim,
            value: s ? fmt(sc.claimRecord, { address: s.claim.address, time: formatDateTime(s.claim.claimed_at) }) : "",
          },
          { id: "browser", label: sc.browser, value: s?.claim.user_agent, title: s?.claim.user_agent },
          {
            id: "lockout",
            label: sc.lockout,
            value: s
              ? s.lockout.locked_until
                ? fmt(sc.lockedUntil, { time: formatDateTime(s.lockout.locked_until) })
                : fmt(sc.notLocked, { count: s.lockout.consecutive_failures })
              : "",
          },
        ]}
      />
    </Panel>
  );
}

export default function SecurityPage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader title={sc.title} description={sc.intro} />
      <div className="grid gap-6 xl:grid-cols-2">
        <PasswordCard />
        <div className="flex flex-col gap-6">
          <CodesCard />
          <TotpCard />
        </div>
      </div>
      <FirewallCard />
      <SessionsCard />
      <ClaimCard />
    </div>
  );
}
