package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jmvbambico/adoboflix/internal/handler"
	"github.com/jmvbambico/adoboflix/internal/playlistcode"
	"github.com/jmvbambico/adoboflix/internal/playlistfile"
	"github.com/jmvbambico/adoboflix/internal/source"
	"github.com/jmvbambico/adoboflix/internal/source/file"
	"github.com/jmvbambico/adoboflix/internal/sourcemode"
)

const bootTestPlaylist = `{"channels":[{"id":"news","name":"News One","category":"News"}]}`

func bootTestRequest(t *testing.T, r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// A stored mode pointing at a playlist that is not there must not stop the
// server booting: it is user state the chooser repairs. This drives the real
// boot selection, the real status endpoint and a real import, so it asserts the
// server survives rather than that one helper returns an error.
func TestBootFallsBackToSourcelessWhenStoredModeCannotOpen(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// The file adapter must not pick up a real path from the environment.
	t.Setenv(file.EnvPath, "")

	dir := t.TempDir()
	modeStore := sourcemode.New(filepath.Join(dir, "source-mode"))
	fileStore := playlistfile.New(filepath.Join(dir, "playlist"))
	codeStore := playlistcode.New(filepath.Join(dir, "playlist-code"))

	// The stranded state: the mode says "file" but no playlist was ever stored.
	if err := modeStore.Save(file.Name); err != nil {
		t.Fatalf("seed stored mode: %v", err)
	}
	resolution, err := modeStore.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolution.Mode != file.Name || resolution.Origin != sourcemode.OriginStored {
		t.Fatalf("resolution = %+v, want the stored file mode", resolution)
	}

	cfg := source.Config{Name: resolution.Mode}
	if p := fileStore.Path(); p != "" {
		cfg.FilePath = p
	}

	// The boot must survive, ending sourceless rather than exiting.
	src, err := openBootSource(cfg, resolution.Origin == sourcemode.OriginEnv, source.Open)
	if err != nil {
		t.Fatalf("openBootSource = %v, want the boot to survive a stranded mode", err)
	}
	if src.Name() != "" {
		t.Fatalf("source = %q, want sourceless after the fallback", src.Name())
	}

	// Wire the handlers exactly as main does, so the assertions below are about
	// the running server, not about the boot helper alone.
	player := handler.NewPlayerHandler(src)
	h := handler.NewSourceHandler(handler.SourceHandlerOptions{
		Player:    player,
		Config:    source.Config{},
		CodeStore: codeStore,
		ModeStore: modeStore,
		FileStore: fileStore,
	})
	r := gin.New()
	r.GET("/api/v1/source/status", h.GetStatus)
	r.POST("/api/v1/source/playlist-file", h.SetPlaylistFile)

	w := bootTestRequest(t, r, http.MethodGet, "/api/v1/source/status", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var status map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status["active"] != false || status["source"] != "" || status["origin"] != sourcemode.OriginNone {
		t.Fatalf("status = %v, want the sourceless state after the fallback", status)
	}

	// From that recovered state an import must work: nothing had to be deleted
	// by hand to get here.
	w = bootTestRequest(t, r, http.MethodPost, "/api/v1/source/playlist-file", bootTestPlaylist)
	if w.Code != http.StatusOK {
		t.Fatalf("import after fallback = %d, want 200: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode import status: %v", err)
	}
	if status["source"] != file.Name || status["active"] != true {
		t.Errorf("status after import = %v, want the file mode active", status)
	}
}

// An env-pinned source that will not open is operator configuration and must
// still be fatal to the boot; the fallback must not swallow it. Paired with the
// same failure in a stored mode, which falls back instead.
func TestOpenBootSourceKeepsEnvPinnedFailureFatal(t *testing.T) {
	sentinel := errors.New("operator misconfiguration")
	failing := func(source.Config) (source.Source, error) { return nil, sentinel }

	if _, err := openBootSource(source.Config{Name: file.Name}, true, failing); !errors.Is(err, sentinel) {
		t.Fatalf("openBootSource(pinned) = %v, want the env-pinned failure returned", err)
	}

	src, err := openBootSource(source.Config{Name: file.Name}, false, failing)
	if err != nil {
		t.Fatalf("openBootSource(stored) = %v, want a fallback", err)
	}
	if src.Name() != "" {
		t.Fatalf("source = %q, want sourceless", src.Name())
	}
}
