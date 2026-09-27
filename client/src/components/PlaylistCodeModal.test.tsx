import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { SourceStatus } from "../api/client";
import PlaylistCodeModal from "./PlaylistCodeModal";
import { SOURCE_STATUS_QUERY_KEY } from "./playlistCode";

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

interface Backend {
  configured: boolean;
  post: { status: number; body: { error?: string; code?: string } };
}

const STATUS_URL = "/api/v1/source/status";
const CODE_URL = "/api/v1/source/playlist-code";
const SECRET = "SUPER-SECRET-PLAYLIST-CODE";

function installBackend(init: Partial<Backend> = {}) {
  const backend: Backend = { configured: false, post: { status: 200, body: {} }, ...init };
  const calls: FetchCall[] = [];
  const statusBody = () => ({
    source: "adobotv-http",
    needs_playlist_code: true,
    playlist_code_configured: backend.configured,
  });

  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = (init?.method ?? "GET").toUpperCase();
      const body = typeof init?.body === "string" ? init.body : null;
      calls.push({ url, method, body });

      if (url.endsWith(STATUS_URL)) return makeResponse(200, statusBody());
      if (url.endsWith(CODE_URL) && method === "POST") {
        if (backend.post.status < 300) backend.configured = true;
        return makeResponse(backend.post.status, backend.post.body);
      }
      throw new Error(`unexpected fetch: ${method} ${url}`);
    }),
  );

  return { backend, calls };
}

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 5 * 60 * 1000 } },
  });
}

function renderModal(
  props: { configured: boolean; onClose: () => void },
  queryClient: QueryClient = makeQueryClient(),
) {
  return {
    queryClient,
    ...render(
      <QueryClientProvider client={queryClient}>
        <PlaylistCodeModal {...props} />
      </QueryClientProvider>,
    ),
  };
}

async function submit(code: string, buttonName: RegExp) {
  const input = await screen.findByLabelText("Playlist code");
  fireEvent.change(input, { target: { value: code } });
  fireEvent.click(screen.getByRole("button", { name: buttonName }));
}

describe("PlaylistCodeModal", () => {
  it("submits the code through the shared logic and closes on a full connect", async () => {
    const backend = installBackend();
    const onClose = vi.fn();
    renderModal({ configured: false, onClose });

    await submit(SECRET, /^connect$/i);

    await waitFor(() => expect(backend.calls.some((c) => c.method === "POST")).toBe(true));
    const post = backend.calls.find((c) => c.method === "POST");
    expect(post?.url.endsWith(CODE_URL)).toBe(true);
    expect(JSON.parse(post?.body ?? "{}")).toEqual({ code: SECRET });
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it("says 'change' rather than 'connect' when a code is already configured", async () => {
    installBackend({ configured: true });
    renderModal({ configured: true, onClose: vi.fn() });

    expect(screen.getByText(/change playlist code/i)).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: /^change code$/i })).toBeInTheDocument();
  });

  it("shows a saved-but-gated connect as saved and does not close", async () => {
    installBackend({ post: { status: 403, body: { error: "gate", code: "device_pending" } } });
    const onClose = vi.fn();
    renderModal({ configured: false, onClose });

    await submit(SECRET, /^connect$/i);

    expect(await screen.findByText("Playlist code saved")).toBeInTheDocument();
    expect(screen.getByText(/nothing needs re-entering/i)).toBeInTheDocument();
    // Positive: the modal is still open with a Done action.
    expect(screen.getByRole("button", { name: /done/i })).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
  });

  it("shows the failure copy and keeps the form for a rejected code", async () => {
    installBackend({
      post: { status: 403, body: { error: "rejected", code: "playlist_rejected" } },
    });
    renderModal({ configured: false, onClose: vi.fn() });

    await submit(SECRET, /^connect$/i);

    expect(await screen.findByText("Playlist code was rejected")).toBeInTheDocument();
    expect(screen.getByLabelText("Playlist code")).toBeInTheDocument();
  });

  it("closes on Escape", async () => {
    installBackend();
    const onClose = vi.fn();
    renderModal({ configured: false, onClose });

    fireEvent.keyDown(document, { key: "Escape" });

    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  // Pins the hook-level onSuccess the gate depends on, not just the modal's own
  // close: the optimistic status-cache write and the query invalidation must
  // still fire now that submitCode also passes a mutate-level onSuccess.
  it("fires the hook-level onSuccess: optimistic status cache and invalidation", async () => {
    installBackend();
    const queryClient = makeQueryClient();
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");

    let snapshot: SourceStatus | undefined;
    const onClose = vi.fn(() => {
      // Captured the instant the connect completes, before the invalidated
      // refetch can resolve, so this reads the optimistic write itself.
      snapshot = queryClient.getQueryData<SourceStatus>(SOURCE_STATUS_QUERY_KEY);
    });

    renderModal({ configured: false, onClose }, queryClient);
    await submit(SECRET, /^connect$/i);

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(invalidate).toHaveBeenCalled();
    expect(snapshot?.playlist_code_configured).toBe(true);
  });

  it("traps Tab within the dialog", async () => {
    installBackend();
    renderModal({ configured: false, onClose: vi.fn() });

    const close = screen.getByRole("button", { name: /close/i });
    const input = screen.getByLabelText("Playlist code");

    // Shift+Tab from the first focusable wraps to the last.
    close.focus();
    fireEvent.keyDown(close, { key: "Tab", shiftKey: true });
    expect(input).toHaveFocus();

    // Tab from the last focusable wraps to the first.
    input.focus();
    fireEvent.keyDown(input, { key: "Tab" });
    expect(close).toHaveFocus();
  });
});
