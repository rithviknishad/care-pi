import { AlertTriangleIcon, CheckCircle2Icon, CircleIcon, InfoIcon, XCircleIcon } from "lucide-react";
import type { DestinationTestResult } from "@/api/types";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { formatBytes } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";
import { cn } from "@/lib/utils";
import { onRootDisk } from "./destination-meta";

const t = strings.dest;

// The result of POST /backups/destinations/test: the summary, the checks, and the notes of the agent.
export function TestResultView({ result }: { result: DestinationTestResult }) {
  const repo = result.repository;
  const waiting = result.next_action === "accept-host-key" || result.next_action === "install-ssh-key";
  return (
    <div className="flex flex-col gap-3" data-testid="dest-test-result" data-next={result.next_action}>
      {result.ok ? (
        <Alert variant="success">
          <CheckCircle2Icon aria-hidden="true" />
          <AlertTitle>{t.passed}</AlertTitle>
          {result.free_bytes !== null ? (
            <AlertDescription>{fmt(t.free, { free: formatBytes(result.free_bytes) })}</AlertDescription>
          ) : null}
        </Alert>
      ) : waiting ? null : (
        <Alert variant="destructive">
          <XCircleIcon aria-hidden="true" />
          <AlertTitle>{t.failed}</AlertTitle>
          <AlertDescription>
            {result.checks
              .filter((c) => !c.ok && c.level === "required")
              .map((c) => (c.fix ? `${c.detail} (${c.fix})` : c.detail))
              .join(" ")}
          </AlertDescription>
        </Alert>
      )}
      {onRootDisk(result) ? (
        <Alert variant="warning" data-testid="root-disk-warning">
          <AlertTriangleIcon aria-hidden="true" />
          <AlertDescription>{t.rootDisk}</AlertDescription>
        </Alert>
      ) : null}
      {repo?.exists ? (
        <Alert variant="info">
          <InfoIcon aria-hidden="true" />
          <AlertDescription>{t.repoExists}</AlertDescription>
        </Alert>
      ) : null}
      {repo && repo.other_key_ids.length > 0 ? (
        <Alert variant="info">
          <InfoIcon aria-hidden="true" />
          <AlertDescription>{fmt(t.otherKeys, { ids: repo.other_key_ids.join(", ") })}</AlertDescription>
        </Alert>
      ) : null}
      <details className="text-sm">
        <summary className="cursor-pointer text-soft-foreground">{t.checks}</summary>
        <ul className="mt-2 flex flex-col gap-1">
          {result.checks.map((c) => (
            <li key={c.name} className="flex items-start gap-2">
              {c.ok ? (
                <CheckCircle2Icon
                  className="mt-0.5 size-4 shrink-0 text-green-600 dark:text-green-400"
                  aria-hidden="true"
                />
              ) : c.level === "required" ? (
                <XCircleIcon className="mt-0.5 size-4 shrink-0 text-destructive" aria-hidden="true" />
              ) : (
                <CircleIcon className="mt-0.5 size-4 shrink-0 text-amber-600" aria-hidden="true" />
              )}
              <span className={cn("min-w-0 break-words", !c.ok && "font-semibold")}>
                {c.name}: {c.detail}
              </span>
            </li>
          ))}
        </ul>
      </details>
    </div>
  );
}
