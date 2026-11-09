from unittest import mock

import requests
from django.test import override_settings
from django.urls import reverse

from care_pi_onboarding.models import OnboardingRecord, OnboardingStep
from care_pi_onboarding.tests.base import (
    BASE,
    FACILITY,
    ORGANIZATION,
    STAFF,
    OnboardingTestBase,
)

STEPS = [
    "organization",
    "facility",
    "roles",
    "staff",
    "valuesets",
    "questionnaires",
    "catalog",
    "demo_data",
]


class FullRunTest(OnboardingTestBase):
    def test_every_step_twice_creates_nothing_the_second_time(self):
        first = {}
        facility_id = None
        for step in STEPS:
            data = self._input(step, facility_id)
            body = self.apply(step, data)
            self.assertEqual(body["status"], "done", body)
            first[step] = body
            if step == "facility":
                facility_id = body["result"]["facility"]["id"]
            self.assertGreater(
                sum(body["created"].values()), 0, f"{step} made nothing: {body}"
            )

        for step in STEPS:
            body = self.apply(step, self._input(step, facility_id))
            self.assertEqual(body["status"], "done", body)
            self.assertEqual(
                sum(body["created"].values()), 0, f"{step} made objects again: {body}"
            )
            self.assertEqual(
                sum(body["existing"].values()),
                sum(first[step]["created"].values())
                + sum(first[step]["existing"].values()),
                f"{step}: the second run does not find what the first made",
            )
            self.assertNotIn("secrets", body)

        status = self.status_(facility_id)
        self.assertTrue(status["can_manage"])
        self.assertIsNone(status["next_step"])
        self.assertEqual([s["state"] for s in status["steps"]], ["done"] * len(STEPS))
        self.assertFalse(status["wizard"]["redirect"])
        for step in STEPS:
            preview = self.preview(step, facility_id)
            self.assertEqual(preview["state"], "done")
            self.assertEqual(preview["runs"], 2)

    def _input(self, step, facility_id):
        return {
            "organization": ORGANIZATION,
            "facility": FACILITY,
            "roles": {},
            "staff": {"facility": facility_id, "members": STAFF},
            "valuesets": {},
        }.get(step, {"facility": facility_id})


class OrganizationStepTest(OnboardingTestBase):
    def test_state_and_district(self):
        from care.emr.models.organization import Organization

        body = self.apply("organization", ORGANIZATION)
        self.assertEqual(body["created"], {"govt_organization": 2})
        self.assertEqual(
            body["result"]["geo_organization"]["path"], ["Kerala", "Ernakulam"]
        )
        district = Organization.objects.get(name="Ernakulam")
        state = Organization.objects.get(name="Kerala")
        self.assertEqual(district.parent_id, state.id)
        self.assertEqual(district.level_cache, 1)
        self.assertEqual(district.parent_cache, [state.id])
        self.assertEqual(district.root_org_id, state.id)
        self.assertTrue(Organization.objects.get(id=state.id).has_children)

        again = self.apply("organization", {"state": "kerala", "district": "ERNAKULAM"})
        self.assertEqual(again["created"], {"govt_organization": 0})
        self.assertEqual(again["existing"], {"govt_organization": 2})

    def test_all_districts_in_one_insert(self):
        from care.emr.models.organization import Organization

        self.apply("organization", ORGANIZATION)
        body = self.apply("organization", {**ORGANIZATION, "all_districts": True})
        self.assertEqual(body["created"], {"govt_organization": 13})
        state = Organization.objects.get(name="Kerala")
        children = Organization.objects.filter(parent=state)
        self.assertEqual(children.count(), 14)
        self.assertEqual(set(children.values_list("level_cache", flat=True)), {1})
        self.assertEqual(
            set(children.values_list("root_org_id", flat=True)), {state.id}
        )
        # The CARE read API serialises them (the parent JSON comes from the caches).
        response = self.client.get(
            reverse("organization-list"),
            {"parent": str(state.external_id), "limit": 50},
        )
        self.assertEqual(response.status_code, 200)
        self.assertEqual(response.data["count"], 14)

    def test_local_body_and_parent(self):
        body = self.apply(
            "organization", {**ORGANIZATION, "local_body": "Kochi Corporation"}
        )
        self.assertEqual(
            body["result"]["geo_organization"]["path"],
            ["Kerala", "Ernakulam", "Kochi Corporation"],
        )
        parent = body["result"]["geo_organization"]["id"]
        again = self.apply("organization", {"parent": parent, "district": "Ward 12"})
        self.assertEqual(again["created"], {"govt_organization": 1})
        self.assertEqual(again["result"]["geo_organization"]["path"][-1], "Ward 12")

    def test_unknown_state_fails_and_is_recorded(self):
        body = self.apply("organization", {"state": "Atlantis"}, expect=400)
        self.assertEqual(body["status"], "failed")
        self.assertIn("Atlantis", body["detail"])
        self.assertEqual(
            OnboardingStep.objects.get(step="organization").status, "failed"
        )
        self.assertEqual(self.status_()["steps"][0]["state"], "failed")

    def test_invalid_input(self):
        body = self.apply("organization", {}, expect=400)
        self.assertEqual(body["status"], "failed")
        self.assertTrue(body["errors"])

    def test_preview(self):
        preview = self.preview("organization")
        self.assertEqual(len(preview["options"]["states"]), 36)
        self.assertEqual(preview["existing"]["states"], [])
        self.apply("organization", ORGANIZATION)
        preview = self.preview("organization")
        self.assertEqual(preview["existing"]["states"], ["Kerala"])
        self.assertEqual(preview["existing"]["districts"], {"Kerala": ["Ernakulam"]})
        self.assertEqual(preview["defaults"]["district"], "Ernakulam")


