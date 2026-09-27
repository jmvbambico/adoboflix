package source

import (
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
)

// fakeSource is only here to prove registration/opening works without a
// database. It implements Source trivially.
type fakeSource struct{ Source }

func TestValidateRejectsUnsetSource(t *testing.T) {
	for _, name := range []string{"", "   "} {
		err := Validate(name)
		if err == nil {
			t.Fatalf("Validate(%q) = nil, want an error", name)
		}
		if !strings.Contains(err.Error(), EnvSource) {
			t.Errorf("Validate(%q) error %q does not name %s", name, err, EnvSource)
		}
	}
}

func TestValidateRejectsUnknownSource(t *testing.T) {
	err := Validate("no-such-adapter")
	if err == nil {
		t.Fatal("Validate(unknown) = nil, want an error")
	}
	if !strings.Contains(err.Error(), "no-such-adapter") {
		t.Errorf("error %q does not name the rejected source", err)
	}
}

func TestOpenRequiresConfiguredSource(t *testing.T) {
	if _, err := Open(Config{}); err == nil {
		t.Fatal("Open with no source configured = nil error, want failure")
	}
	// An unconfigured source must never resolve to some default adapter.
	if _, err := Open(Config{}); !strings.Contains(err.Error(), EnvSource) {
		t.Errorf("Open error does not name %s: %v", EnvSource, err)
	}
}

func TestOpenUsesRegisteredFactory(t *testing.T) {
	const name = "test-only-adapter"
	want := &fakeSource{}
	Register(name, Requirement{}, func(Config) (Source, error) { return want, nil })

	got, err := Open(Config{Name: name})
	if err != nil {
		t.Fatalf("Open(%q): %v", name, err)
	}
	if got != want {
		t.Errorf("Open(%q) = %v, want the registered source", name, got)
	}
}

func TestRegisteredSourceIsAvailable(t *testing.T) {
	const name = "test-only-adapter-2"
	Register(name, Requirement{}, func(Config) (Source, error) { return &fakeSource{}, nil })

	for _, n := range Available() {
		if n == name {
			return
		}
	}
	t.Errorf("Available() = %v, want it to contain %q", Available(), name)
}

// An adapter that declares no database need opens with a nil handle, and the
// nil is passed through unchanged.
func TestOpenPassesNilHandleToDatabaseAgnosticAdapter(t *testing.T) {
	const name = "test-no-db-adapter"
	var gotDB *sqlx.DB
	want := &fakeSource{}
	Register(name, Requirement{}, func(cfg Config) (Source, error) {
		gotDB = cfg.DB
		return want, nil
	})

	got, err := Open(Config{Name: name})
	if err != nil {
		t.Fatalf("Open(%q): %v", name, err)
	}
	if got != want {
		t.Errorf("Open returned %v, want the registered source", got)
	}
	if gotDB != nil {
		t.Errorf("factory received handle %v, want nil", gotDB)
	}
	if NeedsDatabase(name) {
		t.Errorf("NeedsDatabase(%q) = true, want false", name)
	}
}

// A database-requiring adapter is never handed a nil handle: Open refuses
// before the factory runs.
func TestOpenRefusesNilHandleForDatabaseAdapter(t *testing.T) {
	const name = "test-db-adapter"
	called := false
	Register(name, Requirement{Database: true}, func(Config) (Source, error) {
		called = true
		return &fakeSource{}, nil
	})

	if !NeedsDatabase(name) {
		t.Fatalf("NeedsDatabase(%q) = false, want true", name)
	}

	_, err := Open(Config{Name: name})
	if err == nil {
		t.Fatal("Open with a nil handle = nil error, want a clear failure")
	}
	if !strings.Contains(err.Error(), name) {
		t.Errorf("error %q does not name the adapter", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "database") {
		t.Errorf("error %q does not say why the adapter needs a handle", err)
	}
	if called {
		t.Error("the factory ran despite the missing handle; Open must fail before it")
	}
}

// With a handle supplied, a database-requiring adapter opens normally.
func TestOpenPassesHandleToDatabaseAdapter(t *testing.T) {
	const name = "test-db-adapter-with-handle"
	handle := &sqlx.DB{}
	var gotDB *sqlx.DB
	Register(name, Requirement{Database: true}, func(cfg Config) (Source, error) {
		gotDB = cfg.DB
		return &fakeSource{}, nil
	})

	if _, err := Open(Config{Name: name, DB: handle}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if gotDB != handle {
		t.Errorf("factory received %v, want the supplied handle", gotDB)
	}
}

// NeedsDatabase reports false for a name that is not registered.
func TestNeedsDatabaseUnknownName(t *testing.T) {
	if NeedsDatabase("no-such-adapter") {
		t.Error("NeedsDatabase(unknown) = true, want false")
	}
}

// An adapter declares its playlist-code need at registration, the same way it
// declares a database need, so the server can offer the code-entry endpoints
// without hardcoding an adapter name.
func TestNeedsPlaylistCodeFollowsRegistration(t *testing.T) {
	const needs = "test-code-adapter"
	const needsNot = "test-no-code-adapter"
	Register(needs, Requirement{PlaylistCode: true}, func(Config) (Source, error) { return &fakeSource{}, nil })
	Register(needsNot, Requirement{}, func(Config) (Source, error) { return &fakeSource{}, nil })

	if !NeedsPlaylistCode(needs) {
		t.Errorf("NeedsPlaylistCode(%q) = false, want true", needs)
	}
	if NeedsPlaylistCode(needsNot) {
		t.Errorf("NeedsPlaylistCode(%q) = true, want false", needsNot)
	}
	if NeedsPlaylistCode("no-such-adapter") {
		t.Error("NeedsPlaylistCode(unknown) = true, want false")
	}
}

// An explicit playlist code is handed to a factory that asks for one, and the
// field is ignored (passed through unchanged) for an adapter that does not.
func TestOpenPassesPlaylistCodeToAdapter(t *testing.T) {
	const name = "test-code-passthrough-adapter"
	var got string
	Register(name, Requirement{PlaylistCode: true}, func(cfg Config) (Source, error) {
		got = cfg.PlaylistCode
		return &fakeSource{}, nil
	})

	if _, err := Open(Config{Name: name, PlaylistCode: "  SECRET  "}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Open does not interpret the value; the adapter trims and validates it.
	if got != "  SECRET  " {
		t.Errorf("factory received %q, want the code passed through unchanged", got)
	}

	const plain = "test-no-code-passthrough-adapter"
	var plainGot string
	Register(plain, Requirement{}, func(cfg Config) (Source, error) {
		plainGot = cfg.PlaylistCode
		return &fakeSource{}, nil
	})
	if _, err := Open(Config{Name: plain, PlaylistCode: "ignored"}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if plainGot != "ignored" {
		t.Errorf("factory received %q; Config is passed through verbatim", plainGot)
	}
}
