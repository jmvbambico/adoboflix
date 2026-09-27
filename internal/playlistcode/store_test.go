package playlistcode

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "playlist-code")
	store := New(path)

	code, ok, err := store.Load()
	if err != nil {
		t.Fatalf("Load on absent file: %v", err)
	}
	if ok || code != "" {
		t.Fatalf("Load on absent file = (%q, %v), want (\"\", false)", code, ok)
	}

	const secret = "SUPER-SECRET-CODE-42"
	if err := store.Save(secret); err != nil {
		t.Fatalf("Save: %v", err)
	}

	code, ok, err = store.Load()
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if !ok || code != secret {
		t.Fatalf("Load after Save = (%q, %v), want (%q, true)", code, ok, secret)
	}
}

// The file is a credential, so its mode must be 0600 exactly — not merely
// "not world-writable".
func TestSaveWrites0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "playlist-code")
	if err := New(path).Save("CODE-ABC"); err != nil {
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

// An existing file with looser permissions is tightened on the next Save:
// the guarantee is about the file the store owns, not only a fresh one.
func TestSaveTightensExistingPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "playlist-code")
	if err := os.WriteFile(path, []byte("OLD\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if err := New(path).Save("NEW"); err != nil {
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

// A file that exists but holds no code must fail clearly rather than silently
// yielding an empty code.
func TestLoadEmptyOrWhitespaceFileFails(t *testing.T) {
	for _, name := range []string{"empty", "whitespace", "newline"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "playlist-code")
			var content string
			switch name {
			case "empty":
				content = ""
			case "whitespace":
				content = "   \t "
			case "newline":
				content = "\n"
			}
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatalf("seed file: %v", err)
			}

			code, ok, err := New(path).Load()
			if err == nil {
				t.Fatalf("Load = (%q, %v, nil), want a clear error", code, ok)
			}
			if !errors.Is(err, ErrEmptyCode) {
				t.Fatalf("Load error = %v, want it to wrap ErrEmptyCode", err)
			}
			if ok || code != "" {
				t.Fatalf("Load = (%q, %v), want (\"\", false) alongside the error", code, ok)
			}
		})
	}
}

func TestSaveRejectsEmptyCode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "playlist-code")
	store := New(path)
	for _, blank := range []string{"", "   ", "\n"} {
		if err := store.Save(blank); !errors.Is(err, ErrEmptyCode) {
			t.Fatalf("Save(%q) = %v, want ErrEmptyCode", blank, err)
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Save of an empty code created a file: %v", err)
	}
}

func TestSaveTrimsSurroundingWhitespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "playlist-code")
	store := New(path)
	if err := store.Save("  CODE-WITH-PADDING  "); err != nil {
		t.Fatalf("Save: %v", err)
	}
	code, ok, err := store.Load()
	if err != nil || !ok {
		t.Fatalf("Load = (%q, %v, %v)", code, ok, err)
	}
	if code != "CODE-WITH-PADDING" {
		t.Fatalf("code = %q, want it trimmed", code)
	}
}

func TestClearRemovesStoredCode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "playlist-code")
	store := New(path)
	if err := store.Save("CODE-XYZ"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, ok, err := store.Load(); err != nil || ok {
		t.Fatalf("Load after Clear = (%v, %v, %v), want ok=false and no error", "", ok, err)
	}
	// Clearing an already-absent code is not an error.
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear (idempotent): %v", err)
	}
}

// Precedence: the stored file wins over the environment.
func TestResolveFileBeatsEnv(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "playlist-code"))
	if err := store.Save("FROM-FILE"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	code, ok, err := store.Resolve("FROM-ENV")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !ok || code != "FROM-FILE" {
		t.Fatalf("Resolve = (%q, %v), want the stored code to win", code, ok)
	}
}

// Precedence: with no stored file, the environment is the fallback.
func TestResolveFallsBackToEnv(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "playlist-code"))
	code, ok, err := store.Resolve("FROM-ENV")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !ok || code != "FROM-ENV" {
		t.Fatalf("Resolve = (%q, %v), want the env fallback", code, ok)
	}
}

// With neither a file nor an env value the source is simply unconfigured: no
// error, ok=false. This is the state the server boots in for a new subscriber.
func TestResolveNeitherIsUnconfiguredNotAnError(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "playlist-code"))
	for _, env := range []string{"", "   "} {
		code, ok, err := store.Resolve(env)
		if err != nil {
			t.Fatalf("Resolve(%q) returned an error: %v", env, err)
		}
		if ok || code != "" {
			t.Fatalf("Resolve(%q) = (%q, %v), want (\"\", false)", env, code, ok)
		}
	}
}

// A malformed stored file is an error, never a silent fallback to the env var.
func TestResolveMalformedFileDoesNotFallBackToEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "playlist-code")
	if err := os.WriteFile(path, []byte("  \n"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	code, ok, err := New(path).Resolve("FROM-ENV")
	if !errors.Is(err, ErrEmptyCode) {
		t.Fatalf("Resolve error = %v, want ErrEmptyCode", err)
	}
	if ok || code != "" {
		t.Fatalf("Resolve = (%q, %v), want no code alongside the error", code, ok)
	}
}

// A store with no path is inert: it must not read or write anything, so a
// misconfiguration cannot silently persist a credential somewhere unexpected.
func TestEmptyPathStoreIsInert(t *testing.T) {
	store := New("")
	if _, ok, err := store.Load(); err != nil || ok {
		t.Fatalf("Load = (_, %v, %v), want ok=false with no error", ok, err)
	}
	if err := store.Save("X"); err == nil {
		t.Fatal("Save = nil, want it refused for a store with no path")
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear = %v, want nil", err)
	}
}

func TestDefaultPathHonoursEnvThenDefault(t *testing.T) {
	t.Setenv(EnvPlaylistCodeFile, "")
	if got := DefaultPath(); !strings.Contains(got, "playlist-code") {
		t.Fatalf("DefaultPath = %q, want the built-in default", got)
	}

	custom := filepath.Join(t.TempDir(), "custom-code-file")
	t.Setenv(EnvPlaylistCodeFile, custom)
	if got := DefaultPath(); got != custom {
		t.Fatalf("DefaultPath = %q, want %q", got, custom)
	}
}
