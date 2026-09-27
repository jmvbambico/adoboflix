import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import Header from "./Header";

function makeResponse(status: number, body: unknown): Response {
  const text = body === undefined ? "" : JSON.stringify(body);
  return {
    ok: status >= 200 && status < 300,
    status,
    text: async () => text,
    json: async () => JSON.parse(text) as unknown,
  } as Response;
}

// The account menu fetches the source status on mount, so Header needs a
// backend that answers /source/status.
function installBackend() {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      if (String(input).endsWith("/api/v1/source/status")) {
        return makeResponse(200, {
          source: "adobotv-http",
          active: true,
          origin: "stored",
          dev: false,
          needs_playlist_code: true,
          playlist_code_configured: true,
          playlist_file_configured: false,
          modes: [
            {
              name: "adobotv-http",
              selectable: true,
              dev: false,
              active: true,
              configured: true,
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
      throw new Error(`unexpected fetch: ${String(input)}`);
    }),
  );
}

function renderHeader() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 5 * 60 * 1000 } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <Header
        searchQuery=""
        setSearchQuery={vi.fn()}
        watchlistCount={0}
        historyCount={0}
        onNavigateToWatchlist={vi.fn()}
        onNavigateToLibrary={vi.fn()}
      />
    </QueryClientProvider>,
  );
}

describe("Header account control", () => {
  it("replaces the hardcoded identity placeholder with the real account menu", async () => {
    installBackend();
    const { container } = renderHeader();

    // Positive first: the header has rendered a real, labelled account control.
    const button = screen.getByRole("button", { name: "Account menu" });
    expect(button).toBeInTheDocument();
    expect(button).toHaveAttribute("aria-haspopup", "menu");

    // Negative: with that render confirmed, the invented identity is gone from it.
    expect(screen.queryByText("Aether Voyager")).not.toBeInTheDocument();
    expect(screen.queryByText("Diamond Elite Premium")).not.toBeInTheDocument();
    expect(container.innerHTML).not.toContain("Aether Voyager");
    expect(container.innerHTML).not.toContain("Diamond Elite Premium");

    // And it surfaces real state, not a fabricated tier: the active source, in
    // our words rather than the adapter's.
    fireEvent.click(button);
    expect(await screen.findByText("AdoboTV account")).toBeInTheDocument();
    expect(screen.getByText("Playlist code connected")).toBeInTheDocument();
    expect(screen.queryByText("adobotv-http")).not.toBeInTheDocument();
  });
});
