package adobotvhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// envelope is the JSON document served by GET /v1/playlist/:code. It is
// self-describing: the EPG and VOD-library URLs arrive pre-tokenized, so the
// adapter follows them verbatim rather than constructing them.
type envelope struct {
	Provider   providerInfo            `json:"provider"`
	Categories map[string]categoryInfo `json:"categories"`
	Channels   []channelInfo           `json:"channels"`
}

type providerInfo struct {
	UserMessage string `json:"user_message"`
	EPG         string `json:"epg"`
	VODLibrary  string `json:"vod_library"`
	// BilledTill is a string upstream (fmt.Sprintf("%d", unixSeconds)), not a
	// number, and is omitted entirely for non-subscription tiers.
	BilledTill string `json:"billed_till"`
}

type categoryInfo struct {
	Name  string `json:"name"`
	Icon  string `json:"icon"`
	Adult bool   `json:"adult"`
}

type channelInfo struct {
	Name           string `json:"name"`
	Category       string `json:"category"`
	Icon           string `json:"icon"`
	EpgID          string `json:"epg_id"`
	URL            string `json:"url"`
	RuntimeAttrURL string `json:"runtime_attr_url"`
}

// fetchEnvelope performs one GET of the playlist and maps every documented
// rejection to its own error.
func (a *Adapter) fetchEnvelope(ctx context.Context) (*envelope, error) {
	endpoint := a.baseURL + "/v1/playlist/" + url.PathEscape(a.playlistCode)
	body, status, err := a.get(ctx, endpoint)
	if err != nil {
		return nil, err
	}

	// The playlist endpoint is the gate that admits active, inactive and
	// expired accounts alike. A 401/403 here is the code itself (or expiry),
	// not a playback problem.
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		detail := upstreamMessage(body)
		lower := strings.ToLower(detail)
		switch {
		case strings.Contains(lower, "expired"):
			return nil, subscriptionInactiveError(detail)
		case strings.Contains(lower, "player") || strings.Contains(lower, "user-agent") || strings.Contains(lower, "user agent"):
			return nil, fmt.Errorf("%w: %s", ErrUserAgentRejected, detail)
		default:
			return nil, playlistRejectedError(status, detail)
		}
	}
	if status < 200 || status >= 300 {
		return nil, upstreamError(status, body)
	}

	// The splash trap: an unapproved device gets HTTP 200 with an M3U body
	// even though JSON was requested. Match the #EXTM3U shape, never the
	// configurable splash URL, and report it as device approval rather than a
	// parse failure.
	if looksLikeM3U(body) {
		return nil, devicePendingDetail("")
	}

	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedEnvelope, err)
	}
	return &env, nil
}

// looksLikeM3U reports whether body begins with the M3U marker after leading
// whitespace. This is the pending-device signature; it is intentionally not
// tied to any URL the operator might configure.
func looksLikeM3U(body []byte) bool {
	return bytes.HasPrefix(bytes.TrimLeft(body, " \t\r\n"), []byte("#EXTM3U"))
}

// get issues one GET with the adapter's User-Agent and reads a bounded body.
// It returns the body and status even for non-2xx so callers can map the
// upstream error message; a transport failure is the only case with no status.
func (a *Adapter) get(ctx context.Context, rawURL string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: build request for %s: %v", ErrUpstream, redactURL(rawURL), err)
	}
	req.Header.Set("User-Agent", a.userAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("%w: read response: %v", ErrUpstream, err)
	}
	return body, resp.StatusCode, nil
}

// redactURL strips the query string (which carries the content token) so a
// failed request can be named in an error without leaking the credential.
func redactURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "upstream"
	}
	u.RawQuery = ""
	return u.String()
}
