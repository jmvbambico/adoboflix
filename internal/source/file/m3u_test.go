package file

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/jmvbambico/adoboflix/internal/source"
)

// The M3U parser is defined by its tolerance as much as by its happy path, so
// most of what follows feeds it deliberately malformed playlists and asserts on
// what survives and what is counted as dropped.

// The attribute-parsing tolerances: order, both quote styles, unknown keys,
// blank and comment lines, CRLF endings, and a missing #EXTM3U header. Every
// case yields exactly one live channel (none of the groups reads as VOD).
func TestM3UAttributeTolerances(t *testing.T) {
	cases := []struct {
		name         string
		body         string
		wantName     string
		wantCategory string
		wantLogo     string
		wantEpg      string
	}{
		{
			name: "attributes in any order, double quotes",
			body: "#EXTM3U\r\n#EXTINF:-1 group-title=\"News\" tvg-name=\"N\" " +
				"tvg-logo=\"https://img.example/n.png\" tvg-id=\"news.tvg\",News One\r\n" +
				"https://cdn.example/news.m3u8\r\n",
			wantName: "News One", wantCategory: "News",
			wantLogo: "https://img.example/n.png", wantEpg: "news.tvg",
		},
		{
			name: "single-quoted values",
			body: "#EXTM3U\n#EXTINF:-1 tvg-id='sports.tvg' group-title='Sports',Sports One\n" +
				"https://cdn.example/sports.m3u8\n",
			wantName: "Sports One", wantCategory: "Sports", wantEpg: "sports.tvg",
		},
		{
			name: "unknown attributes are ignored, not fatal",
			body: "#EXTM3U\n#EXTINF:-1 catchup=\"append\" timeshift=\"1\" " +
				"group-title=\"News\",News Two\nhttps://cdn.example/news2.m3u8\n",
			wantName: "News Two", wantCategory: "News",
		},
		{
			name: "blank lines, comment lines and directives",
			body: "\n#EXTM3U\n\n# a bare comment\n#EXTINF:-1 group-title=\"News\",News Three\n\n" +
				"https://cdn.example/news3.m3u8\n#KODIPROP:inputstream.adaptive.license_type=clearkey\n",
			wantName: "News Three", wantCategory: "News",
		},
		{
			name:     "missing #EXTM3U header",
			body:     "#EXTINF:-1 group-title=\"News\",Headerless\nhttps://cdn.example/h.m3u8\n",
			wantName: "Headerless", wantCategory: "News",
		},
		{
			name:     "no group-title leaves the category empty",
			body:     "#EXTM3U\n#EXTINF:-1 tvg-name=\"Bare\",Bare Channel\nhttps://cdn.example/b.m3u8\n",
			wantName: "Bare Channel",
		},
		{
			name:     "duration present but no attributes",
			body:     "#EXTM3U\n#EXTINF:0,Plain\nhttps://cdn.example/p.m3u8\n",
			wantName: "Plain",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adapter := mustRawM3UAdapter(t, tc.body)

			channels, total, err := adapter.ListChannels("", 10, 0)
			if err != nil {
				t.Fatalf("ListChannels: %v", err)
			}
			if total != 1 || len(channels) != 1 {
				t.Fatalf("channels = %d (total %d), want 1", len(channels), total)
			}
			ch := channels[0]
			if ch.Name != tc.wantName {
				t.Errorf("name = %q, want %q", ch.Name, tc.wantName)
			}
			if got := derefOr(ch.Category, ""); got != tc.wantCategory {
				t.Errorf("category = %q, want %q", got, tc.wantCategory)
			}
			if got := derefOr(ch.Logo, ""); got != tc.wantLogo {
				t.Errorf("logo = %q, want %q", got, tc.wantLogo)
			}
			if got := derefOr(ch.EpgChannelID, ""); got != tc.wantEpg {
				t.Errorf("epg channel id = %q, want %q", got, tc.wantEpg)
			}

			// The synthesised stream must be the channel's playable default.
			stream, err := adapter.ResolveChannelStream(ch.ID)
			if err != nil {
				t.Fatalf("ResolveChannelStream: %v", err)
			}
			if stream.URL == "" || !stream.IsDefault {
				t.Errorf("stream = %+v, want a default stream with a URL", stream)
			}
			if stream.SourceType != Name {
				t.Errorf("stream provider = %q, want %q", stream.SourceType, Name)
			}
		})
	}
}

