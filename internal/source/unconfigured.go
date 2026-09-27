package source

import "errors"

// ErrNotConfigured is what every read returns while AdoboFlix has no content
// source. A server with neither ADOBOFLIX_SOURCE nor a remembered mode boots in
// this state on purpose so the UI can offer the user the two real paths; it is
// a valid state, not a startup failure. Handlers surface it as a distinct,
// actionable answer rather than an empty library, which would be
// indistinguishable from a broken one.
var ErrNotConfigured = errors.New("no content source is configured")

// Unconfigured returns a Source that answers every read with ErrNotConfigured.
// The server uses it as the active source when the user has not chosen a mode,
// so the player handlers never hold a nil and never need a nil check: every
// route fails cleanly with one recognizable error until a source is chosen.
//
// It is not registered under a name and cannot be selected: it exists only to
// make "no source" a first-class state within the same read-only boundary every
// adapter implements.
func Unconfigured() Source { return unconfigured{} }

type unconfigured struct{}

// Name is empty, which is how callers tell "no source" from a real adapter and
// why status reports an empty source string in that state.
func (unconfigured) Name() string { return "" }

func (unconfigured) GetStats() (*Stats, error) { return nil, ErrNotConfigured }

func (unconfigured) GetEntries(string, string, string, int, int) ([]Entry, int, error) {
	return nil, 0, ErrNotConfigured
}

func (unconfigured) GetEntry(string) (*Entry, error) { return nil, ErrNotConfigured }

func (unconfigured) Search(string, string, string, string, int, int) ([]Entry, int, error) {
	return nil, 0, ErrNotConfigured
}

func (unconfigured) GetProviders() ([]string, error) { return nil, ErrNotConfigured }

func (unconfigured) GetGenres() ([]string, error) { return nil, ErrNotConfigured }

func (unconfigured) EpisodeCounts([]string) (map[string]int, error) {
	return nil, ErrNotConfigured
}

func (unconfigured) GetVodStreams(string) ([]VodStream, error) { return nil, ErrNotConfigured }

func (unconfigured) GetEpisodes(string) ([]Episode, error) { return nil, ErrNotConfigured }

func (unconfigured) GetEpisode(string) (*Episode, error) { return nil, ErrNotConfigured }

func (unconfigured) ListChannels(string, int, int) ([]Channel, int, error) {
	return nil, 0, ErrNotConfigured
}

func (unconfigured) ListChannelCategories() ([]string, error) { return nil, ErrNotConfigured }

func (unconfigured) GetChannel(string) (*Channel, error) { return nil, ErrNotConfigured }

func (unconfigured) GetChannelWithStreams(string) (*Channel, []Stream, error) {
	return nil, nil, ErrNotConfigured
}

func (unconfigured) ResolveChannelStream(string) (*Stream, error) { return nil, ErrNotConfigured }
