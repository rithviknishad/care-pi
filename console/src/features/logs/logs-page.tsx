import { useQuery } from "@tanstack/react-query";
import { ArrowDownToLineIcon, PauseIcon, PlayIcon, RotateCwIcon } from "lucide-react";
import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router";
import { api, apiUrl, call } from "@/api/client";
import { qk, useLogUnits } from "@/api/queries";
import type { LogLine, LogPage, LogUnit } from "@/api/types";
import { PageHeader } from "@/components/app/page";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { Switch } from "@/components/ui/switch";
import { formatDateTime } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";
import { cn } from "@/lib/utils";

const l = strings.logs;
const COUNTS = [100, 200, 500, 1000];
const MAX_LINES = 5000;

function priorityClass(p: number): string {
  if (p <= 3) return "text-destructive";
  if (p === 4) return "text-amber-700 dark:text-amber-300";
  return "";
}

interface FollowState {
  key: string;
  lines: LogLine[];
  pending: LogLine[];
  error: boolean;
}

const EMPTY: LogLine[] = [];

function emptyFollow(key: string): FollowState {
  return { key, lines: EMPTY, pending: EMPTY, error: false };
}

function append(lines: LogLine[], more: LogLine[]): LogLine[] {
  const next = lines.concat(more);
  return next.length > MAX_LINES ? next.slice(next.length - MAX_LINES) : next;
}