class FacilityStepTest(OnboardingTestBase):
    def test_clinic_template(self):
        from care.emr.models.location import (
            FacilityLocation,
            FacilityLocationOrganization,
        )
        from care.emr.models.organization import (
            FacilityOrganization,
            FacilityOrganizationUser,
        )
        from care.facility.models import Facility

        self.apply("organization", ORGANIZATION)
        body = self.apply("facility", FACILITY)
        self.assertEqual(
            body["created"],
            {"facility": 1, "department": 5, "location": 8, "location_department": 7},
        )
        facility = Facility.objects.get(external_id=body["result"]["facility"]["id"])
        self.assertEqual(facility.geo_organization.name, "Ernakulam")
        root = FacilityOrganization.objects.get(facility=facility, org_type="root")
        self.assertEqual(root.name, "Administration")
        self.assertTrue(
            FacilityOrganizationUser.objects.filter(
                organization=root, user=self.superuser, role__name="Facility Admin"
            ).exists()
        )
        beds = FacilityLocation.objects.filter(facility=facility, form="bd")
        self.assertEqual(beds.count(), 2)
        ward = FacilityLocation.objects.get(facility=facility, name="Observation")
        self.assertTrue(ward.has_beds)
        self.assertEqual(
            FacilityLocationOrganization.objects.filter(location=ward).count(), 2
        )
        nursing = FacilityOrganization.objects.get(facility=facility, name="Nursing")
        self.assertIn(nursing.id, beds.first().facility_organization_cache)

        # The CARE APIs list what the step made.
        response = self.client.get(
            reverse(
                "facility-organization-list",
                kwargs={"facility_external_id": facility.external_id},
            )
        )
        self.assertEqual(response.data["count"], 6)
        response = self.client.get(
            reverse(
                "location-list", kwargs={"facility_external_id": facility.external_id}
            ),
            {"limit": 50},
        )
        self.assertEqual(response.data["count"], 8)

        again = self.apply("facility", FACILITY)
        self.assertEqual(sum(again["created"].values()), 0)
        self.assertEqual(again["existing"]["department"], 5)

    def test_pharmacy_template_and_selected_departments(self):
        self.apply("organization", ORGANIZATION)
        body = self.apply(
            "facility",
            {
                **FACILITY,
                "name": "Kochi Pharmacy",
                "template": "pharmacy",
                "departments": ["Pharmacy"],
            },
        )
        self.assertEqual(body["created"]["department"], 1)
        # Counter belongs to Pharmacy and Front Desk; Front Desk is left out, so only one link.
        self.assertEqual(body["created"]["location"], 2)
        self.assertEqual(body["created"]["location_department"], 2)

    def test_needs_an_area(self):
        body = self.apply("facility", FACILITY, expect=400)
        self.assertIn("organization", body["detail"])

    def test_bad_phone_number(self):
        self.apply("organization", ORGANIZATION)
        body = self.apply("facility", {**FACILITY, "phone_number": "12345"}, expect=400)
        self.assertIn("phone_number", body["detail"])

    def test_bad_pincode(self):
        self.apply("organization", ORGANIZATION)
        body = self.apply("facility", {**FACILITY, "pincode": 12}, expect=400)
        self.assertIn("pincode", body["detail"])

    def test_preview(self):
        preview = self.preview("facility")
        self.assertIn("clinic", preview["options"]["templates"])
        self.assertIn("Private Hospital", preview["options"]["facility_types"])


