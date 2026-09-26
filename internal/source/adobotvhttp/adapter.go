// Package adobotvhttp implements the adobotv-http source adapter: the real
// subscriber path, where a user pastes an AdoboTV playlist code and AdoboFlix
// becomes their web player.
//
// It is an HTTP client and nothing else. It never imports internal/db, sqlx
// or a database driver, so the read-only invariant in AGENTS.md holds
// structurally: there is no handle through which it could write.
//
// It implements source.Source and the optional source.CompiledEPGProvider. It
// deliberately does NOT implement source.StreamProbeLister: an HTTP adapter
// only ever sees one subscriber's playlist, never the operator's whole
// library, and health scanning is a report about that whole library.
//
// # The three gates
//
// AdoboTV rejects a real user in three different ways, and each is surfaced as
// its own error rather than a generic failure:
//
//   - An unapproved device gets HTTP 200 with an M3U splash body, even though
//     JSON was requested (ErrDevicePending). Every first connect hits this.
//   - An inactive subscriber gets a full playlist and then has every playable
//     URL refused (ErrSubscriptionInactive); the playlist endpoint admits
//     inactive accounts, ContentAuthMiddleware admits active only.
//   - A bad code is refused outright with 403 (ErrPlaylistRejected).
//
// # The decoy
//
// channels[].url (and the VOD library's video field) is a placeholder by
// design: the real source is withheld so a shared playlist cannot reveal the
// CDN. The playable path is always runtime_attr_url. No method here reads the
// decoy, and no method exports a resolvable stream URL.
package adobotvhttp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jmvbambico/adoboflix/internal/source"
)

// Name is the value of ADOBOFLIX_SOURCE that selects this adapter.
const Name = "adobotv-http"

// sourceType is the provider value every item reports. For an HTTP subscriber
// the provider is the platform itself.
const sourceType = "adobotv"

// The adapter must satisfy the read-only boundary and can supply the compiled
// EPG bytes. It must NOT satisfy StreamProbeLister; that omission is what
// keeps the optional capability honest.
var (
	_ source.Source              = (*Adapter)(nil)
	_ source.CompiledEPGProvider = (*Adapter)(nil)
)

func init() {
	// The source.Config database handle is intentionally ignored: this adapter
	// has no database access by construction.
	source.Register(Name, func(source.Config) (source.Source, error) {
		return NewFromEnv()
	})
}

// Adapter is the adobotv-http source. It holds one subscriber's playlist code
// and an in-memory cache of the envelope and VOD library.
type Adapter struct {
	baseURL      string
	playlistCode string
	userAgent    string
	cacheTTL     time.Duration
	httpClient   *http.Client
	now          func() time.Time

	mu    sync.RWMutex
	env   *envelope
	envAt time.Time
	vod   []vodAsset
	vodAt time.Time

	// fetchMu serialises upstream fetches so concurrent reads do not stampede
	// AdoboTV with duplicate playlist/library requests.
	fetchMu sync.Mutex
}

// NewFromEnv builds the adapter from the process environment, failing fast and
// naming the missing key when a required value is absent. It never falls back
// to another source: selection is the registry's job.
func NewFromEnv() (*Adapter, error) {
	cfg, err := configFromEnv()
	if err != nil {
		return nil, err
	}
	return newWithConfig(cfg)
}

// Name returns the adapter's registered name.
func (a *Adapter) Name() string { return Name }

// --- envelope cache ---------------------------------------------------------

// library returns the cached playlist envelope, refetching when the cache is
// empty or stale. A cacheTTL of zero disables caching (used by tests).
func (a *Adapter) library(ctx context.Context) (*envelope, error) {
	if env := a.cachedEnvelope(); env != nil {
		return env, nil
	}

	a.fetchMu.Lock()
	defer a.fetchMu.Unlock()
	// Another goroutine may have fetched while we waited for the lock.
	if env := a.cachedEnvelope(); env != nil {
		return env, nil
	}

	env, err := a.fetchEnvelope(ctx)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.env = env
	a.envAt = a.now()
	a.mu.Unlock()
	return env, nil
}

func (a *Adapter) cachedEnvelope() *envelope {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.env == nil || a.cacheTTL <= 0 {
		return nil
	}
	if a.now().Sub(a.envAt) >= a.cacheTTL {
		return nil
	}
	return a.env
}

