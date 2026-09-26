package adobotvhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jmvbambico/adoboflix/internal/source"
)

// vodAsset is one element of the flat array served by the envelope's
// vod_library URL. `seasons` present means a series; absent means a movie.
// `video` is the same decoy placeholder the channel envelope carries and is
// never used as a stream URL — the playable leaf is runtime_attr_url.
type vodAsset struct {
	Category       string      `json:"category"`
	Name           string      `json:"name"`
	Info           vodInfo     `json:"info"`
	Video          string      `json:"video"`
	RuntimeAttrURL string      `json:"runtime_attr_url"`
	Seasons        []vodSeason `json:"seasons"`
}

type vodInfo struct {
	Cast     []string `json:"cast"`
	Director []string `json:"director"`
	Country  []string `json:"country"`
	Genre    []string `json:"genre"`
	Rating   string   `json:"rating"`
	Year     string   `json:"year"`
	Added    string   `json:"added"`
	Duration string   `json:"duration"`
	Poster   string   `json:"poster"`
	Trailer  string   `json:"trailer"`
}

type vodSeason struct {
	Season   int          `json:"season"`
	Episodes []vodEpisode `json:"episodes"`
}

type vodEpisode struct {
	Episode        int    `json:"episode"`
	Video          string `json:"video"`
	RuntimeAttrURL string `json:"runtime_attr_url"`
}

// fetchVODLibrary follows the pre-tokenized vod_library URL from the envelope.
// An empty URL is not an error: the subscriber simply has no active VOD
// collection, and AdoboFlix serves an empty VOD library.
func (a *Adapter) fetchVODLibrary(ctx context.Context, env *envelope) ([]vodAsset, error) {
	if strings.TrimSpace(env.Provider.VODLibrary) == "" {
		return []vodAsset{}, nil
	}

	body, status, err := a.get(ctx, env.Provider.VODLibrary)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		detail := upstreamMessage(body)
		lower := strings.ToLower(detail)
		switch {
		case strings.Contains(lower, "not active"):
			return nil, subscriptionInactiveError(detail)
		default:
			return nil, upstreamError(status, body)
		}
	}

	// The library is a flat JSON array. A non-array body (for example the
	// {"success":...} wrapper used when no collection is active) is reported
	// as malformed rather than silently treated as empty.
	var assets []vodAsset
	if err := json.Unmarshal(body, &assets); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedVODLibrary, err)
	}
	return assets, nil
}

// entriesFrom maps cached assets to the internal VOD format. StreamURL and the
// DRM fields are intentionally left empty: they require a per-item
// runtime_attr_url resolution, which happens only when the player asks to play
// something. That keeps a library listing from exporting any resolvable URL.
func entriesFrom(assets []vodAsset) []source.Entry {
	entries := make([]source.Entry, 0, len(assets))
	for _, asset := range assets {
		entries = append(entries, entryFrom(asset))
	}
	return entries
}

func entryFrom(asset vodAsset) source.Entry {
	e := source.Entry{
		ID:         vodAssetID(asset.Category, asset.Name),
		Name:       asset.Name,
		Type:       assetType(asset),
		Status:     "active",
		SourceType: sourceType,
	}
	if asset.Category != "" {
		category := asset.Category
		e.Category = &category
	}
	if asset.Info.Poster != "" {
		poster := asset.Info.Poster
		e.Poster = &poster
	}
	if asset.Info.Rating != "" {
		rating := asset.Info.Rating
		e.Rating = &rating
	}
	e.CastMembers = append(e.CastMembers, asset.Info.Cast...)
	e.Directors = append(e.Directors, asset.Info.Director...)
	if year, err := strconv.Atoi(strings.TrimSpace(asset.Info.Year)); err == nil && year > 0 {
		e.ReleaseYear = &year
	}
	return e
}

// assetType is the contentType the client filters on: a series carries
// seasons, a movie does not.
func assetType(asset vodAsset) string {
	if len(asset.Seasons) > 0 {
		return "Series"
	}
	return "Movie"
}

// sortEntriesNewestFirst orders by the info.added unix timestamp descending,
// then by name, so listing order is stable across refetches.
func sortEntriesNewestFirst(entries []source.Entry, assets []vodAsset) {
	added := make(map[string]int64, len(assets))
	byID := make(map[string]vodAsset, len(assets))
	for _, asset := range assets {
		id := vodAssetID(asset.Category, asset.Name)
		byID[id] = asset
		if ts, err := strconv.ParseInt(strings.TrimSpace(asset.Info.Added), 10, 64); err == nil {
			added[id] = ts
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		ai, aj := added[entries[i].ID], added[entries[j].ID]
		if ai != aj {
			return ai > aj
		}
		return byID[entries[i].ID].Name < byID[entries[j].ID].Name
	})
}

// paginate returns the page slice and total for a 1-based page number,
// mirroring the SQL adapter's LIMIT/OFFSET arithmetic.
func paginate[T any](items []T, page, limit int) ([]T, int) {
	total := len(items)
	if limit <= 0 {
		if page <= 1 {
			return items, total
		}
		return []T{}, total
	}
	if page < 1 {
		page = 1
	}
	start := (page - 1) * limit
	if start >= total {
		return []T{}, total
	}
	end := start + limit
	if end > total {
		end = total
	}
	return items[start:end], total
}

// filterEntries applies the provider / genre / contentType filters the
// interface promises, over the cached library.
func filterEntries(assets []vodAsset, provider, genre, contentType string) []vodAsset {
	out := make([]vodAsset, 0, len(assets))
	for _, asset := range assets {
		if provider != "" && !strings.EqualFold(provider, sourceType) {
			continue
		}
		if genre != "" && !assetHasGenre(asset, genre) {
			continue
		}
		if contentType != "" && !strings.EqualFold(contentType, assetType(asset)) {
			continue
		}
		out = append(out, asset)
	}
	return out
}

func assetHasGenre(asset vodAsset, genre string) bool {
	if strings.EqualFold(asset.Category, genre) {
		return true
	}
	for _, g := range asset.Info.Genre {
		if strings.EqualFold(g, genre) {
			return true
		}
	}
	return false
}

// distinctGenres is the union of asset categories and info.genre values,
// sorted, and is what GetGenres reports.
func distinctGenres(assets []vodAsset) []string {
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, v)
	}
	for _, asset := range assets {
		add(asset.Category)
		for _, g := range asset.Info.Genre {
			add(g)
		}
	}
	sort.Strings(out)
	return out
}

// episodesOf flattens an asset's seasons into episodes ordered by season then
// episode. StreamURL is left nil: resolving each leaf would be one HTTP call
// per episode, and the resolve endpoints fetch the single needed one.
func episodesOf(assetID string, asset vodAsset) []source.Episode {
	episodes := []source.Episode{}
	for _, season := range asset.Seasons {
		for _, ep := range season.Episodes {
			episodes = append(episodes, source.Episode{
				ID:            episodeID(assetID, season.Season, ep.Episode),
				VodID:         assetID,
				SeasonNumber:  season.Season,
				EpisodeNumber: ep.Episode,
				SourceType:    sourceType,
			})
		}
	}
	sort.SliceStable(episodes, func(i, j int) bool {
		if episodes[i].SeasonNumber != episodes[j].SeasonNumber {
			return episodes[i].SeasonNumber < episodes[j].SeasonNumber
		}
		return episodes[i].EpisodeNumber < episodes[j].EpisodeNumber
	})
	return episodes
}
