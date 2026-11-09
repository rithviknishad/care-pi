from django.conf import settings
from django.core.exceptions import ValidationError as DjangoValidationError
from django.db.models.functions import Lower, Trim
from rest_framework import serializers
from rest_framework.exceptions import PermissionDenied

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

STEP = "facility"
ROOT = "Administration"


def templates() -> dict:
    return fixtures.load("facility_templates.json")


class FacilityInput(serializers.Serializer):
    facility = serializers.UUIDField(required=False)
    template = serializers.ChoiceField(choices=[], default="clinic")
    name = serializers.CharField(required=False, max_length=1000)
    facility_type = serializers.CharField(required=False, max_length=255)
    description = serializers.CharField(required=False, allow_blank=True, default="")
    address = serializers.CharField(required=False, allow_blank=True)
    pincode = serializers.IntegerField(required=False, min_value=1)
    phone_number = serializers.CharField(required=False, max_length=14)
    latitude = serializers.FloatField(
        required=False, allow_null=True, min_value=-90, max_value=90
    )
    longitude = serializers.FloatField(
        required=False, allow_null=True, min_value=-180, max_value=180
    )
    features = serializers.ListField(child=serializers.IntegerField(), required=False)
    is_public = serializers.BooleanField(default=False)
    geo_organization = serializers.UUIDField(required=False)
    departments = serializers.ListField(
        child=serializers.CharField(max_length=255), required=False, max_length=100
    )
    locations = serializers.BooleanField(default=True)

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.fields["template"].choices = list(templates())

    def validate(self, attrs):
        if not attrs.get("facility"):
            missing = [
                f
                for f in ("name", "address", "pincode", "phone_number")
                if attrs.get(f) in (None, "")
            ]
            if missing:
                raise serializers.ValidationError(
                    {f: "This field is required for a new facility." for f in missing}
                )
        return attrs


def _find_facility(ctx: Context, data: dict):
    from care.facility.models import Facility

    if data.get("facility"):
        facility = Facility.objects.filter(external_id=data["facility"]).first()
        if not facility:
            raise StepError("The facility does not exist.")
        return facility
    previous = ctx.previous(STEP).get("facility") or {}
    if previous.get("id"):
        facility = Facility.objects.filter(external_id=previous["id"]).first()
        if facility and facility.name.strip().lower() == data["name"].strip().lower():
            return facility
    return (
        Facility.objects.annotate(normalized=Lower(Trim("name")))
        .filter(normalized=data["name"].strip().lower())
        .first()
    )


def _geo_organization(ctx: Context, data: dict):
    from care.emr.models.organization import Organization

    geo_id = data.get("geo_organization") or (
        ctx.previous("organization").get("geo_organization") or {}
    ).get("id")
    if not geo_id:
        raise StepError(
            "The facility needs a government organisation. Apply the step organization first, "
            "or give geo_organization."
        )
    geo = Organization.objects.filter(external_id=geo_id, org_type="govt").first()
    if not geo:
        raise StepError("geo_organization is not a government organisation.")
    return geo


def _create_facility(ctx: Context, data: dict, template: dict):
    from care.emr.resources.facility.spec import FacilityCreateSpec
    from care.facility.models import REVERSE_REVERSE_FACILITY_TYPES
    from care.utils.models.validators import mobile_or_landline_number_validator

    facility_type = data.get("facility_type") or template["facility_type"]
    if facility_type not in REVERSE_REVERSE_FACILITY_TYPES:
        raise StepError(
            f"The facility type {facility_type} is not a CARE facility type."
        )
    try:
        mobile_or_landline_number_validator(data["phone_number"])
    except DjangoValidationError as exc:
        raise StepError(f"phone_number: {' '.join(exc.messages)}") from exc
    pattern = ctx.locale.get("pincode_pattern")
    if pattern:
        import re

        if not re.fullmatch(pattern, str(data["pincode"])):
            raise StepError(
                f"pincode: {data['pincode']} is not a pincode of {ctx.locale['country']}."
            )
    geo = _geo_organization(ctx, data)
    spec = validate_spec(
        FacilityCreateSpec,
        {
            "name": data["name"].strip(),
            "description": data.get("description", ""),
            "facility_type": facility_type,
            "address": data["address"],
            "pincode": data["pincode"],
            "phone_number": data["phone_number"],
            "latitude": data.get("latitude"),
            "longitude": data.get("longitude"),
            "is_public": data.get("is_public", False),
            "geo_organization": str(geo.external_id),
            "features": data.get("features", template.get("features", [])),
        },
        "Facility",
    )
    obj = spec.de_serialize()
    obj.created_by = ctx.user
    # Facility.save makes the root organisation Administration and adds created_by as Facility Admin.
    obj.save()
    return obj


