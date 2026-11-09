from care.utils.tests.base import CareAPITestBase
from django.core.cache import cache
from django.core.management import call_command

BASE = "/api/care_pi_onboarding"

ORGANIZATION = {"state": "Kerala", "district": "Ernakulam"}
FACILITY = {
    "template": "clinic",
    "name": "Kochi Family Clinic",
    "address": "MG Road, Kochi",
    "pincode": 682011,
    "phone_number": "+914842123456",
}
STAFF = [
    {
        "username": "dr-anil",
        "first_name": "Anil",
        "last_name": "Varma",
        "phone_number": "+919446000001",
        "gender": "male",
        "role": "doctor",
    },
    {
        "username": "nurse-beena",
        "first_name": "Beena",
        "last_name": "Joseph",
        "phone_number": "+919446000002",
        "gender": "female",
        "role": "nurse",
    },
    {
        "username": "front-chitra",
        "first_name": "Chitra",
        "last_name": "Das",
        "phone_number": "+919446000003",
        "gender": "female",
        "role": "receptionist",
    },
    {
        "username": "admin-dev",
        "first_name": "Dev",
        "last_name": "Pillai",
        "phone_number": "+919446000004",
        "gender": "male",
        "role": "admin",
    },
]


class OnboardingTestBase(CareAPITestBase):
    """CARE's API test base (it mocks the terminology lookups) and the onboarding calls."""

    @classmethod
    def setUpTestData(cls):
        # CARE runs this at each start; the test database starts empty.
        call_command("sync_permissions_roles")

    def setUp(self):
        super().setUp()
        # Throttle history and the CARE create locks live in the cache; a test starts clean.
        cache.clear()
        self.superuser = self.create_super_user(username="setup-admin")
        self.client.force_authenticate(user=self.superuser)

    def apply(self, step, data=None, expect=200):
        with self.captureOnCommitCallbacks(execute=True):
            response = self.client.post(
                f"{BASE}/steps/{step}/apply/", data or {}, format="json"
            )
        self.assertEqual(response.status_code, expect, response.data)
        return response.data

    def preview(self, step, facility=None, expect=200):
        params = {"facility": facility} if facility else {}
        response = self.client.get(f"{BASE}/steps/{step}/preview/", params)
        self.assertEqual(response.status_code, expect, response.data)
        return response.data

    def status_(self, facility=None):
        params = {"facility": facility} if facility else {}
        response = self.client.get(f"{BASE}/status/", params)
        self.assertEqual(response.status_code, 200, response.data)
        return response.data

    def setup_until(self, last):
        """Apply the steps in order up to and including last. Returns the facility id."""
        facility_id = None
        order = [
            "organization",
            "facility",
            "roles",
            "staff",
            "valuesets",
            "questionnaires",
            "catalog",
            "demo_data",
        ]
        for step in order:
            if step == "organization":
                self.apply(step, ORGANIZATION)
            elif step == "facility":
                facility_id = self.apply(step, FACILITY)["result"]["facility"]["id"]
            elif step == "roles":
                self.apply(step, {})
            elif step == "staff":
                self.apply(step, {"facility": facility_id, "members": STAFF})
            elif step == "valuesets":
                self.apply(step, {})
            else:
                self.apply(step, {"facility": facility_id})
            if step == last:
                break
        return facility_id
