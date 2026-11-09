import logging
import time

from django.core.exceptions import ValidationError as DjangoValidationError
from django.db import IntegrityError, transaction
from django.http import Http404
from django.utils import timezone
from rest_framework import serializers, status
from rest_framework.exceptions import NotFound, PermissionDenied
from rest_framework.exceptions import ValidationError as DRFValidationError
from rest_framework.response import Response
from rest_framework.views import APIView

from care_pi_onboarding.api.throttling import (
    READ_SCOPE,
    WRITE_SCOPE,
    OnboardingThrottle,
)
from care_pi_onboarding.models import OnboardingStep, OnboardingWizard
from care_pi_onboarding.services import fixtures, registry
from care_pi_onboarding.services.access import is_facility_admin, managed_facilities
from care_pi_onboarding.services.base import Context, StepError, Tally
from care_pi_onboarding.services.demo_data import demo_allowed
from care_pi_onboarding.settings import plugin_settings

logger = logging.getLogger("care_pi_onboarding")


def _locale() -> dict:
    return fixtures.locale(str(plugin_settings.ONBOARDING_LOCALE))


def _facility(value):
    from care.facility.models import Facility

    if not value:
        return None
    try:
        facility = Facility.objects.filter(external_id=value).first()
    except DjangoValidationError as exc:
        raise NotFound("The facility does not exist.") from exc
    if facility is None:
        raise NotFound("The facility does not exist.")
    return facility


def _default_facility(user):
    from care.facility.models import Facility

    row = OnboardingStep.objects.filter(step="facility", scope="").first()
    facility_id = ((row.result if row else {}).get("facility") or {}).get("id")
    if facility_id:
        facility = Facility.objects.filter(external_id=facility_id).first()
        if facility and is_facility_admin(user, facility):
            return facility
    return managed_facilities(user).first()


def _scope(step, facility) -> str:
    if step.scope == "facility" and step.name != "facility" and facility is not None:
        return str(facility.external_id)
    return ""


def _context(user, step, facility) -> Context:
    return Context(
        user=user, facility=facility, scope=_scope(step, facility), locale=_locale()
    )


def _step_or_404(name):
    step = registry.get(name)
    if step is None:
        raise NotFound(f"There is no onboarding step {name}.")
    return step


def _allowed(step, user, facility, ignore_flags=False) -> bool:
    if step.name == "demo_data" and not demo_allowed() and not ignore_flags:
        return False
    if step.scope == "facility" and step.name != "facility" and facility is None:
        return False
    return step.authorize(user, facility if step.scope == "facility" else None)


def _authorize(step, user, facility, ignore_flags=False):
    if step.scope == "facility" and step.name != "facility" and facility is None:
        raise DRFValidationError({"facility": "This step needs the id of a facility."})
    if not _allowed(step, user, facility, ignore_flags):
        if step.name == "demo_data" and not demo_allowed() and not ignore_flags:
            raise PermissionDenied(
                "The demo data is off on this CARE (ONBOARDING_DEMO_ALLOWED)."
            )
        if step.scope == "instance":
            raise PermissionDenied("Only a CARE superuser can apply this step.")
        raise PermissionDenied(
            "Only a facility admin of this facility or a CARE superuser can apply this step."
        )


def _wizard(user, rows) -> dict:
    wizard = OnboardingWizard.objects.filter(pk=1).first()
    closed = bool(wizard and wizard.closed_reason)
    facility_done = any(r.step == "facility" and r.status == "done" for r in rows)
    return {
        "closed": closed,
        "reason": wizard.closed_reason if wizard else "",
        "redirect": bool(user.is_superuser and not closed and not facility_done),
    }


def _row_json(row) -> dict:
    if row is None:
        return {
            "state": "pending",
            "runs": 0,
            "applied_at": None,
            "created": {},
            "existing": {},
            "detail": "",
        }
    return {
        "state": row.status,
        "runs": row.runs,
        "applied_at": row.applied_at.isoformat() if row.applied_at else None,
        "created": row.created,
        "existing": row.existing,
        "detail": row.detail,
    }