// vodLibrary returns the cached VOD assets, following the envelope's
// pre-tokenized vod_library URL on a miss.
func (a *Adapter) vodLibrary(ctx context.Context) ([]vodAsset, error) {
	env, err := a.library(ctx)
	if err != nil {
		return nil, err
	}
	if assets := a.cachedVOD(); assets != nil {
		return assets, nil
	}

	a.fetchMu.Lock()
	defer a.fetchMu.Unlock()
	if assets := a.cachedVOD(); assets != nil {
		return assets, nil
	}

	assets, err := a.fetchVODLibrary(ctx, env)
	if err != nil {
		return nil, err
	}
	if assets == nil {
		assets = []vodAsset{}
	}
	a.mu.Lock()
	a.vod = assets
	a.vodAt = a.now()
	a.mu.Unlock()
	return assets, nil
}

func (a *Adapter) cachedVOD() []vodAsset {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.vod == nil || a.cacheTTL <= 0 {
		return nil
	}
	if a.now().Sub(a.vodAt) >= a.cacheTTL {
		return nil
	}
	return a.vod
}

// --- VOD (Source) -----------------------------------------------------------

func (a *Adapter) GetStats() (*source.Stats, error) {
	assets, err := a.vodLibrary(context.Background())
	if err != nil {
		return nil, err
	}
	stats := &source.Stats{
		TotalTitles: len(assets),
		TotalGenres: len(distinctGenres(assets)),
	}
	if len(assets) > 0 {
		stats.TotalProviders = 1
	}
	return stats, nil
}

func (a *Adapter) GetEntries(provider, genre, contentType string, page, limit int) ([]source.Entry, int, error) {
	assets, err := a.vodLibrary(context.Background())
	if err != nil {
		return nil, 0, err
	}
	filtered := filterEntries(assets, provider, genre, contentType)
	entries := entriesFrom(filtered)
	sortEntriesNewestFirst(entries, filtered)
	pageEntries, total := paginate(entries, page, limit)
	return pageEntries, total, nil
}

func (a *Adapter) GetEntry(id string) (*source.Entry, error) {
	assets, err := a.vodLibrary(context.Background())
	if err != nil {
		return nil, err
	}
	asset, ok := findAsset(assets, id)
	if !ok {
		return nil, fmt.Errorf("%w: no VOD asset with id %q", ErrContentNotFound, id)
	}
	entry := entryFrom(asset)
	return &entry, nil
}

func (a *Adapter) Search(q, provider, genre, contentType string, page, limit int) ([]source.Entry, int, error) {
	assets, err := a.vodLibrary(context.Background())
	if err != nil {
		return nil, 0, err
	}
	matched := make([]vodAsset, 0, len(assets))
	for _, asset := range assets {
		if matchesQuery(asset, q) {
			matched = append(matched, asset)
		}
	}
	filtered := filterEntries(matched, provider, genre, contentType)
	entries := entriesFrom(filtered)
	sortEntriesNewestFirst(entries, filtered)
	pageEntries, total := paginate(entries, page, limit)
	return pageEntries, total, nil
}

func (a *Adapter) GetProviders() ([]string, error) {
	assets, err := a.vodLibrary(context.Background())
	if err != nil {
		return nil, err
	}
	if len(assets) == 0 {
		return []string{}, nil
	}
	return []string{sourceType}, nil
}

func (a *Adapter) GetGenres() ([]string, error) {
	assets, err := a.vodLibrary(context.Background())
	if err != nil {
		return nil, err
	}
	genres := distinctGenres(assets)
	if genres == nil {
		genres = []string{}
	}
	return genres, nil
}

func (a *Adapter) EpisodeCounts(vodIDs []string) (map[string]int, error) {
	counts := map[string]int{}
	if len(vodIDs) == 0 {
		return counts, nil
	}
	assets, err := a.vodLibrary(context.Background())
	if err != nil {
		return nil, err
	}
	byID := make(map[string]vodAsset, len(assets))
	for _, asset := range assets {
		byID[vodAssetID(asset.Category, asset.Name)] = asset
	}
	for _, id := range vodIDs {
		asset, ok := byID[id]
		if !ok {
			continue
		}
		total := 0
		for _, season := range asset.Seasons {
			total += len(season.Episodes)
		}
		if total > 0 {
			counts[id] = total
		}
	}
	return counts, nil
}

func (a *Adapter) GetVodStreams(vodID string) ([]source.VodStream, error) {
	assets, err := a.vodLibrary(context.Background())
	if err != nil {
		return nil, err
	}
	asset, ok := findAsset(assets, vodID)
	if !ok {
		return nil, fmt.Errorf("%w: no VOD asset with id %q", ErrContentNotFound, vodID)
	}
	// A series asset has no runtime_attr_url of its own; its episodes do. An
	// empty stream list is the honest answer for it.
	if strings.TrimSpace(asset.RuntimeAttrURL) == "" {
		return []source.VodStream{}, nil
	}
	resolved, err := a.resolveRuntimeAttr(context.Background(), asset.RuntimeAttrURL)
	if err != nil {
		return nil, err
	}
	return []source.VodStream{vodStreamFrom(vodID, resolved)}, nil
}

