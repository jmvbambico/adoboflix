import { render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import CustomPlayer from "./CustomPlayer";

// The proxy URL is built inside Shaka's request filter, which lives in a
// useEffect that only runs once a player is attached. Shaka is mocked here so
// the filter registers without a real media stack, and the test drives the
// exact callback the component hands to registerRequestFilter — the behaviour
// that regressed — rather than a reimplementation of it.
type RequestFilter = (type: number, request: { uris: string[] }) => void;

const shakaMock = vi.hoisted(() => {
  const state: { requestFilter: RequestFilter | null } = { requestFilter: null };

  class NetworkingEngine {
    registerRequestFilter(fn: RequestFilter): void {
      state.requestFilter = fn;
    }
    registerResponseFilter(): void {}
  }

  class Player {
    static isBrowserSupported(): boolean {
      return true;
    }
    attach(): Promise<void> {
      return Promise.resolve();
    }
    getNetworkingEngine(): NetworkingEngine {
      return new NetworkingEngine();
    }
    configure(): void {}
    // Never settles: the filter is registered before load() runs, and leaving
    // it pending keeps the test out of the play()/setState path.
    load(): Promise<void> {
      return new Promise<void>(() => {});
    }
    destroy(): void {}
    addEventListener(): void {}
  }

  return { state, Player, polyfill: { installAll(): void {} } };
});

vi.mock("shaka-player/dist/shaka-player.ui.js", () => ({
  default: { polyfill: shakaMock.polyfill, Player: shakaMock.Player },
}));

const CDN_MANIFEST = "https://cdn.example.com/live/index.m3u8";
const CDN_SEGMENT = "https://cdn.example.com/live/seg1.ts";

async function capturedFilter(): Promise<RequestFilter> {
  await waitFor(() => expect(shakaMock.state.requestFilter).toBeTypeOf("function"));
  const filter = shakaMock.state.requestFilter;
  if (!filter) throw new Error("CustomPlayer never registered a request filter");
  return filter;
}

function renderPlayer(userAgent?: string, referer?: string, videoUrl: string = CDN_MANIFEST) {
  return render(
    <CustomPlayer
      id="player-under-test"
      videoUrl={videoUrl}
      title="Test Stream"
      thumbnailUrl=""
      durationSeconds={0}
      userAgent={userAgent}
      referer={referer}
    />,
  );
}

describe("CustomPlayer proxy request filter", () => {
  beforeEach(() => {
    shakaMock.state.requestFilter = null;
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("adds no ua parameter when the stream configures no user agent", async () => {
    renderPlayer();
    const filter = await capturedFilter();

    const request = { uris: [CDN_SEGMENT] };
    filter(0, request);

    const url = new URL(request.uris[0]);
    expect(url.pathname).toBe("/api/v1/proxy");
    expect(url.searchParams.get("url")).toBe(CDN_SEGMENT);
    expect(url.searchParams.has("ua")).toBe(false);
  });

  it("forwards exactly the configured user agent", async () => {
    renderPlayer("A2Z-Custom/9.0");
    const filter = await capturedFilter();

    const request = { uris: [CDN_SEGMENT] };
    filter(0, request);

    const url = new URL(request.uris[0]);
    expect(url.searchParams.get("ua")).toBe("A2Z-Custom/9.0");
  });

  it("adds no ref parameter when the stream configures no referer", async () => {
    renderPlayer();
    const filter = await capturedFilter();

    const request = { uris: [CDN_SEGMENT] };
    filter(0, request);

    const url = new URL(request.uris[0]);
    expect(url.searchParams.has("ref")).toBe(false);
  });

  it("forwards exactly the configured referer", async () => {
    renderPlayer(undefined, "https://embed.example.com/player/abc");
    const filter = await capturedFilter();

    const request = { uris: [CDN_SEGMENT] };
    filter(0, request);

    const url = new URL(request.uris[0]);
    expect(url.searchParams.get("ref")).toBe("https://embed.example.com/player/abc");
  });

  it("forwards the manifest's source and keeps ua and ref closed", async () => {
    renderPlayer(undefined, undefined, `${CDN_MANIFEST}?source=DRM`);
    const filter = await capturedFilter();

    const request = { uris: [CDN_SEGMENT] };
    filter(0, request);

    const url = new URL(request.uris[0]);
    expect(url.searchParams.get("source")).toBe("DRM");
    expect(url.searchParams.has("ua")).toBe(false);
    expect(url.searchParams.has("ref")).toBe(false);
  });

  it("adds no source parameter when the manifest URL declares none", async () => {
    renderPlayer();
    const filter = await capturedFilter();

    const request = { uris: [CDN_SEGMENT] };
    filter(0, request);

    const url = new URL(request.uris[0]);
    expect(url.searchParams.has("source")).toBe(false);
  });
});
