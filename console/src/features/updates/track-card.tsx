import { useMutation, useQueryClient } from "@tanstack/react-query";
import { RotateCwIcon, SaveIcon, SlidersHorizontalIcon } from "lucide-react";
import { useId, useState } from "react";
import { Link } from "react-router";
import { api, call, callFull } from "@/api/client";
import { asProblem } from "@/api/problem";
import { qk, useSolution } from "@/api/queries";
import type { AvailableLock, Job, UpdatesOverview, UpdateTrack } from "@/api/types";
import { ProblemAlert } from "@/components/app/problem-alert";
import { SectionCard } from "@/components/app/section-card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { fmt, strings } from "@/lib/strings";
import { trackBadge } from "./update-text";

const u = strings.updates;
const TRACKS: UpdateTrack[] = ["stable", "pinned", "custom"];

function lockLabel(l: AvailableLock, current: string | null | undefined): string {
  if (l.version === current) return fmt(u.pinCurrent, { version: l.version });
  if (l.withdrawn) return fmt(u.pinWithdrawn, { version: l.version });
  if (!l.installable) return fmt(u.pinTooOld, { version: l.version, min: l.min_agent_version });
  return l.version;
}

// The track: stable, pinned (with the version), or custom (the refs are in Settings). A change is a config change:
// PATCH /config with solution.track and solution.pin and If-Match, then POST /updates/check (docs/api/README.md 14.1).
export function TrackCard({
  o,
  busy,
  onJob,
}: {
  o: UpdatesOverview | undefined;
  busy: boolean;
  onJob: (job: Job) => void;
}) {
  const id = useId();
  const qc = useQueryClient();
  const solution = useSolution(o?.solution.name);
  const [track, setTrack] = useState<UpdateTrack | null>(null);
  const [pin, setPin] = useState<string | null>(null);
  const current = o?.track;
  const selected = track ?? current ?? "stable";
  const locks = solution.data?.locks ?? o?.available ?? [];
  const selectedPin = pin ?? o?.pin ?? o?.current?.version ?? locks[0]?.version ?? null;
  const changed = Boolean(o) && (selected !== current || (selected === "pinned" && selectedPin !== (o?.pin ?? null)));

  const save = useMutation({
    mutationFn: async () => {
      const { response } = await callFull(api.GET("/config", { params: { query: { view: "file" } } }));
      const etag = response.headers.get("ETag") ?? "";
      const patch =
        selected === "pinned"
          ? { track: selected, pin: selectedPin }
          : selected === "stable"
            ? { track: selected, pin: null }
            : { track: selected };
      await call(
        api.PATCH("/config", {
          params: { header: { "If-Match": etag } },
          headers: { "Content-Type": "application/merge-patch+json" },
          body: { solution: patch } as never,
        }),
      );
      return call(api.POST("/updates/check"));
    },
    onSuccess: (job) => {
      setTrack(null);
      setPin(null);
      void qc.invalidateQueries({ queryKey: ["config"] });
      void qc.invalidateQueries({ queryKey: qk.updates });
      onJob(job);
    },
  });
  const conflict = asProblem(save.error)?.is("config-changed", "precondition-required");

  return (
    <SectionCard
      title={u.trackTitle}
      testId="track-card"
      action={
        o ? (
          <Badge size="sm" variant={trackBadge(current).variant}>
            {trackBadge(current).text}
          </Badge>
        ) : (
          <Skeleton className="h-5 w-16" />
        )
      }
      footer={
        <Button
          type="button"
          onClick={() => save.mutate()}
          disabled={!changed || busy || save.isPending || (selected === "pinned" && !selectedPin)}
          data-testid="track-save"
        >
          {save.isPending ? <Spinner data-icon="inline-start" /> : <SaveIcon data-icon="inline-start" />}
          {u.saveTrack}
        </Button>
      }
    >
      {/* The options are static text, so they show before the data arrives (disabled). */}
      <RadioGroup
        value={selected}
        onValueChange={(v) => setTrack(v as UpdateTrack)}
        aria-label={u.trackTitle}
        disabled={!o}
        data-testid="track-options"
      >
        {TRACKS.map((t) => (
          <Label key={t} htmlFor={`${id}-${t}`} className="flex items-start gap-3 font-normal">
            <RadioGroupItem id={`${id}-${t}`} value={t} className="mt-0.5" />
            <span className="flex flex-col gap-0.5">
              <span className="font-semibold">{trackBadge(t).text}</span>
              <span className="text-xs text-soft-foreground">{u.trackHelp[t]}</span>
            </span>
          </Label>
        ))}
      </RadioGroup>
      {selected === "pinned" && o ? (
        <div className="flex flex-col gap-1">
          <Label htmlFor={`${id}-pin`}>{u.pinVersion}</Label>
          <NativeSelect
            id={`${id}-pin`}
            value={selectedPin ?? ""}
            onChange={(e) => setPin(e.target.value)}
            className="w-full sm:w-64"
            data-testid="pin-version"
          >
            {locks.map((l) => (
              <NativeSelectOption
                key={l.version}
                value={l.version}
                disabled={!l.installable && l.version !== o.current?.version}
              >
                {lockLabel(l, o.current?.version)}
              </NativeSelectOption>
            ))}
          </NativeSelect>
          <p className="text-xs text-soft-foreground">{u.pinHelp}</p>
        </div>
      ) : null}
      {selected === "custom" && o ? (
        <Button asChild variant="outline" size="sm" className="self-start">
          <Link to="/settings?tab=solution" data-testid="custom-refs-link">
            <SlidersHorizontalIcon data-icon="inline-start" />
            {u.customLink}
          </Link>
        </Button>
      ) : null}
      {conflict ? (
        <div className="flex flex-col items-start gap-2">
          <ProblemAlert error={save.error} />
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => {
              save.reset();
              void qc.invalidateQueries({ queryKey: ["config"] });
            }}
          >
            <RotateCwIcon data-icon="inline-start" />
            {strings.settings.reload}
          </Button>
        </div>
      ) : save.error ? (
        <ProblemAlert error={save.error} />
      ) : null}
    </SectionCard>
  );
}
