import { describe, expect, it } from "vitest";
import { describePlaylistContents, formatImportedAt } from "./playlistFacts";

describe("describePlaylistContents", () => {
  it("names channels when the playlist holds channels", () => {
    expect(describePlaylistContents(0, 3)).toBe("3 channels loaded");
  });

  it("names titles when the playlist holds titles", () => {
    expect(describePlaylistContents(5, 0)).toBe("5 titles loaded");
  });

  it("names both when the playlist holds both", () => {
    expect(describePlaylistContents(5, 3)).toBe("3 channels and 5 titles loaded");
  });

  it("pluralises a single channel and title", () => {
    expect(describePlaylistContents(1, 1)).toBe("1 channel and 1 title loaded");
  });

  it("describes a genuinely empty playlist distinctly", () => {
    expect(describePlaylistContents(0, 0)).toMatch(/has no channels or titles/i);
  });

  // While a count is still unknown, silence is correct: claiming "empty" would
  // assert a state we have not yet seen.
  it("says nothing until both counts are known", () => {
    expect(describePlaylistContents(undefined, undefined)).toBeNull();
    expect(describePlaylistContents(0, undefined)).toBeNull();
    expect(describePlaylistContents(undefined, 0)).toBeNull();
  });

  // A known-but-zero count on one side must not suppress the other side's real
  // content.
  it("reports the known side while the other is still unknown", () => {
    expect(describePlaylistContents(5, undefined)).toBe("5 titles loaded");
    expect(describePlaylistContents(undefined, 3)).toBe("3 channels loaded");
  });
});

describe("formatImportedAt", () => {
  it("renders a past import as a readable date, not the raw timestamp", () => {
    const text = formatImportedAt("2026-03-12T09:30:00Z");
    expect(text).toMatch(/^Imported /);
    expect(text).toContain("2026");
    expect(text).not.toContain("2026-03-12T09:30:00Z");
  });

  it("renders today's import as today, with a time", () => {
    const text = formatImportedAt(new Date().toISOString());
    expect(text).toMatch(/^Imported today at /);
  });

  // The billed_till lesson: absence must never become an epoch date.
  it.each([
    ["undefined", undefined],
    ["empty", ""],
    ["unparseable", "not-a-date"],
    ["the epoch itself", "1970-01-01T00:00:00Z"],
    ["Go's zero time", "0001-01-01T00:00:00Z"],
  ])("renders nothing for %s", (_label, value) => {
    expect(formatImportedAt(value)).toBeNull();
  });
});