def _record_failure(step, scope, user, detail):
    with transaction.atomic():
        row, _ = OnboardingStep.objects.get_or_create(step=step.name, scope=scope)
        row.status = "failed"
        row.detail = detail
        row.created = {}
        row.existing = {}
        row.runs += 1
        row.applied_by = user
        row.applied_at = timezone.now()
        row.save()


def _log(step, scope, state, tally, user, started):
    logger.info(
        "care_pi_onboarding step=%s scope=%s status=%s created=%s existing=%s user=%s ms=%d",
        step.name,
        scope or "-",
        state,
        sum(tally.created.values()),
        sum(tally.existing.values()),
        getattr(user, "username", "-"),
        (time.monotonic() - started) * 1000,
    )


def _failed(step, detail, errors=None, http_status=status.HTTP_400_BAD_REQUEST):
    return Response(
        {
            "step": step.name,
            "status": "failed",
            "created": {},
            "existing": {},
            "detail": detail,
            "errors": errors or [],
        },
        status=http_status,
    )


def _error_text(exc) -> tuple[str, list]:
    if isinstance(exc, StepError):
        return exc.detail, exc.errors
    if isinstance(exc, DRFValidationError):
        detail = exc.detail
        if isinstance(detail, dict) and "errors" in detail:
            messages = [
                str(e.get("msg", e)) if isinstance(e, dict) else str(e)
                for e in detail["errors"]
            ]
        elif isinstance(detail, dict):
            messages = [f"{k}: {v}" for k, v in detail.items()]
        elif isinstance(detail, list):
            messages = [str(d) for d in detail]
        else:
            messages = [str(detail)]
        return "; ".join(messages), messages
    if isinstance(exc, DjangoValidationError):
        return "; ".join(exc.messages), list(exc.messages)
    if isinstance(exc, Http404):
        return f"CARE cannot find an object: {exc}", [str(exc)]
    if isinstance(exc, IntegrityError):
        return (
            "CARE refused the change because of a conflict with existing data. Apply again.",
            [],
        )
    return str(exc), [str(exc)]


class OnboardingView(APIView):
    throttle_classes = [OnboardingThrottle]
    throttle_scope = READ_SCOPE


class StatusView(OnboardingView):
    def get(self, request):
        user = request.user
        facility = _facility(request.query_params.get("facility")) or _default_facility(
            user
        )
        if facility is not None and not is_facility_admin(user, facility):
            raise PermissionDenied("You are not an admin of this facility.")
        can_manage = user.is_superuser or facility is not None
        if not can_manage:
            return Response(
                {
                    "can_manage": False,
                    "is_superuser": False,
                    "facility": None,
                    "steps": [],
                    "next_step": None,
                    "wizard": {"closed": False, "reason": "", "redirect": False},
                }
            )
        steps = registry.steps()
        scopes = {"", str(facility.external_id)} if facility else {""}
        rows = list(OnboardingStep.objects.filter(scope__in=scopes))
        by_key = {(r.step, r.scope): r for r in rows}
        items = []
        next_step = None
        for step in steps:
            scope = _scope(step, facility)
            row = by_key.get((step.name, scope))
            ctx = _context(user, step, facility)
            allowed = _allowed(step, user, facility)
            item = {
                "step": step.name,
                "title": step.title,
                "description": step.description,
                "optional": step.optional,
                "scope": step.scope,
                "allowed": allowed,
                **_row_json(row),
                "exists": step.inventory(ctx) if allowed else {},
            }
            if step.name == "demo_data":
                item["available"] = demo_allowed()
            items.append(item)
            if next_step is None and allowed and item["state"] in ("pending", "failed"):
                next_step = step.name
        geo = ((by_key.get(("organization", "")) or OnboardingStep()).result or {}).get(
            "geo_organization"
        )
        return Response(
            {
                "can_manage": True,
                "is_superuser": user.is_superuser,
                "locale": _locale()["code"],
                "facility": {"id": str(facility.external_id), "name": facility.name}
                if facility
                else None,
                "geo_organization": geo,
                "steps": items,
                "next_step": next_step,
                "wizard": _wizard(user, [r for r in rows if r.scope == ""]),
            }
        )


