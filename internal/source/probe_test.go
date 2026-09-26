package source

import (
	"strings"
	"testing"
)

// probeAgnosticSource implements Source but deliberately NOT
// StreamProbeLister: it stands in for an adapter (an HTTP adapter that only
// ever sees one user's playlist) that cannot enumerate the whole library.
type probeAgnosticSource struct{ Source }

func (probeAgnosticSource) Name() string { return "adobotv-http" }

// probeCapableSource also implements the optional capability.
type probeCapableSource struct{ Source }

func (probeCapableSource) Name() string { return "postgres-direct" }

func (probeCapableSource) ListStreamsForProbe() ([]ProbeTarget, error) {
	return []ProbeTarget{{StreamID: "s1", URL: "https://example.com/index.mpd"}}, nil
}

// TestProbeCapabilityIsOptional pins the whole point of the interface being
// optional: a plain Source is not a StreamProbeLister, and one that adds the
// method is. Were StreamProbeLister folded into Source, every adapter would be
// forced to fake an enumeration it cannot perform.
func TestProbeCapabilityIsOptional(t *testing.T) {
	if _, ok := any(probeAgnosticSource{}).(StreamProbeLister); ok {
		t.Fatal("a Source without ListStreamsForProbe must not satisfy StreamProbeLister")
	}
	if _, ok := any(probeCapableSource{}).(StreamProbeLister); !ok {
		t.Fatal("a Source implementing ListStreamsForProbe must satisfy StreamProbeLister")
	}
}

func TestUnsupportedScanErrorNamesTheSource(t *testing.T) {
	err := UnsupportedScanError("adobotv-http")
	if err == nil {
		t.Fatal("UnsupportedScanError returned nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "adobotv-http") {
		t.Errorf("error %q does not name the active source", msg)
	}
	if !strings.Contains(msg, "does not support health scanning") {
		t.Errorf("error %q does not say scanning is unsupported", msg)
	}
}
