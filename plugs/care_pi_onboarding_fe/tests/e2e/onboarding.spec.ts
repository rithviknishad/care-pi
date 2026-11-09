import { expect, test, type Page } from "@playwright/test";

import { FACILITY_ID, MockBackend } from "./mock-backend";

const SHOTS = process.env.SHOTS_DIR;

async function shot(page: Page, name: string) {
  if (SHOTS)
    await page.screenshot({ path: `${SHOTS}/${name}.png`, fullPage: true });
}

async function applyAndContinue(page: Page, step: string) {
  await page.getByTestId("apply").click();
  await expect(page.getByTestId("result-done")).toBeVisible();
  await expect(page.getByTestId(`rail-${step}`)).toHaveAttribute(
    "data-state",
    "done",
  );
  await page.getByTestId("continue").click();
}

test("one full run: one request for each screen and each action", async ({
  page,
}) => {
  const backend = new MockBackend();
  await backend.install(page);
  await page.goto("/");

  // Area
  await expect(page.getByTestId("screen-organization")).toBeVisible();
  await page.getByTestId("field-state").selectOption("Kerala");
  await page.getByTestId("field-district").selectOption("Ernakulam");
  await expect(page.getByTestId("plan")).toContainText("2 new areas");
  await shot(page, "01-area");
  await applyAndContinue(page, "organization");

  // Facility
  await expect(page.getByTestId("screen-facility")).toBeVisible();
  await page.getByTestId("field-name").fill("Kochi Family Clinic");
  await page.getByTestId("field-phone").fill("+914842123456");
  await page.getByTestId("field-pincode").fill("682011");
  await page.getByTestId("field-address").fill("MG Road, Kochi");
  await shot(page, "02-facility");
  await applyAndContinue(page, "facility");

  // Roles
  await expect(page.getByTestId("screen-roles")).toBeVisible();
  await applyAndContinue(page, "roles");

  // Staff: the initial password shows once.
  await expect(page.getByTestId("screen-staff")).toBeVisible();
  await page.getByTestId("member-0-first-name").fill("Anil");
  await page.getByTestId("member-0-username").fill("dr-anil");
  await page.getByTestId("member-0-phone").fill("+919446000001");
  await page.getByTestId("apply").click();
  await expect(page.getByTestId("credentials")).toContainText("Xy7pQ2mN8rT4s@");
  await shot(page, "03-staff-credentials");
  await page.getByTestId("continue").click();

  // Coded lists and forms
  await expect(page.getByTestId("screen-valuesets")).toBeVisible();
  await applyAndContinue(page, "valuesets");
  await expect(page.getByTestId("screen-questionnaires")).toBeVisible();
  await applyAndContinue(page, "questionnaires");

  // Medicines: skip.
  await expect(page.getByTestId("screen-catalog")).toBeVisible();
  await page.getByTestId("skip").click();
  await expect(page.getByTestId("rail-catalog")).toHaveAttribute(
    "data-state",
    "skipped",
  );
  await page.getByTestId("continue").click();

  // Demo patients
  await expect(page.getByTestId("screen-demo_data")).toBeVisible();
  await applyAndContinue(page, "demo_data");

  // Finish
  await expect(page.getByTestId("screen-done")).toBeVisible();
  await expect(page.getByTestId("progress-count")).toContainText("8 of 8");
  await shot(page, "04-done");
  await page.getByTestId("open-care").click();
  await expect(page).toHaveURL(
    new RegExp(`/facility/${FACILITY_ID}/overview$`),
  );

  // The request log: one status GET, one preview GET for each screen, one POST for each action.
  expect(backend.requests).toEqual([
    "GET /status/",
    "GET /steps/organization/preview/",
    "POST /steps/organization/apply/",
    "GET /steps/facility/preview/",
    "POST /steps/facility/apply/",
    "GET /steps/roles/preview/",
    "POST /steps/roles/apply/",
    "GET /steps/staff/preview/",
    "POST /steps/staff/apply/",
    "GET /steps/valuesets/preview/",
    "POST /steps/valuesets/apply/",
    "GET /steps/questionnaires/preview/",
    "POST /steps/questionnaires/apply/",
    "GET /steps/catalog/preview/",
    "POST /steps/catalog/skip/",
    "GET /steps/demo_data/preview/",
    "POST /steps/demo_data/apply/",
    "POST /wizard/close/",
  ]);
  const staff = backend.bodies.find((b) => b.step === "staff") as {
    members: { department: string }[];
  };
  expect(staff.members[0].department).toBe("General Medicine");
  expect(backend.closed).toBe("finished");
});