class RolesStepTest(OnboardingTestBase):
    def test_roles(self):
        from care.emr.models.organization import Organization
        from care.security.models import RoleModel, RolePermission

        body = self.apply("roles", {})
        self.assertEqual(body["created"]["role"], 1)
        self.assertEqual(body["created"]["role_organization"], 5)
        receptionist = RoleModel.objects.get(name="Receptionist")
        self.assertFalse(receptionist.is_system)
        slugs = set(
            RolePermission.objects.filter(role=receptionist).values_list(
                "permission__slug", flat=True
            )
        )
        self.assertIn("can_create_patient", slugs)
        self.assertNotIn("can_view_clinical_data", slugs)
        self.assertEqual(body["result"]["roles"]["admin"]["name"], "Facility Admin")
        self.assertEqual(Organization.objects.filter(org_type="role").count(), 5)

        again = self.apply("roles", {})
        self.assertEqual(sum(again["created"].values()), 0)

    def test_without_role_organizations(self):
        body = self.apply("roles", {"role_organizations": False})
        self.assertNotIn("role_organization", body["created"])
        self.assertIsNone(body["result"]["roles"]["doctor"]["role_organization"])


class StaffStepTest(OnboardingTestBase):
    def test_staff_and_passwords_once(self):
        from care.emr.models.organization import FacilityOrganizationUser
        from care.users.models import User

        facility_id = self.setup_until("roles")
        body = self.apply("staff", {"facility": facility_id, "members": STAFF})
        self.assertEqual(body["created"]["user"], 4)
        self.assertEqual(body["created"]["membership"], 4)
        credentials = {
            c["username"]: c["password"] for c in body["secrets"]["credentials"]
        }
        self.assertEqual(set(credentials), {m["username"] for m in STAFF})
        doctor = User.objects.get(username="dr-anil")
        self.assertTrue(doctor.check_password(credentials["dr-anil"]))
        self.assertTrue(doctor.email.endswith("@staff.invalid"))
        membership = FacilityOrganizationUser.objects.get(user=doctor)
        self.assertEqual(
            (membership.organization.name, membership.role.name),
            ("General Medicine", "Doctor"),
        )
        admin = FacilityOrganizationUser.objects.get(user__username="admin-dev")
        self.assertEqual(
            (admin.organization.org_type, admin.role.name), ("root", "Facility Admin")
        )
        receptionist = FacilityOrganizationUser.objects.get(
            user__username="front-chitra"
        )
        self.assertEqual(receptionist.role.name, "Receptionist")
        self.assertTrue(doctor.role_org_ids)
        # The passwords are not stored by the plug.
        stored = str(list(OnboardingStep.objects.values_list("result", flat=True)))
        for password in credentials.values():
            self.assertNotIn(password, stored)

        again = self.apply("staff", {"facility": facility_id, "members": STAFF})
        self.assertEqual(sum(again["created"].values()), 0)
        self.assertNotIn("secrets", again)

    def test_given_password_and_department(self):
        from care.emr.models.organization import FacilityOrganizationUser
        from care.users.models import User

        facility_id = self.setup_until("roles")
        member = {**STAFF[1], "password": "Clinic#Pass2026", "department": "Front Desk"}
        body = self.apply("staff", {"facility": facility_id, "members": [member]})
        self.assertEqual(
            body["secrets"]["credentials"][0]["password"], "Clinic#Pass2026"
        )
        self.assertTrue(
            User.objects.get(username="nurse-beena").check_password("Clinic#Pass2026")
        )
        self.assertEqual(
            FacilityOrganizationUser.objects.get(
                user__username="nurse-beena"
            ).organization.name,
            "Front Desk",
        )

    def test_conflicting_role_fails_and_rolls_back(self):
        from care.users.models import User

        facility_id = self.setup_until("staff")
        changed = [
            {**STAFF[1], "username": "new-person", "phone_number": "+919446000099"},
            {**STAFF[0], "role": "nurse", "department": "General Medicine"},
        ]
        body = self.apply(
            "staff", {"facility": facility_id, "members": changed}, expect=400
        )
        self.assertIn("dr-anil", body["detail"])
        self.assertFalse(User.objects.filter(username="new-person").exists())

    def test_invite_needs_an_email(self):
        from care.users.models import User

        facility_id = self.setup_until("roles")
        member = {**STAFF[0], "invite": True}
        body = self.apply(
            "staff", {"facility": facility_id, "members": [member]}, expect=400
        )
        self.assertIn("members[0].email", body["detail"])
        self.assertFalse(User.objects.filter(username=member["username"]).exists())

        body = self.apply(
            "staff",
            {"facility": facility_id, "members": [{**member, "email": "   "}]},
            expect=400,
        )
        self.assertIn("email", body["detail"])

    def test_invite_sends_the_mail_and_gives_no_password(self):
        from care.users.models import User
        from django.core import mail

        facility_id = self.setup_until("roles")
        member = {**STAFF[0], "invite": True, "email": "anil@clinic.example"}
        body = self.apply("staff", {"facility": facility_id, "members": [member]})
        self.assertNotIn("secrets", body)
        self.assertEqual(body["result"]["invited"], ["dr-anil"])
        user = User.objects.get(username="dr-anil")
        self.assertFalse(user.has_usable_password())
        self.assertEqual([m.to for m in mail.outbox], [["anil@clinic.example"]])

    def test_duplicate_username(self):
        facility_id = self.setup_until("roles")
        body = self.apply(
            "staff",
            {"facility": facility_id, "members": [STAFF[0], STAFF[0]]},
            expect=400,
        )
        self.assertIn("twice", body["detail"])

    def test_needs_roles(self):
        from care.security.models import RoleModel

        facility_id = self.setup_until("facility")
        RoleModel.objects.filter(name="Receptionist").delete()
        body = self.apply(
            "staff", {"facility": facility_id, "members": STAFF}, expect=400
        )
        self.assertIn("roles", body["detail"])


