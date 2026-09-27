package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jmvbambico/adoboflix/internal/source"
	"github.com/jmvbambico/adoboflix/internal/source/adobotvhttp"
	"github.com/jmvbambico/adoboflix/internal/source/file"
	"github.com/jmvbambico/adoboflix/internal/sourcemode"
)

const validJSONPlaylist = `{"channels":[{"id":"news","name":"News One","category":"News"}]}`
const validM3UPlaylist = "#EXTM3U\n#EXTINF:-1 group-title=\"News\",News One\nhttps://cdn.example/news.m3u8\n"

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

// A valid import is parsed by the real adapter, persisted, remembered as the
// mode, and swapped in to serve the imported content.
func TestSetPlaylistFileValidPersistsSwapsAndServes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, player, _ := newTestSourceHandler(t, source.Config{}, "")

	w := doJSON(t, sourceControlRouter(h), http.MethodPost, "/api/v1/source/playlist-file", validJSONPlaylist)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	if !h.files.Exists() {
		t.Fatal("a valid import stored nothing")
	}
	stored, path, ok, err := h.files.Load()
	if err != nil || !ok || string(stored) != validJSONPlaylist {
		t.Fatalf("stored = (%q, %v, %v), want the uploaded bytes", string(stored), ok, err)
	}
	if !strings.HasSuffix(path, ".json") {
		t.Errorf("stored path = %q, want a .json extension", path)
	}

	if player.src().Name() != file.Name {
		t.Errorf("active source = %q, want %q", player.src().Name(), file.Name)
	}
	channels, _, err := player.src().ListChannels("", 10, 0)
	if err != nil {
		t.Fatalf("serving the imported playlist: %v", err)
	}
	if len(channels) != 1 || channels[0].Name != "News One" {
		t.Errorf("channels = %+v, want the one imported channel", channels)
	}

	mode, ok, err := h.modes.Load()
	if err != nil || !ok || mode != file.Name {
		t.Errorf("stored mode = (%q, %v, %v), want %q", mode, ok, err, file.Name)
	}

	body := decodeBody(t, w)
	if body["source"] != file.Name || body["active"] != true || body["origin"] != sourcemode.OriginStored {
		t.Errorf("response status = %v, want the file mode from the stored choice", body)
	}
	if body["playlist_file_configured"] != true {
		t.Errorf("playlist_file_configured = %v, want true", body["playlist_file_configured"])
	}
}

// The adapter's M3U format is accepted too, and stored under an extension its
// parser selector reads as M3U.
func TestSetPlaylistFileAcceptsM3U(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, player, _ := newTestSourceHandler(t, source.Config{}, "")

	w := doJSON(t, sourceControlRouter(h), http.MethodPost, "/api/v1/source/playlist-file", validM3UPlaylist)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	path := h.files.Path()
	if !strings.HasSuffix(path, ".m3u") {
		t.Errorf("stored path = %q, want a .m3u extension", path)
	}
	channels, _, err := player.src().ListChannels("", 10, 0)
	if err != nil {
		t.Fatalf("serving the imported M3U: %v", err)
	}
	if len(channels) != 1 || channels[0].Name != "News One" {
		t.Errorf("channels = %+v, want the one imported channel", channels)
	}
}

// A malformed upload persists nothing and swaps nothing, and its rejection
// names what the parser found wrong rather than the bare fact of failure.
func TestSetPlaylistFileInvalidPersistsNothingSwapsNothing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, player, _ := newTestSourceHandler(t, source.Config{}, "")
	before := player.src()

	w := doJSON(t, sourceControlRouter(h), http.MethodPost, "/api/v1/source/playlist-file", `{"channels": [`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	if body["code"] != codeInvalidPlaylist {
		t.Errorf("code = %v, want %q", body["code"], codeInvalidPlaylist)
	}
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, "JSON") {
		t.Errorf("error = %q, want it to name the format the parser tried", msg)
	}
	if !strings.Contains(msg, "docs/source-adapters.md") {
		t.Errorf("error = %q, want a pointer to the documented format", msg)
	}
	if strings.TrimSpace(msg) == "invalid playlist" {
		t.Errorf("error = %q, want the parser's reason, not a bare rejection", msg)
	}

	if h.files.Exists() {
		t.Error("an invalid import persisted a playlist")
	}
	if player.src() != before {
		t.Error("an invalid import swapped the live source")
	}
	if _, ok, _ := h.modes.Load(); ok {
		t.Error("an invalid import remembered a mode")
	}
}

// An empty body is rejected the same way, with nothing persisted.
func TestSetPlaylistFileRejectsEmptyBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, player, _ := newTestSourceHandler(t, source.Config{}, "")
	before := player.src()

	w := doJSON(t, sourceControlRouter(h), http.MethodPost, "/api/v1/source/playlist-file", "   \n")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if decodeBody(t, w)["code"] != codeInvalidPlaylist {
		t.Errorf("body = %s, want code %q", w.Body.String(), codeInvalidPlaylist)
	}
	if h.files.Exists() || player.src() != before {
		t.Error("an empty import touched the store or the source")
	}
}

