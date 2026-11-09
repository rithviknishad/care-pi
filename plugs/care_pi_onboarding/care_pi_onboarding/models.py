from django.conf import settings
from django.db import models


class OnboardingStep(models.Model):
    """
    The state of one onboarding step. One row for each step and scope.
    scope is "" for an instance step, or the external id of the facility for a facility step.
    apply locks this row (select_for_update), so two calls of the same step run one after the other.
    """

    step = models.CharField(max_length=64)
    scope = models.CharField(max_length=64, blank=True, default="")
    status = models.CharField(max_length=16, default="pending")
    created = models.JSONField(default=dict)
    existing = models.JSONField(default=dict)
    detail = models.TextField(blank=True, default="")
    # The ids that later steps need. Never a password.
    result = models.JSONField(default=dict)
    runs = models.PositiveIntegerField(default=0)
    applied_by = models.ForeignKey(
        settings.AUTH_USER_MODEL,
        on_delete=models.SET_NULL,
        null=True,
        blank=True,
        related_name="+",
    )
    applied_at = models.DateTimeField(null=True, blank=True)

    class Meta:
        constraints = [
            models.UniqueConstraint(
                fields=["step", "scope"], name="care_pi_onboarding_step_scope"
            )
        ]

    def __str__(self):
        return f"{self.step}[{self.scope}]={self.status}"


class OnboardingRecord(models.Model):
    """
    One object that a step made. The demo data removal and the status use these rows.
    """

    step = models.CharField(max_length=64)
    scope = models.CharField(max_length=64, blank=True, default="")
    model = models.CharField(max_length=128)
    object_id = models.BigIntegerField()
    key = models.CharField(max_length=512)
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        constraints = [
            models.UniqueConstraint(
                fields=["model", "object_id"], name="care_pi_onboarding_record_object"
            )
        ]
        indexes = [models.Index(fields=["step", "scope"])]

    def __str__(self):
        return f"{self.step}:{self.model}:{self.object_id}"


class OnboardingWizard(models.Model):
    """
    One row (id 1): the wizard closed for good. After that, the dashboard does not open the wizard.
    """

    closed_reason = models.CharField(max_length=16, blank=True, default="")
    closed_by = models.ForeignKey(
        settings.AUTH_USER_MODEL,
        on_delete=models.SET_NULL,
        null=True,
        blank=True,
        related_name="+",
    )
    closed_at = models.DateTimeField(null=True, blank=True)

    def __str__(self):
        return self.closed_reason or "open"
