from django.core.cache import cache
from django.core.management import call_command
from rest_framework import serializers

from care_pi_onboarding.services import fixtures
from care_pi_onboarding.services.base import (
    Context,
    Step,
    StepError,
    StepResult,
    Tally,
    save_new,
    validate_spec,
)

STEP = "roles"


def role_fixture() -> list[dict]:
    return fixtures.load("roles.json")["roles"]


def role_keys() -> list[str]:
    return [r["key"] for r in role_fixture()]


def care_role_name(entry: dict) -> str:
    return entry.get("care_role") or entry["custom"]["name"]


class RolesInput(serializers.Serializer):
    role_organizations = serializers.BooleanField(default=True)


def _ensure_system_roles(names: set[str]):
    from care.security.models import RoleModel

    def missing():
        have = set(
            RoleModel.objects.filter(name__in=names, is_system=True).values_list(
                "name", flat=True
            )
        )
        return names - have

    if missing():
        # CARE runs this command at each start. It is idempotent and takes a Redis lock.
        call_command("sync_permissions_roles")
    gone = missing()
    if gone:
        raise StepError(
            f"CARE has no system role {', '.join(sorted(gone))}. Check the CARE version."
        )


def _clear_role_cache(role):
    from care.security.models.role import (
        ROLE_PERMISSION_SK_CACHE_KEY,
        ROLE_PERMISSIONS_CACHE_KEY,
    )

    cache.delete(ROLE_PERMISSIONS_CACHE_KEY.format(role.id))
    cache.delete(ROLE_PERMISSION_SK_CACHE_KEY.format(role.id))


def _ensure_custom_role(ctx: Context, tally: Tally, entry: dict):
    from care.emr.resources.role.spec import RoleCreateSpec
    from care.security.models import PermissionModel, RoleModel, RolePermission

    custom = entry["custom"]
    slugs = list(dict.fromkeys(custom["permissions"]))
    permissions = {p.slug: p for p in PermissionModel.objects.filter(slug__in=slugs)}
    unknown = [s for s in slugs if s not in permissions]
    if unknown:
        raise StepError(
            f"The role {custom['name']} uses permissions that CARE does not have: {', '.join(unknown)}."
        )
    role = RoleModel.objects.filter(name__iexact=custom["name"]).first()
    if role is None:
        spec = validate_spec(
            RoleCreateSpec,
            {
                "name": custom["name"],
                "description": entry.get("description", ""),
                "contexts": custom.get("contexts", ["FACILITY"]),
                "permissions": slugs,
            },
            f"Role {custom['name']}",
        )
        role = spec.de_serialize()
        role.is_system = False
        role.save()
        ctx.record(STEP, role, f"role:{role.name}")
        tally.made("role")
    elif role.is_system:
        raise StepError(
            f"{custom['name']} is a system role of CARE. Rename the custom role in the fixture."
        )
    else:
        tally.found("role")
    have = set(
        RolePermission.objects.filter(role=role, temp_deleted=False).values_list(
            "permission__slug", flat=True
        )
    )
    new = [
        RolePermission(role=role, permission=permissions[s])
        for s in slugs
        if s not in have
    ]
    tally.found("role_permission", len(slugs) - len(new))
    if new:
        RolePermission.objects.bulk_create(new)
        _clear_role_cache(role)
        tally.made("role_permission", len(new))
    return role


def _ensure_role_organization(ctx: Context, tally: Tally, label: str):
    from care.emr.models.organization import Organization
    from care.emr.resources.organization.spec import OrganizationWriteSpec

    found = Organization.objects.filter(
        org_type="role", parent__isnull=True, name__iexact=label
    ).first()
    if found:
        tally.found("role_organization")
        return found
    spec = validate_spec(
        OrganizationWriteSpec,
        {
            "name": label,
            "org_type": "role",
            "active": True,
            "description": "",
            "metadata": {},
        },
        f"Role organisation {label}",
    )
    obj = save_new(spec.de_serialize(), ctx.user)
    ctx.record(STEP, obj, f"role_org:{label}")
    tally.made("role_organization")
    return obj


class RolesStep(Step):
    name = STEP
    title = "Roles"
    description = "The roles of the clinic staff and their CARE permissions."
    scope = "instance"

    def input_serializer(self):
        return RolesInput

    def apply(self, ctx: Context, data: dict, tally: Tally) -> StepResult:
        from care.security.models import RoleModel

        entries = role_fixture()
        _ensure_system_roles({e["care_role"] for e in entries if e.get("care_role")})
        roles = {}
        for entry in entries:
            if entry.get("custom"):
                role = _ensure_custom_role(ctx, tally, entry)
            else:
                role = RoleModel.objects.get(name=entry["care_role"], is_system=True)
                tally.found("role")
            org = None
            if data.get("role_organizations", True):
                org = _ensure_role_organization(ctx, tally, entry["label"])
            roles[entry["key"]] = {
                "id": str(role.external_id),
                "name": role.name,
                "label": entry["label"],
                "role_organization": str(org.external_id) if org else None,
            }
        return StepResult(
            detail=f"The clinic has {len(roles)} roles: {', '.join(r['label'] for r in roles.values())}.",
            data={"roles": roles},
        )

    def preview(self, ctx: Context, params) -> dict:
        from care.security.models import RoleModel

        names = [care_role_name(e) for e in role_fixture()]
        existing = set(
            RoleModel.objects.filter(name__in=names).values_list("name", flat=True)
        )
        return {
            "options": {
                "roles": [
                    {
                        "key": e["key"],
                        "label": e["label"],
                        "description": e.get("description", ""),
                        "care_role": care_role_name(e),
                        "custom": bool(e.get("custom")),
                        "permissions": len(e["custom"]["permissions"])
                        if e.get("custom")
                        else None,
                        "exists": care_role_name(e) in existing,
                    }
                    for e in role_fixture()
                ]
            },
            "defaults": {"role_organizations": True},
        }

    def inventory(self, ctx: Context) -> dict:
        from care.emr.models.organization import Organization
        from care.security.models import RoleModel

        entries = role_fixture()
        return {
            "role": RoleModel.objects.filter(
                name__in=[care_role_name(e) for e in entries]
            ).count(),
            "role_organization": Organization.objects.filter(
                org_type="role", name__in=[e["label"] for e in entries]
            ).count(),
        }
