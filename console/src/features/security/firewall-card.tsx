import { useMutation, useQueryClient } from "@tanstack/react-query";
import { PlusIcon, ShieldIcon, XIcon } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { api, call } from "@/api/client";
import { qk, useConfig, useFirewall } from "@/api/queries";
import type { Config, FirewallState } from "@/api/types";
import { FormField } from "@/components/app/form-field";
import { DataList, DataRow } from "@/components/app/page";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import { ConfigChange } from "@/features/settings/config-change";
import { formatDateTime } from "@/lib/format";
import { describedBy } from "@/lib/form";
import { fmt, strings } from "@/lib/strings";
import {
  COMMENT_MAX,
  allowPortKey,
  checkDraft,
  protoOf,
  secondsLeft,
  type AllowDraft,
  type AllowPort,
  type DraftErrors,
} from "./firewall-logic";

const fc = strings.security.firewall;
const al = fc.allow;

function useNow(active: boolean) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [active]);
  return now;
}

function PendingAlert({ fw }: { fw: FirewallState }) {
  const qc = useQueryClient();
  const now = useNow(true);
  const left = secondsLeft(fw, now) ?? 0;
  const confirm = useMutation({
    mutationFn: () => call(api.POST("/security/firewall/confirm")),
    onSuccess: (st) => qc.setQueryData(qk.firewall, st),
  });
  return (
    <Alert variant="warning" data-testid="firewall-pending">
      <AlertTitle>{fc.pendingTitle}</AlertTitle>
      <AlertDescription className="flex flex-col gap-2">
        <span>{fmt(fc.pending, { seconds: left })}</span>
        <span className="flex flex-wrap gap-2">
          <Button
            type="button"
            size="sm"
            onClick={() => confirm.mutate()}
            disabled={confirm.isPending}
            data-testid="firewall-keep"
          >
            {confirm.isPending ? <Spinner data-icon="inline-start" /> : null}
            {fc.keep}
          </Button>
        </span>
        {confirm.error ? <ProblemAlert error={confirm.error} /> : null}
      </AlertDescription>
    </Alert>
  );
}

function PolicyForm({ config }: { config: Config }) {
  const id = useId();
  const qc = useQueryClient();
  const [policy, setPolicy] = useState<"accept" | "drop">(config.security?.firewall?.policy ?? "accept");
  const [ssh, setSsh] = useState<boolean>(config.security?.ssh?.allow ?? true);
  const [ports, setPorts] = useState<AllowPort[]>(config.security?.firewall?.allow_ports ?? []);
  return (
    <ConfigChange
      edit={(file) => ({
        ...file,
        security: {
          ...file.security,
          firewall: { ...file.security?.firewall, policy, allow_ports: ports.length ? ports : undefined },
          ssh: { ...file.security?.ssh, allow: ssh },
        },
      })}
      valid
      onApplied={() => void qc.invalidateQueries({ queryKey: qk.firewall })}
    >
      <div className="flex flex-col gap-6">
        <fieldset className="flex flex-col gap-3">
          <legend className="mb-2 font-semibold">{fc.policy}</legend>
          <RadioGroup name="firewall-policy" value={policy} onValueChange={(v) => setPolicy(v as "accept" | "drop")}>
            {(["accept", "drop"] as const).map((p) => (
              <Label key={p} htmlFor={`${id}-${p}`} className="flex items-start gap-3 font-normal">
                <RadioGroupItem id={`${id}-${p}`} value={p} data-testid={`firewall-policy-${p}`} />
                <span className="flex flex-col">
                  <span>{fc.policies[p]}</span>
                  <span className="text-xs text-soft-foreground">{fc.policyHelp[p]}</span>
                </span>
              </Label>
            ))}
          </RadioGroup>
          <Label className="flex items-start gap-3 font-normal">
            <Switch checked={ssh} onCheckedChange={setSsh} data-testid="firewall-ssh" />
            <span className="flex flex-col">
              <span>{fc.ssh}</span>
              <span className="text-xs text-soft-foreground">{fc.sshHelp}</span>
            </span>
          </Label>
        </fieldset>
        <AllowPorts
          ports={ports}
          setPorts={setPorts}
          ctx={{ ssh, tailscale: config.tls?.tailscale?.enabled ?? false }}
        />
      </div>
    </ConfigChange>
  );
}

function entryText(a: AllowPort) {
  return fmt(al.entry, { port: a.port, proto: protoOf(a), iface: a.iface ?? al.anyIface });
}

function draftError(e: DraftErrors, key: "port" | "iface" | "comment", d: AllowDraft) {
  const k = e[key];
  if (!k) return null;
  if (k === "builtin")
    return fmt(al.errors.builtin ?? "", {
      port: d.port.trim(),
      proto: d.proto,
      reason: al.builtin[e.builtin ?? "care"] ?? "",
    });
  return al.errors[k] ?? null;
}

const EMPTY_DRAFT: AllowDraft = { port: "", proto: "tcp", iface: "", comment: "" };