func (a *Adapter) GetEpisodes(vodID string) ([]source.Episode, error) {
	assets, err := a.vodLibrary(context.Background())
	if err != nil {
		return nil, err
	}
	asset, ok := findAsset(assets, vodID)
	if !ok {
		return nil, fmt.Errorf("%w: no VOD asset with id %q", ErrContentNotFound, vodID)
	}
	return episodesOf(vodID, asset), nil
}

func (a *Adapter) GetEpisode(id string) (*source.Episode, error) {
	assets, err := a.vodLibrary(context.Background())
	if err != nil {
		return nil, err
	}
	for _, asset := range assets {
		assetID := vodAssetID(asset.Category, asset.Name)
		for _, season := range asset.Seasons {
			for _, ep := range season.Episodes {
				if id != episodeID(assetID, season.Season, ep.Episode) {
					continue
				}
				resolved, err := a.resolveRuntimeAttr(context.Background(), ep.RuntimeAttrURL)
				if err != nil {
					return nil, err
				}
				url := resolved.URL
				return &source.Episode{
					ID:            id,
					VodID:         assetID,
					SeasonNumber:  season.Season,
					EpisodeNumber: ep.Episode,
					StreamURL:     &url,
					SourceType:    sourceType,
					DrmType:       resolved.DrmType,
					DrmK:          resolved.DrmK,
					LicenseURL:    resolved.LicenseURL,
					UserAgent:     resolved.UserAgent,
					Referer:       resolved.Referer,
				}, nil
			}
		}
	}
	return nil, fmt.Errorf("%w: no episode with id %q", ErrContentNotFound, id)
}

// --- channels (Source) ------------------------------------------------------

// channelView is a mapped channel plus the raw envelope entry it came from, so
// id lookups and stream resolution share one pass.
type channelView struct {
	channel source.Channel
	slug    string
	raw     channelInfo
}

func (a *Adapter) channels(ctx context.Context) ([]channelView, error) {
	env, err := a.library(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]channelView, 0, len(env.Channels))
	for _, ch := range env.Channels {
		// The envelope gives a slug-keyed category table with display names;
		// prefer the display name so the UI shows "Movies", not "movies".
		display := ch.Category
		if cat, ok := env.Categories[ch.Category]; ok && cat.Name != "" {
			display = cat.Name
		}
		channel := source.Channel{
			ID:       channelID(ch.Name, ch.EpgID),
			Name:     ch.Name,
			Category: stringPtr(display),
			Status:   "active",
		}
		channel.Logo = stringPtr(ch.Icon)
		channel.EpgChannelID = stringPtr(ch.EpgID)
		views = append(views, channelView{channel: channel, slug: ch.Category, raw: ch})
	}
	return views, nil
}

func (a *Adapter) ListChannels(category string, limit, offset int) ([]source.Channel, int, error) {
	views, err := a.channels(context.Background())
	if err != nil {
		return nil, 0, err
	}

	filtered := make([]channelView, 0, len(views))
	for _, v := range views {
		if !channelMatchesCategory(v, category) {
			continue
		}
		filtered = append(filtered, v)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		ci, cj := derefOr(filtered[i].channel.Category, ""), derefOr(filtered[j].channel.Category, "")
		if ci != cj {
			return ci < cj
		}
		return filtered[i].channel.Name < filtered[j].channel.Name
	})

	total := len(filtered)
	pageViews := offsetPaginate(filtered, limit, offset)
	channels := make([]source.Channel, 0, len(pageViews))
	for _, v := range pageViews {
		channels = append(channels, v.channel)
	}
	return channels, total, nil
}

func (a *Adapter) ListChannelCategories() ([]string, error) {
	views, err := a.channels(context.Background())
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	categories := []string{}
	for _, v := range views {
		name := derefOr(v.channel.Category, "")
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		categories = append(categories, name)
	}
	sort.Strings(categories)
	return categories, nil
}

func (a *Adapter) GetChannel(channelID string) (*source.Channel, error) {
	views, err := a.channels(context.Background())
	if err != nil {
		return nil, err
	}
	for _, v := range views {
		if v.channel.ID == channelID {
			channel := v.channel
			return &channel, nil
		}
	}
	return nil, fmt.Errorf("%w: no channel with id %q", ErrContentNotFound, channelID)
}

