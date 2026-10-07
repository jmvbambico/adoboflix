import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { SourceStatus } from "../api/client";
import Dashboard from "./Dashboard";
import { SOURCE_STATUS_QUERY_KEY } from "./playlistCode";

function makeResponse(status: number, body: unknown): Response {
  const text = body === undefined ? "" : JSON.stringify(body);
  return {
    ok: status >= 200 && status < 300,
    status,
    text: async () => text,
    json: async () => JSON.parse(text) as unknown,
  } as Response;
}

// The SourceStatus contract. health_scan_supported is the flag that gates the
// tab, so tests drive it directly.
function makeStatus(overrides: Partial<SourceStatus> = {}): SourceStatus {
  return {
    source: "file",
    active: true,
    origin: "stored",
    dev: false,
    needs_playlist_code: false,
    playlist_code_configured: false,
    playlist_file_configured: true,
    health_scan_supported: true,
    modes: [],
    ...overrides,
  };
}

// installBackend serves every read the Dashboard makes on mount, plus the scan
// status the health panel asks for once its tab is opened. It records every call
// and returns the mutable status object it serialized, so a test can both flip
// the response for a later refetch and count reads.
function installBackend(status: SourceStatus, options: { entriesFail?: boolean } = {}) {
  const calls: string[] = [];
  const methods: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = (init?.method ?? "GET").toUpperCase();
      calls.push(url);
      methods.push(method);
      if (url.endsWith("/api/v1/source/status")) return makeResponse(200, status);
      if (url.includes("/api/v1/entries")) {
        if (options.entriesFail) return makeResponse(500, { error: "library read failed" });
        return makeResponse(200, { entries: [], total: 0, page: 1, has_more: false });
      }
      if (url.endsWith("/api/v1/genres")) return makeResponse(200, { genres: [] });
      if (url.endsWith("/api/v1/stats")) {
        return makeResponse(200, { total_titles: 0, total_providers: 0, total_genres: 0 });
      }
      if (url.endsWith("/api/v1/channels/categories")) return makeResponse(200, { categories: [] });
      // The scan START: a POST to the bare scan URL answers 202 with the
      // running status, so a test can prove the scan was really requested.
      if (url.endsWith("/api/v1/channels/scan") && method === "POST") {
        return makeResponse(202, {
          status: { state: "running", total: 0, probed: 0, alive: 0, dead: 0, has_report: false },
          message: "scan started",
        });
      }
      if (url.includes("/api/v1/channels/scan/status")) {
        return makeResponse(200, {
          status: { state: "idle", total: 0, probed: 0, alive: 0, dead: 0, has_report: false },
        });
      }
      if (url.includes("/api/v1/channels")) {
        return makeResponse(200, { channels: [], total: 0, page: 1, has_more: false });
      }
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
  const statusReads = () => calls.filter((u) => u.endsWith("/api/v1/source/status")).length;
  // A method-aware count: only the scan POST targets the bare scan URL, so this
  // proves the POST fired rather than merely that a panel rendered.
  const scanPosts = () =>
    calls.filter((u, i) => u.endsWith("/api/v1/channels/scan") && methods[i] === "POST").length;
  return { calls, statusReads, scanPosts };
}

function renderDashboard(status: SourceStatus, options: { entriesFail?: boolean } = {}) {
  const backend = installBackend(status, options);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <Dashboard />
    </QueryClientProvider>,
  );
  return { queryClient, backend };
}

