import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { ApiError, type SourceStatus } from "../api/client";
import PlaylistCodeGate from "./PlaylistCodeGate";
import { SOURCE_STATUS_QUERY_KEY, usePlaylistCodeController } from "./playlistCode";

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

// A promise whose settlement a test controls, so a request can be held in flight
// while the component is unmounted or a second request is fired.
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

// The status body for an active, code-needing, unconfigured source. Used by the
// login tests that stub fetch for themselves rather than through installBackend.
function activeStatusBody() {
  return {
    source: "adobotv-http",
    active: true,
    origin: "stored",
    dev: false,
    needs_playlist_code: true,
    playlist_code_configured: false,
    playlist_file_configured: false,
    modes: [
      {
        name: "adobotv-http",
        selectable: true,
        dev: false,
        active: true,
        configured: false,
        needs_playlist_code: true,
      },
      {
        name: "file",
        selectable: true,
        dev: false,
        active: false,
        configured: false,
        needs_playlist_code: false,
      },
    ],
  };
}

// The UI disables its Connect button while a login is pending, so a concurrent
// login is only reachable by calling the controller directly. This harness
// mounts it and hands the test the live submitLogin.
let capturedController: ReturnType<typeof usePlaylistCodeController> | null = null;
function ControllerHarness() {
  capturedController = usePlaylistCodeController();
  return null;
}

interface FetchCall {
  url: string;
  method: string;
  body: string | null;
}

interface Backend {
  needs: boolean;
  configured: boolean;
  // Whether any source is configured. False is the first-run state, where the
  // gate must render the chooser rather than a strip above an empty library.
  active: boolean;
  // The reply POST /source/playlist-code gives.
  post: { status: number; body: { error?: string; code?: string } };
  // The reply POST /source/login gives.
  login: { status: number; body: { error?: string; code?: string } };
}

