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

type Reply = { status: number; body: { error?: string; code?: string } };

interface Backend {
  configured: boolean;
  post: Reply;
  login: Reply;
}

const STATUS_URL = "/api/v1/source/status";
const CODE_URL = "/api/v1/source/playlist-code";
const LOGIN_URL = "/api/v1/source/login";
const SECRET = "SUPER-SECRET-PLAYLIST-CODE";
const PASSWORD = "SUPER-SECRET-PASSWORD";
const USERNAME = "alice";

function installBackend(init: Partial<Backend> = {}) {
  const backend: Backend = {
    configured: false,
    post: { status: 200, body: {} },
    login: { status: 200, body: {} },
    ...init,
  };
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
      const reply = method === "POST" && url.endsWith(CODE_URL)
        ? backend.post
        : method === "POST" && url.endsWith(LOGIN_URL)
          ? backend.login
          : null;
      if (reply) {
        if (reply.status < 300) backend.configured = true;
        return makeResponse(reply.status, reply.body);
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

async function submitCredentials(username: string, password: string) {
  fireEvent.change(await screen.findByLabelText("AdoboTV username"), { target: { value: username } });
  fireEvent.change(screen.getByLabelText("AdoboTV password"), { target: { value: password } });
  fireEvent.click(screen.getByRole("button", { name: /^connect$/i }));
}

async function submitPlaylistCode(code: string) {
  fireEvent.click(await screen.findByRole("button", { name: /only have a playlist code/i }));
  const input = await screen.findByLabelText("Playlist code");
  fireEvent.change(input, { target: { value: code } });
  fireEvent.click(screen.getByRole("button", { name: /^connect$/i }));
}

describe("PlaylistCodeModal", () => {
  it("signs in with username and password by default and closes on a full connect", async () => {
    const backend = installBackend();
    const onClose = vi.fn();
    renderModal({ configured: false, onClose });

    await submitCredentials(USERNAME, PASSWORD);

    await waitFor(() => expect(backend.calls.some((c) => c.method === "POST")).toBe(true));
    const post = backend.calls.find((c) => c.method === "POST");
    expect(post?.url.endsWith(LOGIN_URL)).toBe(true);
    expect(JSON.parse(post?.body ?? "{}")).toEqual({ username: USERNAME, password: PASSWORD });
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it("clears the password from the DOM after submitting and never renders it", async () => {
    installBackend({
      login: {
        status: 401,
        body: { error: "AdoboTV rejected the username or password", code: "invalid_credentials" },
      },
    });
    const { container } = renderModal({ configured: false, onClose: vi.fn() });

    const password = await screen.findByLabelText("AdoboTV password");
    fireEvent.change(screen.getByLabelText("AdoboTV username"), { target: { value: USERNAME } });
    fireEvent.change(password, { target: { value: PASSWORD } });
    // Positive: the masked field really held the password.
    expect(password).toHaveValue(PASSWORD);

    fireEvent.click(screen.getByRole("button", { name: /^connect$/i }));
    await screen.findByText("AdoboTV did not accept those credentials");

    // Negative: after submission the password is nowhere, and the field is empty.
    expect(container.innerHTML).not.toContain(PASSWORD);
    expect(screen.getByLabelText("AdoboTV password")).toHaveValue("");
  });

  it("shows the captcha copy distinctly, not as a rejected password", async () => {
    installBackend({ login: { status: 403, body: { error: "recaptcha", code: "captcha_required" } } });
    renderModal({ configured: false, onClose: vi.fn() });

    await submitCredentials(USERNAME, PASSWORD);

    expect(
      await screen.findByRole("heading", { name: "AdoboTV requires a captcha to log in" }),
    ).toBeInTheDocument();
    expect(screen.queryByText("AdoboTV did not accept those credentials")).not.toBeInTheDocument();
    expect(screen.getByLabelText("AdoboTV username")).toBeInTheDocument();
  });

  it("still accepts a playlist code as a secondary path", async () => {
    const backend = installBackend();
    const onClose = vi.fn();
    renderModal({ configured: false, onClose });

    await submitPlaylistCode(SECRET);

    await waitFor(() => expect(backend.calls.some((c) => c.method === "POST")).toBe(true));
    const post = backend.calls.find((c) => c.method === "POST");
    expect(post?.url.endsWith(CODE_URL)).toBe(true);
    expect(JSON.parse(post?.body ?? "{}")).toEqual({ code: SECRET });
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it("says 'reconnect' rather than 'connect' when a code is already configured", async () => {
    installBackend({ configured: true });
    renderModal({ configured: true, onClose: vi.fn() });

    expect(screen.getByText(/reconnect adobotv/i)).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: /^reconnect$/i })).toBeInTheDocument();
  });

  it("shows a saved-but-gated login as saved and does not close", async () => {
    installBackend({ login: { status: 403, body: { error: "gate", code: "device_pending" } } });
    const onClose = vi.fn();
    renderModal({ configured: false, onClose });

    await submitCredentials(USERNAME, PASSWORD);

    expect(await screen.findByText("Playlist code saved")).toBeInTheDocument();
    expect(screen.getByText(/nothing needs re-entering/i)).toBeInTheDocument();
    // Positive: the modal is still open with a Done action.
    expect(screen.getByRole("button", { name: /done/i })).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
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
  // still fire now that submitLogin also passes a mutate-level onSuccess.
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
    await submitCredentials(USERNAME, PASSWORD);

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(invalidate).toHaveBeenCalled();
    expect(snapshot?.playlist_code_configured).toBe(true);
  });

  it("traps Tab within the dialog", async () => {
    installBackend();
    renderModal({ configured: false, onClose: vi.fn() });

    const close = screen.getByRole("button", { name: /close/i });
    const toggle = screen.getByRole("button", { name: /only have a playlist code/i });

    // Shift+Tab from the first focusable wraps to the last.
    close.focus();
    fireEvent.keyDown(close, { key: "Tab", shiftKey: true });
    expect(toggle).toHaveFocus();

    // Tab from the last focusable wraps to the first.
    toggle.focus();
    fireEvent.keyDown(toggle, { key: "Tab" });
    expect(close).toHaveFocus();
  });
});
