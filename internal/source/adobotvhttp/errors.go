package adobotvhttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// The sentinel errors below are the adapter's public failure vocabulary. Each
// one is a distinct, actionable state a subscriber can actually hit, and each
// wraps %w so callers can test with errors.Is. They exist because several of
// AdoboTV's gates return shapes that look like success or like each other:
// a pending device returns HTTP 200 + M3U, an inactive subscriber returns a
// full playlist and then refuses every playable URL. Collapsing either into
// "malformed playlist" or "playback failed" would hide the real cause.
var (
	// ErrDevicePending is returned when /v1/playlist/:code answers 2xx with an
	// M3U splash body instead of the JSON envelope. The device has been queued
	// for operator approval. Every first connect hits this.
	ErrDevicePending = errors.New("adobotv-http: device pending approval")

	// ErrSubscriptionInactive is returned when the playlist itself is served
	// but playable content is refused because the account status is not
	// "active". The two checks disagree on purpose upstream, so this must never
	// be reported as a playback failure.
	ErrSubscriptionInactive = errors.New("adobotv-http: subscription inactive")

	// ErrPlaylistRejected is returned when /v1/playlist/:code refuses the code
	// outright (HTTP 403/401): an unknown code, or an account status/expiry
	// AdoboTV will not serve at all.
	ErrPlaylistRejected = errors.New("adobotv-http: playlist code rejected")

	// ErrNoPlaylistCode is returned when the adapter has no playlist code at
	// all: ADOBOFLIX_ADOBOTV_PLAYLIST_CODE is unset and the user has not entered
	// one through the API. The server boots in this state on purpose so a new
	// subscriber can supply the code while it runs; every read that needs the
	// playlist reports this rather than failing obscurely.
	ErrNoPlaylistCode = errors.New("adobotv-http: no playlist code configured")

	// ErrUserAgentRejected is returned when AdoboTV's User-Agent allowlist
	// refuses the request. The configured UA must begin with one of the
	// server's valid_user_agents prefixes.
	ErrUserAgentRejected = errors.New("adobotv-http: player user-agent rejected")

	// ErrPlaylistFormatM3U is returned when /v1/drm/key/ answers with KODIPROP
	// M3U text instead of the expected base64 JSON, because the account's
	// active playlist has output_format=m3u. It is detected rather than
	// mis-parsed so the user gets a real explanation.
	ErrPlaylistFormatM3U = errors.New("adobotv-http: active playlist output_format is m3u")

	// ErrMalformedEnvelope is returned when a 2xx JSON response is not a
	// usable playlist envelope.
	ErrMalformedEnvelope = errors.New("adobotv-http: malformed playlist envelope")

	// ErrMalformedDRM is returned when the /v1/drm/key/ body is not the
	// documented base64(JSON) shape.
	ErrMalformedDRM = errors.New("adobotv-http: malformed DRM details")

	// ErrMalformedVODLibrary is returned when the vod_library URL does not
	// answer with the flat JSON array of assets.
	ErrMalformedVODLibrary = errors.New("adobotv-http: malformed VOD library")

	// ErrContentNotFound is returned when an opaque id does not resolve, or a
	// requested item carries no playable runtime_attr_url.
	ErrContentNotFound = errors.New("adobotv-http: content not found in this playlist")

	// ErrTokenRejected is returned when AdoboTV refuses a content token that the
	// cached playlist envelope minted — a tokenized URL such as runtime_attr_url,
	// the EPG URL or the VOD library URL. The token is minted per playlist fetch
	// and can lapse; the adapter drops the cached envelope so the next call mints
	// a fresh one, and this surfaces as its own actionable state rather than the
	// generic upstream failure a dead token would otherwise look like.
	ErrTokenRejected = errors.New("adobotv-http: content token rejected")

	// ErrUpstream is the transport/status catch-all. Concrete gate failures
	// above are preferred wherever the cause is known.
	ErrUpstream = errors.New("adobotv-http: AdoboTV request failed")

	// ErrInvalidCredentials is returned when AdoboTV's login endpoint (POST
	// /v1/auth/login) refuses a username/password pair with HTTP 401. AdoboTV
	// returns the same 401 for an unknown username and a wrong password,
	// deliberately, so this error does not distinguish them — and neither must
	// a caller's response, or the endpoint becomes an account-enumeration
	// oracle.
	ErrInvalidCredentials = errors.New("adobotv-http: AdoboTV rejected the username or password")

	// ErrRecaptchaRequired is returned when AdoboTV's login endpoint demands a
	// reCAPTCHA token the caller cannot supply (HTTP 403, "reCAPTCHA token is
	// required"). It is its own sentinel because it is not a credential
	// failure: the password may be perfectly correct, and reporting it as a bad
	// password would send the user to change a working one.
	ErrRecaptchaRequired = errors.New("adobotv-http: AdoboTV requires a reCAPTCHA token for login")

	// ErrProfileWithoutPlaylistCode is returned when login succeeds but the
	// authenticated profile carries no playlist code, so there is no credential
	// to store. It is deliberately distinct from ErrNoPlaylistCode, which means
	// the adapter has no code configured at all: here AdoboTV authenticated the
	// account and simply has none to hand over.
	ErrProfileWithoutPlaylistCode = errors.New("adobotv-http: the AdoboTV profile carries no playlist code")
)

