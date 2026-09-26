package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/jmvbambico/adoboflix/internal/db"
	"github.com/jmvbambico/adoboflix/internal/epg"
	"github.com/jmvbambico/adoboflix/internal/handler"
	"github.com/jmvbambico/adoboflix/internal/middleware"
)

func main() {
	// Defaults can be overridden by SERVER_HOST / SERVER_PORT env vars or CLI flags.
	defaultHost := "0.0.0.0"
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

	// Initialize database connection
	database, err := db.Connect()
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer database.Close()

	// Initialize EPG service (non-fatal if compiled_epg is empty)
	epgService := epg.NewService(database)

	// Setup Gin router
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	// Middleware
	r.Use(middleware.CORS())

	// Initialize handlers
	playerHandler := handler.NewPlayerHandler(database).WithEPG(epgService)

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

	if err := r.Run(addr); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