def ensure_department(ctx: Context, tally: Tally, facility, name: str, description=""):
    from care.emr.models.organization import FacilityOrganization
    from care.emr.resources.facility_organization.spec import (
        FacilityOrganizationWriteSpec,
    )

    if name.strip().lower() == ROOT.lower():
        return FacilityOrganization.objects.get(facility=facility, org_type="root")
    found = FacilityOrganization.objects.filter(
        facility=facility, parent__isnull=True, name__iexact=name.strip()
    ).first()
    if found:
        tally.found("department")
        return found
    if (
        FacilityOrganization.objects.filter(facility=facility).count()
        >= settings.MAX_ORGANIZATION_IN_FACILITY
    ):
        raise StepError(
            f"The facility has the largest number of organisations ({settings.MAX_ORGANIZATION_IN_FACILITY})."
        )
    spec = validate_spec(
        FacilityOrganizationWriteSpec,
        {
            "name": name.strip(),
            "org_type": "dept",
            "description": description,
            "active": True,
            "facility": str(facility.external_id),
        },
        f"Department {name}",
    )
    obj = save_new(spec.de_serialize(), ctx.user)
    ctx.record(STEP, obj, f"dept:{facility.external_id}:{obj.name}")
    tally.made("department")
    return obj


def _ensure_location(
    ctx: Context, tally: Tally, facility, spec_data: dict, parent, departments: dict
):
    from care.emr.models.location import (
        FacilityLocation,
        FacilityLocationOrganization,
    )
    from care.emr.resources.location.spec import FacilityLocationWriteSpec

    name = spec_data["name"].strip()
    queryset = FacilityLocation.objects.filter(facility=facility, name__iexact=name)
    queryset = (
        queryset.filter(parent=parent)
        if parent
        else queryset.filter(parent__isnull=True)
    )
    location = queryset.first()
    wanted = spec_data.get("departments", [])
    orgs = [departments[d.lower()] for d in wanted if d.lower() in departments]
    if wanted and not orgs and location is None:
        # The operator left out every department of this room, so the room is left out too.
        return
    if location:
        tally.found("location")
    else:
        if parent and parent.level_cache >= settings.LOCATION_MAX_DEPTH:
            raise StepError(f"The location {parent.name} is at the largest depth.")
        if (
            FacilityLocation.objects.filter(facility=facility).count()
            >= settings.MAX_LOCATION_IN_FACILITY
        ):
            raise StepError("The facility has the largest number of locations.")
        spec = validate_spec(
            FacilityLocationWriteSpec,
            {
                "name": name,
                "description": spec_data.get("description", ""),
                "status": "active",
                "operational_status": "O",
                "form": spec_data["form"],
                "mode": spec_data["mode"],
                "parent": str(parent.external_id) if parent else None,
                "organizations": [str(o.external_id) for o in orgs],
            },
            f"Location {name}",
        )
        obj = spec.de_serialize()
        obj.facility = facility
        location = save_new(obj, ctx.user)
        ctx.record(STEP, location, f"location:{facility.external_id}:{name}")
        tally.made("location")
    for org in orgs:
        link = FacilityLocationOrganization.objects.filter(
            location=location, organization=org
        )
        if link.exists():
            tally.found("location_department")
            continue
        # FacilityLocationOrganization.save refreshes the organisation cache of the location tree.
        save_new(
            FacilityLocationOrganization(location=location, organization=org), ctx.user
        )
        tally.made("location_department")
    for child in spec_data.get("children", []):
        _ensure_location(ctx, tally, facility, child, location, departments)


