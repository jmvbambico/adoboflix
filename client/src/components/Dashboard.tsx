/**
 * @license
 * SPDX-License-Identifier: Apache-2.0
 */

import React, { useState, useEffect, useCallback } from "react";
import { Video, WatchlistState, WatchHistoryItem } from "../types";
import { CATEGORIES as FALLBACK_CATEGORIES } from "../data/videos";
import { IPTV_CATEGORIES } from "../data/iptv";
import { fetchVideos, fetchCategories, resolveStream } from "../api/client";
import { resolveEpisode, fetchEpisodes } from "../api/client";
import { Episode } from "../types";
import {
  BackendChannel,
  ResolvedChannelStream,
  ChannelEPG,
  fetchChannels as fetchChannelsAPI,
  fetchChannelCategories as fetchChannelCategoriesAPI,
  resolveChannelStream as resolveChannelStreamAPI,
  fetchChannelEPG,
} from "../api/client";
import Header from "./Header";
import MediaCard from "./MediaCard";
import CustomPlayer from "./CustomPlayer";
import GlowBackground from "./GlowBackground";
import SourceStatusPanel from "./SourceStatusPanel";
import { 
  Play, Plus, Heart, Compass, History, Star, 
  ChevronDown, ChevronRight, CircleCheck, Film, ListFilter, Users, BookOpen,
  Tv, X, Sparkles
} from "lucide-react";
import { motion, AnimatePresence } from "motion/react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";

