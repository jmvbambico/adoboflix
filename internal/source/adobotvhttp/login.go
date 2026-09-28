package adobotvhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// AdoboTV's authentication endpoints. POST /v1/auth/login exchanges a
// username/password for a JWT token pair; GET /v1/profile returns the
// authenticated account, including its playlistCode.
const (
	loginPath   = "/v1/auth/login"
	profilePath = "/v1/profile"
)

// LoginClient authenticates a username/password against AdoboTV and returns the
// account's playlist code. It is the credential-entry path: username and
// password replace the playlist code as what the user types, while the code —
// read from the authenticated profile — stays the credential AdoboFlix stores,
// so everything downstream (the weekly re-check, the daily sync, the gate
// classification) is unchanged.
//
// It is an HTTP client and nothing else, like the rest of this package: no
// database handle, no write method. POST /v1/auth/login is authentication, not
// a content mutation; where AdoboTV updates users.last_login, it does so
// server-side as its own job — the same category as the device rows it writes
// while serving a playlist.
type LoginClient struct {
	baseURL   string
	userAgent string
	client    *http.Client
}

// NewLoginClient returns a LoginClient for baseURL. A nil client gets one with
// DefaultTimeout. Tests point it at an httptest AdoboTV; production uses
// NewLoginClientFromEnv.
func NewLoginClient(baseURL string, client *http.Client) *LoginClient {
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	return &LoginClient{
		baseURL:   strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		userAgent: DefaultUserAgent,
		client:    client,
	}
}

// NewLoginClientFromEnv builds a LoginClient from ADOBOFLIX_ADOBOTV_BASE_URL and
// ADOBOFLIX_ADOBOTV_USER_AGENT, reusing the adapter's own configuration
// validation so an invalid base URL is reported the same way. It reads the
// environment on each call and caches nothing: no credential state outlives the
// request that asked for it.
func NewLoginClientFromEnv() (*LoginClient, error) {
	cfg, err := configFromAllowEmptyCode(os.Getenv)
	if err != nil {
		return nil, err
	}
	return &LoginClient{
		baseURL:   cfg.BaseURL,
		userAgent: cfg.UserAgent,
		client:    &http.Client{Timeout: DefaultTimeout},
	}, nil
}

// Login authenticates username/password against AdoboTV and returns the
// playlist code from the authenticated profile.
//
// # The password
//
// The password exists only for the duration of this call. It is placed in the
// login request body and nowhere else: never logged, never stored, and never
// included in a returned error — not even the upstream's own message, which is
// discarded rather than echoed so a response body can never carry a credential
// back out. The token obtained from login is used once, immediately, to read
// the profile and is then discarded; the playlist code is what persists.
//
// # Failure mapping
//
// A 401 becomes ErrInvalidCredentials, deliberately without saying whether the
// username or the password was wrong. A 403 whose body names reCAPTCHA becomes
// ErrRecaptchaRequired — a gate AdoboFlix cannot clear and must not report as a
// bad password. A successful login whose profile carries no playlist code
// becomes ErrProfileWithoutPlaylistCode, so an empty credential is never
// stored. Everything else is ErrUpstream.
func (c *LoginClient) Login(ctx context.Context, username, password string) (string, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		// An empty field cannot be a valid credential; treat it as a refusal
		// rather than spending an upstream request. It maps to the same
		// sentinel as a real 401, so no distinction leaks here either.
		return "", ErrInvalidCredentials
	}

	token, err := c.authenticate(ctx, username, password)
	if err != nil {
		return "", err
	}

	code, err := c.profilePlaylistCode(ctx, token)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(code) == "" {
		return "", ErrProfileWithoutPlaylistCode
	}
	return strings.TrimSpace(code), nil
}

