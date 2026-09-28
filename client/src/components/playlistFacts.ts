// The two facts the import shape shows about the user's own playlist. Both are
// derived from counts that are already reachable — /stats for titles, /channels
// for live channels — because a playlist of channels has no titles at all, and
// reporting "0 titles" for one that loaded fine would tell the user their
// import failed.

// describePlaylistContents names what the playlist actually holds: its live
// channels, its titles, or both. It returns null while a count is still unknown
// (nothing to say yet) and only claims emptiness once BOTH counts have arrived,
// so a slow read is never rendered as "this playlist is empty". A genuinely
// empty playlist gets its own wording, since that is the one case where
// re-importing is the right advice.
export function describePlaylistContents(
  titles: number | undefined,
  channels: number | undefined,
): string | null {
  const parts: string[] = [];
  if (channels !== undefined && channels > 0) {
    parts.push(`${channels} ${channels === 1 ? "channel" : "channels"}`);
  }
  if (titles !== undefined && titles > 0) {
    parts.push(`${titles} ${titles === 1 ? "title" : "titles"}`);
  }
  if (parts.length > 0) return `${parts.join(" and ")} loaded`;

  if (titles === undefined || channels === undefined) return null;
  return "This playlist has no channels or titles — check the file, or import a different one.";
}

// RFC3339 as the status contract specifies it: a full date, a time, and a zone
// (Z or ±hh:mm), with optional fractional seconds. Requiring the shape keeps
// Date's lenient parser from accepting a non-ISO sentinel — Date("0") is a real
// instant (1 Jan 2000) and would otherwise render as a date the server never
// sent. The field crosses a process boundary, so the shape is checked, not
// assumed.
const RFC3339 = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/;

// parseRfc3339Instant is the one guard every status timestamp goes through. It
// returns the instant for a valid RFC3339 value and null for a missing,
// non-ISO, unparseable, zero or pre-epoch one, so absence is never rendered as
// an epoch date (the billed_till lesson). The formatters below share it rather
// than repeating a slightly different check.
export function parseRfc3339Instant(iso: string | undefined): Date | null {
  if (!iso || !RFC3339.test(iso)) return null;
  const date = new Date(iso);
  if (Number.isNaN(date.getTime()) || date.getTime() <= 0) return null;
  return date;
}

// formatDay renders a date as a readable day, no time.
function formatDay(date: Date): string {
  return date.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
}

// formatImportedAt turns the RFC3339 import time into something readable at a
// glance. A missing, non-ISO, unparseable, zero or pre-epoch value renders as
// nothing: absence must never become an epoch date (the billed_till lesson).
export function formatImportedAt(iso: string | undefined): string | null {
  const date = parseRfc3339Instant(iso);
  if (!date) return null;

  const now = new Date();
  if (date.toDateString() === now.toDateString()) {
    const time = date.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
    return `Imported today at ${time}`;
  }
  return `Imported ${formatDay(date)}`;
}

// formatLastSynced turns the RFC3339 last-sync time into something readable.
// The same guard applies: a missing/zero/unparseable value renders as nothing,
// so a source that has never synced shows no line rather than an epoch date.
export function formatLastSynced(iso: string | undefined): string | null {
  const date = parseRfc3339Instant(iso);
  if (!date) return null;

  const now = new Date();
  if (date.toDateString() === now.toDateString()) {
    const time = date.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
    return `Synced today at ${time}`;
  }
  return `Synced ${formatDay(date)}`;
}

// formatRevalidateAt renders when the stored playlist code will next be
// re-checked against AdoboTV: a weekly look at whether it still works, not an
// expiry. Same guard — an absent/zero value renders nothing.
export function formatRevalidateAt(iso: string | undefined): string | null {
  const date = parseRfc3339Instant(iso);
  if (!date) return null;
  return `Next check ${formatDay(date)}`;
}
