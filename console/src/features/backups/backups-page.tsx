import { useMutation } from "@tanstack/react-query";
import { HistoryIcon, PlusIcon } from "lucide-react";
import { useEffect, useState } from "react";
import { Link, useLocation, useSearchParams } from "react-router";
import { api, call } from "@/api/client";
import { useBackups, useBackupsHealth, useSetup } from "@/api/queries";
import type { BackupRunRequest, DestinationState, Job } from "@/api/types";
import { PageHeader } from "@/components/app/page";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Button } from "@/components/ui/button";
import { Empty, EmptyDescription, EmptyHeader } from "@/components/ui/empty";
import { useStableOrder } from "@/hooks/use-stable-order";
import { formatTime } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";
import { jobKindText } from "@/features/jobs/step-text";
import { BackupBannerList, BackupHealthRow } from "./backup-banners";
import {
  DestinationCard,
  DestinationCardSkeleton,
  LocalCard,
  ScheduleCard,
  SshKeyCard,
  VerifyCard,
} from "./backups-cards";
import { AddDestinationDialog, EditDestinationDialog, RemoveDestinationDialog } from "./destination-change";
import { lastOffsiteCopy } from "./health";
import { JobDialog } from "./job-dialog";
import { RecoveryKeyCard } from "./key-card";

const p = strings.backupsPage;
const destKey = (d: DestinationState) => d.id;
// The skeleton shows the number of destinations of the last visit (2 the first time: one near, one off-site), so
// the sections below the destinations do not move when the data arrives (docs/console-ui.md section 5).
const COUNT_KEY = "care-pi.backups.destinations";

function rememberedCount(): number {
  try {
    const n = Number(localStorage.getItem(COUNT_KEY));
    return Number.isInteger(n) && n >= 0 && n <= 8 ? n : 2;
  } catch {
    return 2;
  }
}

