import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/client";
import SourceStatusPanel from "./SourceStatusPanel";

describe("SourceStatusPanel", () => {
  it("renders the mapped copy a user needs to act on", () => {
    render(<SourceStatusPanel error={new ApiError("queued", 403, "device_pending")} />);

    expect(
      screen.getByRole("heading", { name: "This device is awaiting approval" }),
    ).toBeInTheDocument();
    expect(screen.getByText(/AdoboTV has queued this device/)).toBeInTheDocument();
    expect(screen.getByText(/Ask the AdoboTV operator to approve this device/)).toBeInTheDocument();
    expect(screen.getByText("device_pending")).toBeInTheDocument();
  });

  it("offers a retry button labelled from the copy and calls back when clicked", () => {
    const onRetry = vi.fn();
    render(
      <SourceStatusPanel error={new ApiError("queued", 403, "device_pending")} onRetry={onRetry} />,
    );

    const button = screen.getByRole("button", { name: /Reload after approval/ });
    fireEvent.click(button);
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it("hides the retry action when no handler is given", () => {
    render(<SourceStatusPanel error={new ApiError("queued", 403, "device_pending")} />);
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("shows the unreachable copy, with no code chip, for a non-ApiError", () => {
    const { container } = render(<SourceStatusPanel error={new Error("ECONNREFUSED")} />);

    expect(screen.getByRole("heading", { name: "Cannot reach AdoboFlix" })).toBeInTheDocument();
    expect(screen.getByText(/could not reach its own backend/)).toBeInTheDocument();
    // The code chip is the only <span> the panel ever renders, and it renders
    // only when the copy carries a code. Query it structurally: the chip has
    // no role or test id. Asserting on the literal "device_pending" from the
    // previous case would pass even if this error rendered its own chip.
    expect(container.querySelector("span")).toBeNull();
  });

  it("never renders the raw error message", () => {
    const secret = "RAW SECRET BODY";
    render(<SourceStatusPanel error={new ApiError(secret, 500, "upstream_error")} />);

    expect(screen.getByRole("heading", { name: "AdoboTV is unavailable" })).toBeInTheDocument();
    expect(screen.queryByText(new RegExp(secret))).not.toBeInTheDocument();
  });
});
