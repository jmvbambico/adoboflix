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
// DB is a read-only SQL handle, present only for an adapter that declared
// Requirement{Database: true} — every other adapter is opened with a nil
// handle it must ignore.
type Config struct {
	Name string
	DB   *sqlx.DB
	// PlaylistCode is an explicit subscriber credential for an adapter that
	// declared Requirement{PlaylistCode: true}. When empty such an adapter
	// falls back to its own environment key; when a code is supplied here it
	// wins. An adapter that takes no code ignores this field entirely.
	//
	// This is additive and read-only: it carries a credential in, never out,
	// and grants no adapter a write method.
	PlaylistCode string
}

// Factory opens an adapter from a Config.
type Factory func(Config) (Source, error)

// Requirement is what an adapter declares it needs in order to open. It is
// declared once, at registration, so the process can decide whether to open a
// database before calling Open instead of hardcoding adapter names at the
// call site.
type Requirement struct {
	// Database is true when the adapter reads a SQL handle.
	Database bool
	// PlaylistCode is true when the adapter authenticates with a subscriber
	// playlist code the user can enter at runtime (adobotv-http). The server
	// offers the code-entry endpoints only for a source that declares it, the
	// same way it opens a database only for one that asks.
	PlaylistCode bool
}

// registration is a factory plus what it declared it needs.
type registration struct {
	requirement Requirement
	factory     Factory
}

// factories is populated by adapter packages in their init, the same way
// database/sql registers drivers. It is only written during init, before any
// Open call.
var factories = map[string]registration{}

// Register makes an adapter selectable under name, with its declared needs.
// Adapters call it from init.
func Register(name string, req Requirement, f Factory) {
	factories[name] = registration{requirement: req, factory: f}
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

// NeedsDatabase reports whether the adapter registered under name requires a
// SQL handle. It is meaningful for a name Validate accepts; an unknown name
// reports false.
func NeedsDatabase(name string) bool {
	reg, ok := factories[name]
	return ok && reg.requirement.Database
}

// NeedsPlaylistCode reports whether the adapter registered under name
// authenticates with a playlist code the user can enter at runtime. It is
// meaningful for a name Validate accepts; an unknown name reports false.
func NeedsPlaylistCode(name string) bool {
	reg, ok := factories[name]
	return ok && reg.requirement.PlaylistCode
}

// Open validates cfg and opens the selected adapter. An adapter that declared
// a database requirement is never handed a nil handle: a missing handle is an
// error naming the adapter and why it needs one, raised before the factory is
// called.
func Open(cfg Config) (Source, error) {
	if err := Validate(cfg.Name); err != nil {
		return nil, err
	}
	reg := factories[cfg.Name]
	if reg.requirement.Database && cfg.DB == nil {
		return nil, fmt.Errorf("source %q requires a database, but no SQL handle was provided: set ADOBOFLIX_PG_URL or MPDUMPY_PG_URL", cfg.Name)
	}
	return reg.factory(cfg)
}
