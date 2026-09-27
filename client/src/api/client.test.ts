import { describe, expect, it, vi } from "vitest";
import {
  ApiError,
  apiErrorFromResponse,
  fetchCategories,
  fetchChannels,
  fetchVideos,
  isApiError,
  mapEntryToVideo,
  shouldRetryQuery,
} from "./client";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

describe("isApiError", () => {
  it("accepts an ApiError and rejects anything else", () => {
    expect(isApiError(new ApiError("x", 500))).toBe(true);
    expect(isApiError(new Error("x"))).toBe(false);
    expect(isApiError(null)).toBe(false);
    expect(isApiError({ name: "ApiError", status: 500 })).toBe(false);
  });
});

describe("apiErrorFromResponse", () => {
  it("uses the backend envelope's error and code when present", () => {
    const err = apiErrorFromResponse(403, "/api/v1/entries", '{"error":"Device pending","code":"device_pending"}');
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 403, message: "Device pending", code: "device_pending", name: "ApiError" });
  });

  it("keeps the message but drops the code when the envelope omits it", () => {
    const err = apiErrorFromResponse(404, "/api/v1/entries", '{"error":"Content not found"}');
    expect(err.message).toBe("Content not found");
    expect(err.code).toBeUndefined();
  });

  it.each([
    ["a plain-text body", "Bad Gateway"],
    ["an empty body", ""],
    ["a JSON null", "null"],
    ["a JSON primitive", "123"],
    ["an object without a usable error", '{"error":42,"code":true}'],
    ["an empty error string", '{"error":""}'],
  ])("falls back to a non-empty message for %s", (_label, body) => {
    const err = apiErrorFromResponse(502, "/api/v1/entries", body);
    expect(err.message.startsWith("API 502 /api/v1/entries: ")).toBe(true);
    expect(err.code).toBeUndefined();
  });

  it("bounds the echoed body so a huge error page cannot flood the message", () => {
    const err = apiErrorFromResponse(500, "/x", "y".repeat(5000));
    expect(err.message.length).toBeLessThanOrEqual("API 500 /x: ".length + 200);
  });
});

describe("shouldRetryQuery", () => {
  it("stops retrying a settled 4xx answer", () => {
    for (const status of [400, 403, 404, 499]) {
      expect(shouldRetryQuery(0, new ApiError("nope", status))).toBe(false);
    }
  });

  it("retries a 5xx up to the bounded attempt count", () => {
    expect(shouldRetryQuery(0, new ApiError("boom", 503))).toBe(true);
    expect(shouldRetryQuery(1, new ApiError("boom", 503))).toBe(true);
    expect(shouldRetryQuery(2, new ApiError("boom", 503))).toBe(false);
  });

  it("treats a transport failure like a 5xx", () => {
    expect(shouldRetryQuery(1, new TypeError("Failed to fetch"))).toBe(true);
    expect(shouldRetryQuery(2, new TypeError("Failed to fetch"))).toBe(false);
  });
});

describe("mapEntryToVideo", () => {
  it("shapes a fully-populated backend entry", () => {
    const video = mapEntryToVideo({
      id: "42",
      name: "The Film",
      type: "Movie",
      category: "Drama",
      poster: "https://cdn/poster.jpg",
      background_image: "https://cdn/bg.jpg",
      plot: "A plot.",
      cast_members: ["A", "B"],
      directors: ["D"],
      release_year: 1999,
      rating: "9.87",
      provider: "adobotv",
      drm_type: "widevine",
      stream_url: "https://cdn/decoy",
    });

    expect(video).toMatchObject({
      id: "42",
      title: "The Film",
      description: "A plot.",
      category: "Drama",
      thumbnailUrl: "https://cdn/poster.jpg",
      backgroundUrl: "https://cdn/bg.jpg",
      year: 1999,
      imdbRating: 9.9,
      rating: "Movie",
      director: "D",
      cast: ["A", "B"],
      tags: ["Drama", "Movie", "adobotv"],
      type: "Movie",
      provider: "adobotv",
      drmType: "widevine",
      videoUrl: "",
    });
  });

  it("fills every optional field with a default when the entry is bare", () => {
    const video = mapEntryToVideo({ id: "1", name: "Nameless", type: "Series" });

    expect(video).toMatchObject({
      description: "No description available.",
      category: "Uncategorized",
      rating: "TV",
      imdbRating: 0,
      year: 0,
      director: "Unknown",
      cast: [],
      durationSeconds: 0,
      provider: "",
      drmType: "",
      streamUrl: "",
    });
    expect(video.thumbnailUrl).toMatch(/^https:\/\/images\.unsplash\.com/);
    expect(video.backgroundUrl).toBe(video.thumbnailUrl);
  });

  it("formats the duration from a raw second count", () => {
    expect(mapEntryToVideo({ id: "1", name: "x", type: "Movie" }).duration).toBe("—");
    expect(mapEntryToVideo({ id: "1", name: "x", type: "Movie", duration: 45 }).duration).toBe("45s");
    expect(mapEntryToVideo({ id: "1", name: "x", type: "Movie", duration: 90 }).duration).toBe("1m");
    expect(mapEntryToVideo({ id: "1", name: "x", type: "Movie", duration: 3660 }).duration).toBe("1h 1m");
  });

  it("normalises an unusual rating scale down to ten points", () => {
    expect(mapEntryToVideo({ id: "1", name: "x", type: "Movie", rating: "85" }).imdbRating).toBe(8.5);
    expect(mapEntryToVideo({ id: "1", name: "x", type: "Movie", rating: "abc" }).imdbRating).toBe(0);
  });
});

describe("fetch integration", () => {
  it("surfaces a non-OK response as an ApiError carrying the backend code", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        jsonResponse({ error: "Playlist code was rejected", code: "playlist_rejected" }, 403),
      ),
    );

    await expect(fetchVideos({})).rejects.toBeInstanceOf(ApiError);
    await expect(fetchVideos({})).rejects.toMatchObject({
      status: 403,
      code: "playlist_rejected",
      message: "Playlist code was rejected",
    });
  });

  it("prepends the synthetic All category to the genres the backend returns", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => jsonResponse({ genres: ["Drama", "News"] })));
    expect(await fetchCategories()).toEqual(["All", "Drama", "News"]);
  });

  it("filters channels by name on the client, case-insensitively", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        jsonResponse({
          channels: [
            { id: "1", name: "BBC One", status: "active" },
            { id: "2", name: "CNN", status: "active" },
          ],
          total: 2,
          page: 1,
          has_more: false,
        }),
      ),
    );

    const channels = await fetchChannels({ search: "bbc" });
    expect(channels.map((c) => c.name)).toEqual(["BBC One"]);
  });
});
