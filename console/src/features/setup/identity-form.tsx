import { AlertTriangleIcon, InfoIcon } from "lucide-react";
import { useId } from "react";
import { useNetwork } from "@/api/queries";
import { FormField } from "@/components/app/form-field";
import { describedBy } from "@/lib/form";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { fmt, strings } from "@/lib/strings";
import { HOSTNAME_RE } from "@/lib/schema";
import type { IdentityValue } from "./identity-logic";

// The name that this device announces now, the IP addresses, and the notes on a collision and a name change.
function NetworkPanel({ hostname }: { hostname: string }) {
  const network = useNetwork();
  const n = network.data;
  const i = strings.identity;
  const changes = n && HOSTNAME_RE.test(hostname) && hostname !== n.hostname;
  return (
    <section className="flex flex-col gap-2 rounded-lg border bg-soft-background p-4 text-sm" aria-live="polite">
      <h3 className="font-semibold">{i.address}</h3>
      <p className="flex min-h-6 items-center" data-testid="advertised-name">
        {n ? fmt(i.advertised, { name: n.mdns.advertised ?? n.mdns.configured }) : <Skeleton className="h-4 w-64" />}
      </p>
      <p className="flex min-h-6 flex-wrap items-center gap-x-2">
        <span className="text-soft-foreground">{i.ips}:</span>
        {n ? n.addresses.join(", ") : <Skeleton className="h-4 w-32" />}
      </p>
      {n?.mdns.collision ? (
        <Alert variant="warning">
          <AlertTriangleIcon />
          <AlertDescription>
            {fmt(i.collision, { configured: n.mdns.configured, advertised: n.mdns.advertised ?? n.mdns.configured })}
          </AlertDescription>
        </Alert>
      ) : null}
      {changes ? (
        <Alert variant="info">
          <InfoIcon />
          <AlertDescription>{fmt(i.urlChanges, { name: `${hostname}.local` })}</AlertDescription>
        </Alert>
      ) : null}
      {n?.dhcp_hint ? (
        <p className="text-xs text-soft-foreground">
          {fmt(i.dhcp, { address: n.dhcp_hint.address, mac: n.dhcp_hint.mac })}
        </p>
      ) : null}
    </section>
  );
}

export function IdentityForm({
  value,
  onChange,
  editableHostname,
  errors,
}: {
  value: IdentityValue;
  onChange: (v: IdentityValue) => void;
  editableHostname: boolean;
  errors: Partial<Record<keyof IdentityValue, string>>;
}) {
  const id = useId();
  const i = strings.identity;
  return (
    <div className="flex flex-col gap-4">
      <FormField id={`${id}-name`} label={i.displayName} help={i.displayNameHelp} error={errors.display_name}>
        <Input
          id={`${id}-name`}
          name="display-name"
          value={value.display_name}
          maxLength={80}
          autoComplete="organization"
          onChange={(e) => onChange({ ...value, display_name: e.target.value })}
          aria-invalid={errors.display_name ? true : undefined}
          aria-describedby={describedBy(`${id}-name`, true, errors.display_name)}
        />
      </FormField>
      {editableHostname ? (
        <FormField id={`${id}-host`} label={i.hostname} help={i.hostnameHelp} error={errors.hostname}>
          <div className="flex items-center gap-2">
            <Input
              id={`${id}-host`}
              name="hostname"
              value={value.hostname}
              maxLength={63}
              autoCapitalize="none"
              spellCheck={false}
              onChange={(e) => onChange({ ...value, hostname: e.target.value.toLowerCase() })}
              aria-invalid={errors.hostname ? true : undefined}
              aria-describedby={describedBy(`${id}-host`, true, errors.hostname)}
              className="max-w-xs"
            />
            <span className="text-soft-foreground">.local</span>
          </div>
        </FormField>
      ) : (
        <p className="text-xs text-soft-foreground">{i.easyHostname}</p>
      )}
      <NetworkPanel hostname={value.hostname} />
    </div>
  );
}
