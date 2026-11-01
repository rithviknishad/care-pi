import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Network, TlsState } from "@/api/types";
import { ReauthProvider } from "@/components/app/reauth";
import { ConnectivityTab } from "@/features/connectivity/connectivity-tab";

const data = vi.hoisted(() => ({ tls: null as unknown, network: null as unknown }));

vi.mock("@/api/queries", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/queries")>();
  return {
    ...actual,
    useTls: () => ({ data: data.tls, isError: false }),
    useNetwork: () => ({ data: data.network, isError: false }),
    useWifiScan: () => ({ data: undefined, isFetching: false, error: null, refetch: vi.fn() }),
  };
});

function tls(over: Partial<TlsState> = {}): TlsState {
  return {
    local_ca: {
      enabled: true,
      root: null,
      hostnames: ["care.local"],
      uncovered: [],
      ca_changed_at: null,
      previous_fingerprint_sha256: null,
      cert_page_url: "https://care-pi.rithviknishad.dev/cert",
    },
    public: {
      enabled: false,
      clinic_id: null,
      hostname: null,
      acme_ca: null,
      certificate: null,
      dns: { address: null, updated_at: null, last_error: null, last_error_at: null, revoked: false },
      primary: false,
      health_url: null,
    },
    tailscale: {
      enabled: false,
      hostname: null,
      installed: false,
      backend_state: null,
      login_url: null,
      tailnet: null,
      ipv4: null,
      https_enabled: false,
      published: false,
    },
    ...over,
  };
}

const network = {
  dhcp_hints: [{ interface: "eth0", mac: "d8:3a:dd:00:11:22", address: "192.168.1.10" }],
  wifi: {
    ssid: null,
    country: null,
    failover: false,
    connected: false,
    device: "wlan0",
    radio: true,
    active_ssid: null,
    signal: null,
    address: null,
    default_route: false,
    profiles: [{ name: "preconfigured", ssid: "Clinic-Setup", autoconnect: true, active: false, managed: false }],
  },
} as unknown as Network;

function renderTab() {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter>
        <ReauthProvider>
          <ConnectivityTab hostname="care" />
        </ReauthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("connectivity tab", () => {
  beforeEach(() => {
    data.tls = tls();
    data.network = network;
  });

  it("offers each mode when it is off, with the hint and the Wi-Fi form", async () => {
    renderTab();
    const pub = screen.getByTestId("public-card");
    expect(within(pub).getByLabelText("Start of the name")).toHaveValue("care");
    await userEvent.clear(within(pub).getByLabelText("Start of the name"));
    await userEvent.type(within(pub).getByLabelText("Start of the name"), "bad-");
    expect(within(pub).getByText(/Use 1 to 63 characters/)).toBeInTheDocument();
    expect(within(pub).getByTestId("public-on")).toBeDisabled();
    expect(within(screen.getByTestId("tailscale-card")).getByTestId("tailscale-on")).toBeEnabled();
    const wifi = screen.getByTestId("wifi-card");
    expect(within(wifi).getByTestId("wifi-presets")).toHaveTextContent("Clinic-Setup");
    expect(within(wifi).getByTestId("dhcp-hints")).toHaveTextContent("eth0: d8:3a:dd:00:11:22 → 192.168.1.10");
    expect(within(wifi).getByTestId("wifi-failover")).toBeChecked();
  });

  it("shows the public name, its certificate, and the bookmark advice", () => {
    data.tls = tls({
      public: {
        enabled: true,
        clinic_id: "sunrise-k3x9",
        hostname: "sunrise-k3x9.care-pi.rithviknishad.dev",
        acme_ca: null,
        certificate: { state: "issued", not_after: "2027-01-30T10:00:00Z", issuer: "E6", staging: false },
        dns: {
          address: "192.168.1.10",
          updated_at: "2026-11-01T10:00:00Z",
          last_error: null,
          last_error_at: null,
          revoked: false,
        },
        primary: true,
        health_url: "https://sunrise-k3x9.care-pi.rithviknishad.dev/_care-pi/health",
      },
    });
    renderTab();
    const pub = screen.getByTestId("public-card");
    expect(within(pub).getByTestId("public-name")).toHaveAttribute(
      "href",
      "https://sunrise-k3x9.care-pi.rithviknishad.dev/",
    );
    expect(within(pub).getByTestId("public-cert")).toHaveTextContent("Issued");
    expect(within(pub).getByTestId("public-bookmark")).toHaveTextContent(
      "https://sunrise-k3x9.care-pi.rithviknishad.dev/",
    );
    expect(within(pub).getByTestId("rebinding-check")).toBeInTheDocument();
    expect(within(pub).getByTestId("public-off")).toBeEnabled();
  });

  it("asks for a new name after a revocation, and shows a Tailscale name with its warnings", () => {
    data.tls = tls({
      public: {
        ...tls().public,
        enabled: true,
        clinic_id: "x-abcd",
        hostname: "x-abcd.care-pi.rithviknishad.dev",
        certificate: { state: "error", not_after: null, issuer: null, staging: false },
        dns: { address: null, updated_at: null, last_error: "HTTP 410 revoked", last_error_at: null, revoked: true },
      },
      tailscale: {
        enabled: true,
        hostname: "care.tail1234.ts.net",
        installed: true,
        backend_state: "Running",
        login_url: null,
        tailnet: "clinic@example.com",
        ipv4: "100.101.102.103",
        https_enabled: false,
        published: false,
      },
    });
    renderTab();
    expect(screen.getByTestId("public-card")).toHaveTextContent("The DNS service deleted this name");
    const ts = screen.getByTestId("tailscale-card");
    expect(ts).toHaveTextContent("care.tail1234.ts.net");
    expect(ts).toHaveTextContent("Turn on HTTPS Certificates");
    expect(ts).toHaveTextContent("The next apply adds it");
  });

  it("tells that the device has no Wi-Fi", () => {
    data.network = { ...network, wifi: null } as Network;
    renderTab();
    expect(screen.getByTestId("wifi-no-device")).toBeInTheDocument();
  });
});
