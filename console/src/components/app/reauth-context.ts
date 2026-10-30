import { createContext, useContext } from "react";

export class ReauthCancelled extends Error {
  constructor() {
    super("reauthentication cancelled");
    this.name = "ReauthCancelled";
  }
}

export interface ReauthApi {
  // Runs fn. On 403 reauth-required, it asks for the password, then runs fn one more time.
  withReauth: <T>(fn: () => Promise<T>) => Promise<T>;
}

export const ReauthContext = createContext<ReauthApi | null>(null);

export function useReauth(): ReauthApi {
  const ctx = useContext(ReauthContext);
  if (!ctx) throw new Error("useReauth needs ReauthProvider");
  return ctx;
}

export function isReauthCancelled(e: unknown): boolean {
  return e instanceof ReauthCancelled;
}
