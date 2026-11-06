import { useQuery } from "@tanstack/react-query";
import { CheckIcon, ChevronDownIcon, ChevronRightIcon, DownloadIcon, ShieldCheckIcon } from "lucide-react";
import { Fragment, useMemo, useState, type ReactNode } from "react";
import { useSearchParams } from "react-router";
import { apiUrl } from "@/api/client";
import { isProblem, useInstructions, useNetwork, useSession, useTls } from "@/api/queries";
import type { ClientOs, InstructionStep, Network, TlsInstructions, TlsState } from "@/api/types";
import { ProblemAlert } from "@/components/app/problem-alert";
import { QrCode } from "@/components/app/qr-code";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { FocusCard } from "@/components/v2/focus";
import { Illustration } from "@/components/v2/illustration";
import { MonoBlock } from "@/components/v2/mono-block";
import { Panel, WhatNext } from "@/components/v2/panel";
import { ProblemCard } from "@/components/v2/problem-card";
import { ProgressRing } from "@/components/v2/progress-ring";
import { StatusChip } from "@/components/v2/status-chip";
import { CA_NOTICE_KEY, caNoticeVisible, certPageUrl, fingerprintQr } from "@/features/connectivity/connectivity-logic";
import { fingerprintLines, formatDateTime } from "@/lib/format";
import { CLIENT_OSES, detectOs, isClientOs } from "@/lib/os";
import { fmt, strings, tlsStepText } from "@/lib/strings";
import { RotateCard } from "./rotate-card";

const c = strings.certificate;

// The step of each OS that people miss (plans/07 §4.3): the console names it.
const MISSED = new Set([
  "tls.install.ios.trust-settings",
  "tls.install.macos.trust",
  "tls.install.windows.store",
  "tls.install.android.install-anyway",
]);
// The fingerprint step goes to "Check the fingerprint (for experts)".
const FINGERPRINT_STEP = "tls.install.compare-fingerprint";

// "Open Settings > General > About > Certificate Trust Settings. Turn on …": the path of taps as chips.
function Path({ parts }: { parts: string[] }) {
  return (
    <span className="inline-flex flex-wrap items-center gap-1 align-middle">
      {parts.map((p, i) => (
        <span key={`${p}-${i}`} className="inline-flex items-center gap-1">
          {i > 0 ? <ChevronRightIcon className="size-3.5 text-ink-soft" aria-hidden="true" /> : null}
          <span className="rounded-md border border-line bg-surface-tint px-1.5 py-0.5 type-small font-medium text-ink">
            {p}
          </span>
        </span>
      ))}
    </span>
  );
}

const PATH_RE = /((?:[A-Z][\w]*(?: [A-Za-z][\w]*)*)(?: > [A-Za-z"][\w" ]*)+)/;

function StepText({ text }: { text: string }) {
  const m = PATH_RE.exec(text);
  if (!m || m.index === undefined) return <>{text}</>;
  const before = text.slice(0, m.index);
  const after = text.slice(m.index + m[0].length);
  return (
    <>
      {before}
      <Path parts={m[0].split(" > ").map((s) => s.trim())} />
      {after}
    </>
  );
}

function fileKind(data: TlsInstructions): "profile" | "pem" | "der" {
  if (data.root.filename.endsWith(".mobileconfig")) return "profile";
  return data.root.filename.endsWith(".pem") ? "pem" : "der";
}

