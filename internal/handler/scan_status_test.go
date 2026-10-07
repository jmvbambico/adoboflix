package handler

import (
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jmvbambico/adoboflix/internal/playlistcode"
	"github.com/jmvbambico/adoboflix/internal/playlistfile"
	"github.com/jmvbambico/adoboflix/internal/source"
	"github.com/jmvbambico/adoboflix/internal/source/adobotvhttp"
	"github.com/jmvbambico/adoboflix/internal/source/file"
	"github.com/jmvbambico/adoboflix/internal/sourcemode"
)

// health_scan_supported must report the ACTIVE source's capability and track a
// swap, so the client can gate the scan entry point without calling an endpoint
// and reading a 501.
func TestSourceStatusHealthScanSupportedFollowsTheActiveSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := aliveManifestServer(t)

	login := &stubNonProbeSource{name: adobotvhttp.Name}
	local := &stubProbeSource{
		name:    file.Name,
		targets: []source.ProbeTarget{probeTarget("x", srv.URL+"/x/live.m3u8")},
	}

	dir := t.TempDir()
	player := NewPlayerHandler(login)
	h := NewSourceHandler(SourceHandlerOptions{
		Player:    player,
		Config:    source.Config{},
		CodeStore: playlistcode.New(filepath.Join(dir, "playlist-code")),
		ModeStore: sourcemode.New(filepath.Join(dir, "source-mode")),
		FileStore: playlistfile.New(filepath.Join(dir, "playlist")),
	})

	body := statusBody(t, h)
	if got, ok := body["health_scan_supported"].(bool); !ok || got {
		t.Fatalf("health_scan_supported = %v for adobotv-http, want false", body["health_scan_supported"])
	}
	// The new field must not displace the existing contract.
	for _, key := range []string{
		"source", "active", "origin", "dev", "needs_playlist_code",
		"playlist_code_configured", "playlist_file_configured", "modes",
	} {
		if _, present := body[key]; !present {
			t.Errorf("status is missing %q after adding health_scan_supported: %v", key, body)
		}
	}

	player.SwapSource(local)
	body = statusBody(t, h)
	if got, ok := body["health_scan_supported"].(bool); !ok || !got {
		t.Fatalf("health_scan_supported = %v after swapping to the file source, want true", body["health_scan_supported"])
	}

	player.SwapSource(login)
	body = statusBody(t, h)
	if got, ok := body["health_scan_supported"].(bool); !ok || got {
		t.Fatalf("health_scan_supported = %v after swapping back, want false", body["health_scan_supported"])
	}
}
