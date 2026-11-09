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

STEP = "catalog"


def medications() -> list[dict]:
    return fixtures.load("catalog.json")["medications"]


class CatalogInput(serializers.Serializer):
    facility = serializers.UUIDField()
    selected = serializers.ListField(
        child=serializers.CharField(max_length=255), required=False
    )

    def validate_selected(self, value):
        known = {m["slug_value"] for m in medications()}
        unknown = [s for s in value if s not in known]
        if unknown:
            raise serializers.ValidationError(
                f"Unknown catalog items: {', '.join(unknown)}."
            )
        return value


def _slug(facility, slug_value: str) -> str:
    from care.emr.models.product_knowledge import ProductKnowledge

    return ProductKnowledge.calculate_slug_from_facility(
        facility.external_id, slug_value
    )


def _existing(facility, slug_values):
    from care.emr.models.product_knowledge import ProductKnowledge

    slugs = {_slug(facility, s).lower(): s for s in slug_values}
    return {
        slugs[p.slug.lower()]: p
        for p in ProductKnowledge.objects.filter(facility=facility)
        if p.slug.lower() in slugs
    }


class CatalogStep(Step):
    name = STEP
    title = "Medicines"
    description = "A starter list of common medicines for the pharmacy. CARE checks the codes on its terminology server."
    optional = True
    scope = "facility"

    def input_serializer(self):
        return CatalogInput

    def apply(self, ctx: Context, data: dict, tally: Tally) -> StepResult:
        import requests
        from care.emr.resources.inventory.product_knowledge.spec import (
            ProductKnowledgeWriteSpec,
        )

        facility = ctx.facility
        selected = data.get("selected")
        chosen = [
            m for m in medications() if selected is None or m["slug_value"] in selected
        ]
        existing = _existing(facility, [m["slug_value"] for m in chosen])
        items = {}
        for entry in chosen:
            product = existing.get(entry["slug_value"])
            if product:
                tally.found("medication")
            else:
                try:
                    spec = validate_spec(
                        ProductKnowledgeWriteSpec,
                        {**entry, "facility": str(facility.external_id)},
                        f"Medicine {entry['name']}",
                    )
                except requests.RequestException as exc:
                    raise StepError(
                        "CARE cannot reach its terminology server to check the medicine codes. "
                        "Connect the device to the internet and apply again, or skip this step."
                    ) from exc
                product = spec.de_serialize()
                # The slug rule of the CARE viewset: f-<facility external id>-<slug>.
                product.slug = _slug(facility, product.slug)
                save_new(product, ctx.user)
                ctx.record(STEP, product, f"medication:{product.slug}")
                tally.made("medication")
            items[entry["slug_value"]] = str(product.external_id)
        return StepResult(
            detail=f"The pharmacy catalog has {len(items)} starter medicines.",
            data={"medications": items},
        )

    def preview(self, ctx: Context, params) -> dict:
        have = (
            _existing(ctx.facility, [m["slug_value"] for m in medications()])
            if ctx.facility
            else {}
        )
        return {
            "options": {
                "medications": [
                    {
                        "slug": m["slug_value"],
                        "name": m["name"],
                        "exists": m["slug_value"] in have,
                    }
                    for m in medications()
                ]
            },
            "defaults": {"selected": [m["slug_value"] for m in medications()]},
            "requires_internet": True,
        }

    def inventory(self, ctx: Context) -> dict:
        if ctx.facility is None:
            return {}
        return {
            "medication": len(
                _existing(ctx.facility, [m["slug_value"] for m in medications()])
            )
        }
