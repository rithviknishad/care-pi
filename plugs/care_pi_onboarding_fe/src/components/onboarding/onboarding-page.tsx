import { navigate } from "raviger";
import { useReducer, useEffect } from "react";

import PluginComponent from "@/components/common/plugin-component";
import { ProgressRail } from "@/components/onboarding/progress-rail";
import { StepScreen } from "@/components/onboarding/step-screen";
import { useErrorText } from "@/hooks/use-error-text";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
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
import { onboardingApi } from "@/lib/api";
import {
  initialState,
  type StepStatus,
  visibleSteps,
  wizardReducer,
} from "@/lib/wizard";

function DoneScreen({
  steps,
  facility,
  facilityName,
  busy,
  onOpen,
}: {
  steps: StepStatus[];
  facility: string | null;
  facilityName: string | null;
  busy: boolean;
  onOpen: () => void;
}) {
  const { t } = useTranslation();
  const open = steps.filter(
    (s) => s.allowed && (s.state === "pending" || s.state === "failed"),
  );
  return (
    <Card data-testid="screen-done">
      <CardHeader>
        <CardTitle className="text-lg">
          <h2>{open.length ? t("done_title_open") : t("done_title")}</h2>
        </CardTitle>
        <CardDescription>
          {facilityName
            ? t("done_facility", { name: facilityName })
            : t("done_no_facility")}
        </CardDescription>
      </CardHeader>
      <CardContent>
        {open.length > 0 && (
          <Alert variant="warning">
            <AlertTitle>{t("done_open_steps")}</AlertTitle>
            <AlertDescription>
              {open.map((s) => t(`step_${s.step}`)).join(", ")}
            </AlertDescription>
          </Alert>
        )}
      </CardContent>
      <CardFooter>
        <Button
          type="button"
          onClick={onOpen}
          disabled={busy || !facility}
          data-testid="open-care"
        >
          {t("open_care")}
        </Button>
      </CardFooter>
    </Card>
  );
}

export default function OnboardingPage() {
  const { t } = useTranslation();
  const errorText = useErrorText();
  const [state, dispatch] = useReducer(wizardReducer, initialState);

  // One GET when the page opens. Each step result updates the state locally; nothing polls.
  useEffect(() => {
    const controller = new AbortController();
    onboardingApi
      .status(null, controller.signal)
      .then((status) => dispatch({ type: "status_loaded", status }))
      .catch((error) => {
        if (!controller.signal.aborted)
          dispatch({ type: "status_failed", error: errorText(error) });
      });
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const steps = visibleSteps(state.status);
  const current = steps.find((s) => s.step === state.screen);

  const openCare = async () => {
    dispatch({ type: "request" });
    try {
      await onboardingApi.closeWizard("finished");
    } catch {
      // Closing only stops the redirect after sign-in. CARE opens anyway.
    }
    navigate(`/facility/${state.facility}/overview`);
  };

  return (
    <PluginComponent>
      <main className="mx-auto flex w-full max-w-6xl flex-col gap-6 p-4 md:p-6">
        <header className="flex flex-col gap-1">
          <h1 className="text-2xl font-semibold">{t("title")}</h1>
          <p className="text-muted-foreground text-sm">{t("subtitle")}</p>
        </header>
        {state.phase === "loading" && (
          <p
            className="text-muted-foreground flex items-center gap-2 text-sm"
            role="status"
          >
            <Spinner aria-hidden="true" />
            {t("loading")}
          </p>
        )}
        {state.phase === "error" && (
          <Alert variant="destructive" data-testid="page-error">
            <AlertTitle>{t("error_title")}</AlertTitle>
            <AlertDescription>{state.error}</AlertDescription>
          </Alert>
        )}
        {state.phase === "denied" && (
          <Alert data-testid="page-denied">
            <AlertTitle>{t("denied_title")}</AlertTitle>
            <AlertDescription>{t("denied_body")}</AlertDescription>
          </Alert>
        )}
        {state.phase === "ready" && state.status && (
          <div className="grid gap-6 md:grid-cols-[16rem_1fr]">
            <aside>
              <ProgressRail
                steps={steps}
                screen={state.screen}
                busy={state.busy}
                onGo={(screen) => dispatch({ type: "go", screen })}
              />
            </aside>
            <section className="flex min-w-0 flex-col gap-4" aria-live="polite">
              {state.error && (
                <Alert variant="destructive" data-testid="request-error">
                  <AlertDescription>{state.error}</AlertDescription>
                </Alert>
              )}
              {current && (
                <StepScreen
                  key={current.step}
                  step={current}
                  facility={state.facility}
                  busy={state.busy}
                  result={state.results[current.step]}
                  dispatch={dispatch}
                />
              )}
              {state.screen === "done" && (
                <DoneScreen
                  steps={steps}
                  facility={state.facility}
                  facilityName={state.status.facility?.name ?? null}
                  busy={state.busy}
                  onOpen={openCare}
                />
              )}
            </section>
          </div>
        )}
      </main>
    </PluginComponent>
  );
}
