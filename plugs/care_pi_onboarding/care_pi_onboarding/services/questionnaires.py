from rest_framework import serializers

from care_pi_onboarding.services import fixtures
from care_pi_onboarding.services.base import (
    Context,
    Step,
    StepResult,
    Tally,
    save_new,
    validate_spec,
)

STEP = "questionnaires"
FIXTURE_ONLY_KEYS = {"departments"}


def clinic_questionnaires() -> list[dict]:
    return fixtures.load("questionnaires.json")["questionnaires"]


class QuestionnairesInput(serializers.Serializer):
    facility = serializers.UUIDField()
    selected = serializers.ListField(
        child=serializers.CharField(max_length=255), required=False
    )

    def validate_selected(self, value):
        known = {q["slug"] for q in clinic_questionnaires()}
        unknown = [s for s in value if s not in known]
        if unknown:
            raise serializers.ValidationError(
                f"Unknown questionnaires: {', '.join(unknown)}."
            )
        return value


def _existing(facility, slugs):
    from care.emr.models import Questionnaire

    return {
        q.slug: q
        for q in Questionnaire.objects.filter(
            facility=facility,
            auth_context="facility",
            latest_revision__isnull=True,
            slug__in=slugs,
        )
    }


def _departments(facility, wanted):
    from care.emr.models.organization import FacilityOrganization

    queryset = FacilityOrganization.objects.filter(facility=facility).exclude(
        org_type="root"
    )
    if wanted != "*":
        names = {n.lower() for n in wanted}
        return [o for o in queryset if o.name.lower() in names]
    return list(queryset)


def _link_departments(ctx: Context, tally: Tally, questionnaire, departments):
    from care.emr.models.questionnaire import QuestionnaireFacilityOrganization

    have = set(
        QuestionnaireFacilityOrganization.objects.filter(
            questionnaire=questionnaire
        ).values_list("organization_id", flat=True)
    )
    new = [d for d in departments if d.id not in have]
    tally.found("questionnaire_department", len(departments) - len(new))
    for org in new:
        save_new(
            QuestionnaireFacilityOrganization(
                questionnaire=questionnaire, organization=org
            ),
            ctx.user,
        )
    if new:
        tally.made("questionnaire_department", len(new))
    # The read access of department staff comes from this cache (CARE: set_facility_organizations).
    questionnaire.sync_facility_org_cache()


class QuestionnairesStep(Step):
    name = STEP
    title = "Questionnaires"
    description = "The forms of the clinic: vitals, consultation note, triage, and pharmacy intake."
    scope = "facility"

    def input_serializer(self):
        return QuestionnairesInput

    def apply(self, ctx: Context, data: dict, tally: Tally) -> StepResult:
        from care.emr.resources.questionnaire.spec import QuestionnaireCreateSpec

        facility = ctx.facility
        selected = data.get("selected")
        chosen = [
            q
            for q in clinic_questionnaires()
            if selected is None or q["slug"] in selected
        ]
        existing = _existing(facility, [q["slug"] for q in chosen])
        made = {}
        for entry in chosen:
            questionnaire = existing.get(entry["slug"])
            if questionnaire:
                tally.found("questionnaire")
            else:
                payload = {k: v for k, v in entry.items() if k not in FIXTURE_ONLY_KEYS}
                spec = validate_spec(
                    QuestionnaireCreateSpec,
                    {
                        **payload,
                        "auth_context": "facility",
                        "facility": str(facility.external_id),
                    },
                    f"Questionnaire {entry['slug']}",
                    {"user": ctx.user},
                )
                questionnaire = save_new(spec.de_serialize(), ctx.user)
                ctx.record(
                    STEP,
                    questionnaire,
                    f"questionnaire:{facility.external_id}:{entry['slug']}",
                )
                tally.made("questionnaire")
            _link_departments(
                ctx,
                tally,
                questionnaire,
                _departments(facility, entry.get("departments", "*")),
            )
            made[entry["slug"]] = str(questionnaire.external_id)
        return StepResult(
            detail=f"The facility has {len(made)} clinic questionnaires.",
            data={"questionnaires": made},
        )

    def preview(self, ctx: Context, params) -> dict:
        clinic = clinic_questionnaires()
        have = (
            _existing(ctx.facility, [q["slug"] for q in clinic]) if ctx.facility else {}
        )
        return {
            "options": {
                "questionnaires": [
                    {
                        "slug": q["slug"],
                        "title": q["title"],
                        "description": q.get("description", ""),
                        "questions": _count(q["questions"]),
                        "departments": q.get("departments", "*"),
                        "exists": q["slug"] in have,
                    }
                    for q in clinic
                ]
            },
            "defaults": {"selected": [q["slug"] for q in clinic]},
        }

    def inventory(self, ctx: Context) -> dict:
        if ctx.facility is None:
            return {}
        return {
            "questionnaire": len(
                _existing(ctx.facility, [q["slug"] for q in clinic_questionnaires()])
            )
        }


def _count(questions) -> int:
    return sum(
        _count(q.get("questions", [])) if q["type"] == "group" else 1 for q in questions
    )
