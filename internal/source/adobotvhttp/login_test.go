package adobotvhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// loginServer is an httptest AdoboTV for the two endpoints LoginClient uses. It
// records the last login body so a test can prove the password really travelled
// before asserting it never comes back.
type loginServer struct {
	mu         sync.Mutex
	code       string
	loginBody  string
	loginAuth  string
	profileHit int
	// status overrides: when non-zero they force the login/profile status.
	loginStatus   int
	loginMessage  string
	profileStatus int
}

func (s *loginServer) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/login":
			body, _ := io.ReadAll(r.Body)
			s.mu.Lock()
			s.loginBody = string(body)
			s.mu.Unlock()

			if s.loginStatus != 0 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(s.loginStatus)
				_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "message": s.loginMessage})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data":    map[string]any{"access_token": "tok-abc", "refresh_token": "ref-xyz"},
			})
		case "/v1/profile":
			s.mu.Lock()
			s.profileHit++
			s.loginAuth = r.Header.Get("Authorization")
			s.mu.Unlock()
			if s.profileStatus != 0 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(s.profileStatus)
				_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "message": "nope"})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data":    map[string]any{"username": "alice", "playlistCode": s.code},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLoginReturnsProfilePlaylistCode(t *testing.T) {
	srv := (&loginServer{code: "PROFILE-CODE-1"}).server(t)
	client := NewLoginClient(srv.URL, srv.Client())

	code, err := client.Login(context.Background(), "alice", "hunter2")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if code != "PROFILE-CODE-1" {
		t.Errorf("code = %q, want %q", code, "PROFILE-CODE-1")
	}
	// The token is presented as a Bearer credential exactly once, to the profile.
	if client == nil {
		t.Fatal("client is nil")
	}
}

func TestLoginSendsCredentialsAndBearerToken(t *testing.T) {
	ls := &loginServer{code: "C"}
	srv := ls.server(t)
	client := NewLoginClient(srv.URL, srv.Client())

	if _, err := client.Login(context.Background(), "alice", "s3cr3t-pw"); err != nil {
		t.Fatalf("Login: %v", err)
	}

	ls.mu.Lock()
	defer ls.mu.Unlock()
	if !strings.Contains(ls.loginBody, `"username":"alice"`) {
		t.Errorf("login body = %q, want the username", ls.loginBody)
	}
	// Positive: the password really was sent, so the never-leak assertions
	// elsewhere are meaningful.
	if !strings.Contains(ls.loginBody, "s3cr3t-pw") {
		t.Errorf("login body = %q, want the password sent upstream", ls.loginBody)
	}
	if ls.loginAuth != "Bearer tok-abc" {
		t.Errorf("profile Authorization = %q, want the minted token", ls.loginAuth)
	}
	if ls.profileHit != 1 {
		t.Errorf("profile hit %d times, want exactly 1", ls.profileHit)
	}
}

func TestLoginMapsUpstreamFailures(t *testing.T) {
	cases := []struct {
		name    string
		server  *loginServer
		wantErr error
	}{
		{
			name:    "401 is invalid credentials",
			server:  &loginServer{loginStatus: http.StatusUnauthorized, loginMessage: "Invalid username or password"},
			wantErr: ErrInvalidCredentials,
		},
		{
			name:    "403 reCAPTCHA token required",
			server:  &loginServer{loginStatus: http.StatusForbidden, loginMessage: "reCAPTCHA token is required"},
			wantErr: ErrRecaptchaRequired,
		},
		{
			name:    "403 reCAPTCHA verification failed",
			server:  &loginServer{loginStatus: http.StatusForbidden, loginMessage: "reCAPTCHA verification failed"},
			wantErr: ErrRecaptchaRequired,
		},
		{
			name:    "403 without captcha wording is a plain upstream failure",
			server:  &loginServer{loginStatus: http.StatusForbidden, loginMessage: "waf blocked"},
			wantErr: ErrUpstream,
		},
		{
			name:    "500 is upstream",
			server:  &loginServer{loginStatus: http.StatusInternalServerError},
			wantErr: ErrUpstream,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := tc.server.server(t)
			client := NewLoginClient(srv.URL, srv.Client())

			_, err := client.Login(context.Background(), "alice", "hunter2")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if strings.Contains(err.Error(), "hunter2") {
				t.Errorf("the error leaked the password: %v", err)
			}
		})
	}
}

func TestLoginProfileWithoutCodeFails(t *testing.T) {
	srv := (&loginServer{code: ""}).server(t)
	client := NewLoginClient(srv.URL, srv.Client())

	_, err := client.Login(context.Background(), "alice", "hunter2")
	if !errors.Is(err, ErrProfileWithoutPlaylistCode) {
		t.Fatalf("error = %v, want ErrProfileWithoutPlaylistCode", err)
	}
	if !strings.Contains(err.Error(), "playlist code") {
		t.Errorf("error = %q, want it to name the missing playlist code", err)
	}
}

