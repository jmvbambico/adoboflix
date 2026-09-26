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

	"github.com/gin-gonic/gin"
	"github.com/jmvbambico/adoboflix/internal/db"
	"github.com/jmvbambico/adoboflix/internal/epg"
	"github.com/jmvbambico/adoboflix/internal/scanner"
	"github.com/lib/pq"
)

type PlayerHandler struct {
	db  *db.DB
	epg *epg.Service
}

func NewPlayerHandler(database *db.DB) *PlayerHandler {
	return &PlayerHandler{db: database}
}

func (h *PlayerHandler) WithEPG(service *epg.Service) *PlayerHandler {
	h.epg = service
	return h
}

func (h *PlayerHandler) GetStats(c *gin.Context) {
	stats, err := h.db.GetStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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

	entries, total, err := h.db.GetEntries(provider, genre, contentType, page, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Attach episode counts for Series entries
	type enrichedEntry struct {
		db.Entry
		EpisodeCount *int `json:"episode_count,omitempty"`
	}
	enriched := make([]enrichedEntry, len(entries))

	// Collect the Series IDs on this page and resolve every episode count in a
	// single aggregate query instead of one GetEpisodes call per entry.
	seriesIDs := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Type == "Series" {
			seriesIDs = append(seriesIDs, e.ID)
		}
	}

	episodeCounts := map[string]int{}
	countsAvailable := false
	if len(seriesIDs) > 0 {
		type countRow struct {
			VodID string `db:"vod_id"`
			Count int    `db:"episode_count"`
		}
		var rows []countRow
		query := `SELECT vod_id, COUNT(*) AS episode_count FROM episodes WHERE vod_id = ANY($1) GROUP BY vod_id`
		if err := h.db.Select(&rows, query, pq.Array(seriesIDs)); err != nil {
			// Do not report a misleading 0 for every Series: leave the field
			// absent, exactly as when the count lookup was unavailable.
			log.Printf("entries: episode count query failed for %d series: %v", len(seriesIDs), err)
		} else {
			countsAvailable = true
			for _, r := range rows {
				episodeCounts[r.VodID] = r.Count
			}
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
	entry, err := h.db.GetEntry(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Entry not found"})
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

	entries, total, err := h.db.Search(q, provider, genre, contentType, page, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"results": entries, "total": total,
		"page": page, "has_more": (page * limit) < total,
	})
}

func (h *PlayerHandler) GetProviders(c *gin.Context) {
	providers, err := h.db.GetProviders()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"providers": providers})
}

