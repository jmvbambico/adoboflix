package epg

import (
	"bytes"
	"compress/gzip"
	"errors"
	"testing"
	"time"
)

// fakeProvider stands in for a source adapter that supplies the compiled
// XMLTV blob. It is deliberately not a database handle: the point of the
// capability is that internal/epg no longer knows or cares where the bytes
// come from.
type fakeProvider struct {
	data []byte
	hash string
	err  error
}

func (f fakeProvider) CompiledEPG() ([]byte, string, error) {
	return f.data, f.hash, f.err
}

func gzipBytes(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte(s)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// TestNewServiceLoadsGzippedXMLTVFromProvider proves the service decodes the
// gzipped XMLTV a source hands it, without any knowledge of a database.
func TestNewServiceLoadsGzippedXMLTVFromProvider(t *testing.T) {
	now := time.Now().UTC()
	start := now.Add(-30 * time.Minute).Format("20060102150405 -0700")
	stop := now.Add(30 * time.Minute).Format("20060102150405 -0700")
	xmltv := `<?xml version="1.0"?>
<tv>
  <channel id="ch.one"><display-name>Channel One</display-name></channel>
  <programme start="` + start + `" stop="` + stop + `" channel="ch.one"><title>Now Playing</title></programme>
</tv>`

	svc := NewService(fakeProvider{data: gzipBytes(t, xmltv), hash: "abc123"})

	resp := svc.GetForChannel("ch.one")
	if resp.ChannelName != "Channel One" {
		t.Errorf("ChannelName = %q, want %q", resp.ChannelName, "Channel One")
	}
	if resp.Current == nil {
		t.Fatal("Current = nil, want the programme currently airing")
	}
	if resp.Current.Title != "Now Playing" {
		t.Errorf("Current.Title = %q, want %q", resp.Current.Title, "Now Playing")
	}
}

// TestNewServiceNonFatalWhenProviderFails pins the fresh-install behavior: an
// empty or missing compiled EPG must still yield a usable service.
func TestNewServiceNonFatalWhenProviderFails(t *testing.T) {
	svc := NewService(fakeProvider{err: errors.New("compiled_epg is empty")})
	if svc == nil {
		t.Fatal("NewService returned nil, want a usable service even with no EPG data")
	}
	resp := svc.GetForChannel("ch.one")
	if resp == nil {
		t.Fatal("GetForChannel returned nil, want an empty response")
	}
	if resp.Current != nil || resp.Next != nil {
		t.Errorf("expected no programmes, got current=%v next=%v", resp.Current, resp.Next)
	}
}

// TestRefreshWithoutProviderIsAnError pins the explicit nil-provider guard:
// Refresh reports an error instead of dereferencing a nil capability.
func TestRefreshWithoutProviderIsAnError(t *testing.T) {
	svc := NewService(nil)
	if svc == nil {
		t.Fatal("NewService(nil) returned nil, want a usable service")
	}
	if err := svc.Refresh(); err == nil {
		t.Fatal("Refresh with no provider = nil error, want failure")
	}
}