// The numbered steps of one OS (4 at most), the step that people miss named, and the download with "what next".
function Steps({ os }: { os: ClientOs }) {
  const instructions = useInstructions(os);
  const data = instructions.data;
  if (instructions.isError) {
    // not-found: the local CA does not exist yet (Caddy makes it at its first start).
    const noCa = isProblem(instructions.error, "not-found");
    return (
      <ProblemAlert
        error={instructions.error}
        title={noCa ? c.noCa : undefined}
        onRetry={() => void instructions.refetch()}
      />
    );
  }
  if (!data) {
    return (
      <div className="flex flex-col gap-3" aria-busy="true">
        {[0, 1, 2, 3].map((i) => (
          <Skeleton key={i} className="h-14" />
        ))}
      </div>
    );
  }
  const steps = data.steps.filter((s) => s.key !== FINGERPRINT_STEP);
  const kind = fileKind(data);
  return (
    <div className="flex flex-col gap-5">
      <ol className="flex flex-col gap-1" data-testid="ca-steps">
        {steps.map((step: InstructionStep, i) => (
          <li
            key={step.key}
            className="grid grid-cols-[2.25rem_minmax(0,1fr)] gap-x-3 rounded-card p-2"
            data-key={step.key}
          >
            <span className="grid size-9 place-items-center rounded-full bg-surface-tint-strong type-data font-semibold text-primary-ink">
              {i + 1}
            </span>
            <div className="min-w-0 pt-1.5">
              {MISSED.has(step.key) ? (
                <p className="mb-1">
                  <StatusChip tone="warn" size="sm">
                    {c.missed}
                  </StatusChip>
                </p>
              ) : null}
              <p className="type-body break-words text-ink">
                <StepText text={tlsStepText(step.key, step.params)} />
              </p>
            </div>
          </li>
        ))}
      </ol>
      <div className="flex flex-col gap-2">
        <Button asChild size="lg" className="self-start">
          <a href={data.root.download_path} download={data.root.filename} data-testid="ca-download">
            <DownloadIcon data-icon="inline-start" />
            {kind === "profile" ? c.downloadProfile : kind === "pem" ? c.downloadPem : c.downloadDer}
          </a>
        </Button>
        <WhatNext>{c.downloadNext[os] ?? c.downloadNext.other}</WhatNext>
      </div>
      {data.notes.length > 0 ? (
        <ul className="flex flex-col gap-1 type-small text-ink-muted">
          {data.notes.map((n) => (
            <li key={n.key}>{tlsStepText(n.key, n.params)}</li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

// The trust check of docs/api/README.md section 2.7: the console requests another address of this device with mode
// no-cors. The request completes only when the browser trusts the certificate. It runs when the operator asks.
function checkTarget(network: Network | undefined): string {
  const here = window.location.hostname;
  const names = network ? [network.mdns.advertised, ...network.addresses].filter((h): h is string => Boolean(h)) : [];
  const other = names.find((h) => h !== here);
  // Without a session (no GET /network): the site of Caddy on port 443 of the same name is another origin.
  if (other) return `https://${other}:${window.location.port || "443"}/api/v1/health`;
  return `https://${here}/_care-pi/health`;
}

type CheckState = "idle" | "checking" | "ok" | "failed";

function TrustCheck({ network, missed }: { network: Network | undefined; missed: string | null }) {
  const [state, setState] = useState<CheckState>("idle");
  const target = checkTarget(network);
  const run = async () => {
    setState("checking");
    try {
      await fetch(target, { mode: "no-cors", cache: "no-store" });
      setState("ok");
    } catch {
      setState("failed");
    }
  };
  return (
    <Panel variant="tint" as="h2" title={c.trustCheck} description={c.trustCheckIntro} testId="trust-check">
      <div className="flex min-h-16 flex-wrap items-center gap-4" aria-live="polite" data-state={state}>
        {state === "ok" ? (
          <>
            <span className="grid size-12 place-items-center rounded-full bg-ok-solid text-white dark:text-black/80 motion-ok:animate-v2-pop">
              <CheckIcon className="size-7" strokeWidth={3} aria-hidden="true" />
            </span>
            <div className="min-w-0 flex-1">
              <p className="type-body font-semibold text-ok-fg">{c.trusted}</p>
              <p className="type-small text-ink-muted">{c.trustedNext}</p>
            </div>
          </>
        ) : state === "checking" ? (
          <>
            <ProgressRing value={null} size={48} stroke={5} label={c.checking} />
            <p className="type-body text-ink-muted">{c.checking}</p>
          </>
        ) : (
          <Button size="lg" onClick={() => void run()} data-testid="trust-check-run">
            <ShieldCheckIcon data-icon="inline-start" />
            {state === "failed" ? c.checkAgain : c.trustCheck}
          </Button>
        )}
      </div>
      {state === "failed" ? (
        <ProblemCard
          tone="warn"
          announce
          title={c.notTrusted}
          description={c.notTrustedText}
          steps={missed ? [<StepText key="missed" text={missed} />, c.thenCheckAgain] : [c.thenCheckAgain]}
          onRetry={() => void run()}
          retryLabel={c.checkAgain}
          technical={`GET ${target}\nmode: no-cors\nresult: the request did not complete (certificate not trusted, or the address is not reachable)`}
        />
      ) : null}
    </Panel>
  );
}

// The one-time warning after a new root certificate. The browser keeps the dismissal for the fingerprint.
function ChangedNotice({ tls }: { tls: TlsState }) {
  const fp = tls.local_ca.root?.fingerprint_sha256;
  const [dismissed, setDismissed] = useState(() => localStorage.getItem(CA_NOTICE_KEY));
  if (!caNoticeVisible(tls.local_ca.ca_changed_at, fp, dismissed)) return null;
  const rotated = tls.local_ca.change_reason === "rotation";
  const prev = tls.local_ca.previous_root;
  return (
    <div data-testid="ca-changed" data-reason={tls.local_ca.change_reason ?? ""}>
      <ProblemCard
        tone="warn"
        title={rotated ? c.rotatedTitle : c.changedTitle}
        description={
          <div className="flex flex-col gap-2">
            <p>
              {rotated
                ? fmt(c.rotated, { time: formatDateTime(tls.local_ca.ca_changed_at) })
                : fmt(c.changed, {
                    time: formatDateTime(tls.local_ca.ca_changed_at),
                    fingerprint: fingerprintLines(fp ?? "")[0] ?? "",
                  })}
            </p>
            {rotated && fp ? (
              <dl className="grid gap-x-4 gap-y-1 type-data sm:grid-cols-2" data-testid="ca-fingerprints">
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
                    <dd className="text-ink-soft">
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
              <p className="type-data">
                {fmt(c.changedOld, {
                  fingerprint: fingerprintLines(tls.local_ca.previous_fingerprint_sha256)[0] ?? "",
                })}
              </p>
            ) : null}
          </div>
        }
        actions={
          <Button
            type="button"
            size="sm"
            variant="outline"
            onClick={() => {
              if (fp) localStorage.setItem(CA_NOTICE_KEY, fp);
              setDismissed(fp ?? null);
            }}
            data-testid="ca-changed-dismiss"
          >
            {c.changedDismiss}
          </Button>
        }
      />
    </div>
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

function Disclosure({ title, children, testId }: { title: string; children: ReactNode; testId?: string }) {
  return (
    <details className="group rounded-card border border-line-soft" data-testid={testId}>
      <summary className="flex h-12 cursor-pointer list-none items-center gap-2 px-4 type-small font-medium text-ink-muted outline-none select-none hover:text-ink focus-visible:ring-3 focus-visible:ring-ring/50 [&::-webkit-details-marker]:hidden">
        <ChevronDownIcon
          className="size-4 transition-transform group-open:rotate-180 motion-reduce:transition-none"
          aria-hidden="true"
        />
        {title}
      </summary>
      <div className="flex flex-col gap-4 px-4 pb-4">{children}</div>
    </details>
  );
}

// The certificate ceremony (plans/07 §4.3, console/design.md section 15): "this is normal and safe" first, the OS of
// this device, one path at a time with the exact taps, the download, the trust check with a green tick, a QR code for
// a phone, and the fingerprint only in "Check the fingerprint (for experts)".
function Ceremony() {
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
  const missedStep = instructions.data?.steps.find((s) => MISSED.has(s.key));
  const fingerprintStep = instructions.data?.steps.find((s) => s.key === FINGERPRINT_STEP);
  const constraints = tls.data?.local_ca.root?.name_constraints;

  return (
    <>
      {tls.data ? <ChangedNotice tls={tls.data} /> : null}
      {publicName ? (
        <p className="rounded-card border border-info-border bg-info-bg p-4 type-body text-info-fg">
          {fmt(c.publicHint, { name: `https://${publicName}/` })}
        </p>
      ) : null}
      <Tabs
        value={os}
        onValueChange={(v) => {
          const next = new URLSearchParams(params);
          next.set("os", String(v));
          setParams(next, { replace: true });
        }}
      >
        <div className="flex flex-col gap-2">
          <h2 className="type-h3 text-ink">{c.os}</h2>
          <div className="-mx-1 overflow-x-auto px-1 pb-1">
            <TabsList aria-label={c.os} className="max-w-full justify-start">
              {CLIENT_OSES.map((o) => (
                <TabsTrigger key={o} value={o} className="gap-1.5 px-3">
                  {c.osNames[o]}
                  {o === detected ? (
                    <StatusChip tone="primary" size="sm">
                      {c.detected}
                    </StatusChip>
                  ) : null}
                </TabsTrigger>
              ))}
            </TabsList>
          </div>
        </div>
      </Tabs>
      <section aria-label={c.osNames[os] ?? os}>
        <Steps os={os} />
      </section>
      <TrustCheck network={network.data} missed={missedStep ? tlsStepText(missedStep.key, missedStep.params) : null} />
      <section className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-start" aria-labelledby="qr-title">
        <div className="flex min-w-0 flex-col gap-3">
          <div className="flex items-center gap-4">
            <Illustration name="phone-qr" size="sm" className="w-20 shrink-0" />
            <div>
              <h2 id="qr-title" className="type-h3 text-ink">
                {c.qrTitle}
              </h2>
              <p className="min-h-10 type-small text-ink-muted" data-testid="qr-text" data-qr={qrValue ?? ""}>
                {noCa ? null : qrText}
              </p>
            </div>
          </div>
          <div className="flex flex-col gap-1">
            <span className="type-small text-ink-soft" id="qr-mode-label">
              {c.qrMode}
            </span>
            <ToggleGroup
              aria-labelledby="qr-mode-label"
              variant="outline"
              size="sm"
              value={[qrMode]}
              onValueChange={(v: string[]) => {
                const next = v[0] as QrMode | undefined;
                if (next) setQrMode(next);
              }}
              className="flex-wrap justify-start"
              data-testid="qr-mode"
            >
              {(["page", "fingerprint", "download"] as const).map((m) => (
                <ToggleGroupItem key={m} value={m}>
                  {c.qrModes[m]}
                </ToggleGroupItem>
              ))}
            </ToggleGroup>
          </div>
        </div>
        {qrValue ? (
          <QrCode value={qrValue} className="size-40 justify-self-start border border-line p-1" />
        ) : noCa ? (
          <div className="size-40 rounded-lg border border-dashed border-line-strong" aria-hidden="true" />
        ) : (
          <Skeleton className="size-40" />
        )}
      </section>
      <Disclosure title={c.experts} testId="ca-experts">
        <MonoBlock
          label={c.fingerprint}
          text={root ? fingerprintLines(root.fingerprint_sha256).join("\n") : noCa ? c.noCa : "—"}
          testId="ca-fingerprint"
        />
        <p className="type-small text-ink-muted">
          {fingerprintStep ? tlsStepText(fingerprintStep.key, fingerprintStep.params) : c.fingerprintHelp}
        </p>
        {constraints ? (
          <div className="flex flex-col gap-2" data-testid="ca-constraints">
            <h3 className="type-small font-semibold text-ink">{c.constraints}</h3>
            <p className="type-small text-ink-muted">{c.constraintsHelp}</p>
            <dl className="grid grid-cols-[minmax(0,8rem)_minmax(0,1fr)] gap-x-3 gap-y-1 type-small">
              <dt className="text-ink-soft">{c.constraintsDns}</dt>
              <dd className="min-w-0 type-data" data-testid="ca-constraints-dns">
                {constraints.permitted_dns_domains.map((d) => (
                  <span key={d} className="block break-words">
                    {d}
                  </span>
                ))}
              </dd>
              <dt className="text-ink-soft">{c.constraintsIp}</dt>
              <dd className="min-w-0 type-data" data-testid="ca-constraints-ip">
                {constraints.permitted_ip_ranges.map((r) => (
                  <span key={r} className="block">
                    {r}
                  </span>
                ))}
              </dd>
            </dl>
            {(tls.data?.local_ca.uncovered.length ?? 0) > 0 ? (
              <p className="type-small text-warn-fg">
                {fmt(c.uncovered, { names: tls.data?.local_ca.uncovered.join(", ") ?? "" })}
              </p>
            ) : null}
          </div>
        ) : tls.data?.local_ca.root ? (
          <p className="type-small text-ink-muted" data-testid="ca-constraints">
            {c.constraintsNone}
          </p>
        ) : null}
        <div className="flex flex-col gap-2">
          <p className="type-small text-ink-soft">{c.otherFormats}</p>
          <div className="flex flex-wrap gap-2">
            {[
              [apiUrl("/tls/root-ca", { format: "der" }), c.downloadDer],
              [apiUrl("/tls/root-ca", { format: "pem" }), c.downloadPem],
              [apiUrl("/tls/root-ca/mobileconfig"), c.downloadProfile],
            ].map(([href, label]) => (
              <Fragment key={href}>
                <Button asChild variant="outline" size="sm">
                  <a href={href} download>
                    {label}
                  </a>
                </Button>
              </Fragment>
            ))}
          </div>
        </div>
      </Disclosure>
      {tls.data && tls.data.local_ca.enabled ? <RotateCard tls={tls.data} /> : null}
    </>
  );
}

export default function CertificatePage() {
  const session = useSession();
  if (session.data) {
    // With a session: the shell has the main landmark; the ceremony is one stage card in it.
    return (
      <div className="mx-auto flex max-w-3xl flex-col gap-6 rounded-stage border border-line bg-surface-card p-6 shadow-soft sm:p-8">
        <header className="flex flex-col gap-3">
          <Illustration name="shield-certificate" size="sm" className="w-24" />
          <h1 className="type-h1 text-ink">{c.title}</h1>
          <p className="type-body text-ink-muted">{c.intro}</p>
        </header>
        <Ceremony />
      </div>
    );
  }
  return (
    <FocusCard width="lg" illustration="shield-certificate" title={c.title} description={c.intro}>
      <Ceremony />
    </FocusCard>
  );
}
