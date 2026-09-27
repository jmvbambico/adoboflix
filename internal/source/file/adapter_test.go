package file

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jmvbambico/adoboflix/internal/source"
)

func TestNameAndOptionalCapabilities(t *testing.T) {
	adapter := newFixtureAdapter(t)

	if adapter.Name() != Name || Name != "file" {
		t.Fatalf("Name = %q, want file", adapter.Name())
	}

	var asSource source.Source = adapter
	if _, ok := asSource.(source.StreamProbeLister); !ok {
		t.Error("adapter does not implement source.StreamProbeLister: a local playlist can be enumerated")
	}
	if _, ok := asSource.(source.CompiledEPGProvider); ok {
		t.Error("adapter must NOT implement source.CompiledEPGProvider: a file carries no XMLTV blob")
	}
}

func TestRegistryEntry(t *testing.T) {
	if err := source.Validate(Name); err != nil {
		t.Fatalf("source.Validate(%q) = %v, want nil (init should register it)", Name, err)
	}
	found := false
	for _, name := range source.Available() {
		if name == Name {
			found = true
		}
	}
	if !found {
		t.Errorf("source.Available() = %v, want it to contain %q", source.Available(), Name)
	}
	if source.NeedsDatabase(Name) {
		t.Errorf("NeedsDatabase(%q) = true, want false: this adapter needs no database", Name)
	}
}

func TestNewFromEnvFailsFast(t *testing.T) {
	t.Setenv(EnvPath, "")
	if _, err := NewFromEnv(); err == nil || !strings.Contains(err.Error(), EnvPath) {
		t.Fatalf("NewFromEnv with no path = %v, want an error naming %s", err, EnvPath)
	}

	t.Setenv(EnvPath, "   ")
	if _, err := NewFromEnv(); err == nil || !strings.Contains(err.Error(), EnvPath) {
		t.Fatalf("NewFromEnv with a blank path = %v, want an error naming %s", err, EnvPath)
	}

	t.Setenv(EnvPath, filepath.Join("testdata", "library.json"))
	adapter, err := NewFromEnv()
	if err != nil {
		t.Fatalf("NewFromEnv(valid): %v", err)
	}
	if adapter == nil || adapter.Name() != Name {
		t.Fatalf("NewFromEnv returned %+v", adapter)
	}
}

