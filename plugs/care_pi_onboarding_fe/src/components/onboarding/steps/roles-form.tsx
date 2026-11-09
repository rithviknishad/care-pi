import { useState } from "react";

import {
  ApplyButton,
  CheckRow,
  type StepFormProps,
} from "@/components/onboarding/form-parts";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useTranslation } from "@/hooks/use-translation";

interface RoleOption {
  key: string;
  label: string;
  description: string;
  care_role: string;
  custom: boolean;
  permissions: number | null;
  exists: boolean;
}

export function RolesForm({ preview, busy, onApply }: StepFormProps) {
  const { t } = useTranslation();
  const roles: RoleOption[] = preview.options?.roles ?? [];
  const [roleOrganizations, setRoleOrganizations] = useState<boolean>(
    preview.defaults?.role_organizations ?? true,
  );
  return (
    <form
      className="flex flex-col gap-5"
      onSubmit={(e) => {
        e.preventDefault();
        onApply({ role_organizations: roleOrganizations });
      }}
    >
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t("roles_job")}</TableHead>
            <TableHead>{t("roles_care_role")}</TableHead>
            <TableHead>{t("roles_status")}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {roles.map((role) => (
            <TableRow key={role.key}>
              <TableCell>
                <div className="font-medium">{role.label}</div>
                <div className="text-muted-foreground text-xs whitespace-normal">
                  {role.description}
                </div>
              </TableCell>
              <TableCell>
                {role.custom
                  ? t("roles_custom", {
                      name: role.care_role,
                      count: role.permissions ?? 0,
                    })
                  : role.care_role}
              </TableCell>
              <TableCell>
                <Badge variant={role.exists ? "success" : "neutral"}>
                  {role.exists ? t("roles_exists") : t("roles_new")}
                </Badge>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <CheckRow
        label={t("field_role_organizations")}
        description={t("field_role_organizations_help")}
        checked={roleOrganizations}
        onChange={setRoleOrganizations}
      />
      <div>
        <ApplyButton busy={busy} />
      </div>
    </form>
  );
}
