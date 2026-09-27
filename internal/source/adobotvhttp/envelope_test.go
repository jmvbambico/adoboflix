package adobotvhttp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A well-formed JSON envelope: channels, categories, EPG URL and VOD URL all
// parse, with the absolute URLs preserved verbatim.
func TestFetchEnvelopeParsesChannelsCategoriesEPGAndVOD(t *testing.T) {
	var serverURL string
	adapter, srv := newTestServer(t, time.Minute, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/playlist/"+testPlaylistCode {
			writeErrorEnvelope(w, http.StatusNotFound, "unexpected path")
			return
		}
		serverURL = abs(r, "")
		writeBody(w, http.StatusOK, "application/json", string(envelopeBody(t, r, nil)))
	})

	env, err := adapter.fetchEnvelope(context.Background())
	if err != nil {
		t.Fatalf("fetchEnvelope: %v", err)
	}
	if want := serverURL + "/v1/epg/adobotv.xml.gz?token=" + testToken; env.Provider.EPG != want {
		t.Errorf("provider.epg = %q, want %q", env.Provider.EPG, want)
	}
	if want := serverURL + "/v1/vod?token=" + testToken; env.Provider.VODLibrary != want {
		t.Errorf("provider.vod_library = %q, want %q", env.Provider.VODLibrary, want)
	}
	if env.Provider.BilledTill != "1893456000" {
		t.Errorf("provider.billed_till = %q, want 1893456000", env.Provider.BilledTill)
	}
	if got := env.Categories["movies"].Name; got != "Movies" {
		t.Errorf("categories[movies].name = %q, want Movies", got)
	}
	if len(env.Channels) != 2 {
		t.Fatalf("len(channels) = %d, want 2", len(env.Channels))
	}
	if env.Channels[0].Name != "Alpha TV" || env.Channels[0].EpgID != "alpha.tvg" {
		t.Errorf("channel[0] = %+v, want Alpha TV/alpha.tvg", env.Channels[0])
	}
	if env.Channels[0].RuntimeAttrURL == "" {
		t.Error("channel[0].runtime_attr_url is empty")
	}
	_ = srv
}