def facility_json(facility) -> dict:
    return {"id": str(facility.external_id), "name": facility.name}


class FacilityStep(Step):
    name = STEP
    title = "Facility"
    description = "The facility, its departments, and its rooms from a template."
    scope = "facility"

    def input_serializer(self):
        return FacilityInput

    def authorize(self, user, facility) -> bool:
        if user.is_superuser:
            return True
        if facility is not None:
            return super().authorize(user, facility)
        from care.security.authorization import AuthorizationController

        return AuthorizationController.call("can_create_facility", user)

    def apply(self, ctx: Context, data: dict, tally: Tally) -> StepResult:
        from care.emr.models.organization import FacilityOrganization

        template_key = data.get("template", "clinic")
        template = templates()[template_key]
        facility = ctx.facility or _find_facility(ctx, data)
        if facility is None:
            facility = _create_facility(ctx, data, template)
            ctx.record(STEP, facility, f"facility:{facility.name}")
            tally.made("facility")
            made_facility = True
        else:
            if not self.authorize(ctx.user, facility):
                raise PermissionDenied(
                    "Only a facility admin of this facility can change it."
                )
            tally.found("facility")
            made_facility = False

        wanted = data.get("departments")
        template_departments = {d["name"].lower(): d for d in template["departments"]}
        if wanted is None:
            wanted = [d["name"] for d in template["departments"]]
        departments = {}
        for name in wanted:
            description = template_departments.get(name.lower(), {}).get(
                "description", ""
            )
            org = ensure_department(ctx, tally, facility, name, description)
            departments[org.name.lower()] = org
        root = FacilityOrganization.objects.get(facility=facility, org_type="root")
        departments[root.name.lower()] = root

        if data.get("locations", True):
            for location in template.get("locations", []):
                _ensure_location(ctx, tally, facility, location, None, departments)

        verb = "made" if made_facility else "found"
        return StepResult(
            detail=(
                f"The step {verb} the facility {facility.name}. "
                f"Departments of this step in the facility: {len(departments) - 1}."
            ),
            data={
                "facility": facility_json(facility),
                "template": template_key,
                "departments": {
                    org.name: str(org.external_id) for org in departments.values()
                },
            },
        )

    def preview(self, ctx: Context, params) -> dict:
        from care.facility.models import REVERSE_FACILITY_TYPES, FacilityFeature

        facility = ctx.facility
        current = None
        if facility is not None:
            from care.emr.models.organization import FacilityOrganization

            current = {
                **facility_json(facility),
                "departments": list(
                    FacilityOrganization.objects.filter(facility=facility)
                    .exclude(org_type="root")
                    .order_by("name")
                    .values_list("name", flat=True)
                ),
            }
        geo = ctx.previous("organization").get("geo_organization")
        return {
            "options": {
                "templates": {
                    key: {
                        "label": t["label"],
                        "description": t["description"],
                        "facility_type": t["facility_type"],
                        "departments": [d["name"] for d in t["departments"]],
                        "locations": _location_names(t.get("locations", [])),
                    }
                    for key, t in templates().items()
                },
                "facility_types": sorted(REVERSE_FACILITY_TYPES.values()),
                "features": [
                    {"value": f.value, "label": f.label} for f in FacilityFeature
                ],
            },
            "defaults": {
                "template": ctx.previous(STEP).get("template", "clinic"),
                "geo_organization": geo,
                "phone_example": ctx.locale.get("phone_example", ""),
            },
            "current": current,
        }

    def inventory(self, ctx: Context) -> dict:
        from care.emr.models.location import FacilityLocation
        from care.emr.models.organization import FacilityOrganization
        from care.facility.models import Facility

        data = {"facility": Facility.objects.count()}
        if ctx.facility is not None:
            data["department"] = (
                FacilityOrganization.objects.filter(facility=ctx.facility)
                .exclude(org_type="root")
                .count()
            )
            data["location"] = FacilityLocation.objects.filter(
                facility=ctx.facility
            ).count()
        return data


def _location_names(locations: list) -> list[str]:
    names = []
    for location in locations:
        names.append(location["name"])
        names.extend(
            f"{location['name']} > {n}"
            for n in _location_names(location.get("children", []))
        )
    return names