// An upload beyond the cap is rejected with its own code, before any parse.
func TestSetPlaylistFileOversizedBodyRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	original := maxPlaylistImportBytes
	maxPlaylistImportBytes = 64
	t.Cleanup(func() { maxPlaylistImportBytes = original })

	h, player, _ := newTestSourceHandler(t, source.Config{}, "")
	before := player.src()
	h.open = func(source.Config) (source.Source, error) {
		t.Error("an oversized body must not reach the parser")
		return nil, nil
	}

	body := `{"channels":["` + strings.Repeat("a", 256) + `"]}`
	w := doJSON(t, sourceControlRouter(h), http.MethodPost, "/api/v1/source/playlist-file", body)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
	if got := decodeBody(t, w)["code"]; got != codePlaylistTooLarge {
		t.Errorf("code = %v, want %q", got, codePlaylistTooLarge)
	}
	if h.files.Exists() || player.src() != before {
		t.Error("an oversized import touched the store or the source")
	}
}

// DELETE clears the imported playlist and, when it was the active source,
// returns the server to the sourceless state the UI offers choices from.
func TestDeletePlaylistFileClearsAndReturnsSourceless(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, player, _ := newTestSourceHandler(t, source.Config{}, "")

	if w := doJSON(t, sourceControlRouter(h), http.MethodPost, "/api/v1/source/playlist-file", validJSONPlaylist); w.Code != http.StatusOK {
		t.Fatalf("import status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if player.src().Name() != file.Name {
		t.Fatalf("precondition: active source = %q, want %q", player.src().Name(), file.Name)
	}

	w := doJSON(t, sourceControlRouter(h), http.MethodDelete, "/api/v1/source/playlist-file", "")
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if h.files.Exists() {
		t.Error("DELETE left the playlist stored")
	}
	if player.src().Name() != "" {
		t.Errorf("active source = %q, want none after DELETE", player.src().Name())
	}
	if _, ok, _ := h.modes.Load(); ok {
		t.Error("DELETE left a mode that points at a playlist that is gone")
	}
	body := decodeBody(t, w)
	if body["active"] != false || body["origin"] != sourcemode.OriginNone {
		t.Errorf("status after DELETE = %v, want the sourceless state", body)
	}
}

// DELETE leaves an env-pinned file source running: it reads its own path, not
// the imported one.
func TestDeletePlaylistFileLeavesPinnedSourceRunning(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, player, _ := newTestSourceHandler(t, source.Config{Name: file.Name}, "")
	h.envSource = file.Name
	before := player.src()
	if _, _, err := h.files.Save([]byte(validJSONPlaylist)); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	w := doJSON(t, sourceControlRouter(h), http.MethodDelete, "/api/v1/source/playlist-file", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if h.files.Exists() {
		t.Error("DELETE left the playlist stored")
	}
	if player.src() != before {
		t.Error("DELETE swapped an env-pinned source")
	}
}

// A pinned environment source cannot be replaced by an import.
func TestSetPlaylistFileRefusedWhenPinned(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, player, _ := newTestSourceHandler(t, source.Config{Name: adobotvhttp.Name}, "")
	h.envSource = adobotvhttp.Name
	before := player.src()
	h.open = func(source.Config) (source.Source, error) {
		t.Error("a pinned source must not open a candidate")
		return nil, nil
	}

	w := doJSON(t, sourceControlRouter(h), http.MethodPost, "/api/v1/source/playlist-file", validJSONPlaylist)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", w.Code, w.Body.String())
	}
	if got := decodeBody(t, w)["code"]; got != codeSourcePinnedByEnv {
		t.Errorf("code = %v, want %q", got, codeSourcePinnedByEnv)
	}
	if h.files.Exists() || player.src() != before {
		t.Error("a pinned source persisted or swapped an import")
	}
}

// unreadableModeStore returns a mode store whose parent path is a regular file,
// so both Save and Clear fail — how a test forces a compensation or ordering
// branch without a seam in the concrete store.
func unreadableModeStore(t *testing.T) *sourcemode.Store {
	t.Helper()
	notDir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(notDir, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed a file where a directory is expected: %v", err)
	}
	return sourcemode.New(filepath.Join(notDir, "source-mode"))
}

// A rejection of content that matches neither format must not name JSON: the
// adapter guessed JSON to pick a parser, but a bare list of URLs is not JSON,
// and blaming JSON would send the user to fix the wrong thing.
func TestSetPlaylistFileBareURLListRejectedWithoutBlamingJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, player, _ := newTestSourceHandler(t, source.Config{}, "")
	before := player.src()

	const bareList = "https://cdn.example/one.m3u8\nhttps://cdn.example/two.m3u8\n"
	w := doJSON(t, sourceControlRouter(h), http.MethodPost, "/api/v1/source/playlist-file", bareList)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	if body["code"] != codeInvalidPlaylist {
		t.Errorf("code = %v, want %q", body["code"], codeInvalidPlaylist)
	}
	msg, _ := body["error"].(string)
	// Positive: the message names both formats and still points at the docs.
	if !strings.Contains(msg, "did not match") || !strings.Contains(msg, "M3U") {
		t.Errorf("error = %q, want it to say the content matched neither format", msg)
	}
	if !strings.Contains(msg, "docs/source-adapters.md") {
		t.Errorf("error = %q, want a pointer to the documented format", msg)
	}
	// Negative: it does not claim JSON was the format.
	if strings.Contains(msg, "as JSON") {
		t.Errorf("error = %q, must not blame JSON for content that is not JSON", msg)
	}
	if h.files.Exists() || player.src() != before {
		t.Error("a rejected bare list touched the store or the source")
	}
}

