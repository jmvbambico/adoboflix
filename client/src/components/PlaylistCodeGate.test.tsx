import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { ApiError, type SourceStatus } from "../api/client";
import PlaylistCodeGate from "./PlaylistCodeGate";
import { SOURCE_STATUS_QUERY_KEY } from "./playlistCode";

// A minimal Response the component's api layer can consume. Avoids depending on
// a global Response being present in the jsdom environment.
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
  needs: boolean;
  configured: boolean;
  // The reply POST /source/playlist-code gives.
  post: { status: number; body: { error?: string; code?: string } };
}

const STATUS_URL = "/api/v1/source/status";
const CODE_URL = "/api/v1/source/playlist-code";

// The codes the server keeps the submitted code for, mirroring
// playlistCodeProvenValid. The mock persists on these (and on any 2xx) so a
// remount sees the same server state the real backend would report.
const SAVED_CODES = [
  "device_pending",
  "subscription_inactive",
  "playlist_format_m3u",
  "content_token_rejected",
  "content_not_found",
] as const;

// Codes for which nothing is persisted and the user must correct the code.
const FAILED_CODES = ["playlist_rejected", "upstream_error", "malformed_playlist"] as const;

function installBackend(init: Partial<Backend> = {}) {
  const backend: Backend = {
    needs: true,
    configured: false,
    post: { status: 200, body: {} },
    ...init,
  };
  const calls: FetchCall[] = [];

  const statusBody = () => ({
    source: "adobotv-http",
    needs_playlist_code: backend.needs,
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
      if (url.endsWith(CODE_URL)) {
        if (method === "POST") {
          const { status, body: reply } = backend.post;
          const code = reply.code;
          if (status < 300 || (code !== undefined && (SAVED_CODES as readonly string[]).includes(code))) {
            backend.configured = true;
          }
          return makeResponse(status, reply);
        }
        if (method === "DELETE") {
          backend.configured = false;
          return makeResponse(200, statusBody());
        }
      }
      throw new Error(`unexpected fetch: ${method} ${url}`);
    }),
  );

  return { backend, calls };
}

// A long staleTime, like the app's own client, so a remounted query reuses the
// cached answer rather than silently refetching. That is what exposes a status
// cache left stale by a saved connect.
function makeQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 5 * 60 * 1000 } },
  });
}

function renderGateWith(queryClient: QueryClient, errors?: unknown[]) {
  return render(
    <QueryClientProvider client={queryClient}>
      <PlaylistCodeGate errors={errors} />
    </QueryClientProvider>,
  );
}

function renderGate(errors?: unknown[]) {
  const queryClient = makeQueryClient();
  return { queryClient, ...renderGateWith(queryClient, errors) };
}

const SECRET = "SUPER-SECRET-PLAYLIST-CODE";

async function submitCode(code: string) {
  const input = await screen.findByLabelText("Playlist code");
  fireEvent.change(input, { target: { value: code } });
  fireEvent.click(screen.getByRole("button", { name: /connect/i }));
}