class StepPreviewView(OnboardingView):
    def get(self, request, step):
        step_obj = _step_or_404(step)
        facility = _facility(request.query_params.get("facility"))
        if (
            facility is None
            and step_obj.scope == "facility"
            and step_obj.name != "facility"
        ):
            facility = _default_facility(request.user)
        _authorize(step_obj, request.user, facility)
        ctx = _context(request.user, step_obj, facility)
        row = OnboardingStep.objects.filter(step=step_obj.name, scope=ctx.scope).first()
        return Response(
            {
                "step": step_obj.name,
                "title": step_obj.title,
                "description": step_obj.description,
                "optional": step_obj.optional,
                **_row_json(row),
                "result": row.result if row else {},
                **step_obj.preview(ctx, request.query_params),
            }
        )


class StepApplyView(OnboardingView):
    throttle_scope = WRITE_SCOPE

    def post(self, request, step):
        step_obj = _step_or_404(step)
        facility = None
        if step_obj.scope == "facility":
            raw = (
                request.data.get("facility") if isinstance(request.data, dict) else None
            )
            facility = _facility(raw) if raw else None
        _authorize(step_obj, request.user, facility)
        ctx = _context(request.user, step_obj, facility)
        started = time.monotonic()
        tally = Tally()

        serializer = step_obj.input_serializer()(data=request.data)
        if not serializer.is_valid():
            errors = _flatten(serializer.errors)
            detail = "The input is not valid: " + "; ".join(errors)
            _record_failure(step_obj, ctx.scope, request.user, detail)
            _log(step_obj, ctx.scope, "failed", tally, request.user, started)
            return _failed(step_obj, detail, errors)
        try:
            with transaction.atomic():
                row, _ = OnboardingStep.objects.get_or_create(
                    step=step_obj.name, scope=ctx.scope
                )
                # One apply of a step at a time: the second call waits here, then finds the objects of the first.
                row = OnboardingStep.objects.select_for_update().get(pk=row.pk)
                result = step_obj.apply(ctx, serializer.validated_data, tally)
                row.status = "done"
                row.created = tally.created
                row.existing = tally.existing
                row.detail = result.detail
                row.result = result.data
                row.runs += 1
                row.applied_by = request.user
                row.applied_at = timezone.now()
                row.save()
        except PermissionDenied:
            _log(step_obj, ctx.scope, "denied", tally, request.user, started)
            raise
        except (
            StepError,
            DRFValidationError,
            DjangoValidationError,
            Http404,
            IntegrityError,
        ) as exc:
            detail, errors = _error_text(exc)
            _record_failure(step_obj, ctx.scope, request.user, detail)
            _log(step_obj, ctx.scope, "failed", Tally(), request.user, started)
            return _failed(step_obj, detail, errors)
        except Exception as exc:
            logger.exception("care_pi_onboarding step=%s failed", step_obj.name)
            _record_failure(
                step_obj,
                ctx.scope,
                request.user,
                f"Internal error ({type(exc).__name__}). See the CARE log.",
            )
            raise
        _log(step_obj, ctx.scope, "done", tally, request.user, started)
        body = {
            "step": step_obj.name,
            "status": "done",
            "created": tally.created,
            "existing": tally.existing,
            "detail": result.detail,
            "result": result.data,
        }
        if result.secrets:
            body["secrets"] = result.secrets
        return Response(body)


