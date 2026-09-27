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

// formatImportedAt turns the RFC3339 import time into something readable at a
// glance. A missing, unparseable, zero or pre-epoch value renders as nothing:
// absence must never become an epoch date (the billed_till lesson).
export function formatImportedAt(iso: string | undefined): string | null {
  if (!iso) return null;
  const date = new Date(iso);
  if (Number.isNaN(date.getTime()) || date.getTime() <= 0) return null;

  const now = new Date();
  if (date.toDateString() === now.toDateString()) {
    const time = date.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
    return `Imported today at ${time}`;
  }
  return `Imported ${date.toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  })}`;
}
