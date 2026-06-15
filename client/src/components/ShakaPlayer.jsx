import React, { useRef, useEffect, useState } from "react";
import { Play, Pause, Maximize, Volume2 } from "lucide-react";

export default function ShakaPlayer({ src, drmType, drmK, poster }) {
  const videoRef = useRef(null);
  const containerRef = useRef(null);
  const [playing, setPlaying] = useState(false);
  const [error, setError] = useState(null);

  useEffect(() => {
    if (!videoRef.current || !src) return;

    let player = null;
    let unloaded = false;
    let shaka;

    async function initPlayer() {
      try {
        shaka = await import("shaka-player");
        if (unloaded) return;

        shaka.polyfill.installAll();

        if (!shaka.Player.isBrowserSupported()) {
          setError("Browser does not support Shaka Player");
          return;
        }

        player = new shaka.Player(videoRef.current);
        
        // Configure DRM
        const config = {};
        if (drmType === "widevine" && drmK) {
          // Widevine: drmK is the license URL
          config.drm = {
            servers: { "com.widevine.alpha": drmK }
          };
        } else if (drmType === "clearkey" && drmK) {
          // Clearkey: drmK is "kid:key" format
          const [kid, key] = drmK.split(":");
          if (kid && key) {
            config.drm = {
              clearKeys: { [kid.replace(/-/g, "")]: key.replace(/-/g, "") }
            };
          }
        }

        if (Object.keys(config).length > 0) {
          player.configure(config);
        }

        player.addEventListener("error", (e) => {
          setError(`Shaka Error: ${e.detail.message}`);
        });

        await player.load(src);
        if (!unloaded) {
          setPlaying(true);
          videoRef.current.play();
        }
      } catch (err) {
        if (!unloaded) {
          setError(`Failed to load: ${err.message}`);
        }
      }
    }

    initPlayer();

    return () => {
      unloaded = true;
      if (player) {
        player.destroy();
      }
    };
  }, [src, drmType, drmK]);

  const togglePlay = () => {
    if (!videoRef.current) return;
    if (playing) {
      videoRef.current.pause();
      setPlaying(false);
    } else {
      videoRef.current.play();
      setPlaying(true);
    }
  };

  if (error) {
    return (
      <div
        className="flex items-center justify-center rounded-lg"
        style={{ background: "var(--bg-2)", border: "1px solid var(--red-dim)", minHeight: 200 }}
      >
        <div className="text-center p-4">
          <div className="text-sm mb-2" style={{ color: "var(--red)" }}>{error}</div>
          <button
            className="filter-chip active"
            onClick={() => setError(null)}
          >
            Retry
          </button>
        </div>
      </div>
    );
  }

  return (
    <div
      ref={containerRef}
      className="relative rounded-lg overflow-hidden cursor-pointer group"
      style={{ background: "var(--bg-0)", border: "1px solid var(--b1)" }}
    >
      <video
        ref={videoRef}
        className="w-full h-full object-contain"
        style={{ aspectRatio: "16/9" }}
        onClick={togglePlay}
        poster={poster}
        controls
      />

      {/* Custom overlay when controls are hidden */}
      <div
        className="absolute bottom-0 left-0 right-0 p-4 flex items-center gap-3 opacity-0 group-hover:opacity-100 transition-opacity"
        style={{
          background: "linear-gradient(transparent, rgba(0,0,0,0.8))",
        }}
      >
        <button
          className="control-btn play"
          onClick={togglePlay}
        >
          {playing ? <Pause size={14} /> : <Play size={14} />}
        </button>
      </div>

      {/* Loading indicator */}
      {!playing && !error && (
        <div
          className="absolute inset-0 flex items-center justify-center"
          style={{ background: "rgba(0,0,0,0.3)" }}
        >
          <div
            className="w-12 h-12 rounded-full flex items-center justify-center"
            style={{ background: "rgba(245,158,11,0.9)" }}
          >
            <Play size={20} style={{ color: "var(--bg-0)" }} />
          </div>
        </div>
      )}
    </div>
  );
}
