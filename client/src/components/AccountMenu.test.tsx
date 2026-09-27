import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
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

interface StatusShape {
  source: string;
  needs_playlist_code: boolean;
  playlist_code_configured: boolean;
  subscription_expires_at?: string;
  user_message?: string;
}

const STATUS_URL = "/api/v1/source/status";
const CODE_URL = "/api/v1/source/playlist-code";

interface BackendOptions extends Partial<StatusShape> {
  // The status the DELETE returns. >= 300 simulates a failed disconnect.
  deleteStatus?: number;
}

// installBackend serves the status endpoint and the DELETE that disconnects.
// It records every call so a test can assert the destructive request was not
// made until the user confirmed.
function installBackend(init: BackendOptions = {}) {
  const { deleteStatus = 200, ...statusInit } = init;
  const status: StatusShape = {
    source: "adobotv-http",
    needs_playlist_code: true,
    playlist_code_configured: false,
    ...statusInit,
  };
  const calls: FetchCall[] = [];

  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = (init?.method ?? "GET").toUpperCase();
      calls.push({ url, method });

      if (url.endsWith(STATUS_URL)) return makeResponse(200, status);
      if (url.endsWith(CODE_URL) && method === "DELETE") {
        if (deleteStatus >= 300) {
          return makeResponse(deleteStatus, { error: "could not clear", code: "internal_error" });
        }
        status.playlist_code_configured = false;
        return makeResponse(200, status);
      }
      throw new Error(`unexpected fetch: ${method} ${url}`);
    }),
  );

  return { status, calls };
}

function renderMenu() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 5 * 60 * 1000 } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <AccountMenu />
    </QueryClientProvider>,
  );
}

async function openMenu() {
  const button = screen.getByRole("button", { name: "Account menu" });
  fireEvent.click(button);
  await screen.findByRole("menu");
  // The menu renders before the status query settles; wait for the active
  // source so the assertions below see the resolved state, not the loading one.
  await screen.findByText("adobotv-http");
  return button;
}

describe("AccountMenu", () => {
  it("is a labelled, keyboard-openable menu button", async () => {
    installBackend();
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
    installBackend();
    renderMenu();
    const button = await openMenu();

    fireEvent.keyDown(document, { key: "Escape" });

    await waitFor(() => expect(screen.queryByRole("menu")).not.toBeInTheDocument());
    expect(button).toHaveFocus();
  });

  it("closes on a click outside", async () => {
    installBackend();
    renderMenu();
    await openMenu();

    fireEvent.mouseDown(document.body);

    await waitFor(() => expect(screen.queryByRole("menu")).not.toBeInTheDocument());
  });

  it("shows no playlist code is connected, and offers to connect", async () => {
    installBackend({ playlist_code_configured: false });
    renderMenu();
    await openMenu();

    expect(screen.getByText("adobotv-http")).toBeInTheDocument();
    expect(screen.getByText("No playlist code yet")).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: /connect playlist code/i })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: /disconnect/i })).not.toBeInTheDocument();
  });

  it("shows the playlist code is connected, and offers change and disconnect", async () => {
    installBackend({ playlist_code_configured: true });
    renderMenu();
    await openMenu();

    expect(screen.getByText("Playlist code connected")).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: /change playlist code/i })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: /disconnect/i })).toBeInTheDocument();
  });

  it("shows the subscription expiry and upstream message when reported", async () => {
    installBackend({
      playlist_code_configured: true,
      subscription_expires_at: "2030-01-01T00:00:00Z",
      user_message: "Renew by Friday.",
    });
    renderMenu();
    await openMenu();

    expect(screen.getByText(/subscription renews/i)).toBeInTheDocument();
    expect(screen.getByText(/2030/)).toBeInTheDocument();
    expect(screen.getByText("Renew by Friday.")).toBeInTheDocument();
  });

  it("says plainly when the source needs no playlist code", async () => {
    installBackend({ needs_playlist_code: false, playlist_code_configured: false });
    renderMenu();
    await openMenu();

    expect(screen.getByText(/no account needed/i)).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: /connect playlist code/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: /change playlist code/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: /disconnect/i })).not.toBeInTheDocument();
  });

  it("confirms before disconnecting, and only then calls DELETE", async () => {
    const backend = installBackend({ playlist_code_configured: true });
    renderMenu();
    await openMenu();

    fireEvent.click(screen.getByRole("menuitem", { name: /disconnect/i }));

    // Negative: the destructive request has not been made yet.
    expect(backend.calls.some((c) => c.method === "DELETE")).toBe(false);
    // Positive: the confirmation explains what disconnecting costs.
    expect(screen.getByText(/type it in again in full/i)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("menuitem", { name: /yes, disconnect/i }));

    await waitFor(() => expect(backend.calls.some((c) => c.method === "DELETE")).toBe(true));
  });

  it("lets the user back out of the disconnect confirmation", async () => {
    const backend = installBackend({ playlist_code_configured: true });
    renderMenu();
    await openMenu();

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
    renderMenu();

    fireEvent.click(screen.getByRole("button", { name: "Account menu" }));
    await screen.findByRole("menu");

    // Positive: an honest loading state.
    expect(screen.getByText(/checking the active source/i)).toBeInTheDocument();
    // Negative: the false capability claim is not made while the answer is unknown.
    expect(screen.queryByText(/no account needed/i)).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: /connect playlist code/i })).not.toBeInTheDocument();
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
    renderMenu();

    fireEvent.click(screen.getByRole("button", { name: "Account menu" }));
    await screen.findByRole("menu");

    expect(await screen.findByText(/can't read the source status/i)).toBeInTheDocument();
    expect(screen.queryByText(/no account needed/i)).not.toBeInTheDocument();
  });

  it("says so when a disconnect fails, instead of silently staying connected", async () => {
    const backend = installBackend({ playlist_code_configured: true, deleteStatus: 500 });
    renderMenu();
    await openMenu();

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
    installBackend({ playlist_code_configured: true });
    renderMenu();
    await openMenu();

    fireEvent.click(screen.getByRole("menuitem", { name: /disconnect/i }));
    expect(screen.getByRole("menuitem", { name: /yes, disconnect/i })).toHaveFocus();

    fireEvent.click(screen.getByRole("menuitem", { name: /cancel/i }));
    expect(screen.getByRole("menuitem", { name: /^disconnect$/i })).toHaveFocus();
  });

  it("returns focus to the avatar when the modal closes", async () => {
    installBackend({ playlist_code_configured: false });
    renderMenu();
    const button = await openMenu();

    fireEvent.click(screen.getByRole("menuitem", { name: /connect playlist code/i }));
    fireEvent.click(await screen.findByRole("button", { name: /close/i }));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(button).toHaveFocus();
  });
});
