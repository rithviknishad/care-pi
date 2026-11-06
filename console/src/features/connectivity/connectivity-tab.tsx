import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  ExternalLinkIcon,
  GlobeIcon,
  NetworkIcon,
  RadarIcon,
  ShieldQuestionIcon,
  WifiIcon,
  BookmarkIcon,
} from "lucide-react";
import { useState } from "react";
import { api, call } from "@/api/client";
import { qk, useNetwork, useTls, useWifiScan } from "@/api/queries";
import type { Job, Network, TlsState, WifiState } from "@/api/types";
import { CopyButton } from "@/components/app/copy-button";
import { FormField, PasswordInput } from "@/components/app/form-field";
import { DataList, DataRow } from "@/components/app/page";
import { ProblemAlert } from "@/components/app/problem-alert";
import { QrCode } from "@/components/app/qr-code";
import { isReauthCancelled, useReauth } from "@/components/app/reauth-context";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import { JobProgress } from "@/features/jobs/job-progress";
import { describedBy } from "@/lib/form";
import { formatDateTime } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";
import {
  STAGING_CA,
  rebindingCheck,
  signalLevel,
  sortNetworks,
  validAuthKey,
  validControlUrl,
  validHint,
  validateWifi,
  type RebindResult,
  type WifiForm,
} from "./connectivity-logic";

const cs = strings.connectivity;

type Card = "public" | "tailscale" | "wifi";
interface Running {
  card: Card;
  job: Job;
  // done is true after the job ends: the progress stays on the page, and the cards accept a new change.
  done?: boolean;
}

function CardFrame({
  title,
  icon,
  badge,
  testId,
  children,
}: {
  title: string;
  icon: React.ReactNode;
  badge?: React.ReactNode;
  testId: string;
  children: React.ReactNode;
}) {
  return (
    <section
      className="flex min-w-0 flex-col gap-4 rounded-card border border-line bg-surface-card p-4 shadow-soft sm:p-5"
      data-testid={testId}
      aria-label={title}
    >
      <header className="flex min-h-8 flex-wrap items-center gap-3">
        <span
          className="grid size-10 shrink-0 place-items-center rounded-lg bg-surface-tint-strong text-primary-solid"
          aria-hidden="true"
        >
          {icon}
        </span>
        <h3 className="flex-1 type-h3 text-ink">{title}</h3>
        {badge}
      </header>
      {children}
    </section>
  );
}

function OnOff({ on }: { on: boolean }) {
  return (
    <Badge variant={on ? "success" : "neutral"} size="sm" data-testid="mode-state">
      {on ? cs.on : cs.off}
    </Badge>
  );
}

