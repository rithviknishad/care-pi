from django.conf import settings
from rest_framework import serializers

from care_pi_onboarding.models import OnboardingRecord
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

STEP = "organization"


class OrganizationInput(serializers.Serializer):
    state = serializers.CharField(required=False, max_length=255)
    district = serializers.CharField(required=False, allow_blank=True, max_length=255)
    local_body = serializers.CharField(required=False, allow_blank=True, max_length=255)
    parent = serializers.UUIDField(required=False)
    all_districts = serializers.BooleanField(default=False)

    def validate(self, attrs):
        state, parent = attrs.get("state"), attrs.get("parent")
        if not state and not parent:
            raise serializers.ValidationError(
                "Give a state, or the id of a government organisation in parent."
            )
        if state and parent:
            raise serializers.ValidationError("Give a state or a parent, not both.")
        if parent and attrs.get("all_districts"):
            raise serializers.ValidationError(
                "all_districts needs a state from the locale fixture."
            )
        if attrs.get("local_body") and not attrs.get("district") and not parent:
            raise serializers.ValidationError("A local body needs a district.")
        return attrs


def _metadata(loc: dict, level: str) -> dict:
    levels = loc.get("levels", {})
    children = levels.get(level, {}).get("children")
    data = {"country": loc.get("country", ""), "govt_org_type": level}
    if children:
        data["govt_org_children_type"] = children
    return data


def _child_level(loc: dict, parent) -> str:
    level = (parent.metadata or {}).get("govt_org_children_type")
    if level:
        return level
    names = list(loc.get("levels", {}))
    index = min(parent.level_cache + 1, len(names) - 1) if names else 0
    return names[index] if names else "local_body"


def _ensure(ctx: Context, tally: Tally, name: str, parent, level: str, aliases=()):
    """The organisation with this name (or one of its aliases, in that order) under parent, made if missing."""
    from care.emr.models.organization import Organization

    queryset = Organization.objects.filter(org_type="govt")
    queryset = (
        queryset.filter(parent=parent)
        if parent
        else queryset.filter(parent__isnull=True)
    )
    for candidate in [name, *aliases]:
        found = queryset.filter(name__iexact=candidate.strip()).first()
        if found:
            tally.found("govt_organization")
            return found
    return _create(ctx, tally, name, parent, level)


def _create(ctx: Context, tally: Tally, name: str, parent, level: str):
    from care.emr.resources.organization.spec import OrganizationWriteSpec

    if parent and parent.level_cache >= settings.ORGANIZATION_MAX_DEPTH:
        raise StepError(
            f"The organisation {parent.name} is at the largest depth "
            f"({settings.ORGANIZATION_MAX_DEPTH})."
        )
    spec = validate_spec(
        OrganizationWriteSpec,
        {
            "name": name.strip(),
            "org_type": "govt",
            "active": True,
            "description": "",
            "metadata": _metadata(ctx.locale, level),
            "parent": str(parent.external_id) if parent else None,
        },
        f"Organisation {name}",
    )
    obj = save_new(spec.de_serialize(), ctx.user)
    ctx.record(STEP, obj, f"govt:{obj.level_cache}:{obj.name}")
    tally.made("govt_organization")
    return obj


def _ensure_many(ctx: Context, tally: Tally, parent, names: list[str], level: str):
    """Make the missing children of parent in one INSERT. The caches are those of Organization.save."""
    from care.emr.models.organization import Organization

    existing = {
        o.name.strip().lower(): o
        for o in Organization.objects.filter(org_type="govt", parent=parent)
    }
    missing = [n for n in names if n.strip().lower() not in existing]
    tally.found("govt_organization", len(names) - len(missing))
    if not missing:
        return existing
    metadata = _metadata(ctx.locale, level)
    objs = [
        Organization(
            name=name.strip(),
            org_type="govt",
            active=True,
            description="",
            metadata=metadata,
            parent=parent,
            parent_cache=[*parent.parent_cache, parent.id],
            level_cache=parent.level_cache + 1,
            root_org=parent.root_org or parent,
            created_by=ctx.user,
            updated_by=ctx.user,
        )
        for name in missing
    ]
    Organization.objects.bulk_create(objs)
    OnboardingRecord.objects.bulk_create(
        [
            OnboardingRecord(
                step=STEP,
                scope=ctx.scope,
                model=Organization._meta.label,
                object_id=o.pk,
                key=f"govt:{o.level_cache}:{o.name}",
            )
            for o in objs
        ],
        ignore_conflicts=True,
    )
    if not parent.has_children:
        parent.has_children = True
        parent.save(update_fields=["has_children"])
    tally.made("govt_organization", len(objs))
    existing.update({o.name.lower(): o for o in objs})
    return existing


