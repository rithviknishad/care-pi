import { test } from "../fixtures";
import { env } from "./real";
import { setupJourney } from "./setup-journey";

// The first contact on a new device from a phone. The device must be in setup mode and unclaimed
// (install.sh without --unattended). setup-journey.ts has the steps.
test("claim the device and finish the setup", async ({ page }, info) => {
  test.skip(!env.claimCode || !env.consolePassword || !env.carePassword, "set CLAIM_CODE, CONSOLE_PASSWORD, CARE_*");
  test.setTimeout(env.setupTimeoutMs + 5 * 60_000);
  await setupJourney(page, info, "phone");
});