// An #EXTINF with no following URL is dropped, and the drop is counted rather
// than silent.
func TestM3UExtinfWithoutURLIsDroppedAndCounted(t *testing.T) {
	adapter := mustRawM3UAdapter(t, "#EXTM3U\n"+
		"#EXTINF:-1 group-title=\"News\",Has URL\nhttps://cdn.example/ok.m3u8\n"+
		"#EXTINF:-1 group-title=\"News\",No URL\n"+
		"#EXTINF:-1 group-title=\"News\",Also No URL\nhttps://cdn.example/ok2.m3u8\n")

	channels, total, err := adapter.ListChannels("", 10, 0)
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if total != 2 {
		t.Fatalf("channels = %d, want 2", total)
	}
	if want := []string{"Also No URL", "Has URL"}; !reflect.DeepEqual(channelNames(channels), want) {
		t.Errorf("channels = %v, want %v", channelNames(channels), want)
	}
	if adapter.summary.entries != 3 || adapter.summary.dropped != 1 {
		t.Errorf("summary = read %d, dropped %d; want 3 read, 1 dropped",
			adapter.summary.entries, adapter.summary.dropped)
	}
}

// A URL line with no preceding #EXTINF has no title and no group, so it is
// dropped and counted.
func TestM3UBareURLIsDroppedAndCounted(t *testing.T) {
	adapter := mustRawM3UAdapter(t, "#EXTM3U\n"+
		"https://cdn.example/bare.m3u8\n"+
		"#EXTINF:-1 group-title=\"News\",Real\nhttps://cdn.example/real.m3u8\n")

	if _, total, err := adapter.ListChannels("", 10, 0); err != nil || total != 1 {
		t.Fatalf("channels total = %d (err %v), want 1", total, err)
	}
	if adapter.summary.bareURLs != 1 {
		t.Errorf("bare URLs = %d, want 1", adapter.summary.bareURLs)
	}
}

// Each season/episode shape the adapter promises to parse, case-insensitively.
func TestM3USeasonEpisodeShapes(t *testing.T) {
	cases := []struct {
		name        string
		title       string
		wantSeason  int
		wantEpisode int
	}{
		{"SxxExx", "Breaking Bad S01E02", 1, 2},
		{"lowercase sxxexx", "Breaking Bad s1e2", 1, 2},
		{"NxNN", "Breaking Bad 1x02", 1, 2},
		{"NxNN uppercase X", "Breaking Bad 3X07", 3, 7},
		{"Season N Episode M", "Breaking Bad Season 1 Episode 2", 1, 2},
		{"lowercase season/episode", "Breaking Bad season 2 episode 10", 2, 10},
		{"Season N Ep M", "Breaking Bad Season 1 Ep 5", 1, 5},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adapter := mustRawM3UAdapter(t, "#EXTM3U\n"+
				"#EXTINF:-1 group-title=\"Series\","+tc.title+"\nhttps://cdn.example/ep.mkv\n")

			entries, total, err := adapter.GetEntries("", "", "", 1, 10)
			if err != nil {
				t.Fatalf("GetEntries: %v", err)
			}
			if total != 1 || len(entries) != 1 {
				t.Fatalf("entries = %d (total %d), want 1 series", len(entries), total)
			}
			if entries[0].Type != "Series" {
				t.Errorf("type = %q, want Series", entries[0].Type)
			}
			if entries[0].Name != "Breaking Bad" {
				t.Errorf("series name = %q, want %q", entries[0].Name, "Breaking Bad")
			}

			episodes, err := adapter.GetEpisodes(entries[0].ID)
			if err != nil {
				t.Fatalf("GetEpisodes: %v", err)
			}
			if len(episodes) != 1 {
				t.Fatalf("episodes = %d, want 1", len(episodes))
			}
			if episodes[0].SeasonNumber != tc.wantSeason || episodes[0].EpisodeNumber != tc.wantEpisode {
				t.Errorf("episode = S%dE%d, want S%dE%d",
					episodes[0].SeasonNumber, episodes[0].EpisodeNumber, tc.wantSeason, tc.wantEpisode)
			}
		})
	}
}