function ConfirmButton({
  label,
  title,
  description,
  onConfirm,
  disabled,
  testId,
}: {
  label: string;
  title: string;
  description: string;
  onConfirm: () => void;
  disabled?: boolean;
  testId: string;
}) {
  const [open, setOpen] = useState(false);
  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <Button
        type="button"
        variant="outline"
        className="self-start"
        onClick={() => setOpen(true)}
        disabled={disabled}
        data-testid={testId}
      >
        {label}
      </Button>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{strings.common.cancel}</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive-solid"
            onClick={() => {
              setOpen(false);
              onConfirm();
            }}
          >
            {label}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

// ---- the public name ----

function RebindingCheck({ url }: { url: string }) {
  const [state, setState] = useState<"idle" | "busy" | RebindResult>("idle");
  return (
    <div className="flex flex-col gap-2">
      <p className="text-xs text-soft-foreground">{cs.rebindingHelp}</p>
      <div className="flex min-h-9 flex-wrap items-center gap-3">
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={state === "busy"}
          onClick={() => {
            setState("busy");
            void rebindingCheck(url).then(setState);
          }}
          data-testid="rebinding-check"
        >
          {state === "busy" ? <Spinner data-icon="inline-start" /> : <ShieldQuestionIcon data-icon="inline-start" />}
          {cs.rebinding}
        </Button>
        {state === "ok" ? <span className="text-sm text-green-700 dark:text-green-400">{cs.rebindingOk}</span> : null}
      </div>
      {state === "blocked" ? (
        <Alert variant="warning">
          <AlertDescription>{cs.rebindingBlocked}</AlertDescription>
        </Alert>
      ) : null}
    </div>
  );
}

function PublicCard({
  tls,
  running,
  onJob,
  hostname,
}: {
  tls: TlsState;
  running: Running | null;
  onJob: (r: Running) => void;
  hostname: string;
}) {
  const pub = tls.public;
  const [hint, setHint] = useState(hostname);
  const [staging, setStaging] = useState(false);
  const hintError = hint !== "" && !validHint(hint) ? cs.hintInvalid : undefined;
  const run = useMutation({
    mutationFn: (enabled: boolean) =>
      call(
        api.PUT("/tls/public", {
          body: enabled
            ? { enabled: true, hostname_hint: hint || null, acme_ca: staging ? STAGING_CA : "" }
            : { enabled: false },
        }),
      ),
    onSuccess: (job) => onJob({ card: "public", job }),
  });
  const busy = Boolean(running && !running.done) || run.isPending;
  const host = pub.hostname;
  const cert = pub.certificate;
  return (
    <CardFrame
      title={cs.publicTitle}
      icon={<GlobeIcon className="size-5" />}
      badge={<OnOff on={pub.enabled} />}
      testId="public-card"
    >
      <p className="max-w-prose type-small text-ink-muted">{cs.publicIntro}</p>
      {pub.enabled && host ? (
        <>
          <div className="flex flex-col gap-2 rounded-card border border-line-soft bg-surface-tint p-4">
            <span className="flex min-w-0 items-center gap-2">
              <span className="min-w-0 truncate font-mono text-xl font-semibold text-ink">https://{host}/</span>
              <CopyButton value={`https://${host}/`} />
            </span>
            <p className="flex items-start gap-2 type-body text-ink" data-testid="public-bookmark">
              <BookmarkIcon className="mt-1 size-4 shrink-0 text-primary-solid" aria-hidden="true" />
              {fmt(cs.bookmark, { url: `https://${host}/` })}
            </p>
          </div>
          <DataList>
            <DataRow
              label={cs.name}
              value={
                <span className="flex min-w-0 items-center gap-1">
                  <a
                    href={`https://${host}/`}
                    className="truncate underline-offset-4 hover:underline"
                    data-testid="public-name"
                  >
                    {host}
                  </a>
                  <CopyButton value={`https://${host}/`} />
                </span>
              }
            />
            <DataRow
              label={cs.certificate}
              value={
                <span className="flex flex-wrap items-center gap-2" data-testid="public-cert">
                  <Badge
                    variant={cert?.state === "issued" ? "success" : cert?.state === "pending" ? "info" : "destructive"}
                    size="sm"
                  >
                    {cs.certStates[cert?.state ?? "pending"]}
                  </Badge>
                  {cert?.not_after ? (
                    <span className="text-xs">{fmt(cs.validUntil, { time: formatDateTime(cert.not_after) })}</span>
                  ) : null}
                </span>
              }
            />
            <DataRow
              label={cs.dnsAddress}
              value={
                pub.dns.address
                  ? fmt(cs.dnsUpdated, { address: pub.dns.address, time: formatDateTime(pub.dns.updated_at) })
                  : "—"
              }
            />
          </DataList>
          {cert?.staging ? (
            <Alert variant="warning">
              <AlertDescription>{cs.stagingCert}</AlertDescription>
            </Alert>
          ) : null}
          {pub.dns.revoked ? (
            <Alert variant="destructive">
              <AlertDescription>{cs.revoked}</AlertDescription>
            </Alert>
          ) : pub.dns.last_error ? (
            <Alert variant="warning">
              <AlertDescription>{fmt(cs.dnsError, { error: pub.dns.last_error })}</AlertDescription>
            </Alert>
          ) : null}
          <ul className="flex list-disc flex-col gap-1 pl-4 type-small text-ink-muted">
            {pub.primary ? <li>{cs.primary}</li> : null}
            <li>{cs.hsts}</li>
          </ul>
          {pub.health_url ? <RebindingCheck url={pub.health_url} /> : null}
          <ConfirmButton
            label={cs.turnOff}
            title={cs.publicOffConfirm}
            description={`${cs.publicOffHelp} ${cs.restartNote}`}
            onConfirm={() => run.mutate(false)}
            disabled={busy}
            testId="public-off"
          />
        </>
      ) : (
        <form
          className="flex flex-col gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (!hintError) run.mutate(true);
          }}
        >
          <p className="text-xs text-soft-foreground">{cs.publicNeeds}</p>
          <FormField id="public-hint" label={cs.hint} help={cs.hintHelp} error={hintError}>
            <Input
              id="public-hint"
              value={hint}
              onChange={(e) => setHint(e.target.value.trim().toLowerCase())}
              spellCheck={false}
              autoCapitalize="none"
              aria-invalid={hintError ? true : undefined}
              aria-describedby={describedBy("public-hint", true, hintError)}
              className="max-w-xs"
            />
          </FormField>
          <Label className="flex items-start gap-3 font-normal">
            <Switch checked={staging} onCheckedChange={setStaging} data-testid="public-staging" />
            <span className="flex flex-col">
              <span>{cs.staging}</span>
              <span className="text-xs text-soft-foreground">{cs.stagingHelp}</span>
            </span>
          </Label>
          <p className="text-xs text-soft-foreground">
            {cs.publicLimits} {cs.restartNote}
          </p>
          <Button type="submit" className="self-start" disabled={busy || Boolean(hintError)} data-testid="public-on">
            {run.isPending ? <Spinner data-icon="inline-start" /> : null}
            {cs.turnOn}
          </Button>
        </form>
      )}
      {run.error ? <ProblemAlert error={run.error} /> : null}
    </CardFrame>
  );
}

