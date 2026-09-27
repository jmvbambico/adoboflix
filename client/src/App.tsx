/**
 * @license
 * SPDX-License-Identifier: Apache-2.0
 */

import React from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import Dashboard from "./components/Dashboard";
import { shouldRetryQuery } from "./api/client";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      staleTime: 5000 * 60, // Keep cached simulations fresh for 5 mins
      retry: shouldRetryQuery,
    },
  },
});

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <Dashboard />
    </QueryClientProvider>
  );
}
