from rest_framework.throttling import ScopedRateThrottle

from care_pi_onboarding.settings import plugin_settings

WRITE_SCOPE = "care_pi_onboarding_apply"
READ_SCOPE = "care_pi_onboarding_read"


class OnboardingThrottle(ScopedRateThrottle):
    """ScopedRateThrottle with the rates of the plug settings, not REST_FRAMEWORK of CARE. The key is the user."""

    @property
    def THROTTLE_RATES(self):
        return {
            WRITE_SCOPE: plugin_settings.ONBOARDING_APPLY_RATE,
            READ_SCOPE: plugin_settings.ONBOARDING_READ_RATE,
        }
