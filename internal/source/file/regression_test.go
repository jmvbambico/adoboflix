package file

import (
	"reflect"
	"sort"
	"testing"

	"github.com/jmvbambico/adoboflix/internal/source"
)

func mustRawAdapter(t *testing.T, contents string) *Adapter {
	t.Helper()
	adapter, err := newRawAdapter(t, contents)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return adapter
}

// BLOCKING 1: episodes whose season/episode numbers are omitted both unmarshal
// to 0. They must not hash to the same id and get dropped — N in, N out.
func TestUnnumberedEpisodesAreKept(t *testing.T) {
	adapter := mustRawAdapter(t, `{
	  "entries": [{"id": "s", "name": "Series", "type": "Series", "category": "Shows", "provider": "local", "status": "active"}],
	  "episodes": [
	    {"vod_id": "s", "name": "One", "stream_url": "https://cdn.example/1.m3u8"},
	    {"vod_id": "s", "name": "Two", "stream_url": "https://cdn.example/2.m3u8"},
	    {"vod_id": "s", "name": "Three", "stream_url": "https://cdn.example/3.m3u8"}
	  ]
	}`)

	episodes, err := adapter.GetEpisodes("s")
	if err != nil {
		t.Fatalf("GetEpisodes: %v", err)
	}
	if len(episodes) != 3 {
		t.Fatalf("episodes = %d, want 3 (all unnumbered episodes must survive)", len(episodes))
	}
	seen := map[string]bool{}
	for _, ep := range episodes {
		if seen[ep.ID] {
			t.Errorf("duplicate episode id %q", ep.ID)
		}
		seen[ep.ID] = true
	}

	counts, err := adapter.EpisodeCounts([]string{"s"})
	if err != nil {
		t.Fatalf("EpisodeCounts: %v", err)
	}
	if counts["s"] != 3 {
		t.Errorf("episode count = %d, want 3", counts["s"])
	}
}

// Rows that are identical in every field still must not disappear silently:
// the derived-id guard disambiguates them.
func TestIdenticalEpisodesAreKept(t *testing.T) {
	adapter := mustRawAdapter(t, `{
	  "entries": [{"id": "s", "name": "Series", "type": "Series", "category": "Shows", "provider": "local", "status": "active"}],
	  "episodes": [
	    {"vod_id": "s", "name": "Same", "stream_url": "https://cdn.example/same.m3u8"},
	    {"vod_id": "s", "name": "Same", "stream_url": "https://cdn.example/same.m3u8"}
	  ]
	}`)

	episodes, err := adapter.GetEpisodes("s")
	if err != nil {
		t.Fatalf("GetEpisodes: %v", err)
	}
	if len(episodes) != 2 {
		t.Fatalf("episodes = %d, want 2", len(episodes))
	}
	if episodes[0].ID == episodes[1].ID {
		t.Errorf("both identical episodes share id %q", episodes[0].ID)
	}
}

// A playlist that pads a category/provider with whitespace advertises the
// trimmed value; feeding that value straight back as a filter must match.
func TestAdvertisedValuesRoundTripThroughFilters(t *testing.T) {
	adapter := mustRawAdapter(t, `{
	  "channels": [{"id": "c", "name": "C", "category": " News ", "status": "active"}],
	  "entries": [
	    {"id": "e", "name": "E", "type": " Movie ", "category": " Films ", "provider": " local ", "status": "active"}
	  ]
	}`)

	genres, err := adapter.GetGenres()
	if err != nil {
		t.Fatalf("GetGenres: %v", err)
	}
	if !reflect.DeepEqual(genres, []string{"Films"}) {
		t.Fatalf("genres = %v, want [Films]", genres)
	}
	for _, g := range genres {
		entries, total, err := adapter.GetEntries("", g, "", 1, 10)
		if err != nil {
			t.Fatalf("GetEntries(genre=%q): %v", g, err)
		}
		if total == 0 || len(entries) == 0 {
			t.Errorf("genre %q advertised but filters to nothing", g)
		}
	}

	providers, err := adapter.GetProviders()
	if err != nil {
		t.Fatalf("GetProviders: %v", err)
	}
	if !reflect.DeepEqual(providers, []string{"local"}) {
		t.Fatalf("providers = %v, want [local]", providers)
	}
	for _, p := range providers {
		_, total, err := adapter.GetEntries(p, "", "", 1, 10)
		if err != nil {
			t.Fatalf("GetEntries(provider=%q): %v", p, err)
		}
		if total == 0 {
			t.Errorf("provider %q advertised but filters to nothing", p)
		}
	}

	categories, err := adapter.ListChannelCategories()
	if err != nil {
		t.Fatalf("ListChannelCategories: %v", err)
	}
	if !reflect.DeepEqual(categories, []string{"News"}) {
		t.Fatalf("categories = %v, want [News]", categories)
	}
	for _, c := range categories {
		channels, total, err := adapter.ListChannels(c, 10, 0)
		if err != nil {
			t.Fatalf("ListChannels(%q): %v", c, err)
		}
		if total == 0 || len(channels) == 0 {
			t.Errorf("category %q advertised but filters to nothing", c)
		}
	}

	// The trimmed type still filters too.
	if _, total, err := adapter.GetEntries("", "", "Movie", 1, 10); err != nil || total != 1 {
		t.Errorf("content type Movie: total %d, err %v, want 1", total, err)
	}

	stats, err := adapter.GetStats()
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.TotalProviders != 1 || stats.TotalGenres != 1 {
		t.Errorf("stats = %+v, want 1 provider and 1 genre", stats)
	}
}

