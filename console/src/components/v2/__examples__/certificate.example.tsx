import { CheckIcon, ChevronDownIcon, ChevronRightIcon, DownloadIcon, ShieldCheckIcon } from "lucide-react";
import { useState, type ReactNode } from "react";
import { QrCode } from "@/components/app/qr-code";
import { Button } from "@/components/ui/button";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { FocusBackdrop, FocusBar, FocusCard } from "../focus";
import { Illustration } from "../illustration";
import { MonoBlock } from "../mono-block";
import { Panel, WhatNext } from "../panel";
import { ProblemCard } from "../problem-card";
import { ProgressRing } from "../progress-ring";
import { StatusChip } from "../status-chip";
import { FocusBrand, FocusEnd } from "./focus.example";

const FINGERPRINT =
  "21:69:AE:1D:DD:1B:4E:76\nC0:E5:35:CA:0F:86:B1:2D\n16:72:8C:4D:A6:54:E8:AB\n00:14:28:AD:7F:38:99:3F";

function Path({ parts }: { parts: string[] }) {
  return (
    <span className="inline-flex flex-wrap items-center gap-1 align-middle">
      {parts.map((p, i) => (
        <span key={p} className="inline-flex items-center gap-1">
          {i > 0 ? <ChevronRightIcon className="size-3.5 text-ink-soft" aria-hidden="true" /> : null}
          <span className="rounded-md border border-line bg-surface-tint px-1.5 py-0.5 type-small font-medium text-ink">
            {p}
          </span>
        </span>
      ))}
    </span>
  );
}

const STEPS: { title: string; body: ReactNode; path?: string[] }[] = [
  {
    title: "Download the profile",
    body: (
      <>
        Tap the green button below. Safari asks to allow the download. Tap <b>Allow</b>.
      </>
    ),
  },
  {
    title: "Install the profile",
    body: (
      <>
        Open the Settings app and tap <b>Profile Downloaded</b>. Tap <b>Install</b>, enter the code of your phone, and
        tap <b>Install</b> again.
      </>
    ),
    path: ["Settings", "Profile Downloaded"],
  },
  {
    title: "Turn on full trust",
    body: (
      <>
        Many people miss this step: without it, the warning stays. Turn on <b>care.local</b> here:
      </>
    ),
    path: ["Settings", "General", "About", "Certificate Trust Settings"],
  },
  {
    title: "Check",
    body: (
      <>
        Come back to this page and tap <b>Check the trust</b>.
      </>
    ),
  },
];

type CheckState = "idle" | "checking" | "ok" | "failed";

function TrustCheck() {
  const [state, setState] = useState<CheckState>("idle");
  const run = (result: CheckState) => {
    setState("checking");
    setTimeout(() => setState(result), 1200);
  };
  return (
    <Panel
      variant="tint"
      as="h3"
      title="Check the trust"
      description="We open a second address of this device. It works only when the browser trusts the device."
    >
      <div className="flex min-h-16 flex-wrap items-center gap-4" aria-live="polite">
        {state === "ok" ? (
          <>
            <span className="grid size-12 place-items-center rounded-full bg-ok-solid text-white dark:text-black/80 motion-ok:animate-v2-pop">
              <CheckIcon className="size-7" strokeWidth={3} aria-hidden="true" />
            </span>
            <div className="min-w-0 flex-1">
              <p className="type-body font-semibold text-ok-fg">This browser trusts the device</p>
              <p className="type-small text-ink-muted">
                You can close this page. Do the same on each computer and phone of the clinic.
              </p>
            </div>
          </>
        ) : state === "checking" ? (
          <>
            <ProgressRing value={null} size={48} stroke={5} label="Checking" />
            <p className="type-body text-ink-muted">Checking…</p>
          </>
        ) : (
          <>
            <Button size="lg" onClick={() => run("ok")}>
              <ShieldCheckIcon data-icon="inline-start" />
              Check the trust
            </Button>
            <Button variant="ghost" className="no-underline" onClick={() => run("failed")}>
              Show a failed check
            </Button>
          </>
        )}
      </div>
      {state === "failed" ? (
        <ProblemCard
          tone="warn"
          announce
          title="This browser does not trust the device yet"
          description="This is usual after step 2. The trust switch of step 3 is still off."
          steps={[
            <>
              Open the trust settings: <Path parts={["Settings", "General", "About", "Certificate Trust Settings"]} />
            </>,
            "Turn on care.local, then check again.",
          ]}
          onRetry={() => run("ok")}
          retryLabel="Check again"
          technical={
            "GET https://192.168.165.91:8443/api/v1/health\nmode: no-cors\nresult: TypeError: Failed to fetch (certificate not trusted)"
          }
        />
      ) : null}
    </Panel>
  );
}

