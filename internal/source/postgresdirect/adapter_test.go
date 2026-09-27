package postgresdirect

import (
	"strings"
	"testing"

	"github.com/jmvbambico/adoboflix/internal/source"
)

// The harness is the only adapter that reads the schema directly, so it is the
// only one that declares a database requirement.
func TestRequiresDatabase(t *testing.T) {
	if !source.NeedsDatabase(Name) {
		t.Fatalf("NeedsDatabase(%q) = false, want true", Name)
	}
}

// The harness is not a user's choice: it is marked Dev and never Selectable, so
// the UI cannot offer it and only ADOBOFLIX_SOURCE reaches it.
func TestHarnessIsDevAndNotSelectable(t *testing.T) {
	if !source.Dev(Name) {
		t.Errorf("Dev(%q) = false, want true", Name)
	}
	if source.Selectable(Name) {
		t.Errorf("Selectable(%q) = true, want false: it must never be a normal option", Name)
	}
}

// With no handle, Open fails with a clear error naming the adapter rather than
// handing the factory a nil it would dereference.
func TestOpenWithoutHandleFailsClearly(t *testing.T) {
	_, err := source.Open(source.Config{Name: Name})
	if err == nil {
		t.Fatal("Open with no handle = nil error, want a clear failure")
	}
	if !strings.Contains(err.Error(), Name) {
		t.Errorf("error %q does not name %q", err, Name)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "database") {
		t.Errorf("error %q does not say why a handle is needed", err)
	}
}

// New(nil) is the adapter's own last line of defence against a nil handle.
func TestNewRejectsNilHandle(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("New(nil) = nil error, want a failure")
	}
}

// AccountInfoProvider describes a subscriber's AdoboTV account; the development
// tap reads the schema directly and has no account concept, so it must not
// implement the capability. A typed nil is enough: this is a compile-time
// property, and the assertion never calls a method.
func TestDoesNotImplementAccountInfoProvider(t *testing.T) {
	var adapter *Adapter
	var asSource source.Source = adapter
	if _, ok := asSource.(source.AccountInfoProvider); ok {
		t.Error("postgres-direct must NOT implement source.AccountInfoProvider: it has no account concept")
	}
}
