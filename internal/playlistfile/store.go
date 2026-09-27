// Package playlistfile persists a playlist a user imported to a local file the
// server owns, so the file adapter can be pointed at it.
//
// # Why this exists
//
// The second user path in AGENTS.md is someone with no AdoboTV account who
// points AdoboFlix at their own playlist. ADOBOFLIX_FILE_PATH covers that for a
// file already on disk, but a playlist pasted into the UI has to be written
// somewhere the server chose before the file adapter can read it. This store is
// that somewhere, mirroring playlistcode: it owns one file, writes it
// atomically, and never touches AdoboTV.
//
// # The extension is part of the contract
//
// The file adapter chooses its parser by the file's extension (.json vs
// .m3u/.m3u8), never by sniffing content. So this store must name the file it
// writes, and it picks the extension from the content's shape — a two-way
// choice between the only two formats the adapter accepts, not a third parser.
// A wrong guess still fails loudly in the adapter's own parser rather than
// mis-parsing half a file. The store also reports the chosen format so callers
// can describe the file accurately.
//
// Nothing here talks to AdoboTV. Writing the user's own playlist to a file the
// server owns is a local write, not an upstream one; the First Law in AGENTS.md
// is untouched.
package playlistfile

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EnvPlaylistFile names the environment variable that overrides where the
// imported playlist lives. Optional; DefaultPath is used when it is unset.
const EnvPlaylistFile = "ADOBOFLIX_PLAYLIST_FILE"

// fileMode is the mode every write uses. A playlist is not a credential, but it
// may carry stream URLs a user considers private, and the directory it lives in
// is shared with the credential files.
const fileMode os.FileMode = 0o600

// defaultBase is where the playlist lives when EnvPlaylistFile is unset. The
// extension is appended per format, so this is a base name, not a full path.
var defaultBase = filepath.Join(".adoboflix", "playlist")

// Format is the parser the file adapter will select for a stored playlist.
type Format string

const (
	FormatJSON Format = "json"
	FormatM3U  Format = "m3u"
)

// extensions maps a format to the extension the adapter's selector understands.
// .m3u and .m3u8 select the same parser; a stored M3U uses .m3u.
var extensions = map[Format]string{
	FormatJSON: ".json",
	FormatM3U:  ".m3u",
}

// ErrNoPlaylist is returned when a playlist is required but none is present: an
// empty upload, or a Save of empty content. An absent file is deliberately NOT
// this error — that is the ordinary "nothing imported yet" state, reported as
// ok=false.
var ErrNoPlaylist = errors.New("playlistfile: no playlist content")

// ErrUnreadable is returned when a stored playlist exists but cannot be read
// back — the read failed, or returned different bytes than were written. It is
// a clear failure rather than a silently truncated library.
var ErrUnreadable = errors.New("playlistfile: stored playlist could not be read back")

// DefaultPath returns the configured base path: EnvPlaylistFile when set, else
// the built-in default.
func DefaultPath() string {
	if p := strings.TrimSpace(os.Getenv(EnvPlaylistFile)); p != "" {
		return p
	}
	return defaultBase
}

// Store reads and writes the imported-playlist file. It holds no content in
// memory: every call reflects the file on disk.
type Store struct {
	base string
}

// New returns a Store whose file is base plus the format's extension. An empty
// base disables the store: Load reports nothing and Save/WriteTemp/Clear are
// inert or refused, so a misconfiguration cannot write a playlist to an
// unintended location.
func New(base string) *Store {
	return &Store{base: strings.TrimSpace(base)}
}

// Base returns the configured base path (without extension).
func (s *Store) Base() string { return s.base }

