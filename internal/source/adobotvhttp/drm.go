package adobotvhttp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// drmDetails is the document encrypted inside a runtime_attr_url. AdoboTV
// writes it with ObfuscateJSON, which escapes keys and values as \uXXXX but
// preserves JSON punctuation, so a standard parser reads it unescaped. There
// is deliberately no de-obfuscator here.
type drmDetails struct {
	DrmType     string `json:"drm_type"`
	DrmKey      string `json:"drm_key"`
	URL         string `json:"url"`
	UserAgent   string `json:"user_agent"`
	Referer     string `json:"referer"`
	ContentID   string `json:"content_id"`
	ContentType string `json:"content_type"`
}

// resolvedStream is the playable core of any leaf: a channel, a VOD asset or
// an episode, after the DRM field semantics have been normalised.
type resolvedStream struct {
	URL        string
	DrmType    *string
	DrmK       *string
	LicenseURL *string
	UserAgent  *string
	Referer    *string
}

// resolveRuntimeAttr follows a runtime_attr_url: one GET, base64-decode, then
// JSON. It detects the KODIPROP M3U body and the upstream account/device
// rejections before parsing, so none of them surface as a parse error.
func (a *Adapter) resolveRuntimeAttr(ctx context.Context, runtimeAttrURL string) (*resolvedStream, error) {
	if strings.TrimSpace(runtimeAttrURL) == "" {
		return nil, fmt.Errorf("%w: this item carries no runtime_attr_url", ErrContentNotFound)
	}

	body, status, err := a.get(ctx, runtimeAttrURL)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		detail := upstreamMessage(body)
		lower := strings.ToLower(detail)
		switch {
		case strings.Contains(lower, "not active"):
			return nil, subscriptionInactiveError(detail)
		case strings.Contains(lower, "not approved"), strings.Contains(lower, "device"):
			return nil, devicePendingDetail(detail)
		case strings.Contains(lower, "token"):
			return nil, fmt.Errorf("%w: content token rejected: %s; refetch the playlist", ErrUpstream, detail)
		default:
			return nil, upstreamError(status, body)
		}
	}

	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(body)))
	if err != nil {
		return nil, fmt.Errorf("%w: response is not base64: %v", ErrMalformedDRM, err)
	}

	// /v1/drm/key/ branches on the account's active-playlist output_format. When
	// that stored value is "m3u", it answers with KODIPROP M3U text instead of
	// the JSON details. Detect and explain, never mis-parse.
	if looksLikeM3U(decoded) {
		return nil, fmt.Errorf("%w: AdoboTV returned KODIPROP M3U text instead of JSON; "+
			"set the account's active playlist output_format to json (%s)", ErrPlaylistFormatM3U, EnvPlaylistCode)
	}

	var details drmDetails
	if err := json.Unmarshal(decoded, &details); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedDRM, err)
	}
	return applyDRM(details), nil
}

// applyDRM normalises AdoboTV's counter-intuitive DRM fields into the shape
// AdoboFlix's player expects:
//
//   - drm_type "m3u" means NO DRM. It is not a container hint.
//   - when drm_type is a real DRM tech, drm_key is the ClearKey key for
//     clearkey and the license URL for everything else: one field, two
//     meanings.
//   - if drm_type means no DRM but drm_key is populated, it is a "user:pass"
//     pair AdoboTV moved out of a .mpd URL's userinfo when no license existed.
//
// DRM tech names are canonicalised to the capitalised forms the player matches
// on ("Clearkey", "Widevine"), because upstream lowercases them.
func applyDRM(d drmDetails) *resolvedStream {
	rs := &resolvedStream{URL: d.URL}
	if d.UserAgent != "" {
		ua := d.UserAgent
		rs.UserAgent = &ua
	}
	if d.Referer != "" {
		ref := d.Referer
		rs.Referer = &ref
	}

	drmType := strings.ToLower(strings.TrimSpace(d.DrmType))
	if drmType == "" || drmType == "m3u" {
		if creds, ok := userInfoCredentials(d.DrmKey, d.URL); ok {
			rs.URL = injectUserInfo(d.URL, creds)
		}
		return rs
	}

	canonical := canonicalDRMType(drmType)
	rs.DrmType = &canonical
	if d.DrmKey != "" {
		if drmType == "clearkey" {
			key := d.DrmKey
			rs.DrmK = &key
		} else {
			license := d.DrmKey
			rs.LicenseURL = &license
		}
	}
	return rs
}

// userInfoCredentials reports whether drm_key is really a "user:pass" pair.
// AdoboTV only produces this shape for a .mpd URL that carried userinfo and
// had no license, so the .mpd suffix is required; this keeps a ClearKey
// "kid:key" value (which has the same punctuation) from being mistaken for
// credentials.
func userInfoCredentials(drmKey, streamURL string) (string, bool) {
	key := strings.TrimSpace(drmKey)
	if key == "" || strings.Contains(key, "://") || strings.ContainsAny(key, "/@") {
		return "", false
	}
	if strings.Count(key, ":") != 1 {
		return "", false
	}
	u, p, ok := strings.Cut(key, ":")
	if !ok || u == "" || p == "" {
		return "", false
	}
	parsed, err := url.Parse(streamURL)
	if err != nil || !strings.HasSuffix(parsed.Path, ".mpd") {
		return "", false
	}
	return key, true
}

// injectUserInfo re-embeds credentials into a stream URL as userinfo so the
// proxy can authenticate to the origin.
func injectUserInfo(streamURL, credentials string) string {
	u, err := url.Parse(streamURL)
	if err != nil {
		return streamURL
	}
	user, pass, _ := strings.Cut(credentials, ":")
	u.User = url.UserPassword(user, pass)
	return u.String()
}

// canonicalDRMType maps AdoboTV's lowercased tech names onto the capitalised
// values internal to AdoboFlix (the client matches "Clearkey"/"Widevine").
func canonicalDRMType(lower string) string {
	switch lower {
	case "clearkey":
		return "Clearkey"
	case "widevine":
		return "Widevine"
	case "playready":
		return "PlayReady"
	default:
		r := []rune(lower)
		if len(r) > 0 {
			r[0] = unicode.ToUpper(r[0])
		}
		return string(r)
	}
}
