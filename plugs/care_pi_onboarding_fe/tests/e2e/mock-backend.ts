import type { Page, Request } from "@playwright/test";

type StepName =
  | "organization"
  | "facility"
  | "roles"
  | "staff"
  | "valuesets"
  | "questionnaires"
  | "catalog"
  | "demo_data";

const ORDER: StepName[] = [
  "organization",
  "facility",
  "roles",
  "staff",
  "valuesets",
  "questionnaires",
  "catalog",
  "demo_data",
];
const FACILITY_SCOPE = new Set<StepName>([
  "facility",
  "staff",
  "questionnaires",
  "catalog",
  "demo_data",
]);
const OPTIONAL = new Set<StepName>(["catalog", "demo_data"]);
export const FACILITY_ID = "6a3c1f0e-6f0e-4d5e-9a1b-1c2d3e4f5a6b";

const PREVIEWS: Record<StepName, Record<string, unknown>> = {
  organization: {
    options: {
      country: "India",
      states: [
        {
          name: "Kerala",
          kind: "state",
          districts: ["Alappuzha", "Ernakulam", "Idukki"],
        },
        { name: "Goa", kind: "state", districts: ["North Goa", "South Goa"] },
      ],
    },
    existing: { states: [], districts: {} },
    defaults: { state: "", district: "", local_body: "" },
  },
  facility: {
    options: {
      templates: {
        clinic: {
          label: "Clinic",
          description: "An outpatient clinic.",
          facility_type: "Private Hospital",
          departments: ["General Medicine", "Nursing", "Front Desk"],
          locations: ["Reception", "Consultation Room 1"],
        },
      },
      facility_types: ["Other", "Private Hospital"],
    },
    defaults: { template: "clinic", phone_example: "+919876543210" },
    current: null,
  },
  roles: {
    options: {
      roles: [
        {
          key: "doctor",
          label: "Doctor",
          description: "",
          care_role: "Doctor",
          custom: false,
          permissions: null,
          exists: true,
        },
        {
          key: "receptionist",
          label: "Receptionist",
          description: "",
          care_role: "Receptionist",
          custom: true,
          permissions: 24,
          exists: false,
        },
      ],
    },
    defaults: { role_organizations: true },
  },
  staff: {
    options: {
      roles: [
        { key: "doctor", label: "Doctor" },
        { key: "receptionist", label: "Receptionist" },
      ],
      genders: ["male", "female", "transgender", "non_binary"],
      departments: ["Administration", "General Medicine", "Front Desk"],
    },
    defaults: {
      departments: { doctor: "General Medicine", receptionist: "Front Desk" },
      phone_example: "+919876543210",
      max_members: 50,
    },
    existing: { members: [] },
  },
  valuesets: {
    options: {
      valuesets: [
        {
          slug: "care-pi-clinic-complaints",
          name: "Clinic: common complaints",
          description: "",
          codes: 14,
          exists: false,
        },
      ],
    },
    existing: { system: 30, system_total: 34 },
    defaults: { system: true, selected: ["care-pi-clinic-complaints"] },
  },
  questionnaires: {
    options: {
      questionnaires: [
        {
          slug: "care-pi-vitals",
          title: "Vitals",
          description: "Vitals.",
          questions: 9,
          departments: "*",
          exists: false,
        },
      ],
    },
    defaults: { selected: ["care-pi-vitals"] },
  },
  catalog: {
    options: {
      medications: [
        {
          slug: "paracetamol-500-mg",
          name: "Paracetamol 500 mg",
          exists: false,
        },
      ],
    },
    defaults: { selected: ["paracetamol-500-mg"] },
    requires_internet: true,
  },
  demo_data: {
    options: {
      patients: [{ key: "demo-1", name: "Demo Asha Menon", age: 34 }],
      department: "General Medicine",
    },
    allowed: true,
    existing: {},
  },
};

const CREATED: Record<StepName, Record<string, number>> = {
  organization: { govt_organization: 2 },
  facility: { facility: 1, department: 3, location: 2 },
  roles: { role: 1, role_organization: 2 },
  staff: { user: 1, membership: 1 },
  valuesets: { system_valueset: 4, clinic_valueset: 1 },
  questionnaires: { questionnaire: 1 },
  catalog: { medication: 1 },
  demo_data: { patient: 1, encounter: 1 },
};

