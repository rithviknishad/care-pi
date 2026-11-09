import { ChecklistForm } from "@/components/onboarding/steps/checklist-form";
import { FacilityForm } from "@/components/onboarding/steps/facility-form";
import { OrganizationForm } from "@/components/onboarding/steps/organization-form";
import { RolesForm } from "@/components/onboarding/steps/roles-form";
import { StaffForm } from "@/components/onboarding/steps/staff-form";
import {
  ApplyButton,
  type StepFormProps,
} from "@/components/onboarding/form-parts";
import { useTranslation } from "@/hooks/use-translation";
import type { StepName } from "@/lib/wizard";

function DemoForm({ preview, facility, busy, onApply }: StepFormProps) {
  const { t } = useTranslation();
  const patients: { key: string; name: string; age: number }[] =
    preview.options?.patients ?? [];
  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        onApply({ facility });
      }}
    >
      <p className="text-sm">
        {t("demo_intro", { department: preview.options?.department ?? "" })}
      </p>
      <ul className="list-disc pl-5 text-sm">
        {patients.map((p) => (
          <li key={p.key}>{t("demo_patient", { name: p.name, age: p.age })}</li>
        ))}
      </ul>
      <div>
        <ApplyButton busy={busy} label={t("demo_apply")} />
      </div>
    </form>
  );
}

export function StepForm({
  step,
  ...props
}: StepFormProps & { step: StepName }) {
  const { t } = useTranslation();
  const options = props.preview.options ?? {};
  switch (step) {
    case "organization":
      return <OrganizationForm {...props} />;
    case "facility":
      return <FacilityForm {...props} />;
    case "roles":
      return <RolesForm {...props} />;
    case "staff":
      return <StaffForm {...props} />;
    case "valuesets":
      return (
        <ChecklistForm
          {...props}
          facility={null}
          items={(options.valuesets ?? []).map((v: any) => ({
            key: v.slug,
            label: v.name,
            description: t("valueset_codes", { count: v.codes }),
            exists: v.exists,
          }))}
          extra={{
            key: "system",
            initial: true,
            label: t("field_system_valuesets"),
            description: t("field_system_valuesets_help", {
              have: props.preview.existing?.system ?? 0,
              total: props.preview.existing?.system_total ?? 0,
            }),
          }}
        />
      );
    case "questionnaires":
      return (
        <ChecklistForm
          {...props}
          items={(options.questionnaires ?? []).map((q: any) => ({
            key: q.slug,
            label: q.title,
            description: `${q.description} ${t("questionnaire_questions", { count: q.questions })}`,
            exists: q.exists,
          }))}
        />
      );
    case "catalog":
      return (
        <ChecklistForm
          {...props}
          note={t("catalog_note")}
          items={(options.medications ?? []).map((m: any) => ({
            key: m.slug,
            label: m.name,
            exists: m.exists,
          }))}
        />
      );
    case "demo_data":
      return <DemoForm {...props} />;
  }
}
