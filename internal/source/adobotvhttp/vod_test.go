package adobotvhttp

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/jmvbambico/adoboflix/internal/source"
)

// standardDRM is the DRM leaf set the channel and VOD fixtures point at.
func standardDRM() map[string]string {
	return map[string]string{
		"ch-alpha": drmBody(map[string]string{
			"drm_type": "m3u",
			"drm_key":  "",
			"url":      "https://cdn.example/live/alpha/index.m3u8",
		}),
		"ch-beta": drmBody(map[string]string{
			"drm_type": "widevine",
			"drm_key":  "https://license.example/wv",
			"url":      "https://cdn.example/live/beta/index.mpd",
		}),
		"vod-movie": drmBody(map[string]string{
			"drm_type": "m3u",
			"drm_key":  "",
			"url":      "https://cdn.example/vod/movie/index.m3u8",
		}),
		"ep-1": drmBody(map[string]string{
			"drm_type": "clearkey",
			"drm_key":  "kid123:k3y123",
			"url":      "https://cdn.example/vod/series/s01e01/index.mpd",
		}),
		"ep-2": drmBody(map[string]string{
			"drm_type": "m3u",
			"drm_key":  "",
			"url":      "https://cdn.example/vod/series/s01e02/index.m3u8",
		}),
	}
}

// vodFixture is a flat array with a movie (no seasons) and a series (two
// episodes across one season). Every playable leaf carries its own
// runtime_attr_url; `video` is the decoy placeholder.
func vodFixture(r *http.Request) []map[string]any {
	return []map[string]any{
		{
			"category":         "Films",
			"name":             "Movie A",
			"video":            testDecoyURL,
			"runtime_attr_url": abs(r, "/v1/drm/key/vod-movie?token="+testToken),
			"info": map[string]any{
				"cast":     []string{"Actor One"},
				"director": []string{"Director One"},
				"genre":    []string{"Action"},
				"rating":   "PG",
				"year":     "2024",
				"added":    "200",
				"poster":   "https://img/movie-a.jpg",
			},
		},
		{
			"category": "Shows",
			"name":     "Series B",
			"seasons": []map[string]any{
				{
					"season": 1,
					"episodes": []map[string]any{
						{
							"episode":          1,
							"video":            testDecoyURL,
							"runtime_attr_url": abs(r, "/v1/drm/key/ep-1?token="+testToken),
						},
						{
							"episode":          2,
							"video":            testDecoyURL,
							"runtime_attr_url": abs(r, "/v1/drm/key/ep-2?token="+testToken),
						},
					},
				},
			},
			"info": map[string]any{
				"genre": []string{"Drama"},
				"year":  "2023",
				"added": "100",
			},
		},
	}
}

func findEntry(t *testing.T, entries []source.Entry, name string) source.Entry {
	t.Helper()
	for _, e := range entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("entry %q not found in %+v", name, entries)
	return source.Entry{}
}

