package adobotvhttp

import (
	"errors"
	"strings"
	"testing"

	"github.com/jmvbambico/adoboflix/internal/source"
)

// The adapter declares its playlist-code need at registration, the same way a
// database adapter declares its handle, so the server offers the code-entry
// endpoints without hardcoding this adapter's name.
func TestRegistryDeclaresPlaylistCodeNeed(t *testing.T) {
	if !source.NeedsPlaylistCode(Name) {
		t.Errorf("source.NeedsPlaylistCode(%q) = false, want true", Name)
	}
	if source.NeedsDatabase(Name) {
		t.Errorf("source.NeedsDatabase(%q) = true, want false: this adapter needs no database", Name)
	}
}

// The server must boot with no playlist code so a new subscriber can enter one
// while it runs. The adapter opens in that state, and every read that needs the
// playlist reports ErrNoPlaylistCode rather than an upstream failure.
func TestOpenWithoutCodeIsAllowedAndReadsFailClearly(t *testing.T) {
	t.Setenv(EnvBaseURL, "http://127.0.0.1:1")
	t.Setenv(EnvPlaylistCode, "")

	opened, err := source.Open(source.Config{Name: Name})
	if err != nil {
		t.Fatalf("Open with no code = %v, want the adapter to open unconfigured", err)
	}
	adapter, ok := opened.(*Adapter)
	if !ok {
		t.Fatalf("Open returned %T, want *Adapter", opened)
	}

	// The guard fires before any HTTP request, so this cannot reach the
	// unreachable host above.
	_, err = adapter.GetStats()
	if !errors.Is(err, ErrNoPlaylistCode) {
		t.Fatalf("GetStats with no code = %v, want ErrNoPlaylistCode", err)
	}
	// The error must name how to supply a code, and must not carry one.
	if !strings.Contains(err.Error(), EnvPlaylistCode) {
		t.Errorf("error %q does not tell the user which key to set", err)
	}

	if _, _, err := adapter.ListChannels("", 1, 0); !errors.Is(err, ErrNoPlaylistCode) {
		t.Fatalf("ListChannels with no code = %v, want ErrNoPlaylistCode", err)
	}
}

// An explicit code wins over the environment: a code the user entered at
// runtime is their most recent instruction.
func TestExplicitCodeOverridesEnv(t *testing.T) {
	t.Setenv(EnvBaseURL, "http://host")
	t.Setenv(EnvPlaylistCode, "from-env")

	adapter, err := newFromEnvWithCode("from-ui")
	if err != nil {
		t.Fatalf("newFromEnvWithCode: %v", err)
	}
	if adapter.playlistCode != "from-ui" {
		t.Fatalf("playlistCode = %q, want the explicit code", adapter.playlistCode)
	}
}

// An empty explicit code falls back to the environment variable, so a server
// started with only ADOBOFLIX_ADOBOTV_PLAYLIST_CODE set still works.
func TestEmptyCodeFallsBackToEnv(t *testing.T) {
	t.Setenv(EnvBaseURL, "http://host")
	t.Setenv(EnvPlaylistCode, "from-env")

	adapter, err := newFromEnvWithCode("   ")
	if err != nil {
		t.Fatalf("newFromEnvWithCode: %v", err)
	}
	if adapter.playlistCode != "from-env" {
		t.Fatalf("playlistCode = %q, want the environment fallback", adapter.playlistCode)
	}
}

// NewFromEnv keeps its strict, fail-fast behavior: callers that mean "read the
// environment and nothing else" still get a clear error when no code is set.
func TestNewFromEnvStillRequiresCode(t *testing.T) {
	t.Setenv(EnvBaseURL, "http://host")
	t.Setenv(EnvPlaylistCode, "")

	if _, err := NewFromEnv(); err == nil {
		t.Fatal("NewFromEnv with no code = nil, want an error")
	}
}
