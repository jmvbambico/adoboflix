package handler

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jmvbambico/adoboflix/internal/source"
	"github.com/jmvbambico/adoboflix/internal/source/adobotvhttp"
	"github.com/jmvbambico/adoboflix/internal/source/file"
)

// Stable, machine-readable error codes. A client branches on these instead of
// substring-matching an English sentence to decide which screen to show. The
// surrounding "error" field keeps its exact name and message, so an existing
// display path is unaffected; the code is purely additive.
const (
	codeDevicePending        = "device_pending"
	codeSubscriptionInactive = "subscription_inactive"
	codePlaylistRejected     = "playlist_rejected"
	codeUserAgentRejected    = "user_agent_rejected"
	codePlaylistFormatM3U    = "playlist_format_m3u"
	codeContentNotFound      = "content_not_found"
	codeNotFound             = "not_found"
	codeTokenRejected        = "content_token_rejected"
	codeMalformedPlaylist    = "malformed_playlist"
	codeMalformedDRM         = "malformed_drm"
	codeMalformedVODLibrary  = "malformed_vod_library"
	codeUpstreamError        = "upstream_error"
	codeInternalError        = "internal_error"

	// codePlaylistCodeRequired is the source asking for a playlist code that
	// is not configured yet. It is its own code because it is its own state —
	// the client shows the code-entry form rather than a rejection — and it is
	// distinct from playlist_rejected, which means a code was supplied and
	// refused.
	codePlaylistCodeRequired = "playlist_code_required"
	// codeSourceNotConfigured is the "no source chosen yet" state. Every content
	// route answers with it until the user picks one of the two real modes, so
	// the client shows the choice rather than an empty library. It is distinct
	// from playlist_code_required, which means a source is chosen and only its
	// credential is missing.
	codeSourceNotConfigured = "source_not_configured"
	// codeSourcePinnedByEnv is returned by a mode-changing endpoint when
	// ADOBOFLIX_SOURCE pins the source, so the UI cannot switch it.
	codeSourcePinnedByEnv = "source_pinned_by_env"
	// codeInvalidPlaylist is the import endpoint's rejection when the uploaded
	// content does not parse as a playlist. Its "error" text names what failed.
	codeInvalidPlaylist = "invalid_playlist"
	// codePlaylistTooLarge is the import endpoint's rejection when the upload
	// exceeds the request body cap.
	codePlaylistTooLarge = "playlist_too_large"
	// codeSyncUnsupported is the sync endpoint's answer when the active source
	// has no upstream library to refresh — an imported playlist is a snapshot the
	// user re-imports. It is not a failure of the request, so the client can
	// simply not offer sync where it is absent rather than showing an error.
	codeSyncUnsupported = "sync_unsupported"
)