// NIT 5: values differing only by case filter identically, so they must be
// advertised once — not twice, with a doubled count.
func TestAdvertisedValuesAreCaseInsensitivelyDeduped(t *testing.T) {
	adapter := mustRawAdapter(t, `{
	  "entries": [
	    {"id": "a", "name": "A", "type": "Movie", "category": "Action", "provider": "Local", "status": "active"},
	    {"id": "b", "name": "B", "type": "Movie", "category": "action", "provider": "local", "status": "active"}
	  ]
	}`)

	genres, err := adapter.GetGenres()
	if err != nil {
		t.Fatalf("GetGenres: %v", err)
	}
	if len(genres) != 1 {
		t.Errorf("genres = %v, want one entry for Action/action", genres)
	}
	providers, err := adapter.GetProviders()
	if err != nil {
		t.Fatalf("GetProviders: %v", err)
	}
	if len(providers) != 1 {
		t.Errorf("providers = %v, want one entry for Local/local", providers)
	}

	// The single advertised value still filters to both rows.
	for _, g := range genres {
		_, total, err := adapter.GetEntries("", g, "", 1, 10)
		if err != nil {
			t.Fatalf("GetEntries(genre=%q): %v", g, err)
		}
		if total != 2 {
			t.Errorf("genre %q matched %d entries, want 2", g, total)
		}
	}

	stats, err := adapter.GetStats()
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.TotalProviders != 1 || stats.TotalGenres != 1 {
		t.Errorf("stats = %+v, want 1 provider and 1 genre", stats)
	}
}

// NON-BLOCKING 3: postgres-direct orders "category NULLS LAST", so an
// uncategorised channel sorts after the categorised ones, not before them.
func TestUncategorisedChannelsSortLast(t *testing.T) {
	adapter := mustRawAdapter(t, `{
	  "channels": [
	    {"id": "none", "name": "No Category", "status": "active"},
	    {"id": "news", "name": "News One", "category": "News", "status": "active"},
	    {"id": "sports", "name": "Sports One", "category": "Sports", "status": "active"}
	  ],
	  "streams": [
	    {"id": "s-none", "channel_id": "none", "label": "m", "url": "https://cdn.example/none.m3u8", "status": "active"},
	    {"id": "s-news", "channel_id": "news", "label": "m", "url": "https://cdn.example/news.m3u8", "status": "active"}
	  ]
	}`)

	channels, _, err := adapter.ListChannels("", 10, 0)
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if want := []string{"News One", "Sports One", "No Category"}; !reflect.DeepEqual(channelNames(channels), want) {
		t.Errorf("channel order = %v, want %v (NULLS LAST)", channelNames(channels), want)
	}

	targets, err := adapter.ListStreamsForProbe()
	if err != nil {
		t.Fatalf("ListStreamsForProbe: %v", err)
	}
	got := make([]string, 0, len(targets))
	for _, target := range targets {
		got = append(got, target.ChannelName)
	}
	if want := []string{"News One", "No Category"}; !reflect.DeepEqual(got, want) {
		t.Errorf("probe channel order = %v, want %v (NULLS LAST)", got, want)
	}
}

// NON-BLOCKING 4: what GetChannelWithStreams lists first must be what
// ResolveChannelStream returns. Channel streams rank on "active"; an "online"
// stream is not the fallback ResolveChannelStream uses.
func TestResolveAndSortAgreeOnChannelStreams(t *testing.T) {
	adapter := mustRawAdapter(t, `{
	  "channels": [{"id": "c", "name": "C", "category": "News", "status": "active"}],
	  "streams": [
	    {"id": "a-online", "channel_id": "c", "label": "a", "url": "https://cdn.example/a.m3u8", "status": "online"},
	    {"id": "b-active", "channel_id": "c", "label": "b", "url": "https://cdn.example/b.m3u8", "status": "active"}
	  ]
	}`)

	_, streams, err := adapter.GetChannelWithStreams("c")
	if err != nil {
		t.Fatalf("GetChannelWithStreams: %v", err)
	}
	if len(streams) != 2 || streams[0].ID != "b-active" {
		t.Fatalf("stream order = %+v, want the active stream first", streams)
	}

	resolved, err := adapter.ResolveChannelStream("c")
	if err != nil {
		t.Fatalf("ResolveChannelStream: %v", err)
	}
	if resolved.ID != streams[0].ID {
		t.Errorf("ResolveChannelStream returned %q but the list puts %q first", resolved.ID, streams[0].ID)
	}
}