// Path returns the stored playlist's full path, or "" when nothing is stored.
func (s *Store) Path() string {
	if s.base == "" {
		return ""
	}
	for _, ext := range []string{extensions[FormatJSON], extensions[FormatM3U], ".m3u8"} {
		p := s.base + ext
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// Exists reports whether a playlist is stored.
func (s *Store) Exists() bool { return s.Path() != "" }

// ModTime returns the stored playlist's modification time — for this store,
// when the user imported it. ok is false when nothing is stored here or the
// file cannot be stat-ed; callers report that by omitting a timestamp field
// rather than sending a zero time. It deliberately looks only at this store's
// own path, so an ADOBOFLIX_FILE_PATH playlist that was never imported reports
// no import time.
func (s *Store) ModTime() (time.Time, bool) {
	path := s.Path()
	if path == "" {
		return time.Time{}, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

// FormatOf reports the format a stored path was written as, based on its
// extension. It is meaningful for a path this store returned.
func FormatOf(path string) Format {
	if strings.EqualFold(filepath.Ext(path), ".json") {
		return FormatJSON
	}
	return FormatM3U
}

// SniffFormat reports the format the content looks like and whether it matches
// one of the two shapes the adapter reads at all: an M3U (a leading directive,
// or the #EXTM3U / #EXTINF markers) or JSON (a leading { or [). ok is false for
// content that matches neither — a bare list of URLs, say. Callers use that to
// describe a rejection honestly instead of guessing JSON.
func SniffFormat(data []byte) (Format, bool) {
	sample := data
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	s := strings.TrimPrefix(string(sample), "\ufeff")
	trimmed := strings.TrimLeft(s, " \t\r\n")
	if strings.HasPrefix(trimmed, "#") || strings.Contains(s, "#EXTM3U") || strings.Contains(s, "#EXTINF") {
		return FormatM3U, true
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return FormatJSON, true
	}
	return FormatJSON, false
}

// detectFormat guesses the format from the content's shape. It is a two-way
// guess between the adapter's only two formats, not a parser — the adapter
// still does the real parse, by the extension this choice produces. Content
// matching neither shape is given the JSON extension so it still fails loudly
// in the JSON parser rather than being accepted silently; SniffFormat is what
// callers use to say so honestly in a rejection.
func detectFormat(data []byte) Format {
	format, _ := SniffFormat(data)
	return format
}

// WriteTemp writes data to a scratch file in the store's directory, with the
// extension the detected format selects, and returns its path. The caller owns
// the file and must remove it. It exists so a handler can have the real file
// adapter parse an upload — the same validation the playlist-code endpoint does
// — before anything is persisted to the store's own path.
func (s *Store) WriteTemp(data []byte) (string, error) {
	if s.base == "" {
		return "", errors.New("playlistfile: no path configured")
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return "", fmt.Errorf("%w: refusing to stage an empty playlist", ErrNoPlaylist)
	}
	dir := filepath.Dir(s.base)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create playlist directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".playlist-*"+extensions[detectFormat(data)])
	if err != nil {
		return "", fmt.Errorf("create playlist temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("write playlist temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("close playlist temp file: %w", err)
	}
	if err := os.Chmod(tmpName, fileMode); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("set playlist temp file mode: %w", err)
	}
	return tmpName, nil
}

// Save writes data to the store atomically and mode 0600, naming the file by
// the detected format's extension and removing any file of the other format.
// Empty content is refused. After the rename it reads the file back and
// verifies the bytes match, so a Save that cannot be read back reports
// ErrUnreadable rather than leaving a playlist the server cannot load. It
// returns the stored path and the format it was stored as.
func (s *Store) Save(data []byte) (string, Format, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return "", "", fmt.Errorf("%w: refusing to store an empty playlist", ErrNoPlaylist)
	}
	if s.base == "" {
		return "", "", errors.New("playlistfile: no path configured")
	}

	format := detectFormat(data)
	target := s.base + extensions[format]

	dir := filepath.Dir(s.base)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("create playlist directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".playlist-save-*"+extensions[format])
	if err != nil {
		return "", "", fmt.Errorf("create playlist temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return "", "", fmt.Errorf("write playlist: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return "", "", fmt.Errorf("close playlist: %w", err)
	}
	if err := os.Chmod(tmpName, fileMode); err != nil {
		cleanup()
		return "", "", fmt.Errorf("set playlist file mode: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		cleanup()
		return "", "", fmt.Errorf("replace playlist file: %w", err)
	}

	// Drop any file of the other format: one stored playlist, not two.
	for _, ext := range []string{extensions[FormatJSON], extensions[FormatM3U], ".m3u8"} {
		if p := s.base + ext; p != target {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				return "", "", fmt.Errorf("remove stale playlist %s: %w", p, err)
			}
		}
	}

	back, err := os.ReadFile(target)
	if err != nil {
		return "", "", fmt.Errorf("%w: %s: %v", ErrUnreadable, target, err)
	}
	if !bytes.Equal(back, data) {
		return "", "", fmt.Errorf("%w: %s holds different content than was written", ErrUnreadable, target)
	}
	return target, format, nil
}

// Load reads the stored playlist. ok is false, with a nil error, when nothing
// is stored — the ordinary "nothing imported yet" state. A file that exists but
// cannot be read returns ErrUnreadable so a stored-but-broken playlist is never
// mistaken for that state.
func (s *Store) Load() (data []byte, path string, ok bool, err error) {
	path = s.Path()
	if path == "" {
		return nil, "", false, nil
	}
	data, err = os.ReadFile(path)
	if err != nil {
		return nil, "", false, fmt.Errorf("%w: %s: %v", ErrUnreadable, path, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, "", false, fmt.Errorf("%w: %s exists but is empty", ErrUnreadable, path)
	}
	return data, path, true, nil
}

// Clear removes the stored playlist in either format. Removing a file that is
// not there is not an error: the desired state — nothing stored — already
// holds.
func (s *Store) Clear() error {
	if s.base == "" {
		return nil
	}
	for _, ext := range []string{extensions[FormatJSON], extensions[FormatM3U], ".m3u8"} {
		if err := os.Remove(s.base + ext); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove playlist file: %w", err)
		}
	}
	return nil
}
