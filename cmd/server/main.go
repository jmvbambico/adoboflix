package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmvbambico/adoboflix/internal/db"
	"github.com/jmvbambico/adoboflix/internal/epg"
	"github.com/jmvbambico/adoboflix/internal/handler"
	"github.com/jmvbambico/adoboflix/internal/middleware"
	"github.com/jmvbambico/adoboflix/internal/playlistcode"
	"github.com/jmvbambico/adoboflix/internal/playlistfile"
	"github.com/jmvbambico/adoboflix/internal/source"
	"github.com/jmvbambico/adoboflix/internal/source/adobotvhttp"
	"github.com/jmvbambico/adoboflix/internal/source/file"
	"github.com/jmvbambico/adoboflix/internal/sourcemode"
	"github.com/joho/godotenv"

	// Adapters register themselves with internal/source from their init. file is
	// imported by name because main reads its environment key; postgres-direct
	// registers itself and nothing here references it.
	_ "github.com/jmvbambico/adoboflix/internal/source/postgresdirect"
)

func main() {
	// Defaults can be overridden by SERVER_HOST / SERVER_PORT env vars or CLI flags.
	//
	// The bind address defaults to loopback, NOT 0.0.0.0. /api/v1/proxy is an
	// unauthenticated fetcher for arbitrary URLs and /api/v1/resolve exposes
	// the upstream CDN URL in its response, so a default that listens on every
	// interface hands both to anything on the LAN. Exposing AdoboFlix beyond
	// this machine is a deliberate act: set SERVER_HOST=0.0.0.0 or -host.
	defaultHost := "127.0.0.1"
	defaultPort := 5656
	if v := os.Getenv("SERVER_HOST"); v != "" {
		defaultHost = v
	}
	if v := os.Getenv("SERVER_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			defaultPort = n
		}
	}

	var (
		host = flag.String("host", defaultHost, "Bind address")
		port = flag.Int("port", defaultPort, "Port")
	)
	flag.Parse()

	// Load .env if present (non-fatal if missing)
	if err := godotenv.Load(); err != nil {
		log.Printf("[env] no .env file found, using environment variables")
	}

	// Choose the content source before touching the database.
	//
	// ADOBOFLIX_SOURCE is the override: when it is set it pins the source and
	// wins, and it is the only way to reach postgres-direct. When it is unset
	// the server uses the mode the user chose in the UI (remembered locally,
	// the same way the playlist code is). With neither, the server boots with
	// no source at all — which is the opposite of a silent fallback: it offers
	// the user the two real paths. Since env is now the only route to
	// postgres-direct, "behind explicit configuration, never the default" holds
	// harder than before, not weaker.
	envSource := os.Getenv(source.EnvSource)
	modeStore := sourcemode.New(sourcemode.DefaultPath())
	resolution, err := modeStore.Resolve(envSource)
	if err != nil {
		log.Fatalf("Source configuration: %v", err)
	}
	sourceName := resolution.Mode

	// The playlist code a subscriber enters in the UI persists to a local file
	// the server owns. A stored code wins over ADOBOFLIX_ADOBOTV_PLAYLIST_CODE;
	// the environment variable is the fallback used only when nothing has been
	// stored. A UI-entered code is the user's most recent explicit instruction,
	// and a stale .env silently overriding it would make the UI look broken.
	// docs/source-adapters.md states this.
	codeStore := playlistcode.New(playlistcode.DefaultPath())

	// An imported playlist persists to a local file the server owns, so the
	// file adapter can be pointed at something the user pasted rather than a
	// path they had to arrange themselves.
	fileStore := playlistfile.New(playlistfile.DefaultPath())

	envPlaylistCode := os.Getenv(adobotvhttp.EnvPlaylistCode)
	envFilePath := os.Getenv(file.EnvPath)

	// Open a database connection only when the selected adapter declared it
	// needs one. adobotv-http and file read no SQL handle at all, so a user
	// with no AdoboTV database can run them; only postgres-direct reads the
	// schema directly. The requirement comes from the adapter's own
	// source.Register declaration, not from a name check here.
	cfg := source.Config{Name: sourceName}
	if source.NeedsDatabase(sourceName) {
		database, err := db.Connect()
		if err != nil {
			log.Fatalf("Source %q requires a database, but connecting failed: %v", sourceName, err)
		}
		defer database.Close()
		cfg.DB = database.DB
	}

	// Resolve the playlist code for a source that needs one. A malformed code
	// file is fatal: a truncated credential must not be silently ignored. With
	// neither a stored nor an environment code the source opens unconfigured,
	// and the user supplies one through /api/v1/source/playlist-code — that is
	// the point of the feature, so it is a valid state, not an error.
	if source.NeedsPlaylistCode(sourceName) {
		code, configured, err := codeStore.Resolve(envPlaylistCode)
		if err != nil {
			log.Fatalf("Playlist code store: %v", err)
		}
		cfg.PlaylistCode = code
		if !configured {
			log.Printf("[source] %q has no playlist code yet; enter one at POST /api/v1/source/playlist-code", sourceName)
		}
	}

	// Point the file adapter at the imported playlist when one is stored; with
	// none, the adapter's own factory falls back to ADOBOFLIX_FILE_PATH.
	if source.NeedsPlaylistFile(sourceName) {
		if p := fileStore.Path(); p != "" {
			cfg.FilePath = p
		}
	}

	// Open the selected source adapter. Handlers only ever see this interface.
	// With no mode and no override, open the unconfigured source instead of a
	// real one: every content route answers with a clear source_not_configured
	// until the user chooses, and the player never holds a nil.
	var playerSource source.Source
	if sourceName == "" {
		playerSource = source.Unconfigured()
		log.Printf("[source] no source configured; choose one at POST /api/v1/source/playlist-code or POST /api/v1/source/playlist-file")
	} else {
		playerSource, err = source.Open(cfg)
		if err != nil {
			log.Fatalf("Failed to open source %q: %v", sourceName, err)
		}
		if resolution.Origin == sourcemode.OriginEnv {
			log.Printf("[source] using %q (pinned by %s)", sourceName, source.EnvSource)
		} else {
			log.Printf("[source] using %q (chosen in the UI)", sourceName)
		}
	}

	// Setup Gin router
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	// Middleware
	r.Use(middleware.CORS())

	// Initialize handlers
	playerHandler := handler.NewPlayerHandler(playerSource)

	// EPG is an OPTIONAL source capability. Only a source that can supply the
	// compiled XMLTV blob gets an EPG service; for any other source h.epg stays
	// nil and the EPG endpoint reports it as unavailable rather than panicking.
	// Loading is non-fatal if compiled_epg is empty, so a fresh install boots.
	if provider, ok := playerSource.(source.CompiledEPGProvider); ok {
		playerHandler = playerHandler.WithEPG(epg.NewService(provider))
	} else {
		log.Printf("[EPG] source %q does not provide EPG data; the EPG endpoint will report it as unavailable", sourceName)
	}

	// The source endpoints share the player's source so a swap takes effect for
	// every content route at once. The handler resolves the credential and the
	// playlist path itself on each call, so it is handed the configuration with
	// no code and no path in it rather than a copy of either.
	sourceCfg := cfg
	sourceCfg.Name = ""
	sourceCfg.PlaylistCode = ""
	sourceCfg.FilePath = ""
	sourceHandler := handler.NewSourceHandler(handler.SourceHandlerOptions{
		Player:          playerHandler,
		Config:          sourceCfg,
		CodeStore:       codeStore,
		ModeStore:       modeStore,
		FileStore:       fileStore,
		EnvPlaylistCode: envPlaylistCode,
		EnvFilePath:     envFilePath,
		EnvSource:       envSource,
	})

	// API routes
	api := r.Group("/api/v1")
	{
		api.GET("/stats", playerHandler.GetStats)
		api.GET("/entries", playerHandler.GetEntries)
		api.GET("/entry/:id", playerHandler.GetEntry)
		api.GET("/search", playerHandler.Search)
		api.GET("/providers", playerHandler.GetProviders)
		api.GET("/genres", playerHandler.GetGenres)
		api.GET("/resolve", playerHandler.ResolveStream)
		api.GET("/proxy", playerHandler.ProxyStream)
		api.GET("/episodes/:vodId", playerHandler.GetEpisodes)
		api.GET("/resolve/episode/:episodeId", playerHandler.ResolveEpisode)

		// Channel routes
		api.GET("/channels", playerHandler.ListChannels)
		api.GET("/channels/categories", playerHandler.GetChannelCategories)
		api.GET("/channels/:id", playerHandler.GetChannel)
		api.GET("/channels/:id/resolve", playerHandler.ResolveChannelStream)
		api.GET("/channels/:id/epg", playerHandler.GetChannelEPG)
		api.POST("/channels/scan", playerHandler.ScanChannels)
		api.GET("/channels/scan/status", playerHandler.ScanStatus)
		api.GET("/channels/scan/report", playerHandler.ScanReport)

		// Source routes. These select one of the two user paths: entering a
		// playlist code, or importing a playlist. They accept a credential and
		// write it, and an imported playlist, to local files the server owns;
		// they never return the credential. Unauthenticated, like the rest of
		// /api/v1 — see "Stream URL exposure" in docs/source-adapters.md before
		// exposing this server beyond loopback.
		api.GET("/source/status", sourceHandler.GetStatus)
		api.POST("/source/playlist-code", sourceHandler.SetPlaylistCode)
		api.DELETE("/source/playlist-code", sourceHandler.DeletePlaylistCode)
		api.POST("/source/playlist-file", sourceHandler.SetPlaylistFile)
		api.DELETE("/source/playlist-file", sourceHandler.DeletePlaylistFile)
	}

	// Serve built client assets.
	// Static files (JS, CSS, images, etc.) are served directly from ./static.
	// Any path not matched by a route or static file falls back to index.html (SPA).
	r.Static("/assets", "./static/assets")
	r.StaticFile("/", "./static/index.html")
	r.NoRoute(func(c *gin.Context) {
		// Serve real static files (e.g. /logo.webp, /favicon.ico) if they exist.
		// Fall back to index.html for SPA client-side routes.
		path := "./static" + c.Request.URL.Path
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			c.File(path)
			return
		}
		c.File("./static/index.html")
	})

	addr := fmt.Sprintf("%s:%d", *host, *port)

	fmt.Printf("\n🎬 AdoboFlix: http://%s\n", addr)
	fmt.Printf("📺 IPTV Channels, VOD with DRM support, and EPG\n\n")

	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	// Shut down gracefully on SIGINT/SIGTERM. A scan is not an HTTP handler, so
	// http.Server.Shutdown alone would leave an in-flight scan's probes running
	// out their per-probe deadlines and the five-minute budget before the
	// process could exit; the scan is cancelled explicitly below.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("[shutdown] signal received, stopping")

	// Cancel an in-flight scan before waiting on HTTP shutdown. This is a no-op
	// when no scan was ever requested — it never constructs the scan manager.
	handler.CancelActiveScan()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("[shutdown] http server: %v", err)
	}
}
