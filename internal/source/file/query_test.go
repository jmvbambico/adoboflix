package file

import (
	"errors"
	"reflect"
	"testing"

	"github.com/jmvbambico/adoboflix/internal/source"
)

func entryNames(entries []source.Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}

func TestGetEntriesNewestFirst(t *testing.T) {
	adapter := newFixtureAdapter(t)

	entries, total, err := adapter.GetEntries("", "", "", 1, 50)
	if err != nil {
		t.Fatalf("GetEntries: %v", err)
	}
	if total != 5 {
		t.Fatalf("total = %d, want 5", total)
	}
	// created_at descending; the entry with no timestamp sorts last.
	want := []string{"Series E", "Movie D", "Series B", "Movie A", "Documentary C"}
	if got := entryNames(entries); !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestGetEntriesFilters(t *testing.T) {
	adapter := newFixtureAdapter(t)

	cases := []struct {
		name        string
		provider    string
		genre       string
		contentType string
		want        []string
	}{
		{"provider local", "local", "", "", []string{"Movie D", "Series B", "Movie A"}},
		{"provider other", "other", "", "", []string{"Series E", "Documentary C"}},
		{"provider case-insensitive", "LOCAL", "", "", []string{"Movie D", "Series B", "Movie A"}},
		{"genre films", "", "Films", "", []string{"Movie D", "Movie A"}},
		{"genre case-insensitive", "", "shows", "", []string{"Series E", "Series B"}},
		{"content type series", "", "", "Series", []string{"Series E", "Series B"}},
		{"content type movie", "", "", "movie", []string{"Movie D", "Movie A", "Documentary C"}},
		{"combined", "local", "Films", "Movie", []string{"Movie D", "Movie A"}},
		{"unknown provider", "nope", "", "", []string{}},
		{"unknown genre", "", "nope", "", []string{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries, total, err := adapter.GetEntries(tc.provider, tc.genre, tc.contentType, 1, 50)
			if err != nil {
				t.Fatalf("GetEntries: %v", err)
			}
			if got := entryNames(entries); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("entries = %v, want %v", got, tc.want)
			}
			if total != len(tc.want) {
				t.Errorf("total = %d, want %d", total, len(tc.want))
			}
		})
	}
}

func TestGetEntriesPaging(t *testing.T) {
	adapter := newFixtureAdapter(t)

	cases := []struct {
		name      string
		page      int
		limit     int
		wantNames []string
		wantTotal int
	}{
		{"first page", 1, 2, []string{"Series E", "Movie D"}, 5},
		{"second page", 2, 2, []string{"Series B", "Movie A"}, 5},
		{"last page", 3, 2, []string{"Documentary C"}, 5},
		{"past the end", 4, 2, []string{}, 5},
		{"page zero is the first page", 0, 2, []string{"Series E", "Movie D"}, 5},
		{"negative page is the first page", -3, 2, []string{"Series E", "Movie D"}, 5},
		{"limit zero means no limit", 1, 0, []string{"Series E", "Movie D", "Series B", "Movie A", "Documentary C"}, 5},
		{"limit zero past page one is empty", 2, 0, []string{}, 5},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries, total, err := adapter.GetEntries("", "", "", tc.page, tc.limit)
			if err != nil {
				t.Fatalf("GetEntries: %v", err)
			}
			if got := entryNames(entries); !reflect.DeepEqual(got, tc.wantNames) {
				t.Errorf("entries = %v, want %v", got, tc.wantNames)
			}
			if total != tc.wantTotal {
				t.Errorf("total = %d, want %d", total, tc.wantTotal)
			}
		})
	}
}

