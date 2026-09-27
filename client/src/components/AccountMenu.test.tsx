import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { SourceMode } from "../api/client";
import AccountMenu from "./AccountMenu";

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
}

// The full status contract the server reports. The menu renders its choices
// from modes, never from the adapter names, so the mocks carry real flags.
interface StatusShape {
  source: string;
  active: boolean;
  origin: "env" | "stored" | "none";
  dev: boolean;
  needs_playlist_code: boolean;
  playlist_code_configured: boolean;
  playlist_file_configured: boolean;
  modes: SourceMode[];
  subscription_expires_at?: string;
  user_message?: string;
}

const STATUS_URL = "/api/v1/source/status";
const CODE_URL = "/api/v1/source/playlist-code";

const CODE_MODE: SourceMode = {
  name: "adobotv-http",
  selectable: true,
  dev: false,
  active: false,
  configured: false,
  needs_playlist_code: true,
};
const FILE_MODE: SourceMode = {
  name: "file",
  selectable: true,
  dev: false,
  active: false,
  configured: false,
  needs_playlist_code: false,
};

// modeModes is the default two selectable modes with the named one active.
function modesFor(activeName: "adobotv-http" | "file" | "", configured: boolean): SourceMode[] {
  return [
    {
      ...CODE_MODE,
      active: activeName === "adobotv-http",
      configured: activeName === "adobotv-http" && configured,
    },
    { ...FILE_MODE, active: activeName === "file", configured: activeName === "file" },
  ];
}

function defaultStatus(activeName: "adobotv-http" | "file" | "", configured: boolean): StatusShape {
  return {
    source: activeName,
    active: activeName !== "",
    origin: activeName === "" ? "none" : "stored",
    dev: false,
    needs_playlist_code: activeName === "adobotv-http",
    playlist_code_configured: activeName === "adobotv-http" && configured,
    playlist_file_configured: activeName === "file",
    modes: modesFor(activeName, configured),
  };
}

interface BackendOptions extends Partial<StatusShape> {
  // The status the DELETE returns. >= 300 simulates a failed disconnect.
  deleteStatus?: number;
  // The reply DELETE /source/playlist-file gives. >= 300 simulates a failed
  // removal.
  removeStatus?: number;
  // What /api/v1/stats reports — the import shape's playlist count.
  stats?: { total_titles: number; total_providers: number; total_genres: number };
}

const FILE_URL = "/api/v1/source/playlist-file";
const STATS_URL = "/api/v1/stats";

// installBackend serves the status endpoint, the stats read the import shape
// makes, and the two DELETEs that disconnect or remove. It records every call
// so a test can assert a destructive request was not made until the user
// confirmed.
function installBackend(init: BackendOptions = {}) {
  const {
    deleteStatus = 200,
    removeStatus = 200,
    stats = { total_titles: 7, total_providers: 2, total_genres: 3 },
    ...statusInit
  } = init;
  const status: StatusShape = { ...defaultStatus("adobotv-http", false), ...statusInit };
  const calls: FetchCall[] = [];

  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = (init?.method ?? "GET").toUpperCase();
      calls.push({ url, method });

      if (url.endsWith(STATUS_URL)) return makeResponse(200, status);
      if (url.endsWith(STATS_URL)) return makeResponse(200, stats);
      if (url.endsWith(CODE_URL) && method === "DELETE") {
        if (deleteStatus >= 300) {
          return makeResponse(deleteStatus, { error: "could not clear", code: "internal_error" });
        }
        status.playlist_code_configured = false;
        return makeResponse(200, status);
      }
      if (url.endsWith(FILE_URL) && method === "DELETE") {
        if (removeStatus >= 300) {
          return makeResponse(removeStatus, { error: "could not clear", code: "internal_error" });
        }
        // The server clears the mode too when the removed playlist was active,
        // returning to the sourceless state the chooser is offered from.
        Object.assign(status, defaultStatus("", false));
        return makeResponse(200, status);
      }
      throw new Error(`unexpected fetch: ${method} ${url}`);
    }),
  );

  return { status, calls };
}

// importStatus is a normal active local-playlist source.
function importStatus(): Partial<StatusShape> {
  return {
    source: "file",
    needs_playlist_code: false,
    playlist_code_configured: false,
    playlist_file_configured: true,
    modes: modesFor("file", false),
  };
}