// ---- Tailscale ----

function LoginBox({ url }: { url: string }) {
  return (
    <div
      className="flex flex-col gap-3 rounded-lg border p-4 sm:flex-row sm:items-center"
      data-testid="tailscale-login"
    >
      <QrCode value={url} className="w-40 shrink-0 self-center" label={cs.loginTitle} />
      <div className="flex min-w-0 flex-col gap-2">
        <h4 className="font-semibold">{cs.loginTitle}</h4>
        <p className="text-sm">{cs.login}</p>
        <div className="flex flex-wrap items-center gap-2">
          <Button asChild size="sm">
            <a href={url} target="_blank" rel="noreferrer noopener" data-testid="tailscale-login-link">
              <ExternalLinkIcon data-icon="inline-start" />
              {cs.loginOpen}
            </a>
          </Button>
          <CopyButton value={url} />
        </div>
        <p className="flex items-center gap-2 text-xs text-soft-foreground">
          <Spinner className="size-3" />
          {cs.loginWait}
        </p>
      </div>
    </div>
  );
}

function TailscaleCard({
  tls,
  running,
  onJob,
}: {
  tls: TlsState;
  running: Running | null;
  onJob: (r: Running) => void;
}) {
  const ts = tls.tailscale;
  const { withReauth } = useReauth();
  const [key, setKey] = useState("");
  const [controlUrl, setControlUrl] = useState(ts.control_url ?? "");
  const keyError = !validAuthKey(key, controlUrl) ? cs.authKeyInvalid : undefined;
  const controlError = !validControlUrl(controlUrl) ? cs.controlUrlInvalid : undefined;
  const run = useMutation({
    mutationFn: (enabled: boolean) =>
      withReauth(() =>
        call(
          api.PUT("/tls/tailscale", {
            body: enabled ? { enabled: true, auth_key: key || null, control_url: controlUrl } : { enabled: false },
          }),
        ),
      ),
    onSuccess: (job) => {
      setKey("");
      onJob({ card: "tailscale", job });
    },
  });
  const busy = Boolean(running && !running.done) || run.isPending;
  const enrolling = running?.card === "tailscale" && !running.done;
  return (
    <CardFrame
      title={cs.tailscaleTitle}
      icon={<NetworkIcon className="size-5" />}
      badge={<OnOff on={ts.enabled} />}
      testId="tailscale-card"
    >
      <p className="text-sm text-soft-foreground">{cs.tailscaleIntro}</p>
      {enrolling ? (
        <p className="text-sm text-soft-foreground">{cs.working}</p>
      ) : ts.enabled && ts.hostname ? (
        <>
          <DataList>
            <DataRow
              label={cs.name}
              value={
                <a
                  href={`https://${ts.hostname}/`}
                  className="truncate underline-offset-4 hover:underline"
                  data-testid="tailscale-name"
                >
                  {ts.hostname}
                </a>
              }
            />
            <DataRow label={cs.state} value={cs.tsStates[ts.backend_state ?? "NoState"] ?? ts.backend_state} />
            <DataRow label={cs.tailnet} value={ts.tailnet ?? "—"} />
            <DataRow label={cs.tsAddress} value={ts.ipv4 ?? "—"} />
            {ts.control_url ? (
              <DataRow
                label={cs.controlUrl}
                value={<span data-testid="tailscale-control-url">{ts.control_url}</span>}
              />
            ) : null}
          </DataList>
          {!ts.https_enabled ? (
            <Alert variant="warning">
              <AlertDescription>{cs.httpsOff}</AlertDescription>
            </Alert>
          ) : null}
          {!ts.published ? (
            <Alert variant="info">
              <AlertDescription>{cs.notPublished}</AlertDescription>
            </Alert>
          ) : null}
          <p className="text-xs text-soft-foreground">{cs.hsts}</p>
          <ConfirmButton
            label={cs.turnOff}
            title={cs.tailscaleOffConfirm}
            description={cs.tailscaleOffHelp}
            onConfirm={() => run.mutate(false)}
            disabled={busy}
            testId="tailscale-off"
          />
        </>
      ) : (
        <form
          className="flex flex-col gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (!keyError && !controlError) run.mutate(true);
          }}
        >
          <p className="text-xs text-soft-foreground">{cs.tailscaleNeeds}</p>
          <FormField id="tailscale-key" label={cs.authKey} help={cs.authKeyHelp} error={keyError}>
            <PasswordInput
              id="tailscale-key"
              value={key}
              autoComplete="off"
              onChange={(e) => setKey(e.target.value.trim())}
              aria-invalid={keyError ? true : undefined}
              aria-describedby={describedBy("tailscale-key", true, keyError)}
              className="max-w-md"
            />
          </FormField>
          <FormField id="tailscale-control" label={cs.controlUrl} help={cs.controlUrlHelp} error={controlError}>
            <Input
              id="tailscale-control"
              value={controlUrl}
              autoComplete="off"
              inputMode="url"
              placeholder="https://headscale.example.org"
              onChange={(e) => setControlUrl(e.target.value.trim())}
              aria-invalid={controlError ? true : undefined}
              aria-describedby={describedBy("tailscale-control", true, controlError)}
              className="max-w-md"
              data-testid="tailscale-control-input"
            />
          </FormField>
          <p className="text-xs text-soft-foreground">{cs.restartNote}</p>
          <Button
            type="submit"
            className="self-start"
            disabled={busy || Boolean(keyError) || Boolean(controlError)}
            data-testid="tailscale-on"
          >
            {run.isPending ? <Spinner data-icon="inline-start" /> : null}
            {cs.turnOn}
          </Button>
        </form>
      )}
      {run.error && !isReauthCancelled(run.error) ? <ProblemAlert error={run.error} /> : null}
    </CardFrame>
  );
}

