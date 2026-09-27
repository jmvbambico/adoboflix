package file

import (
	"fmt"
	"os"
	"strings"

	"github.com/jmvbambico/adoboflix/internal/source"
)

// EnvPath names the environment variable that points the file adapter at the
// playlist to load. It follows the ADOBOFLIX_* convention already used for
// ADOBOFLIX_SOURCE, ADOBOFLIX_PG_URL and the adobotv-http keys, and is
// deliberately defined here rather than in source.Config: this adapter reads
// nothing but its own key and a path handed to it.
//
// Required unless Config.FilePath supplies a path. An unset path with no
// explicit path is a startup error naming this key, never a silent empty
// library.
const EnvPath = "ADOBOFLIX_FILE_PATH"

// NewFromConfig builds the adapter from a source.Config and logs the M3U
// classification summary. Config.FilePath is the path the server chose (a
// playlist the user imported); when it is empty the adapter falls back to
// EnvPath, the same precedence shape Config.PlaylistCode uses for a code. With
// neither, it fails fast naming the missing key; a path it cannot read or
// parse fails naming the path.
//
// Logging happens here rather than in New so that constructing an adapter in a
// test does not print startup noise; this is the one path the server boots
// through (source.Open -> the registered factory).
func NewFromConfig(cfg source.Config) (*Adapter, error) {
	path := strings.TrimSpace(cfg.FilePath)
	if path == "" {
		path = strings.TrimSpace(os.Getenv(EnvPath))
	}
	if path == "" {
		return nil, fmt.Errorf("%s is not set: set it to the path of your playlist file (.json, .m3u or .m3u8)", EnvPath)
	}
	adapter, err := New(path)
	if err != nil {
		return nil, err
	}
	adapter.logSummary()
	return adapter, nil
}

// NewFromEnv builds the adapter from the process environment alone, ignoring
// any explicit path. It is NewFromConfig with an empty Config, kept because a
// caller that means "read ADOBOFLIX_FILE_PATH" reads better than one that
// passes a zero Config.
func NewFromEnv() (*Adapter, error) {
	return NewFromConfig(source.Config{})
}
