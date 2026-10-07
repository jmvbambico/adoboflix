/**
 * @license
 * SPDX-License-Identifier: Apache-2.0
 */

import { useCallback, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, AlertTriangle, CircleCheck, Download, RefreshCw, SearchX } from "lucide-react";
import { fetchScanReport, fetchScanStatus, startScan, type ScanState } from "../api/client";
import {
  SCAN_REPORT_QUERY_KEY,
  SCAN_STATUS_QUERY_KEY,
  SCAN_STATE_LABEL,
  describeScanReportError,
  describeScanStartError,
  describeScanStatus,
  downloadScanReport,
  scanIsTerminal,
  scanRefetchInterval,
  sortScanStreams,
} from "./scanHealth";

const STATE_TONE: Record<ScanState, string> = {
  idle: "text-slate-300 bg-white/5 border-white/10",
  running: "text-cyan-300 bg-cyan-500/10 border-cyan-500/20",
  done: "text-emerald-400 bg-emerald-500/10 border-emerald-500/20",
  error: "text-red-400 bg-red-500/10 border-red-500/20",
  cancelled: "text-amber-300 bg-amber-500/10 border-amber-500/20",
};

// The scan panel is the whole UI for the health scanner: it starts a scan,
// polls its aggregate progress, and once a report exists shows the per-stream
// rows and offers the report as a download. It reports what is dead — the copy
// repeats that it repairs nothing upstream.
export default function StreamHealthPanel() {
  const queryClient = useQueryClient();
  const [downloadError, setDownloadError] = useState<unknown>(null);

  const statusQuery = useQuery({
    queryKey: SCAN_STATUS_QUERY_KEY,
    queryFn: fetchScanStatus,
    // Poll only while a scan is running; the function returns false for every
    // terminal state so the polling stops on its own.
    refetchInterval: (query) => scanRefetchInterval(query.state.data),
  });

  const status = statusQuery.data;
  const terminal = scanIsTerminal(status?.state);
  const hasReport = Boolean(status?.has_report);

  const reportQuery = useQuery({
    queryKey: SCAN_REPORT_QUERY_KEY,
    queryFn: fetchScanReport,
    enabled: terminal && hasReport,
    retry: false,
  });

  const scan = useMutation({
    mutationFn: startScan,
    onSuccess: (next) => {
      // Starting a new scan drops any previous report so a stale table cannot
      // linger, then writes the returned status (a 409's running scan, or a
      // fresh 202) straight into the shared cache so progress shows immediately.
      setDownloadError(null);
      queryClient.removeQueries({ queryKey: SCAN_REPORT_QUERY_KEY });
      queryClient.setQueryData(SCAN_STATUS_QUERY_KEY, next);
      queryClient.invalidateQueries({ queryKey: SCAN_STATUS_QUERY_KEY });
    },
  });

  const handleDownload = useCallback(async (format: "json" | "csv") => {
    setDownloadError(null);
    try {
      await downloadScanReport(format);
    } catch (err) {
      setDownloadError(err);
    }
  }, []);

  const state = status?.state ?? "idle";
  const running = state === "running";
  const copy = describeScanStatus(status);
  const rows = reportQuery.data ? sortScanStreams(reportQuery.data.streams) : [];

  return (
    <div className="glass-panel rounded-3xl border border-white/5 p-6 sm:p-8 flex flex-col gap-6">
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3">
        <div className="flex items-center gap-3">
          <div className="p-2.5 rounded-2xl border bg-orange-600/10 border-orange-500/20 text-orange-400">
            <Activity className="w-5 h-5" />
          </div>
          <div className="flex flex-col">
            <h3 className="font-display font-semibold text-lg text-slate-100 uppercase tracking-wide">
              Stream health scan
            </h3>
            <span className="text-[11px] text-slate-500 font-mono">
              Probes every stream behind this source and reports the dead ones
            </span>
          </div>
        </div>
        <span
          className={`w-fit px-3 py-1 text-[10px] font-mono font-bold uppercase tracking-widest rounded-md border ${STATE_TONE[state]}`}
        >
          {SCAN_STATE_LABEL[state]}
        </span>
      </div>

      {/* The binding constraint, stated plainly: this reports, it does not
          repair. The report exists to be sent to the operator out of band. */}
      <p className="text-xs sm:text-sm text-slate-400 leading-relaxed max-w-3xl">
        AdoboFlix only <span className="text-slate-200 font-semibold">reports</span> what it finds.
        A scan never fixes, rebuilds or prunes anything upstream. Download the report and send it to
        your operator, who owns the fix.
      </p>

      <div className="flex flex-wrap items-center gap-3">
        <button
          onClick={() => scan.mutate()}
          disabled={running || scan.isPending}
          className="px-5 py-2.5 bg-gradient-to-tr from-orange-600 to-amber-500 hover:from-orange-700 hover:to-amber-600 disabled:opacity-50 disabled:cursor-not-allowed rounded-xl text-xs font-bold tracking-wider text-white shadow-lg shadow-orange-600/20 flex items-center gap-2 transition-all focus:outline-none cursor-pointer"
        >
          <RefreshCw className={`w-3.5 h-3.5 ${running ? "animate-spin" : ""}`} />
          {running ? "Scanning…" : hasReport || scan.isSuccess ? "Scan again" : "Scan now"}
        </button>

        {terminal && hasReport && (
          <>
            <button
              onClick={() => { void handleDownload("csv"); }}
              className="px-4 py-2.5 bg-white/5 border border-white/10 hover:bg-white/10 rounded-xl text-xs font-bold tracking-wide text-slate-200 flex items-center gap-2 transition-all focus:outline-none cursor-pointer"
            >
              <Download className="w-3.5 h-3.5" />
              Download CSV
            </button>
            <button
              onClick={() => { void handleDownload("json"); }}
              className="px-4 py-2.5 bg-white/5 border border-white/10 hover:bg-white/10 rounded-xl text-xs font-bold tracking-wide text-slate-200 flex items-center gap-2 transition-all focus:outline-none cursor-pointer"
            >
              <Download className="w-3.5 h-3.5" />
              Download JSON
            </button>
          </>
        )}
      </div>

      {scan.isError && <ScanCopyBlock copy={describeScanStartError(scan.error)} tone="error" />}
      {downloadError !== null && (
        <ScanCopyBlock copy={describeScanReportError(downloadError)} tone="error" />
      )}
      {reportQuery.isError && (
        <ScanCopyBlock copy={describeScanReportError(reportQuery.error)} tone="notfound" />
      )}

      {/* Aggregate progress / state */}
      <div className="flex flex-col gap-3">
        <div className="flex items-center gap-2">
          <h4 className="font-display font-bold text-xs sm:text-sm text-slate-100 tracking-wide uppercase">
            {copy.title}
          </h4>
        </div>
        <p className="text-xs sm:text-sm text-slate-400 leading-relaxed max-w-2xl">{copy.message}</p>
        {copy.hint && (
          <p className="text-[11px] leading-relaxed max-w-2xl text-slate-500">{copy.hint}</p>
        )}

        <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 mt-1">
          <StatCell label="Probed" value={status ? (status.total > 0 ? `${status.probed} / ${status.total}` : `${status.probed}`) : "—"} />
          <StatCell label="Alive" value={String(status?.alive ?? 0)} tone="text-emerald-400" />
          <StatCell label="Dead" value={String(status?.dead ?? 0)} tone="text-red-400" />
          <StatCell label="State" value={SCAN_STATE_LABEL[state]} />
        </div>
      </div>

      {terminal && hasReport && !reportQuery.isError && (
        <ReportTable
          rows={rows}
          dead={reportQuery.data?.dead_streams ?? 0}
          alive={reportQuery.data?.alive_streams ?? 0}
          loading={reportQuery.isLoading}
        />
      )}
    </div>
  );
}