test("a failed step shows the reason and keeps the form", async ({ page }) => {
  const backend = new MockBackend();
  await backend.install(page);
  // Registered after the backend, so it runs first (Playwright runs routes in reverse order).
  await page.route(
    "**/api/care_pi_onboarding/steps/organization/apply/",
    (route) =>
      route.fulfill({
        status: 400,
        contentType: "application/json",
        body: JSON.stringify({
          step: "organization",
          status: "failed",
          created: {},
          existing: {},
          detail: "The district Atlantis is not in Kerala.",
          errors: [],
        }),
      }),
  );
  await page.goto("/");
  await page.getByTestId("field-state").selectOption("Kerala");
  await page.getByTestId("field-district").selectOption("Idukki");
  await page.getByTestId("apply").click();
  await expect(page.getByTestId("result-failed")).toContainText("Atlantis");
  await expect(page.getByTestId("rail-organization")).toHaveAttribute(
    "data-state",
    "failed",
  );
  await expect(page.getByTestId("apply")).toBeEnabled();
});

test("a rate limit answer shows one sentence", async ({ page }) => {
  const backend = new MockBackend();
  await backend.install(page);
  // Registered after the backend, so it runs first (Playwright runs routes in reverse order).
  await page.route(
    "**/api/care_pi_onboarding/steps/organization/apply/",
    (route) =>
      route.fulfill({
        status: 429,
        contentType: "application/json",
        body: '{"detail": "Request was throttled."}',
      }),
  );
  await page.goto("/");
  await page.getByTestId("field-state").selectOption("Kerala");
  await page.getByTestId("field-district").selectOption("Idukki");
  await page.getByTestId("apply").click();
  await expect(page.getByTestId("request-error")).toContainText(
    "Too many requests",
  );
});

test("the invite option needs an email", async ({ page }) => {
  const backend = new MockBackend();
  backend.state.organization = "done";
  backend.state.facility = "done";
  backend.state.roles = "done";
  backend.facility = { id: FACILITY_ID, name: "Kochi Family Clinic" };
  await backend.install(page);
  await page.goto("/");

  await expect(page.getByTestId("screen-staff")).toBeVisible();
  await page.getByTestId("member-0-first-name").fill("Anil");
  await page.getByTestId("member-0-username").fill("dr-anil");
  await page.getByTestId("member-0-phone").fill("+919446000001");
  await expect(page.getByTestId("apply")).toBeEnabled();

  await page
    .getByRole("checkbox", {
      name: "Send an invitation email instead of a password",
    })
    .click();
  await expect(page.getByTestId("member-0-email-error")).toHaveText(
    /Enter an email address/,
  );
  await expect(page.getByTestId("apply")).toBeDisabled();

  await page.getByTestId("member-0-email").fill("anil@clinic.example");
  await expect(page.getByTestId("member-0-email-error")).toHaveCount(0);
  await page.getByTestId("apply").click();
  await expect(page.getByTestId("result-done")).toBeVisible();
  const staff = backend.bodies.find((b) => b.step === "staff") as {
    members: Record<string, unknown>[];
  };
  expect(staff.members[0]).toMatchObject({
    invite: true,
    email: "anil@clinic.example",
  });
});
