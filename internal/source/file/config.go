package file

import (
	"fmt"
	"os"
	"strings"
)

// EnvPath names the environment variable that points the file adapter at the
// playlist to load. It follows the ADOBOFLIX_* convention already used for
// ADOBOFLIX_SOURCE, ADOBOFLIX_PG_URL and the adobotv-http keys, and is
// deliberately defined here rather than in source.Config: the config struct
// carries only Name and DB, and this adapter reads neither the database nor
// anything but its own key.
//
// Required. An unset path is a startup error naming this key, never a silent
// empty library.
const EnvPath = "ADOBOFLIX_FILE_PATH"

// NewFromEnv builds the adapter from the process environment and logs the M3U
// classification summary. It fails fast, naming the missing key when the path
// is unset and the path itself when the file cannot be read or parsed.
//
// Logging happens here rather than in New so that constructing an adapter in a
// test does not print startup noise; this is the one path the server boots
// through (source.Open -> NewFromEnv).
func NewFromEnv() (*Adapter, error) {
	path := strings.TrimSpace(os.Getenv(EnvPath))
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