// A VOD-classified entry whose title has no season/episode is still a VOD row,
// filed as a movie with a playable stream — never dropped.
func TestM3UVodEntryWithoutSeasonEpisodeIsAMovie(t *testing.T) {
	adapter := mustRawM3UAdapter(t, "#EXTM3U\n"+
		"#EXTINF:-1 group-title=\"Movies\",An Unnumbered Film\nhttps://cdn.example/film.mp4\n"+
		"#EXTINF:-1 group-title=\"Movies\",Another Film\nhttps://cdn.example/film2.mp4\n")

	entries, total, err := adapter.GetEntries("", "", "", 1, 10)
	if err != nil {
		t.Fatalf("GetEntries: %v", err)
	}
	if total != 2 {
		t.Fatalf("entries = %d, want 2 (a VOD row is never dropped for its title)", total)
	}
	for _, e := range entries {
		if e.Type != "Movie" {
			t.Errorf("%q type = %q, want Movie", e.Name, e.Type)
		}
		streams, err := adapter.GetVodStreams(e.ID)
		if err != nil {
			t.Fatalf("GetVodStreams(%q): %v", e.ID, err)
		}
		if len(streams) != 1 || streams[0].URL == "" || !streams[0].IsDefault {
			t.Errorf("%q streams = %+v, want one playable default", e.Name, streams)
		}
	}

	if _, total, err := adapter.ListChannels("", 10, 0); err != nil || total != 0 {
		t.Errorf("channels total = %d (err %v), want 0", total, err)
	}
	if adapter.summary.movies != 2 || adapter.summary.series != 0 {
		t.Errorf("summary movies=%d series=%d, want 2 and 0", adapter.summary.movies, adapter.summary.series)
	}
}

// Episodes of one series land under a single VOD entry with one id, across every
// title shape — including the trailing "SxxExx - Episode Title" form real
// playlists use most. That trailing text belongs on the episode, not on the
// series: keeping it on the series would give every episode a different series
// name and split the show into one-episode entries.
func TestM3UEpisodesOfOneSeriesGroupUnderOneEntry(t *testing.T) {
	titles := []string{
		"Breaking Bad S01E01 - Pilot",
		"Breaking Bad S01E02 - Cat's in the Bag",
		"Breaking Bad S02E01 - Seven Thirty-Seven",
		"Breaking Bad 1x04 - Down",
		"Breaking Bad Season 5 Episode 14 - Ozymandias",
	}

	var body strings.Builder
	body.WriteString("#EXTM3U\n")
	for i, title := range titles {
		fmt.Fprintf(&body, "#EXTINF:-1 group-title=\"Series\",%s\nhttps://cdn.example/bb/%d.mkv\n", title, i)
	}
	adapter := mustRawM3UAdapter(t, body.String())

	entries, total, err := adapter.GetEntries("", "", "", 1, 10)
	if err != nil {
		t.Fatalf("GetEntries: %v", err)
	}
	if total != 1 || len(entries) != 1 {
		t.Fatalf("entries = %d (total %d), want exactly one series", len(entries), total)
	}
	series := entries[0]
	if series.Name != "Breaking Bad" {
		t.Fatalf("series name = %q, want %q (no dangling separator or doubled space)", series.Name, "Breaking Bad")
	}
	if series.Type != "Series" {
		t.Errorf("series type = %q, want Series", series.Type)
	}

	counts, err := adapter.EpisodeCounts([]string{series.ID})
	if err != nil {
		t.Fatalf("EpisodeCounts: %v", err)
	}
	if counts[series.ID] != len(titles) {
		t.Errorf("episode count = %d, want %d", counts[series.ID], len(titles))
	}

	episodes, err := adapter.GetEpisodes(series.ID)
	if err != nil {
		t.Fatalf("GetEpisodes: %v", err)
	}
	if len(episodes) != len(titles) {
		t.Fatalf("episodes = %d, want %d", len(episodes), len(titles))
	}
	got := map[string]string{}
	for _, ep := range episodes {
		key := strconv.Itoa(ep.SeasonNumber) + "x" + strconv.Itoa(ep.EpisodeNumber)
		if _, dup := got[key]; dup {
			t.Errorf("duplicate episode %s", key)
		}
		got[key] = ep.Name
		if ep.VodID != series.ID {
			t.Errorf("episode %s vod_id = %q, want %q", key, ep.VodID, series.ID)
		}
	}
	want := map[string]string{
		"1x1":  "Pilot",
		"1x2":  "Cat's in the Bag",
		"2x1":  "Seven Thirty-Seven",
		"1x4":  "Down",
		"5x14": "Ozymandias",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("episodes = %v, want %v", got, want)
	}
}

