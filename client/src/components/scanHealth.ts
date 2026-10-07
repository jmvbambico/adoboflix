/**
 * @license
 * SPDX-License-Identifier: Apache-2.0
 */

import {
  fetchScanReportBlob,
  isApiError,
  type ScanState,
  type ScanStatus,
  type ScanStream,
  type SourceStatus,
} from "../api/client";

// Scan health is REPORT-ONLY. Every string in this module is written from that
// constraint: a scan probes streams to tell the user what is dead, and the
// report exists so it can be sent to the operator out of band — it never fixes,
// rebuilds or prunes anything upstream. Keep the copy honest about that.

// ── Query keys & lifecycle ────────────────────────────────────────────────

export const SCAN_STATUS_QUERY_KEY = ["scan-status"] as const;
export const SCAN_REPORT_QUERY_KEY = ["scan-report"] as const;

// While a scan is running the status is polled; the delay is short enough to
// feel live and long enough not to hammer the Go handler, which updates its
// counters once per probe (20 probes run in parallel, each with a 10s timeout).
// 1.5s lands between the two.
export const SCAN_POLL_INTERVAL_MS = 1500;

const RUNNING_STATES: ReadonlySet<ScanState> = new Set(["running"]);

// scanIsTerminal is true once no more progress will arrive without a new scan:
// a finished, failed or cancelled run. "idle" is NOT terminal in the sense of
// holding a report, but it also never polls — see scanRefetchInterval.
export function scanIsTerminal(state: ScanState | undefined): boolean {
  return state === "done" || state === "error" || state === "cancelled";
}

// scanRefetchInterval drives React Query's polling. It returns a delay while the
// scan is running and `false` for every other state, so polling stops on its own
// the moment the scan reaches a terminal state — no timer left running against a
// finished scan.
export function scanRefetchInterval(status: ScanStatus | undefined): number | false {
  return status && RUNNING_STATES.has(status.state) ? SCAN_POLL_INTERVAL_MS : false;
}

// ── Dashboard tab gating ──────────────────────────────────────────────────

export type DashboardTab = "browse" | "iptv" | "watchlist" | "history" | "health";

// healthScanTabAvailable reports whether the active source can be scanned. The
// server gates the whole feature on this flag (health_scan_supported), so the
// tab is offered — and the panel is reachable — only when it is true.
export function healthScanTabAvailable(status: SourceStatus | undefined): boolean {
  return Boolean(status?.health_scan_supported);
}

// resolveDashboardTab keeps the user off a dead panel. If the active tab is the
// scan tab but the source can no longer be scanned — a swap to adobotv-http, by
// design — the view falls back to Browse rather than rendering an empty body and
// stranding the user on a tab that is no longer offered.
export function resolveDashboardTab(tab: DashboardTab, healthAvailable: boolean): DashboardTab {
  if (tab === "health" && !healthAvailable) return "browse";
  return tab;
}

// ── Copy ──────────────────────────────────────────────────────────────────

export interface ScanCopy {
  title: string;
  message: string;
  hint?: string;
}

export const SCAN_STATE_LABEL: Record<ScanState, string> = {
  idle: "Idle",
  running: "Scanning",
  done: "Complete",
  error: "Error",
  cancelled: "Cancelled",
};

// describeScanStatus turns the pollable status into the panel's headline copy.
// It never invents progress: `total` is 0 until the lister returns, so the
// "x of y" line is only produced once a total is known.
export function describeScanStatus(status: ScanStatus | undefined): ScanCopy {
  if (!status) {
    return {
      title: "Reading scan state",
      message: "Checking whether a scan is available for this source.",
    };
  }

  switch (status.state) {
    case "running":
      return {
        title: "Scan in progress",
        message:
          status.total > 0
            ? `Probing streams: ${status.probed} of ${status.total} so far.`
            : "Preparing the list of streams to probe.",
        hint: "This only probes — it does not change or repair anything upstream.",
      };
    case "done":
      return {
        title: "Scan complete",
        message: "The report is ready. Download it and send it to your operator.",
        hint: "Nothing was changed upstream — the report describes what was probed.",
      };
    case "error":
      return {
        title: "The scan stopped with an error",
        message: status.error || "AdoboFlix could not finish the scan.",
        hint: "Run a new scan to try again.",
      };
    case "cancelled":
      return {
        title: "The scan was cancelled",
        message:
          "A cancelled scan is not published as a report, so there is nothing to download.",
        hint: "Run a new scan to produce one.",
      };
    case "idle":
    default:
      return {
        title: "No scan yet",
        message: "Run a scan to probe every stream behind this source and find the dead ones.",
        hint: "This reports only — it does not change or repair anything upstream.",
      };
  }
}

const GENERIC_START_ERROR: ScanCopy = {
  title: "The scan could not be started",
  message: "AdoboFlix could not start a scan.",
  hint: "Try again; if it keeps failing, check the AdoboFlix server logs.",
};

const UNSUPPORTED_SCAN: ScanCopy = {
  title: "This source cannot be scanned",
  message: "The active source cannot enumerate its streams for health probing.",
};