// ---- Wi-Fi ----

function Bars({ signal }: { signal: number }) {
  const level = signalLevel(signal);
  return (
    <span className="inline-flex items-end gap-0.5" aria-label={fmt(cs.signal, { signal })} role="img">
      {[0, 1, 2].map((i) => (
        <span
          key={i}
          className={`w-1 rounded-sm ${i < level ? "bg-foreground" : "bg-muted-background"}`}
          style={{ height: `${6 + i * 4}px` }}
        />
      ))}
    </span>
  );
}

const OTHER = "\u0000other";

function WifiForm({ wifi, busy, onJob }: { wifi: WifiState; busy: boolean; onJob: (r: Running) => void }) {
  const scan = useWifiScan();
  const [pick, setPick] = useState<string>(wifi.ssid ?? "");
  const [form, setForm] = useState<WifiForm>({
    ssid: wifi.ssid ?? "",
    psk: "",
    country: wifi.country ?? "",
    failover: wifi.ssid ? wifi.failover : true,
  });
  const [touched, setTouched] = useState(false);
  const errors = validateWifi(form);
  const show = (k: keyof typeof errors) => (touched && errors[k] ? true : undefined);
  const run = useMutation({
    mutationFn: () =>
      call(
        api.PUT("/network/wifi", {
          body: { ssid: form.ssid, psk: form.psk, country: form.country || null, failover: form.failover },
        }),
      ),
    onSuccess: (job) => {
      setForm((f) => ({ ...f, psk: "" }));
      onJob({ card: "wifi", job });
    },
  });
  const nets = scan.data ? sortNetworks(scan.data.networks) : [];
  return (
    <form
      className="flex flex-col gap-2"
      onSubmit={(e) => {
        e.preventDefault();
        setTouched(true);
        if (Object.keys(errors).length === 0) run.mutate();
      }}
    >
      <div className="flex flex-wrap items-center gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => void scan.refetch()}
          disabled={scan.isFetching}
          data-testid="wifi-scan"
        >
          {scan.isFetching ? <Spinner data-icon="inline-start" /> : <RadarIcon data-icon="inline-start" />}
          {scan.data ? cs.rescan : cs.scan}
        </Button>
        {scan.isFetching ? <span className="text-xs text-soft-foreground">{cs.scanning}</span> : null}
      </div>
      {scan.error ? <ProblemAlert error={scan.error} /> : null}
      {scan.data ? (
        <fieldset className="flex flex-col gap-1" data-testid="wifi-networks">
          <legend className="mb-1 text-sm font-medium">{cs.network}</legend>
          {nets.length === 0 ? <p className="text-sm text-soft-foreground">{cs.noNetworks}</p> : null}
          {[...nets.map((n) => n.ssid), OTHER].map((ssid) => {
            const n = nets.find((x) => x.ssid === ssid);
            const id = `wifi-net-${ssid === OTHER ? "other" : ssid}`;
            return (
              <label
                key={ssid}
                htmlFor={id}
                className="flex min-h-9 items-center gap-3 rounded-md px-2 hover:bg-muted-background"
              >
                <input
                  id={id}
                  type="radio"
                  name="wifi-network"
                  checked={pick === ssid}
                  onChange={() => {
                    setPick(ssid);
                    setForm((f) => ({ ...f, ssid: ssid === OTHER ? "" : ssid }));
                  }}
                />
                {n ? (
                  <>
                    <span className="min-w-0 flex-1 truncate">{n.ssid}</span>
                    <span className="text-xs text-soft-foreground">{n.security ? cs.secured : cs.open}</span>
                    <Bars signal={n.signal} />
                  </>
                ) : (
                  <span className="flex-1">{cs.otherNetwork}</span>
                )}
              </label>
            );
          })}
        </fieldset>
      ) : null}
      {!scan.data || pick === OTHER ? (
        <FormField id="wifi-ssid" label={cs.ssid} error={show("ssid") && cs.ssidInvalid}>
          <Input
            id="wifi-ssid"
            value={form.ssid}
            onChange={(e) => setForm({ ...form, ssid: e.target.value })}
            spellCheck={false}
            autoCapitalize="none"
            aria-invalid={show("ssid")}
            aria-describedby={describedBy("wifi-ssid", false, show("ssid") && cs.ssidInvalid)}
            className="max-w-xs"
          />
        </FormField>
      ) : null}
      <FormField id="wifi-psk" label={cs.psk} help={cs.pskHelp} error={show("psk") && cs.pskInvalid}>
        <PasswordInput
          id="wifi-psk"
          value={form.psk}
          autoComplete="off"
          onChange={(e) => setForm({ ...form, psk: e.target.value })}
          aria-invalid={show("psk")}
          aria-describedby={describedBy("wifi-psk", true, show("psk") && cs.pskInvalid)}
          className="max-w-xs"
        />
      </FormField>
      <FormField
        id="wifi-country"
        label={cs.country}
        help={cs.countryHelp}
        error={show("country") && cs.countryInvalid}
      >
        <Input
          id="wifi-country"
          value={form.country}
          maxLength={2}
          onChange={(e) => setForm({ ...form, country: e.target.value.trim().toUpperCase() })}
          spellCheck={false}
          autoCapitalize="characters"
          aria-invalid={show("country")}
          aria-describedby={describedBy("wifi-country", true, show("country") && cs.countryInvalid)}
          className="max-w-24"
        />
      </FormField>
      <Label className="flex items-start gap-3 font-normal">
        <Switch
          checked={form.failover}
          onCheckedChange={(c) => setForm({ ...form, failover: c })}
          data-testid="wifi-failover"
        />
        <span className="flex flex-col">
          <span>{cs.failover}</span>
          <span className="text-xs text-soft-foreground">{cs.failoverHelp}</span>
        </span>
      </Label>
      <p className="text-xs text-soft-foreground">{cs.restartNote}</p>
      <Button type="submit" className="self-start" disabled={busy || run.isPending} data-testid="wifi-save">
        {run.isPending ? <Spinner data-icon="inline-start" /> : <WifiIcon data-icon="inline-start" />}
        {cs.save}
      </Button>
      {run.error ? <ProblemAlert error={run.error} /> : null}
    </form>
  );
}