// A trailing-title entry with no series prefix ("1x02 - Pilot") has no series
// name in its title at all, so it falls back to the group name rather than
// becoming a series called "Pilot".
func TestM3UTrailingTitleWithNoSeriesPrefixFallsBackToGroup(t *testing.T) {
	adapter := mustRawM3UAdapter(t, "#EXTM3U\n"+
		"#EXTINF:-1 group-title=\"Series\",1x02 - Pilot\nhttps://cdn.example/p.mkv\n")

	entries, total, err := adapter.GetEntries("", "", "", 1, 10)
	if err != nil {
		t.Fatalf("GetEntries: %v", err)
	}
	if total != 1 {
		t.Fatalf("entries = %d, want 1", total)
	}
	if entries[0].Name != "Series" {
		t.Errorf("series name = %q, want the group name %q", entries[0].Name, "Series")
	}
	episodes, err := adapter.GetEpisodes(entries[0].ID)
	if err != nil {
		t.Fatalf("GetEpisodes: %v", err)
	}
	if len(episodes) != 1 {
		t.Fatalf("episodes = %d, want 1", len(episodes))
	}
	if episodes[0].Name != "Pilot" {
		t.Errorf("episode name = %q, want Pilot", episodes[0].Name)
	}
	if episodes[0].SeasonNumber != 1 || episodes[0].EpisodeNumber != 2 {
		t.Errorf("episode = S%dE%d, want S1E2", episodes[0].SeasonNumber, episodes[0].EpisodeNumber)
	}
}

// An episode title with no trailing text keeps the full title rather than an
// empty name.
func TestM3UEpisodeWithNoTrailingTextKeepsFullTitle(t *testing.T) {
	adapter := mustRawM3UAdapter(t, "#EXTM3U\n"+
		"#EXTINF:-1 group-title=\"Series\",Breaking Bad S01E01\nhttps://cdn.example/bb.mkv\n")

	entries, _, err := adapter.GetEntries("", "", "", 1, 10)
	if err != nil {
		t.Fatalf("GetEntries: %v", err)
	}
	episodes, err := adapter.GetEpisodes(entries[0].ID)
	if err != nil {
		t.Fatalf("GetEpisodes: %v", err)
	}
	if len(episodes) != 1 || episodes[0].Name != "Breaking Bad S01E01" {
		t.Fatalf("episodes = %+v, want one named the full title", episodes)
	}
}

// Two different shows whose titles carry no series prefix sit in one group; the
// tvg-name attribute is what tells them apart, and it must be preferred over
// the group fallback that would otherwise merge them. Case/whitespace variants
// of one tvg-name still merge.
func TestM3USeriesIdentityUsesTvgNameOverGroup(t *testing.T) {
	adapter := mustRawM3UAdapter(t, "#EXTM3U\n"+
		"#EXTINF:-1 tvg-name=\"The Office US\" group-title=\"Series\",1x02 - The Dundies\nhttps://cdn.example/office1.mkv\n"+
		"#EXTINF:-1 tvg-name=\"the  office us\" group-title=\"series\",1x05 - Basketball\nhttps://cdn.example/office2.mkv\n"+
		"#EXTINF:-1 tvg-name=\"Fawlty Towers\" group-title=\"Series\",1x03 - The Wedding\nhttps://cdn.example/fawlty1.mkv\n")

	entries, total, err := adapter.GetEntries("", "", "", 1, 10)
	if err != nil {
		t.Fatalf("GetEntries: %v", err)
	}
	if total != 2 {
		t.Fatalf("series = %d, want 2 (two shows, not one merged %q)", total, "Series")
	}
	if want := []string{"Fawlty Towers", "The Office US"}; !reflect.DeepEqual(entryNames(entries), want) {
		t.Fatalf("series = %v, want %v", entryNames(entries), want)
	}

	office, found := findEntry(entries, "The Office US")
	if !found {
		t.Fatalf("no series named %q; entries = %v", "The Office US", entryNames(entries))
	}
	officeEpisodes, err := adapter.GetEpisodes(office.ID)
	if err != nil {
		t.Fatalf("GetEpisodes: %v", err)
	}
	if len(officeEpisodes) != 2 {
		t.Errorf("The Office US episodes = %d, want 2 (case/whitespace variants merge)", len(officeEpisodes))
	}
	if got := episodeNameByNumber(officeEpisodes); !reflect.DeepEqual(got, map[string]string{"1x2": "The Dundies", "1x5": "Basketball"}) {
		t.Errorf("The Office US episodes = %v", got)
	}

	fawlty, found := findEntry(entries, "Fawlty Towers")
	if !found {
		t.Fatalf("no series named %q; entries = %v", "Fawlty Towers", entryNames(entries))
	}
	fawltyEpisodes, err := adapter.GetEpisodes(fawlty.ID)
	if err != nil {
		t.Fatalf("GetEpisodes: %v", err)
	}
	if len(fawltyEpisodes) != 1 || fawltyEpisodes[0].Name != "The Wedding" {
		t.Errorf("Fawlty Towers episodes = %+v, want one named The Wedding", fawltyEpisodes)
	}

	if adapter.summary.seriesFromTvgName != 2 || adapter.summary.seriesFromTitle != 0 || adapter.summary.seriesFromGroup != 0 {
		t.Errorf("identity = title %d / tvg-name %d / group %d, want 0/2/0",
			adapter.summary.seriesFromTitle, adapter.summary.seriesFromTvgName, adapter.summary.seriesFromGroup)
	}
}