func (h *PlayerHandler) GetGenres(c *gin.Context) {
	genres, err := h.db.GetGenres()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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

// buildProxyURL wraps a raw stream URL through /api/v1/proxy exactly the way
// resolve has always done: QueryEscape the url, append &source=, then optional
// &ua= / &ref= when the stream carries its own headers.
func buildProxyURL(scheme, host, rawURL, sourceType, ua, ref string) string {
	proxyURL := fmt.Sprintf("%s://%s/api/v1/proxy?url=%s&source=%s",
		scheme, host, url.QueryEscape(rawURL), sourceType)
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

	entry, err := h.db.GetEntry(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Entry not found or no stream URL"})
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
	vodStreams, streamErr := h.db.GetVodStreams(entry.ID)
	if streamErr != nil {
		log.Printf("resolve: GetVodStreams(%s) failed, falling back to cached stream_url: %v", entry.ID, streamErr)
	}
	if streamErr == nil && len(vodStreams) > 0 {
		primary := vodStreams[0]

		// Per-stream headers: these CDNs reject requests with the wrong
		// Referer, so each stream carries its own user_agent/referer.
		ua := derefString(primary.UserAgent)
		ref := derefString(primary.Referer)
		proxyURL := buildProxyURL(scheme, c.Request.Host, primary.URL, primary.SourceType, ua, ref)

		alternates := make([]resolveAlternate, 0, len(vodStreams)-1)
		for _, s := range vodStreams[1:] {
			altUA := derefString(s.UserAgent)
			altRef := derefString(s.Referer)
			alternates = append(alternates, resolveAlternate{
				URL:        buildProxyURL(scheme, c.Request.Host, s.URL, s.SourceType, altUA, altRef),
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
			"user_agent": ua, "referer": ref,
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
	proxyURL := buildProxyURL(scheme, c.Request.Host, *entry.StreamURL, entry.SourceType, ua, ref)

	c.JSON(http.StatusOK, gin.H{
		"url": proxyURL, "provider": entry.SourceType,
		"drm_type": derefString(entry.DrmType), "drm_k": derefString(entry.DrmK),
		"license_url": derefString(entry.LicenseURL),
		"user_agent": ua, "referer": ref,
		"alternates": []resolveAlternate{},
	})
}

func (h *PlayerHandler) GetEpisodes(c *gin.Context) {
	vodID := c.Param("vodId")
	if vodID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing vodId"})
		return
	}

	episodes, _, err := h.db.GetEpisodes(vodID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "No episodes found"})
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

	episode, err := h.db.GetEpisode(episodeID)
	if err != nil || episode.StreamURL == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Episode not found or no stream URL"})
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

	proxyURL := fmt.Sprintf("%s://%s/api/v1/proxy?url=%s&source=%s",
		scheme, c.Request.Host, url.QueryEscape(*episode.StreamURL), episode.SourceType)
	if epUA != "" {
		proxyURL += "&ua=" + url.QueryEscape(epUA)
	}
	if epRef != "" {
		proxyURL += "&ref=" + url.QueryEscape(epRef)
	}

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
	ref := c.DefaultQuery("ref", "https://www.google.com")
	ua := c.DefaultQuery("ua", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

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

	// Build headers based on source
	headers := map[string]string{
		"User-Agent": ua,
		"Referer":    ref,
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

	req, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, targetURL, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create request"})
		return
	}
	for k, v := range headers {
		req.Header.Set(k, v)
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

	channels, total, err := h.db.ListChannels(category, limit, (page-1)*limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"channels": channels, "total": total, "page": page})
}

func (h *PlayerHandler) GetChannelCategories(c *gin.Context) {
	cats, err := h.db.ListChannelCategories()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"categories": cats})
}

func (h *PlayerHandler) GetChannel(c *gin.Context) {
	id := c.Param("id")
	channel, streams, err := h.db.GetChannelWithStreams(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Channel not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"channel": channel, "streams": streams})
}

func (h *PlayerHandler) ResolveChannelStream(c *gin.Context) {
	id := c.Param("id")
	stream, err := h.db.ResolveChannelStream(id)
	if err != nil || stream == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Stream not found"})
		return
	}

	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	proxyURL := fmt.Sprintf("%s://%s/api/v1/proxy?url=%s&source=%s",
		scheme, c.Request.Host, url.QueryEscape(stream.URL), stream.SourceType)

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
	id := c.Param("id")
	channel, err := h.db.GetChannel(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Channel not found"})
		return
	}

	if channel.EpgChannelID == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no EPG channel id configured"})
		return
	}

	result := h.epg.GetForChannel(*channel.EpgChannelID)
	c.JSON(http.StatusOK, result)
}

// Scan state is held in a package-level manager so the scan handlers stay
// self-contained (they are the only handler code this feature owns).
var (
	scanMgrOnce sync.Once
	scanMgr     *scanner.Manager
)

func scanManager(database *db.DB) *scanner.Manager {
	scanMgrOnce.Do(func() {
		scanMgr = scanner.NewManager(database.DB)
	})
	return scanMgr
}

// ScanChannels starts a background scan and returns immediately. A second
// POST while one is running does not start a second scan; it returns the
// in-progress status instead. Nothing is persisted upstream — the scan only
// probes streams and aggregates an in-memory report.
func (h *PlayerHandler) ScanChannels(c *gin.Context) {
	status, started := scanManager(h.db).Start()
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
	c.JSON(http.StatusOK, gin.H{"status": scanManager(h.db).Status()})
}

// ScanReport serves the completed report as a downloadable file (JSON, or CSV
// with ?format=csv) so the user can hand it to the operator out of band.
func (h *PlayerHandler) ScanReport(c *gin.Context) {
	report, err := scanManager(h.db).Report()
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
