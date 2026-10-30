import { expect, test } from "../fixtures";
import { existingState, note, shot } from "./real";

// GET /jobs/<id>/events of a job that does not exist gives 404 job-not-found. The progress page must show the
// problem and stop its reads (a 4xx other than 429 does not change). The test gives the page a job id that does
// not exist: it changes job_id in the response of GET /setup.
const state = existingState("desktop");
test.use({ storageState: state });

test("the job progress stops on 404 job-not-found", async ({ page }, info) => {
  test.skip(!state, "run desktop.setup.spec.ts first (STATE_DIR/desktop.json)");
  const fake = "20000101T000000.000Z-setup";
  await page.route("**/api/v1/setup", async (route) => {
    const res = await route.fetch();
    const body = await res.json();
    await route.fulfill({ response: res, json: { ...body, job_id: fake } });
  });
  const reads: string[] = [];
  page.on("request", (r) => {
    if (r.url().includes(`/api/v1/jobs/${fake}/events`)) reads.push(`${new Date().toISOString()} ${r.url()}`);
  });
  const statuses: number[] = [];
  page.on("response", (r) => {
    if (r.url().includes(`/api/v1/jobs/${fake}/events`)) statuses.push(r.status());
  });
  await page.goto("/setup/progress");
  await expect(page.getByText("The job does not exist.")).toBeVisible();
  await page.waitForTimeout(12_000);
  note(info, `reads of the missing job in 12 s: ${reads.length} (statuses ${statuses.join(",")})`);
  await shot(page, info, "21-job-404");
  expect(statuses.every((s) => s === 404)).toBe(true);
  expect(reads.length).toBeLessThanOrEqual(3);
});
