from care_pi_onboarding.services.base import Step

ORDER = [
    "organization",
    "facility",
    "roles",
    "staff",
    "valuesets",
    "questionnaires",
    "catalog",
    "demo_data",
]

_steps: dict[str, Step] = {}


def load():
    """Make the step objects. AppConfig.ready calls it, after the CARE app registry."""
    if _steps:
        return
    from care_pi_onboarding.services.catalog import CatalogStep
    from care_pi_onboarding.services.demo_data import DemoDataStep
    from care_pi_onboarding.services.facility import FacilityStep
    from care_pi_onboarding.services.organization import OrganizationStep
    from care_pi_onboarding.services.questionnaires import QuestionnairesStep
    from care_pi_onboarding.services.roles import RolesStep
    from care_pi_onboarding.services.staff import StaffStep
    from care_pi_onboarding.services.valuesets import ValuesetsStep

    for cls in (
        OrganizationStep,
        FacilityStep,
        RolesStep,
        StaffStep,
        ValuesetsStep,
        QuestionnairesStep,
        CatalogStep,
        DemoDataStep,
    ):
        step = cls()
        _steps[step.name] = step
    if list(_steps) != ORDER:
        raise RuntimeError("care_pi_onboarding: the step order does not match ORDER")


def get(name: str) -> Step | None:
    load()
    return _steps.get(name)


def steps() -> list[Step]:
    load()
    return [_steps[name] for name in ORDER]