/** An in-memory onboarding API. It records each request, so a test can count them. */
export class MockBackend {
  requests: string[] = [];
  bodies: Record<string, unknown>[] = [];
  state: Record<StepName, "pending" | "done" | "skipped" | "failed"> =
    Object.fromEntries(ORDER.map((s) => [s, "pending"])) as Record<
      StepName,
      "pending" | "done" | "skipped" | "failed"
    >;
  facility: { id: string; name: string } | null = null;
  closed = "";
  /** Extra preview options for a step, for example { staff: { invite_available: false } }. */
  options: Partial<Record<StepName, Record<string, unknown>>> = {};

  async install(page: Page) {
    await page.route("**/api/care_pi_onboarding/**", async (route) => {
      const request = route.request();
      const url = new URL(request.url());
      const path = url.pathname.replace("/api/care_pi_onboarding", "");
      this.requests.push(`${request.method()} ${path}`);
      const [status, body] = this.answer(request, path);
      await route.fulfill({
        status,
        contentType: "application/json",
        body: JSON.stringify(body),
      });
    });
  }

  private status() {
    return {
      can_manage: true,
      is_superuser: true,
      locale: "in",
      facility: this.facility,
      geo_organization: null,
      steps: ORDER.map((step) => ({
        step,
        title: step,
        description: "",
        optional: OPTIONAL.has(step),
        scope: FACILITY_SCOPE.has(step) ? "facility" : "instance",
        allowed:
          !FACILITY_SCOPE.has(step) ||
          step === "facility" ||
          this.facility !== null,
        available: true,
        state: this.state[step],
        runs: 0,
        applied_at: null,
        created: {},
        existing: {},
        detail: "",
        exists: {},
      })),
      next_step: ORDER.find((s) => this.state[s] === "pending") ?? null,
      wizard: {
        closed: Boolean(this.closed),
        reason: this.closed,
        redirect: !this.closed && this.facility === null,
      },
    };
  }

  private answer(request: Request, path: string): [number, unknown] {
    const method = request.method();
    if (method === "GET" && path === "/status/") return [200, this.status()];
    const match = path.match(/^\/steps\/([a-z_]+)\/(preview|apply|skip)\/$/);
    if (match) {
      const step = match[1] as StepName;
      const action = match[2];
      if (action === "preview" && method === "GET") {
        return [
          200,
          {
            step,
            state: this.state[step],
            runs: 0,
            result: {},
            ...PREVIEWS[step],
            ...(this.options[step] && {
              options: {
                ...(PREVIEWS[step].options as Record<string, unknown>),
                ...this.options[step],
              },
            }),
          },
        ];
      }
      const body = (request.postDataJSON() ?? {}) as Record<string, unknown>;
      this.bodies.push({ step, action, ...body });
      if (
        FACILITY_SCOPE.has(step) &&
        step !== "facility" &&
        body.facility !== FACILITY_ID
      ) {
        return [
          400,
          {
            step,
            status: "failed",
            created: {},
            existing: {},
            detail: "facility is missing",
            errors: [],
          },
        ];
      }
      if (action === "skip") {
        this.state[step] = "skipped";
        return [
          200,
          {
            step,
            status: "skipped",
            created: {},
            existing: {},
            detail: "You skipped it.",
          },
        ];
      }
      const again = this.state[step] === "done";
      this.state[step] = "done";
      const counts = CREATED[step];
      const zero = Object.fromEntries(Object.keys(counts).map((k) => [k, 0]));
      const result: Record<string, unknown> = {
        step,
        status: "done",
        created: again ? zero : counts,
        existing: again ? counts : zero,
        detail: `The step ${step} is done.`,
        result: {},
      };
      if (step === "facility") {
        this.facility = {
          id: FACILITY_ID,
          name: String(body.name ?? "Clinic"),
        };
        result.result = { facility: this.facility };
      }
      if (step === "staff" && !again) {
        result.secrets = {
          credentials: [{ username: "dr-anil", password: "Xy7pQ2mN8rT4s@" }],
        };
      }
      return [200, result];
    }
    if (method === "DELETE" && path === "/demo_data/") {
      this.state.demo_data = "pending";
      return [
        200,
        {
          step: "demo_data",
          status: "removed",
          removed: { patient: 1, encounter: 1 },
          detail: "Removed.",
        },
      ];
    }
    if (method === "POST" && path === "/wizard/close/") {
      this.closed = String(
        (request.postDataJSON() as { reason: string }).reason,
      );
      return [
        200,
        { wizard: { closed: true, reason: this.closed, redirect: false } },
      ];
    }
    return [404, { detail: "not found" }];
  }
}