function WifiCard({
  network,
  running,
  onJob,
}: {
  network: Network;
  running: Running | null;
  onJob: (r: Running) => void;
}) {
  const wifi = network.wifi;
  const busy = Boolean(running && !running.done);
  const remove = useMutation({
    mutationFn: () => call(api.DELETE("/network/wifi")),
    onSuccess: (job) => onJob({ card: "wifi", job }),
  });
  return (
    <CardFrame
      title={cs.wifiTitle}
      icon={<WifiIcon className="size-5" />}
      badge={wifi?.ssid ? <OnOff on={wifi.connected} /> : undefined}
      testId="wifi-card"
    >
      <p className="text-sm text-soft-foreground">{cs.wifiIntro}</p>
      {!wifi ? (
        <p className="text-sm" data-testid="wifi-no-device">
          {cs.wifiNoDevice}
        </p>
      ) : (
        <>
          {wifi.ssid ? (
            <DataList>
              <DataRow
                label={cs.network}
                value={
                  <span className="flex items-center gap-2" data-testid="wifi-ssid">
                    {wifi.ssid}
                    <Badge variant={wifi.connected ? "success" : "warning"} size="xs">
                      {wifi.connected ? cs.connected : cs.notConnected}
                    </Badge>
                    {wifi.signal !== null ? <Bars signal={wifi.signal} /> : null}
                  </span>
                }
              />
              <DataRow label={cs.address} value={wifi.address ?? "—"} />
              <DataRow
                label={cs.route}
                value={<span data-testid="wifi-route">{wifi.default_route ? cs.routeWifi : cs.routeEthernet}</span>}
              />
            </DataList>
          ) : null}
          <WifiForm key={wifi.ssid ?? ""} wifi={wifi} busy={busy} onJob={onJob} />
          {wifi.profiles.filter((p) => !p.managed).length > 0 ? (
            <section className="flex flex-col gap-1 text-sm">
              <h4 className="font-medium">{cs.presets}</h4>
              <p className="text-xs text-soft-foreground">{cs.presetHelp}</p>
              <ul className="list-disc pl-4" data-testid="wifi-presets">
                {wifi.profiles
                  .filter((p) => !p.managed)
                  .map((p) => (
                    <li key={p.name}>
                      {p.ssid || p.name} ({p.name})
                    </li>
                  ))}
              </ul>
            </section>
          ) : null}
          {network.dhcp_hints.length > 0 ? (
            <section className="flex flex-col gap-1 text-sm" data-testid="dhcp-hints">
              <p className="text-xs text-soft-foreground">{cs.reserve}</p>
              <ul className="pl-4 font-mono text-xs">
                {network.dhcp_hints.map((h) => (
                  <li key={h.interface}>
                    {fmt(cs.reserveRow, { interface: h.interface, mac: h.mac, address: h.address })}
                  </li>
                ))}
              </ul>
            </section>
          ) : null}
          {wifi.ssid ? (
            <ConfirmButton
              label={cs.remove}
              title={cs.removeConfirm}
              description={fmt(cs.removeHelp, { ssid: wifi.ssid })}
              onConfirm={() => remove.mutate()}
              disabled={busy || remove.isPending}
              testId="wifi-remove"
            />
          ) : null}
          {remove.error ? <ProblemAlert error={remove.error} /> : null}
        </>
      )}
    </CardFrame>
  );
}

