package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gin-gonic/gin"
	"github.com/jmvbambico/adoboflix/internal/epg"
	"github.com/jmvbambico/adoboflix/internal/scanner"
	"github.com/jmvbambico/adoboflix/internal/source"
)

type PlayerHandler struct {
	source *swappableSource
	epg    atomic.Pointer[epg.Service]
	creds  *credentialStore
	scan   scanState
}

// swappableSource holds the active source behind an atomic pointer. Reads are
// hot and a swap is rare, so the load path is a single atomic operation with no
// lock; an in-flight read keeps whichever source it loaded, so a swap can never
// race it. Entering a playlist code reopens the adapter and stores the new one
// here.
type swappableSource struct {
	ptr atomic.Pointer[source.Source]
}

func newSwappableSource(src source.Source) *swappableSource {
	s := &swappableSource{}
	s.Store(src)
	return s
}

func (s *swappableSource) Load() source.Source {
	if p := s.ptr.Load(); p != nil {
		return *p
	}
	return nil
}

func (s *swappableSource) Store(src source.Source) { s.ptr.Store(&src) }

func NewPlayerHandler(src source.Source) *PlayerHandler {
	return &PlayerHandler{source: newSwappableSource(src), creds: newCredentialStore()}
}

// src returns the active source. Every handler reads it through here rather
// than caching it, because a playlist-code swap can replace it between
// requests.
func (h *PlayerHandler) src() source.Source { return h.source.Load() }

// SwapSource replaces the active source and rebuilds the EPG service around
// the new one, so the guide is fetched with the new credential rather than the
// previous adapter's. A swap to a source without the EPG capability leaves EPG
// reporting itself unavailable, exactly as a source that never had it.
//
// It also invalidates the health-scan manager: a scan built for the previous
// source must stop probing it and can never have its report served as the new
// source's. See scanState.invalidate.
func (h *PlayerHandler) SwapSource(src source.Source) {
	h.source.Store(src)
	if provider, ok := src.(source.CompiledEPGProvider); ok {
		h.epg.Store(epg.NewService(provider))
	} else {
		h.epg.Store(nil)
	}
	h.scan.invalidate(src)
}

func (h *PlayerHandler) WithEPG(service *epg.Service) *PlayerHandler {
	h.epg.Store(service)
	return h
}

func (h *PlayerHandler) GetStats(c *gin.Context) {
	stats, err := h.src().GetStats()
	if err != nil {
		writeSourceError(c, err, "")
		return
	}
	c.JSON(http.StatusOK, stats)
}

func (h *PlayerHandler) GetEntries(c *gin.Context) {
	provider := c.Query("provider")
	genre := c.Query("genre")
	contentType := c.Query("type")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))

	entries, total, err := h.src().GetEntries(provider, genre, contentType, page, limit)
	if err != nil {
		writeSourceError(c, err, "")
		return
	}

	// Attach episode counts for Series entries
	type enrichedEntry struct {
		source.Entry
		EpisodeCount *int `json:"episode_count,omitempty"`
	}
	enriched := make([]enrichedEntry, len(entries))

	// Collect the Series ids on this page and resolve every episode count in a
	// single call instead of one per entry.
	seriesIDs := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Type == "Series" {
			seriesIDs = append(seriesIDs, e.ID)
		}
	}

	episodeCounts := map[string]int{}
	countsAvailable := false
	if len(seriesIDs) > 0 {
		counts, err := h.src().EpisodeCounts(seriesIDs)
		if err != nil {
			// Do not report a misleading 0 for every Series: leave the field
			// absent, exactly as when the count lookup was unavailable.
			log.Printf("entries: episode count query failed for %d series: %v", len(seriesIDs), err)
		} else {
			countsAvailable = true
			episodeCounts = counts
		}
	}

	for i, e := range entries {
		enriched[i].Entry = e
		if e.Type == "Series" && countsAvailable {
			count := episodeCounts[e.ID]
			enriched[i].EpisodeCount = &count
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"entries": enriched, "total": total,
		"page": page, "has_more": (page * limit) < total,
	})
}

