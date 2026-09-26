package adobotvhttp

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func resolveTag(t *testing.T, adapter *Adapter, srvURL, tag string) (*resolvedStream, error) {
	t.Helper()
	return adapter.resolveRuntimeAttr(context.Background(), srvURL+"/v1/drm/key/"+tag)
}

// ClearKey: drm_key carries the ClearKey child key.
func TestResolveClearKeyUsesDrmKeyAsKey(t *testing.T) {
	const key = "9b1b0f9e6a2d4c3e8f7a:5f4e3d2c1b0a99887766"
	adapter, srv := newTestServer(t, time.Minute, drmRouter(map[string]string{
		"ch-alpha": drmBody(map[string]string{
			"drm_type":     "clearkey",
			"drm_key":      key,
			"url":          "https://cdn.example/live/alpha/index.mpd",
			"user_agent":   "AlphaUA/1.0",
			"referer":      "https://cdn.example/",
			"content_id":   "11111111-2222-3333-4444-555555555555",
			"content_type": "channel",
		}),
	}))

	resolved, err := resolveTag(t, adapter, srv.URL, "ch-alpha")
	if err != nil {
		t.Fatalf("resolveRuntimeAttr: %v", err)
	}
	if resolved.DrmType == nil || *resolved.DrmType != "Clearkey" {
		t.Errorf("drm_type = %v, want Clearkey", resolved.DrmType)
	}
	if resolved.DrmK == nil || *resolved.DrmK != key {
		t.Errorf("drm_k = %v, want %q", resolved.DrmK, key)
	}
	if resolved.LicenseURL != nil {
		t.Errorf("license_url = %v, want nil for ClearKey", *resolved.LicenseURL)
	}
	if resolved.URL != "https://cdn.example/live/alpha/index.mpd" {
		t.Errorf("url = %q, want unchanged", resolved.URL)
	}
	if resolved.UserAgent == nil || *resolved.UserAgent != "AlphaUA/1.0" {
		t.Errorf("user_agent = %v, want AlphaUA/1.0", resolved.UserAgent)
	}
	if resolved.Referer == nil || *resolved.Referer != "https://cdn.example/" {
		t.Errorf("referer = %v, want https://cdn.example/", resolved.Referer)
	}
}

// Widevine (and every non-ClearKey tech): drm_key is the license URL.
func TestResolveWidevineUsesDrmKeyAsLicenseURL(t *testing.T) {
	adapter, srv := newTestServer(t, time.Minute, drmRouter(map[string]string{
		"ch-beta": drmBody(map[string]string{
			"drm_type": "widevine",
			"drm_key":  "https://license.example/widevine",
			"url":      "https://cdn.example/live/beta/index.mpd",
		}),
	}))

	resolved, err := resolveTag(t, adapter, srv.URL, "ch-beta")
	if err != nil {
		t.Fatalf("resolveRuntimeAttr: %v", err)
	}
	if resolved.DrmType == nil || *resolved.DrmType != "Widevine" {
		t.Errorf("drm_type = %v, want Widevine", resolved.DrmType)
	}
	if resolved.LicenseURL == nil || *resolved.LicenseURL != "https://license.example/widevine" {
		t.Errorf("license_url = %v, want the license URL", resolved.LicenseURL)
	}
	if resolved.DrmK != nil {
		t.Errorf("drm_k = %v, want nil for Widevine", *resolved.DrmK)
	}
}

// drm_type "m3u" means NO DRM — it is not a container hint.
func TestResolveM3UMeansNoDRM(t *testing.T) {
	adapter, srv := newTestServer(t, time.Minute, drmRouter(map[string]string{
		"ch-alpha": drmBody(map[string]string{
			"drm_type": "m3u",
			"drm_key":  "",
			"url":      "https://cdn.example/live/alpha/index.m3u8",
		}),
	}))

	resolved, err := resolveTag(t, adapter, srv.URL, "ch-alpha")
	if err != nil {
		t.Fatalf("resolveRuntimeAttr: %v", err)
	}
	if resolved.DrmType != nil || resolved.DrmK != nil || resolved.LicenseURL != nil {
		t.Errorf("m3u produced DRM fields: type=%v k=%v license=%v", resolved.DrmType, resolved.DrmK, resolved.LicenseURL)
	}
	if resolved.URL != "https://cdn.example/live/alpha/index.m3u8" {
		t.Errorf("url = %q, want unchanged", resolved.URL)
	}
}

// A "user:pass" pair in drm_key is re-embedded into a .mpd URL rather than
// mistaken for a key.
func TestResolveUserPassInDrmKeyIsEmbeddedInURL(t *testing.T) {
	adapter, srv := newTestServer(t, time.Minute, drmRouter(map[string]string{
		"vod-1": drmBody(map[string]string{
			"drm_type": "m3u",
			"drm_key":  "bob:s3cr3t",
			"url":      "https://cdn.example/movies/stream.mpd",
		}),
	}))

	resolved, err := resolveTag(t, adapter, srv.URL, "vod-1")
	if err != nil {
		t.Fatalf("resolveRuntimeAttr: %v", err)
	}
	if want := "https://bob:s3cr3t@cdn.example/movies/stream.mpd"; resolved.URL != want {
		t.Errorf("url = %q, want %q", resolved.URL, want)
	}
	if resolved.DrmType != nil || resolved.DrmK != nil || resolved.LicenseURL != nil {
		t.Errorf("user:pass produced DRM fields: type=%v k=%v license=%v", resolved.DrmType, resolved.DrmK, resolved.LicenseURL)
	}
}

