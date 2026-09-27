package file

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmvbambico/adoboflix/internal/source"
)

// Config.FilePath wins over ADOBOFLIX_FILE_PATH: it is the path the server
// chose for an imported playlist, and an explicit path must not be overridden
// by a stale environment value.
func TestNewFromConfigExplicitPathWins(t *testing.T) {
	envPath := writePlaylist(t, "env.json", `{"channels":[{"id":"env","name":"Env"}]}`)
	explicit := writePlaylist(t, "imported.json", `{"channels":[{"id":"imp","name":"Imported"}]}`)
	t.Setenv(EnvPath, envPath)

	adapter, err := NewFromConfig(source.Config{FilePath: explicit})
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	if adapter.path != explicit {
		t.Fatalf("adapter loaded %q, want the explicit path %q", adapter.path, explicit)
	}
}

// With no explicit path the adapter falls back to its own environment key.
func TestNewFromConfigFallsBackToEnv(t *testing.T) {
	envPath := writePlaylist(t, "env.json", `{"channels":[{"id":"env","name":"Env"}]}`)
	t.Setenv(EnvPath, envPath)

	adapter, err := NewFromConfig(source.Config{})
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	if adapter.path != envPath {
		t.Fatalf("adapter loaded %q, want the env path %q", adapter.path, envPath)
	}
}

// With neither, the failure names the missing key, never a silent empty
// library.
func TestNewFromConfigRequiresAPath(t *testing.T) {
	t.Setenv(EnvPath, "")
	_, err := NewFromConfig(source.Config{})
	if err == nil || !strings.Contains(err.Error(), EnvPath) {
		t.Fatalf("NewFromConfig with no path = %v, want an error naming %s", err, EnvPath)
	}
}

// The adapter declares the two facts the server derives its offers from.
func TestFileAdapterDeclaredRequirements(t *testing.T) {
	if !source.Selectable(Name) {
		t.Errorf("Selectable(%q) = false, want true: it is an end-user path", Name)
	}
	if source.Dev(Name) {
		t.Errorf("Dev(%q) = true, want false", Name)
	}
	if !source.NeedsPlaylistFile(Name) {
		t.Errorf("NeedsPlaylistFile(%q) = false, want true: it reads a playlist file", Name)
	}
	if source.NeedsPlaylistCode(Name) {
		t.Errorf("NeedsPlaylistCode(%q) = true, want false", Name)
	}
}

func writePlaylist(t *testing.T, name, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write playlist: %v", err)
	}
	return path
}