func (h *PlayerHandler) GetEntry(c *gin.Context) {
	entry, err := h.src().GetEntry(c.Param("id"))
	if err != nil {
		writeSourceError(c, err, "Entry not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"entry": entry})
}

func (h *PlayerHandler) Search(c *gin.Context) {
	q := c.Query("q")
	provider := c.Query("provider")
	genre := c.Query("genre")
	contentType := c.Query("type")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))

	entries, total, err := h.src().Search(q, provider, genre, contentType, page, limit)
	if err != nil {
		writeSourceError(c, err, "")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"results": entries, "total": total,
		"page": page, "has_more": (page * limit) < total,
	})
}

func (h *PlayerHandler) GetProviders(c *gin.Context) {
	providers, err := h.src().GetProviders()
	if err != nil {
		writeSourceError(c, err, "")
		return
	}
	c.JSON(http.StatusOK, gin.H{"providers": providers})
}

func (h *PlayerHandler) GetGenres(c *gin.Context) {
	genres, err := h.src().GetGenres()
	if err != nil {
		writeSourceError(c, err, "")
		return
	}
	c.JSON(http.StatusOK, gin.H{"genres": genres})
}

// derefString safely unwraps a nullable *string into a plain string.
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// buildProxyURL wraps a raw stream URL through /api/v1/proxy: QueryEscape the
// url, append &source=, then optional &ua= / &ref= when the stream carries its
// own headers.
//
// Any HTTP Basic credentials the stream URL carries in its userinfo are moved
// into the server-side credential store first, so the browser is handed a URL
// without them; the proxy re-attaches them as a header. See credentials.go.
func (h *PlayerHandler) buildProxyURL(scheme, host, rawURL, sourceType, ua, ref string) string {
	proxyURL := fmt.Sprintf("%s://%s/api/v1/proxy?url=%s&source=%s",
		scheme, host, url.QueryEscape(h.creds.stash(rawURL)), sourceType)
	if ua != "" {
		proxyURL += "&ua=" + url.QueryEscape(ua)
	}
	if ref != "" {
		proxyURL += "&ref=" + url.QueryEscape(ref)
	}
	return proxyURL
}

// resolveAlternate is one non-primary stream from vod_streams, already wrapped
// through the proxy so it is directly playable. Additive field on /api/v1/resolve.
type resolveAlternate struct {
	URL        string `json:"url"`
	Provider   string `json:"provider"`
	SourceType string `json:"source_type"`
	DrmType    string `json:"drm_type"`
	DrmK       string `json:"drm_k"`
	LicenseURL string `json:"license_url"`
	UserAgent  string `json:"user_agent"`
	Referer    string `json:"referer"`
	Label      string `json:"label"`
	Resolution string `json:"resolution,omitempty"`
	Bitrate    *int   `json:"bitrate,omitempty"`
}

