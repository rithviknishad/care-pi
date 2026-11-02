import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ShieldIcon } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { api, call } from "@/api/client";
import { qk, useConfig, useFirewall } from "@/api/queries";
import type { Config, FirewallState } from "@/api/types";
import { DataList, DataRow } from "@/components/app/page";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import { ConfigChange } from "@/features/settings/config-change";
import { formatDateTime } from "@/lib/format";
import { fmt, strings } from "@/lib/strings";
import { secondsLeft } from "./firewall-logic";

const fc = strings.security.firewall;

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
  return (
    <ConfigChange
      edit={(file) => ({
        ...file,
        security: {
          ...file.security,
          firewall: { ...file.security?.firewall, policy },
          ssh: { ...file.security?.ssh, allow: ssh },
        },
      })}
      valid
      onApplied={() => void qc.invalidateQueries({ queryKey: qk.firewall })}
    >
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
    </ConfigChange>
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