// The certificate ceremony (plans/07 §4.3): the scariest moment, so it is calm, one OS at a time, numbered, with
// the exact path of each tap, the trust check at the end, and the fingerprint only in a disclosure.
export function CertificateExample() {
  return (
    <FocusBackdrop top={<FocusBar start={<FocusBrand />} end={<FocusEnd />} />}>
      <FocusCard
        width="lg"
        illustration="shield-certificate"
        title="Make this browser trust your Care box"
        description="Your browser shows a warning because it does not know this device yet. This is normal and safe. Install the certificate of the device one time on each computer and phone. Then the warning goes away."
      >
        <Tabs defaultValue="ios">
          <TabsList className="max-w-full justify-start overflow-x-auto">
            <TabsTrigger value="ios">
              iPhone and iPad{" "}
              <StatusChip tone="primary" size="sm">
                This device
              </StatusChip>
            </TabsTrigger>
            <TabsTrigger value="android">Android</TabsTrigger>
            <TabsTrigger value="windows">Windows</TabsTrigger>
            <TabsTrigger value="mac">Mac</TabsTrigger>
          </TabsList>
        </Tabs>
        <ol className="flex flex-col gap-1">
          {STEPS.map((s, i) => (
            <li key={s.title} className="grid grid-cols-[2.25rem_minmax(0,1fr)] gap-x-3 rounded-card p-2">
              <span className="grid size-9 place-items-center rounded-full bg-surface-tint-strong type-data type-body font-semibold text-primary-ink">
                {i + 1}
              </span>
              <div className="min-w-0 pt-1.5">
                <p className="type-body font-semibold text-ink">{s.title}</p>
                <p className="mt-1 type-body text-ink-muted">{s.body}</p>
                {s.path ? (
                  <p className="mt-2">
                    <Path parts={s.path} />
                  </p>
                ) : null}
              </div>
            </li>
          ))}
        </ol>
        <div className="flex flex-col gap-2">
          <Button size="lg" className="self-start">
            <DownloadIcon data-icon="inline-start" />
            Download the profile
          </Button>
          <WhatNext>Safari asks to allow the download. Then do step 2.</WhatNext>
        </div>
        <TrustCheck />
        <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center">
          <div className="flex items-center gap-4">
            <Illustration name="phone-qr" size="sm" className="w-20" />
            <div>
              <p className="type-body font-semibold text-ink">Install on a phone</p>
              <p className="type-small text-ink-muted">
                Scan this code with the camera of the phone. It opens this page there.
              </p>
            </div>
          </div>
          <QrCode
            value="https://care.local:8443/certificate?os=ios"
            className="size-32 justify-self-start border border-line p-1"
            label="QR code of this page"
          />
        </div>
        <details className="group rounded-card border border-line-soft p-3">
          <summary className="flex cursor-pointer list-none items-center gap-1.5 type-small font-medium text-ink-muted outline-none focus-visible:ring-3 focus-visible:ring-ring/50 [&::-webkit-details-marker]:hidden">
            <ChevronDownIcon
              className="size-4 transition-transform group-open:rotate-180 motion-reduce:transition-none"
              aria-hidden="true"
            />
            Check the fingerprint (for experts)
          </summary>
          <MonoBlock className="mt-3" label="Fingerprint (SHA-256)" text={FINGERPRINT} />
        </details>
      </FocusCard>
    </FocusBackdrop>
  );
}
