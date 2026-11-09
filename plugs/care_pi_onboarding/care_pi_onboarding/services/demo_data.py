from django.db import transaction
from rest_framework import serializers
from rest_framework.exceptions import PermissionDenied

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
from care_pi_onboarding.settings import plugin_settings

STEP = "demo_data"


def demo() -> dict:
    return fixtures.load("demo.json")


def demo_allowed() -> bool:
    value = plugin_settings.ONBOARDING_DEMO_ALLOWED
    if isinstance(value, str):
        return value.strip().lower() in {"1", "true", "yes", "on"}
    return bool(value)


class DemoInput(serializers.Serializer):
    facility = serializers.UUIDField()


def _acquire(lock, busy: str, held: list):
    """Take a CARE create lock; the caller releases each lock in held."""
    from care.utils.lock import ObjectLocked

    try:
        lock.acquire()
    except ObjectLocked as exc:
        raise StepError(busy) from exc
    held.append(lock)


def _records(ctx: Context, model_label: str) -> dict[str, int]:
    return dict(
        OnboardingRecord.objects.filter(
            step=STEP, scope=ctx.scope, model=model_label
        ).values_list("key", "object_id")
    )


def _department(facility, name: str):
    from care.emr.models.organization import FacilityOrganization

    return FacilityOrganization.objects.filter(
        facility=facility, name__iexact=name
    ).first() or FacilityOrganization.objects.get(facility=facility, org_type="root")


def _create_patient(ctx: Context, entry: dict, facility):
    from care.emr.resources.patient.spec import PatientCreateSpec
    from care.emr.resources.patient_identifier.default_expression_evaluator import (
        evaluate_patient_instance_default_values,
    )

    if facility.geo_organization is None:
        raise StepError(
            "The facility has no government organisation. Apply the step facility again."
        )
    spec = validate_spec(
        PatientCreateSpec,
        {
            "name": entry["name"],
            "gender": entry["gender"],
            "phone_number": entry["phone_number"],
            "age": entry["age"],
            "blood_group": entry.get("blood_group"),
            "address": "Demo address",
            "geo_organization": str(facility.geo_organization.external_id),
        },
        f"Patient {entry['name']}",
    )
    patient = save_new(spec.de_serialize(), ctx.user)
    # The rest of PatientViewSet.perform_create: default identifiers and the identifier cache.
    evaluate_patient_instance_default_values(patient)
    patient.build_instance_identifiers()
    patient.save()
    return patient


def _create_encounter(ctx: Context, patient, facility, department):
    from care.emr.models.encounter import EncounterOrganization
    from care.emr.resources.encounter.spec import EncounterCreateSpec
    from care.emr.resources.patient_identifier.default_expression_evaluator import (
        evaluate_patient_facility_default_values,
    )

    settings = demo()["encounter"]
    spec = validate_spec(
        EncounterCreateSpec,
        {
            **settings,
            "patient": str(patient.external_id),
            "facility": str(facility.external_id),
            "organizations": [str(department.external_id)],
        },
        f"Visit of {patient.name}",
    )
    encounter = save_new(spec.de_serialize(), ctx.user)
    # The rest of EncounterViewSet.perform_create.
    save_new(
        EncounterOrganization(encounter=encounter, organization=department), ctx.user
    )
    evaluate_patient_facility_default_values(patient, facility)
    patient.build_facility_identifiers(facility.id)
    patient.save(update_fields=["facility_identifiers"])
    return encounter