// sourceErrorStatus maps an error returned by the active source to the HTTP
// status and stable code the client branches on. It is the single place this
// classification lives; a handler must never embed its own copy.
//
// Why these statuses:
//
//   - 403 for the three gates (device pending, subscription inactive, playlist
//     rejected) and the UA allowlist. AdoboTV understood the request and
//     refused it, and retrying it unchanged will not help until a human acts —
//     the same semantic AdoboTV itself uses for these gates. Device pending is
//     the outcome of every first connect, so presenting it as a 500 made the
//     commonest first-run state look like a server crash and invited
//     react-query's 5xx retry churn.
//   - 502 for the upstream misconfiguration and transport/malformed cases
//     (output_format=m3u, ErrUpstream, a malformed envelope/DRM/VOD body):
//     the upstream, not the caller and not AdoboFlix, is what needs fixing.
//   - 404 for a lookup miss, preserving the endpoints' existing behavior:
//     adobotv-http reports ErrContentNotFound, postgres-direct reports
//     sql.ErrNoRows.
//   - 500 only for something genuinely unexpected, i.e. our own fault.
//
// A transport timeout cannot be singled out as 504 here: adobotv-http collapses
// the underlying error with %v, so the timeout type is gone by the time the
// error reaches the handler. Detecting it would mean editing the adapter's
// envelope fetch, which is outside this change's scope; ErrUpstream therefore
// maps to 502. Either 502 or 504 satisfies "upstream, not us" — the point is
// that neither is a 500.
//
// The handler imports the concrete adobotv-http package to recognize its
// sentinels. The handler arguably should not know an adapter's types, but the
// clean alternative — small classifier predicates in internal/source — is a
// wider change than this defect warrants, so this import is the least-bad
// option for now and is flagged in the change report.
func sourceErrorStatus(err error) (int, string) {
	switch {
	case errors.Is(err, source.ErrSyncUnsupported):
		// The source has no upstream library to refresh. Not Implemented: the
		// request was understood and is simply not applicable to this source.
		return http.StatusNotImplemented, codeSyncUnsupported
	case errors.Is(err, source.ErrNotConfigured):
		// No source has been chosen yet. 409: the request conflicts with the
		// current state and a human must pick a mode before it can succeed. Not
		// a 5xx, which would look like our fault and invite retry churn.
		return http.StatusConflict, codeSourceNotConfigured
	case errors.Is(err, adobotvhttp.ErrDevicePending):
		return http.StatusForbidden, codeDevicePending
	case errors.Is(err, adobotvhttp.ErrSubscriptionInactive):
		return http.StatusForbidden, codeSubscriptionInactive
	case errors.Is(err, adobotvhttp.ErrPlaylistRejected):
		return http.StatusForbidden, codePlaylistRejected
	case errors.Is(err, adobotvhttp.ErrNoPlaylistCode):
		// No code has been entered yet. 403 with its own code, not 500: the
		// request was understood and a human must supply a credential before it
		// can succeed.
		return http.StatusForbidden, codePlaylistCodeRequired
	case errors.Is(err, adobotvhttp.ErrUserAgentRejected):
		return http.StatusForbidden, codeUserAgentRejected
	case errors.Is(err, adobotvhttp.ErrPlaylistFormatM3U):
		return http.StatusBadGateway, codePlaylistFormatM3U
	case errors.Is(err, adobotvhttp.ErrContentNotFound):
		return http.StatusNotFound, codeContentNotFound
	case errors.Is(err, file.ErrContentNotFound):
		return http.StatusNotFound, codeContentNotFound
	case errors.Is(err, sql.ErrNoRows):
		return http.StatusNotFound, codeNotFound
	case errors.Is(err, adobotvhttp.ErrMalformedEnvelope):
		return http.StatusBadGateway, codeMalformedPlaylist
	case errors.Is(err, adobotvhttp.ErrMalformedDRM):
		return http.StatusBadGateway, codeMalformedDRM
	case errors.Is(err, adobotvhttp.ErrMalformedVODLibrary):
		return http.StatusBadGateway, codeMalformedVODLibrary
	case errors.Is(err, adobotvhttp.ErrTokenRejected):
		return http.StatusBadGateway, codeTokenRejected
	case errors.Is(err, adobotvhttp.ErrUpstream):
		return http.StatusBadGateway, codeUpstreamError
	default:
		return http.StatusInternalServerError, codeInternalError
	}
}

// writeSourceError is the single exit for a Source error: it maps the error to
// a status and code and writes {"error": ..., "code": ...}. The "error" text is
// the error's own message, unchanged.
//
// notFoundMsg is the endpoint's wording for an ordinary lookup miss: a 404 then
// keeps the pre-existing message (so postgres-direct behavior is unchanged)
// while still gaining the code field. Pass "" from endpoints where a 404 is not
// a normal outcome. A known gate is never downgraded to the miss wording — a
// pending device on a lookup endpoint must surface as 403, not "not found".
func writeSourceError(c *gin.Context, err error, notFoundMsg string) {
	status, code := sourceErrorStatus(err)
	msg := err.Error()
	if status == http.StatusNotFound && notFoundMsg != "" {
		msg = notFoundMsg
	}
	c.JSON(status, gin.H{"error": msg, "code": code})
}