class ValuesetsStepTest(OnboardingTestBase):
    def test_valuesets(self):
        from care.emr.models import ValueSet
        from care.emr.registries.care_valueset.care_valueset import SystemValueSet

        body = self.apply("valuesets", {})
        system = len(SystemValueSet.get_all_valuesets())
        self.assertEqual(body["created"]["system_valueset"], system)
        self.assertEqual(body["created"]["clinic_valueset"], 2)
        complaints = ValueSet.objects.get(slug="care-pi-clinic-complaints")
        self.assertEqual(len(complaints.compose["include"][0]["concept"]), 14)
        again = self.apply("valuesets", {})
        self.assertEqual(sum(again["created"].values()), 0)
        self.assertEqual(again["existing"]["system_valueset"], system)

    def test_selected(self):
        body = self.apply(
            "valuesets", {"system": False, "selected": ["care-pi-clinic-conditions"]}
        )
        self.assertEqual(body["created"], {"clinic_valueset": 1})
        self.apply("valuesets", {"selected": ["nope"]}, expect=400)


class QuestionnairesStepTest(OnboardingTestBase):
    def test_questionnaires_visible_to_department_staff(self):
        from care.emr.models import Questionnaire
        from care.users.models import User

        facility_id = self.setup_until("staff")
        body = self.apply("questionnaires", {"facility": facility_id})
        self.assertEqual(body["created"]["questionnaire"], 4)
        soap = Questionnaire.objects.get(slug="care-pi-soap-note")
        self.assertEqual(soap.auth_context, "facility")
        # A doctor of General Medicine reads the facility questionnaires through the CARE API.
        self.client.force_authenticate(User.objects.get(username="dr-anil"))
        response = self.client.get(
            reverse("questionnaire-list"), {"facility": facility_id, "limit": 20}
        )
        self.assertEqual(response.status_code, 200)
        slugs = {q["slug"] for q in response.data["results"]}
        self.assertIn("care-pi-vitals", slugs)
        self.assertIn("care-pi-soap-note", slugs)
        self.assertNotIn("care-pi-pharmacy-intake", slugs)

        self.client.force_authenticate(self.superuser)
        again = self.apply("questionnaires", {"facility": facility_id})
        self.assertEqual(sum(again["created"].values()), 0)


