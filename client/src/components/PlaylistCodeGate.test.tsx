import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/client";
import PlaylistCodeGate from "./PlaylistCodeGate";

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
  // The reply POST /source/playlist-code gives. On a 2xx the code is treated as
  // saved, matching the server.
  post: { status: number; body: unknown };
}

const STATUS_URL = "/api/v1/source/status";
const CODE_URL = "/api/v1/source/playlist-code";

function installBackend(init: Partial<Backend> = {}) {
  const backend: Backend = {
    needs: true,
    configured: false,
    post: {
      status: 200,
      body: { source: "adobotv-http", needs_playlist_code: true, playlist_code_configured: true },
    },
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
          if (status >= 200 && status < 300) backend.configured = true;
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

function renderGate(errors?: unknown[]) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return {
    queryClient,
    ...render(
      <QueryClientProvider client={queryClient}>
        <PlaylistCodeGate errors={errors} />
      </QueryClientProvider>,
    ),
  };
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

  it("shows no entry form when a code is already configured, and offers to clear it", async () => {
    installBackend({ configured: true });
    renderGate();

    // Positive: the configured state is rendered with its clear action.
    expect(await screen.findByRole("button", { name: /clear playlist code/i })).toBeInTheDocument();
    // Negative: the credential field is not offered when one is configured.
    expect(screen.queryByLabelText("Playlist code")).not.toBeInTheDocument();
  });

  it("renders nothing for a source that takes no playlist code", async () => {
    installBackend({ needs: false, configured: false });
    const { container } = renderGate();

    await waitFor(() =>
      expect(
        screen.queryByRole("heading", { name: /playlist code/i }),
      ).not.toBeInTheDocument(),
    );
    expect(container.firstChild).toBeNull();
  });

  it("shows the form when a request fails with playlist_code_required even if status says configured", async () => {
    installBackend({ configured: true });
    renderGate([new ApiError("needs a code", 403, "playlist_code_required")]);

    // Positive: the error signal opens the entry path.
    expect(await screen.findByLabelText("Playlist code")).toBeInTheDocument();
    // Negative: the configured-state control gives way to the form.
    expect(screen.queryByRole("button", { name: /clear playlist code/i })).not.toBeInTheDocument();
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

describe("PlaylistCodeGate — outcomes", () => {
  it("treats device_pending as saved and does not ask the user to re-enter", async () => {
    installBackend({
      post: { status: 403, body: { error: "device pending", code: "device_pending" } },
    });
    const { container } = renderGate();

    await submitCode(SECRET);

    // Positive: the saved reassurance is shown, naming the approval gate.
    expect(await screen.findByRole("heading", { name: /playlist code saved/i })).toBeInTheDocument();
    expect(screen.getByText(/nothing needs re-entering/i)).toBeInTheDocument();
    expect(screen.getByText(/once the AdoboTV operator approves this device/i)).toBeInTheDocument();
    // Negative: no entry form, and no rejection copy.
    expect(screen.queryByLabelText("Playlist code")).not.toBeInTheDocument();
    expect(screen.queryByText("Playlist code was rejected")).not.toBeInTheDocument();
    expect(container.innerHTML).not.toContain(SECRET);
  });

  it("treats subscription_inactive as saved too", async () => {
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

  it("treats a persisted non-device gate (m3u format) as saved, mirroring the server", async () => {
    installBackend({
      post: { status: 502, body: { error: "output format m3u", code: "playlist_format_m3u" } },
    });
    renderGate();

    await submitCode(SECRET);

    expect(await screen.findByRole("heading", { name: /playlist code saved/i })).toBeInTheDocument();
    expect(screen.queryByLabelText("Playlist code")).not.toBeInTheDocument();
  });

  it("renders the retry copy for playlist_rejected and keeps the form", async () => {
    installBackend({
      post: { status: 403, body: { error: "playlist code rejected", code: "playlist_rejected" } },
    });
    renderGate();

    await submitCode(SECRET);

    // Positive: the mapped rejection copy, and the form is still offered.
    expect(await screen.findByRole("heading", { name: "Playlist code was rejected" })).toBeInTheDocument();
    expect(screen.getByText(/Check the configured playlist code, then try again/i)).toBeInTheDocument();
    expect(screen.getByLabelText("Playlist code")).toBeInTheDocument();
    // Negative: nothing was persisted, so the saved reassurance is absent.
    expect(screen.queryByText(/nothing needs re-entering/i)).not.toBeInTheDocument();
  });

  it("shows the unreachable copy for a transport failure", async () => {
    installBackend();
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input).endsWith(STATUS_URL)) {
          return makeResponse(200, {
            source: "adobotv-http",
            needs_playlist_code: true,
            playlist_code_configured: false,
          });
        }
        void init;
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

describe("PlaylistCodeGate — clearing", () => {
  it("sends DELETE when the clear control is used, then re-offers entry", async () => {
    const backend = installBackend({ configured: true });
    renderGate();

    fireEvent.click(await screen.findByRole("button", { name: /clear playlist code/i }));

    await waitFor(() => expect(backend.calls.some((c) => c.method === "DELETE")).toBe(true));
    expect(await screen.findByLabelText("Playlist code")).toBeInTheDocument();
  });
});
