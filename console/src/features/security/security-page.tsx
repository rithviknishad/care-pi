import { useMutation, useQueryClient } from "@tanstack/react-query";
import { KeyRoundIcon, ShieldCheckIcon, ShieldOffIcon } from "lucide-react";
import { useId, useState } from "react";
import { toast } from "sonner";
import { api, call } from "@/api/client";
import { asProblem } from "@/api/problem";
import { qk, storeSession, useSecurity, useSessions } from "@/api/queries";
import type { RecoveryCodes, TotpEnrolment } from "@/api/types";
import { CopyButton } from "@/components/app/copy-button";
import { FormField, NewPasswordFields, PasswordInput } from "@/components/app/form-field";
import { validateNewPassword } from "@/lib/form";
import { DataList, DataRow, PageHeader } from "@/components/app/page";
import { ProblemAlert } from "@/components/app/problem-alert";
import { useRetryWait } from "@/hooks/use-retry-wait";
import { QrCode } from "@/components/app/qr-code";
import { isReauthCancelled, useReauth } from "@/components/app/reauth-context";
import { RecoveryCodesPanel } from "@/components/app/recovery-codes";
import { TotpInput } from "@/components/app/totp-input";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatDateTime, formatRelative } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";

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
    <Card size="sm">
      <CardHeader>
        <CardTitle>
          <h2>{sc.password}</h2>
        </CardTitle>
        <CardDescription className="min-h-5">
          {security.data ? fmt(sc.passwordSet, { time: formatRelative(security.data.password.set_at) }) : ""}
        </CardDescription>
      </CardHeader>
      <CardContent>
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
      </CardContent>
    </Card>
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
    <Card size="sm">
      <CardHeader>
        <CardTitle>
          <h2>{sc.codes}</h2>
        </CardTitle>
        <CardDescription className="min-h-5">
          {security.data ? fmt(sc.codesRemaining, { count: security.data.recovery_codes.remaining }) : ""}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {codes ? (
          <RecoveryCodesPanel
            codes={codes.codes}
            generatedAt={codes.generated_at}
            intro={strings.codes.regenerated}
            onContinue={() => setCodes(null)}
          />
        ) : (
          <>
            <p className="text-xs text-soft-foreground">{sc.regenerateHelp}</p>
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
      </CardContent>
    </Card>
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
    <Card size="sm">
      <CardHeader>
        <CardTitle>
          <h2>{sc.totp}</h2>
        </CardTitle>
        <CardDescription className="min-h-5">
          {security.data
            ? enabled
              ? fmt(sc.totpOn, { time: formatDateTime(security.data.totp.enrolled_at) })
              : sc.totpOff
            : ""}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {enrolment ? (
          <form
            className="grid gap-4 sm:grid-cols-[12rem_minmax(0,1fr)]"
            onSubmit={(e) => {
              e.preventDefault();
              if (code.length === 6) verify.mutate();
            }}
          >
            <QrCode value={enrolment.otpauth_uri} className="w-48" label={sc.totpScan} />
            <div className="flex min-w-0 flex-col gap-3">
              <p className="text-sm">{sc.totpScan}</p>
              <div className="flex items-center gap-2">
                <span className="text-xs text-soft-foreground">{sc.totpKey}</span>
                <code className="truncate text-sm tracking-wider">{enrolment.secret.match(/.{1,4}/g)?.join(" ")}</code>
                <CopyButton value={enrolment.secret} />
              </div>
              <p className="text-xs text-soft-foreground">
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
      </CardContent>
    </Card>
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
    <Card size="sm">
      <CardHeader>
        <CardTitle>
          <h2>{sc.sessions}</h2>
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {sessions.isError ? <ProblemAlert error={sessions.error} /> : null}
        {revoke.error ? <ProblemAlert error={revoke.error} /> : null}
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{sc.address}</TableHead>
              <TableHead>{sc.method}</TableHead>
              <TableHead>{sc.created}</TableHead>
              <TableHead>{sc.lastSeen}</TableHead>
              <TableHead>{sc.browser}</TableHead>
              <TableHead className="w-[10ch]" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {sessions.data
              ? sessions.data.items.map((s) => (
                  <TableRow key={s.ref} className="h-11">
                    <TableCell>{s.address}</TableCell>
                    <TableCell>{sc.methods[s.method] ?? s.method}</TableCell>
                    <TableCell>{formatDateTime(s.created_at)}</TableCell>
                    <TableCell>{formatRelative(s.last_seen_at)}</TableCell>
                    <TableCell className="max-w-[28ch] truncate" title={s.user_agent}>
                      {s.user_agent}
                    </TableCell>
                    <TableCell className="text-right">
                      {s.current ? (
                        <Badge variant="success" size="sm">
                          {sc.current}
                        </Badge>
                      ) : (
                        <Button
                          type="button"
                          variant="outline"
                          size="xs"
                          onClick={() => revoke.mutate(s.ref)}
                          disabled={revoke.isPending}
                        >
                          {sc.revoke}
                        </Button>
                      )}
                    </TableCell>
                  </TableRow>
                ))
              : [0, 1].map((i) => (
                  <TableRow key={i} className="h-11">
                    <TableCell colSpan={6}>
                      <Skeleton className="h-4" />
                    </TableCell>
                  </TableRow>
                ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}

function ClaimCard() {
  const security = useSecurity();
  const s = security.data;
  return (
    <Card size="sm">
      <CardHeader>
        <CardTitle>
          <h2>{sc.claim}</h2>
        </CardTitle>
      </CardHeader>
      <CardContent>
        <DataList>
          <DataRow
            label={sc.claim}
            loading={!s}
            value={s ? fmt(sc.claimRecord, { address: s.claim.address, time: formatDateTime(s.claim.claimed_at) }) : ""}
          />
          <DataRow label={sc.browser} loading={!s} value={s?.claim.user_agent} title={s?.claim.user_agent} />
          <DataRow
            label={sc.lockout}
            loading={!s}
            value={
              s
                ? s.lockout.locked_until
                  ? fmt(sc.lockedUntil, { time: formatDateTime(s.lockout.locked_until) })
                  : fmt(sc.notLocked, { count: s.lockout.consecutive_failures })
                : ""
            }
          />
        </DataList>
      </CardContent>
    </Card>
  );
}

export default function SecurityPage() {
  return (
    <div className="flex flex-col gap-4">
      <PageHeader title={sc.title} />
      <div className="grid gap-4 xl:grid-cols-2">
        <PasswordCard />
        <div className="flex flex-col gap-4">
          <CodesCard />
          <TotpCard />
        </div>
      </div>
      <SessionsCard />
      <ClaimCard />
    </div>
  );
}