// A ClearKey "kid:key" on a .mpd URL must NOT be mistaken for userinfo.
func TestClearKeyKeyIsNotMistakenForUserInfo(t *testing.T) {
	adapter, srv := newTestServer(t, time.Minute, drmRouter(map[string]string{
		"ch-alpha": drmBody(map[string]string{
			"drm_type": "clearkey",
			"drm_key":  "aaaa:bbbb",
			"url":      "https://cdn.example/live/alpha/index.mpd",
		}),
	}))

	resolved, err := resolveTag(t, adapter, srv.URL, "ch-alpha")
	if err != nil {
		t.Fatalf("resolveRuntimeAttr: %v", err)
	}
	if resolved.DrmK == nil || *resolved.DrmK != "aaaa:bbbb" {
		t.Fatalf("drm_k = %v, want aaaa:bbbb", resolved.DrmK)
	}
	if strings.Contains(resolved.URL, "@") {
		t.Errorf("url = %q, want credentials NOT embedded for ClearKey", resolved.URL)
	}
}

// The output_format=m3u hazard: /v1/drm/key/ returns KODIPROP M3U text. It is
// detected and explained, never mis-parsed as JSON.
func TestResolveKODIPROPM3UIsFormatError(t *testing.T) {
	m3u := "#EXTM3U\n#EXTINF:-1,DRM\n" +
		"#KODIPROP:inputstream.adaptive.license_type=org.w3.clearkey\n" +
		"#KODIPROP:inputstream.adaptive.license_key=aaaa:bbbb\n" +
		"https://cdn.example/live/alpha/index.mpd"
	adapter, srv := newTestServer(t, time.Minute, drmRouter(map[string]string{
		"ch-alpha": base64.StdEncoding.EncodeToString([]byte(m3u)),
	}))

	_, err := resolveTag(t, adapter, srv.URL, "ch-alpha")
	if !errors.Is(err, ErrPlaylistFormatM3U) {
		t.Fatalf("error = %v, want it to wrap ErrPlaylistFormatM3U", err)
	}
	if errors.Is(err, ErrMalformedDRM) {
		t.Errorf("m3u format also read as malformed DRM: %v", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "output_format") {
		t.Errorf("error %q does not mention output_format", err)
	}
}

// An inactive subscriber can fetch a playlist but is refused every playable
// URL. That is "subscription inactive", not a playback failure.
func TestResolveInactiveSubscription(t *testing.T) {
	adapter, srv := newTestServer(t, time.Minute, func(w http.ResponseWriter, r *http.Request) {
		writeErrorEnvelope(w, http.StatusForbidden, "User account is not active")
	})

	_, err := resolveTag(t, adapter, srv.URL, "ch-alpha")
	if !errors.Is(err, ErrSubscriptionInactive) {
		t.Fatalf("error = %v, want it to wrap ErrSubscriptionInactive", err)
	}
	if errors.Is(err, ErrPlaylistRejected) || errors.Is(err, ErrDevicePending) {
		t.Errorf("inactive subscription conflated with another gate: %v", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "active") {
		t.Errorf("error %q does not name the account status", err)
	}
}

// A pending device is refused at content resolution too, and stays distinct.
func TestResolvePendingDevice(t *testing.T) {
	adapter, srv := newTestServer(t, time.Minute, func(w http.ResponseWriter, r *http.Request) {
		writeErrorEnvelope(w, http.StatusForbidden, "Device is not approved")
	})

	_, err := resolveTag(t, adapter, srv.URL, "ch-alpha")
	if !errors.Is(err, ErrDevicePending) {
		t.Fatalf("error = %v, want it to wrap ErrDevicePending", err)
	}
	if errors.Is(err, ErrSubscriptionInactive) {
		t.Errorf("pending device conflated with inactive subscription: %v", err)
	}
}

// The fixtures are escaped exactly as ObfuscateJSON writes them, and a
// standard json.Unmarshal reads them with no de-obfuscator.
func TestObfuscatedJSONDetailsAreParsedTransparently(t *testing.T) {
	body := drmBody(map[string]string{
		"drm_type": "clearkey",
		"drm_key":  "aaaa:bbbb",
		"url":      "https://cdn.example/x.mpd",
	})
	decoded, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if !strings.Contains(string(decoded), `\u0064\u0072\u006d\u005f\u0074\u0079\u0070\u0065`) {
		t.Fatalf("fixture is not obfuscated as expected: %s", decoded)
	}

	adapter, srv := newTestServer(t, time.Minute, drmRouter(map[string]string{"ch-alpha": body}))
	resolved, err := resolveTag(t, adapter, srv.URL, "ch-alpha")
	if err != nil {
		t.Fatalf("resolveRuntimeAttr on obfuscated JSON: %v", err)
	}
	if resolved.DrmType == nil || *resolved.DrmType != "Clearkey" {
		t.Errorf("drm_type = %v, want Clearkey", resolved.DrmType)
	}
}

// A non-base64 or non-JSON body is malformed DRM, distinct from the m3u hazard.
func TestResolveMalformedDRMBody(t *testing.T) {
	adapter, srv := newTestServer(t, time.Minute, drmRouter(map[string]string{
		"ch-alpha": base64.StdEncoding.EncodeToString([]byte("}{ not json")),
	}))

	_, err := resolveTag(t, adapter, srv.URL, "ch-alpha")
	if !errors.Is(err, ErrMalformedDRM) {
		t.Fatalf("error = %v, want it to wrap ErrMalformedDRM", err)
	}
	if errors.Is(err, ErrPlaylistFormatM3U) {
		t.Errorf("malformed body read as the m3u format hazard: %v", err)
	}
}