describe("Dashboard stream health tab", () => {
  // The positive half of the pair: the same query used below must FIND the tab
  // when the source supports scanning, proving the absence assertion can
  // distinguish a present tab from an absent one.
  it("offers the Stream Health tab when the source reports it can be scanned", async () => {
    renderDashboard(makeStatus({ health_scan_supported: true }));

    expect(await screen.findByRole("button", { name: /stream health/i })).toBeInTheDocument();
  });

  it("does not offer the Stream Health tab when the source cannot be scanned", async () => {
    const { queryClient } = renderDashboard(makeStatus({ health_scan_supported: false }));

    // Positive anchor: the tab bar rendered with the always-present Browse tab
    // and the status resolved, so the absence below is measured on a live tab
    // bar rather than a page that simply has not loaded.
    expect(await screen.findByRole("button", { name: /browse catalog/i })).toBeInTheDocument();
    await waitFor(() => expect(queryClient.getQueryData(SOURCE_STATUS_QUERY_KEY)).toBeDefined());

    expect(screen.queryByRole("button", { name: /stream health/i })).not.toBeInTheDocument();
  });

  it("falls back to Browse when scan support disappears while the tab is open", async () => {
    const status = makeStatus({ health_scan_supported: true });
    const { queryClient } = renderDashboard(status);

    fireEvent.click(await screen.findByRole("button", { name: /stream health/i }));
    // Positive: the scan panel really mounted, so the fallback below is a real
    // transition and not a panel that never rendered.
    expect(await screen.findByRole("heading", { name: /stream health scan/i })).toBeInTheDocument();

    // The active source swaps to one that cannot be scanned (adobotv-http, by
    // design) while the user is on the tab. The stub object is flipped too, so a
    // later refetch cannot silently restore the old, scannable source.
    const swapped = makeStatus({
      source: "adobotv-http",
      needs_playlist_code: true,
      health_scan_supported: false,
    });
    Object.assign(status, swapped);
    await act(async () => {
      queryClient.setQueryData(SOURCE_STATUS_QUERY_KEY, swapped);
    });
    // Positive: the swap really landed in the cache, so the fallback below is
    // not just a re-render against unchanged data.
    expect(queryClient.getQueryData(SOURCE_STATUS_QUERY_KEY)).toMatchObject({
      health_scan_supported: false,
    });

    // Positive: the user lands on the Browse panel, not a dead screen.
    expect(await screen.findByText(/no transmission anchored/i)).toBeInTheDocument();
    // Negative: the scan tab is no longer offered, and its panel is gone.
    await waitFor(() =>
      expect(screen.queryByRole("heading", { name: /stream health scan/i })).not.toBeInTheDocument(),
    );
    expect(screen.queryByRole("button", { name: /stream health/i })).not.toBeInTheDocument();
  });

  // The reviewer's defect: a failing VOD read used to replace EVERY panel, so
  // the user whose library is broken could not reach the diagnostic they need.
  it("keeps the health tab reachable and its scan runnable when the VOD read fails", async () => {
    const { backend } = renderDashboard(makeStatus({ health_scan_supported: true }), {
      entriesFail: true,
    });

    // The health tab is still offered even though /entries failed: the tab bar
    // is not gated on the library read.
    fireEvent.click(await screen.findByRole("button", { name: /stream health/i }));
    // Positive: the scan panel really mounted despite the library failure.
    expect(await screen.findByRole("heading", { name: /stream health scan/i })).toBeInTheDocument();
    // The inline note explains the missing catalogue without reproducing the
    // full source-error panel inside the health tab.
    expect(await screen.findByText(/video library could not be read/i)).toBeInTheDocument();

    // Positive: a scan can actually be STARTED, not merely displayed — assert
    // the POST reached the backend, not just that a button rendered.
    fireEvent.click(screen.getByRole("button", { name: /scan now/i }));
    await waitFor(() => expect(backend.scanPosts()).toBeGreaterThan(0));
  });

  // The pairing that keeps the test above honest: in the very same failed-read
  // state, a non-health tab keeps its current behaviour and shows the
  // source-error panel.
  it("still shows the source-error panel on a non-health tab when the VOD read fails", async () => {
    renderDashboard(makeStatus({ health_scan_supported: true }), { entriesFail: true });

    expect(await screen.findByRole("heading", { name: /something went wrong/i })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: /stream health scan/i })).not.toBeInTheDocument();
  });
});