// A tvg-name that is really the entry name ("Breaking Bad S01E01") must not
// invent one series per episode: the same marker split is applied, so both
// episodes reduce to the one series "Breaking Bad".
func TestM3UTvgNameCarryingMarkerDoesNotSplitSeries(t *testing.T) {
	adapter := mustRawM3UAdapter(t, "#EXTM3U\n"+
		"#EXTINF:-1 tvg-name=\"Breaking Bad S01E01\" group-title=\"Series\",1x01 - Pilot\nhttps://cdn.example/bb1.mkv\n"+
		"#EXTINF:-1 tvg-name=\"Breaking Bad S01E02\" group-title=\"Series\",1x02 - Cat's in the Bag\nhttps://cdn.example/bb2.mkv\n")

	entries, total, err := adapter.GetEntries("", "", "", 1, 10)
	if err != nil {
		t.Fatalf("GetEntries: %v", err)
	}
	if total != 1 {
		t.Fatalf("series = %d, want 1 (a marker-carrying tvg-name must not split the show)", total)
	}
	if entries[0].Name != "Breaking Bad" {
		t.Errorf("series name = %q, want %q", entries[0].Name, "Breaking Bad")
	}
	episodes, err := adapter.GetEpisodes(entries[0].ID)
	if err != nil {
		t.Fatalf("GetEpisodes: %v", err)
	}
	if len(episodes) != 2 {
		t.Errorf("episodes = %d, want 2", len(episodes))
	}
	if adapter.summary.seriesFromTvgName != 1 {
		t.Errorf("identity from tvg-name = %d, want 1", adapter.summary.seriesFromTvgName)
	}
}

// A tvg-name that reduces to nothing usable — a bare "S01E01", or whitespace —
// falls all the way back to the group name rather than creating a nameless or
// per-episode series.
func TestM3UTvgNameReducingToEmptyFallsBackToGroup(t *testing.T) {
	adapter := mustRawM3UAdapter(t, "#EXTM3U\n"+
		"#EXTINF:-1 tvg-name=\"S01E01\" group-title=\"Series\",1x01 - Pilot\nhttps://cdn.example/a.mkv\n"+
		"#EXTINF:-1 tvg-name=\"   \" group-title=\"Series\",1x02 - Second\nhttps://cdn.example/b.mkv\n")

	entries, total, err := adapter.GetEntries("", "", "", 1, 10)
	if err != nil {
		t.Fatalf("GetEntries: %v", err)
	}
	if total != 1 {
		t.Fatalf("series = %d, want 1", total)
	}
	if entries[0].Name != "Series" {
		t.Errorf("series name = %q, want the group name %q", entries[0].Name, "Series")
	}
	if adapter.summary.seriesFromGroup != 1 || adapter.summary.seriesFromTvgName != 0 {
		t.Errorf("identity = tvg-name %d / group %d, want 0/1",
			adapter.summary.seriesFromTvgName, adapter.summary.seriesFromGroup)
	}
}

// A title that carries a series prefix wins over a conflicting tvg-name.
func TestM3UTitlePrefixBeatsTvgName(t *testing.T) {
	adapter := mustRawM3UAdapter(t, "#EXTM3U\n"+
		"#EXTINF:-1 tvg-name=\"The Office US\" group-title=\"Series\",Breaking Bad S01E01 - Pilot\nhttps://cdn.example/bb.mkv\n")

	entries, total, err := adapter.GetEntries("", "", "", 1, 10)
	if err != nil {
		t.Fatalf("GetEntries: %v", err)
	}
	if total != 1 {
		t.Fatalf("series = %d, want 1", total)
	}
	if entries[0].Name != "Breaking Bad" {
		t.Errorf("series name = %q, want the title prefix %q", entries[0].Name, "Breaking Bad")
	}
	if adapter.summary.seriesFromTitle != 1 || adapter.summary.seriesFromTvgName != 0 {
		t.Errorf("identity = title %d / tvg-name %d, want 1/0",
			adapter.summary.seriesFromTitle, adapter.summary.seriesFromTvgName)
	}
}