export default function LogsPage() {
  const id = useId();
  const [params, setParams] = useSearchParams();
  const units = useLogUnits();
  const unit = (params.get("unit") ?? "api") as LogUnit;
  const [count, setCount] = useState(200);
  const [follow, setFollow] = useState(false);
  const [paused, setPaused] = useState(false);
  // The lines of the follow stream, tagged with the stream: a new stream starts empty.
  const streamKey = `${unit}|${count}`;
  const [stream, setStream] = useState<FollowState>(() => emptyFollow(streamKey));
  const current = stream.key === streamKey ? stream : emptyFollow(streamKey);
  const streamed = current.lines;
  const pending = current.pending;
  const streamError = current.error;
  const box = useRef<HTMLDivElement>(null);
  const atBottom = useRef(true);
  const pausedRef = useRef(paused);

  const page = useQuery({
    queryKey: qk.logs(unit, count),
    queryFn: () => call(api.GET("/logs/{unit}", { params: { path: { unit }, query: { n: count } } })),
    enabled: !follow,
    staleTime: 0,
  });

  useEffect(() => {
    pausedRef.current = paused;
  }, [paused]);

  // Follow: GET /logs/{unit}?follow=true (SSE). The stream sends the last n lines, then each new line.
  useEffect(() => {
    if (!follow) return;
    const key = `${unit}|${count}`;
    const source = new EventSource(apiUrl(`/logs/${unit}`, { follow: true, n: count }));
    const update = (fn: (s: FollowState) => FollowState) =>
      setStream((prev) => fn(prev.key === key ? prev : emptyFollow(key)));
    let batch: LogLine[] = [];
    let frame = 0;
    const flush = () => {
      frame = 0;
      const lines = batch;
      batch = [];
      update((s) =>
        pausedRef.current ? { ...s, pending: append(s.pending, lines) } : { ...s, lines: append(s.lines, lines) },
      );
    };
    source.addEventListener("log", (e) => {
      try {
        batch.push(JSON.parse((e as MessageEvent<string>).data) as LogLine);
      } catch {
        return;
      }
      if (!frame) frame = requestAnimationFrame(flush);
    });
    source.onopen = () => update((s) => ({ ...s, error: false }));
    source.onerror = () => update((s) => ({ ...s, error: true }));
    return () => {
      cancelAnimationFrame(frame);
      source.close();
      setStream(emptyFollow(""));
    };
  }, [follow, unit, count]);

  const pageLines = (page.data as LogPage | undefined)?.lines;
  const lines = useMemo(() => (follow ? streamed : (pageLines ?? [])), [follow, streamed, pageLines]);

  // Sticky bottom: keep the newest line in view while the operator is at the bottom.
  useLayoutEffect(() => {
    const el = box.current;
    if (el && atBottom.current && !paused) el.scrollTop = el.scrollHeight;
  }, [lines, paused]);

  const onScroll = useCallback(() => {
    const el = box.current;
    if (el) atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
  }, []);

  const resume = () => {
    setStream((s) => ({ ...s, lines: append(s.lines, s.pending), pending: [] }));
    setPaused(false);
    atBottom.current = true;
  };

  const toEnd = () => {
    const el = box.current;
    if (el) el.scrollTop = el.scrollHeight;
    atBottom.current = true;
  };

  return (
    <div className="flex flex-col gap-4">
      <PageHeader title={l.title} />
      <Card size="sm">
        <CardContent className="flex flex-col gap-4">
          <div className="flex flex-wrap items-end gap-x-4 gap-y-3">
            <div className="flex flex-col gap-1">
              <Label htmlFor={`${id}-unit`}>{l.unit}</Label>
              <NativeSelect
                id={`${id}-unit`}
                value={unit}
                onChange={(e) => setParams({ unit: e.target.value }, { replace: true })}
                data-testid="log-unit"
              >
                {(units.data?.items ?? [{ name: unit, unit }]).map((u) => (
                  <NativeSelectOption key={u.name} value={u.name}>
                    {u.name} ({u.unit})
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
            <div className="flex flex-col gap-1">
              <Label htmlFor={`${id}-n`}>{l.lines}</Label>
              <NativeSelect id={`${id}-n`} value={String(count)} onChange={(e) => setCount(Number(e.target.value))}>
                {COUNTS.map((n) => (
                  <NativeSelectOption key={n} value={String(n)}>
                    {n}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
            <Label htmlFor={`${id}-follow`} className="flex h-10 items-center gap-3">
              <Switch
                id={`${id}-follow`}
                checked={follow}
                onCheckedChange={(v) => {
                  setFollow(v);
                  setPaused(false);
                  atBottom.current = true;
                }}
                data-testid="log-follow"
              />
              {l.follow}
            </Label>
            <div className="ml-auto flex flex-wrap gap-2">
              {follow ? (
                paused ? (
                  <Button type="button" variant="outline" size="sm" onClick={resume} data-testid="log-resume">
                    <PlayIcon data-icon="inline-start" />
                    {l.resume}
                  </Button>
                ) : (
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => setPaused(true)}
                    data-testid="log-pause"
                  >
                    <PauseIcon data-icon="inline-start" />
                    {l.pause}
                  </Button>
                )
              ) : (
                <Button type="button" variant="outline" size="sm" onClick={() => void page.refetch()}>
                  <RotateCwIcon data-icon="inline-start" />
                  {l.load}
                </Button>
              )}
              <Button type="button" variant="ghost" size="sm" onClick={toEnd}>
                <ArrowDownToLineIcon data-icon="inline-start" />
                {l.jumpToEnd}
              </Button>
            </div>
          </div>
          <p className="min-h-5 text-xs text-soft-foreground" aria-live="polite">
            {follow
              ? paused
                ? fmt(l.paused, { count: pending.length })
                : streamError
                  ? strings.progress.reconnect
                  : l.following
              : ""}
          </p>
          {page.isError && !follow ? <ProblemAlert error={page.error} /> : null}
          <div
            ref={box}
            onScroll={onScroll}
            className="h-[60dvh] min-h-64 overflow-y-auto rounded-lg border bg-soft-background p-3 text-xs leading-5"
            role="log"
            aria-live="off"
            data-testid="log-lines"
          >
            {lines.length === 0 ? (
              <p className="text-soft-foreground">{page.isPending && !follow ? "" : l.empty}</p>
            ) : null}
            {lines.map((line) => (
              <p
                key={line.cursor}
                className={cn("break-all whitespace-pre-wrap", priorityClass(line.priority))}
                data-testid="log-line"
              >
                <span className="text-soft-foreground">{formatDateTime(line.time)} </span>
                <span className="text-soft-foreground">{l.priority[line.priority] ?? line.priority} </span>
                {line.message}
              </p>
            ))}
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
