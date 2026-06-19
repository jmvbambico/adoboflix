import React, { useRef, useState, useEffect } from "react";
import {
  Play, Pause, RotateCcw, RotateCw, Volume2, VolumeX, Maximize2, Minimize2,
  Settings, Zap, CircleCheck, Tv, RefreshCw, Volume1,
  SkipBack, SkipForward
} from "lucide-react";
import { motion, AnimatePresence } from "motion/react";
// @ts-ignore
import shaka from 'shaka-player/dist/shaka-player.ui.js';

interface CustomPlayerProps {
  id: string;
  videoUrl: string;
  title: string;
  thumbnailUrl: string;
  durationSeconds: number;
  onProgress?: (progress: number, currentTime: number) => void;
  onEnded?: () => void;
  savedTime?: number;
  isLive?: boolean;
  type?: string;
  year?: number;
  tags?: string[];
  description?: string;
  episodeCount?: number;
  drmType?: string;
  drmK?: string;
  licenseUrl?: string;
  userAgent?: string;
  referer?: string;
  hasPrevEpisode?: boolean;
  hasNextEpisode?: boolean;
  onPrevEpisode?: () => void;
  onNextEpisode?: () => void;
}

const DEFAULT_UA = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36';

export default function CustomPlayer({
  id,
  videoUrl,
  title,
  thumbnailUrl,
  durationSeconds,
  onProgress,
  onEnded,
  savedTime = 0,
  isLive = false,
  type,
  year,
  tags,
  description,
  episodeCount,
  drmType,
  drmK,
  licenseUrl,
  userAgent,
  referer,
  hasPrevEpisode,
  hasNextEpisode,
  onPrevEpisode,
  onNextEpisode
}: CustomPlayerProps) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const shakaRef = useRef<any>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const progressContainerRef = useRef<HTMLDivElement>(null);
  const hideControlsTimeoutRef = useRef<number | null>(null);

  // States
  const [isPlaying, setIsPlaying] = useState(false);
  const [currentTime, setCurrentTime] = useState(0);
  const [duration, setDuration] = useState(durationSeconds || 0);
  const [buffered, setBuffered] = useState(0);
  const [volume, setVolume] = useState(0.85);
  const [prevVolume, setPrevVolume] = useState(0.85);
  const [isMuted, setIsMuted] = useState(false);
  const [playbackSpeed, setPlaybackSpeed] = useState(1);
  const [isFullscreen, setIsFullscreen] = useState(false);
  const [isTheater, setIsTheater] = useState(false);
  const [showControls, setShowControls] = useState(true);
  const [showSpeedMenu, setShowSpeedMenu] = useState(false);
  const [bubbleAction, setBubbleAction] = useState<"play" | "pause" | "forward" | "rewind" | null>(null);
  const [errorLoading, setErrorLoading] = useState(false);
  const FALLBACK_POSTER = "https://images.unsplash.com/photo-1489599849927-2ee91cede3ba?w=800&auto=format&fit=crop&q=80";
  const [posterSrc, setPosterSrc] = useState(thumbnailUrl || FALLBACK_POSTER);

  // Detect whether we need Shaka adaptive streaming engine
  const isDRM = drmType === "Clearkey" || drmType === "Widevine";
  const needsShaka = Boolean(videoUrl?.includes(".m3u8") || videoUrl?.includes(".mpd") || isDRM);

  // ── Shaka adaptive streaming engine (HLS, DASH, DRM) ──────────────
  useEffect(() => {
    if (!needsShaka || !videoUrl || !videoRef.current) return;

    let player: any = null;
    let isMounted = true;

    const BACKEND_HOST = window.location.hostname;
    const BACKEND_PORT = '5656';
    const BACKEND_BASE = `http://${BACKEND_HOST}:${BACKEND_PORT}`;

    // Extract the original CDN URL and source type from the proxy URL.
    // We pass the ORIGINAL CDN URL to Shaka so that relative segment
    // paths in manifests resolve correctly. The networking filter then
    // wraps ALL requests (manifest + segments) through the backend proxy.
    let manifestUrl = videoUrl;
    let manifestBase = videoUrl.substring(0, videoUrl.lastIndexOf('/') + 1);
    let sourceType = '';
    try {
      const urlObj = new URL(videoUrl);
      sourceType = urlObj.searchParams.get('source') || '';
      if (urlObj.pathname.includes('/proxy') && urlObj.searchParams.has('url')) {
        const rawUrl = urlObj.searchParams.get('url');
        if (rawUrl) {
          // searchParams.get() already decodes %-encoding, so use directly
          console.log('Shaka: Extracted manifest URL:', rawUrl?.substring(0, 120));
          manifestUrl = rawUrl;
          manifestBase = rawUrl.substring(0, rawUrl.lastIndexOf('/') + 1);
        }
      }
    } catch {}

    // @ts-ignore
    shaka.polyfill.installAll();
    // @ts-ignore
    if (!shaka.Player.isBrowserSupported()) {
      setErrorLoading(true);
      return;
    }

    // @ts-ignore
    player = new shaka.Player();
    shakaRef.current = player;

    // attach() was separated from the constructor in v4.x; constructor-based
    // init is deprecated and will be removed in v5.0.
    // @ts-ignore
    player.attach(videoRef.current)
      .then(() => {
        if (!isMounted) return;
        player.addEventListener('error', (event: any) => {
        const err = event.detail;
        if (err?.code === 7002) return; // Abort
        console.error('Shaka Error:', {
          code: err?.code,
          category: err?.category,
          severity: err?.severity,
          message: err?.message,
          detail: err?.detail,
          data: JSON.stringify(err?.data)
        });
        setErrorLoading(true);
      });

      // ── Networking filter ──────────────────────────────────────
      // Proxies CDN requests through the Go backend so we can set
      // referer / user-agent headers required by the upstream source.
      const net = player.getNetworkingEngine();
      net.registerRequestFilter((type: any, request: any) => {
        // Don't proxy DRM license requests — send directly to the
        // license server. The CDN proxy doesn't forward POST bodies,
        // which EME license exchange requires (the license challenge).
        if (type === 2 || type === 5) return; // LICENSE | KEY_SYSTEM_LICENSE

        let uri = request.uris[0];
        const isLocal = uri.includes('127.0.0.1') || uri.includes('localhost') || uri.includes(BACKEND_HOST);

        if (isLocal) {
          if (uri.includes('/proxy/')) {
            const m = uri.match(/\/proxy\/([^?]+)/);
            if (m) uri = new URL(m[1], manifestBase).href;
          }
          if (uri.includes('/proxy?url=')) {
            try {
              const u = new URL(uri);
              const enc = u.searchParams.get('url');
              if (enc && !decodeURIComponent(enc).startsWith('http')) {
                uri = new URL(decodeURIComponent(enc), manifestBase).href;
              }
            } catch {}
          }
          if (uri.includes('127.0.0.1') || uri.includes('localhost') || uri.includes(BACKEND_HOST)) {
            request.uris = [uri];
            return;
          }
        }

        // Wrap external (CDN) URLs through the backend proxy
        const ua = userAgent || DEFAULT_UA;
        let proxyUrl = `${BACKEND_BASE}/api/v1/proxy?url=${encodeURIComponent(uri)}`;
        if (sourceType) proxyUrl += `&source=${encodeURIComponent(sourceType)}`;
        if (ua) proxyUrl += `&ua=${encodeURIComponent(ua)}`;
        if (referer) proxyUrl += `&ref=${encodeURIComponent(referer)}`;
        request.uris = [proxyUrl];
      });

      net.registerResponseFilter((type: any, response: any) => {
        // Override the response URI for manifest requests so Shaka
        // resolves relative segment paths against the original CDN URL
        // instead of the proxy URL.
        if (type === 0 && manifestUrl) {
          response.uri = manifestUrl;
        }
        if (response.status >= 400) {
          console.error(`Shaka: HTTP ${response.status} for ${response.uri}`);
        }
      });

      // ── DRM configuration ──────────────────────────────────────
      if (isDRM) {
        console.log(`Shaka: DRM config — type=${drmType}, licenseUrl=${!!licenseUrl}, keyProvided=${!!drmK}`);
      }
      const config: any = {
        drm: { servers: {}, clearKeys: {} },
        streaming: { rebufferingGoal: 2, bufferingGoal: 5, lowLatencyMode: true },
      };
      if (licenseUrl) config.drm.servers['com.widevine.alpha'] = licenseUrl;
      if (drmK?.includes(':')) {
        const [kid, key] = drmK.split(':');
        console.log(`Shaka: ClearKey DRM — kid=${kid.substring(0, 8)}..., keyLen=${key.length}`);
        config.drm.clearKeys[kid] = key;
      }
      player.configure(config);

      if (!isMounted) return;

      console.log('Shaka: Calling load with manifestUrl:', manifestUrl?.substring(0, 100));
      player.load(manifestUrl)
        .then(() => {
          if (!isMounted) return;
          console.log('Shaka: Load succeeded — manifestUrl:', manifestUrl?.substring(0, 100));
          setErrorLoading(false);
          // Log video events to trace playback state
          const logEvent = (e: Event) => console.log(`Video: ${e.type}`, videoRef.current?.readyState, videoRef.current?.networkState);
          const events = ['loadstart', 'loadedmetadata', 'loadeddata', 'canplay', 'canplaythrough', 'playing', 'waiting', 'stalled', 'emptied', 'suspend', 'error', 'ended'];
          events.forEach(evt => videoRef.current?.addEventListener(evt, logEvent));
          if (videoRef.current) {
            videoRef.current.muted = false;
            videoRef.current.volume = 1.0;
            console.log('Shaka: Calling play()');
            videoRef.current.play()
              .then(() => {
                console.log('Shaka: play() resolved OK');
                setIsPlaying(true);
              })
              .catch((err: any) => {
                console.warn('Shaka: play() failed, retrying muted:', err?.message);
                if (videoRef.current) {
                  videoRef.current.muted = true;
                  videoRef.current.play()
                    .then(() => {
                      console.log('Shaka: muted play() resolved OK');
                      setIsPlaying(true);
                    })
                    .catch((e2: any) => console.error('Shaka: muted play() also failed:', e2?.message));
                }
              });
          }
        })
        .catch((e: any) => {
          if (e.code === 7002) return;
          console.error('Shaka: Load failed', e);
          setErrorLoading(true);
        });
    })
    .catch((e: any) => {
      if (!isMounted) return;
      console.error('Shaka: Attach failed', e);
      setErrorLoading(true);
    });

    return () => {
      isMounted = false;
      if (player) {
        player.destroy();
        shakaRef.current = null;
      }
    };
  }, [videoUrl, needsShaka, drmType, drmK, licenseUrl, userAgent, referer]);

  // ── Native source for non-adaptive content (simple MP4) ───────
  useEffect(() => {
    if (needsShaka || !videoRef.current || !videoUrl) return;

    // Tear down any lingering Shaka instance
    if (shakaRef.current) {
      shakaRef.current.destroy();
      shakaRef.current = null;
    }

    setIsPlaying(false);
    setCurrentTime(0);
    setErrorLoading(false);
    videoRef.current.src = videoUrl;
    videoRef.current.load();
    if (savedTime > 0) {
      videoRef.current.currentTime = savedTime;
      setCurrentTime(savedTime);
    }
  }, [videoUrl, needsShaka, savedTime]);

  // ── Sync volume state ─────────────────────────────────────────
  useEffect(() => {
    if (videoRef.current) {
      videoRef.current.volume = isMuted ? 0 : volume;
      videoRef.current.muted = isMuted;
    }
  }, [volume, isMuted]);

  // ── Auto-hide controls on inactivity ──────────────────────────
  const triggerShowControls = () => {
    setShowControls(true);
    if (hideControlsTimeoutRef.current) {
      window.clearTimeout(hideControlsTimeoutRef.current);
    }
    if (isPlaying) {
      hideControlsTimeoutRef.current = window.setTimeout(() => {
        setShowControls(false);
        setShowSpeedMenu(false);
      }, 3000);
    }
  };

  useEffect(() => {
    triggerShowControls();
    return () => {
      if (hideControlsTimeoutRef.current) {
        window.clearTimeout(hideControlsTimeoutRef.current);
      }
    };
  }, [isPlaying]);

  // ── Keyboard shortcuts ────────────────────────────────────────
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      const activeElement = document.activeElement;
      if (
        activeElement &&
        (activeElement.tagName === "INPUT" ||
         activeElement.tagName === "TEXTAREA" ||
         activeElement.getAttribute("contenteditable") === "true")
      ) {
        return;
      }
      if (!videoRef.current) return;

      switch (e.key.toLowerCase()) {
        case " ":
          e.preventDefault();
          togglePlay();
          break;
        case "arrowleft":
          e.preventDefault();
          skipTime(-10);
          break;
        case "arrowright":
          e.preventDefault();
          skipTime(10);
          break;
        case "arrowup":
          e.preventDefault();
          changeVolume(0.05);
          break;
        case "arrowdown":
          e.preventDefault();
          changeVolume(-0.05);
          break;
        case "m":
          e.preventDefault();
          toggleMute();
          break;
        case "f":
          e.preventDefault();
          toggleFullscreen();
          break;
        case "t":
          e.preventDefault();
          setIsTheater(prev => !prev);
          break;
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [volume, isMuted, isPlaying]);

  // ── Video event handlers ──────────────────────────────────────
  const handleTimeUpdate = () => {
    if (!videoRef.current) return;
    const current = videoRef.current.currentTime;
    setCurrentTime(current);

    if (videoRef.current.buffered.length > 0) {
      const buf = videoRef.current.buffered.end(videoRef.current.buffered.length - 1);
      setBuffered(buf);
    }

    if (onProgress && duration > 0) {
      onProgress(parseFloat((current / duration).toFixed(3)), current);
    }
  };

  const handleLoadedMetadata = () => {
    if (!videoRef.current) return;
    setDuration(videoRef.current.duration || durationSeconds);
    if (savedTime > 0) {
      videoRef.current.currentTime = savedTime;
    }
  };

  const handleVideoEnded = () => {
    setIsPlaying(false);
    setShowControls(true);
    if (onProgress) onProgress(1.0, duration);
    if (onEnded) onEnded();
  };

  // ── Video actions ─────────────────────────────────────────────
  const togglePlay = () => {
    if (!videoRef.current) return;
    if (isPlaying) {
      videoRef.current.pause();
      setIsPlaying(false);
      triggerActionBubble("pause");
    } else {
      videoRef.current.play().then(() => {
        setIsPlaying(true);
        triggerActionBubble("play");
      }).catch(() => {
        setErrorLoading(true);
      });
    }
  };

  const triggerActionBubble = (action: "play" | "pause" | "forward" | "rewind") => {
    setBubbleAction(action);
    setTimeout(() => setBubbleAction(null), 550);
  };

  const skipTime = (secs: number) => {
    if (!videoRef.current) return;
    let newTime = videoRef.current.currentTime + secs;
    if (newTime < 0) newTime = 0;
    if (newTime > duration) newTime = duration;
    videoRef.current.currentTime = newTime;
    setCurrentTime(newTime);
    triggerActionBubble(secs > 0 ? "forward" : "rewind");
  };

  const changeVolume = (delta: number) => {
    setVolume(prev => {
      const next = Math.min(1, Math.max(0, prev + delta));
      if (next > 0 && isMuted) setIsMuted(false);
      return next;
    });
  };

  const toggleMute = () => {
    if (isMuted) {
      setIsMuted(false);
      setVolume(prevVolume);
    } else {
      setPrevVolume(volume);
      setIsMuted(true);
    }
  };

  const handleScrubberChange = (e: React.MouseEvent<HTMLDivElement>) => {
    if (!progressContainerRef.current || !videoRef.current || duration === 0) return;
    const rect = progressContainerRef.current.getBoundingClientRect();
    const clickX = e.clientX - rect.left;
    const width = rect.width;
    const percentage = Math.min(1, Math.max(0, clickX / width));
    const seekTime = percentage * duration;
    videoRef.current.currentTime = seekTime;
    setCurrentTime(seekTime);
  };

  const handleSpeedChange = (speed: number) => {
    if (videoRef.current) {
      videoRef.current.playbackRate = speed;
      setPlaybackSpeed(speed);
      setShowSpeedMenu(false);
    }
  };

  const toggleFullscreen = () => {
    if (!containerRef.current) return;
    if (!document.fullscreenElement) {
      containerRef.current.requestFullscreen().then(() => setIsFullscreen(true)).catch(err => {
        console.error("Error enabling fullscreen:", err);
      });
    } else {
      document.exitFullscreen().then(() => setIsFullscreen(false));
    }
  };

  // Sync fullscreen state when exited via Escape key
  useEffect(() => {
    const handleFullscreenChange = () => setIsFullscreen(!!document.fullscreenElement);
    document.addEventListener("fullscreenchange", handleFullscreenChange);
    return () => document.removeEventListener("fullscreenchange", handleFullscreenChange);
  }, []);

  const triggerPictureInPicture = async () => {
    if (!videoRef.current) return;
    try {
      if (document.pictureInPictureElement) {
        await document.exitPictureInPicture();
      } else {
        await videoRef.current.requestPictureInPicture();
      }
    } catch (e) {
      console.error("Picture in picture not supported on this device/browser.", e);
    }
  };

  const formatTime = (timeInSecs: number) => {
    if (isNaN(timeInSecs)) return "00:00";
    const hrs = Math.floor(timeInSecs / 3600);
    const mins = Math.floor((timeInSecs % 3600) / 60);
    const secs = Math.floor(timeInSecs % 60);
    const pad = (n: number) => String(n).padStart(2, "0");
    if (hrs > 0) return `${hrs}:${pad(mins)}:${pad(secs)}`;
    return `${pad(mins)}:${pad(secs)}`;
  };

  const progressPercent = duration > 0 ? (currentTime / duration) * 100 : 0;
  const bufferedPercent = duration > 0 ? (buffered / duration) * 100 : 0;

  // ── Render ────────────────────────────────────────────────────
  return (
    <div
      id={`player-container-${id}`}
      ref={containerRef}
      onMouseMove={triggerShowControls}
      onMouseLeave={() => isPlaying && setShowControls(false)}
      className={`relative w-full rounded-2xl overflow-hidden glass-panel group shadow-2xl transition-all duration-500 select-none ${
        isTheater && !isFullscreen ? "max-w-[100%] aspect-[21/9]" : "max-w-5xl aspect-video"
      }`}
    >
      {/* Background Poster (skeletal background before playback) */}
      {!isPlaying && currentTime === 0 && (
        <div className="absolute inset-0 z-10 w-full h-full">
          <img
            src={posterSrc}
            alt={title}
            className="w-full h-full object-cover brightness-[0.4] filter blur-[1px] scale-102 transition-all duration-700"
            referrerPolicy="no-referrer"
            onError={(e) => {
              const target = e.currentTarget;
              if (!target.dataset.fallbackTried) {
                target.dataset.fallbackTried = "1";
                target.src = "https://images.unsplash.com/photo-1489599849927-2ee91cede3ba?w=800&auto=format&fit=crop&q=80";
              }
            }}
          />
          <div className="absolute inset-0 bg-gradient-to-t from-slate-950 via-slate-950/20 to-transparent" />
        </div>
      )}

      {/* HTML5 Video element — Shaka attaches to this when playing adaptive streams */}
      <video
        ref={videoRef}
        className="w-full h-full object-contain bg-black z-0 relative"
        onTimeUpdate={handleTimeUpdate}
        onLoadedMetadata={handleLoadedMetadata}
        onEnded={handleVideoEnded}
        onClick={togglePlay}
        playsInline
        preload="metadata"
        crossOrigin="anonymous"
      />

      {/* Error HUD */}
      {errorLoading && (
        <div className="absolute inset-0 z-30 flex flex-col items-center justify-center bg-slate-950/90 text-center gap-4 px-6">
          <div className="p-3 bg-red-500/10 border border-red-500/30 rounded-full text-red-400">
            <RefreshCw className="w-8 h-8 animate-spin" />
          </div>
          <h3 className="font-display font-semibold text-lg text-slate-100">Playback Interference</h3>
          <p className="text-sm text-slate-400 max-w-md">
            {isDRM
              ? "DRM license or key could not be validated. Check your entitlements."
              : "The source stream could not be loaded cleanly. Attempt a network retry or pick a separate title."}
          </p>
          <button
            onClick={() => { setErrorLoading(false); if (videoRef.current) videoRef.current.load(); }}
            className="px-4 py-2 bg-orange-600 hover:bg-orange-700 rounded-lg text-xs font-semibold tracking-wider uppercase transition-all"
          >
            Hot Reload Player
          </button>
        </div>
      )}

      {/* Interactive Micro Anim Center Overlay */}
      <AnimatePresence>
        {bubbleAction && (
          <motion.div
            initial={{ opacity: 0, scale: 0.5 }}
            animate={{ opacity: 1, scale: 1.2 }}
            exit={{ opacity: 0, scale: 1.5 }}
            transition={{ duration: 0.4, ease: "easeOut" }}
            className="absolute inset-0 flex items-center justify-center z-25 pointer-events-none"
          >
            <div className="p-5 bg-orange-600/30 text-white backdrop-blur-md rounded-full border border-orange-500/30 shadow-lg flex items-center justify-center">
              {bubbleAction === "play" && <Play className="w-8 h-8 fill-current" />}
              {bubbleAction === "pause" && <Pause className="w-8 h-8 fill-current" />}
              {bubbleAction === "forward" && <RotateCw className="w-8 h-8" />}
              {bubbleAction === "rewind" && <RotateCcw className="w-8 h-8" />}
            </div>
          </motion.div>
        )}
      </AnimatePresence>

      {/* Gradient Vignette */}
      <div className={`absolute inset-0 bg-gradient-to-t from-black/90 via-transparent to-black/40 transition-opacity duration-500 pointer-events-none z-10 ${
        showControls ? "opacity-100" : "opacity-0"
      }`} />

      {/* Floating Header Info */}
      <AnimatePresence>
        {showControls && (
          <motion.div
            initial={{ opacity: 0, y: -20 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, y: -20 }}
            className="absolute top-0 inset-x-0 z-20 p-5 flex items-center justify-between pointer-events-auto"
          >
            <div className="flex flex-col gap-0.5 max-w-[70%]">
              <span className="text-[10px] text-orange-400 font-mono tracking-widest uppercase font-semibold">Active Transmission</span>
              <h4 className="font-display font-medium text-slate-100 text-sm md:text-base tracking-wide truncate">{title}</h4>
              {type && (
                <div className="flex flex-wrap items-center gap-1.5 mt-1">
                  <span className={`text-[9px] font-mono uppercase tracking-wider px-1.5 py-0.5 rounded ${
                    type === "Series"
                      ? "bg-purple-500/20 text-purple-300 border border-purple-500/30"
                      : "bg-blue-500/20 text-blue-300 border border-blue-500/30"
                  }`}>
                    {type}
                  </span>
                  {year && <span className="text-[9px] text-slate-400 font-mono">{year}</span>}
                  {episodeCount && type === "Series" && (
                    <span className="text-[9px] bg-emerald-500/20 text-emerald-300 font-mono px-1.5 py-0.5 rounded border border-emerald-500/30">
                      {episodeCount} ep.
                    </span>
                  )}
                  {isDRM && (
                    <span className="text-[9px] bg-red-500/20 text-red-300 font-mono px-1.5 py-0.5 rounded border border-red-500/30">
                      DRM {drmType}
                    </span>
                  )}
                  {tags && tags.length > 0 && tags.slice(0, 3).map(tag => (
                    <span key={tag} className="text-[9px] bg-white/5 text-slate-400 font-mono px-1.5 py-0.5 rounded border border-white/10">
                      {tag}
                    </span>
                  ))}
                </div>
              )}
            </div>

            <div className="flex items-center gap-2">
              {savedTime > 0 && currentTime === savedTime && (
                <span className="text-[10px] bg-orange-500/20 border border-orange-500/30 text-orange-300 font-mono px-2 py-0.5 rounded-full flex items-center gap-1">
                  <span className="relative flex h-1.5 w-1.5">
                    <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-orange-400 opacity-75"></span>
                    <span className="relative inline-flex rounded-full h-1.5 w-1.5 bg-orange-500"></span>
                  </span>
                  Resumed
                </span>
              )}
              {isLive && (
                <span className="text-[10px] font-mono text-red-500 bg-red-500/10 border border-red-500/20 px-2.5 py-1 rounded-full font-bold flex items-center gap-1">
                  <span className="w-1.5 h-1.5 rounded-full bg-red-650 animate-ping" />
                  LIVE
                </span>
              )}
            </div>
          </motion.div>
        )}
      </AnimatePresence>

      {/* Bottom Controls */}
      <div className={`absolute bottom-0 inset-x-0 z-20 transition-opacity duration-500 ${
        showControls ? "opacity-100" : "opacity-0 pointer-events-none"
      }`}>
        {/* Scrubber / Progress Bar */}
        <div
          ref={progressContainerRef}
          onClick={handleScrubberChange}
          className="relative w-full h-1.5 bg-white/10 hover:h-2.5 transition-all cursor-pointer group/scrubber"
        >
          <div
            className="absolute top-0 left-0 h-full bg-white/20 transition-all duration-300"
            style={{ width: `${bufferedPercent}%` }}
          />
          <div
            className="absolute top-0 left-0 h-full bg-orange-500 transition-all duration-100 group-hover/scrubber:bg-orange-400"
            style={{ width: `${progressPercent}%` }}
          />
          <div
            className="absolute top-1/2 -translate-y-1/2 w-3 h-3 bg-orange-500 rounded-full shadow-lg opacity-0 group-hover/scrubber:opacity-100 transition-opacity"
            style={{ left: `calc(${progressPercent}% - 6px)` }}
          />
        </div>

        {/* Button Row */}
        <div className="flex items-center justify-between p-4 bg-gradient-to-t from-black/95 to-transparent">
          <div className="flex items-center gap-3">
            <button
              onClick={togglePlay}
              className="p-2.5 bg-orange-600/20 hover:bg-orange-600/40 text-white rounded-full transition-all focus:outline-none"
            >
              {isPlaying ? <Pause className="w-5 h-5" /> : <Play className="w-5 h-5 fill-current" />}
            </button>
            <button
              onClick={() => skipTime(-10)}
              className="p-1.5 hover:bg-white/10 rounded-full transition-all text-slate-300 focus:outline-none"
            >
              <RotateCcw className="w-4 h-4" />
            </button>
            <button
              onClick={() => skipTime(10)}
              className="p-1.5 hover:bg-white/10 rounded-full transition-all text-slate-300 focus:outline-none"
            >
              <RotateCw className="w-4 h-4" />
            </button>

            {/* Episode navigation */}
            {hasPrevEpisode && (
              <button
                onClick={onPrevEpisode}
                className="p-1.5 hover:bg-white/10 rounded-full transition-all text-slate-300 focus:outline-none flex items-center gap-0.5 text-[10px] font-mono"
                title="Previous episode"
              >
                <SkipBack className="w-3.5 h-3.5" />
              </button>
            )}
            {hasNextEpisode && (
              <button
                onClick={onNextEpisode}
                className="p-1.5 hover:bg-orange-600/20 rounded-full transition-all text-orange-400 focus:outline-none flex items-center gap-0.5 text-[10px] font-mono"
                title="Next episode"
              >
                <SkipForward className="w-3.5 h-3.5" />
              </button>
            )}

            <div className="flex items-center gap-1.5 group/vol">
              <button
                onClick={toggleMute}
                className="p-1.5 hover:bg-white/10 rounded-full transition-all text-slate-300 focus:outline-none"
              >
                {isMuted || volume === 0 ? <VolumeX className="w-4 h-4" /> : <Volume2 className="w-4 h-4" />}
              </button>
              <input
                type="range"
                min="0"
                max="1"
                step="0.05"
                value={isMuted ? 0 : volume}
                onChange={(e) => {
                  const v = parseFloat(e.target.value);
                  setVolume(v);
                  if (v > 0 && isMuted) setIsMuted(false);
                }}
                className="w-0 group-hover/vol:w-20 transition-all appearance-none bg-white/20 h-1 rounded cursor-pointer"
              />
            </div>
            <span className="text-[10px] font-mono text-slate-400 ml-2">
              {formatTime(currentTime)} / {formatTime(duration)}
            </span>
          </div>

          <div className="flex items-center gap-3">
            <div className="relative">
              <button
                onClick={() => setShowSpeedMenu(!showSpeedMenu)}
                className="p-1.5 hover:bg-white/10 rounded-full transition-all text-slate-300 focus:outline-none flex items-center gap-1"
              >
                <span className="text-[10px] font-mono">{playbackSpeed}x</span>
              </button>
              {showSpeedMenu && (
                <div className="absolute bottom-full right-0 mb-2 bg-slate-900 border border-white/10 rounded-xl p-2 shadow-xl min-w-[100px]">
                  {[0.5, 1, 1.25, 1.5, 2].map((s) => (
                    <button
                      key={s}
                      onClick={() => handleSpeedChange(s)}
                      className={`block w-full text-left px-3 py-1.5 rounded-lg text-xs font-mono hover:bg-white/10 ${
                        playbackSpeed === s ? "text-orange-400" : "text-slate-300"
                      }`}
                    >
                      {s}x {playbackSpeed === s && "✓"}
                    </button>
                  ))}
                </div>
              )}
            </div>
            <button
              onClick={triggerPictureInPicture}
              className="p-1.5 hover:bg-white/10 rounded-full transition-all text-slate-300 focus:outline-none"
            >
              <Tv className="w-4 h-4" />
            </button>
            <button
              onClick={() => setIsTheater(!isTheater)}
              className={`p-1.5 hover:bg-white/10 rounded-full transition-all focus:outline-none ${
                isTheater ? "text-orange-400" : "text-slate-300"
              }`}
            >
              <Zap className="w-4 h-4" />
            </button>
            <button
              onClick={toggleFullscreen}
              className="p-1.5 hover:bg-white/10 rounded-full transition-all text-slate-300 focus:outline-none"
            >
              {isFullscreen ? <Minimize2 className="w-4 h-4" /> : <Maximize2 className="w-4 h-4" />}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