func TestSearchHitsAndMisses(t *testing.T) {
	adapter := newFixtureAdapter(t)

	cases := []struct {
		name string
		q    string
		want []string
	}{
		{"case-insensitive substring", "movie", []string{"Movie D", "Movie A"}},
		{"matches anywhere in the name", "series", []string{"Series E", "Series B"}},
		{"no match", "zzz", []string{}},
		{"empty query matches everything", "", []string{"Series E", "Movie D", "Series B", "Movie A", "Documentary C"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results, total, err := adapter.Search(tc.q, "", "", "", 1, 50)
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if got := entryNames(results); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("results = %v, want %v", got, tc.want)
			}
			if total != len(tc.want) {
				t.Errorf("total = %d, want %d", total, len(tc.want))
			}
		})
	}

	// Search combines the name match with the same filters as GetEntries.
	results, total, err := adapter.Search("movie", "", "", "Series", 1, 50)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if total != 0 || len(results) != 0 {
		t.Errorf("Search(movie, contentType=Series) = %v, want none", entryNames(results))
	}
}

// Ids with no episodes are absent from the map, not present with a zero.
func TestEpisodeCountsAbsenceSemantics(t *testing.T) {
	adapter := newFixtureAdapter(t)

	counts, err := adapter.EpisodeCounts([]string{"vod-series", "vod-movie", "vod-unknown"})
	if err != nil {
		t.Fatalf("EpisodeCounts: %v", err)
	}
	if len(counts) != 1 {
		t.Fatalf("counts = %v, want only vod-series", counts)
	}
	if counts["vod-series"] != 3 {
		t.Errorf("vod-series count = %d, want 3", counts["vod-series"])
	}
	if _, present := counts["vod-movie"]; present {
		t.Error("vod-movie has no episodes and must be absent")
	}
	if _, present := counts["vod-unknown"]; present {
		t.Error("an unknown id must be absent")
	}

	empty, err := adapter.EpisodeCounts(nil)
	if err != nil {
		t.Fatalf("EpisodeCounts(nil): %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Errorf("counts = %v, want a non-nil empty map", empty)
	}
}

func TestGetVodStreamsDefaultFirst(t *testing.T) {
	adapter := newFixtureAdapter(t)

	streams, err := adapter.GetVodStreams("vod-movie")
	if err != nil {
		t.Fatalf("GetVodStreams: %v", err)
	}
	if len(streams) != 2 {
		t.Fatalf("streams = %d, want 2", len(streams))
	}
	if streams[0].ID != "movie-main" || !streams[0].IsDefault {
		t.Errorf("first stream = %+v, want the default movie-main", streams[0])
	}
	if streams[1].ID != "movie-backup" {
		t.Errorf("second stream = %q, want movie-backup", streams[1].ID)
	}

	// A known asset with no stream rows is an empty list, not an error.
	none, err := adapter.GetVodStreams("vod-doc")
	if err != nil {
		t.Fatalf("GetVodStreams(no streams): %v", err)
	}
	if none == nil || len(none) != 0 {
		t.Errorf("streams = %#v, want a non-nil empty slice", none)
	}
}

func TestGetEpisodesOrderedBySeasonThenEpisode(t *testing.T) {
	adapter := newFixtureAdapter(t)

	episodes, err := adapter.GetEpisodes("vod-series")
	if err != nil {
		t.Fatalf("GetEpisodes: %v", err)
	}
	want := []string{"series-s1e1", "series-s1e2", "series-s2e1"}
	got := make([]string, 0, len(episodes))
	for _, ep := range episodes {
		got = append(got, ep.ID)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("episode order = %v, want %v", got, want)
	}

	none, err := adapter.GetEpisodes("vod-movie")
	if err != nil {
		t.Fatalf("GetEpisodes(no episodes): %v", err)
	}
	if none == nil || len(none) != 0 {
		t.Errorf("episodes = %#v, want a non-nil empty slice", none)
	}

	episode, err := adapter.GetEpisode("series-s1e2")
	if err != nil {
		t.Fatalf("GetEpisode: %v", err)
	}
	if episode.SeasonNumber != 1 || episode.EpisodeNumber != 2 || episode.VodID != "vod-series" {
		t.Errorf("episode = %+v", episode)
	}
}

func TestListChannelsFilterAndPaging(t *testing.T) {
	adapter := newFixtureAdapter(t)

	channels, total, err := adapter.ListChannels("", 10, 0)
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	wantAll := []string{"Movies HD", "News One", "Orphan"}
	if got := channelNames(channels); !reflect.DeepEqual(got, wantAll) {
		t.Errorf("channels = %v, want %v", got, wantAll)
	}

	for _, filter := range []string{"News", "news", "NEWS"} {
		filtered, filterTotal, err := adapter.ListChannels(filter, 10, 0)
		if err != nil {
			t.Fatalf("ListChannels(%q): %v", filter, err)
		}
		if filterTotal != 2 || !reflect.DeepEqual(channelNames(filtered), []string{"News One", "Orphan"}) {
			t.Errorf("ListChannels(%q) = %v (total %d), want [News One Orphan]", filter, channelNames(filtered), filterTotal)
		}
	}

	all, allTotal, err := adapter.ListChannels("All", 10, 0)
	if err != nil {
		t.Fatalf("ListChannels(All): %v", err)
	}
	if allTotal != 3 || len(all) != 3 {
		t.Errorf("ListChannels(All) = %d channels, total %d, want 3/3", len(all), allTotal)
	}

	pageTwo, pageTotal, err := adapter.ListChannels("", 1, 1)
	if err != nil {
		t.Fatalf("ListChannels(page 2): %v", err)
	}
	if pageTotal != 3 || !reflect.DeepEqual(channelNames(pageTwo), []string{"News One"}) {
		t.Errorf("page 2 = %v (total %d), want [News One]", channelNames(pageTwo), pageTotal)
	}

	beyond, beyondTotal, err := adapter.ListChannels("", 10, 99)
	if err != nil {
		t.Fatalf("ListChannels(beyond): %v", err)
	}
	if beyondTotal != 3 || len(beyond) != 0 {
		t.Errorf("beyond end = %d channels, total %d, want 0/3", len(beyond), beyondTotal)
	}
}

func TestListChannelCategories(t *testing.T) {
	adapter := newFixtureAdapter(t)

	categories, err := adapter.ListChannelCategories()
	if err != nil {
		t.Fatalf("ListChannelCategories: %v", err)
	}
	if want := []string{"Movies", "News"}; !reflect.DeepEqual(categories, want) {
		t.Errorf("categories = %v, want %v", categories, want)
	}
}

func TestResolveChannelStreamPrecedence(t *testing.T) {
	adapter := newFixtureAdapter(t)

	// The default stream wins even though it is not the first row in the file.
	got, err := adapter.ResolveChannelStream("movies-hd")
	if err != nil {
		t.Fatalf("ResolveChannelStream(movies-hd): %v", err)
	}
	if got.ID != "mov-main" || !got.IsDefault {
		t.Errorf("resolved = %+v, want the default mov-main", got)
	}

	// No default, so the first active stream wins over the offline one.
	got, err = adapter.ResolveChannelStream("news-one")
	if err != nil {
		t.Fatalf("ResolveChannelStream(news-one): %v", err)
	}
	if got.ID != "news-backup" {
		t.Errorf("resolved = %+v, want the active news-backup", got)
	}

	// A channel with no streams at all is a not-found error.
	if _, err := adapter.ResolveChannelStream("orphan"); !errors.Is(err, ErrContentNotFound) {
		t.Errorf("ResolveChannelStream(orphan) = %v, want ErrContentNotFound", err)
	}
}

func TestGetChannelWithStreams(t *testing.T) {
	adapter := newFixtureAdapter(t)

	channel, streams, err := adapter.GetChannelWithStreams("movies-hd")
	if err != nil {
		t.Fatalf("GetChannelWithStreams: %v", err)
	}
	if channel.Name != "Movies HD" {
		t.Errorf("channel = %+v", channel)
	}
	want := []string{"mov-main", "mov-alt", "mov-backup", "mov-dead"}
	got := make([]string, 0, len(streams))
	for _, s := range streams {
		got = append(got, s.ID)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("streams = %v, want %v", got, want)
	}

	// A channel with no streams returns an empty, non-nil list.
	_, none, err := adapter.GetChannelWithStreams("orphan")
	if err != nil {
		t.Fatalf("GetChannelWithStreams(orphan): %v", err)
	}
	if none == nil || len(none) != 0 {
		t.Errorf("streams = %#v, want a non-nil empty slice", none)
	}
}

func TestNotFounds(t *testing.T) {
	adapter := newFixtureAdapter(t)

	checks := []struct {
		name string
		call func() error
	}{
		{"GetEntry", func() error { _, err := adapter.GetEntry("nope"); return err }},
		{"GetEpisode", func() error { _, err := adapter.GetEpisode("nope"); return err }},
		{"GetChannel", func() error { _, err := adapter.GetChannel("nope"); return err }},
		{"GetChannelWithStreams", func() error { _, _, err := adapter.GetChannelWithStreams("nope"); return err }},
		{"GetVodStreams", func() error { _, err := adapter.GetVodStreams("nope"); return err }},
		{"GetEpisodes", func() error { _, err := adapter.GetEpisodes("nope"); return err }},
		{"ResolveChannelStream", func() error { _, err := adapter.ResolveChannelStream("nope"); return err }},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if !errors.Is(err, ErrContentNotFound) {
				t.Errorf("error = %v, want ErrContentNotFound", err)
			}
		})
	}
}