// VOD ranking is deliberately its own reference: postgres-direct's
// GetVodStreams orders on status = 'online', so an online stream outranks an
// active one (the default-before-status rule still applies first).
func TestVodStreamsRankOnlineFirst(t *testing.T) {
	adapter := mustRawAdapter(t, `{
	  "entries": [{"id": "v", "name": "V", "type": "Movie", "category": "Films", "provider": "local", "status": "active"}],
	  "vod_streams": [
	    {"id": "a-active", "vod_id": "v", "label": "a", "url": "https://cdn.example/a.m3u8", "status": "active"},
	    {"id": "b-online", "vod_id": "v", "label": "b", "url": "https://cdn.example/b.m3u8", "status": "online"}
	  ]
	}`)

	streams, err := adapter.GetVodStreams("v")
	if err != nil {
		t.Fatalf("GetVodStreams: %v", err)
	}
	if len(streams) != 2 || streams[0].ID != "b-online" {
		t.Fatalf("vod stream order = %+v, want the online stream first", streams)
	}
}

// BLOCKING 1 (delta): file-provided stream ids are reserved, so a derived id
// can never take one. The second row derives exactly the id the first row
// spells out; it must be given a different one instead.
func TestDerivedStreamIDDoesNotTakeProvidedStreamID(t *testing.T) {
	const streamURL = "https://cdn.example/live1.m3u8"
	explicit := derivedStreamID(source.Stream{ChannelID: "c1", Label: "Main", URL: streamURL})

	adapter := mustRawAdapter(t, `{
	  "channels": [{"id": "c1", "name": "Live Channel", "status": "active"}],
	  "streams": [
	    {"id": "`+explicit+`", "channel_id": "c1", "label": "Main", "url": "`+streamURL+`"},
	    {"channel_id": "c1", "label": "Main", "url": "`+streamURL+`"}
	  ]
	}`)

	_, streams, err := adapter.GetChannelWithStreams("c1")
	if err != nil {
		t.Fatalf("GetChannelWithStreams: %v", err)
	}
	if len(streams) != 2 {
		t.Fatalf("streams = %d, want 2", len(streams))
	}
	if streams[0].ID == streams[1].ID {
		t.Fatalf("a derived id took the provided id %q; both streams share it", explicit)
	}
	remaining := false
	for _, s := range streams {
		if s.ID == explicit {
			remaining = true
		}
	}
	if !remaining {
		t.Errorf("the file-provided id %q was not preserved on its own row", explicit)
	}
}

// The deliberate decision the parseLibrary doc states: stream rows are never
// deduplicated, even when the file reuses an id, because no interface method
// looks a stream up by id and two rows under a channel are two streams.
func TestDuplicateProvidedStreamIDsAreKept(t *testing.T) {
	adapter := mustRawAdapter(t, `{
	  "channels": [{"id": "c1", "name": "C", "status": "active"}],
	  "streams": [
	    {"id": "dup", "channel_id": "c1", "label": "a", "url": "https://cdn.example/a.m3u8", "status": "active"},
	    {"id": "dup", "channel_id": "c1", "label": "b", "url": "https://cdn.example/b.m3u8", "status": "active"}
	  ]
	}`)

	_, streams, err := adapter.GetChannelWithStreams("c1")
	if err != nil {
		t.Fatalf("GetChannelWithStreams: %v", err)
	}
	if len(streams) != 2 {
		t.Fatalf("streams = %d, want 2 (stream rows are not deduplicated)", len(streams))
	}
	for _, s := range streams {
		if s.ID != "dup" {
			t.Errorf("provided id changed to %q; explicit ids are used verbatim", s.ID)
		}
	}
}

// BLOCKING 2 (delta): a suffixed derived id must not displace a later row that
// legitimately owns that exact id.
func TestExplicitIDSurvivesDerivedCollision(t *testing.T) {
	category := "Action"
	base := derivedEntryID(source.Entry{Name: "Dup", Category: &category})

	adapter := mustRawAdapter(t, `{"entries":[
	  {"name":"Dup","category":"Action"},
	  {"name":"Dup","category":"Action"},
	  {"id":"`+base+`-2","name":"Independent Title","category":"Comedy"}]}`)

	entries, total, err := adapter.GetEntries("", "", "", 1, 50)
	if err != nil {
		t.Fatalf("GetEntries: %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3: the explicit row must not be displaced by a derived id", total)
	}
	found := false
	for _, e := range entries {
		if e.Name == "Independent Title" {
			found = true
		}
	}
	if !found {
		t.Error("the row with an explicit id was dropped")
	}
}