func (h *PlayerHandler) ResolveStream(c *gin.Context) {
	id := c.Query("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing id"})
		return
	}

	entry, err := h.src().GetEntry(id)
	if err != nil {
		writeSourceError(c, err, "Entry not found or no stream URL")
		return
	}

	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}

	// Prefer the authoritative vod_streams rows; fall back silently to the
	// denormalized vod_assets.stream_url cache when the asset has none.
	//
	// Zero rows and a query error mean different things: zero rows is an
	// expected state (the asset genuinely has no vod_streams row), while an
	// error is a fault. Both still fall back so playback keeps working, but a
	// fault must be visible in the logs rather than silently serving the
	// stale stream_url cache.
	vodStreams, streamErr := h.src().GetVodStreams(entry.ID)
	if streamErr != nil {
		log.Printf("resolve: GetVodStreams(%s) failed, falling back to cached stream_url: %v", entry.ID, streamErr)
	}
	if streamErr == nil && len(vodStreams) > 0 {
		primary := vodStreams[0]

		// Per-stream headers: these CDNs reject requests with the wrong
		// Referer, so each stream carries its own user_agent/referer.
		ua := derefString(primary.UserAgent)
		ref := derefString(primary.Referer)
		proxyURL := h.buildProxyURL(scheme, c.Request.Host, primary.URL, primary.SourceType, ua, ref)

		alternates := make([]resolveAlternate, 0, len(vodStreams)-1)
		for _, s := range vodStreams[1:] {
			altUA := derefString(s.UserAgent)
			altRef := derefString(s.Referer)
			alternates = append(alternates, resolveAlternate{
				URL:        h.buildProxyURL(scheme, c.Request.Host, s.URL, s.SourceType, altUA, altRef),
				Provider:   s.SourceType,
				SourceType: s.SourceType,
				DrmType:    derefString(s.DrmType),
				DrmK:       derefString(s.DrmK),
				LicenseURL: derefString(s.LicenseURL),
				UserAgent:  altUA,
				Referer:    altRef,
				Label:      s.Label,
				Resolution: derefString(s.Resolution),
				Bitrate:    s.Bitrate,
			})
		}

		c.JSON(http.StatusOK, gin.H{
			"url": proxyURL, "provider": primary.SourceType,
			"drm_type": derefString(primary.DrmType), "drm_k": derefString(primary.DrmK),
			"license_url": derefString(primary.LicenseURL),
			"user_agent":  ua, "referer": ref,
			"alternates": alternates,
		})
		return
	}

	// Fallback: asset has no vod_streams rows (or the lookup failed) — play
	// the cached vod_assets.stream_url, exactly as before.
	if entry.StreamURL == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Entry not found or no stream URL"})
		return
	}

	ua := derefString(entry.UserAgent)
	ref := derefString(entry.Referer)
	proxyURL := h.buildProxyURL(scheme, c.Request.Host, *entry.StreamURL, entry.SourceType, ua, ref)

	c.JSON(http.StatusOK, gin.H{
		"url": proxyURL, "provider": entry.SourceType,
		"drm_type": derefString(entry.DrmType), "drm_k": derefString(entry.DrmK),
		"license_url": derefString(entry.LicenseURL),
		"user_agent":  ua, "referer": ref,
		"alternates": []resolveAlternate{},
	})
}

func (h *PlayerHandler) GetEpisodes(c *gin.Context) {
	vodID := c.Param("vodId")
	if vodID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing vodId"})
		return
	}

	episodes, err := h.src().GetEpisodes(vodID)
	if err != nil {
		writeSourceError(c, err, "No episodes found")
		return
	}

	// Collect unique seasons
	seasonMap := map[int]bool{}
	for _, ep := range episodes {
		seasonMap[ep.SeasonNumber] = true
	}
	seasons := make([]int, 0, len(seasonMap))
	for s := range seasonMap {
		seasons = append(seasons, s)
	}

	c.JSON(http.StatusOK, gin.H{
		"episodes": episodes,
		"seasons":  seasons,
	})
}

func (h *PlayerHandler) ResolveEpisode(c *gin.Context) {
	episodeID := c.Param("episodeId")
	if episodeID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing episodeId"})
		return
	}

	episode, err := h.src().GetEpisode(episodeID)
	if err != nil {
		writeSourceError(c, err, "Episode not found or no stream URL")
		return
	}
	if episode.StreamURL == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Episode not found or no stream URL", "code": codeNotFound})
		return
	}

	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}

	epUA := ""
	if episode.UserAgent != nil {
		epUA = *episode.UserAgent
	}
	epRef := ""
	if episode.Referer != nil {
		epRef = *episode.Referer
	}

	proxyURL := h.buildProxyURL(scheme, c.Request.Host, *episode.StreamURL, episode.SourceType, epUA, epRef)

	// Check for per-episode DRM
	drmType := ""
	if episode.DrmType != nil {
		drmType = *episode.DrmType
	}
	drmK := ""
	if episode.DrmK != nil {
		drmK = *episode.DrmK
	}
	licenseURL := ""
	if episode.LicenseURL != nil {
		licenseURL = *episode.LicenseURL
	}

	c.JSON(http.StatusOK, gin.H{
		"url":         proxyURL,
		"provider":    episode.SourceType,
		"drm_type":    drmType,
		"drm_k":       drmK,
		"license_url": licenseURL,
		"user_agent":  epUA,
		"referer":     epRef,
	})
}

