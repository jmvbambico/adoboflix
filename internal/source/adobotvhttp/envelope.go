package adobotvhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
		if err := classifyGate(detail); err != nil {
			return nil, err
		}
		return nil, playlistRejectedError(status, detail)
	}
	if status < 200 || status >= 300 {
		return nil, upstreamError(status, body)
	}

	// The splash trap: an unapproved device gets HTTP 200 with an M3U body
	// even though JSON was requested. Match the #EXTM3U shape, never the
	// configurable splash URL, and report it as device approval rather than a
	// parse failure.
	if looksLikeM3U(body) {
		if isDeviceSplash(body) {
			return nil, devicePendingDetail("")
		}
		// Not the splash: a subscriber whose stored active-playlist
		// output_format is m3u legitimately gets their real M3U channel list
		// here. That is not a pending device (do not send them to the operator
		// for approval) and not a malformed body — it is the same
		// output_format hazard the DRM resolver reports, and only the operator
		// can switch the account back to JSON.
		return nil, fmt.Errorf("%w: AdoboTV returned an M3U playlist for a JSON request; "+
			"the account's active playlist output_format is m3u (%s)", ErrPlaylistFormatM3U, EnvPlaylistCode)
	}

	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedEnvelope, err)
	}
	return &env, nil
}

// m3uBody strips a leading UTF-8 BOM and leading ASCII whitespace. bytes.TrimLeft
// alone only knows ASCII whitespace, so a BOM'd splash would otherwise not match
// and would fall through to json.Unmarshal — reported as a malformed envelope
// instead of the pending-device state this whole mechanism exists to name.
func m3uBody(body []byte) []byte {
	return bytes.TrimLeft(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf")), " \t\r\n")
}

// looksLikeM3U reports whether body begins with the M3U marker after a leading
// BOM and whitespace. This is the pending-device (or m3u-account) signature; it
// is intentionally not tied to any URL the operator might configure.
func looksLikeM3U(body []byte) bool {
	return bytes.HasPrefix(m3uBody(body), []byte("#EXTM3U"))
}

// isDeviceSplash reports whether an M3U body is AdoboTV's pending-device splash
// rather than a subscriber's real playlist. Both are M3U, so the SHAPE tells them
// apart: the splash is a one-entry playlist whose only channel is AdoboTV itself
// (the operator's own name, written into tvg-id/tvg-name and the display name),
// whereas a real playlist is the subscriber's own channel list. The
// operator-configurable splash URL is deliberately not consulted. A genuine
// playlist that happened to be a single channel named exactly "AdoboTV" would be
// filed as device-pending; that collision is not reachable with a real list.
func isDeviceSplash(body []byte) bool {
	var entry string
	entries := 0
	for _, line := range strings.Split(string(m3uBody(body)), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "#EXTINF") {
			continue
		}
		entries++
		if entries > 1 {
			return false
		}
		entry = line
	}
	if entries != 1 {
		return false
	}
	if strings.Contains(entry, `tvg-id="AdoboTV"`) || strings.Contains(entry, `tvg-name="AdoboTV"`) {
		return true
	}
	_, name, _ := strings.Cut(entry, ",")
	return strings.TrimSpace(name) == "AdoboTV"
}

// gateError maps a non-2xx body to a gate sentinel, or nil when the message
// names no known gate. A rejected content token additionally drops the cached
// playlist envelope so the next request refetches and mints a new token, rather
// than re-sending the dead one until the TTL lapses.
func (a *Adapter) gateError(body []byte) error {
	err := classifyGate(upstreamMessage(body))
	if errors.Is(err, ErrTokenRejected) {
		a.invalidateCache()
	}
	return err
}

// get issues one GET with the adapter's User-Agent and reads a bounded body.
// It returns the body and status even for non-2xx so callers can map the
// upstream error message; a transport failure is the only case with no status.
func (a *Adapter) get(ctx context.Context, rawURL string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %s: %v", ErrUpstream, a.redact(rawURL), transportCause(err))
	}
	req.Header.Set("User-Agent", a.userAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %s: %v", ErrUpstream, a.redact(rawURL), transportCause(err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("%w: read response: %v", ErrUpstream, err)
	}
	return body, resp.StatusCode, nil
}

// transportCause returns the underlying cause of a transport error WITHOUT the
// *url.Error wrapper. That wrapper's Error() is "Get <full url>: <cause>", and
// every URL this adapter fetches after the playlist carries ?token=<contentToken>
// — so formatting it verbatim would leak the subscriber's live token into both
// the server log and the HTTP response body. The cause alone (dial/DNS/TLS/
// timeout text) names what broke and carries no URL. It is unwrapped in a loop
// because a redirect chain can nest one *url.Error inside another.
func transportCause(err error) error {
	for {
		var ue *url.Error
		if !errors.As(err, &ue) {
			return err
		}
		if ue.Err == nil {
			return errors.New("request failed")
		}
		err = ue.Err
	}
}

// redact returns rawURL with every credential-bearing part removed: the query
// string (the content token), any userinfo, and the playlist code when it
// appears in the path. It is the only way a URL may reach an error message.
func (a *Adapter) redact(rawURL string) string {
	redacted := redactURL(rawURL)
	if a.playlistCode != "" {
		redacted = strings.ReplaceAll(redacted, url.PathEscape(a.playlistCode), "REDACTED")
	}
	return redacted
}

// redactURL strips the query string (which carries the content token) and any
// userinfo (user:pass@) so a failed request can be named in an error without
// leaking a credential.
func redactURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "upstream"
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	return u.String()
}
