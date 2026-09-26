package source

import (
	"strings"
	"testing"
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
	Register(name, func(Config) (Source, error) { return want, nil })

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
	Register(name, func(Config) (Source, error) { return &fakeSource{}, nil })

	for _, n := range Available() {
		if n == name {
			return
		}
	}
	t.Errorf("Available() = %v, want it to contain %q", Available(), name)
}