func (h *PlayerHandler) ProxyStream(c *gin.Context) {
	targetURL := c.Query("url")
	source := c.DefaultQuery("source", "vidzee")
	ref := c.Query("ref")
	// The proxy speaks for the browser that asked for it. When the stream
	// configures no UA of its own, forward the caller's own User-Agent rather
	// than inventing one; a fabricated UA reads as malformed to origins that
	// filter on it (A2Z/ZTE JITP DRM 403s the old default). If the caller
	// carries none either, send none — never a made-up string.
	ua := c.Query("ua")
	if ua == "" {
		ua = c.GetHeader("User-Agent")
	}

	if targetURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing url"})
		return
	}

	// Handle CORS preflight
	if c.Request.Method == http.MethodOptions {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS, HEAD")
		c.Header("Access-Control-Allow-Headers", "*")
		c.Header("Access-Control-Expose-Headers", "Content-Type, Content-Length, Content-Range, Accept-Ranges")
		c.Status(http.StatusOK)
		return
	}

	// Build headers based on source. User-Agent is always set — an empty value
	// suppresses net/http's own default UA, so a caller with no UA sends none.
	// Referer is sent only when actually configured; the per-provider cases
	// below are deliberate requirements, not defaults.
	headers := map[string]string{
		"User-Agent": ua,
	}
	if ref != "" {
		headers["Referer"] = ref
	}
	switch source {
	case "vidstreaming":
		headers["Referer"] = "https://vidstreaming.io"
	case "vidcloud9":
		headers["Referer"] = "https://vidcloud9.com"
	case "alionscience":
		headers["Referer"] = "https://alionscience.com"
	case "miruro":
		headers["Referer"] = "https://www.miruro.tv"
	case "vixsrc":
		headers["Referer"] = "https://vixcloud.com"
	}

	// Credentials are attached server-side. They are looked up BEFORE the
	// userinfo is stripped, because a direct caller's own userinfo is the more
	// specific source; the outbound request then carries them as an
	// Authorization header instead of in the URL. See credentials.go.
	creds, haveCreds := h.creds.lookup(targetURL)
	targetURL = stripUserInfo(targetURL)

	req, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, targetURL, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create request"})
		return
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if haveCreds {
		req.SetBasicAuth(creds.user, creds.pass)
	}

	// Forward Range header for DASH segment seeks
	if rangeHeader := c.GetHeader("Range"); rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Failed to fetch"})
		return
	}
	defer resp.Body.Close()

	// Forward response headers to client
	forwardHeaders := []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"}
	for _, h := range forwardHeaders {
		if v := resp.Header.Get(h); v != "" {
			c.Header(h, v)
		}
	}

	// Set content-type based on extension — override CDN's generic types
	contentType := resp.Header.Get("Content-Type")
	if strings.HasSuffix(targetURL, ".m3u8") {
		contentType = "application/vnd.apple.mpegurl"
	} else if strings.HasSuffix(targetURL, ".mpd") {
		contentType = "application/dash+xml"
	} else if strings.HasSuffix(targetURL, ".m4s") {
		contentType = "video/iso.segment"
	} else if strings.HasSuffix(targetURL, ".mp4") {
		contentType = "video/mp4"
	} else if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Header("Content-Type", contentType)
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS, HEAD")
	c.Header("Access-Control-Expose-Headers", "Content-Type, Content-Length, Content-Range, Accept-Ranges")

	c.Status(resp.StatusCode)
	io.Copy(c.Writer, resp.Body)
}

func (h *PlayerHandler) ListChannels(c *gin.Context) {
	category := c.Query("category")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))

	channels, total, err := h.src().ListChannels(category, limit, (page-1)*limit)
	if err != nil {
		writeSourceError(c, err, "")
		return
	}

	c.JSON(http.StatusOK, gin.H{"channels": channels, "total": total, "page": page})
}

func (h *PlayerHandler) GetChannelCategories(c *gin.Context) {
	cats, err := h.src().ListChannelCategories()
	if err != nil {
		writeSourceError(c, err, "")
		return
	}
	c.JSON(http.StatusOK, gin.H{"categories": cats})
}