func (a *Adapter) GetChannelWithStreams(channelID string) (*source.Channel, []source.Stream, error) {
	view, err := a.findChannel(context.Background(), channelID)
	if err != nil {
		return nil, nil, err
	}
	stream, err := a.resolveChannel(*view)
	if err != nil {
		return nil, nil, err
	}
	return &view.channel, []source.Stream{*stream}, nil
}

func (a *Adapter) ResolveChannelStream(channelID string) (*source.Stream, error) {
	view, err := a.findChannel(context.Background(), channelID)
	if err != nil {
		return nil, err
	}
	return a.resolveChannel(*view)
}

func (a *Adapter) findChannel(ctx context.Context, channelID string) (*channelView, error) {
	views, err := a.channels(ctx)
	if err != nil {
		return nil, err
	}
	for i := range views {
		if views[i].channel.ID == channelID {
			return &views[i], nil
		}
	}
	return nil, fmt.Errorf("%w: no channel with id %q", ErrContentNotFound, channelID)
}

func (a *Adapter) resolveChannel(view channelView) (*source.Stream, error) {
	resolved, err := a.resolveRuntimeAttr(context.Background(), view.raw.RuntimeAttrURL)
	if err != nil {
		return nil, err
	}
	stream := streamFrom(view.channel.ID, resolved)
	return &stream, nil
}

// --- EPG (optional capability) ----------------------------------------------

// CompiledEPG fetches the gzipped XMLTV blob from the envelope's pre-tokenized
// EPG URL. The blob is returned still gzipped; internal/epg decompresses and
// parses it identically for every source. The hash is computed over the bytes
// because AdoboTV does not expose the compiler's hash.
func (a *Adapter) CompiledEPG() ([]byte, string, error) {
	ctx := context.Background()
	env, err := a.library(ctx)
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(env.Provider.EPG) == "" {
		return nil, "", fmt.Errorf("%w: the playlist envelope carries no EPG URL", ErrContentNotFound)
	}
	body, status, err := a.get(ctx, env.Provider.EPG)
	if err != nil {
		return nil, "", err
	}
	if status < 200 || status >= 300 {
		return nil, "", upstreamError(status, body)
	}
	sum := sha256.Sum256(body)
	return body, hex.EncodeToString(sum[:]), nil
}

// --- helpers ----------------------------------------------------------------

func channelMatchesCategory(view channelView, category string) bool {
	if category == "" || category == "All" {
		return true
	}
	if slug := strings.TrimSpace(view.slug); slug != "" && strings.EqualFold(slug, category) {
		return true
	}
	return strings.EqualFold(derefOr(view.channel.Category, ""), category)
}

func matchesQuery(asset vodAsset, q string) bool {
	if strings.TrimSpace(q) == "" {
		return true
	}
	needle := strings.ToLower(q)
	return strings.Contains(strings.ToLower(asset.Name), needle) ||
		strings.Contains(strings.ToLower(asset.Category), needle)
}

func findAsset(assets []vodAsset, id string) (vodAsset, bool) {
	for _, asset := range assets {
		if vodAssetID(asset.Category, asset.Name) == id {
			return asset, true
		}
	}
	return vodAsset{}, false
}

func streamFrom(channelID string, resolved *resolvedStream) source.Stream {
	return source.Stream{
		ID:         channelID + "-stream",
		ChannelID:  channelID,
		Label:      "default",
		URL:        resolved.URL,
		SourceType: sourceType,
		DrmType:    resolved.DrmType,
		DrmK:       resolved.DrmK,
		LicenseURL: resolved.LicenseURL,
		IsDefault:  true,
		Status:     "active",
		UserAgent:  resolved.UserAgent,
		Referer:    resolved.Referer,
	}
}

func vodStreamFrom(vodID string, resolved *resolvedStream) source.VodStream {
	return source.VodStream{
		ID:         vodID + "-stream",
		VodID:      vodID,
		Label:      "default",
		URL:        resolved.URL,
		SourceType: sourceType,
		DrmType:    resolved.DrmType,
		DrmK:       resolved.DrmK,
		LicenseURL: resolved.LicenseURL,
		IsDefault:  true,
		Status:     "active",
		UserAgent:  resolved.UserAgent,
		Referer:    resolved.Referer,
	}
}

func offsetPaginate[T any](items []T, limit, offset int) []T {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(items) {
		return []T{}
	}
	if limit <= 0 {
		return items[offset:]
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func derefOr(s *string, fallback string) string {
	if s == nil || *s == "" {
		return fallback
	}
	return *s
}
