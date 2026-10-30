import { expect, test } from "../fixtures";
import { existingState, note, shot } from "./real";

// After "care-pi auth sessions revoke <ref>" of the phone session: the next request gives 401, and the console
// opens the login page.
const state = existingState("phone");
test.use({ storageState: state });

test("a revoked session goes to the login page", async ({ page }, info) => {
  test.skip(!state, "run phone.ready.spec.ts first (STATE_DIR/phone.json)");
  const session = await page.request.get("/api/v1/session");
  note(info, `GET /api/v1/session with the revoked cookie: ${session.status()}`);
  expect(session.status()).toBe(401);
  expect((await session.json()).type).toMatch(/not-authenticated$/);
  await page.goto("/dashboard");
  await expect(page).toHaveURL(/\/login\?next=%2Fdashboard$/);
  await shot(page, info, "20-revoked-login");
});