// An unreadable path is a startup error, never a silent empty library.
func TestMissingFileIsStartupError(t *testing.T) {
	_, err := New(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if !errors.Is(err, ErrLibraryUnreadable) {
		t.Fatalf("New(missing) = %v, want ErrLibraryUnreadable", err)
	}

	dir := t.TempDir()
	_, err = New(dir)
	if !errors.Is(err, ErrLibraryUnreadable) {
		t.Fatalf("New(directory) = %v, want ErrLibraryUnreadable", err)
	}
}

func TestBlankPathIsStartupError(t *testing.T) {
	if _, err := New("  "); err == nil {
		t.Fatal("New(blank) = nil error, want a startup error")
	}
}

func TestMalformedJSONIsStartupError(t *testing.T) {
	_, err := newRawAdapter(t, `{"channels": [`)
	if !errors.Is(err, ErrMalformedLibrary) {
		t.Fatalf("New(malformed) = %v, want ErrMalformedLibrary", err)
	}
	if strings.Contains(err.Error(), "does-not") {
		t.Errorf("error %q should name the path", err)
	}
}

// A well-formed but empty file is a valid empty library, not an error.
func TestEmptyFileIsEmptyLibrary(t *testing.T) {
	adapter, err := newRawAdapter(t, `{}`)
	if err != nil {
		t.Fatalf("New(empty): %v", err)
	}
	entries, total, err := adapter.GetEntries("", "", "", 1, 10)
	if err != nil {
		t.Fatalf("GetEntries: %v", err)
	}
	if total != 0 || len(entries) != 0 {
		t.Errorf("entries = %d, total %d, want empty", len(entries), total)
	}
	providers, err := adapter.GetProviders()
	if err != nil {
		t.Fatalf("GetProviders: %v", err)
	}
	if providers == nil || len(providers) != 0 {
		t.Errorf("providers = %#v, want a non-nil empty slice", providers)
	}
}

func TestGetStats(t *testing.T) {
	adapter := newFixtureAdapter(t)

	stats, err := adapter.GetStats()
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	// 5 entries, 2 distinct providers (local, other), 3 distinct genres.
	if stats.TotalTitles != 5 || stats.TotalProviders != 2 || stats.TotalGenres != 3 {
		t.Errorf("stats = %+v, want titles 5, providers 2, genres 3", stats)
	}
}

// ListStreamsForProbe enumerates live streams in a deterministic order, skips
// URL-less streams, and carries the channel metadata a report needs.
func TestListStreamsForProbe(t *testing.T) {
	adapter := newFixtureAdapter(t)

	targets, err := adapter.ListStreamsForProbe()
	if err != nil {
		t.Fatalf("ListStreamsForProbe: %v", err)
	}
	got := make([]string, 0, len(targets))
	for _, target := range targets {
		got = append(got, target.ChannelName+"/"+target.StreamID)
	}
	// Channels by name: Movies HD, News One, Orphan (no streams). Within a
	// channel, the default stream first; the URL-less "dead" stream is skipped.
	want := []string{
		"Movies HD/mov-main",
		"Movies HD/mov-alt",
		"Movies HD/mov-backup",
		"News One/news-backup",
		"News One/news-primary",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("probe targets = %v, want %v", got, want)
	}

	main := targets[0]
	if main.ChannelID != "movies-hd" || main.Category != "Movies" {
		t.Errorf("first target metadata = %+v", main)
	}
	if main.URL == "" {
		t.Error("first target has no URL to probe")
	}
	for _, target := range targets {
		if target.StreamID == "mov-dead" {
			t.Error("a stream with no URL must be skipped by the probe listing")
		}
	}
}

// A file that omits ids gets stable derived ones, and an id round-trips back
// through GetChannel.
func TestDerivedIDsAreStableAndRoundTrip(t *testing.T) {
	first, err := newRawAdapter(t, derivedFixture)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	second, err := newRawAdapter(t, derivedFixture)
	if err != nil {
		t.Fatalf("New (second): %v", err)
	}

	channels, _, err := first.ListChannels("", 10, 0)
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if len(channels) != 1 {
		t.Fatalf("channels = %d, want 1", len(channels))
	}
	other, _, err := second.ListChannels("", 10, 0)
	if err != nil {
		t.Fatalf("ListChannels (second): %v", err)
	}
	if channels[0].ID != other[0].ID {
		t.Errorf("derived channel id changed between loads: %q != %q", channels[0].ID, other[0].ID)
	}
	if !strings.HasPrefix(channels[0].ID, "ch-") {
		t.Errorf("derived channel id %q lacks the ch- prefix", channels[0].ID)
	}
	got, err := first.GetChannel(channels[0].ID)
	if err != nil {
		t.Fatalf("GetChannel(derived): %v", err)
	}
	if got.Name != "No Id TV" {
		t.Errorf("GetChannel returned %q, want No Id TV", got.Name)
	}

	entries, _, err := first.GetEntries("", "", "", 1, 10)
	if err != nil {
		t.Fatalf("GetEntries: %v", err)
	}
	if len(entries) != 1 || !strings.HasPrefix(entries[0].ID, "vod-") {
		t.Fatalf("derived entry = %+v, want one vod- id", entries)
	}
}

// The JSON envelope is a direct serialisation of the internal models and
// round-trips: parsing, re-marshalling and parsing again yields the same
// library. This is what proves the model json tags are sufficient.
func TestSchemaRoundTrip(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "library.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	first, err := parseLibrary(raw)
	if err != nil {
		t.Fatalf("parseLibrary: %v", err)
	}

	file := libraryFile{
		Channels:   first.channels,
		Entries:    first.entries,
		Streams:    allStreams(first),
		VodStreams: allVodStreams(first),
		Episodes:   allEpisodes(first),
	}
	remarshalled, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	second, err := parseLibrary(remarshalled)
	if err != nil {
		t.Fatalf("parseLibrary (round trip): %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("round trip changed the library:\n first = %#v\nsecond = %#v", first, second)
	}
}

func allStreams(lib *library) []source.Stream {
	out := []source.Stream{}
	for _, ch := range lib.channels {
		out = append(out, lib.streamsByChannel[ch.ID]...)
	}
	return out
}

func allVodStreams(lib *library) []source.VodStream {
	out := []source.VodStream{}
	for _, e := range lib.entries {
		out = append(out, lib.vodStreamsByVod[e.ID]...)
	}
	return out
}

func allEpisodes(lib *library) []source.Episode {
	out := []source.Episode{}
	for _, e := range lib.entries {
		out = append(out, lib.episodesByVod[e.ID]...)
	}
	return out
}
