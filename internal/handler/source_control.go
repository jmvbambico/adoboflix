package handler

import (
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jmvbambico/adoboflix/internal/playlistcode"
	"github.com/jmvbambico/adoboflix/internal/source"
)

// SourceHandler serves the playlist-code entry endpoints. They let a subscriber
// supply the one credential AdoboFlix needs while the server runs, instead of
// editing .env and restarting.
//
// # The credential is write-only from the API's point of view
//
// The endpoints accept a code and persist it to a local file the server owns.
// They never return it — not in a response body, not masked, not in an error.
// status exposes only booleans. The swap is done by reopening the adapter with
// the new code, so no request ever carries the credential after entry.
//
// # Security posture
//
// These endpoints are unauthenticated, like the rest of /api/v1. That is
// consistent with the current posture — the server binds loopback by default
// and /api/v1/proxy is unauthenticated too — but accepting a credential and
// writing it to disk is a step up in consequence. See "Stream URL exposure" in
// docs/source-adapters.md: if AdoboFlix ever becomes multi-user or
// internet-exposed, these endpoints need authentication before anything else.
type SourceHandler struct {
	player *PlayerHandler
	store  *playlistcode.Store
	// cfg is the source configuration the running process selected: the name
	// and, for a database-backed adapter, the open handle. The playlist code is
	// resolved fresh on every call, never cached here.
	cfg source.Config
	// envCode is the environment fallback (ADOBOFLIX_ADOBOTV_PLAYLIST_CODE). It
	// is consulted only when no code has been stored.
	envCode string
	// open reopens an adapter from a configuration. It is a field so tests can
	// exercise the endpoints without a live AdoboTV.
	open func(source.Config) (source.Source, error)
}

// NewSourceHandler wires the endpoints to the live player (whose source is
// swapped), the code store, and the selected source configuration. envCode is
// the environment fallback; pass "" when there is none.
func NewSourceHandler(player *PlayerHandler, store *playlistcode.Store, cfg source.Config, envCode string) *SourceHandler {
	return &SourceHandler{
		player:  player,
		store:   store,
		cfg:     cfg,
		envCode: envCode,
		open:    source.Open,
	}
}

// GetStatus reports the active source, whether it uses a playlist code, and
// whether one is currently configured. It never returns the code itself. The
// client uses needs_playlist_code to decide whether to offer the entry form and
// playlist_code_configured to know whether the source can serve content yet.
func (h *SourceHandler) GetStatus(c *gin.Context) {
	needs, configured := h.state()
	c.JSON(http.StatusOK, gin.H{
		"source":                   h.cfg.Name,
		"needs_playlist_code":      needs,
		"playlist_code_configured": configured,
	})
}

// SetPlaylistCode accepts a playlist code, validates it against AdoboTV with a
// cheap real call before persisting, then persists it and swaps the live
// source. On any validation failure it persists nothing and swaps nothing, and
// returns the same stable gate code the rest of the API uses. The code is never
// echoed in the response or written to the log.
func (h *SourceHandler) SetPlaylistCode(c *gin.Context) {
	var body struct {
		Code string `json:"code"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected a JSON body with a code field"})
		return
	}
	code := strings.TrimSpace(body.Code)
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing code"})
		return
	}

	if !source.NeedsPlaylistCode(h.cfg.Name) {
		c.JSON(http.StatusConflict, gin.H{
			"error": "the active source does not use a playlist code",
			"code":  codePlaylistCodeNotSupported,
		})
		return
	}

	// Validate before persisting or swapping: a wrong code must fail here, not
	// on the user's first playback attempt. Nothing is written until it passes.
	candidate, err := h.open(h.configWith(code))
	if err != nil {
		log.Printf("source playlist code: opening %q with the entered code: %v", h.cfg.Name, err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "could not open the source with that code",
			"code":  codeInternalError,
		})
		return
	}
	if err := validatePlaylistCode(candidate); err != nil {
		writeSourceError(c, err, "")
		return
	}

	if err := h.store.Save(code); err != nil {
		log.Printf("source playlist code: persisting: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "could not save the playlist code",
			"code":  codeInternalError,
		})
		return
	}
	h.player.SwapSource(candidate)
	// Log the length only: the code itself must never reach the log.
	log.Printf("[source] playlist code entered for %q; source reopened (%d characters)", h.cfg.Name, len(code))

	c.JSON(http.StatusOK, gin.H{
		"source":                   h.cfg.Name,
		"needs_playlist_code":      true,
		"playlist_code_configured": true,
	})
}

// DeletePlaylistCode clears the stored code and reopens the source from what
// remains: the environment fallback if one is set, otherwise the unconfigured
// state. Clearing an already-absent code is not an error.
func (h *SourceHandler) DeletePlaylistCode(c *gin.Context) {
	if err := h.store.Clear(); err != nil {
		log.Printf("source playlist code: clearing: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "could not clear the playlist code",
			"code":  codeInternalError,
		})
		return
	}

	needs := source.NeedsPlaylistCode(h.cfg.Name)
	if needs {
		// The stored code is gone; reopen with whatever remains, so the running
		// source matches the precedence rule the store enforces.
		code, _, err := h.store.Resolve(h.envCode)
		if err != nil {
			log.Printf("source playlist code: resolving after clear: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "could not reopen the source after clearing",
				"code":  codeInternalError,
			})
			return
		}
		reopened, err := h.open(h.configWith(code))
		if err != nil {
			log.Printf("source playlist code: reopening after clear: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "could not reopen the source after clearing",
				"code":  codeInternalError,
			})
			return
		}
		h.player.SwapSource(reopened)
	}

	_, configured := h.state()
	c.JSON(http.StatusOK, gin.H{
		"source":                   h.cfg.Name,
		"needs_playlist_code":      needs,
		"playlist_code_configured": configured,
	})
}

// state reports whether the active source uses a playlist code and whether one
// is currently configured. Precedence lives in the store; this only asks it.
// A malformed stored file is reported as "not configured" rather than an error:
// the status endpoint must always answer, and an unreadable credential is
// exactly the state the user needs to re-enter one from.
func (h *SourceHandler) state() (needs bool, configured bool) {
	needs = source.NeedsPlaylistCode(h.cfg.Name)
	if !needs {
		return false, false
	}
	_, ok, err := h.store.Resolve(h.envCode)
	if err != nil {
		log.Printf("source status: reading stored playlist code: %v", err)
		return true, false
	}
	return true, ok
}

// configWith returns the base configuration with an explicit playlist code.
// The code is never held on the handler: it is supplied per open and lives only
// inside the adapter being built.
func (h *SourceHandler) configWith(code string) source.Config {
	cfg := h.cfg
	cfg.PlaylistCode = code
	return cfg
}

// validatePlaylistCode exercises a candidate adapter with one cheap real read,
// so a rejected code fails here rather than on first playback. ListChannels is
// the cheapest Source call that forces adobotv-http to fetch the playlist
// envelope: exactly one upstream request, against the endpoint that enforces
// the code, device and User-Agent gates. Its error is returned unchanged, so
// the caller maps it to the same stable code the rest of the API uses.
func validatePlaylistCode(candidate source.Source) error {
	_, _, err := candidate.ListChannels("", 1, 0)
	return err
}
