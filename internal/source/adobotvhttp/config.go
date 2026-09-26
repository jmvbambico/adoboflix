package adobotvhttp

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Environment keys this adapter reads. They follow the ADOBOFLIX_* convention
// already used for ADOBOFLIX_SOURCE and ADOBOFLIX_PG_URL.
const (
	// EnvBaseURL is AdoboTV's base URL, e.g. http://127.0.0.1:8080. Required.
	EnvBaseURL = "ADOBOFLIX_ADOBOTV_BASE_URL"
	// EnvPlaylistCode is the subscriber's AdoboTV playlist code. Required.
	EnvPlaylistCode = "ADOBOFLIX_ADOBOTV_PLAYLIST_CODE"
	// EnvUserAgent overrides the User-Agent sent upstream. Optional; the
	// default begins with "AdoboFlix", which is in AdoboTV's allowlist, so the
	// traffic is distinguishable in the operator's audit logs.
	EnvUserAgent = "ADOBOFLIX_ADOBOTV_USER_AGENT"
	// EnvCacheTTL overrides how long the playlist envelope and VOD library are
	// cached, as a Go duration string. Optional; defaults to DefaultCacheTTL.
	EnvCacheTTL = "ADOBOFLIX_ADOBOTV_CACHE_TTL"
)

const (
	// DefaultUserAgent is deliberately prefixed with "AdoboFlix", a value the
	// AdoboTV operator added to valid_user_agents so this client is
	// identifiable rather than indistinguishable from a browser.
	DefaultUserAgent = "AdoboFlix/1.0 (+https://github.com/jmvbambico/adoboflix)"
	// DefaultCacheTTL matches AdoboTV's own 5-minute playlist cache. Being
	// more aggressive than the server would only add latency, never freshness.
	DefaultCacheTTL = 5 * time.Minute
	// DefaultTimeout bounds every upstream request; no request is unbounded.
	DefaultTimeout = 30 * time.Second
	// maxBodyBytes caps how much of an upstream response is buffered. The
	// compiled EPG and VOD library are the large ones; 128 MiB is far above
	// any real payload and still a hard ceiling.
	maxBodyBytes = 128 << 20
)

// Config is the adapter's constructor input. Tests build it directly; the
// registry factory builds it from the environment.
type Config struct {
	// BaseURL is AdoboTV's origin, without a trailing slash.
	BaseURL string
	// PlaylistCode is the subscriber credential.
	PlaylistCode string
	// UserAgent is sent on every upstream request.
	UserAgent string
	// CacheTTL is how long the envelope and VOD library stay cached. Zero
	// means "never cache" and is used by tests to force a refetch.
	CacheTTL time.Duration
	// HTTPClient is overridable for tests. When nil, a client with
	// DefaultTimeout is created.
	HTTPClient *http.Client
	// Clock is overridable for tests. When nil, time.Now is used.
	Clock func() time.Time
}

// configFromEnv reads and validates the adapter's configuration from os.Getenv.
func configFromEnv() (Config, error) {
	return configFrom(func(key string) string { return os.Getenv(key) })
}

// configFrom is configFromEnv with an injectable getter, so validation can be
// tested without touching the process environment.
func configFrom(getenv func(string) string) (Config, error) {
	cfg := Config{
		BaseURL:      strings.TrimSpace(getenv(EnvBaseURL)),
		PlaylistCode: strings.TrimSpace(getenv(EnvPlaylistCode)),
		UserAgent:    strings.TrimSpace(getenv(EnvUserAgent)),
		CacheTTL:     DefaultCacheTTL,
	}

	if cfg.BaseURL == "" {
		return Config{}, fmt.Errorf("%s is not set: set it to the AdoboTV base URL (e.g. http://127.0.0.1:8080)", EnvBaseURL)
	}
	parsed, err := url.Parse(cfg.BaseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return Config{}, fmt.Errorf("%s is not a valid absolute URL: %q", EnvBaseURL, cfg.BaseURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return Config{}, fmt.Errorf("%s must use http or https, got %q", EnvBaseURL, parsed.Scheme)
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")

	if cfg.PlaylistCode == "" {
		return Config{}, fmt.Errorf("%s is not set: set it to your AdoboTV playlist code", EnvPlaylistCode)
	}

	if cfg.UserAgent == "" {
		cfg.UserAgent = DefaultUserAgent
	}

	if raw := strings.TrimSpace(getenv(EnvCacheTTL)); raw != "" {
		ttl, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("%s is not a valid duration: %q (%w)", EnvCacheTTL, raw, err)
		}
		if ttl < 0 {
			return Config{}, fmt.Errorf("%s must not be negative, got %q", EnvCacheTTL, raw)
		}
		cfg.CacheTTL = ttl
	}

	return cfg, nil
}

// newWithConfig validates a Config and builds an Adapter.
func newWithConfig(cfg Config) (*Adapter, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("adobotv-http: base URL is required")
	}
	if strings.TrimSpace(cfg.PlaylistCode) == "" {
		return nil, errors.New("adobotv-http: playlist code is required")
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = DefaultUserAgent
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	return &Adapter{
		baseURL:      strings.TrimRight(cfg.BaseURL, "/"),
		playlistCode: cfg.PlaylistCode,
		userAgent:    cfg.UserAgent,
		cacheTTL:     cfg.CacheTTL,
		httpClient:   client,
		now:          clock,
	}, nil
}
