/**
 * @license
 * SPDX-License-Identifier: Apache-2.0
 */

import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleCheck, KeyRound, LoaderCircle, RefreshCw, Trash2 } from "lucide-react";
import {
  clearPlaylistCode,
  fetchSourceStatus,
  isApiError,
  setPlaylistCode,
  type SourceStatus,
} from "../api/client";
import { describeSourceError, sourceStatusCopy, type SourceStatusCopy } from "./sourceStatus";

export const SOURCE_STATUS_QUERY_KEY = ["source-status"] as const;

// Codes for which the server kept the submitted code: it proved the code itself
// valid and only a gate outside the code's control remains. This mirrors
// playlistCodeProvenValid in internal/handler/source_control.go — the two must
// stay in step, or the form would tell a user to re-enter a code the server
// already saved.
const SAVED_GATE_CODES = new Set([
  "device_pending",
  "subscription_inactive",
  "playlist_format_m3u",
  "content_token_rejected",
  "content_not_found",
]);

// A saved code is not a failure. Say plainly that nothing needs re-entering and
// why playback has not started yet, so a first connect does not send the user
// back to retype a code that is already on the server.
const SAVED_REASSURANCE: Record<string, string> = {
  device_pending:
    "AdoboTV accepted this code and AdoboFlix saved it on the server. Nothing needs re-entering — playback starts on its own once the AdoboTV operator approves this device.",
  subscription_inactive:
    "AdoboTV accepted this code and AdoboFlix saved it on the server. Nothing needs re-entering — playback starts on its own once your subscription is active again.",
};
const SAVED_REASSURANCE_DEFAULT =
  "AdoboTV accepted this code and AdoboFlix saved it on the server. Nothing needs re-entering.";

type Outcome =
  | { kind: "saved"; copy: SourceStatusCopy }
  | { kind: "failed"; copy: SourceStatusCopy }
  | null;

interface PlaylistCodeGateProps {
  // Errors from requests the caller already issued. Any carrying
  // playlist_code_required shows the entry form even before, or without, the
  // status query settling — the server tells the client it needs a code the
  // same way it tells it a stream failed.
  errors?: unknown[];
}

