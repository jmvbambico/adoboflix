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

// NewFromEnv builds the adapter from the process environment. It fails fast,
// naming the missing key when the path is unset and the path itself when the
// file cannot be read or parsed.
func NewFromEnv() (*Adapter, error) {
	path := strings.TrimSpace(os.Getenv(EnvPath))
	if path == "" {
		return nil, fmt.Errorf("%s is not set: set it to the path of your playlist JSON file", EnvPath)
	}
	return New(path)
}
