import { useQuery } from "@tanstack/react-query";
import { CheckCircle2Icon, DownloadIcon, ShieldQuestionIcon, XCircleIcon } from "lucide-react";
import { useMemo, useState } from "react";
import { useSearchParams } from "react-router";
import { apiUrl } from "@/api/client";
import { isProblem, useInstructions, useNetwork, useSession, useTls } from "@/api/queries";
import type { ClientOs, Network, TlsState } from "@/api/types";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { CA_NOTICE_KEY, caNoticeVisible, certPageUrl, fingerprintQr } from "@/features/connectivity/connectivity-logic";
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
import { fingerprintLines, formatDateTime } from "@/lib/format";
import { CLIENT_OSES, detectOs, isClientOs } from "@/lib/os";
import { fmt, strings, tlsStepText } from "@/lib/strings";
import { RotateCard } from "./rotate-card";

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

// The name constraints of the root (07 §4.1): the names and the address ranges that the root can sign.
function Constraints({ tls }: { tls: TlsState }) {
  const root = tls.local_ca.root;
  if (!root) return null;
  const nc = root.name_constraints;
  return (
    <section className="flex flex-col gap-2" data-testid="ca-constraints">
      <h2 className="font-semibold">{c.constraints}</h2>
      {nc ? (
        <>
          <p className="text-xs text-soft-foreground">{c.constraintsHelp}</p>
          <dl className="grid grid-cols-[minmax(0,8rem)_minmax(0,1fr)] gap-x-3 gap-y-1 text-sm">
            <dt className="text-soft-foreground">{c.constraintsDns}</dt>
            <dd className="min-w-0 font-mono" data-testid="ca-constraints-dns">
              {nc.permitted_dns_domains.map((d) => (
                <span key={d} className="block break-words">
                  {d}
                </span>
              ))}
            </dd>
            <dt className="text-soft-foreground">{c.constraintsIp}</dt>
            <dd className="min-w-0 font-mono" data-testid="ca-constraints-ip">
              {nc.permitted_ip_ranges.map((r) => (
                <span key={r} className="block">
                  {r}
                </span>
              ))}
            </dd>
          </dl>
          {tls.local_ca.uncovered.length > 0 ? (
            <Alert variant="warning">
              <AlertDescription>{fmt(c.uncovered, { names: tls.local_ca.uncovered.join(", ") })}</AlertDescription>
            </Alert>
          ) : null}
        </>
      ) : (
        <p className="text-sm text-soft-foreground">{c.constraintsNone}</p>
      )}
    </section>
  );
}

// The one-time warning after the switch to the name-constrained root. The browser keeps the dismissal for the
// fingerprint of the new root.
function ChangedNotice({ tls }: { tls: TlsState }) {
  const fp = tls.local_ca.root?.fingerprint_sha256;
  const [dismissed, setDismissed] = useState(() => localStorage.getItem(CA_NOTICE_KEY));
  if (!caNoticeVisible(tls.local_ca.ca_changed_at, fp, dismissed)) return null;
  const rotated = tls.local_ca.change_reason === "rotation";
  const prev = tls.local_ca.previous_root;
  return (
    <Alert variant="warning" data-testid="ca-changed" data-reason={tls.local_ca.change_reason ?? ""}>
      <AlertTitle>{rotated ? c.rotatedTitle : c.changedTitle}</AlertTitle>
      <AlertDescription className="flex flex-col gap-2">
        <p>
          {rotated
            ? fmt(c.rotated, { time: formatDateTime(tls.local_ca.ca_changed_at) })
            : fmt(c.changed, {
                time: formatDateTime(tls.local_ca.ca_changed_at),
                fingerprint: fingerprintLines(fp ?? "")[0] ?? "",
              })}
        </p>
        {rotated && fp ? (
          <dl className="grid gap-x-4 gap-y-1 text-xs sm:grid-cols-2" data-testid="ca-fingerprints">
            <div>
              <dt className="font-semibold">{c.rotatedNew}</dt>
              <dd>
                {fingerprintLines(fp).map((l) => (
                  <span key={l} className="block">
                    {l}
                  </span>
                ))}
              </dd>
            </div>
            {prev ? (
              <div>
                <dt className="font-semibold">{c.rotatedOld}</dt>
                <dd className="text-soft-foreground">
                  {fingerprintLines(prev.fingerprint_sha256).map((l) => (
                    <span key={l} className="block">
                      {l}
                    </span>
                  ))}
                </dd>
              </div>
            ) : null}
          </dl>
        ) : tls.local_ca.previous_fingerprint_sha256 ? (
          <p className="text-xs">
            {fmt(c.changedOld, { fingerprint: fingerprintLines(tls.local_ca.previous_fingerprint_sha256)[0] ?? "" })}
          </p>
        ) : null}
        <Button
          type="button"
          size="sm"
          variant="outline"
          className="self-start"
          onClick={() => {
            if (fp) localStorage.setItem(CA_NOTICE_KEY, fp);
            setDismissed(fp ?? null);
          }}
          data-testid="ca-changed-dismiss"
        >
          {c.changedDismiss}
        </Button>
      </AlertDescription>
    </Alert>
  );
}

