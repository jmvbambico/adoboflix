package source

import (
	"strings"
	"testing"
)

// epgAgnosticSource implements Source but deliberately NOT
// CompiledEPGProvider: it stands in for an adapter whose playlist envelope
// carries no EPG URL, so it has no compiled XMLTV blob to offer.
type epgAgnosticSource struct{ Source }

func (epgAgnosticSource) Name() string { return "adobotv-http" }

// epgCapableSource also implements the optional EPG capability.
type epgCapableSource struct{ Source }

func (epgCapableSource) Name() string { return "postgres-direct" }

func (epgCapableSource) CompiledEPG() ([]byte, string, error) {
	return []byte("gzipped-xmltv"), "hash", nil
}

// TestEPGCapabilityIsOptional pins the whole point of the interface being
// optional: a plain Source is not a CompiledEPGProvider, and one that adds the
// method is. Were CompiledEPGProvider folded into Source, every adapter would
// be forced to fake an EPG feed it cannot supply.
func TestEPGCapabilityIsOptional(t *testing.T) {
	if _, ok := any(epgAgnosticSource{}).(CompiledEPGProvider); ok {
		t.Fatal("a Source without CompiledEPG must not satisfy CompiledEPGProvider")
	}
	if _, ok := any(epgCapableSource{}).(CompiledEPGProvider); !ok {
		t.Fatal("a Source implementing CompiledEPG must satisfy CompiledEPGProvider")
	}
}

func TestUnsupportedEPGErrorNamesTheSource(t *testing.T) {
	err := UnsupportedEPGError("adobotv-http")
	if err == nil {
		t.Fatal("UnsupportedEPGError returned nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "adobotv-http") {
		t.Errorf("error %q does not name the active source", msg)
	}
	if !strings.Contains(msg, "does not provide EPG data") {
		t.Errorf("error %q does not say EPG is unsupported", msg)
	}
}
