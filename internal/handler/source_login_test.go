package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jmvbambico/adoboflix/internal/playlistcode"
	"github.com/jmvbambico/adoboflix/internal/source"
	"github.com/jmvbambico/adoboflix/internal/source/adobotvhttp"
)

// fakeAuth is an httptest AdoboTV implementing just the two endpoints the login
// flow uses: POST /v1/auth/login and GET /v1/profile. It records what it was
// sent so a test can prove a request really carried a value before asserting
// that value is absent from the response and the logs.
type fakeAuth struct {
	username, password string
	code               string
	// recaptcha makes login answer 403 the way AdoboTV does when a secret is
	// configured.
	recaptcha bool
	// loginStatus, when non-zero, forces that status on login.
	loginStatus int
	// profileStatus, when non-zero, forces that status on an otherwise
	// successful profile read.
	profileStatus int

	mu        sync.Mutex
	seenUser  string
	seenPass  string
	loginHit  int
	profileHi int
}

func (f *fakeAuth) seen() (user, pass string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seenUser, f.seenPass
}

func (f *fakeAuth) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/login":
			f.mu.Lock()
			f.loginHit++
			f.mu.Unlock()

			body, _ := io.ReadAll(r.Body)
			var req struct {
				Username       string `json:"username"`
				Password       string `json:"password"`
				RecaptchaToken string `json:"recaptchaToken"`
			}
			_ = json.Unmarshal(body, &req)
			f.mu.Lock()
			f.seenUser, f.seenPass = req.Username, req.Password
			f.mu.Unlock()

			switch {
			case f.loginStatus != 0:
				writeAuthError(w, f.loginStatus, "login refused")
			case f.recaptcha:
				writeAuthError(w, http.StatusForbidden, "reCAPTCHA token is required")
			case req.Username == f.username && req.Password == f.password:
				writeAuthJSON(w, http.StatusOK, map[string]any{
					"success": true,
					"data":    map[string]any{"access_token": "test-access-token", "refresh_token": "test-refresh-token"},
					"message": "Login successful",
				})
			default:
				writeAuthError(w, http.StatusUnauthorized, "Invalid username or password")
			}
		case "/v1/profile":
			f.mu.Lock()
			f.profileHi++
			f.mu.Unlock()
			if f.profileStatus != 0 {
				writeAuthError(w, f.profileStatus, "profile unavailable")
				return
			}
			if r.Header.Get("Authorization") != "Bearer test-access-token" {
				writeAuthError(w, http.StatusUnauthorized, "Invalid or expired token")
				return
			}
			writeAuthJSON(w, http.StatusOK, map[string]any{
				"success": true,
				"data":    map[string]any{"username": f.username, "playlistCode": f.code},
				"message": "Profile retrieved",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeAuthJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeAuthError(w http.ResponseWriter, status int, message string) {
	writeAuthJSON(w, status, map[string]any{"success": false, "message": message})
}

func loginRouter(h *SourceHandler) *gin.Engine {
	r := gin.New()
	r.POST("/api/v1/source/login", h.Login)
	return r
}

func loginRequestBody(t *testing.T, username, password string) string {
	t.Helper()
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		t.Fatalf("marshal login body: %v", err)
	}
	return string(body)
}

func doLogin(t *testing.T, r *gin.Engine, username, password string) *httptest.ResponseRecorder {
	t.Helper()
	return doJSON(t, r, http.MethodPost, "/api/v1/source/login", loginRequestBody(t, username, password))
}

func decodeCode(t *testing.T, w *httptest.ResponseRecorder) (int, string, string) {
	t.Helper()
	var got struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body %q: %v", w.Body.String(), err)
	}
	return w.Code, got.Code, got.Error
}

// newLoginHandler wires a SourceHandler to a fake AdoboTV through the real
// LoginClient (so the JSON parsing and status mapping are exercised), with an
// injectable candidate adapter so a swap can be observed without opening a real
// source.
func newLoginHandler(t *testing.T, fake *fakeAuth, candidate *codeStubSource) (*SourceHandler, *PlayerHandler, *playlistcode.Store, *httptest.Server) {
	t.Helper()
	srv := fake.server(t)
	h, player, store := newTestSourceHandler(t, source.Config{Name: adobotvhttp.Name}, "")
	h.login = adobotvhttp.NewLoginClient(srv.URL, srv.Client()).Login
	h.open = func(source.Config) (source.Source, error) { return candidate, nil }
	return h, player, store, srv
}

