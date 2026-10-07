import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ScanReport, ScanStatus } from "../api/client";
import StreamHealthPanel from "./StreamHealthPanel";
import { SCAN_POLL_INTERVAL_MS } from "./scanHealth";

// A minimal Response the component's api layer can consume (JSON path).
function makeResponse(status: number, body: unknown): Response {
  const text = body === undefined ? "" : JSON.stringify(body);
  return {
    ok: status >= 200 && status < 300,
    status,
    text: async () => text,
    json: async () => JSON.parse(text) as unknown,
  } as Response;
}

// A Response for the download path: it needs headers.get and blob().
function fileResponse(status: number, body: string, headers: Record<string, string> = {}): Response {
  const lower = new Map(Object.entries(headers).map(([k, v]) => [k.toLowerCase(), v]));
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: { get: (name: string) => lower.get(name.toLowerCase()) ?? null },
    text: async () => body,
    json: async () => JSON.parse(body) as unknown,
    blob: async () => new Blob([body]),
  } as unknown as Response;
}

const IDLE: ScanStatus = { state: "idle", total: 0, probed: 0, alive: 0, dead: 0, has_report: false };
const RUNNING: ScanStatus = {
  state: "running",
  total: 10,
  probed: 4,
  alive: 3,
  dead: 1,
  has_report: false,
};
const DONE: ScanStatus = {
  state: "done",
  total: 10,
  probed: 10,
  alive: 7,
  dead: 3,
  has_report: true,
};

const REPORT: ScanReport = {
  started_at: "2026-01-01T11:00:00Z",
  finished_at: "2026-01-01T11:02:00Z",
  total_channels: 2,
  alive_channels: 1,
  dead_channels: 1,
  total_streams: 2,
  alive_streams: 1,
  dead_streams: 1,
  // Deliberately alive-first: the panel must surface the dead row ahead of it.
  streams: [
    {
      channel_id: "c1",
      channel_name: "News One",
      category: "News",
      stream_id: "s1",
      label: "HD",
      host: "cdn.example",
      manifest: "index.m3u8",
      alive: true,
      probed_at: "2026-01-01T11:00:10Z",
    },
    {
      channel_id: "c2",
      channel_name: "Sports HD",
      category: "Sports",
      stream_id: "s2",
      label: "Main",
      host: "dead.example",
      manifest: "live.mpd",
      alive: false,
      http_status: 503,
      reason: "HTTP 503 Service Unavailable",
      probed_at: "2026-01-01T11:00:11Z",
    },
  ],
};

// jsdom does not implement createObjectURL; install recording stand-ins so the
// download path can run and its object URL lifecycle can be asserted.
const createObjectURL = vi.fn(() => "blob:mock-url");
const revokeObjectURL = vi.fn();
Object.defineProperty(URL, "createObjectURL", { configurable: true, value: createObjectURL });
Object.defineProperty(URL, "revokeObjectURL", { configurable: true, value: revokeObjectURL });

interface FetchCall {
  url: string;
  method: string;
}

// installBackend serves the three scan endpoints and records every call. The
// `status` callback is read on each poll, so a test can drive the scan through
// running → done.
function installBackend(opts: {
  status: () => ScanStatus;
  start?: () => Response;
  report?: () => Response;
}) {
  const calls: FetchCall[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = (init?.method ?? "GET").toUpperCase();
      calls.push({ url, method });

      if (url.includes("/api/v1/channels/scan/status")) {
        return makeResponse(200, { status: opts.status() });
      }
      if (url.includes("/api/v1/channels/scan/report")) {
        if (opts.report) return opts.report();
        return fileResponse(200, JSON.stringify(REPORT), {
          "content-disposition": 'attachment; filename="adoboflix-scan-report-20260101-110200.json"',
        });
      }
      if (url.endsWith("/api/v1/channels/scan") && method === "POST") {
        if (opts.start) return opts.start();
        return makeResponse(202, { status: RUNNING, message: "scan started" });
      }
      throw new Error(`unexpected fetch: ${method} ${url}`);
    }),
  );
  const statusCalls = () => calls.filter((c) => c.url.includes("/api/v1/channels/scan/status")).length;
  return { calls, statusCalls };
}

function renderPanel() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <StreamHealthPanel />
    </QueryClientProvider>,
  );
  return { queryClient };
}

afterEach(() => {
  vi.useRealTimers();
});

