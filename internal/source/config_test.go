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
