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

// installBackend serves the status endpoint and the DELETE that disconnects.
// It records every call so a test can assert the destructive request was not
// made until the user confirmed.
function installBackend(init: Partial<StatusShape> = {}) {
  const status: StatusShape = {
    source: "adobotv-http",
    needs_playlist_code: true,
    playlist_code_configured: false,
    ...init,
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
});
