import { render } from "@testing-library/react";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { ApiError, shouldRetryQuery } from "./api/client";
import { AppProviders } from "./main";
import { queryClient } from "./queryClient";

// Guards the single app-wide QueryClient against a silent regression to a
// weaker config (e.g. `retry: 1`, `staleTime: 30_000`) — the exact drift that
// hid behind the second, dead client before it was collapsed into one.
describe("app queryClient", () => {
  const queries = queryClient.getDefaultOptions().queries;

  it("pins the app's retry policy by identity, not a blanket retry count", () => {
    expect(queries?.retry).toBe(shouldRetryQuery);
  });

  it("keeps cached data fresh for five minutes", () => {
    expect(queries?.staleTime).toBe(5 * 60 * 1000);
  });

  it("does not refetch on window focus", () => {
    expect(queries?.refetchOnWindowFocus).toBe(false);
  });

  it("refuses a settled 4xx and retries a transport error under the cap", () => {
    const retry = queries?.retry;
    if (typeof retry !== "function") {
      throw new Error("retry must be a function");
    }
    expect(retry(0, new ApiError("device pending", 403))).toBe(false);
    expect(retry(0, new ApiError("rejected", 400))).toBe(false);
    expect(retry(0, new TypeError("Failed to fetch"))).toBe(true);
    expect(retry(2, new TypeError("Failed to fetch"))).toBe(false);
  });
});

// The config above only holds if the running tree actually receives this client.
// Render the root provider tree main.tsx mounts and read the client the tree is
// given; a regression that wires up a locally-built client fails here.
describe("app provider tree", () => {
  it("hands the tree the one shared client, never a locally-built one", () => {
    let received: QueryClient | undefined;

    function CaptureClient() {
      received = useQueryClient();
      return null;
    }

    render(
      <AppProviders>
        <CaptureClient />
      </AppProviders>,
    );

    expect(received).toBe(queryClient);
  });
});