// describeScanStartError maps a real start failure to copy. A 409 never reaches
// here — startScan adopts a running scan instead of throwing — so a 409 case
// means something else went wrong and the generic copy is the honest answer.
export function describeScanStartError(error: unknown): ScanCopy {
  if (isApiError(error) && error.status === 501) return UNSUPPORTED_SCAN;
  if (isApiError(error) && error.status === 409) {
    return {
      title: "A scan is already running",
      message: "AdoboFlix is already scanning this source.",
      hint: "Wait for it to finish, or reload to pick it up.",
    };
  }
  return GENERIC_START_ERROR;
}

const GENERIC_REPORT_ERROR: ScanCopy = {
  title: "The report could not be read",
  message: "AdoboFlix could not read the scan report.",
  hint: "Try again; if it keeps failing, check the AdoboFlix server logs.",
};

// describeScanReportError maps the report endpoint's status codes (404, 409,
// 501 — the server sends no `code` on these) to copy, so a missing or
// unavailable report renders as a sentence rather than a thrown error.
export function describeScanReportError(error: unknown): ScanCopy {
  if (!isApiError(error)) {
    return {
      title: "Cannot reach AdoboFlix",
      message: "The player could not reach its own backend for the report.",
      hint: "Make sure the AdoboFlix server is running, then try again.",
    };
  }
  switch (error.status) {
    case 404:
      return {
        title: "No report yet",
        message: "There is no scan report to read. Run a scan first — its report is ready once the scan finishes.",
        hint: "The report only describes what was probed; nothing was changed upstream.",
      };
    case 409:
      return {
        title: "The scan is still running",
        message: "The report becomes available once the current scan finishes.",
        hint: "It reports only — nothing is changed or repaired upstream.",
      };
    case 501:
      return UNSUPPORTED_SCAN;
    default:
      return GENERIC_REPORT_ERROR;
  }
}

// ── Report rows ───────────────────────────────────────────────────────────

// sortScanStreams surfaces the dead rows first: a dead stream is the reason the
// report exists, so it should not be buried under the healthy ones. Within each
// group rows fall back to the server's own stable order (channel, label,
// stream id) so the table does not reshuffle between renders.
export function sortScanStreams(streams: ScanStream[]): ScanStream[] {
  return [...streams].sort((a, b) => {
    if (a.alive !== b.alive) return a.alive ? 1 : -1;
    if (a.channel_name !== b.channel_name) return a.channel_name.localeCompare(b.channel_name);
    if (a.label !== b.label) return a.label.localeCompare(b.label);
    return a.stream_id.localeCompare(b.stream_id);
  });
}

// ── Downloads ─────────────────────────────────────────────────────────────

// safeReportBasename reduces a header-supplied name to the only part that can
// name a file. The header is written by our own Go handler, but it is still a
// trust boundary: a malformed or crafted value must not be able to point the
// download outside the browser's download directory. Strip every directory
// component (so "../../etc/passwd" becomes "passwd" and "C:\\evil\\x.csv"
// becomes "x.csv"), reject control characters, and return null when nothing
// plausible is left so the caller can fall back. A legitimate server filename
// passes through byte-for-byte.
function safeReportBasename(raw: string): string | null {
  if (/[\u0000-\u001f\u007f]/.test(raw)) return null;
  const segments = raw.split(/[\\/]/);
  const name = segments[segments.length - 1].trim();
  // Empty, or a pure dot-name ("." / "..") that names a directory rather than a
  // file, is not a usable filename.
  if (name === "" || /^\.+$/.test(name)) return null;
  return name;
}

// parseReportFilename reads the server's Content-Disposition attachment
// filename, falling back to a sane default when the header is absent,
// malformed, or names nothing safe. Handles both the quoted form the Go handler
// writes (filename="...") and an unquoted token.
export function parseReportFilename(disposition: string | null, fallback: string): string {
  if (disposition) {
    const match = /filename="?([^";]+)"?/i.exec(disposition);
    if (match && match[1]) {
      const safe = safeReportBasename(match[1]);
      if (safe) return safe;
    }
  }
  return fallback;
}

// saveBlob hands a blob to the browser as a download without navigating away —
// a programmatic <a download> the code creates, clicks and revokes. Using this
// instead of a bare link or window.open is what lets a failed report request
// (404/409/501) be handled as UI copy: the fetch rejects before any download
// starts, so raw JSON is never dumped into a new tab.
export function saveBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  // Revoke on every path: if the append or the click throws, the object URL
  // must still be released or it leaks for the life of the document. anchor.remove()
  // is a no-op when the append never happened, so this is safe either way, and
  // on the success path it is the same DOM cleanup as before.
  try {
    document.body.appendChild(anchor);
    anchor.click();
  } finally {
    anchor.remove();
    URL.revokeObjectURL(url);
  }
}

// downloadScanReport fetches the report as a blob and saves it under the
// server's own filename. It returns the blob and filename so a caller — or a
// test — can inspect what was produced. A non-OK reply throws an ApiError that
// describeScanReportError turns into copy.
export async function downloadScanReport(
  format: "json" | "csv",
): Promise<{ filename: string; blob: Blob }> {
  const { blob, contentDisposition } = await fetchScanReportBlob(format);
  const filename = parseReportFilename(contentDisposition, `adoboflix-scan-report.${format}`);
  saveBlob(blob, filename);
  return { filename, blob };
}