// episodeNameByNumber maps "SxE" to the episode's name, for order-insensitive
// assertions.
func episodeNameByNumber(episodes []source.Episode) map[string]string {
	out := make(map[string]string, len(episodes))
	for _, ep := range episodes {
		out[strconv.Itoa(ep.SeasonNumber)+"x"+strconv.Itoa(ep.EpisodeNumber)] = ep.Name
	}
	return out
}

func TestM3UClassificationSummary(t *testing.T) {
	adapter := mustRawM3UAdapter(t, "#EXTM3U\n"+
		"#EXTINF:-1 group-title=\"News\",News One\nhttps://cdn.example/news.m3u8\n"+
		"#EXTINF:-1 group-title=\"MOVIES\",The Matrix 1999\nhttps://cdn.example/matrix.mp4\n"+
		"#EXTINF:-1 group-title=\"TV Shows\",Breaking Bad S01E01\nhttps://cdn.example/bb1.mkv\n"+
		"#EXTINF:-1 group-title=\"tv shows\",breaking  bad S01E02\nhttps://cdn.example/bb2.mkv\n")

	channels, chTotal, err := adapter.ListChannels("", 10, 0)
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if chTotal != 1 || channelNames(channels)[0] != "News One" {
		t.Errorf("channels = %v, want [News One]", channelNames(channels))
	}

	entries, entryTotal, err := adapter.GetEntries("", "", "", 1, 10)
	if err != nil {
		t.Fatalf("GetEntries: %v", err)
	}
	if entryTotal != 2 {
		t.Fatalf("entries = %d, want 2 (a movie and a series)", entryTotal)
	}
	if want := []string{"Breaking Bad", "The Matrix 1999"}; !reflect.DeepEqual(entryNames(entries), want) {
		t.Errorf("entries = %v, want %v", entryNames(entries), want)
	}
	// The two case/whitespace-variant episodes are one series with two episodes.
	series, found := findEntry(entries, "Breaking Bad")
	if !found {
		t.Fatalf("no entry named %q; entries = %v", "Breaking Bad", entryNames(entries))
	}
	episodes, err := adapter.GetEpisodes(series.ID)
	if err != nil {
		t.Fatalf("GetEpisodes: %v", err)
	}
	if len(episodes) != 2 {
		t.Errorf("Breaking Bad episodes = %d, want 2", len(episodes))
	}

	if adapter.summary == nil {
		t.Fatal("an M3U adapter must carry a classification summary")
	}
	got := *adapter.summary
	if got.entries != 4 || got.channels != 1 || got.vodRows != 3 || got.series != 1 || got.movies != 1 {
		t.Errorf("summary = %+v, want 4 read / 1 channel / 3 VOD / 1 series / 1 movie", got)
	}
	if want := []string{"MOVIES", "TV Shows"}; !reflect.DeepEqual(got.vodGroups, want) {
		t.Errorf("VOD groups = %v, want %v (deduped case-insensitively)", got.vodGroups, want)
	}

	// The startup summary is one line per category and names the VOD groups.
	lines := got.logLines("/tmp/playlist.m3u")
	if len(lines) != 6 {
		t.Fatalf("summary has %d lines, want 6:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"read 4 entries from /tmp/playlist.m3u",
		"1 live channels",
		"3 VOD rows across 1 series",
		"series identity: 1 from title, 0 from tvg-name, 0 from group name",
		"1 VOD titles had no parseable season/episode",
		("VOD group names: MOVIES, TV Shows"),
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("summary missing %q:\n%s", want, joined)
		}
	}
}

// The parser is selected by extension, and an unknown extension is a clear
// startup error naming the supported set.
func TestM3UFormatSelectionByExtension(t *testing.T) {
	playlist := "#EXTM3U\n#EXTINF:-1 group-title=\"News\",News One\nhttps://cdn.example/n.m3u8\n"

	for _, name := range []string{"playlist.m3u", "playlist.m3u8", "PLAYLIST.M3U"} {
		t.Run(name, func(t *testing.T) {
			adapter, err := newRawFileAt(t, name, playlist)
			if err != nil {
				t.Fatalf("New(%s): %v", name, err)
			}
			if _, total, err := adapter.ListChannels("", 10, 0); err != nil || total != 1 {
				t.Errorf("channels total = %d (err %v), want 1", total, err)
			}
		})
	}

	// A .json file still goes through the JSON parser and gets no M3U summary.
	t.Run("json path unchanged", func(t *testing.T) {
		adapter, err := newRawFileAt(t, "library.json", `{"channels":[{"id":"c","name":"C","status":"active"}]}`)
		if err != nil {
			t.Fatalf("New(json): %v", err)
		}
		if adapter.summary != nil {
			t.Error("a JSON playlist must not carry an M3U classification summary")
		}
		if _, total, err := adapter.ListChannels("", 10, 0); err != nil || total != 1 {
			t.Errorf("channels total = %d (err %v), want 1", total, err)
		}
	})

	// An extension this adapter does not know is refused, naming the ones it does.
	t.Run("unknown extension", func(t *testing.T) {
		_, err := newRawFileAt(t, "playlist.txt", playlist)
		if !errors.Is(err, ErrUnsupportedFormat) {
			t.Fatalf("New(.txt) = %v, want ErrUnsupportedFormat", err)
		}
		for _, ext := range []string{".json", ".m3u", ".m3u8"} {
			if !strings.Contains(err.Error(), ext) {
				t.Errorf("error %q does not name %s", err, ext)
			}
		}
	})
}

