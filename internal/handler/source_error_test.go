package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jmvbambico/adoboflix/internal/source"
	"github.com/jmvbambico/adoboflix/internal/source/adobotvhttp"
)

// TestSourceErrorStatus pins one case per sentinel: the status code the client
// sees and the stable code string it branches on, plus an unknown error.
func TestSourceErrorStatus(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantHTTP int
		wantCode string
	}{
		{"device pending", fmt.Errorf("%w: splash body", adobotvhttp.ErrDevicePending), http.StatusForbidden, codeDevicePending},
		{"subscription inactive", fmt.Errorf("%w: status expired", adobotvhttp.ErrSubscriptionInactive), http.StatusForbidden, codeSubscriptionInactive},
		{"playlist rejected", fmt.Errorf("%w: HTTP 403", adobotvhttp.ErrPlaylistRejected), http.StatusForbidden, codePlaylistRejected},
		{"user-agent rejected", fmt.Errorf("%w: prefix", adobotvhttp.ErrUserAgentRejected), http.StatusForbidden, codeUserAgentRejected},
		{"playlist format m3u", fmt.Errorf("%w: KODIPROP", adobotvhttp.ErrPlaylistFormatM3U), http.StatusBadGateway, codePlaylistFormatM3U},
		{"content not found", fmt.Errorf("%w: opaque id", adobotvhttp.ErrContentNotFound), http.StatusNotFound, codeContentNotFound},
		{"sql no rows", fmt.Errorf("get vod asset x: %w", sql.ErrNoRows), http.StatusNotFound, codeNotFound},
		{"malformed envelope", fmt.Errorf("%w: not json", adobotvhttp.ErrMalformedEnvelope), http.StatusBadGateway, codeMalformedPlaylist},
		{"malformed drm", fmt.Errorf("%w: not base64", adobotvhttp.ErrMalformedDRM), http.StatusBadGateway, codeMalformedDRM},
		{"malformed vod library", fmt.Errorf("%w: not an array", adobotvhttp.ErrMalformedVODLibrary), http.StatusBadGateway, codeMalformedVODLibrary},
		{"content token rejected", fmt.Errorf("%w: dead token", adobotvhttp.ErrTokenRejected), http.StatusBadGateway, codeTokenRejected},
		{"upstream transport", fmt.Errorf("%w: connection refused", adobotvhttp.ErrUpstream), http.StatusBadGateway, codeUpstreamError},
		{"unknown internal fault", errors.New("boom"), http.StatusInternalServerError, codeInternalError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotHTTP, gotCode := sourceErrorStatus(tc.err)
			if gotHTTP != tc.wantHTTP {
				t.Errorf("status = %d, want %d", gotHTTP, tc.wantHTTP)
			}
			if gotCode != tc.wantCode {
				t.Errorf("code = %q, want %q", gotCode, tc.wantCode)
			}
		})
	}
}

// TestWriteSourceErrorKeepsMessageAndAddsCode pins the additive contract: the
// "error" field keeps the error's exact text and the code field rides along.
func TestWriteSourceErrorKeepsMessageAndAddsCode(t *testing.T) {
	gin.SetMode(gin.TestMode)

	err := fmt.Errorf("%w: this device is in the operator's device-approval queue. Ask the AdoboTV operator to approve this device, then reload.", adobotvhttp.ErrDevicePending)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	writeSourceError(c, err, "")

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error != err.Error() {
		t.Errorf("error = %q, want the unchanged error text %q", body.Error, err.Error())
	}
	if body.Code != codeDevicePending {
		t.Errorf("code = %q, want %q", body.Code, codeDevicePending)
	}
}

// TestWriteSourceErrorNotFoundWording pins that an ordinary lookup miss keeps
// the endpoint's existing message ("postgres-direct behavior identical") while
// a real gate still wins over that wording.
func TestWriteSourceErrorNotFoundWording(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("miss keeps endpoint wording", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		writeSourceError(c, fmt.Errorf("get vod asset x: %w", sql.ErrNoRows), "Entry not found")

		var body struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
		if body.Error != "Entry not found" {
			t.Errorf("error = %q, want %q", body.Error, "Entry not found")
		}
		if body.Code != codeNotFound {
			t.Errorf("code = %q, want %q", body.Code, codeNotFound)
		}
	})

	t.Run("gate beats endpoint wording", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		writeSourceError(c, fmt.Errorf("%w: pending", adobotvhttp.ErrDevicePending), "Entry not found")

		var body struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", w.Code)
		}
		if body.Code != codeDevicePending {
			t.Errorf("code = %q, want %q", body.Code, codeDevicePending)
		}
	})
}

// stubSource satisfies source.Source by embedding the interface; tests override
// only the methods they exercise. Any unoverridden method panics, which is what
// we want if a test drifts from what it claims to test.
type stubSource struct {
	source.Source
	err error
}

func (s *stubSource) Name() string { return "stub" }

func (s *stubSource) GetStats() (*source.Stats, error) { return nil, s.err }

func (s *stubSource) ListChannels(category string, limit, offset int) ([]source.Channel, int, error) {
	return nil, 0, s.err
}

func (s *stubSource) GetEntry(id string) (*source.Entry, error) { return nil, s.err }

// TestHandlerBoundaryMapsGateTo403 proves every Source-backed endpoint routes
// through the helper: a pending device is a 403 on the blanket-500 stats path,
// the channels path, and the lookup path alike, and never a 500.
func TestHandlerBoundaryMapsGateTo403(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewPlayerHandler(&stubSource{err: fmt.Errorf("%w: pending", adobotvhttp.ErrDevicePending)})
	r := gin.New()
	r.GET("/stats", h.GetStats)
	r.GET("/channels", h.ListChannels)
	r.GET("/entry/:id", h.GetEntry)

	for _, path := range []string{"/stats", "/channels", "/entry/abc"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))

		if w.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", path, w.Code)
		}
		var body struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: decode body: %v", path, err)
		}
		if body.Code != codeDevicePending {
			t.Errorf("%s: code = %q, want %q", path, body.Code, codeDevicePending)
		}
	}
}

// TestHandlerBoundaryKeepsInternalFaultAt500 proves a genuine, unclassified
// source failure is still a 500 — the mapping does not whitewash real faults.
func TestHandlerBoundaryKeepsInternalFaultAt500(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewPlayerHandler(&stubSource{err: errors.New("database is on fire")})
	r := gin.New()
	r.GET("/stats", h.GetStats)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/stats", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}
