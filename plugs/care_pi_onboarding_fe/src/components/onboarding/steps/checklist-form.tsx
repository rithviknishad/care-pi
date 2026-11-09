import { useState } from "react";

import {
  ApplyButton,
  CheckRow,
  type StepFormProps,
} from "@/components/onboarding/form-parts";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { useTranslation } from "@/hooks/use-translation";

interface Item {
  key: string;
  label: string;
  description?: string;
  exists: boolean;
}

/** One form for the steps that pick items from a fixture list: valuesets, questionnaires, catalog. */
export function ChecklistForm({
  preview,
  facility,
  busy,
  onApply,
  items,
  extra,
  note,
}: StepFormProps & {
  items: Item[];
  extra?: { label: string; description: string; key: string; initial: boolean };
  note?: string;
}) {
  const { t } = useTranslation();
  const [selected, setSelected] = useState<string[]>(
    preview.defaults?.selected ?? items.map((i) => i.key),
  );
  const [flag, setFlag] = useState<boolean>(extra?.initial ?? false);
  const toggle = (key: string, on: boolean) =>
    setSelected((list) =>
      on ? [...list, key] : list.filter((k) => k !== key),
    );

  return (
    <form
      className="flex flex-col gap-5"
      onSubmit={(e) => {
        e.preventDefault();
        onApply({
          ...(facility ? { facility } : {}),
          selected,
          ...(extra ? { [extra.key]: flag } : {}),
        });
      }}
    >
      {note && (
        <Alert variant="info">
          <AlertDescription>{note}</AlertDescription>
        </Alert>
      )}
      <fieldset className="flex flex-col gap-1">
        <legend className="mb-1 text-sm font-semibold">
          {t("choose_items")}
        </legend>
        {items.map((item) => (
          <CheckRow
            key={item.key}
            label={
              item.exists ? t("item_exists", { name: item.label }) : item.label
            }
            description={item.description}
            checked={selected.includes(item.key)}
            onChange={(on) => toggle(item.key, on)}
          />
        ))}
      </fieldset>
      {extra && (
        <CheckRow
          label={extra.label}
          description={extra.description}
          checked={flag}
          onChange={setFlag}
        />
      )}
      <div>
        <ApplyButton busy={busy} disabled={selected.length === 0 && !flag} />
      </div>
    </form>
  );
}
