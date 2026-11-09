def is_facility_admin(user, facility) -> bool:
    """
    A facility admin holds can_create_facility_organization in the root organisation
    (Administration) of the facility. In CARE 3.1 only the role Facility Admin has it.
    can_update_facility is not enough: the role Staff has it too.
    """
    if user.is_superuser:
        return True
    from care.security.authorization.base import AuthorizationHandler
    from care.security.permissions.facility_organization import (
        FacilityOrganizationPermissions,
    )

    return AuthorizationHandler().check_permission_in_facility_organization(
        [FacilityOrganizationPermissions.can_create_facility_organization.name],
        user,
        facility=facility,
        root=True,
    )


def managed_facilities(user):
    """The facilities that the user administers. A superuser administers each facility."""
    from care.emr.models.organization import FacilityOrganizationUser
    from care.facility.models import Facility
    from care.security.authorization.base import AuthorizationHandler
    from care.security.permissions.facility_organization import (
        FacilityOrganizationPermissions,
    )

    queryset = Facility.objects.all().order_by("created_date")
    if user.is_superuser:
        return queryset
    roles = AuthorizationHandler().get_role_from_permissions(
        [FacilityOrganizationPermissions.can_create_facility_organization.name]
    )
    return queryset.filter(
        id__in=FacilityOrganizationUser.objects.filter(
            user=user, role_id__in=roles, organization__org_type="root"
        ).values_list("organization__facility_id", flat=True)
    )