const STATUS_URL = "/api/v1/source/status";
const CODE_URL = "/api/v1/source/playlist-code";
const LOGIN_URL = "/api/v1/source/login";

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
    active: true,
    post: { status: 200, body: {} },
    login: { status: 200, body: {} },
    ...init,
  };
  const calls: FetchCall[] = [];

  // The full status contract the server reports: the two selectable modes are
  // offered from these flags, never from their names.
  const statusBody = () => ({
    source: backend.active ? "adobotv-http" : "",
    active: backend.active,
    origin: backend.active ? "stored" : "none",
    dev: false,
    needs_playlist_code: backend.needs,
    playlist_code_configured: backend.configured,
    playlist_file_configured: false,
    modes: [
      {
        name: "adobotv-http",
        selectable: true,
        dev: false,
        active: backend.active,
        configured: backend.configured,
        needs_playlist_code: true,
      },
      {
        name: "file",
        selectable: true,
        dev: false,
        active: false,
        configured: false,
        needs_playlist_code: false,
      },
    ],
  });

  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = (init?.method ?? "GET").toUpperCase();
      const body = typeof init?.body === "string" ? init.body : null;
      calls.push({ url, method, body });

      if (url.endsWith(STATUS_URL)) return makeResponse(200, statusBody());
      // Both connect endpoints share one reply shape and one "was it kept"
      // rule, so a login that proves the code valid persists it exactly as a
      // typed code does.
      const connect =
        method === "POST" && url.endsWith(CODE_URL)
          ? backend.post
          : method === "POST" && url.endsWith(LOGIN_URL)
            ? backend.login
            : null;
      if (connect) {
        const code = connect.body.code;
        if (
          connect.status < 300 ||
          (code !== undefined && (SAVED_CODES as readonly string[]).includes(code))
        ) {
          backend.configured = true;
        }
        return makeResponse(connect.status, connect.body);
      }
      if (url.endsWith(CODE_URL) && method === "DELETE") {
        backend.configured = false;
        return makeResponse(200, statusBody());
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
const PASSWORD = "SUPER-SECRET-PASSWORD";
const USERNAME = "alice";
// A password distinctive enough that finding it anywhere is unambiguous, used
// by the mutation-cache test below.
const CACHE_PASSWORD = "wJ7q-canary-9f3e1a2b";

// The code form is the secondary path now, so a test that exercises it first
// switches off the default username/password form.
async function openCodeForm() {
  fireEvent.click(await screen.findByRole("button", { name: /only have a playlist code/i }));
}

async function submitCode(code: string) {
  await openCodeForm();
  const input = await screen.findByLabelText("Playlist code");
  fireEvent.change(input, { target: { value: code } });
  fireEvent.click(screen.getByRole("button", { name: /^connect$/i }));
}

// The default path: username and password.
async function submitCredentials(username: string, password: string) {
  const user = await screen.findByLabelText("AdoboTV username");
  fireEvent.change(user, { target: { value: username } });
  fireEvent.change(screen.getByLabelText("AdoboTV password"), { target: { value: password } });
  fireEvent.click(screen.getByRole("button", { name: /^connect$/i }));
}

describe("PlaylistCodeGate — entry", () => {
  it("shows the login form when the status says a credential is needed", async () => {
    installBackend({ configured: false });
    renderGate();

    expect(await screen.findByLabelText("AdoboTV username")).toBeInTheDocument();
    expect(screen.getByLabelText("AdoboTV password")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /connect/i })).toBeInTheDocument();
    // The playlist code is still reachable, as the secondary path.
    expect(
      screen.getByRole("button", { name: /only have a playlist code/i }),
    ).toBeInTheDocument();
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
    // Negative: neither credential field nor a duplicate clear action.
    expect(screen.queryByLabelText("AdoboTV username")).not.toBeInTheDocument();
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

  it("still opens the login form for a required error when no code is configured", async () => {
    installBackend({ configured: false });
    renderGate([new ApiError("needs a code", 403, "playlist_code_required")]);

    expect(await screen.findByLabelText("AdoboTV username")).toBeInTheDocument();
  });

  it("defers to server truth: a configured status renders nothing even for a required error", async () => {
    installBackend({ configured: true });
    const { container } = renderGate([new ApiError("needs a code", 403, "playlist_code_required")]);

    // The required error momentarily shows the form until the status resolves;
    // once it says configured, server truth wins and the gate goes quiet.
    await waitFor(() => expect(screen.queryByLabelText("AdoboTV username")).not.toBeInTheDocument());
    expect(container.firstChild).toBeNull();
  });
});

describe("PlaylistCodeGate — first-run chooser", () => {
  it("renders both connection choices as primary content when no source is active", async () => {
    installBackend({ active: false, needs: false });
    renderGate();

    // Positive: the chooser heading, both derived options, and the line that
    // makes the two equal rather than login-first.
    expect(await screen.findByRole("heading", { name: /choose how to connect/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /login to adobotv/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /import local playlist/i })).toBeInTheDocument();
    expect(screen.getByText(/an adobotv account is optional/i)).toBeInTheDocument();
    // Negative: the chooser is the content, so no code form is forced on the
    // user before they pick the login path.
    expect(screen.queryByLabelText("Playlist code")).not.toBeInTheDocument();
  });

  it("opens the login form from Login to AdoboTV, with the code as a secondary path and a way back", async () => {
    installBackend({ active: false, needs: false });
    renderGate();

    fireEvent.click(await screen.findByRole("button", { name: /login to adobotv/i }));
    // Default: username and password.
    expect(screen.getByLabelText("AdoboTV username")).toBeInTheDocument();
    expect(screen.getByLabelText("AdoboTV password")).toBeInTheDocument();

    // The playlist code is reachable behind the secondary toggle.
    fireEvent.click(screen.getByRole("button", { name: /only have a playlist code/i }));
    expect(screen.getByLabelText("Playlist code")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /back/i }));
    expect(
      await screen.findByRole("heading", { name: /choose how to connect/i }),
    ).toBeInTheDocument();
    expect(screen.queryByLabelText("AdoboTV username")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Playlist code")).not.toBeInTheDocument();
  });

  it("opens the import modal from Import Local Playlist", async () => {
    installBackend({ active: false, needs: false });
    renderGate();

    fireEvent.click(await screen.findByRole("button", { name: /import local playlist/i }));

    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(screen.getByLabelText("Playlist file")).toBeInTheDocument();
  });

  // Paired with the chooser case above: once a source is active the chooser is
  // gone. The login form's presence is the positive guarantee that the status
  // resolved and the active source is really being measured.
  it("renders no chooser once a source is active", async () => {
    installBackend({ active: true, needs: true, configured: false });
    renderGate();

    expect(await screen.findByLabelText("AdoboTV username")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: /choose how to connect/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /login to adobotv/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /import local playlist/i })).not.toBeInTheDocument();
  });

  it("shows a failed status read as an error with Retry, never a chooser", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        throw new TypeError("Failed to fetch");
      }),
    );
    renderGate();

    // Positive: the failure is reported with a retry action.
    expect(await screen.findByRole("heading", { name: "Cannot reach AdoboFlix" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /reload/i })).toBeInTheDocument();
    // Negative: no chooser built on the absent data.
    expect(screen.queryByRole("heading", { name: /choose how to connect/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /login to adobotv/i })).not.toBeInTheDocument();
  });

  it("renders nothing, not a chooser, while the status is still pending", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>(() => {})),
    );
    const { container, queryClient } = renderGate();

    // Guarantee the state being measured has arrived: the status read is
    // genuinely in flight and unresolved, so the emptiness below is "pending",
    // not "resolved to nothing".
    await waitFor(() => expect(queryClient.isFetching()).toBe(1));
    expect(queryClient.getQueryData(SOURCE_STATUS_QUERY_KEY)).toBeUndefined();

    expect(container.firstChild).toBeNull();
    expect(screen.queryByRole("heading", { name: /choose how to connect/i })).not.toBeInTheDocument();
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

    await openCodeForm();
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
            active: true,
            origin: "stored",
            dev: false,
            needs_playlist_code: true,
            playlist_code_configured: false,
            playlist_file_configured: false,
            modes: [
              {
                name: "adobotv-http",
                selectable: true,
                dev: false,
                active: true,
                configured: false,
                needs_playlist_code: true,
              },
              {
                name: "file",
                selectable: true,
                dev: false,
                active: false,
                configured: false,
                needs_playlist_code: false,
              },
            ],
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

