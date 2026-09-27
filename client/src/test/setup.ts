import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach, vi } from "vitest";

// The test globals are imported explicitly rather than enabled globally, so
// Testing Library's automatic cleanup never registers itself. Tear the DOM
// down and drop any stubbed globals between cases here, so no test file can
// leak state by forgetting its own teardown.
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
