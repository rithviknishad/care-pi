import secrets
import string

from django.db.models import Q
from rest_framework import serializers
from rest_framework.exceptions import PermissionDenied

from care_pi_onboarding.services.base import (
    Context,
    Step,
    StepError,
    StepResult,
    Tally,
    save_new,
    validate_spec,
)
from care_pi_onboarding.services.facility import ROOT, templates
from care_pi_onboarding.services.roles import care_role_name, role_fixture, role_keys
from care_pi_onboarding.settings import plugin_settings

STEP = "staff"
GENDERS = ["male", "female", "transgender", "non_binary"]
PLACEHOLDER_EMAIL_DOMAIN = "staff.invalid"


class MemberInput(serializers.Serializer):
    username = serializers.RegexField(r"^[a-zA-Z0-9_-]{3,150}$")
    first_name = serializers.CharField(max_length=150)
    last_name = serializers.CharField(max_length=150, allow_blank=True, default="")
    phone_number = serializers.CharField(max_length=14)
    email = serializers.EmailField(required=False, allow_blank=True)
    gender = serializers.ChoiceField(choices=GENDERS)
    role = serializers.ChoiceField(choices=[])
    department = serializers.CharField(required=False, allow_blank=True, max_length=255)
    password = serializers.CharField(
        required=False, allow_blank=True, write_only=True, max_length=128
    )
    invite = serializers.BooleanField(default=False)

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.fields["role"].choices = role_keys()

    def validate(self, attrs):
        # An invited account has no password: without a real address nobody can activate it.
        if attrs.get("invite") and not (attrs.get("email") or "").strip():
            raise serializers.ValidationError(
                {
                    "email": "Give an email address when invite is on. CARE sends the invitation to this address."
                }
            )
        return attrs


class StaffInput(serializers.Serializer):
    facility = serializers.UUIDField()
    members = MemberInput(many=True, allow_empty=False)

    def validate_members(self, members):
        limit = int(plugin_settings.ONBOARDING_MAX_STAFF)
        if len(members) > limit:
            raise serializers.ValidationError(
                f"Give at most {limit} staff members in one call."
            )
        seen = set()
        for member in members:
            key = member["username"].lower()
            if key in seen:
                raise serializers.ValidationError(
                    f"The username {member['username']} is in the list twice."
                )
            seen.add(key)
        return members


def generate_password(length: int = 14) -> str:
    """A random password that passes the CARE password validators (letters, digits, and a symbol)."""
    alphabet = string.ascii_letters + string.digits
    while True:
        body = "".join(secrets.choice(alphabet) for _ in range(length - 1))
        if (
            any(c.isupper() for c in body)
            and any(c.islower() for c in body)
            and any(c.isdigit() for c in body)
        ):
            return body + secrets.choice("@#%+=")


def _role_map(ctx: Context) -> dict:
    from care.security.models import RoleModel

    previous = ctx.previous("roles", scope="").get("roles") or {}
    names = {e["key"]: care_role_name(e) for e in role_fixture()}
    roles = {r.name: r for r in RoleModel.objects.filter(name__in=names.values())}
    missing = [n for n in names.values() if n not in roles]
    if missing:
        raise StepError(
            f"CARE has no role {', '.join(missing)}. Apply the step roles first."
        )
    return {
        key: {
            "role": roles[name],
            "role_organization": (previous.get(key) or {}).get("role_organization"),
        }
        for key, name in names.items()
    }


def _member_role():
    from care.security.models import RoleModel

    return RoleModel.objects.filter(
        name="Member", contexts__contains=["ROLE_ORG"]
    ).first()


def _department(facility, name: str):
    from care.emr.models.organization import FacilityOrganization

    if name.strip().lower() == ROOT.lower():
        return FacilityOrganization.objects.get(facility=facility, org_type="root")
    org = (
        FacilityOrganization.objects.filter(
            facility=facility, name__iexact=name.strip()
        )
        .exclude(org_type="root")
        .first()
    )
    if not org:
        raise StepError(
            f"The facility has no department {name}. Apply the step facility first, or give a department of the facility."
        )
    return org


def _ensure_membership(ctx: Context, tally: Tally, user, org, role, label: str):
    from care.emr.models.organization import FacilityOrganizationUser

    memberships = FacilityOrganizationUser.objects.filter(
        user=user, organization__facility=org.facility
    )
    same = memberships.filter(organization=org).first()
    if same:
        if same.role_id != role.id:
            raise StepError(
                f"{label} is in {org.name} with the role {same.role.name}, not {role.name}. "
                "Change the role in CARE, or remove the person from the list."
            )
        tally.found("membership")
        return
    # The CARE rule: a person is in one organisation of a branch, not in its parent or child too.
    conflict = memberships.filter(
        Q(organization_id__in=org.parent_cache)
        | Q(organization__parent_cache__overlap=[org.id])
    ).first()
    if conflict:
        raise StepError(
            f"{label} is in {conflict.organization.name}, which is in the same branch as {org.name}."
        )
    obj = FacilityOrganizationUser(organization=org, user=user, role=role)
    save_new(obj, ctx.user)
    ctx.record(STEP, obj, f"membership:{user.username}:{org.external_id}")
    tally.made("membership")


def _ensure_role_organization(
    ctx: Context, tally: Tally, user, role_org_id, member_role
):
    """Membership of the role organisation of the job, with the role Member (context ROLE_ORG only)."""
    from care.emr.models.organization import Organization, OrganizationUser

    if not (role_org_id and member_role and ctx.user.is_superuser):
        return
    org = Organization.objects.filter(external_id=role_org_id, org_type="role").first()
    if org is None:
        return
    if OrganizationUser.objects.filter(organization=org, user=user).exists():
        tally.found("role_organization_membership")
        return
    # OrganizationUser.save refreshes the role organisation cache of the user.
    save_new(OrganizationUser(organization=org, user=user, role=member_role), ctx.user)
    tally.made("role_organization_membership")


