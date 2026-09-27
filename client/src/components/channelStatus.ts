// Channel status is not a single literal. In the real library channels carry
// "online" (631 of 650) and "active" (19); streams are a different table with
// their own vocabulary and are deliberately not unified with this. Treat both
// channel literals as live so a playing channel is never labelled standby.
const LIVE_CHANNEL_STATUSES = new Set(["online", "active"]);

const LIVE_LABEL = "Now Playing · HLS Ready";
const STANDBY_LABEL = "Standby · Inactive";

export function describeChannelStatus(status?: string | null): string {
  return status && LIVE_CHANNEL_STATUSES.has(status) ? LIVE_LABEL : STANDBY_LABEL;
}
