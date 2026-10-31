import { QueryClient, useInfiniteQuery, useQuery, useQueryClient, type QueryClient as QC } from "@tanstack/react-query";
import { api, call, callFull, root, setCsrfToken } from "./client";
import { ProblemError, asProblem } from "./problem";
import type { ClientOs, Config, LogUnit, PublicStatus, Session, SetupState } from "./types";

export const qk = {
  session: ["session"] as const,
  publicStatus: ["public-status"] as const,
  setup: ["setup"] as const,
  status: ["status"] as const,
  network: ["network"] as const,
  hostCheck: ["host-check"] as const,
  presets: ["presets"] as const,
  configSchema: ["config-schema"] as const,
  config: (view: "file" | "effective") => ["config", view] as const,
  security: ["security"] as const,
  sessions: ["sessions"] as const,
  logUnits: ["log-units"] as const,
  logs: (unit: LogUnit, n: number) => ["logs", unit, n] as const,
  fe: ["fe"] as const,
  care: ["care"] as const,
  tls: ["tls"] as const,
  jobs: ["jobs"] as const,
  job: (id: string) => ["job", id] as const,
  instructions: (os: ClientOs) => ["tls-instructions", os] as const,
  phase: (feature: string) => ["phase", feature] as const,
  // Phase 4. The keys below "updates" share the prefix, so one invalidation reads each of them again.
  updates: ["updates"] as const,
  updatesHistory: ["updates", "history"] as const,
  updatesOs: ["updates", "os"] as const,
  plugs: ["plugs"] as const,
  plugSchema: (id: string) => ["plugs", id, "settings-schema"] as const,
  solution: (name: string) => ["solution", name] as const,
};

export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // Do not repeat a request that the agent rejected. Repeat a network error 2 times.
        retry: (count, error) => {
          const p = asProblem(error);
          if (p && p.slug !== "network" && p.slug !== "unavailable") return false;
          return count < 2;
        },
        refetchOnWindowFocus: false,
        staleTime: 5_000,
      },
      mutations: { retry: false },
    },
  });
}

// Keeps the CSRF token and the session in the cache together.
export function storeSession(qc: QC, session: Session | null): void {
  setCsrfToken(session?.csrf_token ?? null);
  qc.setQueryData(qk.session, session);
}

export function storeSetup(qc: QC, setup: SetupState): void {
  qc.setQueryData(qk.setup, setup);
  const session = qc.getQueryData<Session | null>(qk.session);
  if (session && session.setup_state !== setup.state)
    qc.setQueryData(qk.session, { ...session, setup_state: setup.state });
}

export function useSession() {
  return useQuery({
    queryKey: qk.session,
    queryFn: async (): Promise<Session | null> => {
      try {
        const session = await call(api.GET("/session"));
        setCsrfToken(session.csrf_token);
        return session;
      } catch (error) {
        if (asProblem(error)?.is("not-authenticated")) return null;
        throw error;
      }
    },
    staleTime: Infinity,
    retry: (count, error) => asProblem(error)?.slug === "network" && count < 2,
  });
}

export function usePublicStatus(enabled = true) {
  return useQuery({
    queryKey: qk.publicStatus,
    queryFn: () => call(root.GET("/_care-pi/status")),
    enabled,
    staleTime: 2_000,
  });
}

export function useSetup() {
  const qc = useQueryClient();
  return useQuery({
    queryKey: qk.setup,
    queryFn: async () => {
      const setup = await call(api.GET("/setup"));
      storeSetup(qc, setup);
      return setup;
    },
  });
}

export function useStatus(refetchInterval: number | false = 10_000) {
  return useQuery({
    queryKey: qk.status,
    queryFn: () => call(api.GET("/status")),
    refetchInterval,
    refetchIntervalInBackground: false,
  });
}

export function useNetwork(enabled = true) {
  return useQuery({
    queryKey: qk.network,
    queryFn: () => call(api.GET("/network")),
    enabled,
    staleTime: 30_000,
    refetchInterval: 60_000,
  });
}

export function useHostCheck() {
  return useQuery({ queryKey: qk.hostCheck, queryFn: () => call(api.GET("/host/check")), staleTime: 60_000 });
}

export function usePresets() {
  return useQuery({ queryKey: qk.presets, queryFn: () => call(api.GET("/config/presets")), staleTime: Infinity });
}

export type JsonSchema = Record<string, unknown>;

export function useConfigSchema() {
  return useQuery({
    queryKey: qk.configSchema,
    queryFn: async () => (await call(api.GET("/config/schema"))) as JsonSchema,
    staleTime: Infinity,
  });
}

export interface ConfigWithEtag {
  config: Config;
  etag: string;
}