function StatCell({ label, value, tone }: { label: string; value: string; tone?: string }) {
  return (
    <div className="flex flex-col gap-1 p-3 rounded-xl bg-white/[0.03] border border-white/5">
      <span className="text-[10px] font-mono text-slate-500 uppercase tracking-wider">{label}</span>
      <span className={`text-sm font-semibold ${tone ?? "text-slate-200"}`}>{value}</span>
    </div>
  );
}

function ScanCopyBlock({
  copy,
  tone,
}: {
  copy: { title: string; message: string; hint?: string };
  tone: "error" | "notfound";
}) {
  const Icon = tone === "error" ? AlertTriangle : SearchX;
  const toneClass =
    tone === "error"
      ? "text-red-400 bg-red-500/10 border-red-500/20"
      : "text-slate-300 bg-white/5 border-white/10";
  return (
    <div role="alert" className="flex items-start gap-3 p-4 rounded-2xl border border-white/5 bg-white/[0.02]">
      <div className={`p-2 rounded-xl border shrink-0 ${toneClass}`}>
        <Icon className="w-4 h-4" />
      </div>
      <div className="flex flex-col gap-1 min-w-0">
        <span className="text-xs font-bold text-slate-100">{copy.title}</span>
        <span className="text-[11px] text-slate-400 leading-relaxed">{copy.message}</span>
        {copy.hint && <span className="text-[11px] text-slate-500 leading-relaxed">{copy.hint}</span>}
      </div>
    </div>
  );
}

