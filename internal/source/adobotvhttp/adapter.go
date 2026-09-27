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
	"strconv"
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

// minBilledTill is the earliest billed_till AdoboFlix treats as a real expiry:
// 2019-01-01T00:00:00Z. Anything earlier is refused as a sentinel rather than
// rendered as a date.
//
// Upstream sends "0" for accounts whose expiry must be hidden, which alone
// renders as "1 Jan 1970"; but every value through the end of 1970 (1 to
// 31535999) is equally indistinguishable from that sentinel, and none is a
// plausible subscription date for a platform that did not exist then. The floor
// is set far above that band and far below any real AdoboTV subscription, so it
// never rejects a genuine lapse — a 2019-or-later expiry is real information and
// is still shown. It is deliberately not an upper bound: a far-future lifetime
// or reseller expiry is also real.
const minBilledTill = 1546300800 // 2019-01-01T00:00:00Z

// The adapter must satisfy the read-only boundary, can supply the compiled EPG
// bytes, and can describe the subscriber's account. It must NOT satisfy
// StreamProbeLister; that omission is what keeps the optional capability
// honest.
var (
	_ source.Source               = (*Adapter)(nil)
	_ source.CompiledEPGProvider  = (*Adapter)(nil)
	_ source.AccountInfoProvider  = (*Adapter)(nil)
	_ source.SyncProvider         = (*Adapter)(nil)
	_ source.ContextChannelLister = (*Adapter)(nil)
)

func init() {
	// The source.Config database handle is intentionally ignored: this adapter
	// has no database access by construction. The playlist code is taken from
	// Config when supplied (a code the user entered at runtime) and from the
	// environment otherwise.
	// Selectable marks it as one of the two end-user paths in AGENTS.md; it is
	// never Dev, so the UI presents it as a normal choice.
	source.Register(Name, source.Requirement{PlaylistCode: true, Selectable: true}, func(cfg source.Config) (source.Source, error) {
		return newFromEnvWithCode(cfg.PlaylistCode)
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

// invalidateCache drops the cached envelope and VOD library so the next read
// refetches from upstream. It is called when AdoboTV rejects a content token
// that the cached envelope minted: the tokenized URLs in that envelope are all
// dead, and keeping them would re-send the same token until the TTL lapsed. The
// next fetch mints a fresh token.
func (a *Adapter) invalidateCache() {
	a.mu.Lock()
	a.env = nil
	a.envAt = time.Time{}
	a.vod = nil
	a.vodAt = time.Time{}
	a.mu.Unlock()
}

// --- sync (optional capability) ---------------------------------------------

// Refresh re-fetches the playlist envelope and VOD library from upstream now,
// so the next read is warm, and records the fetch time. It is the optional
// source.SyncProvider capability; an adapter with no upstream library does not
// implement it.
//
// It fetches into locals and swaps the cache only once both fetches succeed, so
// a failed refresh leaves the library the caller is already serving from
// untouched. A refresh is a convenience ahead of use, not a reason to drop a
// good cache on a network blip — the next normal read is still the source of
// truth. It holds fetchMu so it serialises with the read path instead of racing
// a concurrent miss.
func (a *Adapter) Refresh(ctx context.Context) error {
	a.fetchMu.Lock()
	defer a.fetchMu.Unlock()

	env, err := a.fetchEnvelope(ctx)
	if err != nil {
		return err
	}
	assets, err := a.fetchVODLibrary(ctx, env)
	if err != nil {
		return err
	}
	if assets == nil {
		assets = []vodAsset{}
	}

	now := a.now()
	a.mu.Lock()
	a.env = env
	a.envAt = now
	a.vod = assets
	a.vodAt = now
	a.mu.Unlock()
	return nil
}

// LastSyncedAt reports when the playlist envelope was last fetched from
// upstream, from the timestamp the cache already records. It reads cached state
// only and never triggers a fetch, so the status endpoint can report it without
// touching the network. ok is false before anything has been fetched.
func (a *Adapter) LastSyncedAt() (time.Time, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.env == nil || a.envAt.IsZero() {
		return time.Time{}, false
	}
	return a.envAt, true
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
			ID:       channelID(ch.Name, ch.EpgID, ch.Category),
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
	return a.listChannels(context.Background(), category, limit, offset)
}

// ListChannelsContext is ListChannels with a caller's context, so the fetch it
// may trigger is cancelled when the context is. It is the optional
// source.ContextChannelLister capability, used by the boot credential re-check
// so a shutdown does not wait out an unreachable upstream's HTTP timeout.
func (a *Adapter) ListChannelsContext(ctx context.Context, category string, limit, offset int) ([]source.Channel, int, error) {
	return a.listChannels(ctx, category, limit, offset)
}

func (a *Adapter) listChannels(ctx context.Context, category string, limit, offset int) ([]source.Channel, int, error) {
	views, err := a.channels(ctx)
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
		if err := a.gateError(body); err != nil {
			return nil, "", err
		}
		return nil, "", upstreamError(status, body)
	}
	sum := sha256.Sum256(body)
	return body, hex.EncodeToString(sum[:]), nil
}

// --- account (optional capability) ------------------------------------------

// AccountInfo reports the account facts the playlist envelope carries: the
// operator's user_message and the subscription's billing expiry.
//
// It is cache-only: it reads the envelope only when it is already warm and
// never triggers a fetch. /api/v1/source/status calls this to decorate its
// response, and that endpoint must stay fast, so a cold cache reports
// ErrAccountInfoUnavailable and the facts appear on a later poll once the
// library has fetched the envelope — which a normal app load does anyway.
// Making status wait on upstream here would let an unreachable AdoboTV stall
// the account UI for the client's whole 30s timeout.
//
// billed_till is a string of unix seconds upstream. It is absent for
// non-subscription tiers, and upstream currently sends "0" — rather than
// omitting the field — for accounts whose expiry must not be shown (AdoboTV's
// own hiding task is outstanding). A value below minBilledTill is treated as
// that sentinel rather than a date, because rendering it would assert an expiry
// we do not know (see minBilledTill); so is a value that is present but not a
// unix-seconds integer. Either way it is treated exactly like an absent field
// and cannot hide a perfectly good user_message. A real expiry — past or future
// — keeps its value: a lapsed subscription is information, not silence.
func (a *Adapter) AccountInfo(ctx context.Context) (source.AccountInfo, error) {
	if err := ctx.Err(); err != nil {
		return source.AccountInfo{}, err
	}
	env := a.cachedEnvelope()
	if env == nil {
		return source.AccountInfo{}, source.ErrAccountInfoUnavailable
	}
	info := source.AccountInfo{UserMessage: strings.TrimSpace(env.Provider.UserMessage)}
	if raw := strings.TrimSpace(env.Provider.BilledTill); raw != "" {
		if secs, err := strconv.ParseInt(raw, 10, 64); err == nil && secs >= minBilledTill {
			expiry := time.Unix(secs, 0).UTC()
			info.SubscriptionExpiresAt = &expiry
		}
	}
	return info, nil
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