describe("PlaylistCodeGate — login by username and password", () => {
  it("posts the credentials to the login endpoint and clears the gate on success", async () => {
    const backend = installBackend();
    renderGate();

    await submitCredentials(USERNAME, PASSWORD);

    await waitFor(() => expect(backend.calls.some((c) => c.method === "POST")).toBe(true));
    const post = backend.calls.find((c) => c.method === "POST");
    expect(post?.url.endsWith(LOGIN_URL)).toBe(true);
    expect(JSON.parse(post?.body ?? "{}")).toEqual({ username: USERNAME, password: PASSWORD });
    // The gate goes quiet once the server has stored the code.
    await waitFor(() => expect(screen.queryByLabelText("AdoboTV username")).not.toBeInTheDocument());
  });

  it("clears the password from the DOM after submitting and never renders it", async () => {
    const backend = installBackend({
      login: {
        status: 401,
        body: { error: "AdoboTV rejected the username or password", code: "invalid_credentials" },
      },
    });
    const { container } = renderGate();

    const password = await screen.findByLabelText("AdoboTV password");
    fireEvent.change(screen.getByLabelText("AdoboTV username"), { target: { value: USERNAME } });
    fireEvent.change(password, { target: { value: PASSWORD } });
    // Positive: the masked field holds the password before submission.
    expect(password).toHaveValue(PASSWORD);

    fireEvent.click(screen.getByRole("button", { name: /connect/i }));
    await screen.findByText("AdoboTV did not accept those credentials");

    // The POST really carried the password, so the absence below is not vacuous.
    const post = backend.calls.find((c) => c.method === "POST");
    expect(post?.body).toContain(PASSWORD);
    // Negative: after submission the password appears nowhere in the output, and
    // the masked field is emptied.
    expect(container.innerHTML).not.toContain(PASSWORD);
    expect(screen.getByLabelText("AdoboTV password")).toHaveValue("");
  });

  it("shows the captcha copy distinctly, never as a rejected password", async () => {
    installBackend({ login: { status: 403, body: { error: "recaptcha", code: "captcha_required" } } });
    renderGate();

    await submitCredentials(USERNAME, PASSWORD);

    expect(
      await screen.findByRole("heading", { name: "AdoboTV requires a captcha to log in" }),
    ).toBeInTheDocument();
    expect(screen.getByText(/not been rejected/i)).toBeInTheDocument();
    // Negative: it is not the credential-refusal wording.
    expect(screen.queryByText("AdoboTV did not accept those credentials")).not.toBeInTheDocument();
    // And the form stays so the user can switch to the code path.
    expect(screen.getByLabelText("AdoboTV username")).toBeInTheDocument();
  });

  it("treats a saved-but-gated login as saved, exactly like a typed code", async () => {
    installBackend({ login: { status: 403, body: { error: "device pending", code: "device_pending" } } });
    const { container } = renderGate();

    await submitCredentials(USERNAME, PASSWORD);

    expect(await screen.findByRole("heading", { name: /playlist code saved/i })).toBeInTheDocument();
    expect(screen.getByText(/once the AdoboTV operator approves this device/i)).toBeInTheDocument();
    expect(container.innerHTML).not.toContain(PASSWORD);
  });

  it("shows the account-without-a-code copy for that failure", async () => {
    installBackend({
      login: { status: 502, body: { error: "no code", code: "account_without_playlist_code" } },
    });
    renderGate();

    await submitCredentials(USERNAME, PASSWORD);

    expect(
      await screen.findByRole("heading", { name: "This AdoboTV account has no playlist code" }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("AdoboTV username")).toBeInTheDocument();
  });

  // The gate clears the password from its own form state on submit, but the
  // controller's old variables form handed the secret to React Query, whose
  // Mutation record keeps its variables in the cache until garbage collection —
  // so the password outlived the request one layer down. This drives a login and
  // proves the password never entered that cache.
  it("keeps the password out of React Query's mutation cache", async () => {
    const backend = installBackend();
    const { queryClient } = renderGate();

    await submitCredentials(USERNAME, CACHE_PASSWORD);
    await waitFor(() => expect(backend.calls.some((c) => c.method === "POST")).toBe(true));
    await waitFor(() => expect(screen.queryByLabelText("AdoboTV username")).not.toBeInTheDocument());

    // Positive: the password really was sent, so the cache absence below measures
    // where the secret is held and not an unused value.
    const post = backend.calls.find((c) => c.method === "POST");
    expect(post?.body).toContain(CACHE_PASSWORD);

    const mutations = queryClient.getMutationCache().getAll();
    // Positive: the record we inspect exists and its variables are populated —
    // the username is findable in them — so the absence check is not vacuous.
    expect(mutations.length).toBeGreaterThanOrEqual(1);
    const serializedVariables = mutations
      .map((mutation) => JSON.stringify(mutation.state.variables))
      .join("\n");
    expect(serializedVariables).toContain(USERNAME);
    // Negative: the password never entered React Query's mutation state, so it
    // cannot survive the request in the cache.
    expect(serializedVariables).not.toContain(CACHE_PASSWORD);
  });

  // Hole 1: a mutate()-level onSettled is dropped when the observer unmounts
  // before the request settles, so a password cleared only there survives the
  // request. This drives a real login, unmounts mid-flight, lets it settle, then
  // probes the controller's retained secret store through the mutation's own
  // mutationFn — the only handle left on that store once the component is gone.
  it("clears the password when the gate unmounts before the login settles", async () => {
    const UNMOUNT_PASSWORD = "wJ7q-unmount-canary-2c4d";
    const loginReply = deferred<Response>();
    const calls: FetchCall[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        const method = (init?.method ?? "GET").toUpperCase();
        const body = typeof init?.body === "string" ? init.body : null;
        calls.push({ url, method, body });
        if (url.endsWith(STATUS_URL)) return makeResponse(200, activeStatusBody());
        if (method === "POST" && url.endsWith(LOGIN_URL)) return loginReply.promise;
        throw new Error(`unexpected fetch: ${method} ${url}`);
      }),
    );

    const queryClient = makeQueryClient();
    const { unmount } = renderGateWith(queryClient);

    const password = await screen.findByLabelText("AdoboTV password");
    fireEvent.change(screen.getByLabelText("AdoboTV username"), { target: { value: USERNAME } });
    fireEvent.change(password, { target: { value: UNMOUNT_PASSWORD } });
    fireEvent.click(screen.getByRole("button", { name: /^connect$/i }));

    const loginBodies = () =>
      calls
        .filter((c) => c.method === "POST" && c.url.endsWith(LOGIN_URL))
        .map((c) => c.body ?? "");

    // Positive: the mutation is genuinely in flight, and the real POST carried
    // the password, so the secret was in the store at submission time.
    await waitFor(() => expect(loginBodies()).toHaveLength(1));
    const loginMutations = queryClient
      .getMutationCache()
      .getAll()
      .filter((mutation) => mutation.state.status === "pending");
    expect(loginMutations).toHaveLength(1);
    const loginMutation = loginMutations[0];
    const variables = loginMutation.state.variables as { id: string; username: string };
    expect(variables.username).toBe(USERNAME);
    expect(loginBodies().at(-1)).toContain(UNMOUNT_PASSWORD);

    // Probe the retained secret store before settle: re-issuing through the
    // mutation's own mutationFn reads the same store, and the fresh send still
    // carries the password — proving the store held it and this probe sees it.
    const sendLoginSecret = loginMutation.options.mutationFn as (v: unknown) => unknown;
    sendLoginSecret(variables);
    expect(loginBodies().at(-1)).toContain(UNMOUNT_PASSWORD);

    // Unmount mid-flight, then let the request settle.
    unmount();
    loginReply.resolve(makeResponse(200, {}));
    await waitFor(() => expect(loginMutation.state.status).not.toBe("pending"));

    // Negative: the retained store no longer yields the password — a fresh send
    // through the same mutationFn now carries an empty secret.
    sendLoginSecret(variables);
    expect(loginBodies().at(-1)).not.toContain(UNMOUNT_PASSWORD);

    // Negative: and it never entered React Query's mutation variables, which
    // outlive the request.
    const serializedVariables = queryClient
      .getMutationCache()
      .getAll()
      .map((mutation) => JSON.stringify(mutation.state.variables))
      .join("\n");
    expect(serializedVariables).toContain(USERNAME);
    expect(serializedVariables).not.toContain(UNMOUNT_PASSWORD);
  });

  // Hole 2: one shared secret slot races across concurrent logins. Both calls
  // park their password in the same place before either mutation function runs,
  // so a request can read the other call's password. This fires two logins
  // before either settles and checks the outgoing pairs directly.
  it("keeps concurrent logins on their own secret, never crossing credentials", async () => {
    const alice = { username: "alice-concurrent", password: "alice-pw-1A" };
    const bob = { username: "bob-concurrent", password: "bob-pw-2B" };
    const pendingLogins: Array<(response: Response) => void> = [];
    const calls: FetchCall[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        const method = (init?.method ?? "GET").toUpperCase();
        const body = typeof init?.body === "string" ? init.body : null;
        calls.push({ url, method, body });
        if (url.endsWith(STATUS_URL)) return makeResponse(200, activeStatusBody());
        if (method === "POST" && url.endsWith(LOGIN_URL)) {
          return new Promise<Response>((resolve) => {
            pendingLogins.push(resolve);
          });
        }
        throw new Error(`unexpected fetch: ${method} ${url}`);
      }),
    );

    const queryClient = makeQueryClient();
    capturedController = null;
    render(
      <QueryClientProvider client={queryClient}>
        <ControllerHarness />
      </QueryClientProvider>,
    );
    await waitFor(() => expect(capturedController).not.toBeNull());
    await waitFor(() => expect(queryClient.getQueryData(SOURCE_STATUS_QUERY_KEY)).toBeDefined());

    // Fire both before either resolves, as two rapid submits would.
    capturedController!.submitLogin(alice.username, alice.password);
    capturedController!.submitLogin(bob.username, bob.password);

    const loginBodies = () =>
      calls
        .filter((c) => c.method === "POST" && c.url.endsWith(LOGIN_URL))
        .map((c) => JSON.parse(c.body ?? "{}") as { username: string; password: string });
    await waitFor(() => expect(loginBodies()).toHaveLength(2));

    const bodies = loginBodies();
    // Positive: both distinct usernames actually went out, so the pairing below
    // is a real match and not an accidental pass on an absent entry.
    expect(bodies.map((b) => b.username).sort()).toEqual(
      [alice.username, bob.username].sort(),
    );
    // Negative: each username carries its own password, never the other call's.
    expect(bodies.find((b) => b.username === alice.username)?.password).toBe(alice.password);
    expect(bodies.find((b) => b.username === bob.username)?.password).toBe(bob.password);

    // Let both settle so no request is left pending.
    pendingLogins.forEach((resolve) => resolve(makeResponse(200, {})));
    await waitFor(() => expect(queryClient.isMutating()).toBe(0));
  });
});
