/**
 * @license
 * SPDX-License-Identifier: Apache-2.0
 */

import { FileUp, KeyRound, Plug } from "lucide-react";
import type { SourceMode, SourceStatus } from "../api/client";
import { sourceStatusCopy } from "./sourceStatus";
import { selectableModes } from "./sourceModes";

interface SourceChooserProps {
  status: SourceStatus;
  onLogin: () => void;
  onImport: () => void;
}

// SourceChooser is the first-run experience: when no source is active it is the
// page's primary content, not a strip above an empty library. The options come
// from the server's modes list, filtered to the selectable ones, so the UI
// never decides from an adapter name what it can offer. The two labels are
// ours: the mode that needs a playlist code is the AdoboTV login, the one that
// does not is a local-playlist import.
export default function SourceChooser({ status, onLogin, onImport }: SourceChooserProps) {
  const offers = selectableModes(status);
  const pinned = status.origin === "env";
  const pinnedCopy = sourceStatusCopy("source_pinned_by_env");

  const onChoose = (mode: SourceMode) => {
    if (mode.needs_playlist_code) onLogin();
    else onImport();
  };

  return (
    <section
      aria-label="Choose a content source"
      className="w-full max-w-3xl mx-auto py-10 sm:py-16 flex flex-col gap-8"
    >
      <div className="flex flex-col items-center text-center gap-3">
        <div className="p-4 rounded-3xl border text-orange-300 bg-orange-500/10 border-orange-500/20">
          <Plug className="w-7 h-7" />
        </div>
        <h2 className="font-display font-extrabold text-2xl sm:text-3xl text-slate-100 tracking-wide">
          Choose how to connect
        </h2>
        <p className="text-sm text-slate-400 leading-relaxed max-w-xl">
          AdoboFlix needs a content source before it can show your library. Connect an AdoboTV
          account, or play a playlist you already have.
        </p>
        <p className="text-xs font-medium text-orange-300/90">
          An AdoboTV account is optional — either path is a first-class way to use AdoboFlix.
        </p>
      </div>

      {pinned && (
        <div className="glass-panel rounded-2xl border border-white/10 p-4 flex flex-col gap-1">
          <span className="font-display font-bold text-sm text-slate-100 tracking-wide">
            {pinnedCopy.title}
          </span>
          <p className="text-xs text-slate-400 leading-relaxed">{pinnedCopy.message}</p>
          {pinnedCopy.hint && (
            <p className="text-xs font-medium text-amber-300/90">{pinnedCopy.hint}</p>
          )}
        </div>
      )}

      {offers.length === 0 ? (
        <div className="glass-panel rounded-2xl border border-white/10 p-6 text-center">
          <p className="text-xs text-slate-400 leading-relaxed">
            AdoboFlix could not determine which sources this server offers. Check the AdoboFlix
            server logs, then reload.
          </p>
        </div>
      ) : (
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-5">
          {offers.map((mode) => {
            const login = mode.needs_playlist_code;
            const Icon = login ? KeyRound : FileUp;
            const label = login ? "Login to AdoboTV" : "Import Local Playlist";
            const description = login
              ? "Enter your AdoboTV playlist code and stream the library your subscription entitles you to."
              : "Pick a playlist file on this device — JSON, M3U or M3U8 — and play it with no account.";
            return (
              <button
                key={mode.name}
                type="button"
                onClick={() => onChoose(mode)}
                disabled={pinned}
                className="glass-panel rounded-3xl border border-white/5 p-6 flex flex-col items-start gap-3 text-left hover:border-orange-500/30 hover:bg-white/[0.04] transition-all cursor-pointer focus:outline-none disabled:opacity-50 disabled:cursor-not-allowed"
              >
                <div className="p-3 rounded-2xl border text-orange-300 bg-orange-500/10 border-orange-500/20">
                  <Icon className="w-5 h-5" />
                </div>
                <span className="font-display font-bold text-base text-slate-100 tracking-wide">
                  {label}
                </span>
                <span className="text-xs text-slate-400 leading-relaxed">{description}</span>
              </button>
            );
          })}
        </div>
      )}
    </section>
  );
}
