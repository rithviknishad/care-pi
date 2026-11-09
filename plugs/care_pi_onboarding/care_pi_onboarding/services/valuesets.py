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

STEP = "valuesets"


def clinic_valuesets() -> list[dict]:
    return fixtures.load("valuesets.json")["valuesets"]


class ValuesetsInput(serializers.Serializer):
    system = serializers.BooleanField(default=True)
    selected = serializers.ListField(
        child=serializers.CharField(max_length=255), required=False
    )

    def validate_selected(self, value):
        known = {v["slug"] for v in clinic_valuesets()}
        unknown = [s for s in value if s not in known]
        if unknown:
            raise serializers.ValidationError(
                f"Unknown value sets: {', '.join(unknown)}."
            )
        return value


def _sync_system(ctx: Context, tally: Tally):
    """The missing part of sync_valueset (CARE command, without --overwrite), in one INSERT."""
    from care.emr.models import ValueSet
    from care.emr.registries.care_valueset.care_valueset import SystemValueSet

    system = SystemValueSet.get_all_valuesets()
    have = set(
        ValueSet.objects.filter(slug__in=[v.slug for v in system]).values_list(
            "slug", flat=True
        )
    )
    new = [
        ValueSet(
            slug=v.slug,
            name=v.name,
            status=v.status,
            compose=v.composition.model_dump(exclude_defaults=True),
            is_system_defined=True,
            created_by=ctx.user,
            updated_by=ctx.user,
        )
        for v in system
        if v.slug not in have
    ]
    tally.found("system_valueset", len(system) - len(new))
    if new:
        ValueSet.objects.bulk_create(new)
        tally.made("system_valueset", len(new))


class ValuesetsStep(Step):
    name = STEP
    title = "Value sets"
    description = "The coded lists of CARE, and the short lists of the clinic."
    scope = "instance"

    def input_serializer(self):
        return ValuesetsInput

    def apply(self, ctx: Context, data: dict, tally: Tally) -> StepResult:
        from care.emr.models import ValueSet
        from care.emr.resources.valueset.spec import ValueSetSpec

        selected = data.get("selected")
        chosen = [
            v for v in clinic_valuesets() if selected is None or v["slug"] in selected
        ]
        if not chosen and not data.get("system", True):
            raise StepError("Select at least one value set.")
        if data.get("system", True):
            _sync_system(ctx, tally)
        slugs = {}
        for entry in chosen:
            found = ValueSet.objects.filter(
                slug=entry["slug"], auth_context="instance"
            ).first()
            if found:
                tally.found("clinic_valueset")
                slugs[entry["slug"]] = str(found.external_id)
                continue
            spec = validate_spec(
                ValueSetSpec,
                {
                    "slug": entry["slug"],
                    "name": entry["name"],
                    "description": entry.get("description", ""),
                    "compose": entry["compose"],
                    "status": entry.get("status", "active"),
                    "disable_composition": False,
                },
                f"Value set {entry['slug']}",
            )
            obj = spec.de_serialize()
            obj.auth_context = "instance"
            save_new(obj, ctx.user)
            ctx.record(STEP, obj, f"valueset:{obj.slug}")
            tally.made("clinic_valueset")
            slugs[entry["slug"]] = str(obj.external_id)
        return StepResult(
            detail=f"CARE has {len(slugs)} clinic value sets and each system value set.",
            data={"valuesets": slugs},
        )

    def preview(self, ctx: Context, params) -> dict:
        from care.emr.models import ValueSet
        from care.emr.registries.care_valueset.care_valueset import SystemValueSet

        system = [v.slug for v in SystemValueSet.get_all_valuesets()]
        clinic = clinic_valuesets()
        have = set(
            ValueSet.objects.filter(
                slug__in=system + [v["slug"] for v in clinic]
            ).values_list("slug", flat=True)
        )
        return {
            "options": {
                "valuesets": [
                    {
                        "slug": v["slug"],
                        "name": v["name"],
                        "description": v.get("description", ""),
                        "codes": sum(
                            len(i.get("concept", [])) for i in v["compose"]["include"]
                        ),
                        "exists": v["slug"] in have,
                    }
                    for v in clinic
                ]
            },
            "existing": {
                "system": len([s for s in system if s in have]),
                "system_total": len(system),
            },
            "defaults": {"system": True, "selected": [v["slug"] for v in clinic]},
        }

    def inventory(self, ctx: Context) -> dict:
        from care.emr.models import ValueSet

        return {
            "system_valueset": ValueSet.objects.filter(is_system_defined=True).count(),
            "clinic_valueset": ValueSet.objects.filter(
                slug__in=[v["slug"] for v in clinic_valuesets()]
            ).count(),
        }
