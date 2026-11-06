import { InfoIcon } from "lucide-react";
import { useId } from "react";
import { useNetwork } from "@/api/queries";
import { FormField } from "@/components/app/form-field";
import { describedBy } from "@/lib/form";
import { Input } from "@/components/ui/input";
import { CopyButton } from "@/components/v2/copy-button";
import { InlineSkeleton } from "@/components/v2/inline-skeleton";
import { ProblemCard } from "@/components/v2/problem-card";
import { fmt, strings } from "@/lib/strings";
import { HOSTNAME_RE } from "@/lib/schema";
import type { IdentityValue } from "./identity-logic";

// The address of this device, big (S66): staff type it in the browser. The IP addresses, a collision, and a name
// change are under it.
function NetworkPanel({ hostname }: { hostname: string }) {
  const network = useNetwork();
  const n = network.data;
  const i = strings.identity;
  const changes = n && HOSTNAME_RE.test(hostname) && hostname !== n.hostname;
  const name = n ? (n.mdns.advertised ?? n.mdns.configured) : null;
  return (
    <section
      className="flex flex-col gap-3 rounded-card border border-line-soft bg-surface-tint p-4 sm:p-5"
      aria-live="polite"
    >
      <h3 className="type-small font-medium text-ink-muted">{i.addressTitle}</h3>
      <p className="flex min-h-10 min-w-0 items-center gap-2" data-testid="advertised-name">
        {name ? (
          <>
            <span className="min-w-0 truncate font-mono text-2xl leading-10 font-semibold tracking-tight text-ink">
              https://{name}
            </span>
            <span className="sr-only">{fmt(i.advertised, { name })}</span>
            <CopyButton text={`https://${name}`} iconOnly size="xs" variant="ghost" />
          </>
        ) : (
          <InlineSkeleton className="h-7 w-64" />
        )}
      </p>
      <p className="type-small text-ink-muted">{i.addressHelp}</p>
      <p className="flex min-h-6 flex-wrap items-center gap-x-2 type-small" data-testid="network-ips">
        <span className="text-ink-soft">{i.ips}:</span>
        {n ? (
          <span className="type-data text-ink">{n.addresses.join(", ")}</span>
        ) : (
          <InlineSkeleton className="h-4 w-32" />
        )}
      </p>
      {n?.mdns.collision ? (
        <ProblemCard
          tone="warn"
          title={fmt(i.collision, {
            configured: n.mdns.configured,
            advertised: n.mdns.advertised ?? n.mdns.configured,
          })}
        />
      ) : null}
      {changes ? (
        <p className="flex items-start gap-2 rounded-input border border-info-border bg-info-bg p-3 type-small text-info-fg">
          <InfoIcon className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
          {fmt(i.urlChanges, { name: `${hostname}.local` })}
        </p>
      ) : null}
      {n?.dhcp_hint ? (
        <p className="type-small text-ink-soft">
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
            <span className="type-data text-ink-soft">.local</span>
          </div>
        </FormField>
      ) : (
        <p className="type-small text-ink-soft">{i.easyHostname}</p>
      )}
      <NetworkPanel hostname={value.hostname} />
    </div>
  );
}
