import { ArrowRight, RotateCcw, SkipForward, Trash2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { ResultPanel } from "@/components/onboarding/result-panel";
import { StepForm } from "@/components/onboarding/step-forms";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Spinner } from "@/components/ui/spinner";
import { useTranslation } from "@/hooks/use-translation";
import { useErrorText } from "@/hooks/use-error-text";
import { onboardingApi, type Preview } from "@/lib/api";
import type { StepResult, StepStatus, WizardAction } from "@/lib/wizard";

export function StepScreen({
  step,
  facility,
  busy,
  result,
  dispatch,
}: {
  step: StepStatus;
  facility: string | null;
  busy: boolean;
  result: StepResult | undefined;
  dispatch: (action: WizardAction) => void;
}) {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [preview, setPreview] = useState<Preview | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [showForm, setShowForm] = useState(!result);
  const facilityRef = useRef(facility);
  facilityRef.current = facility;

  // One GET for each screen: the preview fills the form. No polling.
  useEffect(() => {
    const controller = new AbortController();
    setPreview(null);
    setLoadError(null);
    onboardingApi
      .preview(
        step.step,
        step.scope === "facility" ? facilityRef.current : null,
        controller.signal,
      )
      .then(setPreview)
      .catch((error) => {
        if (!controller.signal.aborted) setLoadError(errorText(error));
      });
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [step.step]);

  const run = async (call: () => Promise<StepResult>) => {
    dispatch({ type: "request" });
    try {
      const outcome = await call();
      dispatch({ type: "result", result: outcome });
      setShowForm(outcome.status === "failed");
    } catch (error) {
      dispatch({ type: "request_failed", error: errorText(error) });
    }
  };
  const apply = (body: Record<string, unknown>) =>
    run(() => onboardingApi.apply(step.step, body));
  const skip = () => run(() => onboardingApi.skip(step.step, facility));
  const removeDemo = () =>
    run(async () => ({
      ...(await onboardingApi.removeDemo(facility ?? "")),
      step: "demo_data",
    }));

  const done = step.state === "done" || step.state === "skipped";
  return (
    <Card data-testid={`screen-${step.step}`}>
      <CardHeader>
        <div className="flex flex-wrap items-center gap-2">
          <CardTitle className="text-lg">
            <h2>{t(`step_${step.step}`)}</h2>
          </CardTitle>
          <Badge
            variant={
              step.state === "done"
                ? "success"
                : step.state === "failed"
                  ? "destructive"
                  : "neutral"
            }
            data-testid="step-state"
          >
            {t(`state_${step.state}`)}
          </Badge>
        </div>
        <CardDescription>{t(`step_${step.step}_help`)}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        {result && <ResultPanel result={result} />}
        {!result && done && step.detail && (
          <Alert>
            <AlertTitle>{t("done_before")}</AlertTitle>
            <AlertDescription>{step.detail}</AlertDescription>
          </Alert>
        )}
        {loadError && (
          <Alert variant="destructive">
            <AlertDescription>{loadError}</AlertDescription>
          </Alert>
        )}
        {showForm &&
          (preview ? (
            <StepForm
              step={step.step}
              preview={preview}
              facility={facility}
              busy={busy}
              onApply={apply}
            />
          ) : (
            !loadError && (
              <p
                className="text-muted-foreground flex items-center gap-2 text-sm"
                role="status"
              >
                <Spinner aria-hidden="true" />
                {t("loading_step")}
              </p>
            )
          ))}
      </CardContent>
      <CardFooter className="flex flex-wrap gap-3">
        {(done ||
          result?.status === "done" ||
          result?.status === "skipped") && (
          <Button
            type="button"
            onClick={() => dispatch({ type: "next" })}
            disabled={busy}
            data-testid="continue"
          >
            {t("continue")}
            <ArrowRight aria-hidden="true" />
          </Button>
        )}
        {!showForm && (
          <Button
            type="button"
            variant="outline"
            onClick={() => setShowForm(true)}
            disabled={busy}
          >
            <RotateCcw aria-hidden="true" />
            {t("apply_again")}
          </Button>
        )}
        {step.optional && step.state !== "done" && (
          <Button
            type="button"
            variant="ghost"
            onClick={skip}
            disabled={busy}
            data-testid="skip"
          >
            <SkipForward aria-hidden="true" />
            {t("skip")}
          </Button>
        )}
        {step.step === "demo_data" && step.state === "done" && (
          <Button
            type="button"
            variant="destructive"
            onClick={removeDemo}
            disabled={busy}
            data-testid="remove-demo"
          >
            <Trash2 aria-hidden="true" />
            {t("demo_remove")}
          </Button>
        )}
      </CardFooter>
    </Card>
  );
}