function renderMenu(init: BackendOptions = {}) {
  const backend = installBackend(init);
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 5 * 60 * 1000 } },
  });
  render(
    <QueryClientProvider client={queryClient}>
      <AccountMenu />
    </QueryClientProvider>,
  );
  return { backend, queryClient };
}

// openMenu opens the menu and waits for the status read to resolve, so every
// assertion below measures the resolved state and not the loading one. The
// expected label is the menu's own positive confirmation that resolution
// happened.
async function openMenu(queryClient: QueryClient, label = "AdoboTV account") {
  const button = screen.getByRole("button", { name: "Account menu" });
  fireEvent.click(button);
  await screen.findByRole("menu");
  await waitFor(() => expect(queryClient.getQueryData(["source-status"])).toBeDefined());
  await screen.findByText(label);
  return button;
}

describe("AccountMenu", () => {
  it("is a labelled, keyboard-openable menu button", async () => {
    renderMenu();

    const button = screen.getByRole("button", { name: "Account menu" });
    expect(button).toHaveAttribute("aria-haspopup", "menu");
    expect(button).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();

    // Keyboard-reachable: ArrowDown on the focused button opens the menu.
    button.focus();
    fireEvent.keyDown(button, { key: "ArrowDown" });

    expect(await screen.findByRole("menu")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Account menu" })).toHaveAttribute(
      "aria-expanded",
      "true",
    );
  });

  it("closes on Escape and returns focus to the avatar", async () => {
    const { queryClient } = renderMenu();
    const button = await openMenu(queryClient);

    fireEvent.keyDown(document, { key: "Escape" });

    await waitFor(() => expect(screen.queryByRole("menu")).not.toBeInTheDocument());
    expect(button).toHaveFocus();
  });

  it("closes on a click outside", async () => {
    const { queryClient } = renderMenu();
    await openMenu(queryClient);

    fireEvent.mouseDown(document.body);

    await waitFor(() => expect(screen.queryByRole("menu")).not.toBeInTheDocument());
  });

  it("names the active source in our words, never the adapter name", async () => {
    const { queryClient } = renderMenu({ playlist_code_configured: true });
    await openMenu(queryClient);

    // Positive: the user-facing name for the AdoboTV path.
    expect(screen.getByText("AdoboTV account")).toBeInTheDocument();
    // Negative: the raw adapter name is not presented as the user's choice.
    expect(screen.queryByText("adobotv-http")).not.toBeInTheDocument();
  });

  it("shows no playlist code is connected, and offers to log in and import", async () => {
    const { queryClient } = renderMenu({ playlist_code_configured: false });
    await openMenu(queryClient);

    expect(screen.getByText("No playlist code yet")).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: /login to adobotv/i })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: /import local playlist/i })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: /disconnect/i })).not.toBeInTheDocument();
  });

  it("shows the playlist code is connected, and offers change and disconnect", async () => {
    const { queryClient } = renderMenu({ playlist_code_configured: true });
    await openMenu(queryClient);

    expect(screen.getByText("Playlist code connected")).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: /change playlist code/i })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: /disconnect/i })).toBeInTheDocument();
  });

  it("shows the subscription expiry and upstream message when reported", async () => {
    const { queryClient } = renderMenu({
      playlist_code_configured: true,
      subscription_expires_at: "2030-01-01T00:00:00Z",
      user_message: "Renew by Friday.",
    });
    await openMenu(queryClient);

    expect(screen.getByText(/subscription renews/i)).toBeInTheDocument();
    expect(screen.getByText(/2030/)).toBeInTheDocument();
    expect(screen.getByText("Renew by Friday.")).toBeInTheDocument();
  });

  // Paired with the case above: when the server reports no expiry (the live
  // account whose billed_till is the "0" sentinel), the menu says nothing about
  // a subscription — it must not invent a date, least of all 1 Jan 1970.
  it("renders no subscription line when the server reports no expiry", async () => {
    const { queryClient } = renderMenu({
      playlist_code_configured: true,
      user_message: "Welcome to AdoboTV cryogenix!",
    });
    await openMenu(queryClient);

    expect(screen.queryByText(/subscription renews/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/1970/)).not.toBeInTheDocument();
    // The real message beside it still renders.
    expect(screen.getByText("Welcome to AdoboTV cryogenix!")).toBeInTheDocument();
  });

  // The import user is a peer of the subscriber, not a second-class citizen:
  // they see a positive statement of their setup and the size of their own
  // playlist, and they get a Remove action. Nothing is framed as an absence.
  it("shows the import user their playlist and how much is in it", async () => {
    const { queryClient } = renderMenu({ ...importStatus(), stats: { total_titles: 7, total_providers: 2, total_genres: 3 } });
    await openMenu(queryClient, "Local playlist");

    // Positive: our word for the local-playlist path, a positive framing, and
    // the count from the server's own stats read.
    expect(screen.getByText("Local playlist")).toBeInTheDocument();
    expect(screen.getByText(/playing your local playlist/i)).toBeInTheDocument();
    expect(screen.getByText(/an adobotv account is optional/i)).toBeInTheDocument();
    expect(await screen.findByText("7 titles loaded")).toBeInTheDocument();
    // Negative: the raw adapter name is not printed as the user's choice, and
    // the old absence framing is gone.
    expect(screen.queryByText("file")).not.toBeInTheDocument();
    expect(screen.queryByText(/no account needed/i)).not.toBeInTheDocument();
    // The import user gets a Remove action; Disconnect belongs to the code path.
    expect(screen.getByRole("menuitem", { name: /remove playlist/i })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: /disconnect/i })).not.toBeInTheDocument();
    // The code path is still offered as a login — a first-class switch, not a
    // fallback the user has to discover.
    expect(screen.getByRole("menuitem", { name: /login to adobotv/i })).toBeInTheDocument();
  });

  // The two shapes are different sections, not one panel with empty slots: an
  // import user is not shown AdoboTV-only facts, and a subscriber is not shown
  // a playlist count.
  it("does not show playlist facts to an AdoboTV subscriber", async () => {
    const { queryClient, backend } = renderMenu({
      playlist_code_configured: true,
      subscription_expires_at: "2030-01-01T00:00:00Z",
      user_message: "Renew by Friday.",
      stats: { total_titles: 7, total_providers: 2, total_genres: 3 },
    });
    await openMenu(queryClient);

    // Positive: the subscriber's own facts render.
    expect(screen.getByText("Playlist code connected")).toBeInTheDocument();
    expect(screen.getByText(/2030/)).toBeInTheDocument();
    expect(screen.getByText("Renew by Friday.")).toBeInTheDocument();
    // Negative: no playlist framing, and the stats endpoint was never read for
    // this shape, so the absence is not merely a slow query.
    expect(screen.queryByText(/playing your local playlist/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/titles loaded/i)).not.toBeInTheDocument();
    expect(backend.calls.some((c) => c.url.endsWith("/api/v1/stats"))).toBe(false);
  });

  // A development harness is labelled plainly as one and never by its adapter
  // name; the owner's instruction was not to document it, so no copy explains
  // it either.
  it("labels a development source and never prints its adapter name", async () => {
    const { queryClient, backend } = renderMenu({
      source: "postgres-direct",
      active: true,
      origin: "env",
      dev: true,
      needs_playlist_code: false,
      playlist_code_configured: false,
      modes: [
        CODE_MODE,
        FILE_MODE,
        {
          name: "postgres-direct",
          selectable: false,
          dev: true,
          active: true,
          configured: false,
          needs_playlist_code: false,
        },
      ],
    });
    await openMenu(queryClient, "Development source");

    // Positive: the dev source is presented, in plain words.
    expect(screen.getByText("Development source")).toBeInTheDocument();
    // Negative: neither its adapter name nor the code-adapter name leaks as a
    // user-facing choice.
    expect(screen.queryByText("postgres-direct")).not.toBeInTheDocument();
    expect(screen.queryByText("adobotv-http")).not.toBeInTheDocument();
    // The raw name really is in the payload being rendered, so the absence
    // above is not vacuous.
    expect(backend.status.source).toBe("postgres-direct");
  });

  it("disables mode-changing actions and explains an env-pinned source", async () => {
    const { queryClient } = renderMenu({
      origin: "env",
      active: true,
      playlist_code_configured: true,
    });
    await openMenu(queryClient);

    // Positive: the pin is explained and Disconnect — not mode-changing — is
    // still offered.
    expect(screen.getByText(/pinned to a content source by its server configuration/i)).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: /disconnect/i })).toBeInTheDocument();
    // Negative: the actions that would fail with 409 are disabled, not offered.
    expect(screen.getByRole("menuitem", { name: /change playlist code/i })).toBeDisabled();
    expect(screen.getByRole("menuitem", { name: /import local playlist/i })).toBeDisabled();
  });

  it("opening the import action shows the file picker", async () => {
    const { queryClient } = renderMenu({ needs_playlist_code: false, source: "file", modes: modesFor("file", false) });
    await openMenu(queryClient, "Local playlist");

    fireEvent.click(screen.getByRole("menuitem", { name: /import local playlist/i }));

    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(screen.getByLabelText("Playlist file")).toBeInTheDocument();
  });

  it("confirms before removing a playlist, and only then calls DELETE", async () => {
    const { backend, queryClient } = renderMenu(importStatus());
    await openMenu(queryClient, "Local playlist");

    fireEvent.click(screen.getByRole("menuitem", { name: /remove playlist/i }));

    // Negative: the destructive request has not been made yet.
    expect(backend.calls.some((c) => c.method === "DELETE")).toBe(false);
    // Positive: the confirmation says what removing costs.
    expect(screen.getByText(/returns you to the start screen/i)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("menuitem", { name: /yes, remove/i }));

    await waitFor(() => expect(backend.calls.some((c) => c.method === "DELETE")).toBe(true));
    const del = backend.calls.find((c) => c.method === "DELETE");
    expect(del?.url.endsWith(FILE_URL)).toBe(true);
  });

  it("lets the user back out of the remove confirmation", async () => {
    const { backend, queryClient } = renderMenu(importStatus());
    await openMenu(queryClient, "Local playlist");

    fireEvent.click(screen.getByRole("menuitem", { name: /remove playlist/i }));
    fireEvent.click(screen.getByRole("menuitem", { name: /cancel/i }));

    expect(screen.queryByText(/returns you to the start screen/i)).not.toBeInTheDocument();
    expect(backend.calls.some((c) => c.method === "DELETE")).toBe(false);
  });

  // Removal returns the server to the sourceless state; the menu must reflect
  // that rather than continuing to describe a playlist that is gone.
  it("shows no source after the playlist is removed", async () => {
    const { queryClient } = renderMenu(importStatus());
    await openMenu(queryClient, "Local playlist");

    fireEvent.click(screen.getByRole("menuitem", { name: /remove playlist/i }));
    fireEvent.click(screen.getByRole("menuitem", { name: /yes, remove/i }));

    // Positive: the menu now describes the sourceless state.
    expect(await screen.findByText("Not connected")).toBeInTheDocument();
    // Negative: the playlist framing is gone with it.
    expect(screen.queryByText(/playing your local playlist/i)).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: /remove playlist/i })).not.toBeInTheDocument();
  });

  it("says so when removing a playlist fails, instead of silently removing it", async () => {
    const { queryClient } = renderMenu({ ...importStatus(), removeStatus: 500 });
    await openMenu(queryClient, "Local playlist");

    fireEvent.click(screen.getByRole("menuitem", { name: /remove playlist/i }));
    fireEvent.click(screen.getByRole("menuitem", { name: /yes, remove/i }));

    // Positive: the failure is reported.
    expect(await screen.findByRole("alert")).toHaveTextContent(/could not complete this request/i);
    // Negative: the source is still described as the playlist, not removed.
    expect(screen.getByText("Local playlist")).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: /remove playlist/i })).toBeInTheDocument();
  });

  it("disables Remove when the source is pinned", async () => {
    const { queryClient } = renderMenu({ ...importStatus(), origin: "env" });
    await openMenu(queryClient, "Local playlist");

    expect(screen.getByText(/pinned to a content source by its server configuration/i)).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: /remove playlist/i })).toBeDisabled();
  });

  it("confirms before disconnecting, and only then calls DELETE", async () => {
    const { backend, queryClient } = renderMenu({ playlist_code_configured: true });
    await openMenu(queryClient);

    fireEvent.click(screen.getByRole("menuitem", { name: /disconnect/i }));

    // Negative: the destructive request has not been made yet.
    expect(backend.calls.some((c) => c.method === "DELETE")).toBe(false);
    // Positive: the confirmation explains what disconnecting costs.
    expect(screen.getByText(/type it in again in full/i)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("menuitem", { name: /yes, disconnect/i }));

    await waitFor(() => expect(backend.calls.some((c) => c.method === "DELETE")).toBe(true));
  });

  it("lets the user back out of the disconnect confirmation", async () => {
    const { backend, queryClient } = renderMenu({ playlist_code_configured: true });
    await openMenu(queryClient);

    fireEvent.click(screen.getByRole("menuitem", { name: /disconnect/i }));
    fireEvent.click(screen.getByRole("menuitem", { name: /cancel/i }));

    expect(screen.queryByText(/type it in again in full/i)).not.toBeInTheDocument();
    expect(backend.calls.some((c) => c.method === "DELETE")).toBe(false);
  });

  // While the status query is still in flight we do not know whether the source
  // takes a code. Claiming "no account needed" here is a falsehood about the
  // source's capability, so the menu must say it is still checking.
  it("does not claim the source needs no account while status is loading", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise<Response>(() => {})),
    );
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: 5 * 60 * 1000 } },
    });
    render(
      <QueryClientProvider client={queryClient}>
        <AccountMenu />
      </QueryClientProvider>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Account menu" }));
    await screen.findByRole("menu");
    // Guarantee the read really is pending before measuring the absences.
    await waitFor(() => expect(queryClient.isFetching()).toBe(1));

    // Positive: an honest loading state.
    expect(screen.getByText(/checking the active source/i)).toBeInTheDocument();
    // Negative: the false capability claim is not made while the answer is unknown.
    expect(screen.queryByText(/no account needed/i)).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: /login to adobotv/i })).not.toBeInTheDocument();
  });

  // A failed status read is also not "no account needed": the menu must say it
  // could not read the status rather than inventing a capability.
  it("does not claim the source needs no account when status cannot be read", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        throw new TypeError("Failed to fetch");
      }),
    );
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: 5 * 60 * 1000 } },
    });
    render(
      <QueryClientProvider client={queryClient}>
        <AccountMenu />
      </QueryClientProvider>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Account menu" }));
    await screen.findByRole("menu");

    expect(await screen.findByText(/can't read the source status/i)).toBeInTheDocument();
    expect(screen.queryByText(/no account needed/i)).not.toBeInTheDocument();
  });

  it("says so when a disconnect fails, instead of silently staying connected", async () => {
    const { backend, queryClient } = renderMenu({
      playlist_code_configured: true,
      deleteStatus: 500,
    });
    await openMenu(queryClient);

    fireEvent.click(screen.getByRole("menuitem", { name: /disconnect/i }));
    fireEvent.click(screen.getByRole("menuitem", { name: /yes, disconnect/i }));

    // Positive: the failure is reported.
    expect(await screen.findByRole("alert")).toHaveTextContent(/could not complete this request/i);
    // Negative: it did not falsely report a disconnect.
    expect(screen.getByText("Playlist code connected")).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: /disconnect/i })).toBeInTheDocument();
    expect(backend.calls.some((c) => c.method === "DELETE")).toBe(true);
  });

  it("moves focus into the disconnect confirmation and back on cancel", async () => {
    const { queryClient } = renderMenu({ playlist_code_configured: true });
    await openMenu(queryClient);

    fireEvent.click(screen.getByRole("menuitem", { name: /disconnect/i }));
    expect(screen.getByRole("menuitem", { name: /yes, disconnect/i })).toHaveFocus();

    fireEvent.click(screen.getByRole("menuitem", { name: /cancel/i }));
    expect(screen.getByRole("menuitem", { name: /^disconnect$/i })).toHaveFocus();
  });

  it("returns focus to the avatar when the modal closes", async () => {
    const { queryClient } = renderMenu({ playlist_code_configured: false });
    const button = await openMenu(queryClient);

    fireEvent.click(screen.getByRole("menuitem", { name: /login to adobotv/i }));
    fireEvent.click(await screen.findByRole("button", { name: /close/i }));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(button).toHaveFocus();
  });
});
