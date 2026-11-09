import { useState } from "react";

import {
  ApplyButton,
  CheckRow,
  SelectField,
  type StepFormProps,
  TextField,
} from "@/components/onboarding/form-parts";
import { useTranslation } from "@/hooks/use-translation";

interface Template {
  label: string;
  description: string;
  facility_type: string;
  departments: string[];
  locations: string[];
}

export function FacilityForm({ preview, busy, onApply }: StepFormProps) {
  const { t } = useTranslation();
  const templates: Record<string, Template> = preview.options?.templates ?? {};
  const facilityTypes: string[] = preview.options?.facility_types ?? [];
  const current = preview.current as
    { id: string; name: string; departments: string[] } | null | undefined;
  const [template, setTemplate] = useState<string>(
    preview.defaults?.template ?? Object.keys(templates)[0] ?? "clinic",
  );
  const chosen = templates[template];
  const [name, setName] = useState("");
  const [facilityType, setFacilityType] = useState(chosen?.facility_type ?? "");
  const [phone, setPhone] = useState("");
  const [pincode, setPincode] = useState("");
  const [address, setAddress] = useState("");
  const [departments, setDepartments] = useState<string[]>(
    chosen?.departments ?? [],
  );
  const [locations, setLocations] = useState(true);

  const chooseTemplate = (key: string) => {
    setTemplate(key);
    setFacilityType(templates[key]?.facility_type ?? "");
    setDepartments(templates[key]?.departments ?? []);
  };
  const toggle = (dept: string, on: boolean) =>
    setDepartments((list) =>
      on ? [...list, dept] : list.filter((d) => d !== dept),
    );

  const ready = current
    ? true
    : name.trim() && phone.trim() && pincode.trim() && address.trim();

  return (
    <form
      className="flex flex-col gap-5"
      onSubmit={(e) => {
        e.preventDefault();
        const common = { template, departments, locations };
        onApply(
          current
            ? { ...common, facility: current.id }
            : {
                ...common,
                name: name.trim(),
                facility_type: facilityType,
                phone_number: phone.trim(),
                pincode: Number(pincode),
                address: address.trim(),
              },
        );
      }}
    >
      {current && (
        <p className="text-sm" data-testid="facility-current">
          {t("facility_exists", { name: current.name })}
        </p>
      )}
      <div className="grid gap-4 md:grid-cols-2">
        <SelectField
          label={t("field_template")}
          value={template}
          testId="field-template"
          onChange={chooseTemplate}
          description={chosen?.description}
          options={Object.entries(templates).map(([key, value]) => ({
            value: key,
            label: value.label,
          }))}
        />
        {!current && (
          <>
            <TextField
              label={t("field_facility_name")}
              value={name}
              onChange={setName}
              required
              testId="field-name"
            />
            <SelectField
              label={t("field_facility_type")}
              value={facilityType}
              onChange={setFacilityType}
              options={facilityTypes.map((v) => ({ value: v, label: v }))}
            />
            <TextField
              label={t("field_phone")}
              value={phone}
              onChange={setPhone}
              type="tel"
              required
              placeholder={preview.defaults?.phone_example}
              description={t("field_phone_help")}
              testId="field-phone"
            />
            <TextField
              label={t("field_pincode")}
              value={pincode}
              onChange={(v) => setPincode(v.replace(/\D/g, ""))}
              inputMode="numeric"
              required
              testId="field-pincode"
            />
            <TextField
              label={t("field_address")}
              value={address}
              onChange={setAddress}
              required
              testId="field-address"
            />
          </>
        )}
      </div>
      <fieldset className="flex flex-col gap-1">
        <legend className="mb-1 text-sm font-semibold">
          {t("field_departments")}
        </legend>
        {(chosen?.departments ?? []).map((dept) => (
          <CheckRow
            key={dept}
            label={dept}
            checked={departments.includes(dept)}
            onChange={(on) => toggle(dept, on)}
          />
        ))}
      </fieldset>
      <CheckRow
        label={t("field_locations")}
        description={(chosen?.locations ?? []).join(", ")}
        checked={locations}
        onChange={setLocations}
      />
      <div>
        <ApplyButton busy={busy} disabled={!ready} />
      </div>
    </form>
  );
}
