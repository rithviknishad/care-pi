import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ChevronDownIcon, ChevronRightIcon, DownloadIcon, PackageIcon } from "lucide-react";
import { Link } from "react-router";
import { useId, useState } from "react";
import { api, call } from "@/api/client";
import { qk, useJobs, useStatus } from "@/api/queries";
import type { Job, SupportBundleResult } from "@/api/types";
import { PageHeader } from "@/components/app/page";
import { CopyButton } from "@/components/v2/copy-button";
import { KeyValueList } from "@/components/v2/key-value-list";
import { MonoBlock } from "@/components/v2/mono-block";
import { Panel, WhatNext } from "@/components/v2/panel";
import { Timeline } from "@/components/v2/timeline";
import { JOB_STATE_TONE } from "@/features/jobs/job-steps";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { JobProgress } from "@/features/jobs/job-progress";
import { formatBytes, formatDateTime, shortSha } from "@/lib/format";
import { fmt, jobKindText, strings } from "@/lib/strings";
import { DoctorCard } from "./doctor-card";

const sp = strings.support;

function bundleOf(job: Job | null): SupportBundleResult | null {
  const r = job?.result;
  return r && "kind" in r && r.kind === "support-bundle" ? (r as SupportBundleResult) : null;
}

function BundleCard() {
  const id = useId();
  const qc = useQueryClient();
  const [lines, setLines] = useState(1000);
  const [hours, setHours] = useState(24);
  const [job, setJob] = useState<Job | null>(null);
  const [done, setDone] = useState<Job | null>(null);
  const create = useMutation({
    mutationFn: () => call(api.POST("/support-bundle", { body: { log_lines: lines, since_hours: hours } })),
    onSuccess: (j) => {
      setDone(null);
      setJob(j);
    },
  });
  const bundle = bundleOf(done);
  return (
    <Panel title={sp.create} description={sp.intro} testId="bundle-card">
      <div className="flex flex-wrap items-end gap-4">
        <div className="flex flex-col gap-1">
          <Label htmlFor={`${id}-lines`}>{sp.lines}</Label>
          <NativeSelect id={`${id}-lines`} value={String(lines)} onChange={(e) => setLines(Number(e.target.value))}>
            {[100, 1000, 5000, 10000].map((n) => (
              <NativeSelectOption key={n} value={String(n)}>
                {n}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
        <div className="flex flex-col gap-1">
          <Label htmlFor={`${id}-hours`}>{sp.hours}</Label>
          <NativeSelect id={`${id}-hours`} value={String(hours)} onChange={(e) => setHours(Number(e.target.value))}>
            {[1, 24, 72, 168, 720].map((n) => (
              <NativeSelectOption key={n} value={String(n)}>
                {n}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
        <Button
          type="button"
          size="lg"
          onClick={() => create.mutate()}
          disabled={create.isPending || (job !== null && done === null)}
          data-testid="bundle-create"
        >
          {create.isPending ? <Spinner data-icon="inline-start" /> : <PackageIcon data-icon="inline-start" />}
          {sp.create}
        </Button>
      </div>
      {create.error ? <ProblemAlert error={create.error} /> : null}
      {job ? (
        <JobProgress
          key={job.id}
          jobId={job.id}
          initial={job}
          onDone={(j) => {
            setDone(j);
            void qc.invalidateQueries({ queryKey: qk.jobs });
          }}
        >
          {bundle ? (
            <div
              className="flex flex-col gap-3 rounded-card border border-ok-border bg-ok-bg p-4 sm:p-5"
              data-testid="bundle-ready"
            >
              <p className="type-body font-medium text-ok-fg">{sp.ready}</p>
              <KeyValueList
                items={[
                  { id: "file", label: sp.file, value: bundle.filename, mono: true, copy: bundle.filename },
                  { id: "size", label: sp.size, value: formatBytes(bundle.size_bytes), mono: true },
                  { id: "expires", label: sp.expires, value: formatDateTime(bundle.expires_at), mono: true },
                ]}
              />
              <div className="flex flex-wrap gap-2">
                <Button asChild>
                  <a href={bundle.download_path} download={bundle.filename}>
                    <DownloadIcon data-icon="inline-start" />
                    {sp.download}
                  </a>
                </Button>
                <CopyButton
                  text={fmt(sp.message, {
                    file: bundle.filename,
                    size: formatBytes(bundle.size_bytes),
                    sha: bundle.sha256,
                    until: formatDateTime(bundle.expires_at),
                  })}
                  report
                  title={sp.messageTitle}
                  label={sp.copyMessage}
                />
              </div>
              <WhatNext>{sp.sendNext}</WhatNext>
            </div>
          ) : null}
        </JobProgress>
      ) : null}
      <details className="group rounded-card border border-line-soft">
        <summary className="flex h-11 cursor-pointer list-none items-center gap-2 px-4 type-small font-medium text-ink-muted outline-none select-none hover:text-ink focus-visible:ring-3 focus-visible:ring-ring/50 [&::-webkit-details-marker]:hidden">
          <ChevronDownIcon
            aria-hidden="true"
            className="size-4 transition-transform group-open:rotate-180 motion-reduce:transition-none"
          />
          {sp.cliTitle}
        </summary>
        <div className="flex flex-col gap-2 px-4 pb-4">
          <p className="type-small text-ink-muted">{sp.cliText}</p>
          <MonoBlock text="sudo care-pi support-bundle" mode="pre" testId="bundle-cli" />
        </div>
      </details>
    </Panel>
  );
}

function AgentCard() {
  const status = useStatus(false);
  const a = status.data?.agent;
  return (
    <Panel title={sp.agent} testId="agent-card">
      {status.isError ? <ProblemAlert error={status.error} onRetry={() => void status.refetch()} /> : null}
      <KeyValueList
        loading={!a}
        items={[
          {
            id: "version",
            label: sp.version,
            value: a?.version,
            mono: true,
            copy: a?.version,
            testId: "agent-version",
          },
          { id: "commit", label: sp.commit, value: shortSha(a?.commit), mono: true, title: a?.commit, copy: a?.commit },
          { id: "built", label: sp.built, value: formatDateTime(a?.date), mono: true },
          { id: "dev", label: sp.devBuild, value: a ? (a.dev_build ? strings.common.yes : strings.common.no) : "" },
          { id: "started", label: sp.started, value: formatDateTime(a?.started_at), mono: true },
        ]}
      />
    </Panel>
  );
}

function JobsCard() {
  const jobs = useJobs();
  const items = jobs.data?.items ?? [];
  return (
    <Panel
      title={sp.jobs}
      action={
        <Link
          to="/jobs"
          className="flex items-center gap-1 type-small font-medium text-primary-ink underline-offset-4 hover:underline"
        >
          {strings.dashboard.allJobs}
          <ChevronRightIcon className="size-4" aria-hidden="true" />
        </Link>
      }
    >
      {jobs.isError ? <ProblemAlert error={jobs.error} onRetry={() => void jobs.refetch()} /> : null}
      {!jobs.data ? (
        <div className="flex flex-col gap-3" aria-hidden="true">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-10" />
          ))}
        </div>
      ) : items.length === 0 ? (
        <p className="type-body text-ink-muted">{sp.noJobs}</p>
      ) : (
        <Timeline
          items={items.slice(0, 6).map((j) => ({
            id: j.id,
            time: formatDateTime(j.started_at ?? j.created_at).slice(5, 16),
            datetime: j.started_at ?? j.created_at,
            tone: JOB_STATE_TONE[j.state] ?? "neutral",
            title: (
              <Link to={`/jobs/${encodeURIComponent(j.id)}`} className="underline-offset-4 hover:underline">
                {jobKindText(j.kind)}
              </Link>
            ),
            description: `${strings.progress.states[j.state] ?? j.state} · ${strings.jobsPage.from[j.source] ?? j.source}`,
          }))}
        />
      )}
    </Panel>
  );
}

export default function SupportPage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader title={sp.title} description={sp.pageIntro} />
      <DoctorCard />
      <BundleCard />
      <div className="grid gap-6 xl:grid-cols-2">
        <AgentCard />
        <JobsCard />
      </div>
    </div>
  );
}