// A row's survival must not depend on where it sits relative to an unrelated
// collision: explicit-id-first and explicit-id-last must keep the same rows.
func TestRowSurvivalIsIndependentOfFileOrder(t *testing.T) {
	category := "Action"
	base := derivedEntryID(source.Entry{Name: "Dup", Category: &category})
	explicit := `{"id":"` + base + `-2","name":"Independent Title","category":"Comedy"}`
	dup := `{"name":"Dup","category":"Action"}`

	withExplicitLast := mustRawAdapter(t, `{"entries":[`+dup+`,`+dup+`,`+explicit+`]}`)
	withExplicitFirst := mustRawAdapter(t, `{"entries":[`+explicit+`,`+dup+`,`+dup+`]}`)

	last := sortedEntryNames(t, withExplicitLast)
	first := sortedEntryNames(t, withExplicitFirst)
	if !reflect.DeepEqual(last, first) {
		t.Fatalf("surviving rows depend on file order: last=%v first=%v", last, first)
	}
	if len(last) != 3 {
		t.Errorf("entries = %v, want 3 rows in both orders", last)
	}
}

// NON-BLOCKING 3 (delta): a file-provided id is trimmed when stored, and the
// channel_id/vod_id references are trimmed with it, so a padded id is still
// looked up by its trimmed form.
func TestPaddedIDsAndReferencesAreNormalized(t *testing.T) {
	adapter := mustRawAdapter(t, `{
	  "channels": [{"id": "  ch-padded  ", "name": "Padded", "category": "News", "status": "active"}],
	  "entries": [{"id": "  vod-padded  ", "name": "Padded Movie", "type": "Movie", "category": "Films", "provider": "local", "status": "active"}],
	  "episodes": [{"id": "  ep-padded  ", "vod_id": "  vod-padded  ", "season_number": 1, "episode_number": 1, "name": "P1", "stream_url": "https://cdn.example/p1.m3u8", "source_type": "local"}],
	  "streams": [{"id": "  str-padded  ", "channel_id": "  ch-padded  ", "label": "m", "url": "https://cdn.example/s.m3u8", "status": "active"}],
	  "vod_streams": [{"id": "  vstr-padded  ", "vod_id": "  vod-padded  ", "label": "m", "url": "https://cdn.example/vs.m3u8", "status": "online"}]
	}`)

	channel, err := adapter.GetChannel("ch-padded")
	if err != nil {
		t.Fatalf("GetChannel(trimmed): %v", err)
	}
	if channel.ID != "ch-padded" {
		t.Errorf("channel id = %q, want the trimmed form", channel.ID)
	}
	if _, err := adapter.GetEntry("vod-padded"); err != nil {
		t.Fatalf("GetEntry(trimmed): %v", err)
	}
	if _, err := adapter.GetEpisode("ep-padded"); err != nil {
		t.Fatalf("GetEpisode(trimmed): %v", err)
	}

	episodes, err := adapter.GetEpisodes("vod-padded")
	if err != nil {
		t.Fatalf("GetEpisodes(trimmed): %v", err)
	}
	if len(episodes) != 1 {
		t.Errorf("episodes = %d, want 1: the vod_id reference must be trimmed", len(episodes))
	}

	if _, streams, err := adapter.GetChannelWithStreams("ch-padded"); err != nil {
		t.Fatalf("GetChannelWithStreams(trimmed): %v", err)
	} else if len(streams) != 1 || streams[0].ID != "str-padded" {
		t.Errorf("streams = %+v, want one str-padded: the channel_id reference must be trimmed", streams)
	}
	if _, err := adapter.GetVodStreams("vod-padded"); err != nil {
		t.Fatalf("GetVodStreams(trimmed): %v", err)
	}

	channels, _, err := adapter.ListChannels("", 10, 0)
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if len(channels) != 1 || channels[0].ID != "ch-padded" {
		t.Errorf("listed channel id = %q, want the trimmed form", channels[0].ID)
	}
}

func sortedEntryNames(t *testing.T, adapter *Adapter) []string {
	t.Helper()
	entries, _, err := adapter.GetEntries("", "", "", 1, 50)
	if err != nil {
		t.Fatalf("GetEntries: %v", err)
	}
	names := entryNames(entries)
	sort.Strings(names)
	return names
}