func (h *PlayerHandler) GetChannel(c *gin.Context) {
	id := c.Param("id")
	channel, streams, err := h.src().GetChannelWithStreams(id)
	if err != nil {
		writeSourceError(c, err, "Channel not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"channel": channel, "streams": streams})
}

func (h *PlayerHandler) ResolveChannelStream(c *gin.Context) {
	id := c.Param("id")
	stream, err := h.src().ResolveChannelStream(id)
	if err != nil {
		writeSourceError(c, err, "Stream not found")
		return
	}
	if stream == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Stream not found", "code": codeNotFound})
		return
	}

	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	proxyURL := h.buildProxyURL(scheme, c.Request.Host, stream.URL, stream.SourceType,
		derefString(stream.UserAgent), derefString(stream.Referer))

	drmType := ""
	if stream.DrmType != nil {
		drmType = *stream.DrmType
	}
	drmK := ""
	if stream.DrmK != nil {
		drmK = *stream.DrmK
	}
	licenseURL := ""
	if stream.LicenseURL != nil {
		licenseURL = *stream.LicenseURL
	}

	c.JSON(http.StatusOK, gin.H{
		"url":         proxyURL,
		"provider":    stream.SourceType,
		"drm_type":    drmType,
		"drm_k":       drmK,
		"license_url": licenseURL,
	})
}

func (h *PlayerHandler) GetChannelEPG(c *gin.Context) {
	// EPG is an optional source capability. A source that cannot supply a
	// compiled XMLTV blob leaves the service unset; report that plainly, naming
	// the active source, instead of dereferencing a nil service. The service is
	// loaded once so a concurrent swap cannot change it between the nil check
	// and the call.
	svc := h.epg.Load()
	if svc == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": source.UnsupportedEPGError(h.src().Name()).Error()})
		return
	}

	id := c.Param("id")
	channel, err := h.src().GetChannel(id)
	if err != nil {
		writeSourceError(c, err, "Channel not found")
		return
	}

	if channel.EpgChannelID == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no EPG channel id configured"})
		return
	}

	result := svc.GetForChannel(*channel.EpgChannelID)
	c.JSON(http.StatusOK, result)
}

// scanState is the per-handler health-scan lifecycle. A scan manager is a
// capability of ONE source adapter — it enumerates that adapter's streams — so
// it must follow whichever source is active, not be built once for the process.
// It is held on the handler rather than in a package global so two
// PlayerHandlers in the same process (the server and a test, say) cannot share
// a manager or leak scan state into each other.
//
// The manager is keyed by the source VALUE compared with ==. For every adapter
// that is pointer identity: reopening the same adapter name with a different
// playlist code is a different pointer, so it gets a fresh manager rather than
// reusing the previous one. (Every Source implementation is a pointer or the
// empty unconfigured struct, so == is always safe.) A source that cannot
// enumerate its streams has no manager; its unsupported error is memoised here
// instead — for as long as THAT source stays active, never for the process.
type scanState struct {
	mu     sync.Mutex
	built  bool
	source source.Source
	mgr    *scanner.Manager
	err    error
}

// managerFor returns the scan manager for active, building it the first time
// and rebuilding whenever active's identity changes. When the active source has
// changed, any manager built for the previous source is cancelled — an
// in-flight scan must stop probing an adapter that is no longer active — and
// discarded, so its (partial or complete) report can never be served as the new
// source's.
func (s *scanState) managerFor(active source.Source) (*scanner.Manager, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.built || s.source != active {
		s.resetLocked(active)
	}
	return s.mgr, s.err
}

// invalidate cancels and drops a manager built for a source other than current,
// so a swap stops an in-flight scan promptly instead of at the next scan
// request. A manager already built for current — including the "no scan was
// ever requested" state — is left untouched. Called from SwapSource after the
// new source is stored.
func (s *scanState) invalidate(current source.Source) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.built || s.source == current {
		return
	}
	if s.mgr != nil {
		s.mgr.Cancel()
	}
	s.built = false
	s.source = nil
	s.mgr = nil
	s.err = nil
}

