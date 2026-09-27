import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// The test globals are imported explicitly rather than enabled globally, so
// Testing Library's automatic cleanup never registers itself. Tear the DOM
// down between cases here instead.
afterEach(() => {
  cleanup();
});