func TestVODLibraryFlatArrayMapping(t *testing.T) {
	adapter, _ := newTestServer(t, time.Minute, libraryHandler(t, vodFixture, standardDRM(), nil))

	entries, total, err := adapter.GetEntries("", "", "", 1, 50)
	if err != nil {
		t.Fatalf("GetEntries: %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	// Newest first: Movie A (added 200) before Series B (added 100).
	if entries[0].Name != "Movie A" || entries[1].Name != "Series B" {
		t.Fatalf("order = [%s, %s], want [Movie A, Series B]", entries[0].Name, entries[1].Name)
	}

	movie := findEntry(t, entries, "Movie A")
	if movie.Type != "Movie" {
		t.Errorf("Movie A type = %q, want Movie", movie.Type)
	}
	if movie.ID == "" || movie.Category == nil || *movie.Category != "Films" {
		t.Errorf("Movie A mapping = %+v", movie)
	}
	if movie.Poster == nil || *movie.Poster != "https://img/movie-a.jpg" {
		t.Errorf("Movie A poster = %v", movie.Poster)
	}
	if len(movie.CastMembers) != 1 || movie.CastMembers[0] != "Actor One" {
		t.Errorf("Movie A cast = %v", movie.CastMembers)
	}
	if movie.ReleaseYear == nil || *movie.ReleaseYear != 2024 {
		t.Errorf("Movie A year = %v", movie.ReleaseYear)
	}
	if movie.StreamURL != nil {
		t.Errorf("Movie A stream_url = %v, want nil (resolved on demand)", *movie.StreamURL)
	}

	series := findEntry(t, entries, "Series B")
	if series.Type != "Series" {
		t.Errorf("Series B type = %q, want Series", series.Type)
	}

	counts, err := adapter.EpisodeCounts([]string{series.ID, movie.ID})
	if err != nil {
		t.Fatalf("EpisodeCounts: %v", err)
	}
	if counts[series.ID] != 2 {
		t.Errorf("Series B episode count = %d, want 2", counts[series.ID])
	}
	if _, ok := counts[movie.ID]; ok {
		t.Errorf("Movie A has an episode count, want absent")
	}

	episodes, err := adapter.GetEpisodes(series.ID)
	if err != nil {
		t.Fatalf("GetEpisodes: %v", err)
	}
	if len(episodes) != 2 {
		t.Fatalf("len(episodes) = %d, want 2", len(episodes))
	}
	if episodes[0].SeasonNumber != 1 || episodes[0].EpisodeNumber != 1 || episodes[1].EpisodeNumber != 2 {
		t.Errorf("episode order = %+v", episodes)
	}
	if episodes[0].VodID != series.ID {
		t.Errorf("episode vod id = %q, want %q", episodes[0].VodID, series.ID)
	}
}

func TestVODStreamsResolveMovieAndDeclineSeriesAsset(t *testing.T) {
	adapter, _ := newTestServer(t, time.Minute, libraryHandler(t, vodFixture, standardDRM(), nil))

	entries, _, err := adapter.GetEntries("", "", "", 1, 50)
	if err != nil {
		t.Fatalf("GetEntries: %v", err)
	}
	movie := findEntry(t, entries, "Movie A")
	series := findEntry(t, entries, "Series B")

	streams, err := adapter.GetVodStreams(movie.ID)
	if err != nil {
		t.Fatalf("GetVodStreams(movie): %v", err)
	}
	if len(streams) != 1 {
		t.Fatalf("movie streams = %d, want 1", len(streams))
	}
	if streams[0].URL != "https://cdn.example/vod/movie/index.m3u8" {
		t.Errorf("movie stream url = %q", streams[0].URL)
	}
	if streams[0].DrmType != nil {
		t.Errorf("movie drm_type = %v, want nil for m3u", streams[0].DrmType)
	}

	seriesStreams, err := adapter.GetVodStreams(series.ID)
	if err != nil {
		t.Fatalf("GetVodStreams(series): %v", err)
	}
	if len(seriesStreams) != 0 {
		t.Errorf("series asset streams = %d, want 0 (episodes carry the leaves)", len(seriesStreams))
	}
}

func TestGetEpisodeResolvesClearKey(t *testing.T) {
	adapter, _ := newTestServer(t, time.Minute, libraryHandler(t, vodFixture, standardDRM(), nil))

	entries, _, err := adapter.GetEntries("", "", "", 1, 50)
	if err != nil {
		t.Fatalf("GetEntries: %v", err)
	}
	series := findEntry(t, entries, "Series B")
	episodes, err := adapter.GetEpisodes(series.ID)
	if err != nil {
		t.Fatalf("GetEpisodes: %v", err)
	}

	episode, err := adapter.GetEpisode(episodes[0].ID)
	if err != nil {
		t.Fatalf("GetEpisode: %v", err)
	}
	if episode.StreamURL == nil || *episode.StreamURL != "https://cdn.example/vod/series/s01e01/index.mpd" {
		t.Errorf("episode stream_url = %v", episode.StreamURL)
	}
	if episode.DrmType == nil || *episode.DrmType != "Clearkey" {
		t.Errorf("episode drm_type = %v, want Clearkey", episode.DrmType)
	}
	if episode.DrmK == nil || *episode.DrmK != "kid123:k3y123" {
		t.Errorf("episode drm_k = %v", episode.DrmK)
	}
}

func TestGetEntryAndEpisodeNotFound(t *testing.T) {
	adapter, _ := newTestServer(t, time.Minute, libraryHandler(t, vodFixture, standardDRM(), nil))

	if _, err := adapter.GetEntry("vod-missing"); !errors.Is(err, ErrContentNotFound) {
		t.Errorf("GetEntry(unknown) = %v, want ErrContentNotFound", err)
	}
	if _, err := adapter.GetEpisode("ep-missing"); !errors.Is(err, ErrContentNotFound) {
		t.Errorf("GetEpisode(unknown) = %v, want ErrContentNotFound", err)
	}
}

func TestFiltersSearchProvidersGenresAndStats(t *testing.T) {
	adapter, _ := newTestServer(t, time.Minute, libraryHandler(t, vodFixture, standardDRM(), nil))

	if got := mustEntries(t, adapter, "adobotv", "", "Series", 1, 10); len(got) != 1 || got[0].Name != "Series B" {
		t.Errorf("provider+type filter = %+v", got)
	}
	if got := mustEntries(t, adapter, "other", "", "", 1, 10); len(got) != 0 {
		t.Errorf("unknown provider filter returned %d entries", len(got))
	}
	if got := mustEntries(t, adapter, "", "Action", "", 1, 10); len(got) != 1 || got[0].Name != "Movie A" {
		t.Errorf("genre filter = %+v", got)
	}

	results, total, err := adapter.Search("movie", "", "", "", 1, 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if total != 1 || len(results) != 1 || results[0].Name != "Movie A" {
		t.Errorf("Search(movie) = %d results, total %d", len(results), total)
	}

	providers, err := adapter.GetProviders()
	if err != nil {
		t.Fatalf("GetProviders: %v", err)
	}
	if len(providers) != 1 || providers[0] != sourceType {
		t.Errorf("providers = %v, want [%s]", providers, sourceType)
	}

	genres, err := adapter.GetGenres()
	if err != nil {
		t.Fatalf("GetGenres: %v", err)
	}
	want := []string{"Action", "Drama", "Films", "Shows"}
	if len(genres) != len(want) {
		t.Fatalf("genres = %v, want %v", genres, want)
	}
	for i := range want {
		if genres[i] != want[i] {
			t.Errorf("genres = %v, want %v", genres, want)
			break
		}
	}

	stats, err := adapter.GetStats()
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.TotalTitles != 2 || stats.TotalProviders != 1 || stats.TotalGenres != 4 {
		t.Errorf("stats = %+v, want titles 2, providers 1, genres 4", stats)
	}
}

func TestPagination(t *testing.T) {
	adapter, _ := newTestServer(t, time.Minute, libraryHandler(t, vodFixture, standardDRM(), nil))

	page, total, err := adapter.GetEntries("", "", "", 2, 1)
	if err != nil {
		t.Fatalf("GetEntries: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if len(page) != 1 || page[0].Name != "Series B" {
		t.Errorf("page 2 = %+v, want [Series B]", page)
	}

	beyond, total, err := adapter.GetEntries("", "", "", 9, 1)
	if err != nil {
		t.Fatalf("GetEntries: %v", err)
	}
	if len(beyond) != 0 || total != 2 {
		t.Errorf("page beyond end = %d entries, total %d", len(beyond), total)
	}
}

func TestNoActiveVODCollectionIsEmptyNotError(t *testing.T) {
	// An envelope whose vod_library URL is empty means the subscriber has no
	// active collection: an empty library, not a failure.
	adapter, _ := newTestServer(t, time.Minute, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/playlist/"+testPlaylistCode {
			env := map[string]any{
				"provider":   map[string]any{"epg": abs(r, "/v1/epg/x?token="+testToken)},
				"categories": map[string]any{},
				"channels":   []map[string]any{},
			}
			writeBody(w, http.StatusOK, "application/json", mustJSON(t, env))
			return
		}
		writeErrorEnvelope(w, http.StatusNotFound, "unexpected")
	})

	entries, total, err := adapter.GetEntries("", "", "", 1, 10)
	if err != nil {
		t.Fatalf("GetEntries: %v", err)
	}
	if total != 0 || len(entries) != 0 {
		t.Errorf("entries = %d, total %d, want empty", len(entries), total)
	}
}

func TestMalformedVODLibrary(t *testing.T) {
	adapter, _ := newTestServer(t, time.Minute, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/playlist/" + testPlaylistCode:
			writeBody(w, http.StatusOK, "application/json", string(envelopeBody(t, r, nil)))
		case "/v1/vod":
			// The {"success":...} wrapper, not the documented flat array.
			writeBody(w, http.StatusOK, "application/json", `{"success":true,"data":[]}`)
		default:
			writeErrorEnvelope(w, http.StatusNotFound, "unexpected")
		}
	})

	_, _, err := adapter.GetEntries("", "", "", 1, 10)
	if !errors.Is(err, ErrMalformedVODLibrary) {
		t.Fatalf("error = %v, want it to wrap ErrMalformedVODLibrary", err)
	}
}

func mustEntries(t *testing.T, adapter *Adapter, provider, genre, contentType string, page, limit int) []source.Entry {
	t.Helper()
	entries, _, err := adapter.GetEntries(provider, genre, contentType, page, limit)
	if err != nil {
		t.Fatalf("GetEntries: %v", err)
	}
	return entries
}
