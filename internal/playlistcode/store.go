// Package playlistcode persists the AdoboTV playlist code a user enters in the
// UI to a local file the server owns.
//
// The playlist code is the subscriber's one credential, so this package treats
// it as one: it is never logged, never returned in an API response (not even
// masked), and never included in an error message. The file is written 0600.
//
// Nothing here talks to AdoboTV. Persisting the user's own credential in a
// file the server owns is a local write, not an upstream one; the First Law in
// AGENTS.md is untouched.
//
// # Precedence
//
// A stored code wins over the environment; the environment variable is the
// fallback used only when no code has been stored. The rationale is that a
// code entered in the UI is the user's most recent explicit instruction, and a
// stale .env silently overriding it would make the UI look broken. Resolve
// owns that rule so the server and the endpoints that report the active state
// cannot disagree about it.
package playlistcode

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvPlaylistCodeFile names the environment variable that overrides where the
// code file lives. Optional; DefaultPath is used when it is unset.
const EnvPlaylistCodeFile = "ADOBOFLIX_PLAYLIST_CODE_FILE"

// DefaultFileMode is the mode every write uses. The file holds a credential,
// so it is owner-read/write only.
const DefaultFileMode os.FileMode = 0o600

// defaultPath is where the code lives when EnvPlaylistCodeFile is unset: a
// dot-directory beside the .env the server already loads from its working
// directory. It is gitignored.
var defaultPath = filepath.Join(".adoboflix", "playlist-code")

// ErrEmptyCode is returned when a code is required but none is present: a
// stored file that holds no code, or a Save of an empty string. An absent file
// is deliberately NOT this error — that is the ordinary "no code entered yet"
// state, reported as ok=false — so a truncated or half-written file fails
// loudly instead of silently yielding an empty code.
var ErrEmptyCode = errors.New("playlistcode: no playlist code")

// DefaultPath returns the configured code-file path: EnvPlaylistCodeFile when
// set, else the built-in default.
func DefaultPath() string {
	if p := strings.TrimSpace(os.Getenv(EnvPlaylistCodeFile)); p != "" {
		return p
	}
	return defaultPath
}

// Store reads and writes the playlist code file. It holds no code in memory
// and adds no caching: every call reflects the file on disk, which is what
// lets the status endpoint and the running source agree.
type Store struct {
	path string
}

// New returns a Store for path. An empty path disables the store: Load reports
// no code and Save/Clear are refused, so a misconfiguration cannot write a
// credential to an unintended location.
func New(path string) *Store {
	return &Store{path: strings.TrimSpace(path)}
}

// Path returns the file the store reads and writes. The path is not a secret;
// the code is.
func (s *Store) Path() string { return s.path }

// Load reads the stored code. ok is false, with a nil error, when no file
// exists — the ordinary "the user has not entered a code yet" state. A file
// that exists but holds no code returns ErrEmptyCode so a truncated file is
// never mistaken for that state. The code itself is never placed in an error.
func (s *Store) Load() (code string, ok bool, err error) {
	if s.path == "" {
		return "", false, nil
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read playlist code file: %w", err)
	}
	code = strings.TrimSpace(string(data))
	if code == "" {
		return "", false, fmt.Errorf("%w: %s exists but is empty", ErrEmptyCode, s.path)
	}
	return code, true, nil
}

// Save writes code to the store atomically and mode 0600: a temp file in the
// same directory is written and renamed into place, so a crash never leaves a
// half-written credential. An empty code is refused rather than written.
func (s *Store) Save(code string) error {
	code = strings.TrimSpace(code)
	if code == "" {
		return fmt.Errorf("%w: refusing to store an empty code", ErrEmptyCode)
	}
	if s.path == "" {
		return errors.New("playlistcode: no path configured")
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create playlist code directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".playlist-code-*")
	if err != nil {
		return fmt.Errorf("create playlist code temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.WriteString(code + "\n"); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write playlist code: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close playlist code: %w", err)
	}
	// os.CreateTemp already creates 0600; chmod makes the guarantee explicit
	// and independent of the process umask.
	if err := os.Chmod(tmpName, DefaultFileMode); err != nil {
		cleanup()
		return fmt.Errorf("set playlist code file mode: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		cleanup()
		return fmt.Errorf("replace playlist code file: %w", err)
	}
	return nil
}

// Clear removes the stored code. Removing a file that is not there is not an
// error: the desired state — no stored code — already holds.
func (s *Store) Clear() error {
	if s.path == "" {
		return nil
	}
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove playlist code file: %w", err)
	}
	return nil
}

// Resolve returns the code the source should be opened with and whether one is
// configured at all. A stored code wins; envCode is the fallback used only
// when no code has been stored. When neither is present the result is
// ("", false, nil): an unconfigured source is a valid state, not an error, so
// the server can boot and the user can enter a code while it runs.
//
// A malformed stored file is an error, never a silent fallback to the
// environment: a truncated credential must not be quietly ignored.
func (s *Store) Resolve(envCode string) (string, bool, error) {
	stored, ok, err := s.Load()
	if err != nil {
		return "", false, err
	}
	if ok {
		return stored, true, nil
	}
	if envCode = strings.TrimSpace(envCode); envCode != "" {
		return envCode, true, nil
	}
	return "", false, nil
}