class CatalogStepTest(OnboardingTestBase):
    def test_catalog(self):
        from care.emr.models.product_knowledge import ProductKnowledge

        facility_id = self.setup_until("facility")
        body = self.apply("catalog", {"facility": facility_id})
        self.assertEqual(body["created"], {"medication": 8})
        item = ProductKnowledge.objects.get(name="Paracetamol 500 mg")
        self.assertEqual(item.slug, f"f-{facility_id}-paracetamol-500-mg")
        response = self.client.get(
            reverse("product_knowledge-list"), {"facility": facility_id, "limit": 20}
        )
        self.assertEqual(response.status_code, 200, response.data)
        self.assertEqual(response.data["count"], 8)
        again = self.apply("catalog", {"facility": facility_id})
        self.assertEqual(again["created"], {"medication": 0})

    def test_offline_terminology_fails_cleanly(self):
        facility_id = self.setup_until("facility")
        with mock.patch(
            "care.emr.utils.valueset_coding_type.validate_valueset",
            side_effect=requests.ConnectionError("offline"),
        ):
            body = self.apply("catalog", {"facility": facility_id}, expect=400)
        self.assertIn("terminology server", body["detail"])

    def test_skip(self):
        facility_id = self.setup_until("facility")
        response = self.client.post(
            f"{BASE}/steps/catalog/skip/", {"facility": facility_id}, format="json"
        )
        self.assertEqual(response.data["status"], "skipped")
        response = self.client.post(f"{BASE}/steps/roles/skip/", {}, format="json")
        self.assertEqual(response.status_code, 400)


class DemoDataStepTest(OnboardingTestBase):
    def test_demo_data_and_removal(self):
        from care.emr.models import Encounter, Patient

        facility_id = self.setup_until("facility")
        body = self.apply("demo_data", {"facility": facility_id})
        self.assertEqual(body["created"], {"patient": 5, "encounter": 5})
        self.assertEqual(Patient.objects.filter(name__startswith="Demo ").count(), 5)
        again = self.apply("demo_data", {"facility": facility_id})
        self.assertEqual(again["created"], {"patient": 0, "encounter": 0})

        with self.captureOnCommitCallbacks(execute=True):
            response = self.client.delete(f"{BASE}/demo_data/?facility={facility_id}")
        self.assertEqual(response.status_code, 200, response.data)
        self.assertEqual(response.data["removed"], {"encounter": 5, "patient": 5})
        self.assertEqual(Patient.objects.filter(name__startswith="Demo ").count(), 0)
        self.assertEqual(
            Encounter.objects.filter(facility__external_id=facility_id).count(), 0
        )
        self.assertFalse(OnboardingRecord.objects.filter(step="demo_data").exists())
        self.assertEqual(self.preview("demo_data", facility_id)["state"], "pending")

        body = self.apply("demo_data", {"facility": facility_id})
        self.assertEqual(body["created"], {"patient": 5, "encounter": 5})

    def test_a_failed_visit_releases_the_care_locks(self):
        from care.emr.locks.billing import (
            FacilityEncounterCreateLock,
            PatientCreateLock,
        )
        from care.emr.models import Patient
        from care.facility.models import Facility
        from django.core.cache import cache

        from care_pi_onboarding.services.base import StepError

        facility_id = self.setup_until("facility")
        facility = Facility.objects.get(external_id=facility_id)
        with mock.patch(
            "care_pi_onboarding.services.demo_data._create_encounter",
            side_effect=StepError("The visit failed."),
        ):
            body = self.apply("demo_data", {"facility": facility_id}, expect=400)
        self.assertIn("The visit failed.", body["detail"])
        # The transaction rolled back: no demo patient stays, and CARE can register patients at once.
        self.assertFalse(Patient.objects.filter(name__startswith="Demo ").exists())
        self.assertIsNone(cache.get(PatientCreateLock().key))
        self.assertIsNone(cache.get(FacilityEncounterCreateLock(facility.id).key))

        body = self.apply("demo_data", {"facility": facility_id})
        self.assertEqual(body["created"], {"patient": 5, "encounter": 5})
        self.assertIsNone(cache.get(PatientCreateLock().key))

    def test_a_busy_visit_lock_gives_back_the_patient_lock(self):
        from care.emr.locks.billing import (
            FacilityEncounterCreateLock,
            PatientCreateLock,
        )
        from care.facility.models import Facility
        from django.core.cache import cache

        facility_id = self.setup_until("facility")
        facility = Facility.objects.get(external_id=facility_id)
        lock = FacilityEncounterCreateLock(facility.id)
        lock.acquire()
        try:
            body = self.apply("demo_data", {"facility": facility_id}, expect=400)
        finally:
            lock.release()
        self.assertIn("another visit", body["detail"])
        # The step took the patient lock first and gave it back when the visit lock was busy.
        self.assertIsNone(cache.get(PatientCreateLock().key))

    @override_settings(
        PLUGIN_CONFIGS={"care_pi_onboarding": {"ONBOARDING_DEMO_ALLOWED": False}}
    )
    def test_demo_off(self):
        facility_id = self.setup_until("facility")
        self.apply("demo_data", {"facility": facility_id}, expect=403)
        demo = next(
            s for s in self.status_(facility_id)["steps"] if s["step"] == "demo_data"
        )
        self.assertFalse(demo["allowed"])
        response = self.client.post(
            f"{BASE}/steps/demo_data/skip/", {"facility": facility_id}, format="json"
        )
        self.assertEqual(response.data["status"], "skipped")


