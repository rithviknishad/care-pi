import { api, call } from "./client";
import type { LogUnit } from "./types";

// The plain-text "Copy" targets of the agent (S68, docs/api/README.md section 6.4). Each text has the support
// header of the agent (device, agent version, lock, time), so the console copies it as it is.
const TEXT = { parseAs: "text", headers: { Accept: "text/plain" } } as const;

export function exportJob(jobId: string): Promise<string> {
  return call(api.GET("/jobs/{job_id}/export", { params: { path: { job_id: jobId } }, ...TEXT })) as Promise<string>;
}

export function exportLogs(unit: LogUnit, n: number): Promise<string> {
  return call(api.GET("/logs/{unit}/export", { params: { path: { unit }, query: { n } }, ...TEXT })) as Promise<string>;
}

export function exportDoctor(): Promise<string> {
  return call(api.GET("/doctor/export", { ...TEXT })) as Promise<string>;
}
