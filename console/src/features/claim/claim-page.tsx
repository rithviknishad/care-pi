import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useId, useState } from "react";
import { Link, Navigate, useNavigate } from "react-router";
import { api, call } from "@/api/client";
import { asProblem } from "@/api/problem";
import { qk, usePublicStatus, useSession } from "@/api/queries";
import type { ClaimMethod } from "@/api/types";
import { useStoreSession } from "@/app/session";
import { FormField, NewPasswordFields, PasswordInput } from "@/components/app/form-field";
import { describedBy, validateNewPassword } from "@/lib/form";
import { ProblemAlert } from "@/components/app/problem-alert";
import { useRetryWait } from "@/hooks/use-retry-wait";
import { RecoveryCodesPanel } from "@/components/app/recovery-codes";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { formatClaimCode, looksLikeClaimCode } from "@/lib/codes";
import { strings } from "@/lib/strings";

export default function ClaimPage() {
  const id = useId();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const storeSession = useStoreSession();
  const session = useSession();
  const pub = usePublicStatus();
  // null: the first method of claim_methods. The agent lists claim-code first when the installer printed the code.
  const [method, setMethod] = useState<ClaimMethod | null>(null);
  const [autoSwitched, setAutoSwitched] = useState(false);
  const [secret, setSecret] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [touched, setTouched] = useState(false);
  const [codes, setCodes] = useState<{ codes: string[]; at: string } | null>(null);

  const claim = useMutation({
    mutationFn: (body: { method: ClaimMethod; secret: string; new_password: string }) =>
      call(api.POST("/session/claim", { body })),
    onSuccess: (res) => {
      storeSession(res.session);
      setCodes({ codes: res.recovery_codes, at: res.session.created_at });
      void qc.invalidateQueries({ queryKey: qk.publicStatus });
    },
  });
  const wait = useRetryWait(claim.error);
  const problem = asProblem(claim.error);

  if (codes) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>
            <h1>{strings.codes.title}</h1>
          </CardTitle>
        </CardHeader>
        <CardContent>
          <RecoveryCodesPanel
            codes={codes.codes}
            generatedAt={codes.at}
            onContinue={() => navigate("/setup/recovery-codes", { replace: true })}
          />
        </CardContent>
      </Card>
    );
  }

  if (session.data) return <Navigate to="/" replace />;
  if (problem?.is("already-claimed")) return <Navigate to="/login" replace />;

  if (pub.isPending) {
    return (
      <Card aria-busy="true">
        <CardHeader>
          <Skeleton className="h-6 w-48" />
          <Skeleton className="h-10 w-full" />
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <Skeleton className="h-16" />
          <Skeleton className="h-16" />
          <Skeleton className="h-16" />
        </CardContent>
      </Card>
    );
  }
  if (pub.isError) return <ProblemAlert error={pub.error} />;

  const methods = pub.data.claim_methods;
  if (pub.data.setup_state !== "unclaimed" || methods.length === 0) {
    return (
      <Alert>
        <AlertDescription className="flex flex-col items-start gap-3">
          <p>{strings.claim.noMethods}</p>
          <Button asChild variant="outline">
            <Link to="/login">{strings.claim.goLogin}</Link>
          </Button>
        </AlertDescription>
      </Alert>
    );
  }

  const current: ClaimMethod = method && methods.includes(method) ? method : (methods[0] ?? "claim-code");
  const other = methods.find((m) => m !== current);
  const pwErrors = touched ? validateNewPassword(password, confirm) : {};
  const secretWrong = problem?.is("bad-credentials") ? strings.claim.badSecret : null;
  const weak = problem?.is("weak-password")
    ? (problem.fieldError("/new_password") ?? strings.problems["weak-password"])
    : undefined;
  const showAlert = problem && !problem.is("bad-credentials", "weak-password");

  const onOsPassword = (value: string) => {
    // The operator has the claim code of the installer: show the claim code field. An
    // operator who chose the Imager password with the link keeps that field.
    if (method === null && methods.includes("claim-code") && looksLikeClaimCode(value)) {
      setMethod("claim-code");
      setAutoSwitched(true);
      setSecret(formatClaimCode(value));
      claim.reset();
      return;
    }
    setSecret(value);
  };

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    setTouched(true);
    const errs = validateNewPassword(password, confirm);
    if (errs.password || errs.confirm || secret.trim() === "") return;
    claim.mutate({
      method: current,
      secret: current === "claim-code" ? formatClaimCode(secret) : secret,
      new_password: password,
    });
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h1 className="text-xl">{strings.claim.title}</h1>
        </CardTitle>
        <CardDescription>{strings.claim.intro}</CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={submit} className="flex flex-col gap-2" noValidate>
          {current === "claim-code" ? (
            <FormField
              id={`${id}-secret`}
              label={strings.claim.codeLabel}
              help={strings.claim.codeHelp}
              error={secretWrong}
            >
              <Input
                id={`${id}-secret`}
                name="claim-code"
                value={secret}
                onChange={(e) => setSecret(formatClaimCode(e.target.value))}
                placeholder="XXXX-XXXX-XXXX"
                autoComplete="one-time-code"
                autoCapitalize="characters"
                spellCheck={false}
                className="tracking-widest uppercase"
                autoFocus={autoSwitched}
                aria-invalid={secretWrong ? true : undefined}
                aria-describedby={describedBy(`${id}-secret`, true, secretWrong)}
              />
            </FormField>
          ) : (
            <FormField
              id={`${id}-secret`}
              label={strings.claim.osPasswordLabel}
              help={strings.claim.osPasswordHelp}
              error={secretWrong}
            >
              <PasswordInput
                id={`${id}-secret`}
                name="os-password"
                value={secret}
                onChange={(e) => onOsPassword(e.target.value)}
                autoComplete="off"
                aria-invalid={secretWrong ? true : undefined}
                aria-describedby={describedBy(`${id}-secret`, true, secretWrong)}
              />
            </FormField>
          )}
          {other ? (
            <Button
              type="button"
              variant="link"
              className="-mt-3 mb-2 h-auto self-start p-0"
              onClick={() => {
                setMethod(other);
                setAutoSwitched(false);
                setSecret("");
                claim.reset();
              }}
            >
              {other === "claim-code" ? strings.claim.useCode : strings.claim.useOsPassword}
            </Button>
          ) : null}
          <NewPasswordFields
            password={password}
            confirm={confirm}
            onPassword={setPassword}
            onConfirm={setConfirm}
            errors={{ password: pwErrors.password ?? weak, confirm: pwErrors.confirm }}
          />
          {showAlert ? <ProblemAlert error={claim.error} className="mb-2" /> : null}
          <Button type="submit" disabled={claim.isPending || wait > 0}>
            {claim.isPending ? <Spinner data-icon="inline-start" /> : null}
            {strings.claim.submit}
          </Button>
        </form>
      </CardContent>
    </Card>
  );
}
