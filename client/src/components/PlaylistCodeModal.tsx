/**
 * @license
 * SPDX-License-Identifier: Apache-2.0
 */

import { useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import { KeyRound, LoaderCircle, X } from "lucide-react";
import { sourceStatusCopy } from "./sourceStatus";
import { savedReassurance, usePlaylistCodeController } from "./playlistCode";

interface PlaylistCodeModalProps {
  // Whether a code is already connected. Changes the title and the submit
  // label so "change" does not read like a first connect.
  configured: boolean;
  onClose: () => void;
}

// PlaylistCodeModal is the account menu's entry point for a playlist code. It
// is mounted only while open, so each open starts from a clean input. It never
// renders the code back and clears it the moment it is submitted, exactly as
// the inline gate does; both share usePlaylistCodeController for the submit and
// gate-code handling.
export default function PlaylistCodeModal({ configured, onClose }: PlaylistCodeModalProps) {
  const [code, setCode] = useState("");
  const { submit, submitCode, outcome } = usePlaylistCodeController();
  const dialogRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [onClose]);

  // aria-modal="true" promises the rest of the page is inert, so Tab must cycle
  // within the dialog instead of walking into the content behind the backdrop.
  const handleDialogKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== "Tab") return;
    const focusables = Array.from(
      dialogRef.current?.querySelectorAll<HTMLElement>(
        'a[href], button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])',
      ) ?? [],
    );
    if (focusables.length === 0) return;
    const first = focusables[0];
    const last = focusables[focusables.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  };

  const saved = outcome?.kind === "saved";
  const failed = outcome?.kind === "failed" ? outcome.copy : null;

  const handleSubmit = (event: FormEvent) => {
    event.preventDefault();
    const value = code.trim();
    if (!value) return;
    // Clear before the request settles: the entered value must not stay in the
    // mounted DOM.
    setCode("");
    // onConnected fires only on a full connect; a saved-but-gated outcome
    // reports itself below instead of closing.
    submitCode(value, onClose);
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/70 backdrop-blur-sm"
      onClick={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="playlist-code-modal-title"
        onKeyDown={handleDialogKeyDown}
        className="w-full max-w-md glass-panel rounded-2xl border border-white/10 p-5 sm:p-6 flex flex-col gap-4"
      >
        <div className="flex items-start justify-between gap-4">
          <div className="flex flex-col gap-1 min-w-0">
            <h4
              id="playlist-code-modal-title"
              className="font-display font-bold text-base text-slate-100 tracking-wide"
            >
              {configured ? "Change playlist code" : "Connect AdoboTV"}
            </h4>
            <p className="text-xs text-slate-400 leading-relaxed">
              Your AdoboTV playlist code is the only credential AdoboFlix needs. It is stored on this
              server and never shown back.
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            aria-label="Close"
            className="shrink-0 p-1.5 rounded-lg text-slate-400 hover:text-white hover:bg-white/5 transition-all cursor-pointer focus:outline-none"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        {saved ? (
          <div className="flex flex-col gap-2">
            <h5 className="font-display font-bold text-sm text-emerald-300 tracking-wide">
              Playlist code saved
            </h5>
            <p className="text-xs text-slate-300 leading-relaxed">{savedReassurance(outcome.copy.code)}</p>
            <p className="text-xs text-slate-400 leading-relaxed">{outcome.copy.message}</p>
            <button
              type="button"
              onClick={onClose}
              className="w-fit px-5 py-2.5 bg-white/5 border border-white/10 hover:bg-white/10 rounded-xl text-xs font-bold tracking-wider text-slate-200 transition-all cursor-pointer focus:outline-none"
            >
              Done
            </button>
          </div>
        ) : (
          <>
            {failed ? (
              <div className="flex flex-col gap-1">
                <h5 className="font-display font-bold text-sm text-slate-100 tracking-wide">
                  {failed.title}
                </h5>
                <p className="text-xs text-slate-400 leading-relaxed">{failed.message}</p>
                {failed.hint && <p className="text-xs font-medium text-amber-300/90">{failed.hint}</p>}
              </div>
            ) : (
              <p className="text-xs font-medium text-amber-300/90">
                {sourceStatusCopy("playlist_code_required").hint}
              </p>
            )}

            <form onSubmit={handleSubmit} className="flex flex-col gap-3">
              <div className="flex flex-col gap-1.5">
                <label
                  htmlFor="account-playlist-code"
                  className="text-[11px] font-semibold text-slate-300"
                >
                  Playlist code
                </label>
                <input
                  id="account-playlist-code"
                  name="playlist-code"
                  type="password"
                  value={code}
                  onChange={(event) => setCode(event.target.value)}
                  placeholder="Enter your AdoboTV playlist code"
                  autoComplete="off"
                  autoFocus
                  disabled={submit.isPending}
                  className="w-full px-3 py-2.5 rounded-xl bg-slate-950/50 border border-white/10 text-sm text-slate-100 placeholder:text-slate-600 focus:outline-none focus:border-orange-500/40 focus:ring-1 focus:ring-orange-500/20 disabled:opacity-60"
                />
              </div>
              <button
                type="submit"
                disabled={submit.isPending || code.trim() === ""}
                className="w-fit px-5 py-2.5 bg-gradient-to-tr from-orange-600 to-amber-500 hover:from-orange-700 hover:to-amber-600 rounded-xl text-xs font-bold tracking-wider text-white shadow-lg shadow-orange-600/20 flex items-center gap-2 transition-all active:scale-98 cursor-pointer focus:outline-none disabled:opacity-50 disabled:cursor-not-allowed"
              >
                {submit.isPending ? (
                  <LoaderCircle className="w-3.5 h-3.5 animate-spin" />
                ) : (
                  <KeyRound className="w-3.5 h-3.5" />
                )}
                {submit.isPending ? "Connecting…" : configured ? "Change code" : "Connect"}
              </button>
            </form>
          </>
        )}
      </div>
    </div>
  );
}