class StepSkipView(OnboardingView):
    throttle_scope = WRITE_SCOPE

    def post(self, request, step):
        step_obj = _step_or_404(step)
        facility = (
            _facility(request.data.get("facility"))
            if step_obj.scope == "facility"
            else None
        )
        if not step_obj.optional:
            return _failed(
                step_obj, f"The step {step_obj.title} is not optional. Apply it."
            )
        # A step that a setting turned off can still be skipped, so the wizard can finish.
        _authorize(step_obj, request.user, facility, ignore_flags=True)
        ctx = _context(request.user, step_obj, facility)
        with transaction.atomic():
            row, _ = OnboardingStep.objects.select_for_update().get_or_create(
                step=step_obj.name, scope=ctx.scope
            )
            row.status = "skipped"
            row.detail = f"{request.user.username} skipped this step."
            row.created = {}
            row.existing = {}
            row.applied_by = request.user
            row.applied_at = timezone.now()
            row.save()
        _log(step_obj, ctx.scope, "skipped", Tally(), request.user, time.monotonic())
        return Response(
            {
                "step": step_obj.name,
                "status": "skipped",
                "created": {},
                "existing": {},
                "detail": row.detail,
            }
        )


class DemoDataView(OnboardingView):
    throttle_scope = WRITE_SCOPE

    def delete(self, request):
        step_obj = _step_or_404("demo_data")
        facility = _facility(
            request.query_params.get("facility") or request.data.get("facility")
        )
        if facility is None:
            raise DRFValidationError(
                {"facility": "Give the id of the facility in ?facility=."}
            )
        if not is_facility_admin(request.user, facility):
            raise PermissionDenied(
                "Only a facility admin of this facility or a CARE superuser can remove demo data."
            )
        ctx = _context(request.user, step_obj, facility)
        started = time.monotonic()
        with transaction.atomic():
            removed = step_obj.remove(ctx)
            OnboardingStep.objects.filter(step=step_obj.name, scope=ctx.scope).update(
                status="pending",
                detail="The demo data was removed.",
                created={},
                existing={},
                result={},
            )
        tally = Tally()
        _log(step_obj, ctx.scope, "removed", tally, request.user, started)
        return Response(
            {
                "step": step_obj.name,
                "status": "removed",
                "removed": removed,
                "detail": f"CARE removed {removed['patient']} demo patients and {removed['encounter']} visits.",
            }
        )


class WizardCloseInput(serializers.Serializer):
    reason = serializers.ChoiceField(choices=["finished", "later"])


class WizardCloseView(OnboardingView):
    throttle_scope = WRITE_SCOPE

    def post(self, request):
        if (
            not request.user.is_superuser
            and not managed_facilities(request.user).exists()
        ):
            raise PermissionDenied("Only an administrator can close the clinic setup.")
        serializer = WizardCloseInput(data=request.data)
        serializer.is_valid(raise_exception=True)
        wizard, _ = OnboardingWizard.objects.get_or_create(pk=1)
        wizard.closed_reason = serializer.validated_data["reason"]
        wizard.closed_by = request.user
        wizard.closed_at = timezone.now()
        wizard.save()
        return Response(
            {
                "wizard": {
                    "closed": True,
                    "reason": wizard.closed_reason,
                    "redirect": False,
                }
            }
        )


def _flatten(errors, prefix="") -> list[str]:
    messages = []
    if isinstance(errors, dict):
        for key, value in errors.items():
            name = "" if key == "non_field_errors" else key
            messages.extend(
                _flatten(value, f"{prefix}.{name}".strip(".") if name else prefix)
            )
    elif isinstance(errors, list):
        for index, value in enumerate(errors):
            if isinstance(value, dict | list):
                messages.extend(_flatten(value, f"{prefix}[{index}]"))
            else:
                messages.append(f"{prefix}: {value}" if prefix else str(value))
    else:
        messages.append(f"{prefix}: {errors}" if prefix else str(errors))
    return messages