// A failure to open the just-saved playlist removes it, and must also release a
// remembered file mode, or the next boot would point at a playlist that is gone.
func TestSetPlaylistFileUnopenableStoredPlaylistReleasesTheFileMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, player, _ := newTestSourceHandler(t, source.Config{Name: file.Name}, "")
	// The user is already on the file mode (a re-import), so a stranded mode
	// would point at the playlist this failure removes.
	if err := h.modes.Save(file.Name); err != nil {
		t.Fatalf("seed stored mode: %v", err)
	}
	if player.src().Name() != file.Name {
		t.Fatalf("precondition: live source = %q, want %q", player.src().Name(), file.Name)
	}

	calls := 0
	h.open = func(source.Config) (source.Source, error) {
		calls++
		if calls == 1 {
			return &codeStubSource{name: file.Name}, nil // the scratch copy validates
		}
		return nil, errors.New("the final path is unreadable")
	}

	w := doJSON(t, sourceControlRouter(h), http.MethodPost, "/api/v1/source/playlist-file", validJSONPlaylist)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", w.Code, w.Body.String())
	}
	if h.files.Exists() {
		t.Error("the unopenable playlist was left stored")
	}
	if mode, ok, _ := h.modes.Load(); ok {
		t.Errorf("mode = %q, want it released with the playlist it pointed at", mode)
	}

	// The live source must stop claiming to serve the playlist just removed, so
	// status cannot report an active source whose backing file is gone.
	if player.src().Name() != "" {
		t.Errorf("live source = %q, want it stopped when its playlist was removed", player.src().Name())
	}
	body := statusBody(t, h)
	if body["active"] != false || body["source"] != "" || body["playlist_file_configured"] != false {
		t.Errorf("status = %v, want the sourceless state after the playlist was removed", body)
	}
}

// A failure to save the mode must leave the stored playlist in place: the file
// is valid and self-consistent, and removing it would strand a remembered file
// mode on a playlist that is gone.
func TestSetPlaylistFileModeSaveFailureKeepsTheStoredPlaylist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, player, _ := newTestSourceHandler(t, source.Config{Name: file.Name}, "")
	before := player.src()
	h.modes = unreadableModeStore(t)

	w := doJSON(t, sourceControlRouter(h), http.MethodPost, "/api/v1/source/playlist-file", validJSONPlaylist)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", w.Code, w.Body.String())
	}
	if got := decodeBody(t, w)["error"]; got != "could not save the source mode" {
		t.Errorf("error = %v, want the mode-save failure", got)
	}
	if !h.files.Exists() {
		t.Error("a mode-save failure removed the valid stored playlist")
	}
	if player.src() != before {
		t.Error("a failed import swapped the live source")
	}
}

// DELETE releases the mode before removing the file, so a mode-clear failure
// leaves the playlist the mode still points at, rather than stranding the boot.
func TestDeletePlaylistFileModeClearFailureLeavesThePlaylist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, player, _ := newTestSourceHandler(t, source.Config{Name: file.Name}, "")
	if w := doJSON(t, sourceControlRouter(h), http.MethodPost, "/api/v1/source/playlist-file", validJSONPlaylist); w.Code != http.StatusOK {
		t.Fatalf("import status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if player.src().Name() != file.Name {
		t.Fatalf("precondition: active source = %q, want %q", player.src().Name(), file.Name)
	}
	h.modes = unreadableModeStore(t)

	w := doJSON(t, sourceControlRouter(h), http.MethodDelete, "/api/v1/source/playlist-file", "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("DELETE status = %d, want 500: %s", w.Code, w.Body.String())
	}
	if got := decodeBody(t, w)["error"]; got != "could not clear the source mode" {
		t.Errorf("error = %v, want the mode-clear failure", got)
	}
	// Positive: the playlist the (uncleared) mode points at is still there.
	if !h.files.Exists() {
		t.Error("a mode-clear failure removed the playlist, stranding the mode")
	}
	if player.src().Name() != file.Name {
		t.Errorf("active source = %q, want it left running", player.src().Name())
	}
}
