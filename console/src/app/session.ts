import { useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { storeSession } from "@/api/queries";
import type { Session } from "@/api/types";

export function loginPath(location: { pathname: string; search: string }): string {
  const next = location.pathname + location.search;
  return next && next !== "/" ? `/login?next=${encodeURIComponent(next)}` : "/login";
}

// Only a path on this origin: "/x" but not "//host" or "https://host".
export function safeNext(next: string | null): string | null {
  return next && next.startsWith("/") && !next.startsWith("//") && !next.startsWith("/\\") ? next : null;
}

export function useStoreSession() {
  const qc = useQueryClient();
  return useCallback((session: Session | null) => storeSession(qc, session), [qc]);
}
