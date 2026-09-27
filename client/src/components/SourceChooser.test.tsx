import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { SourceMode, SourceStatus } from "../api/client";
import SourceChooser from "./SourceChooser";

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
const DEV_MODE: SourceMode = {
  name: "postgres-direct",
  selectable: false,
  dev: true,
  active: false,
  configured: false,
  needs_playlist_code: false,
};

function status(modes: SourceMode[], overrides: Partial<SourceStatus> = {}): SourceStatus {
  return {
    source: "",
    active: false,
    origin: "none",
    dev: false,
    needs_playlist_code: false,
    playlist_code_configured: false,
    playlist_file_configured: false,
    modes,
    ...overrides,
  };
}

function renderChooser(modes: SourceMode[], overrides: Partial<SourceStatus> = {}) {
  const onLogin = vi.fn();
  const onImport = vi.fn();
  render(<SourceChooser status={status(modes, overrides)} onLogin={onLogin} onImport={onImport} />);
  return { onLogin, onImport };
}

describe("SourceChooser", () => {
  // The owner's requirement, pinned where a new user meets it: the dev harness
  // must never appear as a first-run choice even though the server reports it.
  it("offers only the selectable modes and never the dev harness", () => {
    const { onLogin, onImport } = renderChooser([CODE_MODE, FILE_MODE, DEV_MODE]);

    // Positive: exactly the two selectable choices. A non-selectable mode with
    // needs_playlist_code:false would, unfiltered, add a third "Import Local
    // Playlist" button, so the count is the guard.
    expect(screen.getByRole("button", { name: /login to adobotv/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /import local playlist/i })).toBeInTheDocument();
    expect(screen.getAllByRole("button")).toHaveLength(2);
    // Negative: the dev adapter is not offered, by name or as a choice.
    expect(screen.queryByText("postgres-direct")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /login to adobotv/i }));
    expect(onLogin).toHaveBeenCalledTimes(1);
    expect(onImport).not.toHaveBeenCalled();
  });

  it("routes the import choice to the import handler", () => {
    const { onLogin, onImport } = renderChooser([CODE_MODE, FILE_MODE]);

    fireEvent.click(screen.getByRole("button", { name: /import local playlist/i }));

    expect(onImport).toHaveBeenCalledTimes(1);
    expect(onLogin).not.toHaveBeenCalled();
  });

  // The filter is what decides: a merely non-selectable mode is dropped, so the
  // remaining selectable one is offered alone.
  it("offers only the code path when the file mode is not selectable", () => {
    renderChooser([CODE_MODE, { ...FILE_MODE, selectable: false }]);

    expect(screen.getByRole("button", { name: /login to adobotv/i })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /import local playlist/i })).not.toBeInTheDocument();
  });
});
