import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "@/api/client";
import type { ClaimMethod } from "@/api/types";
import ClaimPage from "@/features/claim/claim-page";
import { looksLikeClaimCode } from "@/lib/codes";

const status = vi.hoisted(() => ({ methods: [] as ClaimMethod[], firstbootError: null as string | null }));

vi.mock("@/api/queries", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/queries")>();
  return {
    ...actual,
    useSession: () => ({ data: null }),
    usePublicStatus: () => ({
      isPending: false,
      isError: false,
      data: { setup_state: "unclaimed", claim_methods: status.methods, firstboot_error: status.firstbootError },
    }),
  };
});

function renderPage() {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter initialEntries={["/claim"]}>
        <ClaimPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

const CODE_LABEL = "Claim code";
const OS_LABEL = "Password of the device user";

describe("claim page", () => {
  beforeEach(() => {
    status.methods = [];
    status.firstbootError = null;
  });

  it("shows no pre-seed error without one", () => {
    status.methods = ["os-password", "claim-code"];
    renderPage();
    expect(screen.queryByText("The pre-seed was not used")).toBeNull();
  });

  it("shows the error of the pre-seed above the claim form", () => {
    status.methods = ["os-password", "claim-code"];
    status.firstbootError = "The first boot did not use the pre-seed: the pre-seed is not valid.";
    renderPage();
    expect(screen.getByText("The pre-seed was not used")).toBeInTheDocument();
    expect(screen.getByText(status.firstbootError)).toBeInTheDocument();
    expect(screen.getByLabelText(OS_LABEL)).toBeInTheDocument();
  });

  it("shows the field of the first claim method: claim-code after an installer install", () => {
    status.methods = ["claim-code", "os-password"];
    renderPage();
    expect(screen.getByLabelText(CODE_LABEL)).toBeInTheDocument();
    expect(screen.queryByLabelText(OS_LABEL)).toBeNull();
    expect(screen.getByRole("button", { name: "Use the Imager password" })).toBeInTheDocument();
  });

  it("switches to the claim code when the operator enters a claim code as the Imager password", async () => {
    status.methods = ["os-password", "claim-code"];
    renderPage();
    await userEvent.type(screen.getByLabelText(OS_LABEL), "4F7K-Q2MX-9PDA");
    const field = screen.getByLabelText(CODE_LABEL);
    expect(field).toHaveValue("4F7K-Q2MX-9PDA");
    expect(field).toHaveFocus();
    expect(screen.queryByLabelText(OS_LABEL)).toBeNull();
  });

  it("keeps the Imager password field for a password", async () => {
    status.methods = ["os-password", "claim-code"];
    renderPage();
    await userEvent.type(screen.getByLabelText(OS_LABEL), "4f7k-q2mx-9pda and more");
    expect(screen.getByLabelText(OS_LABEL)).toHaveValue("4f7k-q2mx-9pda and more");
  });

  it("sends the time of the browser with the claim (S52)", async () => {
    status.methods = ["claim-code", "os-password"];
    const post = vi.spyOn(api, "POST").mockResolvedValue({
      data: undefined,
      error: { type: "about:blank", title: "x", status: 401 },
      response: new Response(null, { status: 401 }),
    } as never);
    renderPage();
    await userEvent.type(screen.getByLabelText(CODE_LABEL), "4F7K-Q2MX-9PDA");
    await userEvent.type(screen.getByLabelText("New console password"), "a long console password");
    await userEvent.type(screen.getByLabelText("Type the password again"), "a long console password");
    const before = Date.now();
    await userEvent.click(screen.getByRole("button", { name: "Claim the device" }));
    expect(post).toHaveBeenCalledTimes(1);
    const body = (post.mock.calls[0]![1] as { body: { client_time: string } }).body;
    expect(body.client_time).toMatch(/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$/);
    expect(Math.abs(Date.parse(body.client_time) - before)).toBeLessThan(10_000);
    post.mockRestore();
  });

  it("keeps the Imager password field after the operator chose it", async () => {
    status.methods = ["claim-code", "os-password"];
    renderPage();
    await userEvent.click(screen.getByRole("button", { name: "Use the Imager password" }));
    await userEvent.type(screen.getByLabelText(OS_LABEL), "ABCD-EFGH-JKMN");
    expect(screen.getByLabelText(OS_LABEL)).toHaveValue("ABCD-EFGH-JKMN");
  });
});

describe("looksLikeClaimCode", () => {
  it("matches the form of the claim code of the installer", () => {
    expect(looksLikeClaimCode("4F7K-Q2MX-9PDA")).toBe(true);
    expect(looksLikeClaimCode(" 4F7K-Q2MX-9PDA ")).toBe(true);
    expect(looksLikeClaimCode("4f7k-q2mx-9pda")).toBe(false);
    expect(looksLikeClaimCode("4F7KQ2MX9PDA")).toBe(false);
    expect(looksLikeClaimCode("4F7K-Q2MX-9PD")).toBe(false);
  });
});
