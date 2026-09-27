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
