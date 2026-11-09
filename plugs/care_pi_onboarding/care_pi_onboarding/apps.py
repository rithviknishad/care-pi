from django.apps import AppConfig
from django.utils.translation import gettext_lazy as _

PLUGIN_NAME = "care_pi_onboarding"


class CarePiOnboardingConfig(AppConfig):
    name = PLUGIN_NAME
    verbose_name = _("Clinic onboarding")
    default_auto_field = "django.db.models.BigAutoField"

    def ready(self):
        # Care modules are imported here and not at module level: the app
        # registry is not populated when this module loads.
        from care_pi_onboarding.services import registry

        registry.load()