// A successful login reads the profile's playlist code, stores it through the
// existing store, and swaps the live source — the same path a typed-in code
// takes.
func TestLoginStoresProfileCodeAndSwapsSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const password = "SUPER-SECRET-PASSWORD-1"
	const code = "CODE-FROM-PROFILE"

	fake := &fakeAuth{username: "alice", password: password, code: code}
	candidate := &codeStubSource{name: adobotvhttp.Name} // ListChannels succeeds
	h, player, store, _ := newLoginHandler(t, fake, candidate)

	w := doLogin(t, loginRouter(h), "alice", password)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	// The code read from the profile is what persists and what the source was
	// swapped to.
	stored, ok, err := store.Load()
	if err != nil || !ok || stored != code {
		t.Fatalf("store = (%q, ok=%v, err=%v), want the profile's code persisted", stored, ok, err)
	}
	if player.src() != source.Source(candidate) {
		t.Error("the live source was not swapped to the candidate")
	}
	// Positive that the request really carried the credential (so the absence
	// assertions below are not vacuous).
	if user, pass := fake.seen(); user != "alice" || pass != password {
		t.Errorf("AdoboTV saw (%q, %q), want the submitted credentials", user, pass)
	}
	// Neither the password nor the playlist code is echoed back.
	body := w.Body.String()
	if strings.Contains(body, password) {
		t.Errorf("the response leaked the password: %s", body)
	}
	if strings.Contains(body, code) {
		t.Errorf("the response leaked the playlist code: %s", body)
	}
}

// AdoboTV returns one 401 for an unknown username and a wrong password alike.
// AdoboFlix must not distinguish them, or the endpoint becomes an enumeration
// oracle.
func TestLoginDoesNotDistinguishUsernameFromPassword(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const password = "RIGHT-PASSWORD"

	fake := &fakeAuth{username: "alice", password: password, code: "CODE"}
	h, player, store, _ := newLoginHandler(t, fake, &codeStubSource{name: adobotvhttp.Name})
	before := player.src()
	r := loginRouter(h)

	wrongUser := doLogin(t, r, "mallory", password)
	_, wrongUserCode, wrongUserMsg := decodeCode(t, wrongUser)
	wrongPass := doLogin(t, r, "alice", "WRONG-PASSWORD")
	_, wrongPassCode, wrongPassMsg := decodeCode(t, wrongPass)

	if wrongUser.Code != http.StatusUnauthorized || wrongPass.Code != http.StatusUnauthorized {
		t.Fatalf("statuses = %d and %d, want 401 for both", wrongUser.Code, wrongPass.Code)
	}
	if wrongUserCode != codeInvalidCredentials || wrongPassCode != codeInvalidCredentials {
		t.Errorf("codes = %q and %q, want %q for both", wrongUserCode, wrongPassCode, codeInvalidCredentials)
	}
	// Positive: both say the credentials were refused...
	if !strings.Contains(wrongUserMsg, "username or password") {
		t.Errorf("message = %q, want it to name the credentials", wrongUserMsg)
	}
	// ...identically, so neither field can be isolated.
	if wrongUserMsg != wrongPassMsg {
		t.Errorf("messages differ by which field was wrong:\n username: %q\n password: %q", wrongUserMsg, wrongPassMsg)
	}
	// Nothing was persisted and nothing swapped.
	if _, ok, err := store.Load(); err != nil || ok {
		t.Errorf("store = (ok=%v, err=%v), want nothing persisted", ok, err)
	}
	if player.src() != before {
		t.Error("a refused login swapped the source; it must not")
	}
}

// A 403 reCAPTCHA demand is its own outcome: the password may be correct, so it
// must not read as a credential failure.
func TestLoginCaptchaRequiredIsDistinct(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const password = "SUPER-SECRET-PASSWORD-2"

	fake := &fakeAuth{username: "alice", password: password, code: "CODE", recaptcha: true}
	h, player, store, _ := newLoginHandler(t, fake, &codeStubSource{name: adobotvhttp.Name})
	before := player.src()

	w := doLogin(t, loginRouter(h), "alice", password)
	status, code, msg := decodeCode(t, w)

	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", status)
	}
	if code != codeCaptchaRequired {
		t.Errorf("code = %q, want %q", code, codeCaptchaRequired)
	}
	// Positive: the message names the captcha and points at the workaround...
	if !strings.Contains(strings.ToLower(msg), "captcha") {
		t.Errorf("message = %q, want it to name the captcha", msg)
	}
	// ...and is not the credential wording, so the user is not sent to change a
	// working password.
	if strings.Contains(msg, "username or password") {
		t.Errorf("message = %q, want it distinct from a credential failure", msg)
	}
	if _, ok, err := store.Load(); err != nil || ok {
		t.Errorf("store = (ok=%v, err=%v), want nothing persisted", ok, err)
	}
	if player.src() != before {
		t.Error("a captcha refusal swapped the source; it must not")
	}
}

