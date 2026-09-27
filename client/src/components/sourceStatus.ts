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
  // Not the user's to fix by typing: the running source takes no code at all,
  // which is a choice made by whoever configured AdoboFlix.
  playlist_code_not_supported: {
    severity: "blocked",
    title: "This source has no playlist code",
    message:
      "The active AdoboFlix source does not take a playlist code, so there is nothing to enter. AdoboFlix is configured to read a local file or the development database directly.",
    hint: "Switch the AdoboFlix source to adobotv-http, then reload.",
    retryLabel: "Reload",
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
