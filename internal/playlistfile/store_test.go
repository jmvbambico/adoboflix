package playlistfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const jsonPlaylist = `{"channels":[{"id":"c1","name":"News"}]}`
const m3uPlaylist = "#EXTM3U\n#EXTINF:-1,News\nhttps://cdn.example/news.m3u8\n"

func TestSaveLoadRoundTripJSON(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "playlist"))

	if _, path, ok, err := store.Load(); err != nil || ok || path != "" {
		t.Fatalf("Load on absent file = (%q, %v, %v), want (\"\", false, nil)", path, ok, err)
	}

	path, format, err := store.Save([]byte(jsonPlaylist))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if format != FormatJSON || !strings.HasSuffix(path, ".json") {
		t.Fatalf("Save = (%q, %q), want a .json path and JSON format", path, format)
	}
	data, gotPath, ok, err := store.Load()
	if err != nil || !ok || gotPath != path {
		t.Fatalf("Load = (%q, %v, %v), want the stored path", gotPath, ok, err)
	}
	if string(data) != jsonPlaylist {
		t.Fatalf("Load data = %q, want the saved bytes", data)
	}
}

// M3U is stored under an extension the adapter's selector maps to its M3U
// parser, since the adapter chooses its parser by extension.
func TestSaveDetectsM3UExtension(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "playlist"))
	path, format, err := store.Save([]byte(m3uPlaylist))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if format != FormatM3U || !strings.HasSuffix(path, ".m3u") {
		t.Fatalf("Save = (%q, %q), want a .m3u path and M3U format", path, format)
	}
	if got := detectFormat([]byte(jsonPlaylist)); got != FormatJSON {
		t.Fatalf("detectFormat(JSON) = %q, want %q", got, FormatJSON)
	}
}

func TestSaveWrites0600(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "playlist"))
	path, _, err := store.Save([]byte(jsonPlaylist))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode = %o, want 600", got)
	}
}

// Saving a different format replaces the old file rather than leaving two.
func TestSaveReplacesOtherFormat(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "playlist"))
	jsonPath, _, err := store.Save([]byte(jsonPlaylist))
	if err != nil {
		t.Fatalf("Save JSON: %v", err)
	}
	m3uPath, _, err := store.Save([]byte(m3uPlaylist))
	if err != nil {
		t.Fatalf("Save M3U: %v", err)
	}
	if _, err := os.Stat(jsonPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the old JSON file still exists at %s", jsonPath)
	}
	if _, err := os.Stat(m3uPath); err != nil {
		t.Fatalf("the new M3U file is missing: %v", err)
	}
	if got := store.Path(); got != m3uPath {
		t.Fatalf("Path = %q, want the stored M3U path %q", got, m3uPath)
	}
}

func TestSaveRejectsEmpty(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "playlist"))
	for _, blank := range []string{"", "   ", "\n"} {
		if _, _, err := store.Save([]byte(blank)); !errors.Is(err, ErrNoPlaylist) {
			t.Fatalf("Save(%q) = %v, want ErrNoPlaylist", blank, err)
		}
	}
	if store.Exists() {
		t.Fatal("an empty Save stored something")
	}
}

func TestWriteTempUsesDetectedExtension(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "playlist"))
	jsonTmp, err := store.WriteTemp([]byte(jsonPlaylist))
	if err != nil {
		t.Fatalf("WriteTemp JSON: %v", err)
	}
	defer os.Remove(jsonTmp)
	if !strings.HasSuffix(jsonTmp, ".json") {
		t.Fatalf("JSON temp = %q, want a .json extension", jsonTmp)
	}
	m3uTmp, err := store.WriteTemp([]byte(m3uPlaylist))
	if err != nil {
		t.Fatalf("WriteTemp M3U: %v", err)
	}
	defer os.Remove(m3uTmp)
	if !strings.HasSuffix(m3uTmp, ".m3u") {
		t.Fatalf("M3U temp = %q, want a .m3u extension", m3uTmp)
	}
	// A scratch upload is not a stored playlist.
	if store.Exists() {
		t.Fatal("WriteTemp left a playlist at the store's own path")
	}
	if _, err := store.WriteTemp([]byte("  ")); !errors.Is(err, ErrNoPlaylist) {
		t.Fatalf("WriteTemp(empty) = %v, want ErrNoPlaylist", err)
	}
}

func TestClearRemovesBothFormats(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "playlist"))
	if _, _, err := store.Save([]byte(jsonPlaylist)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if store.Exists() {
		t.Fatal("Clear left a playlist stored")
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear (idempotent): %v", err)
	}
}

func TestEmptyBaseStoreIsInert(t *testing.T) {
	store := New("")
	if _, _, ok, err := store.Load(); err != nil || ok {
		t.Fatalf("Load = (_, _, %v, %v), want ok=false and no error", ok, err)
	}
	if _, _, err := store.Save([]byte(jsonPlaylist)); err == nil {
		t.Fatal("Save = nil, want it refused for a store with no base")
	}
	if _, err := store.WriteTemp([]byte(jsonPlaylist)); err == nil {
		t.Fatal("WriteTemp = nil, want it refused for a store with no base")
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear = %v, want nil", err)
	}
}

// A file that exists but holds nothing fails loudly rather than loading as an
// empty library.
func TestLoadEmptyFileFails(t *testing.T) {
	base := filepath.Join(t.TempDir(), "playlist")
	if err := os.WriteFile(base+".json", []byte("  \n"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if _, _, _, err := New(base).Load(); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("Load = %v, want ErrUnreadable", err)
	}
}

func TestDefaultPathHonoursEnvThenDefault(t *testing.T) {
	t.Setenv(EnvPlaylistFile, "")
	if got := DefaultPath(); !strings.Contains(got, "playlist") {
		t.Fatalf("DefaultPath = %q, want the built-in default", got)
	}
	custom := filepath.Join(t.TempDir(), "custom-playlist")
	t.Setenv(EnvPlaylistFile, custom)
	if got := DefaultPath(); got != custom {
		t.Fatalf("DefaultPath = %q, want %q", got, custom)
	}
}
