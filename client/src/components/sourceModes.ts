import type { SourceMode, SourceStatus } from "../api/client";

// The two choices AdoboFlix offers are ours to name; which modes exist, and
// which of them are offered, comes from the server's modes list. Every helper
// here branches on a mode's declared requirement — whether it needs a playlist
// code — and never on the adapter's name, which is the whole point of the
// flags. A name is only ever carried through for keys, never rendered as a
// user's choice.

// selectableModes are the modes a normal user may choose. A development
// harness is not selectable, so it can never be offered as a switch target.
export function selectableModes(status: SourceStatus | undefined): SourceMode[] {
  return (status?.modes ?? []).filter((mode) => mode.selectable);
}

// modeChoiceLabel is what the user reads for a mode they can choose: the mode
// that takes a playlist code is the AdoboTV login, the one that does not is a
// local-playlist import.
export function modeChoiceLabel(mode: SourceMode): string {
  return mode.needs_playlist_code ? "Login to AdoboTV" : "Import Local Playlist";
}

// activeMode is the entry the server marks active, or undefined when no source
// is running (or the server named none).
export function activeMode(status: SourceStatus | undefined): SourceMode | undefined {
  return status?.modes?.find((mode) => mode.active);
}

// activeSourceLabel names the running source in our words. A development
// harness is labelled plainly as one — the owner's instruction was not to
// document it — and an unrecognised active source is described generically
// rather than leaking the adapter name as though it were a choice.
export function activeSourceLabel(status: SourceStatus | undefined): string {
  if (!status) return "…";
  if (status.dev) return "Development source";
  const mode = activeMode(status);
  if (mode) return mode.needs_playlist_code ? "AdoboTV account" : "Local playlist";
  if (!status.active) return "Not connected";
  return "Connected source";
}

// isPinned reports whether ADOBOFLIX_SOURCE pins the source. While it does, a
// mode-changing request is refused with 409 source_pinned_by_env, so the UI
// must explain that instead of offering an action that would fail.
export function isPinned(status: SourceStatus | undefined): boolean {
  return status?.origin === "env";
}