export default function Dashboard() {
  const queryClient = useQueryClient();

  // Search & Category states
  const [searchQuery, setSearchQuery] = useState("");
  const [selectedCategory, setSelectedCategory] = useState("All");
  
  // IPTV Specific States
  const [selectedIptvCategory, setSelectedIptvCategory] = useState("All");
  const [selectedChannel, setSelectedChannel] = useState<BackendChannel | null>(null);
  const [channelStream, setChannelStream] = useState<ResolvedChannelStream | null>(null);
  
  // Collapsible category sections for IPTV (tab view)
  const [collapsedCategories, setCollapsedCategories] = useState<Record<string, boolean>>({});
  const toggleCategoryCollapse = (cat: string) => setCollapsedCategories(prev => ({ ...prev, [cat]: !prev[cat] }));

  // Accordion state for the IPTV sidebar playlist — key=category, true=expanded
  const [channelAccordion, setChannelAccordion] = useState<Record<string, boolean>>({});
  const toggleChannelAccordion = (cat: string) => setChannelAccordion(prev => ({ ...prev, [cat]: !prev[cat] }));

  // "More" accordion state for category pills
  const [showAllCategories, setShowAllCategories] = useState(false);
  const [showAllIptvCategories, setShowAllIptvCategories] = useState(false);
  const PILL_VISIBLE_COUNT = 6;

  // Curated genre ordering (stand-in for view-based popularity until API provides per-category view data)
  const GENRE_POPULARITY = [
    "All", "Drama", "Action", "Comedy", "Romance", "Thriller", 
    "Horror", "Sci-Fi & Fantasy", "Adventure", "Fantasy", "Anime",
    "Documentary", "Family", "Mystery", "Crime", "Musical",
    "Reality", "Variety", "News", "Sports", "Kids"
  ];
  const CHANNEL_CATEGORY_POPULARITY = [
    "All", "News", "Music", "Sports", "Entertainment", 
    "Documentary", "Kids", "Educational", "Religion"
  ];

  const sortByPopularity = (items: string[], ordered: string[]) => {
    const score = (cat: string) => {
      const idx = ordered.indexOf(cat);
      return idx >= 0 ? idx : ordered.length;
    };
    return [...items].sort((a, b) => score(a) - score(b));
  };
  
  
  // Navigation tabs: 'browse' (VOD Catalog) or 'iptv' (Live IPTV) or 'watchlist' or 'history'
  const [activeTab, setActiveTab] = useState<"browse" | "iptv" | "watchlist" | "history">("browse");

  // Selected Movie for active cinematic playback details
  const [selectedVideo, setSelectedVideo] = useState<Video | null>(null);

  // A failed playable call (resolve/stream). Kept so the gate it carries can be
  // shown instead of an empty player; retry re-runs the action that failed.
  const [playbackError, setPlaybackError] = useState<{ error: unknown; retry: () => void } | null>(null);

  // Series episode state
  const [episodes, setEpisodes] = useState<Episode[]>([]);
  const [currentEpisode, setCurrentEpisode] = useState<Episode | null>(null);
  const [selectedSeason, setSelectedSeason] = useState<number>(1);
  const [episodesLoading, setEpisodesLoading] = useState(false);

  // Sidebar height matching — measures player column to cap sidebar at Stream Details bottom
  const [iptvPlayerHeight, setIptvPlayerHeight] = useState(0);
  const [vodPlayerHeight, setVodPlayerHeight] = useState(0);
  const iptvPlayerColRef = useCallback((node: HTMLDivElement | null) => {
    if (node) setIptvPlayerHeight(node.offsetHeight);
  }, []);
  const vodPlayerColRef = useCallback((node: HTMLDivElement | null) => {
    if (node) setVodPlayerHeight(node.offsetHeight);
  }, []);

  // Core Queries using TanStack Query

  // 1. Fetching catalogue from AdoboTV PostgreSQL backend
  const {
    data: videos = [],
    isLoading,
    isError: isVideosError,
    error: videosError,
    refetch: refetchVideos,
  } = useQuery<Video[]>({
    queryKey: ["videos", searchQuery, selectedCategory],
    queryFn: () => fetchVideos({ search: searchQuery, category: selectedCategory }),
    staleTime: 5 * 60 * 1000,
  });

  // The library failed and there is no cached data to fall back on.
  const libraryUnavailable = isVideosError && videos.length === 0;

  // 1b. Fetch genre categories from backend
  const { data: categories = FALLBACK_CATEGORIES } = useQuery<string[]>({
    queryKey: ["categories"],
    queryFn: fetchCategories,
    staleTime: 30 * 60 * 1000,
  });

  // 1c. Fetch IPTV channel categories from backend
  const { data: iptvCategories = ["All"] } = useQuery<string[]>({
    queryKey: ["iptv-categories"],
    queryFn: fetchChannelCategoriesAPI,
    staleTime: 30 * 60 * 1000,
  });

  // 1d. Fetch IPTV channels from backend
  const { data: iptvChannels = [], isLoading: iptvLoading } = useQuery<BackendChannel[]>({
    queryKey: ["channels", selectedIptvCategory],
    queryFn: () => fetchChannelsAPI({ category: selectedIptvCategory, limit: 500 }),
    staleTime: 60 * 1000,
  });

  // 1e. Fetch EPG for currently selected channel
  const { data: channelEPG } = useQuery<ChannelEPG | null>({
    queryKey: ["channel-epg", selectedChannel?.id],
    queryFn: () => selectedChannel ? fetchChannelEPG(selectedChannel.id) : Promise.resolve(null),
    enabled: !!selectedChannel,
    staleTime: 60 * 1000,
    refetchInterval: 60 * 1000, // auto-refresh every minute
  });

  // 2. Fetch Watchlist Query
  const { data: watchlist = [] } = useQuery<string[]>({
    queryKey: ["watchlist"],
    queryFn: () => {
      const stored = localStorage.getItem("glassstream-watchlist");
      return stored ? JSON.parse(stored) : [];
    }
  });

  // Watchlist Toggle Mutation
  const toggleWatchlistMutation = useMutation({
    mutationFn: async (videoId: string) => {
      const current = [...watchlist];
      const index = current.indexOf(videoId);
      if (index > -1) {
        current.splice(index, 1);
      } else {
        current.push(videoId);
      }
      localStorage.setItem("glassstream-watchlist", JSON.stringify(current));
      return current;
    },
    onSuccess: (updatedWatchlist) => {
      queryClient.setQueryData(["watchlist"], updatedWatchlist);
    }
  });

  // 3. Fetch Watch History Query
  const { data: watchHistory = [] } = useQuery<WatchHistoryItem[]>({
    queryKey: ["watch-history"],
    queryFn: () => {
      const stored = localStorage.getItem("glassstream-history");
      return stored ? JSON.parse(stored) : [];
    }
  });

  // Update History Progress Mutation
  const updateHistoryMutation = useMutation({
    mutationFn: async ({ videoId, currentTime }: { videoId: string; currentTime: number }) => {
      const video = videos.find(v => v.id === videoId);
      if (!video) return watchHistory;

      const dur = video.durationSeconds || 100;
      const progress = parseFloat((currentTime / dur).toFixed(3));

      let currentHistory = [...watchHistory];
      const existingIdx = currentHistory.findIndex(h => h.videoId === videoId);

      const newItem: WatchHistoryItem = {
        videoId,
        watchedAt: new Date().toISOString(),
        progress,
        currentTime
      };

      if (existingIdx > -1) {
        currentHistory.splice(existingIdx, 1);
      }
      // Insert to the front representing active recents
      currentHistory = [newItem, ...currentHistory];
      localStorage.setItem("glassstream-history", JSON.stringify(currentHistory));
      return currentHistory;
    },
    onSuccess: (updatedHistory) => {
      queryClient.setQueryData(["watch-history"], updatedHistory);
    }
  });

  // Find Featured Movie (Tears of Steel default)
  const featuredVideo = videos.find(v => v.isFeatured) || videos[0];

  // Helper selectors
  const isVideoInWatchlist = (id: string) => watchlist.includes(id);
  const getVideoHistory = (id: string) => watchHistory.find(h => h.videoId === id);

  // Choose watch session savedTime to resume
  const getResumeTime = (id: string) => {
    const hist = getVideoHistory(id);
    return hist ? hist.currentTime : 0;
  };

  // Scroll back to player when changing movie
  const triggerPlayVideo = async (video: Video) => {
    setSelectedChannel(null);
    setCurrentEpisode(null);
    setPlaybackError(null);
    try {
      const resolved = await resolveStream(video.id);
      setSelectedVideo({ ...video, videoUrl: resolved.url, drmType: resolved.drm_type, drmK: resolved.drm_k, licenseUrl: resolved.license_url, provider: resolved.provider, userAgent: resolved.user_agent, referer: resolved.referer });
      
      // Load episodes for series
      if (video.type === "Series") {
        setEpisodesLoading(true);
        try {
          const eps = await fetchEpisodes(video.id);
          setEpisodes(eps || []);
          if (eps && eps.length > 0) {
            const seasons = [...new Set(eps.map(e => e.season_number))].sort();
            setSelectedSeason(seasons[0]);
          }
        } catch (e) {
          console.error("Failed to load episodes:", e);
          setEpisodes([]);
        }
        setEpisodesLoading(false);
      } else {
        setEpisodes([]);
      }
    } catch (e) {
      setSelectedVideo(video);
      setPlaybackError({ error: e, retry: () => { void triggerPlayVideo(video); } });
    }
    window.scrollTo({ top: 0, behavior: "smooth" });
  };

  // Play a specific episode
  const playEpisode = async (episode: Episode) => {
    setPlaybackError(null);
    try {
      const resolved = await resolveEpisode(episode.id);
      setCurrentEpisode(episode);
      setSelectedVideo(prev => prev ? {
        ...prev,
        videoUrl: resolved.url,
        drmType: resolved.drm_type,
        drmK: resolved.drm_k,
        licenseUrl: resolved.license_url,
        title: episode.name || `Episode ${episode.episode_number}`,
        provider: resolved.provider,
        userAgent: resolved.user_agent,
        referer: resolved.referer,
      } : null);
    } catch (e) {
      console.error("Failed to resolve episode:", e);
      setPlaybackError({ error: e, retry: () => { void playEpisode(episode); } });
    }
  };

  // Go to previous episode
  const playPrevEpisode = async () => {
    if (episodes.length === 0 || !currentEpisode) return;
    const sorted = episodes.filter(e => e.season_number === selectedSeason)
      .sort((a, b) => a.episode_number - b.episode_number);
    const currentIdx = sorted.findIndex(e => e.id === currentEpisode.id);
    if (currentIdx > 0) {
      await playEpisode(sorted[currentIdx - 1]);
    } else {
      // Check previous season
      const seasons = [...new Set(episodes.map(e => e.season_number))].sort();
      const currentSeasonIdx = seasons.indexOf(selectedSeason);
      if (currentSeasonIdx > 0) {
        const prevSeason = seasons[currentSeasonIdx - 1];
        setSelectedSeason(prevSeason);
        const prevEps = episodes.filter(e => e.season_number === prevSeason).sort((a, b) => a.episode_number - b.episode_number);
        if (prevEps.length > 0) {
          await playEpisode(prevEps[prevEps.length - 1]);
        }
      }
    }
  };

  const getEpisodeNavState = () => {
    if (episodes.length === 0 || !currentEpisode) return { hasPrev: false, hasNext: false };
    const sorted = episodes.filter(e => e.season_number === selectedSeason)
      .sort((a, b) => a.episode_number - b.episode_number);
    const idx = sorted.findIndex(e => e.id === currentEpisode.id);
    if (idx === -1) return { hasPrev: false, hasNext: sorted.length > 0 };

    // Has next in current season or if there's another season
    let hasNext = idx < sorted.length - 1;
    if (!hasNext) {
      const seasons = [...new Set(episodes.map(e => e.season_number))].sort();
      const currSeasonIdx = seasons.indexOf(selectedSeason);
      hasNext = currSeasonIdx < seasons.length - 1;
    }

    // Has prev in current season or if there's a previous season
    let hasPrev = idx > 0;
    if (!hasPrev) {
      const seasons = [...new Set(episodes.map(e => e.season_number))].sort();
      const currSeasonIdx = seasons.indexOf(selectedSeason);
      hasPrev = currSeasonIdx > 0;
    }

    return { hasPrev, hasNext };
  };

  // Auto-advance to next episode
  const playNextEpisode = async () => {
    if (episodes.length === 0 || !currentEpisode) return;
    const sorted = episodes.filter(e => e.season_number === selectedSeason)
      .sort((a, b) => a.episode_number - b.episode_number);
    const currentIdx = sorted.findIndex(e => e.id === currentEpisode.id);
    if (currentIdx < sorted.length - 1) {
      await playEpisode(sorted[currentIdx + 1]);
    } else {
      // Check next season
      const seasons = [...new Set(episodes.map(e => e.season_number))].sort();
      const currentSeasonIdx = seasons.indexOf(selectedSeason);
      if (currentSeasonIdx < seasons.length - 1) {
        const nextSeason = seasons[currentSeasonIdx + 1];
        setSelectedSeason(nextSeason);
        const nextEps = episodes.filter(e => e.season_number === nextSeason).sort((a, b) => a.episode_number - b.episode_number);
        if (nextEps.length > 0) {
          await playEpisode(nextEps[0]);
        }
      }
    }
  };

  // Scroll back to player when changing IPTV channel
  const triggerPlayChannel = async (channel: BackendChannel) => {
    setSelectedVideo(null);
    setChannelStream(null);
    setSelectedChannel(channel);
    setPlaybackError(null);
    // Auto-expand this channel's category in the sidebar accordion
    if (channel.category) {
      setChannelAccordion(prev => ({ ...prev, [channel.category!]: true }));
    }
    window.scrollTo({ top: 0, behavior: "smooth" });
    try {
      const resolved = await resolveChannelStreamAPI(channel.id);
      setChannelStream(resolved);
    } catch (e) {
      console.error("Failed to resolve channel stream:", e);
      setPlaybackError({ error: e, retry: () => { void triggerPlayChannel(channel); } });
    }
  };

  return (
    <div className="min-h-screen relative pb-16 flex flex-col">
      <GlowBackground />

      {/* Header element */}
      <Header 
        searchQuery={searchQuery}
        setSearchQuery={(q) => {
          setSearchQuery(q);
          setActiveTab("browse");
        }}
        watchlistCount={watchlist.length}
        historyCount={watchHistory.length}
        onNavigateToWatchlist={() => {
          setActiveTab("watchlist");
          setSearchQuery("");
        }}
        onNavigateToLibrary={() => {
          setActiveTab("browse");
          setSearchQuery("");
          setSelectedCategory("All");
          setSelectedVideo(null);
          setSelectedChannel(null);
          setCurrentEpisode(null);
        }}
      />

      <main className="w-full px-4 md:px-8 py-6 flex-grow flex flex-col gap-8">
        
        {/* TAB 1 & ACTIVE DETAIL : IMMERSIVE CUSTOM CORE PLAYER STAGE */}
        <AnimatePresence mode="wait">
          {selectedChannel ? (
            <motion.div
              initial={{ opacity: 0, y: -20 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, y: -20 }}
              transition={{ duration: 0.4 }}
              className="w-full flex flex-col gap-6"
              key={selectedChannel.id}
            >
              {/* Theater Layout Grid */}
              <div className="flex flex-col lg:flex-row gap-6 items-start">
                <div ref={iptvPlayerColRef} className="w-full lg:flex-[2] flex flex-col">
                  <div className="flex items-center justify-between mb-3 text-slate-400">
                    <button 
                      onClick={() => setSelectedChannel(null)}
                      className="text-xs text-slate-300 hover:text-white flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-white/5 border border-white/5 hover:bg-white/10 transition-all font-medium focus:outline-none cursor-pointer"
                    >
                      <X className="w-4 h-4" />
                      Disconnect TV Stream
                    </button>
                    <div className="flex items-center gap-2">
                      <span className="hidden sm:inline text-xs font-mono text-emerald-400 uppercase tracking-widest bg-emerald-500/10 border border-emerald-500/20 px-2.5 py-1 rounded-full">
                        Secure HLS Stream Tuned
                      </span>
                      <span className="text-xs font-mono text-red-500 bg-red-500/10 border border-red-500/20 px-2.5 py-1 rounded-full font-bold flex items-center gap-1">
                        <span className="w-1.5 h-1.5 rounded-full bg-red-650 animate-ping" />
                        LIVE
                      </span>
                    </div>
                  </div>

                  {playbackError ? (
                    <SourceStatusPanel error={playbackError.error} onRetry={playbackError.retry} />
                  ) : (
                    <CustomPlayer
                      id={selectedChannel.id}
                      videoUrl={channelStream?.url || ""}
                      title={selectedChannel.name}
                      thumbnailUrl={selectedChannel?.logo || ""}
                      drmType={channelStream?.drm_type || ""}
                      drmK={channelStream?.drm_k || ""}
                      licenseUrl={channelStream?.license_url || ""}
                      userAgent={channelStream?.user_agent}
                      referer={channelStream?.referer}
                      durationSeconds={0}
                      isLive={true}
                    />
                  )}

                  {/* Channel Info + Stream Details — merged panel */}
                  <div className="mt-4 glass-panel p-5 rounded-2xl border border-white/5 flex flex-col gap-4">
                    {/* Channel header row */}
                    <div className="flex flex-col sm:flex-row justify-between items-start sm:items-center gap-3">
                      <div className="flex flex-col gap-1.5">
                        <div className="flex items-center gap-2">
                          <h2 className="font-display font-extrabold text-lg sm:text-xl text-slate-105 tracking-wide">{selectedChannel.name}</h2>
                          <span className="text-[10px] uppercase font-bold tracking-widest bg-orange-600/20 text-orange-300 border border-orange-500/20 px-2 py-0.5 rounded">{selectedChannel.category || "General"}</span>
                        </div>
                        {channelStream && (
                          <div className="flex items-center gap-3 mt-1">
                            {channelStream.source_type && (
                              <span className="text-[10px] font-mono text-cyan-400 bg-cyan-500/10 border border-cyan-500/20 px-2 py-0.5 rounded">{channelStream.source_type}</span>
                            )}
                            {channelStream.resolution && (
                              <span className="text-[10px] font-mono text-slate-400">{channelStream.resolution}</span>
                            )}
                            {channelStream.label && (
                              <span className="text-[10px] font-mono text-slate-500">{channelStream.label}</span>
                            )}
                            {channelStream.drm_type && (
                              <span className="text-[9px] font-mono text-red-400 bg-red-500/10 border border-red-500/20 px-1.5 py-0.5 rounded">DRM {channelStream.drm_type}</span>
                            )}
                          </div>
                        )}
                      </div>

                      {/* EPG Programme Guide — right side */}
                      {channelEPG?.current && (
                        <div className="flex flex-col gap-1.5 min-w-0 shrink-0 sm:max-w-xs md:max-w-sm lg:max-w-md">
                          <div className="flex items-center gap-1.5">
                            <span className="text-[9px] font-mono font-bold text-emerald-400 uppercase tracking-wider bg-emerald-500/10 border border-emerald-500/20 px-2 py-0.5 rounded flex items-center gap-1">
                              <span className="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse" />
                              NOW PLAYING
                            </span>
                          </div>
                          <h3 className="text-sm font-display font-bold text-slate-100 truncate">
                            {channelEPG.current.title}
                          </h3>
                          {channelEPG.current.description && (
                            <p className="text-[10px] text-slate-500 leading-relaxed line-clamp-2">
                              {channelEPG.current.description}
                            </p>
                          )}
                          {channelEPG.next && (
                            <div className="flex items-center gap-1.5 text-[10px] mt-0.5">
                              <ChevronRight className="w-3 h-3 text-orange-400 shrink-0" />
                              <span className="text-slate-400 font-medium shrink-0">Up Next:</span>
                              <span className="text-slate-300 truncate font-semibold">{channelEPG.next.title}</span>
                            </div>
                          )}
                        </div>
                      )}

                    </div>

                    {/* Stream Details subsection */}
                    <div className="border-t border-white/5 pt-4">
                      <div className="flex items-center justify-between mb-3">
                        <div className="flex items-center gap-2">
                          <Tv className="w-4 h-4 text-orange-400" />
                          <h3 className="font-display font-bold text-xs sm:text-sm text-slate-100 tracking-wide uppercase">Stream Details</h3>
                        </div>
                        {channelStream && (
                          <span className="text-[10px] font-mono text-emerald-400 uppercase hidden sm:inline">● LIVE</span>
                        )}
                      </div>

                      {channelStream ? (
                        <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-7 gap-4">
                          {/* Source Type */}
                          <div className="flex flex-col gap-1">
                            <span className="text-[10px] font-mono text-slate-500 uppercase tracking-wider">Source</span>
                            <span className={`text-[10px] font-mono font-bold px-2 py-0.5 rounded border w-fit ${
                              channelStream.source_type === "DRM"
                                ? "text-red-400 bg-red-500/10 border-red-500/20"
                                : channelStream.source_type === "HLS"
                                  ? "text-cyan-400 bg-cyan-500/10 border-cyan-500/20"
                                  : channelStream.source_type === "DASH"
                                    ? "text-emerald-400 bg-emerald-500/10 border-emerald-500/20"
                                    : "text-slate-300 bg-white/5 border-white/10"
                            }`}>
                              {channelStream.source_type || "Unknown"}
                            </span>
                          </div>

                          {/* Resolution */}
                          <div className="flex flex-col gap-1">
                            <span className="text-[10px] font-mono text-slate-500 uppercase tracking-wider">Resolution</span>
                            <span className={`text-xs font-semibold ${channelStream.resolution ? "text-emerald-400" : "text-slate-500"}`}>
                              {channelStream.resolution || "Not specified"}
                            </span>
                          </div>

                          {/* Label */}
                          <div className="flex flex-col gap-1">
                            <span className="text-[10px] font-mono text-slate-500 uppercase tracking-wider">Label</span>
                            <span className={`text-xs font-semibold ${channelStream.label ? "text-slate-200" : "text-slate-500"}`}>
                              {channelStream.label || "Not specified"}
                            </span>
                          </div>

                          {/* DRM */}
                          <div className="flex flex-col gap-1">
                            <span className="text-[10px] font-mono text-slate-500 uppercase tracking-wider">DRM</span>
                            <span className={`text-xs font-semibold ${channelStream.drm_type ? "text-red-400" : "text-slate-500"}`}>
                              {channelStream.drm_type || "None"}
                            </span>
                          </div>

                          {/* Provider */}
                          <div className="flex flex-col gap-1">
                            <span className="text-[10px] font-mono text-slate-500 uppercase tracking-wider">Provider</span>
                            <span className="text-[10px] font-mono font-bold px-2 py-0.5 rounded border text-slate-300 bg-white/5 border-white/10 w-fit">
                              {channelStream.provider || "Unknown"}
                            </span>
                          </div>

                          {/* User Agent */}
                          <div className="flex flex-col gap-1">
                            <span className="text-[10px] font-mono text-slate-500 uppercase tracking-wider">User Agent</span>
                            <span className="text-[9px] font-mono text-slate-400 truncate max-w-[180px]" title={channelStream.user_agent || ""}>
                              {channelStream.user_agent
                                ? (channelStream.user_agent.length > 50
                                  ? channelStream.user_agent.slice(0, 50) + "…"
                                  : channelStream.user_agent)
                                : "Not set"}
                            </span>
                          </div>

                          {/* Referer */}
                          <div className="flex flex-col gap-1">
                            <span className="text-[10px] font-mono text-slate-500 uppercase tracking-wider">Referer</span>
                            <span className="text-[9px] font-mono text-slate-400 truncate max-w-[180px]" title={channelStream.referer || ""}>
                              {channelStream.referer
                                ? (() => { try { return new URL(channelStream.referer).hostname; } catch { return channelStream.referer; } })()
                                : "Not set"}
                            </span>
                          </div>
                        </div>
                      ) : (
                        <div className="py-6 text-center text-slate-500 text-xs font-mono">
                          Resolving stream...
                        </div>
                      )}
                    </div>
                  </div>
                </div>

                {/* IPTV SIDEBAR: Channel Playlist */}
                <div className="w-full lg:flex-[1] glass-panel rounded-2xl flex flex-col border border-white/5 overflow-hidden" style={iptvPlayerHeight ? { maxHeight: iptvPlayerHeight } : undefined}>
                  {/* Sidebar Header */}
                  <div className="shrink-0 px-4 py-3 border-b border-white/5 bg-slate-950/40 flex items-center gap-2">
                    <Sparkles className="w-4 h-4 text-orange-400" />
                    <span className="text-xs font-bold tracking-wider text-slate-200 uppercase">Channel Playlist</span>
                    <span className="text-[9px] font-mono text-slate-500 ml-auto">{iptvChannels.length} channels</span>
                  </div>

                  {/* Scrollable accordion list */}
                  <div className="flex-grow overflow-y-auto min-h-0 custom-scrollbar p-2">
                    {(() => {
                      // Group channels by category
                      const grouped = new Map<string, BackendChannel[]>();
                      iptvChannels.forEach(ch => {
                        const cat = ch.category || "General";
                        if (!grouped.has(cat)) grouped.set(cat, []);
                        grouped.get(cat)!.push(ch);
                      });
                      // Use IPTV_CATEGORIES for ordering (excluding "All"), then extras
                      const categoryOrder = IPTV_CATEGORIES.filter(c => c !== "All");
                      const knownCats = categoryOrder.filter(c => grouped.has(c));
                      const extraCats = [...grouped.keys()].filter(c => !categoryOrder.includes(c));
                      const orderedCats = [...knownCats, ...extraCats];

                      if (orderedCats.length === 0) {
                        return <div className="text-center py-8 text-slate-500 text-xs">No channels available</div>;
                      }

                      return orderedCats.map(cat => {
                        const channelsInCat = grouped.get(cat) || [];
                        const isExpanded = channelAccordion[cat] ?? false;
                        return (
                          <div key={cat} className="mb-1">
                            {/* Category header */}
                            <button
                              onClick={() => toggleChannelAccordion(cat)}
                              className="w-full flex items-center justify-between px-2.5 py-2 rounded-lg hover:bg-white/[0.03] transition-all duration-200 cursor-pointer focus:outline-none"
                            >
                              <div className="flex items-center gap-2 min-w-0">
                                <div className="w-6 h-6 rounded-md bg-gradient-to-br from-orange-600/30 to-amber-500/20 flex items-center justify-center border border-white/5 shrink-0">
                                  <Tv className="w-3 h-3 text-orange-400" />
                                </div>
                                <span className="text-xs font-semibold text-slate-200 truncate">{cat}</span>
                                <span className="font-mono text-[9px] text-slate-500 bg-white/[0.03] px-1.5 py-0.5 rounded border border-white/5 shrink-0">
                                  {channelsInCat.length}
                                </span>
                              </div>
                              <ChevronDown className={`w-3.5 h-3.5 text-slate-500 shrink-0 transition-transform duration-200 ${isExpanded ? "" : "-rotate-90"}`} />
                            </button>

                            {/* Channel list (collapsible) */}
                            {isExpanded && (
                              <div className="flex flex-col gap-1 ml-4 mt-0.5 border-l border-white/5 pl-2">
                                {channelsInCat.map(channel => {
                                  const isActive = selectedChannel?.id === channel.id;
                                  return (
                                    <div
                                      key={channel.id}
                                      onClick={() => triggerPlayChannel(channel)}
                                      className={`group flex items-center gap-2.5 p-2 rounded-lg cursor-pointer transition-all duration-200 border ${
                                        isActive
                                          ? "bg-orange-500/10 border-orange-500/30"
                                          : "bg-transparent border-transparent hover:bg-white/[0.04] hover:border-white/10"
                                      }`}
                                    >
                                      <div className="w-8 h-8 rounded-md overflow-hidden shrink-0 bg-slate-900">
                                        <img
                                          src={channel.logo || "https://images.unsplash.com/photo-1489599849927-2ee91cede3ba?w=400&auto=format&fit=crop&q=60"}
                                          alt={channel.name}
                                          className="w-full h-full object-cover"
                                          referrerPolicy="no-referrer"
                                        />
                                      </div>
                                      <div className="flex flex-col min-w-0 flex-1">
                                        <span className={`text-xs font-semibold truncate ${isActive ? "text-orange-400" : "text-slate-200"}`}>
                                          {channel.name}
                                        </span>
                                        <span className="text-[8px] font-mono text-slate-500 flex items-center gap-1">
                                          <span className="w-1.5 h-1.5 rounded-full bg-red-500 inline-block" />
                                          LIVE
                                        </span>
                                      </div>
                                      <Play className="w-3 h-3 text-slate-500 group-hover:text-orange-400 shrink-0" />
                                    </div>
                                  );
                                })}
                              </div>
                            )}
                          </div>
                        );
                      });
                    })()}
                  </div>
                </div>
              </div>
            </motion.div>
          ) : selectedVideo ? (
            <motion.div
              initial={{ opacity: 0, y: -20 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, y: -20 }}
              transition={{ duration: 0.4 }}
              className="w-full flex flex-col gap-6"
              key={selectedVideo.id}
            >
              {/* Theater Layout Grid */}
              <div className="flex flex-col xl:flex-row gap-6 items-start">
                <div ref={vodPlayerColRef} className="w-full xl:flex-[2] flex flex-col">
                  <div className="flex items-center justify-between mb-3 text-slate-400">
                    <button 
                      onClick={() => setSelectedVideo(null)}
                      className="text-xs text-slate-400 hover:text-white flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-white/5 border border-white/5 hover:bg-white/10 transition-all font-medium focus:outline-none"
                    >
                      <X className="w-4 h-4" />
                      Close Player stage
                    </button>
                    <span className="text-xs font-mono text-orange-450">STATUS: INTERACTIVE TRANSMISSION READY</span>
                  </div>

                  {playbackError ? (
                    <SourceStatusPanel error={playbackError.error} onRetry={playbackError.retry} />
                  ) : (
                    <CustomPlayer
                      id={selectedVideo.id}
                      videoUrl={selectedVideo.videoUrl}
                      title={selectedVideo.title}
                      thumbnailUrl={selectedVideo?.thumbnailUrl}
                      durationSeconds={selectedVideo.durationSeconds}
                      onProgress={(progress, currentTime) => {
                        updateHistoryMutation.mutate({ videoId: selectedVideo.id, currentTime });
                      }}
                      onEnded={() => {
                        if (selectedVideo.type === "Series" && episodes.length > 0) {
                          playNextEpisode();
                        }
                      }}
                      savedTime={getResumeTime(selectedVideo.id)}
                      type={selectedVideo.type}
                      year={selectedVideo.year}
                      tags={selectedVideo.tags}
                      description={selectedVideo.description}
                      drmType={selectedVideo.drmType}
                      drmK={selectedVideo.drmK}
                      licenseUrl={selectedVideo.licenseUrl}
                      userAgent={selectedVideo.userAgent}
                      referer={selectedVideo.referer}
                      hasPrevEpisode={selectedVideo.type === "Series" && currentEpisode !== null ? getEpisodeNavState().hasPrev : false}
                      hasNextEpisode={selectedVideo.type === "Series" && currentEpisode !== null ? getEpisodeNavState().hasNext : false}
                      onPrevEpisode={playPrevEpisode}
                      onNextEpisode={playNextEpisode}
                      episodes={selectedVideo.type === "Series" ? episodes : undefined}
                      currentEpisode={currentEpisode}
                      selectedSeason={selectedSeason}
                      onSelectSeason={(s) => setSelectedSeason(s)}
                      onPlayEpisode={(ep) => playEpisode(ep)}
                    />
                  )}

                  {/* Movie Info + Stream Details — merged panel */}
                  <div className="mt-6 glass-panel p-5 rounded-2xl border border-white/5 flex flex-col gap-4">
                    <div className="flex items-center gap-2">
                      <span className="text-[10px] bg-orange-600/20 text-orange-300 font-mono border border-orange-500/20 px-2 py-0.5 rounded uppercase font-bold tracking-widest">{selectedVideo.category}</span>
                      <span className="text-[10px] bg-white/5 text-slate-300 font-mono px-2 py-0.5 rounded border border-white/5">{selectedVideo.year}</span>
                    </div>

                    <h2 className="font-display font-extrabold text-xl sm:text-2xl text-slate-100 tracking-wide">{selectedVideo.title}</h2>

                    <div className="flex items-center gap-4 text-xs text-slate-400 border-y border-white/5 py-3">
                      <span className="flex items-center gap-1 font-mono font-bold text-amber-300">
                        <Star className="w-4 h-4 fill-current text-amber-400" />
                        {selectedVideo.imdbRating.toFixed(1)} IMDB
                      </span>
                      <span>{selectedVideo.duration}</span>
                      <span className="bg-slate-900 border border-white/10 text-[10px] rounded px-1.5 py-0.5">{selectedVideo.rating}</span>
                    </div>

                    <p className="text-xs sm:text-sm text-slate-400 leading-relaxed font-normal max-w-3xl">
                      {selectedVideo.description}
                    </p>

                    <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 pt-2">
                      <div className="flex flex-col gap-1.5">
                        <span className="text-[10px] font-mono font-semibold text-slate-500 uppercase tracking-widest">Director</span>
                        <span className="text-xs font-semibold text-slate-300 flex items-center gap-1">
                          <Users className="w-3.5 h-3.5 text-orange-400" />
                          {selectedVideo.director}
                        </span>
                      </div>

                      <div className="flex flex-col gap-1.5">
                        <span className="text-[10px] font-mono font-semibold text-slate-500 uppercase tracking-widest">Core Cast Stars</span>
                        <div className="flex flex-wrap gap-1.5">
                          {selectedVideo.cast.map(c => (
                            <span key={c} className="text-[10px] font-sans font-medium px-2 py-0.5 rounded bg-white/5 text-slate-300 border border-white/5">
                              {c}
                            </span>
                          ))}
                        </div>
                      </div>
                    </div>

                    {/* Tags assembly */}
                    <div className="flex flex-wrap gap-1.5 mt-1">
                      {selectedVideo.tags.map(t => (
                        <span key={t} className="text-[9px] font-mono text-cyan-400">#{t}</span>
                      ))}
                    </div>

                    {/* Stream Details subsection */}
                    {(() => {
                      const sourceType = selectedVideo.drmType
                        ? "DRM"
                        : (selectedVideo.provider && /HLS|DASH|MP4/i.test(selectedVideo.provider)
                          ? selectedVideo.provider.toUpperCase()
                          : "MP4");
                      const ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36";
                      return (
                        <div className="border-t border-white/5 pt-4">
                          <div className="flex items-center gap-2 mb-3">
                            <Film className="w-4 h-4 text-orange-400" />
                            <h3 className="font-display font-bold text-xs sm:text-sm text-slate-100 tracking-wide uppercase">Stream Details</h3>
                          </div>
                          <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-7 gap-4">
                            {/* Source Type */}
                            <div className="flex flex-col gap-1">
                              <span className="text-[10px] font-mono text-slate-500 uppercase tracking-wider">Source</span>
                              <span className={`text-[10px] font-mono font-bold px-2 py-0.5 rounded border w-fit ${
                                sourceType === "DRM"
                                  ? "text-red-400 bg-red-500/10 border-red-500/20"
                                  : sourceType === "HLS"
                                    ? "text-cyan-400 bg-cyan-500/10 border-cyan-500/20"
                                    : sourceType === "DASH"
                                      ? "text-emerald-400 bg-emerald-500/10 border-emerald-500/20"
                                      : "text-slate-300 bg-white/5 border-white/10"
                              }`}>
                                {sourceType}
                              </span>
                            </div>

                            {/* Resolution */}
                            <div className="flex flex-col gap-1">
                              <span className="text-[10px] font-mono text-slate-500 uppercase tracking-wider">Resolution</span>
                              <span className="text-xs font-semibold text-slate-500">—</span>
                            </div>

                            {/* Label */}
                            <div className="flex flex-col gap-1">
                              <span className="text-[10px] font-mono text-slate-500 uppercase tracking-wider">Label</span>
                              <span className="text-xs font-semibold text-slate-200">{selectedVideo.type || "Movie"}</span>
                            </div>

                            {/* DRM */}
                            <div className="flex flex-col gap-1">
                              <span className="text-[10px] font-mono text-slate-500 uppercase tracking-wider">DRM</span>
                              <span className={`text-xs font-semibold ${selectedVideo.drmType ? "text-red-400" : "text-slate-500"}`}>
                                {selectedVideo.drmType || "None"}
                              </span>
                            </div>

                            {/* Provider */}
                            <div className="flex flex-col gap-1">
                              <span className="text-[10px] font-mono text-slate-500 uppercase tracking-wider">Provider</span>
                              <span className="text-[10px] font-mono font-bold px-2 py-0.5 rounded border text-slate-300 bg-white/5 border-white/10 w-fit">
                                {selectedVideo.provider || "Unknown"}
                              </span>
                            </div>

                            {/* User Agent */}
                            <div className="flex flex-col gap-1">
                              <span className="text-[10px] font-mono text-slate-500 uppercase tracking-wider">User Agent</span>
                              <span className="text-[9px] font-mono text-slate-400 truncate max-w-[180px]" title={ua}>
                                {ua.length > 50 ? ua.slice(0, 50) + "…" : ua}
                              </span>
                            </div>

                            {/* Referer */}
                            <div className="flex flex-col gap-1">
                              <span className="text-[10px] font-mono text-slate-500 uppercase tracking-wider">Referer</span>
                              <span className="text-[9px] font-mono text-slate-400 truncate max-w-[180px]" title="google.com">
                                google.com
                              </span>
                            </div>
                          </div>
                        </div>
                      );
                    })()}

                    <button
                      onClick={() => toggleWatchlistMutation.mutate(selectedVideo.id)}
                      className={`w-full sm:w-fit px-6 py-2.5 rounded-xl border font-sans text-xs font-bold tracking-wide transition-all flex items-center justify-center gap-2 ${
                        isVideoInWatchlist(selectedVideo.id)
                          ? "bg-orange-600/30 text-orange-300 border-orange-500/40 hover:bg-orange-600/40"
                          : "bg-white/5 text-slate-350 border-white/10 hover:bg-white/10 hover:border-white/15"
                      }`}
                    >
                      <Heart className={`w-3.5 h-3.5 ${isVideoInWatchlist(selectedVideo.id) ? "fill-current text-orange-400" : ""}`} />
                      {isVideoInWatchlist(selectedVideo.id) ? "On Watchlist" : "Assemble to Watchlist"}
                    </button>
                  </div>
                </div>

                {/* SIDE COLUMN: You Might Also Like */}
                <div className="w-full xl:flex-[1] glass-panel rounded-2xl flex flex-col border border-white/5 overflow-hidden" style={vodPlayerHeight ? { maxHeight: vodPlayerHeight } : undefined}>
                  {/* Sidebar Header */}
                  <div className="shrink-0 px-4 py-3 border-b border-white/5 bg-slate-950/40 flex items-center gap-2">
                    <Sparkles className="w-4 h-4 text-orange-400" />
                    <span className="text-xs font-bold tracking-wider text-slate-200 uppercase">You Might Also Like</span>
                    <span className="text-[9px] font-mono text-slate-500 ml-auto">
                      {videos.filter(v => v.category === selectedVideo?.category && v.id !== selectedVideo?.id).length} titles
                    </span>
                  </div>

                  {/* Scrollable movie list */}
                  <div className="flex-grow overflow-y-auto min-h-0 custom-scrollbar p-3 flex flex-col gap-2">
                    {(() => {
                      const relatedVideos = videos.filter(
                        v => v.category === selectedVideo?.category && v.id !== selectedVideo?.id
                      );
                      if (relatedVideos.length === 0) {
                        return (
                          <div className="text-center py-8 text-slate-500 text-xs">
                            No other movies in this category
                          </div>
                        );
                      }
                      return relatedVideos.map(video => {
                        const isActive = selectedVideo?.id === video.id;
                        return (
                          <div
                            key={video.id}
                            onClick={() => triggerPlayVideo(video)}
                            className={`group flex items-center gap-3 p-2.5 rounded-xl cursor-pointer transition-all duration-200 border ${
                              isActive
                                ? "bg-orange-500/10 border-orange-500/30"
                                : "bg-white/[0.03] border-white/5 hover:bg-white/[0.06] hover:border-white/10"
                            }`}
                          >
                            <div className="w-10 h-10 rounded-lg overflow-hidden shrink-0 bg-slate-900">
                              <img
                                src={video.thumbnailUrl || "https://images.unsplash.com/photo-1489599849927-2ee91cede3ba?w=400&auto=format&fit=crop&q=60"}
                                alt={video.title}
                                className="w-full h-full object-cover"
                                referrerPolicy="no-referrer"
                              />
                            </div>
                            <div className="flex flex-col min-w-0 flex-1">
                              <span className={`text-xs font-semibold truncate ${isActive ? "text-orange-400" : "text-slate-200"}`}>
                                {video.title}
                              </span>
                              <span className="text-[9px] font-mono text-slate-500 flex items-center gap-1.5">
                                <span>{video.year}</span>
                                <span className="text-slate-700">·</span>
                                <span className="text-orange-300/70">{video.category}</span>
                              </span>
                            </div>
                            <Play className="w-3.5 h-3.5 text-slate-500 group-hover:text-orange-400 shrink-0" />
                          </div>
                        );
                      });
                    })()}
                  </div>
                </div>
              </div>

              {/* EPISODE HORIZONTAL CAROUSEL for Series */}
              {selectedVideo.type === "Series" && (
                <div className="w-full mt-6">
                  <div className="flex items-center gap-2 mb-4 border-b border-white/5 pb-3">
                    <Tv className="w-4 h-4 text-orange-400" />
                    <h3 className="font-display font-semibold text-sm text-slate-100 uppercase tracking-wide">
                      Episodes
                    </h3>
                    {episodesLoading && (
                      <span className="text-[10px] font-mono text-orange-400 animate-pulse uppercase tracking-widest">
                        Loading...
                      </span>
                    )}
                    {!episodesLoading && episodes.length > 0 && (
                      <span className="text-[10px] font-mono text-slate-500">
                        ({episodes.length} episodes)
                      </span>
                    )}
                  </div>

                  {episodesLoading ? (
                    <div className="py-8 text-center">
                      <span className="text-xs font-mono text-orange-400 animate-pulse">Constructing episode lattice...</span>
                    </div>
                  ) : episodes.length === 0 ? (
                    <div className="py-8 text-center text-slate-500 text-xs">
                      No episodes configured for this series.
                    </div>
                  ) : (
                    <>
                      {/* Season pills — compact, orange-themed */}
                      {(() => {
                        const seasons = [...new Set(episodes.map(e => e.season_number))].sort();
                        return seasons.length > 1 ? (
                          <div className="flex items-center gap-1.5 mb-4 overflow-x-auto no-scrollbar">
                            {seasons.map(s => (
                              <button
                                key={s}
                                onClick={() => setSelectedSeason(s)}
                                className={`shrink-0 px-3 py-1.5 rounded-lg text-[11px] font-semibold tracking-wide border transition-all cursor-pointer ${
                                  selectedSeason === s
                                    ? "bg-orange-500/20 text-orange-300 border-orange-500/30"
                                    : "bg-white/5 border-white/5 hover:bg-white/10 text-slate-400"
                                }`}
                              >
                                Season {s}
                              </button>
                            ))}
                          </div>
                        ) : null;
                      })()}

                      {/* Horizontal episode carousel */}
                      <div className="flex gap-3 overflow-x-auto no-scrollbar pb-1 snap-x">
                        {episodes
                          .filter(e => e.season_number === selectedSeason)
                          .sort((a, b) => a.episode_number - b.episode_number)
                          .map((ep) => {
                            const isCurrent = currentEpisode?.id === ep.id;
                            return (
                              <div
                                key={ep.id}
                                onClick={() => playEpisode(ep)}
                                className={`group relative min-w-[240px] w-[240px] shrink-0 snap-start backdrop-blur-lg border rounded-xl overflow-hidden cursor-pointer transition-all duration-300 transform hover:-translate-y-1 ${
                                  isCurrent
                                    ? "bg-orange-500/10 border-orange-500/50 shadow-[0_0_15px_rgba(249,115,22,0.15)] ring-1 ring-orange-500/30"
                                    : "bg-white/[0.03] border-white/5 hover:border-white/15 hover:bg-white/[0.06]"
                                }`}
                              >
                                {/* Thumbnail area with gradient art */}
                                <div className="relative h-28 overflow-hidden bg-gradient-to-br from-slate-800 to-slate-950">
                                  <div className="absolute inset-0 flex items-center justify-center">
                                    <span className="font-display font-bold text-[9px] text-white/15 tracking-widest">
                                      EP {String(ep.episode_number).padStart(2, "0")}
                                    </span>
                                  </div>
                                  <div className="absolute inset-0 bg-gradient-to-t from-slate-950/90 via-slate-950/30 to-transparent" />

                                  {/* Now Playing badge */}
                                  {isCurrent && (
                                    <div className="absolute top-2 left-2 z-10 flex items-center gap-1 bg-orange-500/90 rounded px-1.5 py-0.5">
                                      <Play className="w-2.5 h-2.5 text-white fill-current" />
                                      <span className="text-[7px] font-mono font-bold text-white tracking-wider">NOW</span>
                                    </div>
                                  )}

                                  {/* DRM badge */}
                                  {ep.drm_type && (
                                    <div className="absolute top-2 right-2 z-10">
                                      <span className="text-[7px] font-mono text-red-400 bg-red-500/20 border border-red-500/30 px-1 py-0.5 rounded font-bold">
                                        DRM {ep.drm_type}
                                      </span>
                                    </div>
                                  )}

                                  {/* Duration badge */}
                                  {ep.duration && (
                                    <div className="absolute bottom-2 right-2 z-10">
                                      <span className="text-[7px] font-mono text-white/70 bg-black/60 px-1.5 py-0.5 rounded">
                                        {ep.duration}
                                      </span>
                                    </div>
                                  )}
                                </div>

                                {/* Card footer */}
                                <div className="p-3 flex flex-col gap-0.5">
                                  <span className={`font-mono text-[9px] font-semibold ${isCurrent ? "text-orange-400/80" : "text-slate-500"}`}>
                                    S{ep.season_number} · E{ep.episode_number}
                                  </span>
                                  <span className={`font-display font-bold text-xs truncate transition-colors ${isCurrent ? "text-orange-400" : "text-slate-200 group-hover:text-orange-400"}`}>
                                    {ep.name || `Episode ${ep.episode_number}`}
                                  </span>
                                </div>
                              </div>
                            );
                          })}
                      </div>

                      <div className="flex items-center justify-between mt-3 px-1">
                        <span className="text-[9px] font-mono text-slate-600">
                          {episodes.filter(e => e.season_number === selectedSeason).length} episodes · Season {selectedSeason}
                        </span>
                        <span className="text-[9px] font-mono text-slate-600 flex items-center gap-1">
                          <ChevronRight className="w-3 h-3 -rotate-90" />
                          Scroll horizontally
                        </span>
                      </div>
                    </>
                  )}
                </div>
              )}
            </motion.div>
          ) : libraryUnavailable ? null : (
            /* DYNAMIC PREMIUM FEATURED BANNER HERO (if player is empty) */
            <motion.div
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              exit={{ opacity: 0 }}
              transition={{ duration: 0.5 }}
              className="relative w-full rounded-3xl overflow-hidden glass-panel aspect-video sm:aspect-[16/7] xl:aspect-[21/8] shadow-2xl flex flex-col justify-end"
            >
              {/* Background Art */}
              <div className="absolute inset-0 z-0">
                <img 
                    src={featuredVideo?.thumbnailUrl}
                  alt={featuredVideo?.title}
                  className="w-full h-full object-cover brightness-[0.45] transition-transform duration-1000 scale-[1.01]"
                  referrerPolicy="no-referrer"
                />
                {/* Immersive gradient vignettes overlay */}
                <div className="absolute inset-0 bg-gradient-to-t from-slate-950 via-slate-950/25 to-transparent" />
                <div className="absolute inset-0 bg-gradient-to-r from-slate-950/90 via-slate-950/40 to-transparent" />
              </div>

              {/* Contents block of featured movie */}
              <div className="relative z-10 p-6 md:p-10 lg:p-12 max-w-2xl flex flex-col gap-4 self-start pointer-events-auto">
                <div className="flex items-center gap-2">
                  <span className="px-2.5 py-0.5 text-[9px] font-mono uppercase bg-orange-600 text-white font-bold tracking-widest rounded-md flex items-center gap-1.5 shadow-lg">
                    <span className="h-1.5 w-1.5 rounded-full bg-amber-300 animate-ping" />
                    Cinema Feature
                  </span>
                  <span className="text-[10px] text-slate-300 bg-white/5 border border-white/10 font-mono uppercase px-2 py-0.5 rounded-md">
                    {featuredVideo?.category}
                  </span>
                  <span className="text-xs text-amber-400 font-mono flex items-center gap-1 bg-amber-400/5 border border-amber-400/10 px-2 py-0.5 rounded-md font-bold">
                    <Star className="w-3.5 h-3.5 fill-current" />
                    {featuredVideo?.imdbRating.toFixed(1)} IMDB Score
                  </span>
                </div>

                <h1 className="font-display font-extrabold text-2xl sm:text-3xl md:text-4xl text-white tracking-wide leading-tight drop-shadow-md">
                  {featuredVideo?.title}
                </h1>

                <p className="text-xs sm:text-sm text-slate-300 leading-relaxed line-clamp-3 md:line-clamp-4 max-w-lg mb-2 font-normal">
                  {featuredVideo?.description}
                </p>

                {/* Direct Action buttons */}
                <div className="flex flex-wrap items-center gap-3">
                  <button 
                    onClick={() => triggerPlayVideo(featuredVideo)}
                    className="px-6 py-3 bg-orange-600 hover:bg-orange-700 bg-gradient-to-tr from-orange-600 to-amber-500 rounded-xl text-xs font-bold tracking-wider text-white shadow-lg shadow-orange-600/20 flex items-center gap-2 transition-all active:scale-98 cursor-pointer focus:outline-none"
                  >
                    <Play className="w-4 h-4 fill-current" />
                    WATCH NOW
                  </button>

                  <button 
                    onClick={() => toggleWatchlistMutation.mutate(featuredVideo?.id)}
                    className="px-5 py-3 bg-white/5 border border-white/10 hover:border-orange-500/20 hover:bg-orange-600/10 rounded-xl text-xs font-bold tracking-wide text-slate-200 hover:text-orange-400 flex items-center gap-1.5 transition-all focus:outline-none"
                  >
                    {isVideoInWatchlist(featuredVideo?.id) ? (
                      <>
                        <CircleCheck className="w-4 h-4 text-orange-400" />
                        In Watchlist
                      </>
                    ) : (
                      <>
                        <Plus className="w-4 h-4" />
                        Add to Watchlist
                      </>
                    )}
                  </button>
                </div>
              </div>
            </motion.div>
          )}
        </AnimatePresence>

        {/* COMPONENT TABBAR NAVIGATION VIEW CONTROLS */}
        <div className="flex flex-col gap-6 w-full">
          {/* Unified tab + inline filter bar — single row */}
          <div className="border-b border-white/5 pb-4 relative z-10">
            <div className="flex items-center gap-1.5 p-1 bg-slate-950/40 rounded-xl border border-white/5 overflow-x-auto no-scrollbar">
              {/* Tab buttons */}
              <button
                onClick={() => { setActiveTab("browse"); setSelectedCategory("All"); }}
                className={`shrink-0 px-3.5 md:px-4 py-2 rounded-lg text-xs font-semibold tracking-wide transition-all flex items-center gap-1.5 cursor-pointer whitespace-nowrap ${
                  activeTab === "browse" 
                    ? "bg-orange-600/25 border border-orange-500/30 text-orange-300 font-bold"
                    : "text-slate-400 hover:text-slate-200 border border-transparent"
                }`}
              >
                <Compass className="w-4 h-4 shrink-0" />
                Browse Catalog
              </button>

              <button
                onClick={() => { setActiveTab("iptv"); setSelectedIptvCategory("All"); }}
                className={`shrink-0 px-3.5 md:px-4 py-2 rounded-lg text-xs font-semibold tracking-wide transition-all flex items-center gap-1.5 cursor-pointer whitespace-nowrap ${
                  activeTab === "iptv" 
                    ? "bg-orange-600/25 border border-orange-500/30 text-orange-300 font-bold"
                    : "text-slate-400 hover:text-slate-200 border border-transparent"
                }`}
              >
                <Tv className="w-4 h-4 shrink-0" />
                Live TV
              </button>
              
              <button
                onClick={() => setActiveTab("watchlist")}
                className={`shrink-0 px-3.5 md:px-4 py-2 rounded-lg text-xs font-semibold tracking-wide transition-all relative flex items-center gap-1.5 cursor-pointer whitespace-nowrap ${
                  activeTab === "watchlist"
                    ? "bg-orange-600/25 border border-orange-500/30 text-orange-300 font-bold"
                    : "text-slate-400 hover:text-slate-200 border border-transparent"
                }`}
              >
                <Heart className="w-4 h-4 shrink-0" />
                My Watchlist
                {watchlist.length > 0 && <span className="absolute -top-0.5 -right-0.5 w-1.5 h-1.5 bg-orange-400 rounded-full" />}
              </button>
 
              <button
                onClick={() => setActiveTab("history")}
                className={`shrink-0 px-3.5 md:px-4 py-2 rounded-lg text-xs font-semibold tracking-wide transition-all flex items-center gap-1.5 cursor-pointer whitespace-nowrap ${
                  activeTab === "history"
                    ? "bg-orange-600/25 border border-orange-500/30 text-orange-300 font-bold"
                    : "text-slate-400 hover:text-slate-200 border border-transparent"
                }`}
              >
                <History className="w-4 h-4 shrink-0" />
                Watch History
              </button>

              {/* Vertical divider — only shown when pills are active */}
              {(activeTab === "browse" || activeTab === "iptv") && (
                <div className="shrink-0 w-px h-5 bg-white/10 mx-1" />
              )}

              {/* Inline genre pills for Browse tab */}
              {activeTab === "browse" && (
                <div className="flex items-center gap-1">
                  {(() => {
                    const sorted = sortByPopularity(categories, GENRE_POPULARITY);
                    const visible = sorted.slice(0, PILL_VISIBLE_COUNT);
                    const remaining = sorted.slice(PILL_VISIBLE_COUNT);
                    return (
                      <>
                        {visible.map((cat) => (
                          <button
                            key={cat}
                            onClick={() => setSelectedCategory(cat)}
                            className={`shrink-0 px-2.5 py-1 rounded-lg text-[10px] font-mono font-semibold whitespace-nowrap border transition-all cursor-pointer ${
                              selectedCategory === cat
                                ? "bg-orange-500/20 text-orange-300 border-orange-500/30"
                                : "bg-white/[0.03] border-white/5 hover:bg-white/[0.07] hover:border-white/10 text-slate-400 hover:text-slate-200"
                            }`}
                          >
                            {cat}
                          </button>
                        ))}
                        {remaining.length > 0 && (
                          <button
                            onClick={() => setShowAllCategories(prev => !prev)}
                            className={`shrink-0 px-2.5 py-1 rounded-lg text-[10px] font-mono font-semibold whitespace-nowrap border transition-all cursor-pointer flex items-center gap-0.5 ${
                              showAllCategories
                                ? "bg-orange-500/20 text-orange-300 border-orange-500/30"
                                : "bg-white/[0.03] border-white/5 hover:bg-white/[0.07] hover:border-white/10 text-slate-400 hover:text-slate-200"
                            }`}
                          >
                            +{remaining.length} more
                            <ChevronDown className={`w-3 h-3 transition-transform duration-200 ${showAllCategories ? "rotate-180" : ""}`} />
                          </button>
                        )}
                      </>
                    );
                  })()}
                </div>
              )}

              {/* Inline feed-type pills for IPTV tab */}
              {activeTab === "iptv" && (
                <div className="flex items-center gap-1">
                  {(() => {
                    const sorted = sortByPopularity(iptvCategories, CHANNEL_CATEGORY_POPULARITY);
                    const visible = sorted.slice(0, PILL_VISIBLE_COUNT);
                    const remaining = sorted.slice(PILL_VISIBLE_COUNT);
                    return (
                      <>
                        {visible.map((cat) => (
                          <button
                            key={cat}
                            onClick={() => setSelectedIptvCategory(cat)}
                            className={`shrink-0 px-2.5 py-1 rounded-lg text-[10px] font-mono font-semibold whitespace-nowrap border transition-all cursor-pointer ${
                              selectedIptvCategory === cat
                                ? "bg-orange-500/20 text-orange-300 border-orange-500/30"
                                : "bg-white/[0.03] border-white/5 hover:bg-white/[0.07] hover:border-white/10 text-slate-400 hover:text-slate-200"
                            }`}
                          >
                            {cat}
                          </button>
                        ))}
                        {remaining.length > 0 && (
                          <button
                            onClick={() => setShowAllIptvCategories(prev => !prev)}
                            className={`shrink-0 px-2.5 py-1 rounded-lg text-[10px] font-mono font-semibold whitespace-nowrap border transition-all cursor-pointer flex items-center gap-0.5 ${
                              showAllIptvCategories
                                ? "bg-orange-500/20 text-orange-300 border-orange-500/30"
                                : "bg-white/[0.03] border-white/5 hover:bg-white/[0.07] hover:border-white/10 text-slate-400 hover:text-slate-200"
                            }`}
                          >
                            +{remaining.length} more
                            <ChevronDown className={`w-3 h-3 transition-transform duration-200 ${showAllIptvCategories ? "rotate-180" : ""}`} />
                          </button>
                        )}
                      </>
                    );
                  })()}
                </div>
              )}
            </div>

            {/* Browse category accordion — expands below the tab bar row */}
            {activeTab === "browse" && (() => {
              const sorted = sortByPopularity(categories, GENRE_POPULARITY);
              const remaining = sorted.slice(PILL_VISIBLE_COUNT);
              if (remaining.length === 0) return null;
              return (
                <div
                  className="overflow-hidden transition-all duration-300 ease-out"
                  style={{ maxHeight: showAllCategories ? "500px" : "0" }}
                >
                  <div className="flex flex-wrap gap-1.5 pt-3 pb-2">
                    {remaining.map((cat) => (
                      <button
                        key={cat}
                        onClick={() => setSelectedCategory(cat)}
                        className={`shrink-0 px-2.5 py-1 rounded-lg text-[10px] font-mono font-semibold whitespace-nowrap border transition-all cursor-pointer ${
                          selectedCategory === cat
                            ? "bg-orange-500/20 text-orange-300 border-orange-500/30"
                            : "bg-white/[0.03] border-white/5 hover:bg-white/[0.07] hover:border-white/10 text-slate-400 hover:text-slate-200"
                        }`}
                      >
                        {cat}
                      </button>
                    ))}
                  </div>
                </div>
              );
            })()}

            {/* IPTV category accordion — expands below the tab bar row */}
            {activeTab === "iptv" && (() => {
              const sorted = sortByPopularity(iptvCategories, CHANNEL_CATEGORY_POPULARITY);
              const remaining = sorted.slice(PILL_VISIBLE_COUNT);
              if (remaining.length === 0) return null;
              return (
                <div
                  className="overflow-hidden transition-all duration-300 ease-out"
                  style={{ maxHeight: showAllIptvCategories ? "500px" : "0" }}
                >
                  <div className="flex flex-wrap gap-1.5 pt-3 pb-2">
                    {remaining.map((cat) => (
                      <button
                        key={cat}
                        onClick={() => setSelectedIptvCategory(cat)}
                        className={`shrink-0 px-2.5 py-1 rounded-lg text-[10px] font-mono font-semibold whitespace-nowrap border transition-all cursor-pointer ${
                          selectedIptvCategory === cat
                            ? "bg-orange-500/20 text-orange-300 border-orange-500/30"
                            : "bg-white/[0.03] border-white/5 hover:bg-white/[0.07] hover:border-white/10 text-slate-400 hover:text-slate-200"
                        }`}
                      >
                        {cat}
                      </button>
                    ))}
                  </div>
                </div>
              );
            })()}
          </div>

          {/* DYNAMIC LIST FEED BY SELECTED TABS */}
          <div className="w-full">
            {isLoading ? (
              <div className="flex flex-col gap-4 py-20 justify-center items-center w-full">
                <SpinnerOverlay />
                <span className="text-xs font-mono text-violet-400 animate-pulse uppercase tracking-widest">Constructing glass lattice pipelines...</span>
              </div>
            ) : libraryUnavailable ? (
              <SourceStatusPanel error={videosError} onRetry={() => { void refetchVideos(); }} />
            ) : (
              <AnimatePresence mode="popLayout">
                {activeTab === "browse" && (
                  <motion.div
                    key="browse-panel"
                    initial={{ opacity: 0, y: 10 }}
                    animate={{ opacity: 1, y: 0 }}
                    exit={{ opacity: 0, y: 10 }}
                    transition={{ duration: 0.3 }}
                  >
                    {/* Catalogue Title */}
                    <div className="flex items-center gap-2 mb-6">
                      <Film className="w-5 h-5 text-violet-400" />
                      <h3 className="font-display font-semibold text-lg text-slate-100 uppercase tracking-wide">
                        {selectedCategory === "All" ? "Feature Stream" : selectedCategory} Catalogue
                      </h3>
                      <span className="text-xs text-slate-500 font-mono">({videos.length} items queried)</span>
                    </div>

                    {/* Media Grid */}
                    {videos.length === 0 ? (
                      <div className="py-20 text-center glass-panel rounded-3xl border border-white/5 flex flex-col items-center justify-center gap-3">
                        <span className="text-4xl">🛸</span>
                        <h4 className="font-display text-base font-semibold text-slate-350">No Transmission Anchored</h4>
                        <p className="text-xs text-slate-500 max-w-sm">No items configured for search input "{searchQuery}" or category "{selectedCategory}" in active pipeline.</p>
                        <button 
                          onClick={() => { setSearchQuery(""); setSelectedCategory("All"); }}
                          className="mt-2 px-4 py-2 bg-gradient-to-r from-violet-600 to-indigo-600 rounded-lg text-xs font-bold text-white shadow-lg hover:from-violet-700 hover:to-indigo-700"
                        >
                          Clear Active Filters
                        </button>
                      </div>
                    ) : (
                      <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-6">
                        {videos.map((vid) => {
                          const historyItem = getVideoHistory(vid.id);
                          return (
                            <div key={vid.id} className="relative group">
                              <MediaCard
                                video={vid}
                                isWatchlisted={isVideoInWatchlist(vid.id)}
                                onSelect={() => triggerPlayVideo(vid)}
                                onToggleWatchlist={(e) => {
                                  e.stopPropagation();
                                  toggleWatchlistMutation.mutate(vid.id);
                                }}
                              />
                              {/* Resume watch indicator overlays bar on progress */}
                              {historyItem && historyItem.progress < 0.95 && (
                                <div className="absolute bottom-0 inset-x-0 h-1.5 bg-slate-950/85 rounded-b-2xl overflow-hidden z-12">
                                  <div 
                                    style={{ width: `${historyItem.progress * 100}%` }}
                                    className="h-full bg-violet-500 shadow-[0_0_10px_#8b5cf6]"
                                    title={`Resume watching ${selectedVideo?.id === vid.id ? "now" : `${Math.round(historyItem.progress * 100)}%`}`}
                                  />
                                </div>
                              )}
                            </div>
                          );
                        })}
                      </div>
                    )}
                  </motion.div>
                )}

                {activeTab === "iptv" && (
                  <motion.div
                    key="iptv-panel"
                    initial={{ opacity: 0, y: 10 }}
                    animate={{ opacity: 1, y: 0 }}
                    exit={{ opacity: 0, y: 10 }}
                    transition={{ duration: 0.3 }}
                  >
                    {/* Catalogue Title */}
                    <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2 mb-6">
                      <div className="flex items-center gap-2">
                        <Tv className="w-5 h-5 text-orange-400" />
                        <h3 className="font-display font-semibold text-base sm:text-lg text-slate-100 uppercase tracking-wide">
                          {selectedIptvCategory === "All" ? "Live Broadcast" : selectedIptvCategory} Stations
                        </h3>
                        <span className="text-xs text-slate-500 font-mono">
                          ({iptvChannels.length} channels live)
                        </span>
                      </div>
                      <span className="inline-block w-fit px-3 py-1 text-[10px] font-mono font-bold text-emerald-400 bg-emerald-500/10 border border-emerald-500/25 rounded-md animate-pulse">
                        ● ALL NETWORKS OPERATIONAL
                      </span>
                    </div>

                    {/* COLLAPSIBLE CATEGORY SECTIONS */}
                    {iptvChannels.length === 0 ? (
                      <div className="py-20 text-center glass-panel rounded-3xl border border-white/5 flex flex-col items-center justify-center gap-3">
                        <span className="text-4xl">📡</span>
                        <h4 className="font-display text-base font-semibold text-slate-350">No Channels Tuned</h4>
                        <p className="text-xs text-slate-500 max-w-sm">No live broadcast stations available in this feed type.</p>
                      </div>
                    ) : (
                      <div className="flex flex-col gap-4">
                        {/* Determine which categories to render: respect selectedIptvCategory filter */}
                        {(() => {
                          // Categories to iterate over (exclude "All"), but only those that have channels
                          const categoryOrder = IPTV_CATEGORIES.filter(c => c !== "All");
                          // Group channels by category
                          const grouped = new Map<string, BackendChannel[]>();
                          iptvChannels.forEach((ch) => {
                            const cat = ch.category || "General";
                            if (!grouped.has(cat)) grouped.set(cat, []);
                            grouped.get(cat)!.push(ch);
                          });
                          // Build ordered list: known categories first, then any extras
                          const knownCats = categoryOrder.filter(c => grouped.has(c));
                          const extraCats = [...grouped.keys()].filter(c => !categoryOrder.includes(c));
                          const orderedCats = [...knownCats, ...extraCats];

                          return orderedCats.map((cat) => {
                            const channelsInCat = grouped.get(cat) || [];
                            const isCollapsed = collapsedCategories[cat] || false;

                            return (
                              <div key={cat} className="bg-white/[0.04] backdrop-blur-md border border-white/5 rounded-xl overflow-hidden">
                                {/* Category Header */}
                                <button
                                  onClick={() => toggleCategoryCollapse(cat)}
                                  className="w-full flex items-center justify-between px-4 py-3 hover:bg-white/[0.03] transition-all duration-300 cursor-pointer focus:outline-none"
                                >
                                  <div className="flex items-center gap-3">
                                    <div className="w-7 h-7 rounded-lg bg-gradient-to-br from-orange-600/30 to-amber-500/20 flex items-center justify-center border border-white/5">
                                      <Tv className="w-3.5 h-3.5 text-orange-400" />
                                    </div>
                                    <span className="font-display font-semibold text-sm text-slate-200">{cat}</span>
                                    <span className="font-mono text-[9px] text-slate-500 bg-white/[0.03] px-1.5 py-0.5 rounded border border-white/5">
                                      {channelsInCat.length} channel{channelsInCat.length !== 1 ? "s" : ""}
                                    </span>
                                  </div>
                                  <ChevronDown className={`w-4 h-4 text-slate-500 transition-transform duration-300 ${isCollapsed ? "-rotate-90" : ""}`} />
                                </button>

                                {/* Horizontal scrolling channel row */}
                                {!isCollapsed && (
                                  <div className="border-t border-white/[0.03] px-4 py-3">
                                    <div className="flex gap-3 overflow-x-auto no-scrollbar pb-1 snap-x">
                                      {channelsInCat.map((channel) => {
                                        const isCurrentlyPlaying = selectedChannel?.id === channel.id;
                                        return (
                                          <div
                                            key={channel.id}
                                            onClick={() => triggerPlayChannel(channel)}
                                            className={`group relative min-w-[240px] w-[240px] shrink-0 snap-start bg-white/[0.03] backdrop-blur-lg border rounded-xl overflow-hidden cursor-pointer transition-all duration-300 transform hover:-translate-y-1 ${
                                              isCurrentlyPlaying
                                                ? "border-orange-500/50 shadow-[0_0_20px_rgba(249,115,22,0.15)] bg-orange-600/5"
                                                : "border-white/5 hover:border-orange-500/20"
                                            }`}
                                          >
                                            {/* Thumbnail */}
                                            <div className="relative aspect-video w-full overflow-hidden bg-slate-950">
                                              <img
                                                src={channel?.logo || "https://images.unsplash.com/photo-1489599849927-2ee91cede3ba?w=800&auto=format&fit=crop&q=80"}
                                                alt={channel.name}
                                                className="w-full h-full object-cover transition-transform duration-700 group-hover:scale-105 brightness-[0.70] group-hover:brightness-[0.9]"
                                                referrerPolicy="no-referrer"
                                              />
                                              <div className="absolute inset-x-0 bottom-0 h-16 bg-gradient-to-t from-slate-950/85 to-transparent" />

                                              {/* LIVE badge */}
                                              <div className="absolute top-2.5 left-2.5 bg-red-600/90 px-1.5 py-0.5 rounded-md text-[9px] font-mono text-white flex items-center gap-1 font-bold shadow-md z-10">
                                                <span className="w-1.5 h-1.5 rounded-full bg-white animate-ping" />
                                                <span>LIVE</span>
                                              </div>

                                              {/* Category pill */}
                                              <span className="absolute top-2.5 right-2.5 bg-orange-600/20 text-orange-300 border border-orange-500/25 px-1.5 py-0.5 rounded text-[9px] font-mono tracking-wider uppercase font-bold z-10 backdrop-blur-md">
                                                {channel.category || "General"}
                                              </span>

                                              {/* Play overlay */}
                                              <div className="absolute inset-0 bg-slate-950/15 group-hover:bg-slate-950/0 flex items-center justify-center transition-all duration-300">
                                                <span className={`w-10 h-10 rounded-full flex items-center justify-center transition-all duration-300 scale-90 opacity-0 group-hover:scale-100 group-hover:opacity-100 shadow-lg ${
                                                  isCurrentlyPlaying ? "bg-amber-400 text-slate-950" : "bg-orange-500 text-slate-100"
                                                }`}>
                                                  <Play className="w-4 h-4 fill-current ml-0.5" />
                                                </span>
                                              </div>
                                            </div>

                                            {/* Info footer */}
                                            <div className="p-3 flex flex-col gap-1">
                                              <h4 className={`text-xs font-bold tracking-wide truncate transition-colors ${isCurrentlyPlaying ? "text-orange-400" : "text-slate-100 group-hover:text-amber-400"}`}>
                                                {channel.name}
                                              </h4>
                                              <span className="text-[9px] font-mono text-slate-500 truncate">
                                                {channel.status === "active" ? "Now Playing · HLS Ready" : "Standby · Inactive"}
                                              </span>
                                            </div>
                                          </div>
                                        );
                                      })}
                                    </div>
                                  </div>
                                )}
                              </div>
                            );
                          });
                        })()}
                      </div>
                    )}
                  </motion.div>
                )}

                {activeTab === "watchlist" && (
                  <motion.div
                    key="watchlist-panel"
                    initial={{ opacity: 0, y: 10 }}
                    animate={{ opacity: 1, y: 0 }}
                    exit={{ opacity: 0, y: 10 }}
                    transition={{ duration: 0.3 }}
                  >
                    <div className="flex items-center gap-2 mb-6">
                      <Heart className="w-5 h-5 text-violet-400 fill-current" />
                      <h3 className="font-display font-semibold text-lg text-slate-100 uppercase tracking-wide">My Watchlist Catalog</h3>
                      <span className="text-xs text-slate-500 font-mono">({watchlist.length} pinned)</span>
                    </div>

                    {watchlist.length === 0 ? (
                      <div className="py-20 text-center glass-panel rounded-3xl border border-white/5 flex flex-col items-center justify-center gap-3">
                        <span className="text-4xl text-slate-500">🤍</span>
                        <h4 className="font-display text-base font-semibold text-slate-350">Vacuum Arena</h4>
                        <p className="text-xs text-slate-500 max-w-sm">You haven't accumulated any movies or series. Pinned cinematic entities accumulate here for rapid watching.</p>
                        <button 
                          onClick={() => setActiveTab("browse")}
                          className="mt-2 px-4 py-2 bg-white/5 border border-white/10 hover:bg-white/10 text-xs font-bold rounded-lg transition-all"
                        >
                          Acquire Media Cards
                        </button>
                      </div>
                    ) : (
                      <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-6">
                        {videos.filter(v => isVideoInWatchlist(v.id)).map((vid) => (
                          <div key={vid.id} className="relative group">
                            <MediaCard
                              video={vid}
                              isWatchlisted={true}
                              onSelect={() => triggerPlayVideo(vid)}
                              onToggleWatchlist={(e) => {
                                e.stopPropagation();
                                toggleWatchlistMutation.mutate(vid.id);
                              }}
                            />
                          </div>
                        ))}
                      </div>
                    )}
                  </motion.div>
                )}

                {activeTab === "history" && (
                  <motion.div
                    key="history-panel"
                    initial={{ opacity: 0, y: 10 }}
                    animate={{ opacity: 1, y: 0 }}
                    exit={{ opacity: 0, y: 10 }}
                    transition={{ duration: 0.3 }}
                  >
                    <div className="flex items-center justify-between mb-6 border-b border-white/5 pb-3">
                      <div className="flex items-center gap-2">
                        <History className="w-5 h-5 text-violet-400" />
                        <h3 className="font-display font-semibold text-lg text-slate-100 uppercase tracking-wide">Watch History Logs</h3>
                        <span className="text-xs text-slate-500 font-mono">({watchHistory.length} files scanned)</span>
                      </div>

                      {watchHistory.length > 0 && (
                        <button
                          onClick={() => {
                            localStorage.removeItem("glassstream-history");
                            queryClient.setQueryData(["watch-history"], []);
                          }}
                          className="px-3 py-1.5 bg-red-500/10 hover:bg-red-500/20 border border-red-500/20 text-red-400 text-[10px] font-mono rounded-lg transition-all"
                        >
                          Flush Logs Sync
                        </button>
                      )}
                    </div>

                    {watchHistory.length === 0 ? (
                      <div className="py-20 text-center glass-panel rounded-3xl border border-white/5 flex flex-col items-center justify-center gap-3">
                        <span className="text-4xl">⏱️</span>
                        <h4 className="font-display text-base font-semibold text-slate-350">Void History</h4>
                        <p className="text-xs text-slate-500 max-w-sm">No stream logs cached on this interface. When you stream video blocks, resume indices accumulate here.</p>
                      </div>
                    ) : (
                      <div className="flex flex-col gap-4">
                        {watchHistory.map((hist) => {
                          const vid = videos.find(v => v.id === hist.videoId);
                          if (!vid) return null;

                          return (
                            <motion.div
                              key={hist.videoId}
                              whileHover={{ x: 6, bg: "rgba(255, 255, 255, 0.05)" }}
                              className="p-4 rounded-xl glass-panel flex flex-col md:flex-row items-center justify-between gap-4 cursor-pointer"
                              onClick={() => triggerPlayVideo(vid)}
                            >
                              <div className="flex items-center gap-4 w-full md:w-auto">
                                <img
                                  src={vid?.thumbnailUrl}
                                  className="w-16 h-10 object-cover rounded-lg border border-white/10"
                                  referrerPolicy="no-referrer"
                                />
                                <div className="flex flex-col min-w-0">
                                  <h4 className="font-display font-bold text-xs sm:text-sm text-slate-100 truncate">{vid.title}</h4>
                                  <span className="text-[10px] font-mono text-slate-500">
                                    Last Watched At: {new Date(hist.watchedAt).toLocaleDateString()} at {new Date(hist.watchedAt).toLocaleTimeString([], {hour: '2-digit', minute:'2-digit'})}
                                  </span>
                                </div>
                              </div>

                              <div className="flex items-center justify-between md:justify-end gap-6 w-full md:w-auto">
                                {/* Simple visual progression index marker */}
                                <div className="flex flex-col gap-1 items-end w-32 hidden sm:flex">
                                  <span className="text-[10px] font-mono text-orange-400">{Math.round(hist.progress * 100)}% Complete</span>
                                  <div className="w-full h-1 bg-white/10 rounded-full overflow-hidden">
                                    <div 
                                      style={{ width: `${hist.progress * 100}%` }}
                                      className="h-full bg-orange-400 rounded-full"
                                    />
                                  </div>
                                </div>

                                <button 
                                  className="px-4 py-1.5 bg-orange-600 hover:bg-orange-700 bg-gradient-to-tr from-orange-600 to-amber-500 text-[10px] font-bold text-white rounded-lg flex items-center gap-1 cursor-pointer transition-all focus:outline-none"
                                >
                                  <Play className="w-3 h-3 fill-current" />
                                  {hist.progress >= 0.95 ? "REPLAY" : "RESUME"}
                                </button>
                              </div>
                            </motion.div>
                          );
                        })}
                      </div>
                    )}
                  </motion.div>
                )}
              </AnimatePresence>
            )}
          </div>
        </div>

        {/* EXTRA DECORATIVE FOOTER: BRIEF INFO CARDS */}
        <div className="mt-8 grid grid-cols-1 md:grid-cols-3 gap-6">
          <div className="p-5 glass-panel rounded-2xl flex items-start gap-4">
            <div className="p-3 bg-violet-600/10 border border-violet-500/20 rounded-xl text-violet-400">
              <Film className="w-5 h-5" />
            </div>
            <div className="flex flex-col gap-0.5">
              <h5 className="font-display font-bold text-xs text-slate-100">Optimized Codec Streams</h5>
              <p className="text-[11px] text-slate-400 leading-relaxed font-normal">Our direct-to-browser player queries premium CORS-compliant MP4 assets directly from unified CDN caches.</p>
            </div>
          </div>

          <div className="p-5 glass-panel rounded-2xl flex items-start gap-4">
            <div className="p-3 bg-cyan-600/10 border border-cyan-500/20 rounded-xl text-cyan-400">
              <BookOpen className="w-5 h-5" />
            </div>
            <div className="flex flex-col gap-0.5">
              <h5 className="font-display font-bold text-xs text-slate-100">Decentral Watch Timeline</h5>
              <p className="text-[11px] text-slate-400 leading-relaxed font-normal">Never lose tracking state. Local storage caches keep logs synchronised across browser tabs securely.</p>
            </div>
          </div>

          <div className="p-5 glass-panel rounded-2xl flex items-start gap-4">
            <div className="p-3 bg-pink-600/10 border border-pink-500/20 rounded-xl text-pink-400">
              <Users className="w-5 h-5" />
            </div>
            <div className="flex flex-col gap-0.5">
              <h5 className="font-display font-bold text-xs text-slate-100">Live Reaction Synapses</h5>
              <p className="text-[11px] text-slate-400 leading-relaxed font-normal">Read or broadcast your custom movie reactions. Reviews mutate and update cleanly using React Query.</p>
            </div>
          </div>
        </div>

      </main>
    </div>
  );
}

// Simple micro spinner
function SpinnerOverlay() {
  return (
    <div className="relative flex items-center justify-center w-12 h-12">
      <div className="absolute inset-0 border-2 border-slate-700 rounded-full" />
      <div className="absolute inset-0 border-2 border-t-violet-500 border-r-violet-500 rounded-full animate-spin" />
      <div className="w-2 h-2 rounded-full bg-violet-400 animate-ping" />
    </div>
  );
}
