import { CheckCircle2Icon, DownloadIcon, ShieldQuestionIcon, XCircleIcon } from "lucide-react";
import { useMemo, useState } from "react";
import { useSearchParams } from "react-router";
import { apiUrl } from "@/api/client";
import { isProblem, useInstructions, useNetwork, useSession } from "@/api/queries";
import type { ClientOs, Network } from "@/api/types";
import { CopyButton } from "@/components/app/copy-button";
import { PageHeader } from "@/components/app/page";
import { ProblemAlert } from "@/components/app/problem-alert";
import { QrCode } from "@/components/app/qr-code";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { fingerprintLines } from "@/lib/format";
import { CLIENT_OSES, detectOs, isClientOs } from "@/lib/os";
import { fmt, strings, tlsStepText } from "@/lib/strings";

const c = strings.certificate;

function Fingerprint({ hex, colon, noCa }: { hex: string | undefined; colon: string | undefined; noCa: boolean }) {
  return (
    <section className="flex flex-col gap-2">
      <div className="flex items-center gap-2">
        <h2 className="font-semibold">{c.fingerprint}</h2>
        {colon ? <CopyButton value={colon} /> : null}
      </div>
      <p className="text-xs text-soft-foreground">{c.fingerprintHelp}</p>
      <div
        className="min-h-28 rounded-lg border bg-soft-background p-3 text-sm leading-6 tracking-wide"
        data-testid="ca-fingerprint"
      >
        {hex ? (
          fingerprintLines(hex).map((line) => <p key={line}>{line}</p>)
        ) : noCa ? (
          <p className="text-soft-foreground">{c.noCa}</p>
        ) : (
          <div className="flex flex-col gap-2">
            {[0, 1, 2, 3].map((i) => (
              <Skeleton key={i} className="h-4 w-full" />
            ))}
          </div>
        )}
      </div>
    </section>
  );
}

function Steps({ os }: { os: ClientOs }) {
  const instructions = useInstructions(os);
  const data = instructions.data;
  if (instructions.isError) {
    // not-found: the local CA does not exist yet (Caddy makes it at its first start).
    const noCa = isProblem(instructions.error, "not-found");
    return <ProblemAlert error={instructions.error} title={noCa ? c.noCa : undefined} />;
  }
  if (!data) {
    return (
      <div className="flex flex-col gap-3" aria-busy="true">
        {[0, 1, 2].map((i) => (
          <Skeleton key={i} className="h-12" />
        ))}
      </div>
    );
  }
  const apple = os === "ios" || os === "macos";
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap gap-2">
        <Button asChild>
          <a href={data.root.download_path} download={data.root.filename} data-testid="ca-download">
            <DownloadIcon data-icon="inline-start" />
            {apple ? c.downloadProfile : os === "linux" ? c.downloadPem : c.downloadDer}
          </a>
        </Button>
      </div>
      <ol className="flex flex-col gap-3" data-testid="ca-steps">
        {data.steps.map((step, i) => (
          <li key={step.key} className="grid grid-cols-[2rem_minmax(0,1fr)] gap-2">
            <span className="flex size-7 items-center justify-center rounded-full bg-primary-100 text-xs font-semibold text-primary-900 dark:bg-primary-400/15 dark:text-primary-300">
              {i + 1}
            </span>
            <p className="pt-1 text-sm">{tlsStepText(step.key, step.params)}</p>
          </li>
        ))}
      </ol>
      {data.notes.length > 0 ? (
        <section className="flex flex-col gap-1">
          <h3 className="text-xs font-semibold text-soft-foreground uppercase">{c.notes}</h3>
          <ul className="flex list-disc flex-col gap-1 pl-4 text-sm text-soft-foreground">
            {data.notes.map((n) => (
              <li key={n.key}>{tlsStepText(n.key, n.params)}</li>
            ))}
          </ul>
        </section>
      ) : null}
      <div className="flex flex-col gap-2 border-t pt-4">
        <p className="text-xs text-soft-foreground">{c.otherFormats}</p>
        <div className="flex flex-wrap gap-2">
          <Button asChild variant="outline" size="sm">
            <a href={apiUrl("/tls/root-ca", { format: "der" })} download>
              {c.downloadDer}
            </a>
          </Button>
          <Button asChild variant="outline" size="sm">
            <a href={apiUrl("/tls/root-ca", { format: "pem" })} download>
              {c.downloadPem}
            </a>
          </Button>
          <Button asChild variant="outline" size="sm">
            <a href={apiUrl("/tls/root-ca/mobileconfig")} download>
              {c.downloadProfile}
            </a>
          </Button>
        </div>
      </div>
    </div>
  );
}