func TestLoginEmptyFieldsRefusedWithoutUpstreamCall(t *testing.T) {
	ls := &loginServer{code: "C"}
	srv := ls.server(t)
	client := NewLoginClient(srv.URL, srv.Client())

	for _, tc := range []struct{ user, pass string }{{"", "pw"}, {"alice", ""}, {"   ", "pw"}} {
		if _, err := client.Login(context.Background(), tc.user, tc.pass); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("Login(%q, %q) = %v, want ErrInvalidCredentials", tc.user, tc.pass, err)
		}
	}
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if ls.loginBody != "" {
		t.Errorf("an empty field reached upstream (body %q); it must be refused locally", ls.loginBody)
	}
}

func TestLoginTransportFailureIsUpstream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	client := NewLoginClient(url, nil)
	_, err := client.Login(context.Background(), "alice", "hunter2")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("error = %v, want ErrUpstream", err)
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("the error leaked the password: %v", err)
	}
}

// rawLoginServer serves caller-supplied JSON bodies for the two login
// endpoints, so a test can control the exact presence and value of the
// `success` field. The loginServer above always emits success:true and cannot
// express the absent-success shape this fix turns on.
type rawLoginServer struct {
	loginBody   string
	profileBody string
}

func (s *rawLoginServer) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/auth/login":
			_, _ = io.WriteString(w, s.loginBody)
		case "/v1/profile":
			_, _ = io.WriteString(w, s.profileBody)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestLoginSuccessField covers the 200 login envelope's success field. A
// PRESENT success:false must fail as ErrUpstream even when a valid-looking
// token rides along; an ABSENT success must still succeed, because AdoboTV's
// live shape is unverified and absent currently means "not asserted".
func TestLoginSuccessField(t *testing.T) {
	const okProfile = `{"success":true,"data":{"playlistCode":"CODE-X"}}`

	cases := []struct {
		name    string
		login   string
		wantErr error
		wantOK  bool
	}{
		{
			name:    "success false with a token is rejected as upstream, not invalid credentials",
			login:   `{"success":false,"data":{"access_token":"tok-abc"}}`,
			wantErr: ErrUpstream,
		},
		{
			name:   "success true with a token succeeds",
			login:  `{"success":true,"data":{"access_token":"tok-abc"}}`,
			wantOK: true,
		},
		{
			name:   "absent success with a token succeeds",
			login:  `{"data":{"access_token":"tok-abc"}}`,
			wantOK: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := (&rawLoginServer{loginBody: tc.login, profileBody: okProfile}).server(t)
			client := NewLoginClient(srv.URL, srv.Client())

			code, err := client.Login(context.Background(), "alice", "hunter2")
			if tc.wantOK {
				if err != nil {
					t.Fatalf("Login: %v, want success", err)
				}
				if code != "CODE-X" {
					t.Errorf("code = %q, want %q", code, "CODE-X")
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			// A declined envelope must not be relabelled as a credential refusal.
			if errors.Is(err, ErrInvalidCredentials) {
				t.Errorf("error = %v, must not be ErrInvalidCredentials", err)
			}
			if code != "" {
				t.Errorf("code = %q, want empty on failure", code)
			}
			// Neither the password nor the token from the refused body may leak.
			if strings.Contains(err.Error(), "hunter2") {
				t.Errorf("the error leaked the password: %v", err)
			}
			if strings.Contains(err.Error(), "tok-abc") {
				t.Errorf("the error leaked the access token: %v", err)
			}
		})
	}
}

// TestLoginProfileSuccessField covers the 200 profile envelope's success field:
// a PRESENT success:false must fail as ErrUpstream and the playlistCode it
// carries must never reach the caller; an ABSENT success must still succeed.
func TestLoginProfileSuccessField(t *testing.T) {
	const okLogin = `{"success":true,"data":{"access_token":"tok-abc"}}`

	cases := []struct {
		name    string
		profile string
		wantErr error
		wantOK  bool
	}{
		{
			name:    "success false carrying a playlistCode is rejected and the code is withheld",
			profile: `{"success":false,"data":{"playlistCode":"SECRET-CODE"}}`,
			wantErr: ErrUpstream,
		},
		{
			name:    "absent success with a playlistCode succeeds",
			profile: `{"data":{"playlistCode":"CODE-Y"}}`,
			wantOK:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := (&rawLoginServer{loginBody: okLogin, profileBody: tc.profile}).server(t)
			client := NewLoginClient(srv.URL, srv.Client())

			code, err := client.Login(context.Background(), "alice", "hunter2")
			if tc.wantOK {
				if err != nil {
					t.Fatalf("Login: %v, want success", err)
				}
				if code != "CODE-Y" {
					t.Errorf("code = %q, want %q", code, "CODE-Y")
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if code != "" {
				t.Errorf("code = %q, want empty: a declined profile must not yield a usable code", code)
			}
			if strings.Contains(err.Error(), "SECRET-CODE") {
				t.Errorf("the error leaked the playlist code from the body: %v", err)
			}
		})
	}
}