// resetLocked cancels and drops any existing manager, then records active's
// capability: a manager when it can enumerate the library's streams, and the
// unsupported error when it cannot. Callers must hold s.mu.
func (s *scanState) resetLocked(active source.Source) {
	if s.mgr != nil {
		s.mgr.Cancel()
	}
	s.built = true
	s.source = active
	s.mgr = nil
	s.err = nil
	lister, ok := active.(source.StreamProbeLister)
	if !ok {
		s.err = source.UnsupportedScanError(scanSourceName(active))
		return
	}
	s.mgr = scanner.NewManager(lister)
}

// scanSourceName names a source for the unsupported error, tolerating a nil
// (sourceless) adapter.
func scanSourceName(src source.Source) string {
	if src == nil {
		return ""
	}
	return src.Name()
}

// scanManager returns the scan manager for the ACTIVE source. Health scanning
// is an OPTIONAL source capability: a source that cannot enumerate the whole
// library's streams (an HTTP adapter that only ever sees one user's playlist,
// say) yields a clear unsupported error. The handler never reaches around the
// adapter for a database handle — the capability, or its absence, is the
// whole boundary.
//
// The manager is rebuilt whenever the active source changes identity, and an
// error from a previous source is never cached past that source's tenure: a
// user who first hits a non-scannable source and then imports a local playlist
// gets a working scan, not a 501 for the rest of the process's life.
func (h *PlayerHandler) scanManager() (*scanner.Manager, error) {
	return h.scan.managerFor(h.src())
}

// CancelActiveScan cancels an in-flight scan, if one was ever started. The
// server calls it from its shutdown path so probes stop promptly instead of
// running out their per-probe deadlines and the five-minute scan budget.
//
// It deliberately does NOT construct the manager: when no scan was ever
// requested the manager is nil and this is a harmless no-op. Shutdown must
// never bring a manager — or the lister capability behind it — into existence.
func (h *PlayerHandler) CancelActiveScan() {
	h.scan.mu.Lock()
	defer h.scan.mu.Unlock()
	if h.scan.mgr != nil {
		h.scan.mgr.Cancel()
	}
}

// ScanChannels starts a background scan and returns immediately. A second
// POST while one is running does not start a second scan; it returns the
// in-progress status instead. Nothing is persisted upstream — the scan only
// probes streams and aggregates an in-memory report.
func (h *PlayerHandler) ScanChannels(c *gin.Context) {
	mgr, err := h.scanManager()
	if err != nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": err.Error()})
		return
	}
	status, started := mgr.Start()
	if !started {
		c.JSON(http.StatusConflict, gin.H{
			"status":  status,
			"message": "a scan is already in progress",
		})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{
		"status":  status,
		"message": "scan started",
	})
}

// ScanStatus reports scan progress/completion for polling.
func (h *PlayerHandler) ScanStatus(c *gin.Context) {
	mgr, err := h.scanManager()
	if err != nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": mgr.Status()})
}

// ScanReport serves the completed report as a downloadable file (JSON, or CSV
// with ?format=csv) so the user can hand it to the operator out of band. The
// report carries no resolvable stream URL — only the host and the final
// manifest segment — so a shared report cannot reveal an upstream CDN.
func (h *PlayerHandler) ScanReport(c *gin.Context) {
	mgr, err := h.scanManager()
	if err != nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": err.Error()})
		return
	}
	report, err := mgr.Report()
	switch {
	case errors.Is(err, scanner.ErrScanInProgress):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	case errors.Is(err, scanner.ErrNoReport):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	case err != nil:
		log.Printf("scan report: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load report"})
		return
	}

	if c.DefaultQuery("format", "json") == "csv" {
		var buf bytes.Buffer
		if err := scanner.WriteCSV(&buf, report); err != nil {
			log.Printf("scan report: render csv: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to render report"})
			return
		}
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", report.Filename(".csv")))
		c.Data(http.StatusOK, "text/csv; charset=utf-8", buf.Bytes())
		return
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		log.Printf("scan report: marshal json: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to render report"})
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", report.Filename(".json")))
	c.Data(http.StatusOK, "application/json", data)
}