// The most important test in the task: an unapproved device gets HTTP 200 with
// an M3U splash body even though JSON was requested. That must surface as
// "device pending approval", never as a parse error.
func TestSplashResponseIsPendingDeviceNotParseError(t *testing.T) {
	splash := "#EXTM3U\n" +
		`#EXTINF:-1 tvg-id="AdoboTV" tvg-name="AdoboTV" tvg-logo="https://i.imgur.com/SgtMwOU.png" group-title="Local TV",AdoboTV` +
		"\nhttps://raw.githubusercontent.com/jmvbambico/adoboTV_splash/master/unauthorized.m3u"

	adapter, _ := newTestServer(t, time.Minute, func(w http.ResponseWriter, r *http.Request) {
		writeBody(w, http.StatusOK, "application/vnd.apple.mpegurl", splash)
	})

	env, err := adapter.fetchEnvelope(context.Background())
	if err == nil {
		t.Fatal("fetchEnvelope on splash = nil error, want ErrDevicePending")
	}
	if !errors.Is(err, ErrDevicePending) {
		t.Errorf("error = %v, want it to wrap ErrDevicePending", err)
	}
	if errors.Is(err, ErrMalformedEnvelope) {
		t.Errorf("error also wraps ErrMalformedEnvelope; splash must not read as a parse failure: %v", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "approval") {
		t.Errorf("error %q does not name device approval", err)
	}
	if env != nil {
		t.Error("splash produced a non-nil envelope")
	}
}

// The splash is matched by body shape, not by the configurable splash URL: a
// different URL, and leading whitespace, must still be detected.
func TestSplashDetectedByShapeNotURL(t *testing.T) {
	splash := "\n\t #EXTM3U\n#EXTINF:-1,AdoboTV\nhttps://example.invalid/whatever.m3u"
	adapter, _ := newTestServer(t, time.Minute, func(w http.ResponseWriter, r *http.Request) {
		writeBody(w, http.StatusOK, "application/vnd.apple.mpegurl", splash)
	})

	_, err := adapter.fetchEnvelope(context.Background())
	if !errors.Is(err, ErrDevicePending) {
		t.Errorf("error = %v, want it to wrap ErrDevicePending", err)
	}
}

// A bad playlist code is refused outright. It is its own error, not a
// subscription problem.
func TestBadPlaylistCodeIsRejected(t *testing.T) {
	adapter, _ := newTestServer(t, time.Minute, func(w http.ResponseWriter, r *http.Request) {
		writeErrorEnvelope(w, http.StatusForbidden, "Invalid playlist code or account status")
	})

	_, err := adapter.fetchEnvelope(context.Background())
	if !errors.Is(err, ErrPlaylistRejected) {
		t.Fatalf("error = %v, want it to wrap ErrPlaylistRejected", err)
	}
	if errors.Is(err, ErrSubscriptionInactive) {
		t.Errorf("bad code also read as ErrSubscriptionInactive: %v", err)
	}
	if !strings.Contains(err.Error(), "Invalid playlist code or account status") {
		t.Errorf("error %q does not carry the upstream message", err)
	}
}

// An expired account is refused at the playlist endpoint with a status
// problem, which must be reported as an inactive subscription.
func TestExpiredPlaylistIsSubscriptionInactive(t *testing.T) {
	adapter, _ := newTestServer(t, time.Minute, func(w http.ResponseWriter, r *http.Request) {
		writeErrorEnvelope(w, http.StatusForbidden, "Account expired. Please contact support or renew your plan.")
	})

	_, err := adapter.fetchEnvelope(context.Background())
	if !errors.Is(err, ErrSubscriptionInactive) {
		t.Fatalf("error = %v, want it to wrap ErrSubscriptionInactive", err)
	}
	if errors.Is(err, ErrPlaylistRejected) {
		t.Errorf("expired also read as a bad code: %v", err)
	}
}

// An unrecognised User-Agent is a distinct refusal so the operator can point
// at the allowlist the client is failing.
func TestUserAgentRejected(t *testing.T) {
	adapter, _ := newTestServer(t, time.Minute, func(w http.ResponseWriter, r *http.Request) {
		writeErrorEnvelope(w, http.StatusForbidden, "Unauthorized player")
	})

	_, err := adapter.fetchEnvelope(context.Background())
	if !errors.Is(err, ErrUserAgentRejected) {
		t.Fatalf("error = %v, want it to wrap ErrUserAgentRejected", err)
	}
}

// A 2xx body that is neither JSON nor an M3U splash is a genuine malformed
// envelope.
func TestMalformedEnvelope(t *testing.T) {
	adapter, _ := newTestServer(t, time.Minute, func(w http.ResponseWriter, r *http.Request) {
		writeBody(w, http.StatusOK, "application/json", "not json at all")
	})

	_, err := adapter.fetchEnvelope(context.Background())
	if !errors.Is(err, ErrMalformedEnvelope) {
		t.Fatalf("error = %v, want it to wrap ErrMalformedEnvelope", err)
	}
}

// fetchEnvelope carries the configured User-Agent upstream.
func TestEnvelopeRequestSendsConfiguredUserAgent(t *testing.T) {
	var gotUA string
	adapter, _ := newTestServer(t, time.Minute, func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		writeBody(w, http.StatusOK, "application/json", string(envelopeBody(t, r, nil)))
	})

	if _, err := adapter.fetchEnvelope(context.Background()); err != nil {
		t.Fatalf("fetchEnvelope: %v", err)
	}
	if gotUA != "AdoboFlix-test" {
		t.Errorf("upstream User-Agent = %q, want AdoboFlix-test", gotUA)
	}
	if !strings.HasPrefix(DefaultUserAgent, "AdoboFlix") {
		t.Errorf("DefaultUserAgent %q must begin with AdoboFlix for the allowlist", DefaultUserAgent)
	}
}