// S64: security.firewall.allow_ports. The operator edits the list (add, remove); the form of the policy previews and
// applies it with the policy as one config change (plan action reload-firewall).
function AllowPorts({
  ports,
  setPorts,
  ctx,
}: {
  ports: AllowPort[];
  setPorts: (p: AllowPort[]) => void;
  ctx: { ssh: boolean; tailscale: boolean };
}) {
  const id = useId();
  const [draft, setDraft] = useState<AllowDraft>(EMPTY_DRAFT);
  const [errors, setErrors] = useState<DraftErrors>({});
  const add = () => {
    const r = checkDraft(draft, ports, ctx);
    if (r.errors) {
      setErrors(r.errors);
      return;
    }
    setPorts([...ports, r.entry]);
    setDraft(EMPTY_DRAFT);
    setErrors({});
  };
  const set = (patch: Partial<AllowDraft>) => {
    setDraft({ ...draft, ...patch });
    setErrors({});
  };
  return (
    <fieldset className="flex flex-col gap-3" data-testid="allow-ports">
      <legend className="mb-2 font-semibold">{al.title}</legend>
      <p className="text-sm text-soft-foreground">{al.help}</p>
      {ports.length === 0 ? (
        <p className="text-sm" data-testid="allow-ports-none">
          {al.none}
        </p>
      ) : (
        <ul className="flex flex-col divide-y rounded-lg border text-sm" data-testid="allow-ports-list">
          {ports.map((a, i) => (
            <li key={allowPortKey(a)} className="flex items-center justify-between gap-3 px-3 py-2">
              <span className="flex min-w-0 flex-col">
                <span className="font-mono">{entryText(a)}</span>
                {a.comment ? <span className="truncate text-xs text-soft-foreground">{a.comment}</span> : null}
              </span>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label={fmt(al.remove, { entry: entryText(a) })}
                onClick={() => setPorts(ports.filter((_, j) => j !== i))}
                data-testid="allow-port-remove"
              >
                <XIcon />
              </Button>
            </li>
          ))}
        </ul>
      )}
      <div className="grid gap-x-3 sm:grid-cols-[8rem_7rem_1fr]">
        <FormField id={`${id}-port`} label={al.port} error={draftError(errors, "port", draft)}>
          <Input
            id={`${id}-port`}
            inputMode="numeric"
            value={draft.port}
            onChange={(e) => set({ port: e.target.value })}
            aria-invalid={errors.port ? true : undefined}
            aria-describedby={describedBy(`${id}-port`, false, errors.port)}
            data-testid="allow-port-port"
          />
        </FormField>
        <FormField id={`${id}-proto`} label={al.proto}>
          <NativeSelect
            id={`${id}-proto`}
            value={draft.proto}
            onChange={(e) => set({ proto: e.target.value as "tcp" | "udp" })}
            className="w-full"
            data-testid="allow-port-proto"
          >
            <NativeSelectOption value="tcp">TCP</NativeSelectOption>
            <NativeSelectOption value="udp">UDP</NativeSelectOption>
          </NativeSelect>
        </FormField>
        <FormField id={`${id}-iface`} label={al.iface} help={al.ifaceHelp} error={draftError(errors, "iface", draft)}>
          <Input
            id={`${id}-iface`}
            value={draft.iface}
            list={`${id}-ifaces`}
            spellCheck={false}
            autoCapitalize="none"
            maxLength={15}
            onChange={(e) => set({ iface: e.target.value })}
            aria-invalid={errors.iface ? true : undefined}
            aria-describedby={describedBy(`${id}-iface`, true, errors.iface)}
            data-testid="allow-port-iface"
          />
          <datalist id={`${id}-ifaces`}>
            <option value="eth0" />
            <option value="wlan0" />
            <option value="tailscale0" />
          </datalist>
        </FormField>
      </div>
      <FormField
        id={`${id}-comment`}
        label={al.comment}
        help={al.commentHelp}
        error={draftError(errors, "comment", draft)}
      >
        <Input
          id={`${id}-comment`}
          value={draft.comment}
          maxLength={COMMENT_MAX}
          onChange={(e) => set({ comment: e.target.value })}
          aria-invalid={errors.comment ? true : undefined}
          aria-describedby={describedBy(`${id}-comment`, true, errors.comment)}
          data-testid="allow-port-comment"
        />
      </FormField>
      {errors.list ? (
        <p className="text-sm text-destructive" role="alert">
          {al.errors.full}
        </p>
      ) : null}
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="self-start"
        onClick={add}
        data-testid="allow-port-add"
      >
        <PlusIcon data-icon="inline-start" />
        {al.add}
      </Button>
    </fieldset>
  );
}

export function FirewallCard() {
  const fw = useFirewall();
  const effective = useConfig("effective");
  const s = fw.data;
  return (
    <Card size="sm" data-testid="firewall-card">
      <CardHeader>
        <CardTitle>
          <h2 className="flex items-center gap-2">
            <ShieldIcon className="size-4" aria-hidden="true" />
            {fc.title}
          </h2>
        </CardTitle>
        <CardDescription>{fc.intro}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {fw.error ? <ProblemAlert error={fw.error} /> : null}
        <DataList>
          <DataRow
            label={fc.loaded}
            loading={!s}
            value={
              s ? (
                <Badge variant={s.live_policy === "drop" ? "success" : "info"} size="sm" data-testid="firewall-live">
                  {s.live_policy ? fc.policies[s.live_policy] : fc.notLoaded}
                </Badge>
              ) : (
                ""
              )
            }
          />
          {s && !s.unit_active ? <DataRow label={fc.unit} value={fc.unitInactive} /> : null}
          {s?.rolled_back_at ? (
            <DataRow
              label={fc.rolledBack}
              value={fmt(fc.rolledBackAt, {
                time: formatDateTime(s.rolled_back_at),
                reason: fc.reasons[s.rollback_reason ?? "manual"] ?? "",
              })}
            />
          ) : null}
        </DataList>
        {s?.pending_until ? <PendingAlert fw={s} /> : null}
        {effective.data ? (
          <PolicyForm key={effective.data.etag} config={effective.data.config} />
        ) : (
          <Skeleton className="h-24" />
        )}
      </CardContent>
    </Card>
  );
}