// A login that succeeds but whose profile carries no playlist code fails
// clearly rather than storing an empty credential.
func TestLoginProfileWithoutCodeFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const password = "SUPER-SECRET-PASSWORD-3"

	fake := &fakeAuth{username: "alice", password: password, code: ""}
	h, player, store, _ := newLoginHandler(t, fake, &codeStubSource{name: adobotvhttp.Name})
	before := player.src()

	w := doLogin(t, loginRouter(h), "alice", password)
	status, code, msg := decodeCode(t, w)

	if status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", status)
	}
	if code != codeAccountWithoutPlaylistCode {
		t.Errorf("code = %q, want %q", code, codeAccountWithoutPlaylistCode)
	}
	if !strings.Contains(msg, "playlist code") {
		t.Errorf("message = %q, want it to name the missing playlist code", msg)
	}
	if _, ok, err := store.Load(); err != nil || ok {
		t.Errorf("store = (ok=%v, err=%v), want nothing persisted", ok, err)
	}
	if player.src() != before {
		t.Error("a login with no code swapped the source; it must not")
	}
}

// A transport failure is reported as such and persists nothing.
func TestLoginTransportFailurePersistsNothing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// A server that has been closed refuses the connection: a genuine transport
	// failure.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	h, player, store := newTestSourceHandler(t, source.Config{Name: adobotvhttp.Name}, "")
	h.login = adobotvhttp.NewLoginClient(url, nil).Login
	h.open = func(source.Config) (source.Source, error) { return &codeStubSource{name: adobotvhttp.Name}, nil }
	before := player.src()

	w := doLogin(t, loginRouter(h), "alice", "SUPER-SECRET-PASSWORD-4")
	status, code, msg := decodeCode(t, w)

	if status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", status)
	}
	if code != codeUpstreamError {
		t.Errorf("code = %q, want %q", code, codeUpstreamError)
	}
	if strings.TrimSpace(msg) == "" {
		t.Error("error message is empty; a transport failure must be reported")
	}
	if _, ok, err := store.Load(); err != nil || ok {
		t.Errorf("store = (ok=%v, err=%v), want nothing persisted", ok, err)
	}
	if player.src() != before {
		t.Error("a transport failure swapped the source; it must not")
	}
}

// TestLoginPasswordNeverLeaks is the load-bearing one: across every failure path
// the password appears in no response body and no log line — each paired with a
// positive assertion that the response says what it should.
func TestLoginPasswordNeverLeaks(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name string
		fake *fakeAuth
		// loginSucceeds makes the submitted canary the correct password, so the
		// flow reaches the profile stage; false means login itself is refused.
		loginSucceeds bool
		wantStatus    int
		wantCode      string
		wantPhrase    string
	}{
		{
			name:       "invalid credentials",
			fake:       &fakeAuth{username: "alice", code: "CODE"},
			wantStatus: http.StatusUnauthorized,
			wantCode:   codeInvalidCredentials,
			wantPhrase: "username or password",
		},
		{
			name:       "captcha required",
			fake:       &fakeAuth{username: "alice", code: "CODE", recaptcha: true},
			wantStatus: http.StatusForbidden,
			wantCode:   codeCaptchaRequired,
			wantPhrase: "captcha",
		},
		{
			name:          "profile without a code",
			fake:          &fakeAuth{username: "alice", code: ""},
			loginSucceeds: true,
			wantStatus:    http.StatusBadGateway,
			wantCode:      codeAccountWithoutPlaylistCode,
			wantPhrase:    "playlist code",
		},
		{
			name:          "profile read fails",
			fake:          &fakeAuth{username: "alice", code: "CODE", profileStatus: http.StatusInternalServerError},
			loginSucceeds: true,
			wantStatus:    http.StatusBadGateway,
			wantCode:      codeUpstreamError,
			wantPhrase:    "profile",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A password unique to this case, so a leak from any other path
			// cannot be mistaken for this one's.
			const password = "LEAK-CANARY-PASSWORD-UNIQUE-TO-THIS-CASE"
			if tc.loginSucceeds {
				tc.fake.password = password
			} else {
				tc.fake.password = "a-different-password"
			}

			h, _, _, _ := newLoginHandler(t, tc.fake, &codeStubSource{name: adobotvhttp.Name})

			// Capture the server's own log output for the duration of the call.
			var logBuf bytes.Buffer
			prevOut, prevFlags := log.Writer(), log.Flags()
			log.SetOutput(&logBuf)
			log.SetFlags(0)
			defer func() {
				log.SetOutput(prevOut)
				log.SetFlags(prevFlags)
			}()

			w := doLogin(t, loginRouter(h), "alice", password)
			log.SetOutput(prevOut)
			log.SetFlags(prevFlags)

			status, code, msg := decodeCode(t, w)

			// Positive: the response says what it should, so the absence below
			// is measured against a real message rather than an empty body.
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", status, tc.wantStatus, w.Body.String())
			}
			if code != tc.wantCode {
				t.Errorf("code = %q, want %q", code, tc.wantCode)
			}
			if !strings.Contains(strings.ToLower(msg), strings.ToLower(tc.wantPhrase)) {
				t.Errorf("message = %q, want it to mention %q", msg, tc.wantPhrase)
			}
			// The password really did travel to AdoboTV (these paths all reach
			// it), so its absence below is not vacuous.
			if _, pass := tc.fake.seen(); pass != password {
				t.Errorf("AdoboTV saw password %q, want the submitted one", pass)
			}

			// Negative: the password is nowhere in the response or the logs.
			if strings.Contains(w.Body.String(), password) {
				t.Errorf("the password leaked into the response: %s", w.Body.String())
			}
			if strings.Contains(logBuf.String(), password) {
				t.Errorf("the password leaked into the log: %q", logBuf.String())
			}
			// Every failure is logged (so the log-absence assertion above is
			// measured against real output, not silence).
			if strings.TrimSpace(logBuf.String()) == "" {
				t.Error("no log line was written for a failure; the log-absence check is vacuous")
			}
		})
	}
}

