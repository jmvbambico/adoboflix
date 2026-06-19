package handler

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jmvbambico/adoboflix/internal/db"
)

type PlayerHandler struct {
	db *db.DB
}

func NewPlayerHandler(database *db.DB) *PlayerHandler {
	return &PlayerHandler{db: database}
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
	for i, e := range entries {
		enriched[i].Entry = e
		if e.Type == "Series" {
			episodes, _, err := h.db.GetEpisodes(e.ID)
			if err == nil {
				count := len(episodes)
				enriched[i].EpisodeCount = &count
			}
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

func (h *PlayerHandler) ResolveStream(c *gin.Context) {
	id := c.Query("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing id"})
		return
	}

	entry, err := h.db.GetEntry(id)
	if err != nil || entry.StreamURL == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Entry not found or no stream URL"})
		return
	}

	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	fullURL := fmt.Sprintf("%s://%s/api/v1/proxy?url=%s&source=%s",
		scheme, c.Request.Host, url.QueryEscape(*entry.StreamURL), entry.SourceType)

	drmType := ""
	if entry.DrmType != nil {
		drmType = *entry.DrmType
	}
	drmK := ""
	if entry.DrmK != nil {
		drmK = *entry.DrmK
	}
	licenseURL := ""
	if entry.LicenseURL != nil {
		licenseURL = *entry.LicenseURL
	}

	c.JSON(http.StatusOK, gin.H{
		"url": fullURL, "provider": entry.SourceType,
		"drm_type": drmType, "drm_k": drmK, "license_url": licenseURL,
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
	fullURL := fmt.Sprintf("%s://%s/api/v1/proxy?url=%s&source=%s",
		scheme, c.Request.Host, url.QueryEscape(*episode.StreamURL), episode.SourceType)

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
		"url":         fullURL,
		"provider":    episode.SourceType,
		"drm_type":    drmType,
		"drm_k":       drmK,
		"license_url": licenseURL,
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