// A .m3u that is not a playlist reports as a malformed M3U, and is never
// silently re-parsed as JSON.
func TestM3UMalformedReportsAsMalformed(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"prose", "this is not a playlist\njust some words\n"},
		{"empty", ""},
		{"whitespace only", "   \n\t\n"},
		{"json body in an m3u file", `{"channels":[{"id":"c","name":"Smuggled"}]}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adapter, err := newRawM3UAdapter(t, tc.body)
			if !errors.Is(err, ErrMalformedLibrary) {
				t.Fatalf("New = %v, want ErrMalformedLibrary", err)
			}
			if adapter != nil {
				t.Error("a malformed M3U must not yield an adapter")
			}
		})
	}
}

// Derived ids are stable across reloads of the same file, and round-trip back
// through the id-keyed lookups.
func TestM3UDerivedIDsAreStableAcrossReloads(t *testing.T) {
	playlist := "#EXTM3U\n" +
		"#EXTINF:-1 group-title=\"News\",News One\nhttps://cdn.example/news.m3u8\n" +
		"#EXTINF:-1 group-title=\"Movies\",A Film\nhttps://cdn.example/film.mp4\n" +
		"#EXTINF:-1 group-title=\"Series\",Breaking Bad S01E01\nhttps://cdn.example/bb1.mkv\n"

	first := mustRawM3UAdapter(t, playlist)
	second := mustRawM3UAdapter(t, playlist)

	channelsA, chTotal, _ := first.ListChannels("", 10, 0)
	channelsB, _, _ := second.ListChannels("", 10, 0)
	if chTotal != 1 || len(channelsB) != 1 {
		t.Fatalf("channels = %d/%d, want 1 each", chTotal, len(channelsB))
	}
	if channelsA[0].ID != channelsB[0].ID {
		t.Errorf("channel id changed across reloads: %q != %q", channelsA[0].ID, channelsB[0].ID)
	}
	if !strings.HasPrefix(channelsA[0].ID, "ch-") {
		t.Errorf("channel id %q lacks the ch- prefix", channelsA[0].ID)
	}

	entriesA, entryTotal, _ := first.GetEntries("", "", "", 1, 10)
	entriesB, _, _ := second.GetEntries("", "", "", 1, 10)
	if entryTotal != 2 || len(entriesB) != 2 {
		t.Fatalf("entries = %d/%d, want 2 each (a movie and a series)", entryTotal, len(entriesB))
	}
	for i := range entriesA {
		if entriesA[i].ID != entriesB[i].ID {
			t.Errorf("entry %q id changed across reloads: %q != %q",
				entriesA[i].Name, entriesA[i].ID, entriesB[i].ID)
		}
		if !strings.HasPrefix(entriesA[i].ID, "vod-") {
			t.Errorf("entry id %q lacks the vod- prefix", entriesA[i].ID)
		}
	}

	if _, err := first.GetChannel(channelsA[0].ID); err != nil {
		t.Errorf("GetChannel(derived): %v", err)
	}
	episodeCount := 0
	for _, e := range entriesA {
		entry, err := first.GetEntry(e.ID)
		if err != nil {
			t.Fatalf("GetEntry(derived %q): %v", e.ID, err)
		}
		episodes, err := first.GetEpisodes(entry.ID)
		if err != nil {
			t.Fatalf("GetEpisodes: %v", err)
		}
		episodeCount += len(episodes)
		for _, ep := range episodes {
			if _, err := first.GetEpisode(ep.ID); err != nil {
				t.Errorf("GetEpisode(derived %q): %v", ep.ID, err)
			}
		}
	}
	if episodeCount == 0 {
		t.Error("no episodes round-tripped; the fixture must contain at least one")
	}
}

// A genuinely distinct row is never discarded, even when its derivation inputs
// are identical to another row's.
func TestM3UDistinctRowsAreNotDiscarded(t *testing.T) {
	adapter := mustRawM3UAdapter(t, "#EXTM3U\n"+
		"#EXTINF:-1 group-title=\"News\",Twin\nhttps://cdn.example/twin1.m3u8\n"+
		"#EXTINF:-1 group-title=\"News\",Twin\nhttps://cdn.example/twin2.m3u8\n"+
		"#EXTINF:-1 group-title=\"Movies\",Same Film\nhttps://cdn.example/f1.mp4\n"+
		"#EXTINF:-1 group-title=\"Movies\",Same Film\nhttps://cdn.example/f2.mp4\n"+
		"#EXTINF:-1 group-title=\"Series\",Breaking Bad S01E01\nhttps://cdn.example/same.mkv\n"+
		"#EXTINF:-1 group-title=\"Series\",Breaking Bad S01E01\nhttps://cdn.example/same.mkv\n")

	channels, chTotal, err := adapter.ListChannels("", 10, 0)
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if chTotal != 2 {
		t.Fatalf("channels = %d, want 2", chTotal)
	}
	if channels[0].ID == channels[1].ID {
		t.Errorf("two distinct channels share id %q", channels[0].ID)
	}

	entries, entryTotal, err := adapter.GetEntries("", "", "", 1, 10)
	if err != nil {
		t.Fatalf("GetEntries: %v", err)
	}
	// Two movies that derive the same id must both survive as separate rows,
	// alongside the one series entry.
	if entryTotal != 3 {
		t.Fatalf("entries = %d, want 3 (an identical movie pair must both survive)", entryTotal)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if seen[e.ID] {
			t.Errorf("two distinct entries share id %q", e.ID)
		}
		seen[e.ID] = true
	}

	// The identical episodes of the one series both survive.
	series, found := findEntry(entries, "Breaking Bad")
	if !found || series.Type != "Series" {
		t.Fatalf("no Series entry named Breaking Bad; entries = %v", entryNames(entries))
	}
	episodes, err := adapter.GetEpisodes(series.ID)
	if err != nil {
		t.Fatalf("GetEpisodes: %v", err)
	}
	if len(episodes) != 2 {
		t.Errorf("identical episodes = %d, want 2", len(episodes))
	}
	if episodes[0].ID == episodes[1].ID {
		t.Errorf("identical episodes share id %q", episodes[0].ID)
	}
}

// A group whose name merely contains a VOD word is not VOD unless the word
// stands alone: whole-word matching, not substring.
func TestM3UGroupMatchingIsWholeWordAndCaseInsensitive(t *testing.T) {
	cases := []struct {
		group     string
		wantIsVOD bool
	}{
		{"Movies", true},
		{"MOVIES", true},
		{"  movies  ", true},
		{"VOD", true},
		{"TV Shows", true},
		{"Series", true},
		{"Showtime", false},
		{"Documentaries", false},
		{"News", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.group, func(t *testing.T) {
			if got := groupIsVOD(tc.group); got != tc.wantIsVOD {
				t.Errorf("groupIsVOD(%q) = %v, want %v", tc.group, got, tc.wantIsVOD)
			}
		})
	}
}

// The M3U entry does not regress the JSON path: a JSON file still loads as it
// always did, and is unaffected by the format selector.
func TestM3ULeavesJSONPathIntact(t *testing.T) {
	adapter := mustRawAdapter(t, `{
	  "channels": [{"id":"c","name":"C","category":"News","status":"active"}],
	  "streams": [{"id":"s","channel_id":"c","label":"m","url":"https://cdn.example/c.m3u8","status":"active"}]
	}`)

	if adapter.summary != nil {
		t.Error("a JSON adapter must carry no M3U summary")
	}
	channels, total, err := adapter.ListChannels("", 10, 0)
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	if total != 1 || channels[0].ID != "c" {
		t.Errorf("channels = %+v (total %d), want the explicit id c", channels, total)
	}
	stream, err := adapter.ResolveChannelStream("c")
	if err != nil {
		t.Fatalf("ResolveChannelStream: %v", err)
	}
	if stream.ID != "s" {
		t.Errorf("resolved stream = %q, want s", stream.ID)
	}
}

// findEntry returns the entry with the given name and whether one was found.
// Tests assert on the found flag before asserting about the entry, so an
// assertion about a missing entry fails loudly instead of passing vacuously
// because the loop body never ran.
func findEntry(entries []source.Entry, name string) (source.Entry, bool) {
	for _, e := range entries {
		if e.Name == name {
			return e, true
		}
	}
	return source.Entry{}, false
}
