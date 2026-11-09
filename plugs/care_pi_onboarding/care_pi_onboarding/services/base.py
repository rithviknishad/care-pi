import logging
from dataclasses import dataclass, field

from django.db import transaction
from pydantic import ValidationError as PydanticValidationError

logger = logging.getLogger("care_pi_onboarding")


class StepError(Exception):
    """The step cannot finish. The view rolls back the transaction and answers 400."""

    def __init__(self, detail: str, errors: list | None = None):
        super().__init__(detail)
        self.detail = detail
        self.errors = errors or []


class Tally:
    """The counts of one apply: what the step made and what it found."""

    def __init__(self):
        self.created: dict[str, int] = {}
        self.existing: dict[str, int] = {}

    def made(self, kind: str, n: int = 1):
        self.created[kind] = self.created.get(kind, 0) + n
        self.existing.setdefault(kind, 0)

    def found(self, kind: str, n: int = 1):
        self.existing[kind] = self.existing.get(kind, 0) + n
        self.created.setdefault(kind, 0)

    def count(self, kind: str, created: bool, n: int = 1):
        if created:
            self.made(kind, n)
        else:
            self.found(kind, n)

    @property
    def total_created(self) -> int:
        return sum(self.created.values())


@dataclass
class StepResult:
    detail: str
    # Persisted in OnboardingStep.result: ids for later steps and the wizard.
    data: dict = field(default_factory=dict)
    # Returned once in the response and never stored (for example initial passwords).
    secrets: dict = field(default_factory=dict)


@dataclass
class Context:
    user: object
    facility: object | None
    scope: str
    locale: dict

    def record(self, step: str, obj, key: str):
        from care_pi_onboarding.models import OnboardingRecord

        OnboardingRecord.objects.get_or_create(
            model=obj._meta.label,
            object_id=obj.pk,
            defaults={"step": step, "scope": self.scope, "key": key[:512]},
        )

    def previous(self, step: str, scope: str | None = None) -> dict:
        from care_pi_onboarding.models import OnboardingStep

        # result changes only when an apply succeeds, so a later failed run keeps it.
        row = OnboardingStep.objects.filter(
            step=step, scope=self.scope if scope is None else scope
        ).first()
        return row.result if row else {}


class Step:
    name = ""
    title = ""
    description = ""
    optional = False
    # "instance": only a superuser. "facility": a superuser or a facility admin of the facility.
    scope = "instance"

    def input_serializer(self):
        raise NotImplementedError

    def authorize(self, user, facility) -> bool:
        from care_pi_onboarding.services.access import is_facility_admin

        if user.is_superuser:
            return True
        if self.scope == "facility" and facility is not None:
            return is_facility_admin(user, facility)
        return False

    def preview(self, ctx: Context, params) -> dict:
        raise NotImplementedError

    def apply(self, ctx: Context, data: dict, tally: Tally) -> StepResult:
        raise NotImplementedError

    def inventory(self, ctx: Context) -> dict:
        return {}


def spec_errors(exc: PydanticValidationError) -> list[str]:
    messages = []
    for error in exc.errors():
        loc = ".".join(str(part) for part in error.get("loc", ()) if part != "__root__")
        msg = str(error.get("msg", "")).removeprefix("Value error, ")
        messages.append(f"{loc}: {msg}" if loc else msg)
    return messages


def validate_spec(spec_cls, data: dict, label: str, context: dict | None = None):
    """Validate data with the CARE resource spec, the same validation as the CARE API."""
    try:
        return spec_cls.model_validate(
            data, context={"is_create": True, **(context or {})}
        )
    except PydanticValidationError as exc:
        messages = spec_errors(exc)
        raise StepError(f"{label}: {'; '.join(messages)}", errors=messages) from exc
    except ValueError as exc:
        raise StepError(f"{label}: {exc}", errors=[str(exc)]) from exc


def save_new(obj, user):
    """Save a new CARE object as its viewset does: created_by and updated_by are the caller."""
    if hasattr(obj, "created_by_id"):
        obj.created_by = user
    if hasattr(obj, "updated_by_id"):
        obj.updated_by = user
    with transaction.atomic():
        obj.save()
    return obj


def iexact_index(names) -> dict[str, str]:
    return {name.strip().lower(): name for name in names}