// PlaylistCodeGate is the only place a playlist code is entered. It never
// renders the code back, never logs it, never puts it in a URL, and never
// touches browser storage: the code lives on the server, and the input is
// cleared the moment it is submitted.
export default function PlaylistCodeGate({ errors = [] }: PlaylistCodeGateProps) {
  const queryClient = useQueryClient();
  const [code, setCode] = useState("");
  const [outcome, setOutcome] = useState<Outcome>(null);

  const statusQuery = useQuery<SourceStatus>({
    queryKey: SOURCE_STATUS_QUERY_KEY,
    queryFn: fetchSourceStatus,
  });

  const needsCode = Boolean(statusQuery.data?.needs_playlist_code);
  const configured = Boolean(statusQuery.data?.playlist_code_configured);
  const statusNeedsEntry = needsCode && !configured;
  const requestNeedsEntry = errors.some(
    (error) => describeSourceError(error).code === "playlist_code_required",
  );

  const submit = useMutation({
    mutationFn: (value: string) => setPlaylistCode(value),
    onSuccess: () => {
      // The server already validated the code and swapped the adapter. Drop
      // every cached answer so the library refetches against the new source;
      // a page reload would throw away the session for nothing.
      queryClient.setQueryData<SourceStatus>(SOURCE_STATUS_QUERY_KEY, (prev) =>
        prev ? { ...prev, playlist_code_configured: true } : prev,
      );
      queryClient.invalidateQueries();
      setOutcome(null);
    },
    onError: (error) => {
      // A gate that proves the code valid leaves it persisted; the rest persist
      // nothing and the user must correct the code.
      if (isApiError(error) && error.code && SAVED_GATE_CODES.has(error.code)) {
        setOutcome({ kind: "saved", copy: describeSourceError(error) });
        return;
      }
      setOutcome({ kind: "failed", copy: describeSourceError(error) });
    },
  });

  const clear = useMutation({
    mutationFn: () => clearPlaylistCode(),
    onSuccess: () => {
      queryClient.setQueryData<SourceStatus>(SOURCE_STATUS_QUERY_KEY, (prev) =>
        prev ? { ...prev, playlist_code_configured: false } : prev,
      );
      queryClient.invalidateQueries();
      setOutcome(null);
    },
  });

  const saved = outcome?.kind === "saved";
  const failed = outcome?.kind === "failed" ? outcome.copy : null;
  const showForm = !saved && (statusNeedsEntry || requestNeedsEntry || failed !== null);
  const showConfigured = !saved && !showForm && needsCode && configured;

  if (!saved && !showForm && !showConfigured) return null;

  const handleSubmit = (event: FormEvent) => {
    event.preventDefault();
    const value = code.trim();
    if (!value) return;
    // Clear before the request settles: the entered value must not stay in the
    // mounted DOM, and on failure the user retypes against the error copy.
    setCode("");
    submit.mutate(value);
  };

  return (
    <section
      aria-label="AdoboTV source connection"
      className="glass-panel rounded-3xl border border-white/5 p-5 sm:p-6 flex flex-col gap-4"
    >
      <div className="flex items-start gap-4">
        <div className="p-3 rounded-2xl border shrink-0 text-amber-300 bg-amber-500/10 border-amber-500/20">
          <KeyRound className="w-5 h-5" />
        </div>
        <div className="flex flex-col gap-1.5 min-w-0">
          {saved ? (
            <>
              <h4 className="font-display font-bold text-base sm:text-lg text-emerald-300 tracking-wide">
                Playlist code saved
              </h4>
              <p className="text-xs sm:text-sm text-slate-300 leading-relaxed max-w-2xl">
                {SAVED_REASSURANCE[outcome.copy.code ?? ""] ?? SAVED_REASSURANCE_DEFAULT}
              </p>
              <p className="text-xs text-slate-400 leading-relaxed max-w-2xl">
                {outcome.copy.message}
              </p>
            </>
          ) : showConfigured ? (
            <>
              <h4 className="font-display font-bold text-sm text-slate-100 tracking-wide">
                Playlist code configured
              </h4>
              <p className="text-xs text-slate-400 leading-relaxed max-w-2xl">
                This player is connected to AdoboTV with a playlist code held on the server. Clear
                it to disconnect, or to fall back to the environment configuration.
              </p>
            </>
          ) : (
            <>
              <h4 className="font-display font-bold text-base sm:text-lg text-slate-100 tracking-wide">
                {failed ? failed.title : sourceStatusCopy("playlist_code_required").title}
              </h4>
              <p className="text-xs sm:text-sm text-slate-400 leading-relaxed max-w-2xl">
                {failed ? failed.message : sourceStatusCopy("playlist_code_required").message}
              </p>
              <p className="text-xs leading-relaxed max-w-2xl font-medium text-amber-300/90">
                {failed ? failed.hint : sourceStatusCopy("playlist_code_required").hint}
              </p>
            </>
          )}
        </div>
      </div>

      {showForm && (
        <form onSubmit={handleSubmit} className="flex flex-col sm:flex-row sm:items-end gap-3">
          <div className="flex flex-col gap-1.5 flex-1 min-w-0">
            <label htmlFor="playlist-code-input" className="text-[11px] font-semibold text-slate-300">
              Playlist code
            </label>
            <input
              id="playlist-code-input"
              name="playlist-code"
              type="password"
              value={code}
              onChange={(event) => setCode(event.target.value)}
              placeholder="Enter your AdoboTV playlist code"
              autoFocus={failed === null}
              disabled={submit.isPending}
              className="w-full px-3 py-2.5 rounded-xl bg-slate-950/50 border border-white/10 text-sm text-slate-100 placeholder:text-slate-600 focus:outline-none focus:border-orange-500/40 focus:ring-1 focus:ring-orange-500/20 disabled:opacity-60"
            />
          </div>
          <button
            type="submit"
            disabled={submit.isPending || code.trim() === ""}
            className="shrink-0 px-5 py-2.5 bg-gradient-to-tr from-orange-600 to-amber-500 hover:from-orange-700 hover:to-amber-600 rounded-xl text-xs font-bold tracking-wider text-white shadow-lg shadow-orange-600/20 flex items-center justify-center gap-2 transition-all active:scale-98 cursor-pointer focus:outline-none disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {submit.isPending ? <LoaderCircle className="w-3.5 h-3.5 animate-spin" /> : <KeyRound className="w-3.5 h-3.5" />}
            {submit.isPending ? "Connecting…" : "Connect"}
          </button>
        </form>
      )}

      {saved && (
        <button
          type="button"
          onClick={() => queryClient.invalidateQueries()}
          className="w-fit px-5 py-2.5 bg-white/5 border border-white/10 hover:bg-white/10 rounded-xl text-xs font-bold tracking-wider text-slate-200 flex items-center gap-2 transition-all cursor-pointer focus:outline-none"
        >
          <RefreshCw className="w-3.5 h-3.5" />
          Check again
        </button>
      )}

      {showConfigured && (
        <div className="flex flex-wrap items-center gap-3">
          <span className="text-[11px] font-mono text-emerald-400 bg-emerald-500/10 border border-emerald-500/20 px-2.5 py-1 rounded-full flex items-center gap-1.5">
            <CircleCheck className="w-3.5 h-3.5" />
            {statusQuery.data?.source ?? "source"} ready
          </span>
          <button
            type="button"
            onClick={() => clear.mutate()}
            disabled={clear.isPending}
            className="px-3 py-1.5 rounded-lg border border-white/10 text-slate-300 hover:text-red-300 hover:border-red-500/30 hover:bg-red-500/10 text-[11px] font-semibold flex items-center gap-1.5 transition-all cursor-pointer focus:outline-none disabled:opacity-50"
          >
            <Trash2 className="w-3.5 h-3.5" />
            {clear.isPending ? "Clearing…" : "Clear playlist code"}
          </button>
        </div>
      )}
    </section>
  );
}
