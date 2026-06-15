package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/gin-gonic/gin"
	"github.com/jmvbambico/adoboflix/internal/db"
	"github.com/jmvbambico/adoboflix/internal/handler"
	"github.com/jmvbambico/adoboflix/internal/middleware"
)

func main() {
	var (
		host = flag.String("host", "127.0.0.1", "Bind address")
		port = flag.Int("port", 5656, "Port")
	)
	flag.Parse()

	// Initialize database connection
	database, err := db.Connect()
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer database.Close()

	// Setup Gin router
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	// Middleware
	r.Use(middleware.CORS())

	// Initialize handlers
	playerHandler := handler.NewPlayerHandler(database)

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
	}

	// Serve static files in production
	r.Static("/static", "./static")
	r.NoRoute(func(c *gin.Context) {
		c.File("./static/index.html")
	})

	addr := fmt.Sprintf("%s:%d", *host, *port)

	fmt.Printf("\n🎬 AdoboFlix: http://%s\n", addr)
	fmt.Printf("📚 Browse your media library, search, and stream with DRM support\n\n")

	if err := r.Run(addr); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}