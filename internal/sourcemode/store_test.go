package sourcemode

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	// Register the real adapters so Resolve's selectability check is exercised
	// against the same names the server uses.
	_ "github.com/jmvbambico/adoboflix/internal/source/adobotvhttp"
	_ "github.com/jmvbambico/adoboflix/internal/source/file"
	_ "github.com/jmvbambico/adoboflix/internal/source/postgresdirect"
)

const (
	modeLogin = "adobotv-http"
	modeFile  = "file"
	modeDev   = "postgres-direct"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "source-mode"))

	mode, ok, err := store.Load()
	if err != nil {
		t.Fatalf("Load on absent file: %v", err)
	}
	if ok || mode != "" {
		t.Fatalf("Load on absent file = (%q, %v), want (\"\", false)", mode, ok)
	}

	if err := store.Save(modeFile); err != nil {
		t.Fatalf("Save: %v", err)
	}
	mode, ok, err = store.Load()
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if !ok || mode != modeFile {
		t.Fatalf("Load after Save = (%q, %v), want (%q, true)", mode, ok, modeFile)
	}
}

func TestSaveWrites0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source-mode")
	if err := New(path).Save(modeFile); err != nil {
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

// A dev adapter must never be remembered as the user's mode: it is reachable
// only through the environment override.
func TestSaveRefusesNonSelectableMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source-mode")
	err := New(path).Save(modeDev)
	if !errors.Is(err, ErrNotSelectable) {
		t.Fatalf("Save(%q) = %v, want ErrNotSelectable", modeDev, err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("a refused Save created a file: %v", statErr)
	}
}

func TestSaveRejectsEmptyMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source-mode")
	for _, blank := range []string{"", "   ", "\n"} {
		if err := New(path).Save(blank); !errors.Is(err, ErrEmptyMode) {
			t.Fatalf("Save(%q) = %v, want ErrEmptyMode", blank, err)
		}
	}
}

// A file that exists but holds no mode fails loudly rather than silently
// leaving the server sourceless.
func TestLoadEmptyFileFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source-mode")
	if err := os.WriteFile(path, []byte("  \n"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	mode, ok, err := New(path).Load()
	if !errors.Is(err, ErrEmptyMode) {
		t.Fatalf("Load = (%q, %v, %v), want ErrEmptyMode", mode, ok, err)
	}
	if ok || mode != "" {
		t.Fatalf("Load = (%q, %v), want (\"\", false) alongside the error", mode, ok)
	}
}

// A hand-edited file naming a dev adapter is refused, not opened.
func TestLoadRefusesNonSelectableMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source-mode")
	if err := os.WriteFile(path, []byte(modeDev+"\n"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if _, _, err := New(path).Load(); !errors.Is(err, ErrNotSelectable) {
		t.Fatalf("Load = %v, want ErrNotSelectable", err)
	}
}

func TestClearRemovesStoredMode(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "source-mode"))
	if err := store.Save(modeFile); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, ok, err := store.Load(); err != nil || ok {
		t.Fatalf("Load after Clear = (_, %v, %v), want ok=false and no error", ok, err)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear (idempotent): %v", err)
	}
}

// Precedence: the environment override wins over a stored mode.
func TestResolveEnvWinsOverStored(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "source-mode"))
	if err := store.Save(modeFile); err != nil {
		t.Fatalf("Save: %v", err)
	}
	res, err := store.Resolve(modeDev)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Mode != modeDev || res.Origin != OriginEnv {
		t.Fatalf("Resolve = %+v, want mode=%q origin=%q", res, modeDev, OriginEnv)
	}
}

// A dev adapter is reachable through the override, which is the one path that
// must keep working.
func TestResolveEnvAcceptsDevAdapter(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "source-mode"))
	res, err := store.Resolve(modeDev)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", modeDev, err)
	}
	if res.Mode != modeDev || res.Origin != OriginEnv {
		t.Fatalf("Resolve = %+v, want the dev adapter pinned", res)
	}
}

// With no override, a stored mode is used.
func TestResolveUsesStoredMode(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "source-mode"))
	if err := store.Save(modeLogin); err != nil {
		t.Fatalf("Save: %v", err)
	}
	res, err := store.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Mode != modeLogin || res.Origin != OriginStored {
		t.Fatalf("Resolve = %+v, want mode=%q origin=%q", res, modeLogin, OriginStored)
	}
}

// With neither, the server is sourceless: a valid state, reported as such.
func TestResolveNeitherIsSourcelessNotAnError(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "source-mode"))
	for _, env := range []string{"", "   "} {
		res, err := store.Resolve(env)
		if err != nil {
			t.Fatalf("Resolve(%q) returned an error: %v", env, err)
		}
		if res.Mode != "" || res.Origin != OriginNone {
			t.Fatalf("Resolve(%q) = %+v, want mode=\"\" origin=%q", env, res, OriginNone)
		}
	}
}

// An unknown override is a startup error, never a silent default.
func TestResolveRejectsUnknownEnvSource(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "source-mode"))
	if _, err := store.Resolve("no-such-adapter"); err == nil {
		t.Fatal("Resolve(unknown) = nil error, want a clear failure")
	}
}

// A malformed stored file is an error, never a silent fallback to the
// override or to no source.
func TestResolveMalformedStoredFileDoesNotFallBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source-mode")
	if err := os.WriteFile(path, []byte("  \n"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if _, err := New(path).Resolve(""); !errors.Is(err, ErrEmptyMode) {
		t.Fatalf("Resolve with no override = %v, want ErrEmptyMode", err)
	}
	// With an override present the override still wins, even over a corrupt
	// stored file: the override is what pins the source.
	res, err := New(path).Resolve(modeLogin)
	if err != nil {
		t.Fatalf("Resolve(override): %v", err)
	}
	if res.Origin != OriginEnv {
		t.Fatalf("Resolve(override) origin = %q, want %q", res.Origin, OriginEnv)
	}
}

// A store with no path is inert: it must not read or write anything.
func TestEmptyPathStoreIsInert(t *testing.T) {
	store := New("")
	if _, ok, err := store.Load(); err != nil || ok {
		t.Fatalf("Load = (_, %v, %v), want ok=false with no error", ok, err)
	}
	if err := store.Save(modeFile); err == nil {
		t.Fatal("Save = nil, want it refused for a store with no path")
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear = %v, want nil", err)
	}
	res, err := store.Resolve("")
	if err != nil || res.Origin != OriginNone {
		t.Fatalf("Resolve = (%+v, %v), want the sourceless state", res, err)
	}
}

func TestDefaultPathHonoursEnvThenDefault(t *testing.T) {
	t.Setenv(EnvModeFile, "")
	if got := DefaultPath(); !strings.Contains(got, "source-mode") {
		t.Fatalf("DefaultPath = %q, want the built-in default", got)
	}
	custom := filepath.Join(t.TempDir(), "custom-mode-file")
	t.Setenv(EnvModeFile, custom)
	if got := DefaultPath(); got != custom {
		t.Fatalf("DefaultPath = %q, want %q", got, custom)
	}
}