// Only the redacted host and manifest are ever rendered — never a resolvable
// stream URL. Dead rows come first (sortScanStreams) because they are the point.
function ReportTable({
  rows,
  dead,
  alive,
  loading,
}: {
  rows: ReturnType<typeof sortScanStreams>;
  dead: number;
  alive: number;
  loading: boolean;
}) {
  if (loading) {
    return <div className="py-6 text-center text-xs font-mono text-slate-500">Loading report…</div>;
  }
  if (rows.length === 0) {
    return (
      <div className="py-6 text-center text-xs text-slate-500">The scan probed no streams.</div>
    );
  }
  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <h4 className="font-display font-bold text-xs sm:text-sm text-slate-100 tracking-wide uppercase">
          Report
        </h4>
        <span className="text-[10px] font-mono text-red-400">{dead} dead</span>
        <span className="text-[10px] font-mono text-emerald-400">{alive} alive</span>
      </div>
      <div className="overflow-x-auto no-scrollbar rounded-2xl border border-white/5">
        <table className="w-full text-left text-[11px]">
          <thead className="bg-white/[0.03] text-[10px] font-mono uppercase tracking-wider text-slate-500">
            <tr>
              <th className="px-3 py-2 font-semibold">Result</th>
              <th className="px-3 py-2 font-semibold">Channel</th>
              <th className="px-3 py-2 font-semibold">Category</th>
              <th className="px-3 py-2 font-semibold">Label</th>
              <th className="px-3 py-2 font-semibold">Host</th>
              <th className="px-3 py-2 font-semibold">Manifest</th>
              <th className="px-3 py-2 font-semibold">Reason</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr
                key={`${row.channel_id}-${row.stream_id}`}
                className={row.alive ? "border-t border-white/5" : "border-t border-white/5 bg-red-500/[0.06]"}
              >
                <td className="px-3 py-2">
                  {row.alive ? (
                    <span className="text-emerald-400 flex items-center gap-1">
                      <CircleCheck className="w-3.5 h-3.5" /> Alive
                    </span>
                  ) : (
                    <span className="text-red-400 flex items-center gap-1">
                      <AlertTriangle className="w-3.5 h-3.5" /> Dead
                    </span>
                  )}
                </td>
                <td className="px-3 py-2 text-slate-200">{row.channel_name}</td>
                <td className="px-3 py-2 text-slate-400">{row.category}</td>
                <td className="px-3 py-2 text-slate-400">{row.label}</td>
                <td className="px-3 py-2 font-mono text-slate-300">{row.host}</td>
                <td className="px-3 py-2 font-mono text-slate-400">{row.manifest}</td>
                <td className="px-3 py-2 text-slate-500">
                  {row.alive ? "" : row.reason || (row.http_status ? `HTTP ${row.http_status}` : "")}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