export default function BackupsPage() {
  const backups = useBackups();
  const health = useBackupsHealth();
  const setup = useSetup();
  const location = useLocation();
  const [params, setParams] = useSearchParams();
  const o = backups.data;
  const destinations = useStableOrder(o?.destinations, destKey);
  const [skeletons] = useState(rememberedCount);
  const [job, setJob] = useState<Job | null>(null);
  const [moreOpen, setMoreOpen] = useState(false);
  const [editing, setEditing] = useState<DestinationState | null>(null);
  const [removing, setRemoving] = useState<DestinationState | null>(null);
  const adding = params.get("add") === "1";
  const setAdding = (open: boolean) => setParams(open ? { add: "1" } : {}, { replace: true });

  const start = useMutation({ mutationFn: (fn: () => Promise<Job>) => fn(), onSuccess: setJob });
  const run = (body: BackupRunRequest) => start.mutate(() => call(api.POST("/backups/run", { body })));
  const openRunning = useMutation({
    mutationFn: (id: string) => call(api.GET("/jobs/{job_id}", { params: { path: { job_id: id } } })),
    onSuccess: setJob,
  });

  useEffect(() => {
    if (!o) return;
    try {
      localStorage.setItem(COUNT_KEY, String(o.destinations.length));
    } catch {
      // Private mode: the next visit uses 2.
    }
  }, [o]);

  // A link to #recovery-key or #destination-<id> (the banners) scrolls when the cards exist.
  const hash = location.hash;
  const loaded = Boolean(o);
  useEffect(() => {
    if (!loaded || !hash) return;
    document.getElementById(hash.slice(1))?.scrollIntoView({ block: "start" });
  }, [loaded, hash]);

  const clinic = setup.data?.draft.identity.display_name ?? "";
  // The ages of the cards use the time of the report (the clock of the device), not the clock of the browser.
  const now = o ? Date.parse(o.generated_at) : 0;
  const walTimeout = o?.rpo.wal_archive_timeout_min;
  const busy = start.isPending;

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={p.title}
        description={
          <span className="tabular-nums" data-testid="backups-updated">
            {fmt(p.updated, { time: o ? formatTime(o.generated_at) : "--:--:--" })}
          </span>
        }
        actions={
          <Button asChild variant="outline">
            <Link to="/restore">
              <HistoryIcon data-icon="inline-start" />
              {p.restore}
            </Link>
          </Button>
        }
      />
      {backups.isError ? <ProblemAlert error={backups.error} /> : null}
      <BackupHealthRow
        health={health.data ?? o?.health}
        lastCopy={lastOffsiteCopy(o)}
        loading={!health.data && !o}
        linkToPage={false}
        onMore={() => setMoreOpen((v) => !v)}
        moreOpen={moreOpen}
      />
      {moreOpen ? <BackupBannerList health={health.data ?? o?.health} /> : null}
      <div className="flex h-9 items-center gap-3 text-sm" data-testid="running-job">
        {o?.running_job ? (
          <>
            <span className="truncate">{fmt(p.runningJob, { kind: jobKindText(o.running_job.kind) })}</span>
            <Button
              type="button"
              variant="link"
              size="sm"
              className="px-0"
              onClick={() => openRunning.mutate(o.running_job?.id ?? "")}
            >
              {p.openJob}
            </Button>
          </>
        ) : (
          <span className="text-soft-foreground">{o ? p.noJob : "\u00a0"}</span>
        )}
      </div>
      {start.error ? <ProblemAlert error={start.error} /> : null}

      <div className="grid gap-4 lg:grid-cols-2">
        <LocalCard
          local={o?.local}
          now={now}
          busy={busy}
          onBackup={(type) => run({ scope: "local-db", type, reason: null })}
        />
        <RecoveryKeyCard overview={o} clinic={clinic} onJob={setJob} />
      </div>

      <section className="flex flex-col gap-3" aria-labelledby="destinations-title" data-testid="destinations">
        <div className="flex flex-wrap items-center gap-2">
          <h2 id="destinations-title" className="flex-1 text-base font-semibold">
            {p.destinations}
          </h2>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => setAdding(true)}
            data-testid="destination-add"
          >
            <PlusIcon data-icon="inline-start" />
            {p.add}
          </Button>
        </div>
        {!o ? (
          skeletons > 0 ? (
            <div className="grid gap-4 md:grid-cols-2">
              {Array.from({ length: skeletons }, (_, i) => (
                <DestinationCardSkeleton key={i} />
              ))}
            </div>
          ) : (
            <div className="min-h-32" />
          )
        ) : destinations && destinations.length > 0 ? (
          <div className="grid gap-4 md:grid-cols-2">
            {destinations.map((d) => (
              <DestinationCard
                key={d.id}
                d={d}
                now={now}
                busy={busy}
                onRun={() => run({ scope: "destination", destination: d.id, type: "incr", reason: null })}
                onVerify={() =>
                  start.mutate(() =>
                    call(
                      api.POST("/backups/verify", {
                        body: { scope: "destination", destination: d.id, read_data_subset_pct: 5, prune: false },
                      }),
                    ),
                  )
                }
                onEdit={() => setEditing(d)}
                onRemove={() => setRemoving(d)}
              />
            ))}
          </div>
        ) : (
          <Empty className="min-h-32 rounded-lg border border-dashed" data-testid="no-destinations">
            <EmptyHeader>
              <EmptyDescription>{p.noDestinations}</EmptyDescription>
            </EmptyHeader>
          </Empty>
        )}
      </section>

      <div className="grid gap-4 lg:grid-cols-2">
        <ScheduleCard overview={o} rows={4 + skeletons + (skeletons > 0 ? 1 : 0)} />
        <VerifyCard
          overview={o}
          busy={busy}
          onVerify={() =>
            start.mutate(() =>
              call(api.POST("/backups/verify", { body: { scope: "all", read_data_subset_pct: 5, prune: false } })),
            )
          }
          onDrill={() => start.mutate(() => call(api.POST("/backups/drill", { body: { reason: null } })))}
        />
      </div>
      <SshKeyCard overview={o} />

      <AddDestinationDialog open={adding} onOpenChange={setAdding} walTimeoutMin={walTimeout} onJob={setJob} />
      <EditDestinationDialog
        destination={editing}
        onClose={() => setEditing(null)}
        walTimeoutMin={walTimeout}
        onJob={setJob}
      />
      <RemoveDestinationDialog destination={removing} onClose={() => setRemoving(null)} onJob={setJob} />
      <JobDialog job={job} onClose={() => setJob(null)} />
    </div>
  );
}
