import { isApiError } from "../api/client";

// How a failure should be presented. "action" and "blocked" are gates a human
// has to clear, "upstream" is AdoboTV's problem rather than AdoboFlix's, and
// "notfound"/"error" are the ordinary fallbacks.
export type SourceStatusSeverity = "action" | "blocked" | "upstream" | "notfound" | "error";

export interface SourceStatusCopy {
  code?: string;
  severity: SourceStatusSeverity;
  title: string;
  message: string;
  hint?: string;
  retryLabel?: string;
}

// One entry per code from internal/handler/source_error.go. Each states what
// happened and who can fix it, so a first-time subscriber sees the real reason
// instead of an empty library.
const COPY: Record<string, SourceStatusCopy> = {
  // The entry state, not a failure: no code is configured yet, and the user
  // clears it in this session by entering one. Kept actionable ("action") so
  // the panel points at the fix rather than reading like a rejection.
  playlist_code_required: {
    severity: "action",
    title: "This player needs a playlist code",
    message:
      "AdoboFlix has no AdoboTV playlist code yet, so AdoboTV serves no content. Enter your playlist code to load your library.",
    hint: "You can do this right now — no restart or reinstall needed.",
    retryLabel: "Enter playlist code",
  },
  // The sourceless first-run state: no source has been chosen, so every content
  // route answers with this. It is cleared in this session by choosing one of
  // the two options on the start screen — hence actionable, not an error.
  source_not_configured: {
    severity: "action",
    title: "No source is connected yet",
    message:
      "AdoboFlix has not chosen a content source, so there is no library to load. Connect your AdoboTV account or import a local playlist to continue.",
    hint: "Pick one of the two options on the start screen — no restart needed.",
    retryLabel: "Choose a source",
  },
  // ADOBOFLIX_SOURCE pins the running source, so the mode-changing endpoints
  // refuse. Unlike the other blocked codes this is not something the user can
  // undo from the UI: only the operator's configuration can release it.
  source_pinned_by_env: {
    severity: "blocked",
    title: "The source is pinned by configuration",
    message:
      "AdoboFlix is pinned to a content source by its server configuration, so it cannot be changed from here.",
    hint: "Unset ADOBOFLIX_SOURCE and restart AdoboFlix to choose a source in the UI.",
    retryLabel: "Reload",
  },
  // Import rejection. The server's own message carries the parse cause and the
  // pointer to the documented format, so the import UI renders that verbatim
  // and this copy is the fallback for a rejection with no message body.
  invalid_playlist: {
    severity: "blocked",
    title: "That playlist could not be read",
    message: "The file you chose is not a playlist AdoboFlix can read.",
    hint: "Fix the file as described in docs/source-adapters.md, then import it again.",
    retryLabel: "Choose another file",
  },
  // Import rejection for size. The 10 MiB cap is the server's, and the message
  // names it, so the fix is a smaller or split file.
  playlist_too_large: {
    severity: "blocked",
    title: "That playlist file is too large",
    message: "The file exceeds the 10 MiB import limit, so AdoboFlix did not read it.",
    hint: "Import a smaller playlist, or split this one and import it in parts.",
    retryLabel: "Choose another file",
  },
  device_pending: {
    severity: "action",
    title: "This device is awaiting approval",
    message:
      "AdoboTV has queued this device for the operator to approve, and serves no content until it is. Nothing is broken in AdoboFlix.",
    hint: "Ask the AdoboTV operator to approve this device, then reload.",
    retryLabel: "Reload after approval",
  },
  subscription_inactive: {
    severity: "action",
    title: "Subscription is not active",
    message:
      "Your AdoboTV subscription is not active. The library loads, but every stream will be refused — this is an entitlement state, not a playback failure.",
    hint: "Renew the subscription or contact the AdoboTV operator, then reload.",
    retryLabel: "Reload after renewal",
  },
  playlist_rejected: {
    severity: "blocked",
    title: "Playlist code was rejected",
    message: "AdoboTV did not accept the playlist code this player is configured with.",
    hint: "Check the configured playlist code, then try again.",
    retryLabel: "Try again",
  },
  // The login endpoint refused the username/password pair. AdoboTV returns one
  // 401 for an unknown username and a wrong password alike, to prevent
  // enumeration, and AdoboFlix preserves that — so the copy must not guess which
  // field was wrong.
  invalid_credentials: {
    severity: "blocked",
    title: "AdoboTV did not accept those credentials",
    message:
      "AdoboTV refused that username and password. It does not say which was wrong — deliberately, to prevent account enumeration.",
    hint: "Check both the username and the password, then try again.",
    retryLabel: "Try again",
  },
  // AdoboTV demands a reCAPTCHA token AdoboFlix cannot mint. Distinct from
  // invalid_credentials on purpose: the password may be perfectly correct, and
  // telling the user otherwise would send them to change a working one.
  captcha_required: {
    severity: "blocked",
    title: "AdoboTV requires a captcha to log in",
    message:
      "This AdoboTV server requires a reCAPTCHA token for password logins, and AdoboFlix cannot supply one. Your password has not been rejected — this is an AdoboTV configuration, not a wrong password.",
    hint: "Ask the AdoboTV operator to allow API logins, or sign in with a playlist code instead.",
    retryLabel: "Use a playlist code",
  },
  // The login succeeded but the account has no playlist code to read, so there
  // is nothing to store. Distinct from playlist_code_required: that asks the
  // user for a code; here the account simply has none.
  account_without_playlist_code: {
    severity: "blocked",
    title: "This AdoboTV account has no playlist code",
    message:
      "AdoboTV accepted the login, but the account carries no playlist code, so there is nothing for AdoboFlix to connect with.",
    hint: "Ask the AdoboTV operator to assign a playlist code to this account, then try again.",
    retryLabel: "Try again",
  },
  user_agent_rejected: {
    severity: "blocked",
    title: "This player is not on the allowlist",
    message: "AdoboTV only serves an approved set of players, and this one is not among them.",
    hint: "Ask the AdoboTV operator to allow this player, then reload.",
    retryLabel: "Reload",
  },
  playlist_format_m3u: {
    severity: "upstream",
    title: "AdoboTV returned an unexpected format",
    message:
      "AdoboTV answered with an M3U playlist where AdoboFlix expects JSON. This is an upstream configuration problem at AdoboTV, not an AdoboFlix crash.",
    hint: "Ask the AdoboTV operator to switch this playlist's output format back to JSON.",
    retryLabel: "Retry",
  },
  content_token_rejected: {
    severity: "upstream",
    title: "AdoboTV rejected a content token",
    message:
      "The content token AdoboTV minted with this playlist was refused. AdoboFlix drops the cached playlist and fetches a fresh token on the next request.",
    hint: "Retry; if it keeps happening, contact the AdoboTV operator.",
    retryLabel: "Retry",
  },
  malformed_playlist: {
    severity: "upstream",
    title: "AdoboTV sent a malformed playlist",
    message:
      "AdoboTV's playlist response could not be parsed. This is upstream at AdoboTV, not an AdoboFlix crash.",
    hint: "Ask the AdoboTV operator to check the playlist.",
    retryLabel: "Retry",
  },
  malformed_drm: {
    severity: "upstream",
    title: "AdoboTV sent a malformed DRM response",
    message:
      "Resolving DRM details from AdoboTV failed. This is upstream at AdoboTV, not an AdoboFlix crash.",
    hint: "Ask the AdoboTV operator to check this item's DRM configuration.",
    retryLabel: "Retry",
  },
  malformed_vod_library: {
    severity: "upstream",
    title: "AdoboTV sent a malformed VOD library",
    message:
      "AdoboTV's VOD library response could not be parsed. This is upstream at AdoboTV, not an AdoboFlix crash.",
    hint: "Ask the AdoboTV operator to check the VOD library.",
    retryLabel: "Retry",
  },
  upstream_error: {
    severity: "upstream",
    title: "AdoboTV is unavailable",
    message:
      "AdoboFlix could not get a usable answer from AdoboTV. This is an upstream problem, not an AdoboFlix crash.",
    hint: "Wait a moment and retry; if it persists, contact the AdoboTV operator.",
    retryLabel: "Retry",
  },
  content_not_found: {
    severity: "notfound",
    title: "Content not found",
    message: "This title is no longer in the library.",
    hint: "It may have been removed upstream.",
    retryLabel: "Reload",
  },
  not_found: {
    severity: "notfound",
    title: "Not found",
    message: "AdoboTV has no matching record for this request.",
    hint: "Check the address or head back to the library.",
    retryLabel: "Reload",
  },
};

