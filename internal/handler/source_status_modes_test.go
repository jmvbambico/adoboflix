package handler

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/jmvbambico/adoboflix/internal/source"
	"github.com/jmvbambico/adoboflix/internal/source/adobotvhttp"
	"github.com/jmvbambico/adoboflix/internal/source/file"
	"github.com/jmvbambico/adoboflix/internal/sourcemode"
)

// modeEntry pulls the status's modes list entry for the named adapter, failing
// the test when it is absent — so a test asserting a flag cannot pass
// vacuously on a mode that was never reported.
func modeEntry(t *testing.T, body map[string]any, name string) map[string]any {
	t.Helper()
	modes, ok := body["modes"].([]any)
	if !ok {
		t.Fatalf("modes = %#v, want a list", body["modes"])
	}
	for _, m := range modes {
		entry, _ := m.(map[string]any)
		if entry["name"] == name {
			return entry
		}
	}
	t.Fatalf("mode %q absent from %v", name, modes)
	return nil
}

// The sourceless state reports itself and still offers the two real modes, so
// the UI can render the choice without hardcoding adapter names.
func TestSourceStatusSourcelessOffersTwoChoices(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _, _ := newTestSourceHandler(t, source.Config{}, "")

	body := statusBody(t, h)
	if body["source"] != "" || body["active"] != false || body["origin"] != sourcemode.OriginNone {
		t.Errorf("sourceless status = %v, want source=\"\" active=false origin=%q", body, sourcemode.OriginNone)
	}

	login := modeEntry(t, body, adobotvhttp.Name)
	if login["selectable"] != true || login["dev"] != false || login["needs_playlist_code"] != true {
		t.Errorf("adobotv-http mode = %v, want selectable, non-dev, needs a code", login)
	}
	if login["configured"] != false {
		t.Errorf("adobotv-http configured = %v, want false with no code", login["configured"])
	}

	local := modeEntry(t, body, file.Name)
	if local["selectable"] != true || local["dev"] != false || local["needs_playlist_code"] != false {
		t.Errorf("file mode = %v, want selectable, non-dev, no code", local)
	}
}

// A source pinned by the environment reports origin=env, which is how the UI
// knows the choice is not its to make.
func TestSourceStatusEnvPinnedReportsOriginEnv(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _, _ := newTestSourceHandler(t, source.Config{Name: file.Name}, "")
	h.envSource = file.Name
	h.envFile = "/somewhere/playlist.json"

	body := statusBody(t, h)
	if body["source"] != file.Name || body["origin"] != sourcemode.OriginEnv || body["active"] != true {
		t.Errorf("status = %v, want the env-pinned file source", body)
	}
	if body["playlist_file_configured"] != true {
		t.Errorf("playlist_file_configured = %v, want true from ADOBOFLIX_FILE_PATH", body["playlist_file_configured"])
	}
	entry := modeEntry(t, body, file.Name)
	if entry["configured"] != true || entry["active"] != true {
		t.Errorf("file mode = %v, want configured and active", entry)
	}
}

// A development harness is reported as dev and not selectable, so the UI can
// omit it without matching its name.
func TestSourceStatusDevAdapterFlaggedNotSelectable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const devName = "test-dev-status-adapter"
	source.Register(devName, source.Requirement{Dev: true}, func(source.Config) (source.Source, error) {
		return &codeStubSource{name: devName}, nil
	})

	h, _, _ := newTestSourceHandler(t, source.Config{Name: devName}, "")
	body := statusBody(t, h)
	if body["dev"] != true {
		t.Errorf("dev = %v, want true for a dev harness", body["dev"])
	}
	entry := modeEntry(t, body, devName)
	if entry["dev"] != true || entry["selectable"] != false {
		t.Errorf("dev mode entry = %v, want dev=true selectable=false", entry)
	}
}

// An imported playlist reports when it was imported, from the stored file's own
// mtime. The field is pair-tested against its absence: with nothing imported it
// must be omitted entirely, never sent as a zero time.
func TestSourceStatusReportsPlaylistImportedAtOnlyForAnImport(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _, _ := newTestSourceHandler(t, source.Config{Name: file.Name}, "")

	body := statusBody(t, h)
	if _, present := body["playlist_imported_at"]; present {
		t.Errorf("playlist_imported_at = %v, want it absent with nothing imported", body["playlist_imported_at"])
	}

	if _, _, err := h.files.Save([]byte(`{"entries":[]}`)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	body = statusBody(t, h)
	raw, ok := body["playlist_imported_at"].(string)
	if !ok || raw == "" {
		t.Fatalf("playlist_imported_at = %v, want an RFC3339 string after an import", body["playlist_imported_at"])
	}
	if _, err := time.Parse(time.RFC3339, raw); err != nil {
		t.Errorf("playlist_imported_at = %q, want RFC3339: %v", raw, err)
	}
}

// A playlist supplied through ADOBOFLIX_FILE_PATH was not imported here, so it
// carries no import time — the field is omitted, not the env file's mtime.
func TestSourceStatusEnvFilePathHasNoPlaylistImportedAt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _, _ := newTestSourceHandler(t, source.Config{Name: file.Name}, "")
	h.envFile = "/somewhere/else/playlist.json"

	body := statusBody(t, h)
	if !h.fileConfigured() {
		t.Fatal("fileConfigured = false, want true from ADOBOFLIX_FILE_PATH")
	}
	if _, present := body["playlist_imported_at"]; present {
		t.Errorf("playlist_imported_at = %v, want it absent for an env-supplied path", body["playlist_imported_at"])
	}
}

// modeConfigured means "the server has what it needs to open this adapter". A
// database-backed adapter is configured exactly when the server holds its
// handle, rather than reporting a hardcoded false.
func TestModeConfiguredReportsDatabaseAdapter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const name = "test-status-db-mode"
	source.Register(name, source.Requirement{Database: true}, func(source.Config) (source.Source, error) {
		return &codeStubSource{name: name}, nil
	})

	h, _, _ := newTestSourceHandler(t, source.Config{}, "")
	if h.modeConfigured(name) {
		t.Error("configured = true, want false without a database handle")
	}
	h.cfg.DB = &sqlx.DB{}
	if !h.modeConfigured(name) {
		t.Error("configured = false, want true with a database handle")
	}
}

// A content route on a sourceless server answers with its own code rather than
// an empty library or a nil dereference.
func TestContentRouteSourcelessReturnsSourceNotConfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	player := NewPlayerHandler(source.Unconfigured())
	r := gin.New()
	r.GET("/api/v1/stats", player.GetStats)

	w := doJSON(t, r, http.MethodGet, "/api/v1/stats", "")
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["code"] != codeSourceNotConfigured {
		t.Errorf("code = %v, want %q", body["code"], codeSourceNotConfigured)
	}
}
