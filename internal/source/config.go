package source

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"
)

// EnvSource names the environment variable that selects the content source.
// It has no default: an unset value is a startup error, never a silent
// fallback to a particular adapter.
const EnvSource = "ADOBOFLIX_SOURCE"

// Config is what an adapter is opened with. Name is the requested adapter;
// DB is a read-only SQL handle that a database-backed adapter may use and
// others simply ignore.
type Config struct {
	Name string
	DB   *sqlx.DB
}

// Factory opens an adapter from a Config.
type Factory func(Config) (Source, error)

// factories is populated by adapter packages in their init, the same way
// database/sql registers drivers. It is only written during init, before any
// Open call.
var factories = map[string]Factory{}

// Register makes an adapter selectable under name. Adapters call it from init.
func Register(name string, f Factory) {
	factories[name] = f
}

// Available lists the registered adapter names, sorted.
func Available() []string {
	names := make([]string, 0, len(factories))
	for name := range factories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Validate reports whether name selects a usable adapter. An empty name is a
// configuration error, not a default: AdoboFlix refuses to guess a source.
func Validate(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%s is not set: set it explicitly to choose a content source (available: %s)",
			EnvSource, strings.Join(Available(), ", "))
	}
	if _, ok := factories[name]; !ok {
		return fmt.Errorf("unknown source %q in %s (available: %s)",
			name, EnvSource, strings.Join(Available(), ", "))
	}
	return nil
}

// Open validates cfg and opens the selected adapter.
func Open(cfg Config) (Source, error) {
	if err := Validate(cfg.Name); err != nil {
		return nil, err
	}
	return factories[cfg.Name](cfg)
}