class PermissionsTest(OnboardingTestBase):
    def test_plain_user_sees_nothing(self):
        user = self.create_user(username="plain-user")
        self.client.force_authenticate(user)
        status = self.status_()
        self.assertFalse(status["can_manage"])
        self.assertEqual(status["steps"], [])
        self.apply("organization", ORGANIZATION, expect=403)
        self.preview("organization", expect=403)
        response = self.client.post(
            f"{BASE}/wizard/close/", {"reason": "later"}, format="json"
        )
        self.assertEqual(response.status_code, 403)

    def test_anonymous(self):
        self.client.force_authenticate(None)
        response = self.client.get(f"{BASE}/status/")
        self.assertIn(response.status_code, (401, 403))

    def test_facility_admin_and_doctor(self):
        from care.users.models import User

        facility_id = self.setup_until("staff")
        self.client.force_authenticate(User.objects.get(username="admin-dev"))
        status = self.status_()
        self.assertTrue(status["can_manage"])
        self.assertEqual(status["facility"]["id"], facility_id)
        allowed = {s["step"]: s["allowed"] for s in status["steps"]}
        self.assertFalse(allowed["organization"])
        self.assertFalse(allowed["roles"])
        self.assertTrue(allowed["questionnaires"])
        self.apply("questionnaires", {"facility": facility_id})
        self.apply("organization", ORGANIZATION, expect=403)
        self.apply("valuesets", {}, expect=403)

        self.client.force_authenticate(User.objects.get(username="dr-anil"))
        self.apply("questionnaires", {"facility": facility_id}, expect=403)
        response = self.client.get(f"{BASE}/status/", {"facility": facility_id})
        self.assertEqual(response.status_code, 403)

    @override_settings(
        PLUGIN_CONFIGS={"care_pi_onboarding": {"ONBOARDING_APPLY_RATE": "2/min"}}
    )
    def test_apply_is_rate_limited(self):
        self.apply("roles", {})
        self.apply("roles", {})
        response = self.client.post(f"{BASE}/steps/roles/apply/", {}, format="json")
        self.assertEqual(response.status_code, 429)


class WizardTest(OnboardingTestBase):
    def test_redirect_until_facility_or_close(self):
        status = self.status_()
        self.assertEqual(status["next_step"], "organization")
        self.assertTrue(status["wizard"]["redirect"])
        response = self.client.post(
            f"{BASE}/wizard/close/", {"reason": "later"}, format="json"
        )
        self.assertEqual(response.status_code, 200)
        self.assertFalse(self.status_()["wizard"]["redirect"])
        self.assertEqual(self.status_()["wizard"]["reason"], "later")

    def test_unknown_step(self):
        response = self.client.post(f"{BASE}/steps/nope/apply/", {}, format="json")
        self.assertEqual(response.status_code, 404)

    def test_no_trailing_slash(self):
        response = self.client.post(f"{BASE}/steps/roles/apply", {}, format="json")
        self.assertEqual(response.status_code, 200)