describe("PlaylistCodeGate — entry", () => {
  it("shows the entry form when the status says a code is needed", async () => {
    installBackend({ configured: false });
    renderGate();

    expect(await screen.findByLabelText("Playlist code")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /connect/i })).toBeInTheDocument();
  });

  // The connected happy path is silent. The gate no longer renders a
  // "configured" panel or a Clear control — the account menu owns disconnecting
  // — so this asserts the whole gate subtree is gone, not merely the form.
  // Paired with the entry tests: not connected → form; connected → nothing.
  it("renders nothing when a code is already configured", async () => {
    installBackend({ configured: true });
    const { container, queryClient } = renderGate();

    // Guarantee the status has actually RESOLVED before measuring. The gate
    // renders nothing both while loading (status undefined) and when connected,
    // so an absence on its own would pass on the first tick — before the query
    // settled — and would stay green even if a configured panel came back.
    // Waiting for the resolved data makes the emptiness assertion meaningful.
    await waitFor(() =>
      expect(queryClient.getQueryData<SourceStatus>(SOURCE_STATUS_QUERY_KEY)).toBeDefined(),
    );

    expect(container.firstChild).toBeNull();
    // Negative: neither the credential field nor a duplicate clear action.
    expect(screen.queryByLabelText("Playlist code")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /clear playlist code/i })).not.toBeInTheDocument();
  });

  it("renders nothing for a source that takes no playlist code", async () => {
    installBackend({ needs: false, configured: false });
    const { container, queryClient } = renderGate();

    // As above: wait for the resolved status, then assert the emptiness.
    await waitFor(() =>
      expect(queryClient.getQueryData<SourceStatus>(SOURCE_STATUS_QUERY_KEY)).toBeDefined(),
    );
    expect(container.firstChild).toBeNull();
  });

  it("still opens the entry form for a required error when no code is configured", async () => {
    installBackend({ configured: false });
    renderGate([new ApiError("needs a code", 403, "playlist_code_required")]);

    expect(await screen.findByLabelText("Playlist code")).toBeInTheDocument();
  });

  it("defers to server truth: a configured status renders nothing even for a required error", async () => {
    installBackend({ configured: true });
    const { container } = renderGate([new ApiError("needs a code", 403, "playlist_code_required")]);

    // The required error momentarily shows the form until the status resolves;
    // once it says configured, server truth wins and the gate goes quiet.
    await waitFor(() => expect(screen.queryByLabelText("Playlist code")).not.toBeInTheDocument());
    expect(container.firstChild).toBeNull();
  });
});

describe("PlaylistCodeGate — submission", () => {
  it("posts the entered value to the playlist-code endpoint", async () => {
    const backend = installBackend();
    renderGate();

    await submitCode(SECRET);

    await waitFor(() => expect(backend.calls.some((c) => c.method === "POST")).toBe(true));
    const post = backend.calls.filter((c) => c.method === "POST");
    expect(post).toHaveLength(1);
    expect(post[0].url.endsWith(CODE_URL)).toBe(true);
    expect(JSON.parse(post[0].body ?? "{}")).toEqual({ code: SECRET });
  });

  it("clears the entered code from the DOM after submitting and never renders it", async () => {
    const backend = installBackend({
      post: { status: 403, body: { error: "playlist code rejected", code: "playlist_rejected" } },
    });
    const { container } = renderGate();

    const input = await screen.findByLabelText("Playlist code");
    fireEvent.change(input, { target: { value: SECRET } });
    // Positive: the value is held by the masked field before submission.
    expect(input).toHaveValue(SECRET);

    fireEvent.click(screen.getByRole("button", { name: /connect/i }));
    await screen.findByText("Playlist code was rejected");

    // The POST really carried the code, so the absence below is not vacuous.
    const post = backend.calls.find((c) => c.method === "POST");
    expect(post?.body).toContain(SECRET);
    // Negative: after submission the credential appears nowhere in the output,
    // and the field is emptied.
    expect(container.innerHTML).not.toContain(SECRET);
    expect(screen.getByLabelText("Playlist code")).toHaveValue("");
  });
});