// The trust check of docs/api/README.md section 2.7: the console requests another host name of this
// device with mode no-cors. The request completes only when the browser trusts the certificate.
// It runs only when the operator presses the button.
function otherHost(network: Network): string | null {
  const here = window.location.hostname;
  const names = [network.mdns.advertised, ...network.addresses].filter((h): h is string => Boolean(h));
  return names.find((h) => h !== here) ?? null;
}

function TrustCheck({ network }: { network: Network }) {
  const [state, setState] = useState<"idle" | "busy" | "yes" | "no">("idle");
  const host = otherHost(network);
  if (!host || window.location.protocol !== "https:") return null;
  const check = async () => {
    setState("busy");
    try {
      await fetch(`https://${host}:${window.location.port || "443"}/api/v1/health`, {
        mode: "no-cors",
        cache: "no-store",
      });
      setState("yes");
    } catch {
      setState("no");
    }
  };
  return (
    <Card size="sm">
      <CardContent className="flex flex-col gap-3">
        <p className="text-xs text-soft-foreground">{fmt(c.trustCheckHelp, { host })}</p>
        <div className="flex min-h-9 flex-wrap items-center gap-3">
          <Button type="button" variant="outline" size="sm" onClick={() => void check()} disabled={state === "busy"}>
            {state === "busy" ? <Spinner data-icon="inline-start" /> : <ShieldQuestionIcon data-icon="inline-start" />}
            {c.trustCheck}
          </Button>
          {state === "yes" ? (
            <span className="flex items-center gap-2 text-sm text-green-700 dark:text-green-400">
              <CheckCircle2Icon className="size-4" aria-hidden="true" />
              {c.trusted}
            </span>
          ) : state === "no" ? (
            <span className="flex items-center gap-2 text-sm text-destructive">
              <XCircleIcon className="size-4" aria-hidden="true" />
              {c.notTrusted}
            </span>
          ) : null}
        </div>
      </CardContent>
    </Card>
  );
}

export default function CertificatePage() {
  const [params, setParams] = useSearchParams();
  const detected = useMemo(() => detectOs(navigator.userAgent, navigator.maxTouchPoints), []);
  const osParam = params.get("os");
  const os: ClientOs = isClientOs(osParam) ? osParam : detected;
  const session = useSession();
  const network = useNetwork(Boolean(session.data));
  const instructions = useInstructions(os);
  const root = instructions.data?.root;
  const qrValue = root ? new URL(root.download_path, window.location.origin).toString() : null;
  const noCa = isProblem(instructions.error, "not-found");

  return (
    <div className="flex flex-col gap-4">
      <PageHeader title={c.title} description={c.intro} />
      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_18rem]">
        <Card>
          <CardContent className="flex flex-col gap-6">
            <Fingerprint hex={root?.fingerprint_sha256} colon={root?.fingerprint_sha256_colon} noCa={noCa} />
            <Tabs
              value={os}
              onValueChange={(v) => {
                const next = new URLSearchParams(params);
                next.set("os", String(v));
                setParams(next, { replace: true });
              }}
            >
              <div className="flex flex-col gap-2">
                <h2 className="font-semibold">{c.os}</h2>
                <div className="-mx-1 overflow-x-auto px-1 pb-1">
                  <TabsList aria-label={c.os}>
                    {CLIENT_OSES.map((o) => (
                      <TabsTrigger key={o} value={o} className="gap-1.5 px-3">
                        {c.osNames[o]}
                        {o === detected ? (
                          <Badge variant="primary" size="xs">
                            {c.detected}
                          </Badge>
                        ) : null}
                      </TabsTrigger>
                    ))}
                  </TabsList>
                </div>
              </div>
              {CLIENT_OSES.map((o) => (
                <TabsContent key={o} value={o} className="pt-4">
                  {o === os ? <Steps os={o} /> : null}
                </TabsContent>
              ))}
            </Tabs>
          </CardContent>
        </Card>
        <div className="flex flex-col gap-4">
          <Card size="sm">
            <CardHeader>
              <CardTitle>
                <h2>{c.qrTitle}</h2>
              </CardTitle>
            </CardHeader>
            <CardContent className="flex flex-col items-center gap-3">
              {qrValue ? (
                <QrCode value={qrValue} className="w-56" />
              ) : noCa ? (
                <div className="size-56 rounded-lg border border-dashed" aria-hidden="true" />
              ) : (
                <Skeleton className="size-56" />
              )}
              <p className="min-h-8 text-center text-xs text-soft-foreground">
                {noCa ? null : fmt(c.qr, { os: c.osNames[os] ?? os })}
              </p>
            </CardContent>
          </Card>
          {network.data ? <TrustCheck network={network.data} /> : null}
        </div>
      </div>
    </div>
  );
}
