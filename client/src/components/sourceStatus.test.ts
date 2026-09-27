import { describe, expect, it } from "vitest";
import { ApiError } from "../api/client";
import {
  describeSourceError,
  type SourceStatusSeverity,
} from "./sourceStatus";

// One row per code in the COPY map in sourceStatus.ts. Keeping the list here
// means a code that loses its mapping (or gains a wrong severity) fails a
// test rather than silently falling through to the generic copy.
const MAPPED: Array<{ code: string; severity: SourceStatusSeverity; title: string }> = [
  { code: "playlist_code_required", severity: "action", title: "This player needs a playlist code" },
  { code: "playlist_code_not_supported", severity: "blocked", title: "This source has no playlist code" },
  { code: "source_not_configured", severity: "action", title: "No source is connected yet" },
  { code: "source_pinned_by_env", severity: "blocked", title: "The source is pinned by configuration" },
  { code: "invalid_playlist", severity: "blocked", title: "That playlist could not be read" },
  { code: "playlist_too_large", severity: "blocked", title: "That playlist file is too large" },
  { code: "device_pending", severity: "action", title: "This device is awaiting approval" },
  { code: "subscription_inactive", severity: "action", title: "Subscription is not active" },
  { code: "playlist_rejected", severity: "blocked", title: "Playlist code was rejected" },
  { code: "user_agent_rejected", severity: "blocked", title: "This player is not on the allowlist" },
  { code: "playlist_format_m3u", severity: "upstream", title: "AdoboTV returned an unexpected format" },
  { code: "content_token_rejected", severity: "upstream", title: "AdoboTV rejected a content token" },
  { code: "malformed_playlist", severity: "upstream", title: "AdoboTV sent a malformed playlist" },
  { code: "malformed_drm", severity: "upstream", title: "AdoboTV sent a malformed DRM response" },
  { code: "malformed_vod_library", severity: "upstream", title: "AdoboTV sent a malformed VOD library" },
  { code: "upstream_error", severity: "upstream", title: "AdoboTV is unavailable" },
  { code: "content_not_found", severity: "notfound", title: "Content not found" },
  { code: "not_found", severity: "notfound", title: "Not found" },
];

describe("describeSourceError — mapped codes", () => {
  it.each(MAPPED)("maps $code to the $severity copy", ({ code, severity, title }) => {
    const copy = describeSourceError(new ApiError("upstream said no", 403, code));
    expect(copy).toMatchObject({ code, severity, title });
  });

  // Every mapped code is required to carry all four fields, so presence is
  // asserted before content: an explicit type check for each, then a content
  // check that substitutes "" for a missing value. Without the type checks a
  // field that is absent — `undefined?.trim()` — passes a bare `not.toBe("")`.
  it.each(MAPPED)("gives every mapped code full, user-facing copy for $code", ({ code }) => {
    const copy = describeSourceError(new ApiError("x", 500, code));
    expect(typeof copy.title).toBe("string");
    expect(typeof copy.message).toBe("string");
    expect(typeof copy.hint).toBe("string");
    expect(typeof copy.retryLabel).toBe("string");
    expect(copy.title.trim()).not.toBe("");
    expect(copy.message.trim()).not.toBe("");
    expect((copy.hint ?? "").trim()).not.toBe("");
    expect((copy.retryLabel ?? "").trim()).not.toBe("");
  });

  it("can produce every severity the type allows", () => {
    const produced = new Set<SourceStatusSeverity>([
      describeSourceError(new ApiError("x", 403, "device_pending")).severity,
      describeSourceError(new ApiError("x", 403, "playlist_rejected")).severity,
      describeSourceError(new ApiError("x", 502, "upstream_error")).severity,
      describeSourceError(new ApiError("x", 404, "not_found")).severity,
      describeSourceError(new Error("transport down")).severity,
    ]);
    expect(produced).toEqual(
      new Set<SourceStatusSeverity>(["action", "blocked", "upstream", "notfound", "error"]),
    );
  });
});

describe("describeSourceError — unmapped ApiErrors", () => {
  it("falls back to the generic copy for internal_error but keeps the code", () => {
    const copy = describeSourceError(new ApiError("boom", 500, "internal_error"));
    expect(copy).toMatchObject({ code: "internal_error", severity: "error" });
    expect(copy.title).toBe("Something went wrong");
  });

  it("falls back to the generic copy for a code it does not know", () => {
    const copy = describeSourceError(new ApiError("boom", 500, "brand_new_code"));
    expect(copy).toMatchObject({ code: "brand_new_code", severity: "error" });
    expect(copy.title).toBe("Something went wrong");
  });

  it("returns the generic copy with no code when the ApiError carries none", () => {
    const copy = describeSourceError(new ApiError("boom", 500));
    expect(copy.severity).toBe("error");
    expect(copy.title).toBe("Something went wrong");
    expect(copy.code).toBeUndefined();
  });

  it("never surfaces the raw error message to the screen", () => {
    const secret = "RAW UPSTREAM BODY THAT MUST NOT RENDER";
    const copy = describeSourceError(new ApiError(secret, 500, "internal_error"));
    expect(copy.message).not.toContain(secret);
    expect(copy.title).not.toContain(secret);
  });
});

describe("describeSourceError — awkward inputs", () => {
  const awkward: Array<[label: string, value: unknown]> = [
    ["null", null],
    ["undefined", undefined],
    ["a bare string", "device_pending"],
    ["a number", 42],
    ["a boolean", true],
    ["a plain Error", new Error("device_pending")],
    ["an object with no recognised fields", {}],
    ["an array", ["device_pending"]],
    ["a function", () => "device_pending"],
    [
      "a payload shaped like an ApiError but not one",
      { name: "ApiError", message: "device_pending", status: 403, code: "device_pending" },
    ],
  ];

  it.each(awkward)("treats %s as unreachable, never as a mapped code", (_label, value) => {
    const copy = describeSourceError(value);
    expect(copy.severity).toBe("error");
    expect(copy.title).toBe("Cannot reach AdoboFlix");
    expect(copy.code).toBeUndefined();
  });
});