export function useConfig(view: "file" | "effective") {
  return useQuery({
    queryKey: qk.config(view),
    queryFn: async (): Promise<ConfigWithEtag> => {
      const { data, response } = await callFull(api.GET("/config", { params: { query: { view } } }));
      return { config: data, etag: response.headers.get("ETag") ?? "" };
    },
    staleTime: 0,
  });
}

export function useSecurity() {
  return useQuery({ queryKey: qk.security, queryFn: () => call(api.GET("/security")) });
}

export function useSessions() {
  return useQuery({ queryKey: qk.sessions, queryFn: () => call(api.GET("/security/sessions")) });
}

export function useLogUnits() {
  return useQuery({ queryKey: qk.logUnits, queryFn: () => call(api.GET("/logs")), staleTime: Infinity });
}

export function useFe() {
  return useQuery({ queryKey: qk.fe, queryFn: () => call(api.GET("/fe")) });
}

export function useCare(enabled = true) {
  return useQuery({ queryKey: qk.care, queryFn: () => call(api.GET("/care")), enabled });
}

export function useTls(enabled = true) {
  return useQuery({ queryKey: qk.tls, queryFn: () => call(api.GET("/tls")), enabled });
}

export function useJobs() {
  return useQuery({
    queryKey: qk.jobs,
    queryFn: () => call(api.GET("/jobs", { params: { query: { limit: 10 } } })),
  });
}

export function useInstructions(os: ClientOs) {
  return useQuery({
    queryKey: qk.instructions(os),
    queryFn: () => call(api.GET("/tls/instructions/{os}", { params: { path: { os } } })),
    staleTime: 60_000,
  });
}

// A resource of a later phase. The agent answers 501 not-implemented-in-this-version.
export type PhaseFeature = "backups" | "restore";

export function usePhaseProbe(feature: PhaseFeature) {
  return useQuery({
    queryKey: qk.phase(feature),
    queryFn: async () => {
      if (feature === "backups") return call(api.GET("/backups"));
      return call(api.GET("/restore/points"));
    },
    retry: false,
    staleTime: Infinity,
  });
}

// The maintenance state for the top bar: GET /_care-pi/status (the same data as the maintenance page of Caddy).
// It reads each 15 s, and each 3 s while a job holds the apply lock or the maintenance page shows
// (public reads: 60 each minute for each address; docs/api/README.md section 4).
export function useMaintenance(enabled = true) {
  return useQuery({
    queryKey: qk.publicStatus,
    queryFn: () => call(root.GET("/_care-pi/status")),
    enabled,
    staleTime: 2_000,
    refetchInterval: (query) => {
      const data = query.state.data as PublicStatus | undefined;
      return data?.maintenance?.active || data?.job ? 3_000 : 15_000;
    },
    refetchIntervalInBackground: false,
  });
}

// GET /updates: the whole Updates page in one call. It reads each 30 s and updates the page in place.
export function useUpdates() {
  return useQuery({
    queryKey: qk.updates,
    queryFn: () => call(api.GET("/updates")),
    refetchInterval: 30_000,
    refetchIntervalInBackground: false,
  });
}

export const HISTORY_PAGE = 20;

export function useUpdateHistory(enabled = true) {
  return useInfiniteQuery({
    queryKey: qk.updatesHistory,
    queryFn: ({ pageParam }) =>
      call(
        api.GET("/updates/history", {
          params: { query: { limit: HISTORY_PAGE, ...(pageParam ? { before: pageParam } : {}) } },
        }),
      ),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_before ?? undefined,
    enabled,
  });
}

export function useOsUpdates(enabled = true) {
  return useQuery({ queryKey: qk.updatesOs, queryFn: () => call(api.GET("/updates/os")), enabled });
}

export function usePlugs() {
  return useQuery({ queryKey: qk.plugs, queryFn: () => call(api.GET("/plugs")) });
}

export function usePlugSettingsSchema(id: string, enabled = true) {
  return useQuery({
    queryKey: qk.plugSchema(id),
    queryFn: async () =>
      (await call(api.GET("/plugs/{plug_id}/settings-schema", { params: { path: { plug_id: id } } }))) as JsonSchema,
    enabled,
    staleTime: 60_000,
  });
}

export function useSolution(name: string | null | undefined) {
  return useQuery({
    queryKey: qk.solution(name ?? ""),
    queryFn: () => call(api.GET("/solutions/{name}", { params: { path: { name: name ?? "" } } })),
    enabled: Boolean(name),
    staleTime: 60_000,
  });
}

export function isProblem(error: unknown, ...slugs: Parameters<ProblemError["is"]>): boolean {
  return asProblem(error)?.is(...slugs) ?? false;
}
