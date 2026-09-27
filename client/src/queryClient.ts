import { QueryClient } from "@tanstack/react-query";
import { shouldRetryQuery } from "./api/client";

// The single QueryClient the whole app shares. Its config is the app's
// considered policy: refuse to retry a settled 4xx (a 403 device_pending or
// playlist_rejected needs a human, not a retry) and cap 5xx/transport retries,
// keep cached data fresh for five minutes, and never refetch on window focus.
// queryClient.test.ts pins these so a stray `retry: 1` cannot quietly return.
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      staleTime: 5000 * 60, // Keep cached simulations fresh for 5 mins
      retry: shouldRetryQuery,
    },
  },
});