// authenticate performs POST /v1/auth/login and returns the access token.
// recaptchaToken is always empty: AdoboFlix cannot mint one, so when AdoboTV
// demands it that is surfaced as ErrRecaptchaRequired rather than retried.
func (c *LoginClient) authenticate(ctx context.Context, username, password string) (string, error) {
	payload, err := json.Marshal(struct {
		Username       string `json:"username"`
		Password       string `json:"password"`
		RecaptchaToken string `json:"recaptchaToken"`
	}{Username: username, Password: password})
	if err != nil {
		// json.Marshal of a struct of strings cannot fail for its content, so
		// this is unreachable; the password is not in the message either way.
		return "", fmt.Errorf("%w: encoding the login request: %v", ErrUpstream, err)
	}

	status, body, err := c.do(ctx, http.MethodPost, loginPath, payload, "")
	if err != nil {
		return "", err
	}

	switch status {
	case http.StatusOK:
		var envelope struct {
			Success bool `json:"success"`
			Data    struct {
				AccessToken string `json:"access_token"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return "", fmt.Errorf("%w: login response was not the expected JSON", ErrUpstream)
		}
		if strings.TrimSpace(envelope.Data.AccessToken) == "" {
			return "", fmt.Errorf("%w: login response carried no access token", ErrUpstream)
		}
		return envelope.Data.AccessToken, nil
	case http.StatusUnauthorized:
		// AdoboTV returns one 401 for an unknown username and a wrong password,
		// deliberately, to prevent enumeration. Preserve it: a single sentinel,
		// no hint about which field was wrong.
		return "", ErrInvalidCredentials
	case http.StatusForbidden:
		if isRecaptchaMessage(body) {
			return "", ErrRecaptchaRequired
		}
		return "", fmt.Errorf("%w: login HTTP %d", ErrUpstream, status)
	default:
		return "", fmt.Errorf("%w: login HTTP %d", ErrUpstream, status)
	}
}

// profilePlaylistCode performs GET /v1/profile with the login token and returns
// the playlistCode field. A missing field is not an error here — the caller
// decides that an empty code is ErrProfileWithoutPlaylistCode — so a present
// but empty field and an absent one are treated the same.
func (c *LoginClient) profilePlaylistCode(ctx context.Context, token string) (string, error) {
	status, body, err := c.do(ctx, http.MethodGet, profilePath, nil, token)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("%w: profile HTTP %d", ErrUpstream, status)
	}

	var envelope struct {
		Data struct {
			PlaylistCode string `json:"playlistCode"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", fmt.Errorf("%w: profile response was not the expected JSON", ErrUpstream)
	}
	return envelope.Data.PlaylistCode, nil
}

// do issues one request with the adapter's User-Agent and a bounded read. A
// non-nil payload is sent as JSON; a non-empty bearer is sent as an
// Authorization header. It returns the body and status even for non-2xx so the
// caller can map the upstream state; a transport failure is the only case with
// no status. Errors name the redacted URL and the transport cause only, so no
// credential from the body can reach an error message.
func (c *LoginClient) do(ctx context.Context, method, path string, payload []byte, bearer string) (int, []byte, error) {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	rawURL := c.baseURL + path

	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %s: %v", ErrUpstream, redactURL(rawURL), transportCause(err))
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %s: %v", ErrUpstream, redactURL(rawURL), transportCause(err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("%w: read response: %v", ErrUpstream, err)
	}
	return resp.StatusCode, body, nil
}

// isRecaptchaMessage reports whether a login refusal is AdoboTV's reCAPTCHA
// demand. AdoboTV's login returns 403 only for reCAPTCHA, with either "reCAPTCHA
// token is required" or "reCAPTCHA verification failed", so the wording is
// matched rather than assumed from the status alone: a future 403 for another
// reason must not be mislabelled as a captcha and hidden behind that message.
func isRecaptchaMessage(body []byte) bool {
	msg := strings.ToLower(upstreamMessage(body))
	return strings.Contains(msg, "recaptcha") || strings.Contains(msg, "captcha")
}
