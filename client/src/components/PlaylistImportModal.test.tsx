import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import PlaylistImportModal from "./PlaylistImportModal";

function makeResponse(status: number, body: unknown): Response {
  const text = body === undefined ? "" : JSON.stringify(body);
  return {
    ok: status >= 200 && status < 300,
    status,
    text: async () => text,
    json: async () => JSON.parse(text) as unknown,
  } as Response;
}

interface FetchCall {
  url: string;
  method: string;
  body: string | null;
}

interface BackendOptions {
  // The reply POST /source/playlist-file gives.
  post?: { status: number; body: { error?: string; code?: string } };
}

const FILE_URL = "/api/v1/source/playlist-file";
const STATUS_URL = "/api/v1/source/status";

// A successful import returns the new status the server already swapped to.
const IMPORTED_STATUS = {
  source: "file",
  active: true,
  origin: "stored",
  dev: false,
  needs_playlist_code: false,
  playlist_code_configured: false,
  playlist_file_configured: true,
  modes: [
    {
      name: "file",
      selectable: true,
      dev: false,
      active: true,
      configured: true,
      needs_playlist_code: false,
    },
  ],
};

function installBackend(init: BackendOptions = {}) {
  const post = init.post ?? { status: 200, body: {} };
  const calls: FetchCall[] = [];

  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = (init?.method ?? "GET").toUpperCase();
      const body = typeof init?.body === "string" ? init.body : null;
      calls.push({ url, method, body });

      if (url.endsWith(STATUS_URL)) return makeResponse(200, IMPORTED_STATUS);
      if (url.endsWith(FILE_URL) && method === "POST") {
        return makeResponse(post.status, post.status < 300 ? IMPORTED_STATUS : post.body);
      }
      throw new Error(`unexpected fetch: ${method} ${url}`);
    }),
  );

  return { calls };
}

function renderModal() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 5 * 60 * 1000 } },
  });
  const onClose = vi.fn();
  const result = render(
    <QueryClientProvider client={queryClient}>
      <PlaylistImportModal onClose={onClose} />
    </QueryClientProvider>,
  );
  return { ...result, queryClient, onClose };
}

// jsdom's file input makes `files` read-only, so install a FileList-like array
// before firing the change the component listens for.
function chooseFile(file: File) {
  const input = screen.getByLabelText("Playlist file");
  Object.defineProperty(input, "files", { configurable: true, value: [file] });
  fireEvent.change(input);
  return input;
}

const JSON_PLAYLIST = JSON.stringify({ name: "local", channels: [] });

describe("PlaylistImportModal", () => {
  it("accepts JSON, M3U and M3U8 files", () => {
    installBackend();
    renderModal();

    expect(screen.getByLabelText("Playlist file")).toHaveAttribute("accept", ".json,.m3u,.m3u8");
  });

  it("posts the chosen file's contents to the import endpoint and invalidates instead of reloading", async () => {
    const { calls } = installBackend();
    // jsdom makes location.reload non-configurable, so it cannot be spied in
    // place; replace the global with a stand-in that records a reload. A future
    // change that reaches for location.reload() is caught here.
    const reload = vi.fn();
    vi.stubGlobal("location", {
      href: "http://localhost/",
      origin: "http://localhost",
      protocol: "http:",
      host: "localhost",
      hostname: "localhost",
      port: "",
      pathname: "/",
      search: "",
      hash: "",
      reload,
      assign: vi.fn(),
      replace: vi.fn(),
      toString: () => "http://localhost/",
    });
    const { queryClient } = renderModal();
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");

    chooseFile(new File([JSON_PLAYLIST], "playlist.json", { type: "application/json" }));

    await waitFor(() => expect(invalidate).toHaveBeenCalled());
    const post = calls.find((c) => c.method === "POST");
    expect(post?.url.endsWith(FILE_URL)).toBe(true);
    // The playlist is the request body itself, not a JSON envelope wrapping it.
    expect(post?.body).toBe(JSON_PLAYLIST);
    expect(reload).not.toHaveBeenCalled();
    expect(await screen.findByText("Playlist imported")).toBeInTheDocument();
  });

  it("renders the server's invalid_playlist message verbatim", async () => {
    const message =
      "the playlist could not be parsed as JSON: unexpected end of JSON input. See the documented format in docs/source-adapters.md.";
    installBackend({ post: { status: 400, body: { error: message, code: "invalid_playlist" } } });
    renderModal();

    chooseFile(new File(["{ not json"], "broken.json", { type: "application/json" }));

    // Positive: the server's own words, unchanged, so the parse cause and the
    // pointer to the documented format both survive.
    expect(await screen.findByText(message)).toBeInTheDocument();
    expect(screen.getByText("That playlist could not be read")).toBeInTheDocument();
    // Negative: it is not the fallback copy paraphrasing it.
    expect(
      screen.queryByText("The file you chose is not a playlist AdoboFlix can read."),
    ).not.toBeInTheDocument();
    // The picker stays so the user can choose a corrected file.
    expect(screen.getByLabelText("Playlist file")).toBeInTheDocument();
  });

  it("renders the playlist_too_large copy for a 413", async () => {
    installBackend({
      post: {
        status: 413,
        body: { error: "the playlist is too large: the limit is 10 MiB", code: "playlist_too_large" },
      },
    });
    renderModal();

    chooseFile(new File([JSON_PLAYLIST], "huge.json", { type: "application/json" }));

    expect(await screen.findByText("That playlist file is too large")).toBeInTheDocument();
    expect(screen.getByText(/exceeds the 10 MiB import limit/i)).toBeInTheDocument();
  });

  it("renders the source_pinned_by_env copy and does not offer the picker again", async () => {
    installBackend({
      post: {
        status: 409,
        body: { error: "the content source is pinned by ADOBOFLIX_SOURCE", code: "source_pinned_by_env" },
      },
    });
    renderModal();

    chooseFile(new File([JSON_PLAYLIST], "playlist.json", { type: "application/json" }));

    // Positive: the pin is explained.
    expect(await screen.findByText("The source is pinned by configuration")).toBeInTheDocument();
    expect(screen.getByText(/pinned to a content source by its server configuration/i)).toBeInTheDocument();
    // Negative: no picker offering an action that would fail again.
    expect(screen.queryByLabelText("Playlist file")).not.toBeInTheDocument();
  });

  it("closes on Escape", async () => {
    installBackend();
    const { onClose } = renderModal();

    fireEvent.keyDown(document, { key: "Escape" });

    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });
});