def _path(org) -> list[str]:
    from care.emr.models.organization import Organization

    names = dict(
        Organization.objects.filter(id__in=org.parent_cache).values_list("id", "name")
    )
    return [names.get(i, "") for i in org.parent_cache] + [org.name]


def org_json(org) -> dict:
    return {"id": str(org.external_id), "name": org.name, "path": _path(org)}


class OrganizationStep(Step):
    name = STEP
    title = "Area"
    description = (
        "The government organisations (state, district, local body) of the clinic."
    )
    scope = "instance"

    def input_serializer(self):
        return OrganizationInput

    def _state(self, ctx: Context, name: str) -> dict:
        state = fixtures.states(ctx.locale).get(name.strip().lower())
        if not state:
            raise StepError(
                f"The state {name} is not in the locale fixture {ctx.locale['code']}."
            )
        return state

    def _district_name(self, state: dict, name: str) -> str:
        index = {d.lower(): d for d in state["districts"]}
        canonical = index.get(name.strip().lower())
        if not canonical:
            raise StepError(f"The district {name} is not in {state['name']}.")
        return canonical

    def apply(self, ctx: Context, data: dict, tally: Tally) -> StepResult:
        from care.emr.models.organization import Organization

        district = (data.get("district") or "").strip()
        local_body = (data.get("local_body") or "").strip()

        if data.get("parent"):
            base = Organization.objects.filter(
                external_id=data["parent"], org_type="govt"
            ).first()
            if not base:
                raise StepError(
                    "The parent is not a government organisation of this CARE."
                )
            tally.found("govt_organization")
            leaf = base
            if district:
                leaf = _ensure(
                    ctx, tally, district, base, _child_level(ctx.locale, base)
                )
        else:
            state = self._state(ctx, data["state"])
            base = _ensure(
                ctx, tally, state["name"], None, "state", state.get("aliases", [])
            )
            leaf = base
            names = []
            if data.get("all_districts"):
                names = list(state["districts"])
            if district:
                district = self._district_name(state, district)
                if district not in names:
                    names.append(district)
            if names:
                children = _ensure_many(ctx, tally, base, names, "district")
                if district:
                    leaf = children[district.lower()]
        if local_body:
            leaf = _ensure(ctx, tally, local_body, leaf, _child_level(ctx.locale, leaf))

        geo = org_json(leaf)
        return StepResult(
            detail=f"The clinic area is {' > '.join(geo['path'])}.",
            data={"geo_organization": geo, "root_organization": org_json(base)},
        )

    def preview(self, ctx: Context, params) -> dict:
        from care.emr.models.organization import Organization

        # A root with an alias name (for example "Delhi (NCT)") shows as its state of the fixture.
        canonical = {
            alias.lower(): s["name"]
            for s in ctx.locale.get("states", [])
            for alias in s.get("aliases", [])
        }

        def state_name(name: str) -> str:
            return canonical.get(name.lower(), name)

        roots = [
            state_name(n)
            for n in Organization.objects.filter(org_type="govt", parent__isnull=True)
            .order_by("name")
            .values_list("name", flat=True)[:500]
        ]
        children: dict[str, list[str]] = {}
        for parent_name, name in (
            Organization.objects.filter(
                org_type="govt", level_cache=1, parent__parent__isnull=True
            )
            .order_by("name")
            .values_list("parent__name", "name")[:5000]
        ):
            children.setdefault(state_name(parent_name), []).append(name)
        previous = ctx.previous(STEP)
        geo = previous.get("geo_organization") or {}
        path = geo.get("path") or []
        return {
            "options": {
                "country": ctx.locale.get("country", ""),
                "states": [
                    {
                        "name": s["name"],
                        "kind": s.get("kind", "state"),
                        "districts": s["districts"],
                    }
                    for s in ctx.locale.get("states", [])
                ],
            },
            "existing": {"states": roots, "districts": children},
            "defaults": {
                "state": state_name(path[0]) if path else "",
                "district": path[1] if len(path) > 1 else "",
                "local_body": path[2] if len(path) > 2 else "",
            },
            "current": geo or None,
        }

    def inventory(self, ctx: Context) -> dict:
        from care.emr.models.organization import Organization

        return {
            "govt_organization": Organization.objects.filter(org_type="govt").count()
        }