describe("StreamHealthPanel", () => {
  it("starts a scan when idle and shows the running progress", async () => {
    let state = IDLE;
    const backend = installBackend({
      status: () => state,
      start: () => {
        state = RUNNING;
        return makeResponse(202, { status: RUNNING, message: "scan started" });
      },
    });
    renderPanel();

    await screen.findByText(/no scan yet/i);
    fireEvent.click(screen.getByRole("button", { name: /scan now/i }));

    await waitFor(() =>
      expect(backend.calls.some((c) => c.method === "POST" && c.url.endsWith("/api/v1/channels/scan"))).toBe(true),
    );
    expect(await screen.findByText(/scan in progress/i)).toBeInTheDocument();
    // The progress counters are shown, not just the headline.
    expect(screen.getByText("4 / 10")).toBeInTheDocument();
  });

  it("adopts a running scan on a 409 instead of showing an error", async () => {
    let state = IDLE;
    installBackend({
      status: () => state,
      start: () => {
        // The server already had a scan going: it refuses this one and returns
        // the running scan's status.
        state = RUNNING;
        return makeResponse(409, { status: RUNNING, message: "a scan is already in progress" });
      },
    });
    renderPanel();

    await screen.findByText(/no scan yet/i);
    fireEvent.click(screen.getByRole("button", { name: /scan now/i }));

    // Positive: the running scan is adopted and rendered as progress.
    expect(await screen.findByText(/scan in progress/i)).toBeInTheDocument();
    // Negative: a 409 is not a failure from the user's point of view.
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("polls while the scan runs and stops once it is terminal", async () => {
    vi.useFakeTimers();
    let reads = 0;
    const backend = installBackend({
      status: () => {
        reads += 1;
        return reads <= 2 ? RUNNING : DONE;
      },
      report: () => makeResponse(200, REPORT),
    });
    renderPanel();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(screen.getByText(/scan in progress/i)).toBeInTheDocument();

    // Two poll intervals: the second read still runs, the third returns done.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(SCAN_POLL_INTERVAL_MS + 100);
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(SCAN_POLL_INTERVAL_MS + 100);
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(SCAN_POLL_INTERVAL_MS + 100);
    });

    // Positive: polling genuinely happened — several reads of the status endpoint.
    expect(backend.statusCalls()).toBeGreaterThanOrEqual(3);
    expect(screen.getByText(/scan complete/i)).toBeInTheDocument();

    const settled = backend.statusCalls();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(SCAN_POLL_INTERVAL_MS * 4);
    });
    // Negative: once terminal, the polls have stopped.
    expect(backend.statusCalls()).toBe(settled);
  });

  it("renders the report rows with the dead ones surfaced first", async () => {
    installBackend({ status: () => DONE, report: () => makeResponse(200, REPORT) });
    renderPanel();

    const rows = await screen.findAllByRole("row");
    // Header row plus the two data rows.
    expect(rows).toHaveLength(3);
    // Dead row is first even though the payload listed it second.
    expect(rows[1]).toHaveTextContent("Dead");
    expect(rows[1]).toHaveTextContent("Sports HD");
    // The alive row is still rendered below it.
    expect(rows[2]).toHaveTextContent("Alive");
    expect(rows[2]).toHaveTextContent("News One");
    // The redacted host and manifest are shown as-is.
    expect(screen.getByText("dead.example")).toBeInTheDocument();
    expect(screen.getByText("live.mpd")).toBeInTheDocument();
  });

  it("downloads both report formats through the blob path", async () => {
    const backend = installBackend({
      status: () => DONE,
      report: () =>
        fileResponse(200, JSON.stringify(REPORT), {
          "content-disposition": 'attachment; filename="adoboflix-scan-report-20260101-110200.json"',
        }),
    });
    renderPanel();

    fireEvent.click(await screen.findByRole("button", { name: /download csv/i }));
    await waitFor(() =>
      expect(backend.calls.some((c) => c.url.includes("/api/v1/channels/scan/report?format=csv"))).toBe(true),
    );

    fireEvent.click(screen.getByRole("button", { name: /download json/i }));
    await waitFor(() =>
      expect(
        backend.calls.some(
          (c) => c.url.includes("/api/v1/channels/scan/report") && !c.url.includes("format=csv"),
        ),
      ).toBe(true),
    );
    // The blob path ran (a download URL was created), so the download did not
    // silently no-op.
    expect(createObjectURL).toHaveBeenCalled();
  });

  it("renders a 404 report as copy rather than throwing", async () => {
    installBackend({
      status: () => DONE,
      report: () => makeResponse(404, { error: "no scan report available" }),
    });
    renderPanel();

    // Positive: the mapped copy is shown.
    expect(await screen.findByText(/no report yet/i)).toBeInTheDocument();
    // Negative: the raw server error string does not reach the screen.
    expect(screen.queryByText(/no scan report available/i)).not.toBeInTheDocument();
  });
});
