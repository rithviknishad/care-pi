import { ExternalLinkIcon, FileBadgeIcon, GlobeIcon, InfoIcon, LayoutDashboardIcon } from "lucide-react";
import { Link, useLocation } from "react-router";
import { useCare } from "@/api/queries";
import type { Job, SetupResult } from "@/api/types";
import { ProblemAlert } from "@/components/app/problem-alert";
import { QrCode } from "@/components/app/qr-code";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { CopyButton } from "@/components/v2/copy-button";
import { Hero } from "@/components/v2/hero";
import { Illustration } from "@/components/v2/illustration";
import { Panel, WhatNext } from "@/components/v2/panel";
import { fmt, strings } from "@/lib/strings";
import { loadOnline } from "./wizard";

function setupResult(job: Job | undefined): SetupResult | null {
  const r = job?.result;
  return r && "kind" in r && r.kind === "setup" ? (r as SetupResult) : null;
}

const d = strings.done;

// The hand-off to Care (P8, S66): "Care is ready", the address of Care with a big "Open Care" button, the next action
// "Install the certificate on this device", and a QR code for a phone.
export default function DonePage() {
  const location = useLocation();
  const result = setupResult((location.state as { job?: Job } | null)?.job);
  const care = useCare();
  const handoff = result?.handoff_url ?? care.data?.handoff_url ?? null;
  const urls = result?.urls ?? care.data?.urls ?? [];
  const consoleUrl = result?.console_url ?? null;
  const moved = consoleUrl ? new URL(consoleUrl).host !== window.location.host : false;
  const online = loadOnline();

  return (
    <div className="mx-auto flex max-w-4xl flex-col gap-6" data-testid="handoff">
      <Hero
        size="lg"
        illustration="clinic"
        eyebrow={strings.app.name}
        title={d.title}
        description={d.intro}
        actions={
          <Button asChild size="lg" variant="secondary" disabled={!handoff} className="no-underline">
            <a href={handoff ?? "#"} target="_blank" rel="noreferrer" data-testid="open-care">
              <ExternalLinkIcon data-icon="inline-start" />
              {d.openCare}
            </a>
          </Button>
        }
      />
      {moved && consoleUrl ? (
        <p className="flex items-start gap-2 rounded-card border border-info-border bg-info-bg p-4 type-body text-info-fg">
          <InfoIcon className="mt-1 size-4 shrink-0" aria-hidden="true" />
          {fmt(d.newAddress, { url: consoleUrl })}
        </p>
      ) : null}
      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_18rem]">
        <Panel title={d.careUrl}>
          <div className="flex min-h-10 min-w-0 items-center gap-2">
            {handoff ? (
              <>
                <a
                  href={handoff}
                  className="min-w-0 truncate font-mono text-xl font-semibold text-ink underline underline-offset-4"
                  data-testid="care-url"
                >
                  {handoff}
                </a>
                <CopyButton text={handoff} iconOnly variant="ghost" />
              </>
            ) : (
              <Skeleton className="h-6 w-64" />
            )}
          </div>
          {result?.care_username ? (
            <p className="type-body text-ink-muted">{fmt(d.username, { username: result.care_username })}</p>
          ) : null}
          {urls.length > 1 ? (
            <div className="flex flex-col gap-1">
              <p className="type-small text-ink-soft">{d.otherUrls}</p>
              <ul className="flex flex-col">
                {urls.map((u) => (
                  <li key={u} className="truncate type-data text-ink">
                    {u}
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
          <div className="mt-2 flex flex-col gap-2 border-t border-line-soft pt-4">
            <div className="flex flex-col gap-3 sm:flex-row sm:flex-wrap">
              {/* Below 640 px each button has the full width and its text wraps: no side scroll at 360 px. */}
              <Button asChild size="lg" className="h-auto min-h-11 py-2 text-left whitespace-normal">
                <Link to="/certificate">
                  <FileBadgeIcon data-icon="inline-start" />
                  {d.certificate}
                </Link>
              </Button>
              <Button asChild variant="outline" size="lg" className="h-auto min-h-11 py-2 text-left whitespace-normal">
                <Link to="/dashboard">
                  <LayoutDashboardIcon data-icon="inline-start" />
                  {d.dashboard}
                </Link>
              </Button>
            </div>
            <WhatNext>{d.certificateHelp}</WhatNext>
          </div>
          {online ? (
            <div className="flex items-start gap-3 rounded-card border border-line-soft bg-surface-tint p-4">
              <GlobeIcon className="mt-0.5 size-5 shrink-0 text-primary-solid" aria-hidden="true" />
              <div className="flex flex-col gap-2">
                <p className="type-body text-ink">{strings.tlsStep.onlineHelp}</p>
                <Button asChild variant="outline" size="sm" className="self-start">
                  <Link to="/settings?tab=connectivity">{d.publicName}</Link>
                </Button>
              </div>
            </div>
          ) : null}
        </Panel>
        <Panel title={d.phoneTitle} variant="tint">
          <figure className="flex flex-col items-center gap-3">
            {handoff ? (
              <QrCode value={handoff} className="w-48 border border-line bg-white p-1" />
            ) : (
              <Skeleton className="size-48" />
            )}
            <figcaption className="flex items-center gap-2 text-center type-small text-ink-muted">
              <Illustration name="phone-qr" size="sm" className="w-10" />
              {d.qr}
            </figcaption>
          </figure>
        </Panel>
      </div>
      {care.isError && !result ? <ProblemAlert error={care.error} onRetry={() => void care.refetch()} /> : null}
    </div>
  );
}
