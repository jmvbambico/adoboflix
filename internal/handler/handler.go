package handler

import (
	"fmt"
	"io"
	"net/http"
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
	c.JSON(http.StatusOK, gin.H{
		"entries": entries, "total": total,
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
		scheme, c.Request.Host, *entry.StreamURL, entry.SourceType)

	drmType := ""
	if entry.DrmType != nil {
		drmType = *entry.DrmType
	}
	drmK := ""
	if entry.DrmK != nil {
		drmK = *entry.DrmK
	}

	c.JSON(http.StatusOK, gin.H{
		"url": fullURL, "provider": entry.SourceType,
		"drm_type": drmType, "drm_k": drmK,
	})
}

func (h *PlayerHandler) ProxyStream(c *gin.Context) {
	targetURL := c.Query("url")
	source := c.DefaultQuery("source", "vidzee")
	ref := c.DefaultQuery("ref", "https://www.google.com")

	if targetURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing url"})
		return
	}

	// Build headers based on source
	headers := map[string]string{
		"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
		"Referer":    ref,
	}
	switch source {
	case "vidstreaming":
		headers["Referer"] = "https://vidstreaming.io"
	case "vidcloud9":
		headers["Referer"] = "https://vidcloud9.com"
	case "alionscience":
		headers["Referer"] = "https://alionscience.com"
	}

	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create request"})
		return
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Failed to fetch"})
		return
	}
	defer resp.Body.Close()

	// Set response headers
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		if strings.HasSuffix(targetURL, ".m3u8") || strings.HasSuffix(targetURL, ".ts") {
			contentType = "application/vnd.apple.mpegurl"
		} else {
			contentType = "video/mp4"
		}
	}
	c.Header("Content-Type", contentType)
	c.Header("Access-Control-Allow-Origin", "*")
	c.Status(resp.StatusCode)
	io.Copy(c.Writer, resp.Body)
}