class DemoDataStep(Step):
    name = STEP
    title = "Demo patients"
    description = "Five synthetic patients with an open visit, to try CARE. You can remove them later."
    optional = True
    scope = "facility"

    def input_serializer(self):
        return DemoInput

    def apply(self, ctx: Context, data: dict, tally: Tally) -> StepResult:
        from care.emr.locks.billing import (
            FacilityEncounterCreateLock,
            PatientCreateLock,
        )
        from care.emr.models import Encounter, Patient

        if not demo_allowed():
            raise PermissionDenied(
                "The demo data is off on this CARE (ONBOARDING_DEMO_ALLOWED)."
            )
        facility = ctx.facility
        fixture = demo()
        department = _department(facility, fixture["department"])
        patient_ids = _records(ctx, Patient._meta.label)
        encounter_ids = _records(ctx, Encounter._meta.label)
        live_patients = {
            p.id: p for p in Patient.objects.filter(id__in=patient_ids.values())
        }
        live_encounters = set(
            Encounter.objects.filter(id__in=encounter_ids.values()).values_list(
                "id", flat=True
            )
        )

        missing = [
            e
            for e in fixture["patients"]
            if patient_ids.get(e["key"]) not in live_patients
        ]
        need = [
            e["key"]
            for e in fixture["patients"]
            if encounter_ids.get(f"{e['key']}:encounter") not in live_encounters
        ]
        tally.found("patient", len(fixture["patients"]) - len(missing))
        tally.found("encounter", len(fixture["patients"]) - len(need))

        # The CARE viewsets hold these locks until the commit and release them on any error
        # (PatientViewSet.perform_create). One try spans the whole work: a rollback discards the
        # on_commit callbacks, so a lock registered there would stay set until LOCK_TIMEOUT.
        held = []
        try:
            if missing:
                _acquire(
                    PatientCreateLock(),
                    "CARE is registering another patient now. Apply again in a minute.",
                    held,
                )
            if need:
                _acquire(
                    FacilityEncounterCreateLock(facility.id),
                    "CARE is starting another visit in this facility now. Apply again in a minute.",
                    held,
                )
            patients = {}
            for entry in missing:
                OnboardingRecord.objects.filter(
                    step=STEP, scope=ctx.scope, key=entry["key"]
                ).delete()
                patient = _create_patient(ctx, entry, facility)
                OnboardingRecord.objects.create(
                    step=STEP,
                    scope=ctx.scope,
                    model=Patient._meta.label,
                    object_id=patient.id,
                    key=entry["key"],
                )
                tally.made("patient")
                patients[entry["key"]] = patient
            for entry in fixture["patients"]:
                patients.setdefault(
                    entry["key"], live_patients.get(patient_ids.get(entry["key"]))
                )
            for key in need:
                OnboardingRecord.objects.filter(
                    step=STEP, scope=ctx.scope, key=f"{key}:encounter"
                ).delete()
                encounter = _create_encounter(ctx, patients[key], facility, department)
                OnboardingRecord.objects.create(
                    step=STEP,
                    scope=ctx.scope,
                    model=Encounter._meta.label,
                    object_id=encounter.id,
                    key=f"{key}:encounter",
                )
                tally.made("encounter")
        except BaseException:
            for lock in held:
                lock.release()
            raise
        for lock in held:
            transaction.on_commit(lock.release)
        return StepResult(
            detail=f"The facility has {len(patients)} demo patients, each with an open visit in {department.name}.",
            data={"patients": [str(p.external_id) for p in patients.values()]},
        )

    def remove(self, ctx: Context) -> dict:
        """Soft-delete the demo patients and visits, as CARE deletes them."""
        from care.emr.models import Encounter, Patient

        counts = {"encounter": 0, "patient": 0}
        for model, kind in ((Encounter, "encounter"), (Patient, "patient")):
            ids = _records(ctx, model._meta.label).values()
            for obj in model.objects.filter(id__in=ids):
                obj.deleted = True
                obj.updated_by = ctx.user
                obj.save(update_fields=["deleted", "updated_by", "modified_date"])
                counts[kind] += 1
        OnboardingRecord.objects.filter(step=STEP, scope=ctx.scope).delete()
        return counts

    def preview(self, ctx: Context, params) -> dict:
        fixture = demo()
        return {
            "options": {
                "patients": [
                    {"key": p["key"], "name": p["name"], "age": p["age"]}
                    for p in fixture["patients"]
                ],
                "department": fixture["department"],
            },
            "allowed": demo_allowed(),
            "existing": self.inventory(ctx),
        }

    def inventory(self, ctx: Context) -> dict:
        from care.emr.models import Encounter, Patient

        if ctx.facility is None:
            return {}
        return {
            "patient": Patient.objects.filter(
                id__in=_records(ctx, Patient._meta.label).values()
            ).count(),
            "encounter": Encounter.objects.filter(
                id__in=_records(ctx, Encounter._meta.label).values()
            ).count(),
        }