// A transport failure must not leak the content token that lives in the query
// string of every pre-tokenized URL. http.Client.Do returns a *url.Error whose
// Error() embeds the full URL — token and all — so the adapter must unwrap it
// before formatting.
func TestTransportErrorCarriesNoToken(t *testing.T) {
	const token = "s3cr3t-content-token"
	adapter, err := newWithConfig(Config{
		BaseURL:      "http://adobotv.invalid",
		PlaylistCode: testPlaylistCode,
		UserAgent:    "AdoboFlix-test",
		CacheTTL:     time.Minute,
		HTTPClient: &http.Client{
			Timeout: time.Second,
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("dial tcp 127.0.0.1:9: connect: connection refused")
			}),
		},
	})
	if err != nil {
		t.Fatalf("newWithConfig: %v", err)
	}

	raw := "http://adobotv.invalid/v1/drm/key/ch-alpha?token=" + token
	_, _, err = adapter.get(context.Background(), raw)
	if err == nil {
		t.Fatal("get = nil error, want a transport failure")
	}
	if !errors.Is(err, ErrUpstream) {
		t.Errorf("error = %v, want it to wrap ErrUpstream", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("error leaks the content token: %q", err.Error())
	}
	if strings.Contains(err.Error(), "token=") {
		t.Errorf("error %q still carries the query string", err.Error())
	}
	if !strings.Contains(err.Error(), "/v1/drm/key/ch-alpha") {
		t.Errorf("error %q lost the endpoint path", err.Error())
	}
}

// redactURL is the redaction of last resort: it must drop both the query (token)
// and any userinfo credentials.
func TestRedactURLStripsUserInfoAndQuery(t *testing.T) {
	got := redactURL("https://bob:s3cr3t@cdn.example/vod/stream.mpd?token=abc123")
	if strings.Contains(got, "s3cr3t") {
		t.Errorf("redactURL kept the password: %q", got)
	}
	if strings.Contains(got, "abc123") || strings.Contains(got, "token=") {
		t.Errorf("redactURL kept the query/token: %q", got)
	}
	if !strings.Contains(got, "cdn.example/vod/stream.mpd") {
		t.Errorf("redactURL dropped the endpoint path: %q", got)
	}
}

// A splash served with a UTF-8 BOM must still be recognised as device-pending:
// bytes.TrimLeft only knows ASCII whitespace, so a BOM would otherwise fall
// through to json.Unmarshal and be misreported as a malformed envelope.
func TestBOMSplashIsDevicePending(t *testing.T) {
	splash := "\xef\xbb\xbf#EXTM3U\n" +
		`#EXTINF:-1 tvg-id="AdoboTV" tvg-name="AdoboTV" group-title="Local TV",AdoboTV` + "\n" +
		"https://raw.githubusercontent.com/example/unauthorized.m3u"
	adapter, _ := newTestServer(t, time.Minute, func(w http.ResponseWriter, r *http.Request) {
		writeBody(w, http.StatusOK, "application/vnd.apple.mpegurl", splash)
	})

	_, err := adapter.fetchEnvelope(context.Background())
	if !errors.Is(err, ErrDevicePending) {
		t.Fatalf("error = %v, want it to wrap ErrDevicePending", err)
	}
	if errors.Is(err, ErrMalformedEnvelope) {
		t.Errorf("BOM'd splash read as a malformed envelope: %v", err)
	}
}

// A subscriber whose stored active-playlist output_format is m3u gets a real M3U
// channel list from the playlist endpoint. That is not a pending device and not
// a malformed body: it is the output_format hazard, which only the operator can
// fix, so it must not send the user to the device-approval queue.
func TestM3UAccountPlaylistIsFormatErrorNotDevicePending(t *testing.T) {
	playlist := "#EXTM3U\n" +
		`#EXTINF:-1 tvg-id="one.tvg" group-title="News",Channel One` + "\n" +
		"https://cdn.example/one.m3u8\n" +
		`#EXTINF:-1 tvg-id="two.tvg" group-title="News",Channel Two` + "\n" +
		"https://cdn.example/two.m3u8\n"
	adapter, _ := newTestServer(t, time.Minute, func(w http.ResponseWriter, r *http.Request) {
		writeBody(w, http.StatusOK, "application/vnd.apple.mpegurl", playlist)
	})

	_, err := adapter.fetchEnvelope(context.Background())
	if !errors.Is(err, ErrPlaylistFormatM3U) {
		t.Fatalf("error = %v, want it to wrap ErrPlaylistFormatM3U", err)
	}
	if errors.Is(err, ErrDevicePending) {
		t.Errorf("a real m3u playlist was misreported as a pending device: %v", err)
	}
	if errors.Is(err, ErrMalformedEnvelope) {
		t.Errorf("a real m3u playlist read as malformed: %v", err)
	}
}