// ---- the tab ----

export function ConnectivityTab({ hostname }: { hostname: string }) {
  const qc = useQueryClient();
  const [running, setRunning] = useState<Running | null>(null);
  // While a job runs, read GET /tls each 2 s: the login URL of Tailscale and the certificate state come there.
  const tls = useTls(true, running ? 2000 : false);
  const network = useNetwork();
  const done = () => {
    void qc.invalidateQueries({ queryKey: qk.tls });
    void qc.invalidateQueries({ queryKey: qk.network });
    void qc.invalidateQueries({ queryKey: qk.status });
    void qc.invalidateQueries({ queryKey: qk.config("effective") });
  };
  if (tls.isError) return <ProblemAlert error={tls.error} />;
  if (!tls.data || !network.data) {
    return (
      <div className="flex flex-col gap-4" aria-busy="true">
        {[0, 1, 2].map((i) => (
          <Skeleton key={i} className="h-48" />
        ))}
      </div>
    );
  }
  return (
    <div className="flex flex-col gap-4" data-testid="connectivity">
      <p className="text-sm text-soft-foreground">{cs.intro}</p>
      {running ? (
        <JobProgress
          jobId={running.job.id}
          initial={running.job}
          onDone={() => {
            setRunning((r) => (r ? { ...r, done: true } : r));
            done();
          }}
          onStepChange={() => void qc.invalidateQueries({ queryKey: qk.tls })}
        >
          <Button type="button" variant="outline" className="self-start" onClick={() => setRunning(null)}>
            {strings.common.close}
          </Button>
        </JobProgress>
      ) : null}
      {running?.card === "tailscale" && !running.done && tls.data.tailscale.login_url ? (
        <LoginBox url={tls.data.tailscale.login_url} />
      ) : null}
      <PublicCard tls={tls.data} running={running} onJob={setRunning} hostname={hostname} />
      <TailscaleCard tls={tls.data} running={running} onJob={setRunning} />
      <WifiCard network={network.data} running={running} onJob={setRunning} />
    </div>
  );
}
