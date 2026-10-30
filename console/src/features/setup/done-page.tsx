import { ExternalLinkIcon, FileBadgeIcon, InfoIcon, PartyPopperIcon } from "lucide-react";
import { Link, useLocation } from "react-router";
import { useCare } from "@/api/queries";
import type { Job, SetupResult } from "@/api/types";
import { CopyButton } from "@/components/app/copy-button";
import { ProblemAlert } from "@/components/app/problem-alert";
import { QrCode } from "@/components/app/qr-code";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { fmt, strings } from "@/lib/strings";

function setupResult(job: Job | undefined): SetupResult | null {
  const r = job?.result;
  return r && "kind" in r && r.kind === "setup" ? (r as SetupResult) : null;
}

// The hand-off to Care (P8): the Care URL, a QR code for phones, and the certificate link.
export default function DonePage() {
  const location = useLocation();
  const result = setupResult((location.state as { job?: Job } | null)?.job);
  const care = useCare();
  const handoff = result?.handoff_url ?? care.data?.handoff_url ?? null;
  const urls = result?.urls ?? care.data?.urls ?? [];
  const consoleUrl = result?.console_url ?? null;
  const moved = consoleUrl ? new URL(consoleUrl).host !== window.location.host : false;

  return (
    <div className="mx-auto max-w-3xl">
      <Card data-testid="handoff">
        <CardHeader>
          <CardTitle className="flex items-center gap-3">
            <PartyPopperIcon className="size-6 text-primary" aria-hidden="true" />
            <h1 className="text-xl">{strings.done.title}</h1>
          </CardTitle>
          <CardDescription>{strings.done.intro}</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-6">
          {moved && consoleUrl ? (
            <Alert variant="info">
              <InfoIcon />
              <AlertDescription>{fmt(strings.done.newAddress, { url: consoleUrl })}</AlertDescription>
            </Alert>
          ) : null}
          <div className="grid gap-6 sm:grid-cols-[minmax(0,1fr)_12rem]">
            <div className="flex min-w-0 flex-col gap-3">
              <p className="text-sm text-soft-foreground">{strings.done.careUrl}</p>
              <div className="flex min-h-10 items-center gap-2">
                {handoff ? (
                  <>
                    <a
                      href={handoff}
                      className="truncate text-lg font-semibold underline underline-offset-4"
                      data-testid="care-url"
                    >
                      {handoff}
                    </a>
                    <CopyButton value={handoff} />
                  </>
                ) : (
                  <Skeleton className="h-6 w-64" />
                )}
              </div>
              {result?.care_username ? (
                <p className="text-sm">{fmt(strings.done.username, { username: result.care_username })}</p>
              ) : null}
              {urls.length > 1 ? (
                <div className="text-sm">
                  <p className="text-soft-foreground">{strings.done.otherUrls}</p>
                  <ul>
                    {urls.map((u) => (
                      <li key={u} className="truncate">
                        {u}
                      </li>
                    ))}
                  </ul>
                </div>
              ) : null}
              <div className="mt-2 flex flex-wrap gap-2">
                <Button asChild disabled={!handoff}>
                  <a href={handoff ?? "#"} target="_blank" rel="noreferrer">
                    <ExternalLinkIcon data-icon="inline-start" />
                    {strings.done.openCare}
                  </a>
                </Button>
                <Button asChild variant="outline">
                  <Link to="/dashboard">{strings.done.dashboard}</Link>
                </Button>
              </div>
            </div>
            <figure className="flex flex-col items-center gap-2">
              {handoff ? <QrCode value={handoff} className="w-48" /> : <Skeleton className="size-48" />}
              <figcaption className="text-center text-xs text-soft-foreground">{strings.done.qr}</figcaption>
            </figure>
          </div>
          {care.isError && !result ? <ProblemAlert error={care.error} /> : null}
        </CardContent>
        <CardFooter className="flex flex-col items-start gap-2 border-t pt-6">
          <Button asChild variant="secondary">
            <Link to="/certificate">
              <FileBadgeIcon data-icon="inline-start" />
              {strings.done.certificate}
            </Link>
          </Button>
          <p className="text-xs text-soft-foreground">{strings.done.certificateHelp}</p>
        </CardFooter>
      </Card>
    </div>
  );
}