const GENERIC: SourceStatusCopy = {
  severity: "error",
  title: "Something went wrong",
  message: "AdoboFlix could not complete this request.",
  hint: "Reload to try again. If it keeps failing, check the AdoboFlix server logs.",
  retryLabel: "Reload",
};

const UNREACHABLE: SourceStatusCopy = {
  severity: "error",
  title: "Cannot reach AdoboFlix",
  message: "The player could not reach its own backend.",
  hint: "Make sure the AdoboFlix server is running, then reload.",
  retryLabel: "Reload",
};

// sourceStatusCopy returns the mapped copy for a known code, falling back to
// the generic copy (keeping the code) for anything unrecognised. It lets a
// caller that already holds a stable code — the entry form showing the
// playlist_code_required state — render the same copy without fabricating an
// error object to satisfy describeSourceError.
export function sourceStatusCopy(code: string): SourceStatusCopy {
  if (code !== "internal_error") {
    const known = COPY[code];
    if (known) return { ...known, code };
  }
  return { ...GENERIC, code };
}

// describeSourceError turns any thrown value into UI copy. Only the mapped
// copy is ever rendered — the raw error message and the response body never
// reach the screen.
export function describeSourceError(error: unknown): SourceStatusCopy {
  if (!isApiError(error)) return UNREACHABLE;
  const { code } = error;
  if (code) return sourceStatusCopy(code);
  return { ...GENERIC, code };
}