// classifyGate maps an upstream refusal message onto the adapter's gate
// sentinel, or nil when the message names no known gate. AdoboTV offers no
// machine-readable code, so this is substring matching — but the checks are
// ordered most-specific first: a message that names a device wins over the
// subscription wording ("Device not active" is a device problem, not a lapsed
// subscription), and a token rejection wins over the generic "expired" that a
// token message often carries. The subscription branch admits the full
// vocabulary AdoboTV uses interchangeably: expired, inactive, not active.
//
// Callers keep their own default when this returns nil: a rejected code at the
// playlist endpoint, a generic upstream error everywhere else.
func classifyGate(detail string) error {
	lower := strings.ToLower(strings.TrimSpace(detail))
	switch {
	case strings.Contains(lower, "device"), strings.Contains(lower, "not approved"):
		return devicePendingDetail(detail)
	case strings.Contains(lower, "token"):
		return tokenRejectedError(detail)
	case strings.Contains(lower, "player"), strings.Contains(lower, "user-agent"), strings.Contains(lower, "user agent"):
		return fmt.Errorf("%w: %s", ErrUserAgentRejected, detail)
	case strings.Contains(lower, "expired"), strings.Contains(lower, "inactive"), strings.Contains(lower, "not active"):
		return subscriptionInactiveError(detail)
	default:
		return nil
	}
}

// tokenRejectedError explains a content-token rejection and what the adapter
// already did about it: it is transient by construction, because the next call
// discards the dead token with the cached envelope and mints a fresh one.
func tokenRejectedError(detail string) error {
	msg := "AdoboTV rejected the content token minted with this playlist. " +
		"AdoboFlix has dropped the cached playlist and will fetch a fresh token on the next request."
	if d := strings.TrimSpace(detail); d != "" {
		msg += " (upstream said: " + d + ")"
	}
	return fmt.Errorf("%w: %s", ErrTokenRejected, msg)
}

// devicePendingDetail is the message an approved-required response carries. It
// names the operator's queue as the next step, because "reconnect" will never
// fix it.
func devicePendingDetail(detail string) error {
	msg := "AdoboTV returned a splash playlist instead of the JSON envelope: " +
		"this device is in the operator's device-approval queue. " +
		"Ask the AdoboTV operator to approve this device, then reload."
	if d := strings.TrimSpace(detail); d != "" {
		msg += " (upstream said: " + d + ")"
	}
	return fmt.Errorf("%w: %s", ErrDevicePending, msg)
}

// subscriptionInactiveError builds the inactive/expired error, keeping any
// upstream detail so the user can tell expiry from an operator action.
func subscriptionInactiveError(detail string) error {
	msg := "AdoboTV served the playlist but refused playable content: " +
		"the account status is not \"active\". Renew or reactivate the subscription with the AdoboTV operator."
	if d := strings.TrimSpace(detail); d != "" {
		msg += " (upstream said: " + d + ")"
	}
	return fmt.Errorf("%w: %s", ErrSubscriptionInactive, msg)
}

// playlistRejectedError names both plausible causes, because AdoboTV's own
// message conflates them: "Invalid playlist code or account status".
func playlistRejectedError(status int, detail string) error {
	msg := fmt.Sprintf("AdoboTV refused the playlist (HTTP %d): the playlist code is unknown, "+
		"or the account is expired/inactive. Check %s and the account status.", status, EnvPlaylistCode)
	if d := strings.TrimSpace(detail); d != "" {
		msg += " (upstream said: " + d + ")"
	}
	return fmt.Errorf("%w: %s", ErrPlaylistRejected, msg)
}

// upstreamError is the generic non-2xx error, carrying the status and any
// message AdoboTV's response envelope exposed.
func upstreamError(status int, body []byte) error {
	if m := upstreamMessage(body); m != "" {
		return fmt.Errorf("%w: HTTP %d: %s", ErrUpstream, status, m)
	}
	return fmt.Errorf("%w: HTTP %d", ErrUpstream, status)
}

// upstreamMessage extracts the "message" field from AdoboTV's standard error
// envelope ({"success":false,"message":"..."}). It returns "" for any body
// that is not that shape, so callers never surface raw HTML or a stack trace.
func upstreamMessage(body []byte) string {
	var envelope struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ""
	}
	return strings.TrimSpace(envelope.Message)
}