describe("PlaylistCodeGate — saved outcomes", () => {
  it.each(SAVED_CODES)("treats %s as a saved code and hides the form", async (code) => {
    installBackend({ post: { status: 403, body: { error: "gate", code } } });
    renderGate();

    await submitCode(SECRET);

    // Positive: the saved reassurance is shown.
    expect(await screen.findByRole("heading", { name: /playlist code saved/i })).toBeInTheDocument();
    // Negative: nothing to re-enter, so no form.
    expect(screen.queryByLabelText("Playlist code")).not.toBeInTheDocument();
  });

  it("explains a device_pending save and does not ask the user to re-enter", async () => {
    installBackend({
      post: { status: 403, body: { error: "device pending", code: "device_pending" } },
    });
    const { container } = renderGate();

    await submitCode(SECRET);

    expect(await screen.findByRole("heading", { name: /playlist code saved/i })).toBeInTheDocument();
    expect(screen.getByText(/nothing needs re-entering/i)).toBeInTheDocument();
    expect(screen.getByText(/once the AdoboTV operator approves this device/i)).toBeInTheDocument();
    expect(screen.queryByLabelText("Playlist code")).not.toBeInTheDocument();
    expect(screen.queryByText("Playlist code was rejected")).not.toBeInTheDocument();
    expect(container.innerHTML).not.toContain(SECRET);
  });

  it("explains a subscription_inactive save in its own terms", async () => {
    installBackend({
      post: {
        status: 403,
        body: { error: "subscription inactive", code: "subscription_inactive" },
      },
    });
    renderGate();

    await submitCode(SECRET);

    expect(await screen.findByRole("heading", { name: /playlist code saved/i })).toBeInTheDocument();
    expect(screen.getByText(/once your subscription is active again/i)).toBeInTheDocument();
    expect(screen.queryByLabelText("Playlist code")).not.toBeInTheDocument();
  });

  it("does not ask for the code again after a remount, because the status cache was updated", async () => {
    installBackend({
      post: { status: 403, body: { error: "device pending", code: "device_pending" } },
    });
    const queryClient = makeQueryClient();
    const first = renderGateWith(queryClient);

    await submitCode(SECRET);
    await screen.findByRole("heading", { name: /playlist code saved/i });
    first.unmount();

    const remounted = renderGateWith(queryClient);

    // Positive guarantee before the absences: the shared cache really does say
    // configured, so the emptiness below measures the connected state and not an
    // unresolved query.
    expect(queryClient.getQueryData<SourceStatus>(SOURCE_STATUS_QUERY_KEY)).toMatchObject({
      playlist_code_configured: true,
    });

    // Negative: an ordinary remount must not resurrect the entry form for a
    // code the server already saved.
    expect(screen.queryByLabelText("Playlist code")).not.toBeInTheDocument();
    // And the connected state is silent — the gate renders nothing, having no
    // configured panel to show either.
    expect(remounted.container.firstChild).toBeNull();
  });
});

describe("PlaylistCodeGate — failed outcomes", () => {
  it.each(FAILED_CODES)("treats %s as not saved and keeps the form", async (code) => {
    installBackend({ post: { status: 403, body: { error: "no", code } } });
    renderGate();

    await submitCode(SECRET);

    // Positive: the form is still offered so the code can be corrected.
    expect(await screen.findByLabelText("Playlist code")).toBeInTheDocument();
    // Negative: nothing was persisted, so the saved reassurance is absent.
    expect(screen.queryByRole("heading", { name: /playlist code saved/i })).not.toBeInTheDocument();
  });

  it("renders the retry copy for playlist_rejected", async () => {
    installBackend({
      post: { status: 403, body: { error: "playlist code rejected", code: "playlist_rejected" } },
    });
    renderGate();

    await submitCode(SECRET);

    expect(await screen.findByRole("heading", { name: "Playlist code was rejected" })).toBeInTheDocument();
    expect(screen.getByText(/Check the configured playlist code, then try again/i)).toBeInTheDocument();
    expect(screen.getByLabelText("Playlist code")).toBeInTheDocument();
  });

  it("shows the unreachable copy for a transport failure", async () => {
    installBackend();
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        if (String(input).endsWith(STATUS_URL)) {
          return makeResponse(200, {
            source: "adobotv-http",
            needs_playlist_code: true,
            playlist_code_configured: false,
          });
        }
        throw new TypeError("Failed to fetch");
      }),
    );
    renderGate();

    await submitCode(SECRET);

    expect(await screen.findByRole("heading", { name: "Cannot reach AdoboFlix" })).toBeInTheDocument();
    expect(screen.getByLabelText("Playlist code")).toBeInTheDocument();
  });
});

describe("PlaylistCodeGate — success", () => {
  it("invalidates the caches instead of reloading the page", async () => {
    installBackend();
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
    const { queryClient } = renderGate();
    const invalidate = vi.spyOn(queryClient, "invalidateQueries");

    await submitCode(SECRET);

    await waitFor(() => expect(invalidate).toHaveBeenCalled());
    expect(reload).not.toHaveBeenCalled();
    // The gate clears once the code is configured.
    await waitFor(() => expect(screen.queryByLabelText("Playlist code")).not.toBeInTheDocument());
  });
});

// Clearing a code is no longer the gate's job: it is the account menu's
// Disconnect, with its own confirmation. That path — including the DELETE — is
// covered in AccountMenu.test.tsx; the gate deliberately renders no clear
// control to duplicate it.
