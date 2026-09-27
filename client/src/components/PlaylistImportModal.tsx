/**
 * @license
 * SPDX-License-Identifier: Apache-2.0
 */

import { useEffect, useRef, useState, type ChangeEvent, type KeyboardEvent } from "react";
import { FileUp, LoaderCircle, X } from "lucide-react";
import { usePlaylistFileController } from "./playlistFile";

interface PlaylistImportModalProps {
  onClose: () => void;
}

// PlaylistImportModal reads the chosen playlist client-side and posts its
// contents to the import endpoint. The server accepts the JSON envelope
// documented in docs/source-adapters.md and M3U/M3U8, so the picker accepts all
// three. The failure path is the feature: the server's invalid_playlist message
// names the parse cause and the documented format, so it is rendered verbatim
// rather than paraphrased. The submit/invalidate logic lives in
// usePlaylistFileController, shared with the first-run chooser.
export default function PlaylistImportModal({ onClose }: PlaylistImportModalProps) {
  const { outcome, isPending, importFile } = usePlaylistFileController();
  const dialogRef = useRef<HTMLDivElement>(null);
  const [fileName, setFileName] = useState<string | null>(null);

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

  const imported = outcome?.kind === "imported";
  const failed = outcome?.kind === "failed" ? outcome : null;
  // A pinned source cannot be changed from here, so retrying the picker is one
  // more action that would fail. Render the explanation on its own.
  const pinnedFailure = failed?.copy.code === "source_pinned_by_env";
  const failureMessage =
    failed?.copy.code === "invalid_playlist" && failed.serverMessage
      ? failed.serverMessage
      : failed?.copy.message;

  const handleFile = (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0] ?? null;
    setFileName(file?.name ?? null);
    if (file) importFile(file);
    // Allow choosing the same file again after a failure.
    event.target.value = "";
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
        aria-labelledby="playlist-import-modal-title"
        onKeyDown={handleDialogKeyDown}
        className="w-full max-w-md glass-panel rounded-2xl border border-white/10 p-5 sm:p-6 flex flex-col gap-4"
      >
        <div className="flex items-start justify-between gap-4">
          <div className="flex flex-col gap-1 min-w-0">
            <h4
              id="playlist-import-modal-title"
              className="font-display font-bold text-base text-slate-100 tracking-wide"
            >
              Import Local Playlist
            </h4>
            <p className="text-xs text-slate-400 leading-relaxed">
              Choose a playlist on this device. It is read in the browser and sent to this server,
              which reads it for you — AdoboFlix never writes to your file.
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

        {imported ? (
          <div className="flex flex-col gap-2">
            <h5 className="font-display font-bold text-sm text-emerald-300 tracking-wide">
              Playlist imported
            </h5>
            <p className="text-xs text-slate-300 leading-relaxed">
              AdoboFlix is now reading your local playlist. The library is loading.
            </p>
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
            {failed && (
              <div className="flex flex-col gap-1">
                <h5 className="font-display font-bold text-sm text-slate-100 tracking-wide">
                  {failed.copy.title}
                </h5>
                <p className="text-xs text-slate-400 leading-relaxed">{failureMessage}</p>
                {failed.copy.hint && (
                  <p className="text-xs font-medium text-amber-300/90">{failed.copy.hint}</p>
                )}
              </div>
            )}

            {!pinnedFailure && (
              <div className="flex flex-col gap-1.5">
                <label
                  htmlFor="playlist-file-input"
                  className="text-[11px] font-semibold text-slate-300"
                >
                  Playlist file
                </label>
                <input
                  id="playlist-file-input"
                  name="playlist-file"
                  type="file"
                  accept=".json,.m3u,.m3u8"
                  onChange={handleFile}
                  disabled={isPending}
                  className="w-full text-xs text-slate-300 file:mr-3 file:px-4 file:py-2.5 file:rounded-xl file:border-0 file:bg-gradient-to-tr file:from-orange-600 file:to-amber-500 file:text-white file:text-xs file:font-bold file:cursor-pointer bg-slate-950/50 border border-white/10 rounded-xl cursor-pointer focus:outline-none focus:border-orange-500/40 disabled:opacity-60 file:disabled:opacity-50"
                />
                {fileName && (
                  <span className="text-[11px] text-slate-400 font-mono truncate">{fileName}</span>
                )}
              </div>
            )}

            {isPending && (
              <span className="flex items-center gap-2 text-xs text-slate-300">
                <LoaderCircle className="w-3.5 h-3.5 animate-spin" />
                Importing…
              </span>
            )}

            {!failed && (
              <p className="flex items-center gap-1.5 text-[11px] text-slate-500">
                <FileUp className="w-3.5 h-3.5" />
                JSON, M3U or M3U8.
              </p>
            )}
          </>
        )}
      </div>
    </div>
  );
}
