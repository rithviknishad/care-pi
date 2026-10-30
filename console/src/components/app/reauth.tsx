import { useCallback, useId, useMemo, useRef, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { api, call } from "@/api/client";
import { asProblem } from "@/api/problem";
import { storeSession } from "@/api/queries";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";
import { FormField, PasswordInput } from "@/components/app/form-field";
import { ProblemAlert } from "@/components/app/problem-alert";
import { useRetryWait } from "@/hooks/use-retry-wait";
import { strings } from "@/lib/strings";
import { ReauthCancelled, ReauthContext } from "./reauth-context";

export function ReauthProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient();
  const id = useId();
  const [open, setOpen] = useState(false);
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const pending = useRef<{ resolve: () => void; reject: (e: Error) => void } | null>(null);
  const wait = useRetryWait(error);

  const ask = useCallback(
    () =>
      new Promise<void>((resolve, reject) => {
        pending.current = { resolve, reject };
        setPassword("");
        setError(null);
        setOpen(true);
      }),
    [],
  );

  const withReauth = useCallback(
    async <T,>(fn: () => Promise<T>): Promise<T> => {
      try {
        return await fn();
      } catch (e) {
        if (!asProblem(e)?.is("reauth-required")) throw e;
        await ask();
        return fn();
      }
    },
    [ask],
  );

  const close = () => {
    setOpen(false);
    pending.current?.reject(new ReauthCancelled());
    pending.current = null;
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const session = await call(api.POST("/session/reauth", { body: { password } }));
      storeSession(qc, session);
      setOpen(false);
      pending.current?.resolve();
      pending.current = null;
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };

  const value = useMemo(() => ({ withReauth }), [withReauth]);
  const wrong = asProblem(error)?.is("bad-credentials");

  return (
    <ReauthContext.Provider value={value}>
      {children}
      <Dialog open={open} onOpenChange={(next) => (next ? setOpen(true) : close())}>
        <DialogContent>
          <form onSubmit={submit} className="flex flex-col gap-4">
            <DialogHeader>
              <DialogTitle>{strings.reauth.title}</DialogTitle>
              <DialogDescription>{strings.reauth.intro}</DialogDescription>
            </DialogHeader>
            <FormField id={`${id}-pw`} label={strings.password.label} error={wrong ? strings.login.badPassword : null}>
              <PasswordInput
                id={`${id}-pw`}
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                aria-invalid={wrong ? true : undefined}
              />
            </FormField>
            {error && !wrong ? <ProblemAlert error={error} /> : null}
            <DialogFooter>
              <Button type="button" variant="outline" onClick={close}>
                {strings.common.cancel}
              </Button>
              <Button type="submit" disabled={busy || password.length === 0 || wait > 0}>
                {busy ? <Spinner data-icon="inline-start" /> : null}
                {strings.reauth.submit}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </ReauthContext.Provider>
  );
}
