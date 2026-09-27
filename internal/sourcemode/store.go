// Package sourcemode persists the content-source mode a user chose in the UI
// to a local file the server owns.
//
// # How a source is chosen, and why
//
// ADOBOFLIX_SOURCE is the override. When it is set it pins the source and wins,
// and it is the only way to reach a development adapter such as
// postgres-direct. When it is unset the server uses the mode this store
// remembers — one of the user-selectable adapters, in practice the login path
// (adobotv-http) or the imported-playlist path (file). With neither, the server
// boots with no source at all: a valid state the UI offers those two choices
// from, and the opposite of a silent fallback.
//
// The precedence lives here, in Resolve, so the server and the status endpoint
// cannot disagree about which mode is active — the same shape playlistcode owns
// for the subscriber credential.
//
// Nothing here talks to AdoboTV. Remembering a local mode is a local write, not
// an upstream one; the First Law in AGENTS.md is untouched.
package sourcemode

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jmvbambico/adoboflix/internal/source"
)

// EnvModeFile names the environment variable that overrides where the mode file
// lives. Optional; DefaultPath is used when it is unset.
const EnvModeFile = "ADOBOFLIX_SOURCE_MODE_FILE"

// fileMode is the mode every write uses. The mode is not a credential, but the
// directory it lives in is shared with the playlist-code file, and there is no
// reason to relax the one policy.
const fileMode os.FileMode = 0o600

// defaultPath is where the mode lives when EnvModeFile is unset: a dot-directory
// beside the .env the server already loads from its working directory. It is
// gitignored.
var defaultPath = filepath.Join(".adoboflix", "source-mode")

// Origin describes where the active source came from.
const (
	// OriginEnv is ADOBOFLIX_SOURCE: the override pinned the source.
	OriginEnv = "env"
	// OriginStored is a mode the user chose in the UI and this store remembers.
	OriginStored = "stored"
	// OriginNone is no source at all — the server booted unconfigured.
	OriginNone = "none"
)

// ErrEmptyMode is returned when a mode is required but none is present: a
// stored file that holds no mode, or a Save of an empty string. An absent file
// is deliberately NOT this error — that is the ordinary "the user has not
// chosen yet" state, reported as ok=false — so a truncated file fails loudly
// instead of silently leaving the server sourceless.
var ErrEmptyMode = errors.New("sourcemode: no stored source mode")

// ErrNotSelectable is returned when a mode names an adapter a normal user may
// not choose — a development harness. Such an adapter is reachable only through
// the ADOBOFLIX_SOURCE override, never through a remembered mode, so it is
// refused here rather than being opened.
var ErrNotSelectable = errors.New("sourcemode: source mode is not user-selectable")

// Resolution is the source the server should run: the adapter mode, and where
// that choice came from.
type Resolution struct {
	// Mode is the adapter name, or "" when no source is configured.
	Mode string
	// Origin is OriginEnv, OriginStored or OriginNone.
	Origin string
}

// DefaultPath returns the configured mode-file path: EnvModeFile when set, else
// the built-in default.
func DefaultPath() string {
	if p := strings.TrimSpace(os.Getenv(EnvModeFile)); p != "" {
		return p
	}
	return defaultPath
}

// Store reads and writes the mode file. It holds no mode in memory and adds no
// caching: every call reflects the file on disk, which is what lets the status
// endpoint and the running source agree.
type Store struct {
	path string
}

// New returns a Store for path. An empty path disables the store: Load reports
// no mode and Save/Clear are refused, so a misconfiguration cannot write a mode
// to an unintended location.
func New(path string) *Store {
	return &Store{path: strings.TrimSpace(path)}
}

// Path returns the file the store reads and writes.
func (s *Store) Path() string { return s.path }

// Load reads the stored mode. ok is false, with a nil error, when no file
// exists — the ordinary "the user has not chosen yet" state. A file that exists
// but holds no mode returns ErrEmptyMode; one that names a non-selectable
// adapter returns ErrNotSelectable. Either way a corrupt or hand-edited file
// fails loudly rather than silently changing the source.
func (s *Store) Load() (string, bool, error) {
	if s.path == "" {
		return "", false, nil
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read source mode file: %w", err)
	}
	mode := strings.TrimSpace(string(data))
	if mode == "" {
		return "", false, fmt.Errorf("%w: %s exists but is empty", ErrEmptyMode, s.path)
	}
	if !source.Selectable(mode) {
		return "", false, fmt.Errorf("%w: %q", ErrNotSelectable, mode)
	}
	return mode, true, nil
}

// Save writes mode to the store atomically and mode 0600: a temp file in the
// same directory is written and renamed into place, so a crash never leaves a
// half-written file. An empty mode, or one that is not user-selectable, is
// refused rather than written.
func (s *Store) Save(mode string) error {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return fmt.Errorf("%w: refusing to store an empty mode", ErrEmptyMode)
	}
	if !source.Selectable(mode) {
		return fmt.Errorf("%w: %q", ErrNotSelectable, mode)
	}
	if s.path == "" {
		return errors.New("sourcemode: no path configured")
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create source mode directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".source-mode-*")
	if err != nil {
		return fmt.Errorf("create source mode temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.WriteString(mode + "\n"); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write source mode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close source mode: %w", err)
	}
	if err := os.Chmod(tmpName, fileMode); err != nil {
		cleanup()
		return fmt.Errorf("set source mode file mode: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		cleanup()
		return fmt.Errorf("replace source mode file: %w", err)
	}
	return nil
}

// Clear removes the stored mode. Removing a file that is not there is not an
// error: the desired state — no stored mode — already holds.
func (s *Store) Clear() error {
	if s.path == "" {
		return nil
	}
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove source mode file: %w", err)
	}
	return nil
}

// Resolve returns the source the server should run and where that choice came
// from. The environment override wins when it is set; otherwise a stored mode
// is used; with neither the result is the empty mode with OriginNone — a sourceless
// server the UI offers its choices from, not an error.
//
// A malformed stored file (empty, or naming a non-selectable adapter) is an
// error, never a silent fallback to the environment or to a default: a
// hand-edited file must not quietly change what the server serves.
func (s *Store) Resolve(envSource string) (Resolution, error) {
	if env := strings.TrimSpace(envSource); env != "" {
		if err := source.Validate(env); err != nil {
			return Resolution{}, err
		}
		return Resolution{Mode: env, Origin: OriginEnv}, nil
	}

	stored, ok, err := s.Load()
	if err != nil {
		return Resolution{}, err
	}
	if ok {
		return Resolution{Mode: stored, Origin: OriginStored}, nil
	}
	return Resolution{Mode: "", Origin: OriginNone}, nil
}