type QrMode = "page" | "fingerprint" | "download";

// The DER root for the QR code of the certificate page (07 §4.4).
function useRootDer(enabled: boolean) {
  return useQuery({
    queryKey: ["tls-root-der"],
    queryFn: async () => {
      const res = await fetch(apiUrl("/tls/root-ca", { format: "der" }), { credentials: "same-origin" });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      return new Uint8Array(await res.arrayBuffer());
    },
    enabled,
    staleTime: 300_000,
  });
}

export default function CertificatePage() {
  const [params, setParams] = useSearchParams();
  const detected = useMemo(() => detectOs(navigator.userAgent, navigator.maxTouchPoints), []);
  const osParam = params.get("os");
  const os: ClientOs = isClientOs(osParam) ? osParam : detected;
  const session = useSession();
  const network = useNetwork(Boolean(session.data));
  const instructions = useInstructions(os);
  const tls = useTls(Boolean(session.data));
  const root = instructions.data?.root;
  const noCa = isProblem(instructions.error, "not-found");
  const [qrMode, setQrMode] = useState<QrMode>("page");
  const der = useRootDer(Boolean(root) && qrMode === "page");
  const pageUrl = tls.data?.local_ca.cert_page_url ?? "https://care-pi.rithviknishad.dev/cert";
  const qrValue = !root
    ? null
    : qrMode === "fingerprint"
      ? fingerprintQr(root.fingerprint_sha256_colon)
      : qrMode === "page"
        ? der.data
          ? certPageUrl(pageUrl, der.data)
          : null
        : new URL(root.download_path, window.location.origin).toString();
  const qrText =
    qrMode === "page"
      ? c.qrPage
      : qrMode === "fingerprint"
        ? c.qrFingerprint
        : fmt(c.qrDownload, { os: c.osNames[os] ?? os });
  const publicName = tls.data?.public.hostname;

  return (
    <div className="flex flex-col gap-4">
      <PageHeader title={c.title} description={c.intro} />
      {tls.data ? <ChangedNotice tls={tls.data} /> : null}
      {publicName ? (
        <Alert variant="info">
          <AlertDescription>{fmt(c.publicHint, { name: `https://${publicName}/` })}</AlertDescription>
        </Alert>
      ) : null}
      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_18rem]">
        <Card>
          <CardContent className="flex flex-col gap-6">
            <Fingerprint hex={root?.fingerprint_sha256} colon={root?.fingerprint_sha256_colon} noCa={noCa} />
            {tls.data ? <Constraints tls={tls.data} /> : null}
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
              <div className="flex w-full flex-col gap-1">
                <span className="text-xs text-soft-foreground" id="qr-mode-label">
                  {c.qrMode}
                </span>
                <ToggleGroup
                  aria-labelledby="qr-mode-label"
                  orientation="vertical"
                  variant="outline"
                  size="sm"
                  value={[qrMode]}
                  onValueChange={(v: string[]) => {
                    const next = v[0] as QrMode | undefined;
                    if (next) setQrMode(next);
                  }}
                  className="w-full"
                  data-testid="qr-mode"
                >
                  {(["page", "fingerprint", "download"] as const).map((m) => (
                    <ToggleGroupItem key={m} value={m} className="w-full justify-start text-xs">
                      {c.qrModes[m]}
                    </ToggleGroupItem>
                  ))}
                </ToggleGroup>
              </div>
              {qrValue ? (
                <QrCode value={qrValue} className="w-56" />
              ) : noCa ? (
                <div className="size-56 rounded-lg border border-dashed" aria-hidden="true" />
              ) : (
                <Skeleton className="size-56" />
              )}
              <p
                className="min-h-16 text-center text-xs text-soft-foreground"
                data-testid="qr-text"
                data-qr={qrValue ?? ""}
              >
                {noCa ? null : qrText}
              </p>
            </CardContent>
          </Card>
          {network.data ? <TrustCheck network={network.data} /> : null}
          {tls.data && tls.data.local_ca.enabled ? <RotateCard tls={tls.data} /> : null}
        </div>
      </div>
    </div>
  );
}