def _create_user(ctx: Context, tally: Tally, member: dict, facility):
    from care.emr.resources.user.spec import UserCreateSpec
    from care.utils.models.validators import mobile_or_landline_number_validator
    from django.core.exceptions import ValidationError as DjangoValidationError

    try:
        # The validator of User.phone_number; the CARE user spec does not run it.
        mobile_or_landline_number_validator(member["phone_number"])
    except DjangoValidationError as exc:
        raise StepError(
            f"Staff {member['username']}: phone_number: {' '.join(exc.messages)}"
        ) from exc
    invite = member.get("invite", False)
    password = None if invite else (member.get("password") or generate_password())
    email = (
        member.get("email")
        or f"{member['username'].lower()}@{PLACEHOLDER_EMAIL_DOMAIN}"
    )
    spec = validate_spec(
        UserCreateSpec,
        {
            "username": member["username"],
            "first_name": member["first_name"],
            "last_name": member.get("last_name", ""),
            "phone_number": member["phone_number"],
            "email": email,
            "gender": member["gender"],
            "password": password,
            "geo_organization": str(facility.geo_organization.external_id)
            if facility.geo_organization
            else None,
            "role_orgs": [],
            "is_service_account": False,
        },
        f"Staff {member['username']}",
    )
    user = save_new(spec.de_serialize(), ctx.user)
    ctx.record(STEP, user, f"user:{user.username}")
    tally.made("user")
    if invite:
        from care.emr.resources.common.mail_type import MailTypeChoices
        from care.emr.utils.reset_password import send_password_reset_email

        try:
            send_password_reset_email(user, MailTypeChoices.create.value)
        except Exception as exc:
            raise StepError(
                f"CARE could not send the invitation email to {email}. Check the email settings of CARE, "
                "or give no invite to get an initial password."
            ) from exc
        return user, None
    return user, password


class StaffStep(Step):
    name = STEP
    title = "Staff"
    description = "The staff accounts, each with a role in a department."
    scope = "facility"

    def input_serializer(self):
        return StaffInput

    def apply(self, ctx: Context, data: dict, tally: Tally) -> StepResult:
        from care.security.authorization import AuthorizationController
        from care.users.models import User

        if not AuthorizationController.call("can_create_user", ctx.user):
            raise PermissionDenied("You cannot create users in CARE.")
        facility = ctx.facility
        roles = _role_map(ctx)
        member_role = _member_role()
        template_key = ctx.previous("facility", scope="").get("template", "clinic")
        defaults = (
            templates()
            .get(template_key, templates()["clinic"])
            .get("staff_departments", {})
        )
        credentials = []
        invited = []
        people = []
        for index, member in enumerate(data["members"]):
            label = f"Member {index + 1} ({member['username']})"
            info = roles[member["role"]]
            department = (
                member.get("department") or defaults.get(member["role"]) or ROOT
            )
            org = _department(facility, department)
            user = User.objects.filter(username=member["username"]).first()
            if user is None:
                if User.check_username_exists(member["username"]):
                    raise StepError(
                        f"{label}: the username belongs to a deleted account. Choose another username."
                    )
                user, password = _create_user(ctx, tally, member, facility)
                if password:
                    credentials.append(
                        {"username": user.username, "password": password}
                    )
                else:
                    invited.append(user.username)
            else:
                tally.found("user")
            _ensure_membership(ctx, tally, user, org, info["role"], label)
            _ensure_role_organization(
                ctx, tally, user, info.get("role_organization"), member_role
            )
            people.append(
                {
                    "username": user.username,
                    "id": str(user.external_id),
                    "role": member["role"],
                    "department": org.name,
                }
            )
        made = tally.created.get("user", 0)
        detail = f"The facility has {len(people)} staff members from this list. {made} accounts are new."
        if credentials:
            detail += (
                " Give each person the initial password. CARE does not show it again."
            )
        return StepResult(
            detail=detail,
            data={"staff": people, "invited": invited},
            secrets={"credentials": credentials} if credentials else {},
        )

    def preview(self, ctx: Context, params) -> dict:
        from care.emr.models.organization import (
            FacilityOrganization,
            FacilityOrganizationUser,
        )

        facility = ctx.facility
        departments = []
        members = []
        if facility is not None:
            departments = list(
                FacilityOrganization.objects.filter(facility=facility)
                .order_by("org_type", "name")
                .values_list("name", flat=True)
            )
            members = [
                {"username": u, "department": d, "role": r}
                for u, d, r in FacilityOrganizationUser.objects.filter(
                    organization__facility=facility
                )
                .order_by("user__username")
                .values_list("user__username", "organization__name", "role__name")[:200]
            ]
        template_key = ctx.previous("facility", scope="").get("template", "clinic")
        return {
            "options": {
                "roles": [
                    {"key": e["key"], "label": e["label"]} for e in role_fixture()
                ],
                "genders": GENDERS,
                "departments": departments,
            },
            "defaults": {
                "departments": templates()
                .get(template_key, templates()["clinic"])
                .get("staff_departments", {}),
                "phone_example": ctx.locale.get("phone_example", ""),
                "max_members": int(plugin_settings.ONBOARDING_MAX_STAFF),
            },
            "existing": {"members": members},
        }

    def inventory(self, ctx: Context) -> dict:
        from care.emr.models.organization import FacilityOrganizationUser

        if ctx.facility is None:
            return {}
        return {
            "membership": FacilityOrganizationUser.objects.filter(
                organization__facility=ctx.facility
            ).count()
        }
