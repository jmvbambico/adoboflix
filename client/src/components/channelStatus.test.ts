import { describe, expect, it } from "vitest";
import { describeChannelStatus } from "./channelStatus";

describe("describeChannelStatus", () => {
  it.each(["online", "active"])("labels %s as live", (status) => {
    expect(describeChannelStatus(status)).toBe("Now Playing · HLS Ready");
  });

  it.each(["offline", "inactive", "stopped", "unknown", "", undefined, null])(
    "labels %s as standby",
    (status) => {
      expect(describeChannelStatus(status)).toBe("Standby · Inactive");
    },
  );

  it("is case-sensitive, so a mismatched literal falls back to standby", () => {
    expect(describeChannelStatus("ONLINE")).toBe("Standby · Inactive");
  });
});