// Every list-returning method must return the same order on every call. Go map
// iteration is random, so a method that forgot to sort would flap here.
func TestSortedOutputsAreDeterministic(t *testing.T) {
	adapter := newFixtureAdapter(t)

	for i := 0; i < 5; i++ {
		entriesA, _, _ := adapter.GetEntries("", "", "", 1, 50)
		entriesB, _, _ := adapter.GetEntries("", "", "", 1, 50)
		if !reflect.DeepEqual(entriesA, entriesB) {
			t.Fatalf("GetEntries order flapped: %v vs %v", entryNames(entriesA), entryNames(entriesB))
		}

		channelsA, _, _ := adapter.ListChannels("", 50, 0)
		channelsB, _, _ := adapter.ListChannels("", 50, 0)
		if !reflect.DeepEqual(channelsA, channelsB) {
			t.Fatalf("ListChannels order flapped: %v vs %v", channelNames(channelsA), channelNames(channelsB))
		}

		probeA, _ := adapter.ListStreamsForProbe()
		probeB, _ := adapter.ListStreamsForProbe()
		if !reflect.DeepEqual(probeA, probeB) {
			t.Fatal("ListStreamsForProbe order flapped")
		}

		genresA, _ := adapter.GetGenres()
		genresB, _ := adapter.GetGenres()
		if !reflect.DeepEqual(genresA, genresB) {
			t.Fatal("GetGenres order flapped")
		}

		providersA, _ := adapter.GetProviders()
		providersB, _ := adapter.GetProviders()
		if !reflect.DeepEqual(providersA, providersB) {
			t.Fatal("GetProviders order flapped")
		}
	}
}

func TestGetProvidersAndGenres(t *testing.T) {
	adapter := newFixtureAdapter(t)

	providers, err := adapter.GetProviders()
	if err != nil {
		t.Fatalf("GetProviders: %v", err)
	}
	if want := []string{"local", "other"}; !reflect.DeepEqual(providers, want) {
		t.Errorf("providers = %v, want %v", providers, want)
	}

	genres, err := adapter.GetGenres()
	if err != nil {
		t.Fatalf("GetGenres: %v", err)
	}
	if want := []string{"Docs", "Films", "Shows"}; !reflect.DeepEqual(genres, want) {
		t.Errorf("genres = %v, want %v", genres, want)
	}
}

func channelNames(channels []source.Channel) []string {
	out := make([]string, 0, len(channels))
	for _, c := range channels {
		out = append(out, c.Name)
	}
	return out
}
