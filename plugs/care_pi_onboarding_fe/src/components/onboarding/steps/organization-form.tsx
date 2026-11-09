import { useMemo, useState } from "react";

import {
  ApplyButton,
  CheckRow,
  SelectField,
  type StepFormProps,
  TextField,
} from "@/components/onboarding/form-parts";
import { useTranslation } from "@/hooks/use-translation";

interface StateOption {
  name: string;
  kind: string;
  districts: string[];
}

export function OrganizationForm({ preview, busy, onApply }: StepFormProps) {
  const { t } = useTranslation();
  const states: StateOption[] = useMemo(
    () => preview.options?.states ?? [],
    [preview],
  );
  const existingStates: string[] = useMemo(
    () => preview.existing?.states ?? [],
    [preview],
  );
  const existingDistricts: Record<string, string[]> = useMemo(
    () => preview.existing?.districts ?? {},
    [preview],
  );
  const [state, setState] = useState<string>(preview.defaults?.state ?? "");
  const [district, setDistrict] = useState<string>(
    preview.defaults?.district ?? "",
  );
  const [localBody, setLocalBody] = useState<string>(
    preview.defaults?.local_body ?? "",
  );
  const [allDistricts, setAllDistricts] = useState(false);

  const chosen = states.find((s) => s.name === state);
  const plan = useMemo(() => {
    if (!chosen) return 0;
    const have = new Set(
      (existingDistricts[chosen.name] ?? []).map((d) => d.toLowerCase()),
    );
    let count = existingStates.some(
      (s) => s.toLowerCase() === chosen.name.toLowerCase(),
    )
      ? 0
      : 1;
    const names = allDistricts ? chosen.districts : district ? [district] : [];
    count += names.filter((d) => !have.has(d.toLowerCase())).length;
    if (localBody.trim()) count += 1;
    return count;
  }, [
    chosen,
    district,
    allDistricts,
    localBody,
    existingStates,
    existingDistricts,
  ]);

  return (
    <form
      className="flex flex-col gap-5"
      onSubmit={(e) => {
        e.preventDefault();
        onApply({
          state,
          district,
          local_body: localBody.trim(),
          all_districts: allDistricts,
        });
      }}
    >
      <div className="grid gap-4 md:grid-cols-2">
        <SelectField
          label={t("field_state")}
          value={state}
          testId="field-state"
          placeholder={t("choose")}
          onChange={(value) => {
            setState(value);
            setDistrict("");
          }}
          options={states.map((s) => ({ value: s.name, label: s.name }))}
        />
        <SelectField
          label={t("field_district")}
          value={district}
          testId="field-district"
          placeholder={t("choose")}
          onChange={setDistrict}
          options={(chosen?.districts ?? []).map((d) => ({
            value: d,
            label: d,
          }))}
        />
        <TextField
          label={t("field_local_body")}
          description={t("field_local_body_help")}
          value={localBody}
          onChange={setLocalBody}
        />
      </div>
      <CheckRow
        label={t("field_all_districts")}
        description={t("field_all_districts_help", {
          count: chosen?.districts.length ?? 0,
        })}
        checked={allDistricts}
        onChange={setAllDistricts}
      />
      <p className="text-muted-foreground text-sm" data-testid="plan">
        {t("plan_new_organisations", { count: plan })}
      </p>
      <div>
        <ApplyButton busy={busy} disabled={!state || !district} />
      </div>
    </form>
  );
}