// The transport path has no server to receive a request, so it asserts only that
// the password is absent from the response and the log, with the log proving the
// failure was reported.
func TestLoginTransportPasswordNeverLeaks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const password = "LEAK-CANARY-TRANSPORT"

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	h, _, _ := newTestSourceHandler(t, source.Config{Name: adobotvhttp.Name}, "")
	h.login = adobotvhttp.NewLoginClient(url, nil).Login
	h.open = func(source.Config) (source.Source, error) { return &codeStubSource{name: adobotvhttp.Name}, nil }

	var logBuf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&logBuf)
	log.SetFlags(0)
	w := doLogin(t, loginRouter(h), "alice", password)
	log.SetOutput(prevOut)
	log.SetFlags(prevFlags)

	status, code, _ := decodeCode(t, w)
	if status != http.StatusBadGateway || code != codeUpstreamError {
		t.Errorf("status/code = %d/%q, want 502/%q", status, code, codeUpstreamError)
	}
	if strings.TrimSpace(logBuf.String()) == "" {
		t.Error("a transport failure wrote no log line")
	}
	if strings.Contains(w.Body.String(), password) || strings.Contains(logBuf.String(), password) {
		t.Errorf("the password leaked: response=%q log=%q", w.Body.String(), logBuf.String())
	}
}

// A missing field is a caller error and persists nothing; it is a 400, not an
// upstream 401.
func TestLoginMissingFieldsIsBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeAuth{username: "alice", password: "pw", code: "CODE"}
	h, player, store, _ := newLoginHandler(t, fake, &codeStubSource{name: adobotvhttp.Name})
	before := player.src()
	r := loginRouter(h)

	for _, body := range []string{`{"username":"alice"}`, `{"password":"pw"}`, `{"username":"  ","password":"pw"}`} {
		w := doJSON(t, r, http.MethodPost, "/api/v1/source/login", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, w.Code)
		}
	}
	if _, ok, err := store.Load(); err != nil || ok {
		t.Errorf("store = (ok=%v, err=%v), want nothing persisted", ok, err)
	}
	if player.src() != before {
		t.Error("a malformed request swapped the source; it must not")
	}
}

// A code-taking source that also carries a device gate is persisted by a login
// exactly as by a typed code, so the user never retypes it — the property that
// makes the login path worth having.
func TestLoginDevicePendingPersistsTheCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const password = "SUPER-SECRET-PASSWORD-5"
	const code = "CODE-WITH-PENDING-DEVICE"

	fake := &fakeAuth{username: "alice", password: password, code: code}
	candidate := &codeStubSource{name: adobotvhttp.Name, listErr: fmt.Errorf("%w: splash", adobotvhttp.ErrDevicePending)}
	h, player, store, _ := newLoginHandler(t, fake, candidate)

	w := doLogin(t, loginRouter(h), "alice", password)
	status, gotCode, _ := decodeCode(t, w)

	if status != http.StatusForbidden || gotCode != codeDevicePending {
		t.Fatalf("status/code = %d/%q, want 403/%q", status, gotCode, codeDevicePending)
	}
	stored, ok, err := store.Load()
	if err != nil || !ok || stored != code {
		t.Fatalf("store = (%q, ok=%v, err=%v), want the code kept despite the gate", stored, ok, err)
	}
	if player.src() != source.Source(candidate) {
		t.Error("the gated code was not swapped in; the user would have to retype it")
	}
}
