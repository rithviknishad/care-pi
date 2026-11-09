from django.urls import re_path

from care_pi_onboarding.api.views import (
    DemoDataView,
    StatusView,
    StepApplyView,
    StepPreviewView,
    StepSkipView,
    WizardCloseView,
)

# CARE mounts this module at /api/care_pi_onboarding/ (config/urls.py: one path for each PLUGIN_APPS entry).
# The trailing slash is optional, so a POST without it does not hit APPEND_SLASH.
urlpatterns = [
    re_path(r"^status/?$", StatusView.as_view(), name="care-pi-onboarding-status"),
    re_path(
        r"^steps/(?P<step>[a-z_]+)/preview/?$",
        StepPreviewView.as_view(),
        name="care-pi-onboarding-preview",
    ),
    re_path(
        r"^steps/(?P<step>[a-z_]+)/apply/?$",
        StepApplyView.as_view(),
        name="care-pi-onboarding-apply",
    ),
    re_path(
        r"^steps/(?P<step>[a-z_]+)/skip/?$",
        StepSkipView.as_view(),
        name="care-pi-onboarding-skip",
    ),
    re_path(
        r"^demo_data/?$", DemoDataView.as_view(), name="care-pi-onboarding-demo-data"
    ),
    re_path(
        r"^wizard/close/?$",
        WizardCloseView.as_view(),
        name="care-pi-onboarding-wizard-close",
    ),
]
