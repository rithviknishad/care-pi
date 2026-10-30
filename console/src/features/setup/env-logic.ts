import type { EnvStep, FeProvider } from "@/api/types";
import { ENV_NAME_RE } from "@/lib/schema";
import { strings } from "@/lib/strings";

export interface EnvRow {
  key: number;
  name: string;
  value: string;
}

export interface EnvModel {
  backend: EnvRow[];
  frontend: EnvRow[];
  provider: FeProvider;
  artifactUrl: string;
  artifactSha: string;
}

let nextKey = 1;
export function newRow(name = "", value = ""): EnvRow {
  return { key: nextKey++, name, value };
}

export function modelFromStep(step: Partial<EnvStep> | undefined): EnvModel {
  const rows = (map: Record<string, string> | null | undefined) =>
    Object.entries(map ?? {}).map(([name, value]) => newRow(name, value));
  return {
    backend: rows(step?.backend),
    frontend: rows(step?.frontend),
    provider: step?.fe?.provider ?? "local",
    artifactUrl: step?.fe?.artifact_url ?? "",
    artifactSha: step?.fe?.artifact_sha256 ?? "",
  };
}

export function stepFromModel(model: EnvModel): EnvStep {
  const map = (rows: EnvRow[]) =>
    Object.fromEntries(rows.filter((r) => r.name.trim() !== "").map((r) => [r.name.trim(), r.value]));
  const artifact = model.provider === "artifact";
  return {
    backend: map(model.backend),
    frontend: map(model.frontend),
    fe: {
      provider: model.provider,
      artifact_url: artifact && model.artifactUrl.trim() ? model.artifactUrl.trim() : null,
      artifact_sha256: artifact && model.artifactSha.trim() ? model.artifactSha.trim().toLowerCase() : null,
    },
  };
}

export type EnvErrors = Record<string, string>;

// Errors by "backend.<key>.name", "frontend.<key>.value", "artifactUrl", "artifactSha".
export function validateEnv(model: EnvModel, reserved: { backend: string[]; frontend: string[] }): EnvErrors {
  const errors: EnvErrors = {};
  for (const part of ["backend", "frontend"] as const) {
    const seen = new Map<string, number>();
    for (const row of model[part]) {
      const name = row.name.trim();
      if (name === "" && row.value === "") continue;
      if (!ENV_NAME_RE.test(name)) errors[`${part}.${row.key}.name`] = strings.env.badName;
      else if (reserved[part].includes(name)) errors[`${part}.${row.key}.name`] = strings.env.reserved;
      else if (seen.has(name)) errors[`${part}.${row.key}.name`] = strings.env.duplicate;
      if (/[\r\n]/.test(row.value)) errors[`${part}.${row.key}.value`] = strings.env.lineBreak;
      seen.set(name, row.key);
    }
  }
  if (model.provider === "artifact") {
    const url = model.artifactUrl.trim();
    if (url && !/^(https|file):\/\/\S+$/.test(url)) errors.artifactUrl = strings.env.artifactUrlHelp;
    const sha = model.artifactSha.trim();
    if (sha && !/^[A-Fa-f0-9]{64}$/.test(sha)) errors.artifactSha = strings.env.artifactShaHelp;
  }
  return errors;
}
