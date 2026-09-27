package file

import (
	"reflect"
	"testing"
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
