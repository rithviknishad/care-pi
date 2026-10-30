import { useMutation, useQueryClient } from "@tanstack/react-query";
import { DownloadIcon, PackageIcon } from "lucide-react";
import { useId, useState } from "react";
import { api, call } from "@/api/client";
import { qk, useJobs, useStatus } from "@/api/queries";
import type { Job, SupportBundleResult } from "@/api/types";
import { DataList, DataRow, PageHeader } from "@/components/app/page";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { JobProgress } from "@/features/jobs/job-progress";
import { formatBytes, formatDateTime, shortSha } from "@/lib/format";
import { strings } from "@/lib/strings";

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
    <Card size="sm">
      <CardHeader>
        <CardTitle>
          <h2>{sp.create}</h2>
        </CardTitle>
        <CardDescription>{sp.intro}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
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
              <div className="flex flex-col gap-3 rounded-lg border p-4" data-testid="bundle-ready">
                <p className="text-sm">{sp.ready}</p>
                <DataList>
                  <DataRow label={sp.size} value={formatBytes(bundle.size_bytes)} />
                  <DataRow label={sp.sha} value={shortSha(bundle.sha256, 16)} title={bundle.sha256} />
                  <DataRow label={sp.expires} value={formatDateTime(bundle.expires_at)} />
                </DataList>
                <Button asChild className="self-start">
                  <a href={bundle.download_path} download={bundle.filename}>
                    <DownloadIcon data-icon="inline-start" />
                    {sp.download}
                  </a>
                </Button>
              </div>
            ) : null}
          </JobProgress>
        ) : null}
      </CardContent>
    </Card>
  );
}

function AgentCard() {
  const status = useStatus(false);
  const a = status.data?.agent;
  return (
    <Card size="sm">
      <CardHeader>
        <CardTitle>
          <h2>{sp.agent}</h2>
        </CardTitle>
      </CardHeader>
      <CardContent>
        {status.isError ? <ProblemAlert error={status.error} /> : null}
        <DataList>
          <DataRow label={sp.version} loading={!a} value={a?.version} testId="agent-version" />
          <DataRow label={sp.commit} loading={!a} value={shortSha(a?.commit)} title={a?.commit} />
          <DataRow label={sp.built} loading={!a} value={formatDateTime(a?.date)} />
          <DataRow
            label={sp.devBuild}
            loading={!a}
            value={a ? (a.dev_build ? strings.common.yes : strings.common.no) : ""}
          />
          <DataRow label={sp.started} loading={!a} value={formatDateTime(a?.started_at)} />
        </DataList>
      </CardContent>
    </Card>
  );
}

function JobsCard() {
  const jobs = useJobs();
  return (
    <Card size="sm">
      <CardHeader>
        <CardTitle>
          <h2>{sp.jobs}</h2>
        </CardTitle>
      </CardHeader>
      <CardContent>
        {jobs.isError ? <ProblemAlert error={jobs.error} /> : null}
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{sp.kind}</TableHead>
              <TableHead>{sp.state}</TableHead>
              <TableHead>{sp.source}</TableHead>
              <TableHead>{sp.started}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {jobs.data ? (
              jobs.data.items.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={4} className="text-soft-foreground">
                    {sp.noJobs}
                  </TableCell>
                </TableRow>
              ) : (
                jobs.data.items.map((j) => (
                  <TableRow key={j.id} className="h-11" title={j.id}>
                    <TableCell>{j.kind}</TableCell>
                    <TableCell>
                      <Badge
                        size="sm"
                        variant={j.state === "succeeded" ? "success" : j.state === "failed" ? "destructive" : "info"}
                      >
                        {strings.progress.states[j.state] ?? j.state}
                      </Badge>
                    </TableCell>
                    <TableCell>{j.source}</TableCell>
                    <TableCell>{formatDateTime(j.started_at ?? j.created_at)}</TableCell>
                  </TableRow>
                ))
              )
            ) : (
              [0, 1, 2].map((i) => (
                <TableRow key={i} className="h-11">
                  <TableCell colSpan={4}>
                    <Skeleton className="h-4" />
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}

export default function SupportPage() {
  return (
    <div className="flex flex-col gap-4">
      <PageHeader title={sp.title} />
      <BundleCard />
      <div className="grid gap-4 xl:grid-cols-2">
        <AgentCard />
        <JobsCard />
      </div>
    </div>
  );
}
