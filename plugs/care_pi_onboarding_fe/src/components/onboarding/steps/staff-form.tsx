import { Plus, Trash2 } from "lucide-react";
import { useState } from "react";

import {
  ApplyButton,
  CheckRow,
  SelectField,
  type StepFormProps,
  TextField,
} from "@/components/onboarding/form-parts";
import { Button } from "@/components/ui/button";
import { useTranslation } from "@/hooks/use-translation";
import {
  blankMember,
  inviteAvailable,
  type Member,
  memberBody,
  memberProblem,
} from "@/lib/staff";

export function StaffForm({ preview, facility, busy, onApply }: StepFormProps) {
  const { t } = useTranslation();
  const roles: { key: string; label: string }[] = preview.options?.roles ?? [];
  const genders: string[] = preview.options?.genders ?? [];
  const departments: string[] = preview.options?.departments ?? [];
  const canInvite = inviteAvailable(preview.options);
  const roleDepartments: Record<string, string> =
    preview.defaults?.departments ?? {};
  const max: number = preview.defaults?.max_members ?? 50;
  const existing: { username: string; department: string; role: string }[] =
    preview.existing?.members ?? [];
  const [members, setMembers] = useState<Member[]>([blankMember()]);

  const update = (index: number, patch: Partial<Member>) =>
    setMembers((list) =>
      list.map((m, i) => (i === index ? { ...m, ...patch } : m)),
    );

  return (
    <form
      className="flex flex-col gap-5"
      onSubmit={(e) => {
        e.preventDefault();
        onApply({
          facility,
          members: members.map((m) => memberBody(m, roleDepartments)),
        });
      }}
    >
      {existing.length > 0 && (
        <p className="text-muted-foreground text-sm">
          {t("staff_existing", { count: existing.length })}
        </p>
      )}
      <ol className="flex flex-col gap-4">
        {members.map((m, index) => (
          <li
            key={index}
            className="border-border rounded-lg border p-4"
            data-testid={`member-${index}`}
          >
            <div className="mb-3 flex items-center justify-between">
              <span className="text-sm font-semibold">
                {t("staff_person", { number: index + 1 })}
              </span>
              {members.length > 1 && (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() =>
                    setMembers((list) => list.filter((_, i) => i !== index))
                  }
                >
                  <Trash2 aria-hidden="true" />
                  {t("staff_remove")}
                </Button>
              )}
            </div>
            <div className="grid gap-3 md:grid-cols-3">
              <TextField
                label={t("field_first_name")}
                value={m.first_name}
                onChange={(v) => update(index, { first_name: v })}
                testId={`member-${index}-first-name`}
              />
              <TextField
                label={t("field_last_name")}
                value={m.last_name}
                onChange={(v) => update(index, { last_name: v })}
              />
              <TextField
                label={t("field_username")}
                value={m.username}
                onChange={(v) =>
                  update(index, { username: v.replace(/[^a-zA-Z0-9_-]/g, "") })
                }
                description={t("field_username_help")}
                testId={`member-${index}-username`}
              />
              <TextField
                label={t("field_phone")}
                type="tel"
                value={m.phone_number}
                placeholder={preview.defaults?.phone_example}
                onChange={(v) => update(index, { phone_number: v })}
                testId={`member-${index}-phone`}
              />
              <SelectField
                label={t("field_gender")}
                value={m.gender}
                onChange={(v) => update(index, { gender: v })}
                options={genders.map((g) => ({
                  value: g,
                  label: t(`gender_${g}`),
                }))}
              />
              <SelectField
                label={t("field_role")}
                value={m.role}
                testId={`member-${index}-role`}
                onChange={(v) => update(index, { role: v, department: "" })}
                options={roles.map((r) => ({ value: r.key, label: r.label }))}
              />
              <SelectField
                label={t("field_department")}
                value={m.department || roleDepartments[m.role] || ""}
                onChange={(v) => update(index, { department: v })}
                options={departments.map((d) => ({ value: d, label: d }))}
              />
              <TextField
                label={m.invite ? t("field_email_required") : t("field_email")}
                type="email"
                value={m.email}
                required={m.invite}
                onChange={(v) => update(index, { email: v })}
                description={m.invite ? undefined : t("field_email_help")}
                error={
                  memberProblem(m) === "email_for_invite"
                    ? t("error_email_for_invite")
                    : undefined
                }
                testId={`member-${index}-email`}
              />
            </div>
            <CheckRow
              label={t("field_invite")}
              description={
                canInvite
                  ? t("field_invite_help")
                  : t("field_invite_unavailable")
              }
              checked={canInvite && m.invite}
              disabled={!canInvite}
              onChange={(on) => update(index, { invite: on })}
            />
          </li>
        ))}
      </ol>
      <div className="flex flex-wrap gap-3">
        <Button
          type="button"
          variant="outline"
          disabled={members.length >= max}
          onClick={() =>
            setMembers((list) => [...list, blankMember(list.at(-1)?.role)])
          }
          data-testid="add-member"
        >
          <Plus aria-hidden="true" />
          {t("staff_add")}
        </Button>
        <ApplyButton
          busy={busy}
          disabled={members.some((m) => memberProblem(m) !== null)}
        />
      </div>
      <p className="text-muted-foreground text-sm">
        {t("staff_password_note")}
      </p>
    </form>
  );
}
