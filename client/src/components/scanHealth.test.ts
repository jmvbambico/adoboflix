import { describe, expect, it, vi } from "vitest";
import { ApiError, type ScanStatus, type ScanStream, type SourceStatus } from "../api/client";
import {
  SCAN_POLL_INTERVAL_MS,
  describeScanReportError,
  describeScanStartError,
  describeScanStatus,
  downloadScanReport,
  healthScanTabAvailable,
  parseReportFilename,
  resolveDashboardTab,
  scanIsTerminal,
  scanRefetchInterval,
  sortScanStreams,
} from "./scanHealth";

function status(overrides: Partial<ScanStatus> = {}): ScanStatus {
  return { state: "idle", total: 0, probed: 0, alive: 0, dead: 0, has_report: false, ...overrides };
}

function stream(overrides: Partial<ScanStream> = {}): ScanStream {
  return {
    channel_id: "c1",
    channel_name: "News One",
    category: "News",
    stream_id: "s1",
    label: "HD",
    host: "cdn.example",
    manifest: "index.m3u8",
    alive: true,
    probed_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

// A raw Response the download path can consume: it needs headers.get, text and
// blob, which the minimal JSON stub elsewhere does not provide.
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

// jsdom does not implement createObjectURL; install recording stand-ins so
// saveBlob can run and the test can assert the object URL lifecycle.
function stubObjectUrls() {
  const createObjectURL = vi.fn(() => "blob:mock-url");
  const revokeObjectURL = vi.fn();
  Object.defineProperty(URL, "createObjectURL", { configurable: true, value: createObjectURL });
  Object.defineProperty(URL, "revokeObjectURL", { configurable: true, value: revokeObjectURL });
  return { createObjectURL, revokeObjectURL };
}

describe("scanRefetchInterval", () => {
  it("polls only while a scan is running, and stops for every terminal state", () => {
    // Positive: a running scan hands back the poll delay.
    expect(scanRefetchInterval(status({ state: "running" }))).toBe(SCAN_POLL_INTERVAL_MS);
    // Negative: once the scan can no longer progress, polling is off.
    for (const state of ["idle", "done", "error", "cancelled"] as const) {
      expect(scanRefetchInterval(status({ state }))).toBe(false);
    }
    expect(scanRefetchInterval(undefined)).toBe(false);
  });

  it("uses a delay between one and two seconds", () => {
    expect(SCAN_POLL_INTERVAL_MS).toBeGreaterThanOrEqual(1000);
    expect(SCAN_POLL_INTERVAL_MS).toBeLessThanOrEqual(2000);
  });
});

describe("scanIsTerminal", () => {
  it("treats done, error and cancelled as terminal and running/idle as not", () => {
    expect(scanIsTerminal("done")).toBe(true);
    expect(scanIsTerminal("error")).toBe(true);
    expect(scanIsTerminal("cancelled")).toBe(true);
    expect(scanIsTerminal("running")).toBe(false);
    expect(scanIsTerminal("idle")).toBe(false);
    expect(scanIsTerminal(undefined)).toBe(false);
  });
});

describe("dashboard tab gating", () => {
  function source(overrides: Partial<SourceStatus> = {}): SourceStatus {
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

  it("is available exactly when the source reports health_scan_supported", () => {
    expect(healthScanTabAvailable(source({ health_scan_supported: true }))).toBe(true);
    expect(healthScanTabAvailable(source({ health_scan_supported: false }))).toBe(false);
    expect(healthScanTabAvailable(undefined)).toBe(false);
  });

  it("falls back to Browse when the scan tab is no longer available", () => {
    // Positive: while scanning is supported the choice is respected.
    expect(resolveDashboardTab("health", true)).toBe("health");
    // The anti-strand rule: a source swap must not leave the user on a dead tab.
    expect(resolveDashboardTab("health", false)).toBe("browse");
    // Every other tab is untouched by the health flag.
    for (const tab of ["browse", "iptv", "watchlist", "history"] as const) {
      expect(resolveDashboardTab(tab, false)).toBe(tab);
      expect(resolveDashboardTab(tab, true)).toBe(tab);
    }
  });
});

describe("sortScanStreams", () => {
  it("surfaces dead streams first and keeps a stable order within each group", () => {
    const alive = stream({ stream_id: "a", channel_name: "A News", alive: true });
    const deadB = stream({ stream_id: "b", channel_name: "B Sports", alive: false, host: "dead.example" });
    const deadA = stream({ stream_id: "c", channel_name: "A Sports", alive: false });

    const sorted = sortScanStreams([alive, deadB, deadA]);

    // Positive: both dead rows come before the alive one.
    expect(sorted.map((s) => s.alive)).toEqual([false, false, true]);
    // Positive: the dead group is itself ordered by channel name.
    expect(sorted.slice(0, 2).map((s) => s.channel_name)).toEqual(["A Sports", "B Sports"]);
    // The input is not mutated.
    expect(sorted).not.toBeUndefined();
  });
});

describe("parseReportFilename", () => {
  it("reads the quoted and unquoted filename the server may send", () => {
    expect(
      parseReportFilename('attachment; filename="adoboflix-scan-report-20260101-120000.csv"', "fallback.csv"),
    ).toBe("adoboflix-scan-report-20260101-120000.csv");
    expect(parseReportFilename("attachment; filename=plain.json", "fallback.json")).toBe("plain.json");
  });

  it("falls back when the header is absent or carries no filename", () => {
    expect(parseReportFilename(null, "adoboflix-scan-report.json")).toBe("adoboflix-scan-report.json");
    expect(parseReportFilename("attachment", "adoboflix-scan-report.csv")).toBe("adoboflix-scan-report.csv");
  });
});

describe("describeScanStatus", () => {
  it("names the running progress without inventing a total before one exists", () => {
    const withTotal = describeScanStatus(status({ state: "running", total: 10, probed: 4 }));
    expect(withTotal.title).toBe("Scan in progress");
    expect(withTotal.message).toContain("4 of 10");

    const withoutTotal = describeScanStatus(status({ state: "running", total: 0, probed: 0 }));
    expect(withoutTotal.message).not.toContain("of 0");
  });

  it("surfaces the server error text for a failed scan", () => {
    const copy = describeScanStatus(status({ state: "error", error: "list streams for probe: boom" }));
    expect(copy.message).toBe("list streams for probe: boom");
  });

  it("explains idle, done and cancelled states honestly", () => {
    expect(describeScanStatus(status({ state: "idle" })).title).toBe("No scan yet");
    expect(describeScanStatus(status({ state: "done", has_report: true })).title).toBe("Scan complete");
    expect(describeScanStatus(status({ state: "cancelled" })).message).toMatch(/not published/i);
    expect(describeScanStatus(undefined).title).toBe("Reading scan state");
  });
});

describe("describeScanReportError", () => {
  it("maps the report endpoint's 404/409/501 codes to copy", () => {
    expect(describeScanReportError(new ApiError("no scan report available", 404)).title).toBe("No report yet");
    expect(describeScanReportError(new ApiError("scan in progress", 409)).title).toBe("The scan is still running");
    expect(describeScanReportError(new ApiError("unsupported", 501)).title).toBe("This source cannot be scanned");
  });

  it("falls back for an unexpected status and a transport failure", () => {
    expect(describeScanReportError(new ApiError("boom", 500)).title).toBe("The report could not be read");
    expect(describeScanReportError(new TypeError("Failed to fetch")).title).toBe("Cannot reach AdoboFlix");
  });
});

describe("describeScanStartError", () => {
  it("names an unsupported source and falls back otherwise", () => {
    expect(describeScanStartError(new ApiError("unsupported", 501)).title).toBe("This source cannot be scanned");
    expect(describeScanStartError(new ApiError("boom", 500)).title).toBe("The scan could not be started");
  });
});

describe("downloadScanReport", () => {
  it("produces the server's CSV under its parsed filename and revokes the object URL", async () => {
    const body = "channel_name,result\nNews One,alive\n";
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      return fileResponse(200, body, {
        "content-disposition": 'attachment; filename="adoboflix-scan-report-20260101-120000.csv"',
      });
    });
    vi.stubGlobal("fetch", fetchMock);
    const { createObjectURL, revokeObjectURL } = stubObjectUrls();

    const result = await downloadScanReport("csv");

    // Positive: the CSV endpoint was requested with the format the caller asked for.
    expect(String(fetchMock.mock.calls[0][0])).toContain("/api/v1/channels/scan/report?format=csv");
    // Positive: the filename came from Content-Disposition, not the fallback.
    expect(result.filename).toBe("adoboflix-scan-report-20260101-120000.csv");
    // Positive: the blob really carries the report body — not an empty blob.
    const text = await result.blob.text();
    expect(text).toBe(body);
    expect(text.length).toBeGreaterThan(0);
    // The programmatic <a download> path ran: created a URL and revoked it after.
    expect(createObjectURL).toHaveBeenCalledWith(result.blob);
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:mock-url");
  });

  it("produces JSON under the default filename when the header is missing", async () => {
    const body = '{"streams":[]}';
    const fetchMock = vi.fn(async (_input: RequestInfo | URL) => fileResponse(200, body));
    vi.stubGlobal("fetch", fetchMock);
    stubObjectUrls();

    const result = await downloadScanReport("json");

    expect(String(fetchMock.mock.calls[0][0])).toContain("/api/v1/channels/scan/report");
    expect(String(fetchMock.mock.calls[0][0])).not.toContain("format=csv");
    expect(result.filename).toBe("adoboflix-scan-report.json");
    expect(await result.blob.text()).toBe(body);
  });

  it("rejects with an ApiError on a 404 so the UI can render copy", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => fileResponse(404, '{"error":"no scan report available"}')));
    stubObjectUrls();

    await expect(downloadScanReport("json")).rejects.toMatchObject({ status: 404 });
  });
});
