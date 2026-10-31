import { useQueryClient } from "@tanstack/react-query";
import { RotateCwIcon } from "lucide-react";
import { asProblem } from "@/api/problem";
import { qk } from "@/api/queries";
import { ProblemAlert } from "@/components/app/problem-alert";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";
import { stepActionText, strings } from "@/lib/strings";
import { planEffect, planTitle } from "./plug-text";
import type { PlugChangeFlow } from "./use-plug-change";

const p = strings.plugs;

// The plan preview: what the change does to Care, the downloads, the restarts, the steps, and the warnings.
export function PlanDialog({ flow, label }: { flow: PlugChangeFlow; label: string }) {
  const qc = useQueryClient();
  const preview = flow.preview;
  const plan = preview?.plan;
  const conflict = asProblem(flow.apply.error)?.is("config-changed", "precondition-required");
  return (
    <Dialog open={preview !== null} onOpenChange={(open) => (open ? null : flow.cancel())}>
      <DialogContent data-testid="plug-plan">
        <DialogHeader>
          <DialogTitle>{flow.request ? planTitle(flow.request, label) : label}</DialogTitle>
          <DialogDescription data-testid="plug-plan-effect" data-path={plan?.path}>
            {plan ? planEffect(plan) : ""}
          </DialogDescription>
        </DialogHeader>
        {plan ? (
          <div className="flex max-h-[50dvh] flex-col gap-3 overflow-y-auto text-sm">
            {plan.needs_internet ? <p>{p.needsInternet}</p> : null}
            {plan.restarts.length > 0 ? (
              <p>
                <span className="text-soft-foreground">{p.restarts}: </span>
                {plan.restarts.map((u) => u.replace(/\.service$/, "")).join(", ")}
              </p>
            ) : null}
            {plan.downloads.length > 0 ? (
              <div>
                <p className="text-soft-foreground">{p.downloads}</p>
                <ul className="list-disc pl-4 text-xs break-all">
                  {plan.downloads.map((d) => (
                    <li key={d}>{d.split("/").pop()}</li>
                  ))}
                </ul>
              </div>
            ) : null}
            {plan.builds.length > 0 ? (
              <div>
                <p className="text-soft-foreground">{p.builds}</p>
                <ul className="list-disc pl-4 text-xs break-all">
                  {plan.builds.map((b) => (
                    <li key={b}>{b}</li>
                  ))}
                </ul>
              </div>
            ) : null}
            {plan.steps.length > 0 ? (
              <div>
                <p className="text-soft-foreground">{p.steps}</p>
                <ol className="list-decimal pl-5 text-xs" data-testid="plug-plan-steps">
                  {plan.steps.map((s, i) => (
                    <li key={`${s}-${i}`} title={s}>
                      {stepActionText(s)}
                    </li>
                  ))}
                </ol>
              </div>
            ) : null}
            {plan.warnings.length > 0 ? (
              <Alert variant="warning">
                <AlertDescription>
                  <p className="font-semibold">{p.warnings}</p>
                  <ul className="list-disc pl-4">
                    {plan.warnings.map((w) => (
                      <li key={w}>{w}</li>
                    ))}
                  </ul>
                </AlertDescription>
              </Alert>
            ) : null}
          </div>
        ) : null}
        {conflict ? (
          <div className="flex flex-col items-start gap-2">
            <ProblemAlert error={flow.apply.error} title={p.configChanged} />
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => {
                flow.cancel();
                void qc.invalidateQueries({ queryKey: qk.plugs });
              }}
            >
              <RotateCwIcon data-icon="inline-start" />
              {strings.common.refresh}
            </Button>
          </div>
        ) : flow.apply.error ? (
          <ProblemAlert error={flow.apply.error} />
        ) : null}
        <DialogFooter>
          <Button type="button" variant="outline" onClick={flow.cancel}>
            {strings.common.cancel}
          </Button>
          <Button
            type="button"
            onClick={() => flow.apply.mutate()}
            disabled={flow.apply.isPending || !plan?.changed}
            data-testid="plug-apply"
          >
            {flow.apply.isPending ? <Spinner data-icon="inline-start" /> : null}
            {p.apply}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
