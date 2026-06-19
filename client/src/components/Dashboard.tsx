/**
 * @license
 * SPDX-License-Identifier: Apache-2.0
 */

import React, { useState, useEffect } from "react";
import { Video, WatchlistState, WatchHistoryItem } from "../types";
import { CATEGORIES as FALLBACK_CATEGORIES } from "../data/videos";
import { fetchVideos, fetchCategories, resolveStream } from "../api/client";
import { resolveEpisode, fetchEpisodes } from "../api/client";
import { Episode } from "../types";
import { IPTV_CHANNELS, IPTV_CATEGORIES, IPTVChannel } from "../data/iptv";
import Header from "./Header";
import MediaCard from "./MediaCard";
import CustomPlayer from "./CustomPlayer";
import ReviewsSection from "./ReviewsSection";
import GlowBackground from "./GlowBackground";
import { 
  Play, Plus, Heart, Compass, History, Star, Info, X, 
  ChevronRight, CircleCheck, Film, ListFilter, Users, BookOpen,
  Tv, Send, MessageSquare, ShieldAlert, Sparkles, AlertCircle
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
  const [selectedChannel, setSelectedChannel] = useState<IPTVChannel | null>(null);
  
  // Live Chat Simulation State
  const [chatMessages, setChatMessages] = useState<{ id: string; user: string; text: string; time: string }[]>([
    { id: "c1", user: "CyberGlitch", text: "Wow, is this live in 1080p? Resolution is super crisp!", time: "10:04 AM" },
    { id: "c2", user: "GridSlinger20", text: "The audio ambient score is beautiful.", time: "10:04 AM" },
    { id: "c3", user: "VaporHeart", text: "Amsterdam futuristic setting is elite. Love Ian's CGI style.", time: "10:05 AM" },
  ]);
  const [userChatMessage, setUserChatMessage] = useState("");
  
  // Navigation tabs: 'browse' (VOD Catalog) or 'iptv' (Live IPTV) or 'watchlist' or 'history'
  const [activeTab, setActiveTab] = useState<"browse" | "iptv" | "watchlist" | "history">("browse");

  // Selected Movie for active cinematic playback details
  const [selectedVideo, setSelectedVideo] = useState<Video | null>(null);

  // Series episode state
  const [episodes, setEpisodes] = useState<Episode[]>([]);
  const [currentEpisode, setCurrentEpisode] = useState<Episode | null>(null);
  const [selectedSeason, setSelectedSeason] = useState<number>(1);
  const [episodesLoading, setEpisodesLoading] = useState(false);

  // Core Queries using TanStack Query

  // 1. Fetching catalogue from AdoboTV PostgreSQL backend
  const { data: videos = [], isLoading } = useQuery<Video[]>({
    queryKey: ["videos", searchQuery, selectedCategory],
    queryFn: () => fetchVideos({ search: searchQuery, category: selectedCategory }),
    staleTime: 5 * 60 * 1000,
  });

  // 1b. Fetch genre categories from backend
  const { data: categories = FALLBACK_CATEGORIES } = useQuery<string[]>({
    queryKey: ["categories"],
    queryFn: fetchCategories,
    staleTime: 30 * 60 * 1000,
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
    try {
      const resolved = await resolveStream(video.id);
      setSelectedVideo({ ...video, videoUrl: resolved.url, drmType: resolved.drm_type, drmK: resolved.drm_k, licenseUrl: resolved.license_url, provider: resolved.provider });
      
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
    } catch {
      setSelectedVideo(video);
    }
    window.scrollTo({ top: 0, behavior: "smooth" });
  };

  // Play a specific episode
  const playEpisode = async (episode: Episode) => {
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
      } : null);
    } catch (e) {
      console.error("Failed to resolve episode:", e);
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
  const triggerPlayChannel = (channel: IPTVChannel) => {
    setSelectedVideo(null);
    setSelectedChannel(channel);
    setChatMessages([
      { id: "c1", user: "CyberGlitch", text: `Welcome to ${channel.title}! Stream has initiated successfully.`, time: "Live" },
      { id: "c2", user: "GridSlinger20", text: "Quality is amazing. No macroblocking on dark gradients.", time: "Live" },
      { id: "c3", user: "VaporHeart", text: "Greetings voyagers from Seattle! This channel looks perfect.", time: "Live" }
    ]);
    window.scrollTo({ top: 0, behavior: "smooth" });
  };

  // Simulating Live IPTV incoming peer messages
  useEffect(() => {
    if (!selectedChannel) return;

    const botUsers = ["AlphaDriver", "ZeroLatency_Cool", "GlitchShifter", "SpecterX", "ApexVoyager", "MatrixRebel", "ChronoNerd", "DuneWalker"];
    const quotes = [
      "No buffering at all on high bitrate stream! Amazing glass design.",
      "Is that scene custom rendered or from a physical set?",
      "Wait, the lighting colors are breathtaking.",
      "Just tuned in, what program is next after this?",
      "EPG schedule is very useful, glad I can see what is playing later.",
      "That live tracker looks clean. Real cyberpunk dashboard vibes.",
      "Where can I buy the soundtrack score for this?",
      "Highly responsive player! Awesome work.",
      "This ambient loop is so therapeutic, I am leaving this on all night.",
      "Greetings from Neo-Tokyo!"
    ];

    const interval = setInterval(() => {
      const randomUser = botUsers[Math.floor(Math.random() * botUsers.length)];
      const randomQuote = quotes[Math.floor(Math.random() * quotes.length)];
      const now = new Date();
      const timeString = now.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });

      setChatMessages(prev => [
        ...prev,
        {
          id: Math.random().toString(),
          user: randomUser,
          text: randomQuote,
          time: timeString
        }
      ].slice(-30)); // limit length
    }, 4500);

    return () => clearInterval(interval);
  }, [selectedChannel]);

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
        }}
      />

      <main className="max-w-7xl mx-auto px-4 md:px-8 py-6 flex-grow flex flex-col gap-8 w-full">
        
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
                <div className="w-full lg:flex-grow">
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

                  <CustomPlayer 
                    id={selectedChannel.id}
                    videoUrl={selectedChannel.videoUrl}
                    title={selectedChannel.title}
                    thumbnailUrl={selectedChannel?.thumbnailUrl}
                    durationSeconds={0}
                    isLive={true}
                  />

                  {/* Channel Description Panel beneath player */}
                  <div className="mt-4 glass-panel p-5 rounded-2xl flex flex-col sm:flex-row justify-between items-start sm:items-center gap-4 border border-white/5">
                    <div className="flex flex-col gap-1.5">
                      <div className="flex items-center gap-2">
                        <h2 className="font-display font-extrabold text-lg sm:text-xl text-slate-105 tracking-wide">{selectedChannel.title}</h2>
                        <span className="text-[10px] uppercase font-bold tracking-widest bg-orange-600/20 text-orange-300 border border-orange-500/20 px-2 py-0.5 rounded">{selectedChannel.category}</span>
                      </div>
                      <p className="text-xs text-slate-400 max-w-2xl font-light leading-relaxed">{selectedChannel.description}</p>
                    </div>
                    <div className="p-3 bg-white/5 rounded-xl border border-white/5 flex flex-col items-end shrink-0 w-full sm:w-auto">
                      <span className="text-[10px] font-mono text-slate-500 tracking-wider uppercase">Active Viewers</span>
                      <span className="text-xs sm:text-sm font-display font-bold text-orange-400 flex items-center gap-1">
                        <Users className="w-3.5 h-3.5" />
                        {selectedChannel.viewers}
                      </span>
                    </div>
                  </div>
                </div>

                {/* IPTV SIDEBAR: Chat Pane */}
                <div className="w-full lg:w-80 xl:w-96 glass-panel rounded-2xl self-stretch flex flex-col border border-white/5 overflow-hidden h-[380px] lg:h-auto min-h-[380px]">
                  {/* Sidebar Header Tabs */}
                  <div className="flex border-b border-white/5 bg-slate-950/40 shrink-0">
                    <div className="flex-1 py-3 text-center text-xs font-bold tracking-wider text-slate-200 border-r border-white/5 bg-gradient-to-t from-orange-950/20 to-transparent flex items-center justify-center gap-1.5">
                      <MessageSquare className="w-4 h-4 text-orange-400" />
                      LIVE STREAM CHAT
                    </div>
                  </div>

                  {/* Scrolling Chat container */}
                  <div className="flex-grow p-4 overflow-y-auto flex flex-col gap-3 min-h-0 custom-scrollbar max-h-[260px] lg:max-h-[340px]">
                    {chatMessages.map((msg) => (
                      <div key={msg.id} className="text-xs flex flex-col gap-1 bg-white/5 p-2 rounded-xl border border-white/5 hover:border-white/10 transition-all">
                        <div className="flex items-center justify-between">
                          <span className="font-bold text-orange-300 font-mono text-[10px] sm:text-[11px] hover:text-orange-400 transition-colors flex items-center gap-1">
                            <span className="w-1.5 h-1.5 rounded-full bg-orange-500/40" />
                            {msg.user}
                          </span>
                          <span className="text-[9px] font-mono text-slate-500">{msg.time}</span>
                        </div>
                        <p className="text-slate-350 font-normal leading-relaxed text-[11px]">{msg.text}</p>
                      </div>
                    ))}
                  </div>

                  {/* Chat input form */}
                  <form 
                    onSubmit={(e) => {
                      e.preventDefault();
                      if (!userChatMessage.trim()) return;
                      const now = new Date();
                      const timeString = now.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
                      setChatMessages(prev => [
                        ...prev,
                        {
                          id: Math.random().toString(),
                          user: "AetherVoyager (You)",
                          text: userChatMessage.trim(),
                          time: timeString
                        }
                      ]);
                      setUserChatMessage("");
                    }}
                    className="p-3 border-t border-white/5 bg-slate-950/50 flex gap-2 shrink-0"
                  >
                    <input 
                      type="text"
                      placeholder="Send live reaction..."
                      value={userChatMessage}
                      onChange={(e) => setUserChatMessage(e.target.value)}
                      className="flex-grow bg-[#050505] text-xs text-slate-250 placeholder-slate-500 px-3 py-2 rounded-xl border border-white/15 focus:outline-none focus:border-orange-500/50 focus:ring-1 focus:ring-orange-500/20 font-sans"
                    />
                    <button 
                      type="submit"
                      className="p-2 bg-orange-600 hover:bg-orange-700 text-white rounded-xl transition-all shadow-md active:scale-95 flex items-center justify-center focus:outline-none cursor-pointer"
                    >
                      <Send className="w-3.5 h-3.5" />
                    </button>
                  </form>
                </div>
              </div>

              {/* ELECTRONIC PROGRAM GUIDE (EPG) GRID ROW (Highly visual, responsive and polished) */}
              <div className="glass-panel p-6 rounded-2xl border border-white/5 flex flex-col gap-4">
                <div className="flex items-center justify-between border-b border-white/5 pb-3">
                  <div className="flex items-center gap-2">
                    <Tv className="w-4.5 h-4.5 text-orange-400" />
                    <h3 className="font-display font-bold text-xs sm:text-sm text-slate-100 tracking-wide uppercase">Broadcast Schedule (EPG Guide)</h3>
                  </div>
                  <span className="text-[10px] font-mono text-slate-500 uppercase hidden sm:inline">GMT-07:00 timezone active</span>
                </div>

                <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-4 gap-4">
                  {selectedChannel.epg.map((item, idx) => (
                    <div 
                      key={idx}
                      className={`p-4 rounded-xl border transition-all flex flex-col gap-1.5 relative overflow-hidden ${
                        item.active 
                          ? "bg-orange-600/10 border-orange-500/40 shadow-inner" 
                          : "bg-slate-950/20 border-white/5 hover:border-white/10"
                      }`}
                    >
                      {item.active && (
                        <div className="absolute top-0 right-0 bg-red-600 text-white text-[8px] font-mono font-bold px-2 py-0.5 rounded-bl uppercase tracking-wider animate-pulse">
                          Active Now
                        </div>
                      )}
                      <span className="text-[10px] font-mono text-slate-400 font-semibold">{item.time}</span>
                      <h4 className={`text-xs font-semibold ${item.active ? "text-orange-300" : "text-slate-250"}`}>{item.program}</h4>
                      <p className="text-[11px] text-slate-500 line-clamp-1 font-normal">
                        {item.active ? selectedChannel.nowPlayingDescription : "Incoming transmission blocks..."}
                      </p>
                    </div>
                  ))}
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
                <div className="w-full xl:flex-grow">
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
                    userAgent={"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"}
                    referer={"https://www.google.com"}
                    hasPrevEpisode={selectedVideo.type === "Series" && currentEpisode !== null ? getEpisodeNavState().hasPrev : false}
                    hasNextEpisode={selectedVideo.type === "Series" && currentEpisode !== null ? getEpisodeNavState().hasNext : false}
                    onPrevEpisode={playPrevEpisode}
                    onNextEpisode={playNextEpisode}
                  />
                </div>

                {/* SIDE COLUMN: Information & cast info */}
                <div className="w-full xl:w-80 glass-panel rounded-2xl p-6 self-stretch flex flex-col justify-between gap-6">
                  <div className="flex flex-col gap-4">
                    <div className="flex items-center gap-2">
                      <span className="text-[10px] bg-orange-600/20 text-orange-300 font-mono border border-orange-500/20 px-2 py-0.5 rounded uppercase font-bold tracking-widest">{selectedVideo.category}</span>
                      <span className="text-[10px] bg-white/5 text-slate-300 font-mono px-2 py-0.5 rounded border border-white/5">{selectedVideo.year}</span>
                    </div>

                    <h2 className="font-display font-extrabold text-xl text-slate-100 tracking-wide">{selectedVideo.title}</h2>
                    
                    <div className="flex items-center gap-4 text-xs text-slate-400 border-y border-white/5 py-3">
                      <span className="flex items-center gap-1 font-mono font-bold text-amber-300">
                        <Star className="w-4 h-4 fill-current text-amber-400" />
                        {selectedVideo.imdbRating.toFixed(1)} IMDB
                      </span>
                      <span>{selectedVideo.duration}</span>
                      <span className="bg-slate-900 border border-white/10 text-[10px] rounded px-1.5 py-0.5">{selectedVideo.rating}</span>
                    </div>

                    <p className="text-xs text-slate-400 leading-relaxed font-normal">
                      {selectedVideo.description}
                    </p>
                  </div>

                  <div className="flex flex-col gap-4 pt-4 border-t border-white/5">
                    <dt className="flex flex-col gap-1.5">
                      <span className="text-[10px] font-mono font-semibold text-slate-500 uppercase tracking-widest">Director</span>
                      <span className="text-xs font-semibold text-slate-300 flex items-center gap-1">
                        <Users className="w-3.5 h-3.5 text-orange-400" />
                        {selectedVideo.director}
                      </span>
                    </dt>

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

                    {/* Tags assembly */}
                    <div className="flex flex-wrap gap-1 mt-1">
                      {selectedVideo.tags.map(t => (
                        <span key={t} className="text-[9px] font-mono text-cyan-400">#{t}</span>
                      ))}
                    </div>

                    <button 
                      onClick={() => toggleWatchlistMutation.mutate(selectedVideo.id)}
                      className={`w-full py-2.5 rounded-xl border font-sans text-xs font-bold tracking-wide transition-all flex items-center justify-center gap-2 ${
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
              </div>

              {/* Dynamic Review segment targeting specific videoroot cache */}
              <ReviewsSection videoId={selectedVideo.id} />

              {/* EPISODE LIST PANEL for Series */}
              {selectedVideo.type === "Series" && (
                <div className="w-full mt-6">
                  <div className="flex items-center gap-2 mb-4 border-b border-white/5 pb-3">
                    <Tv className="w-4 h-4 text-purple-400" />
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
                      {/* Season Tabs */}
                      {(() => {
                        const seasons = [...new Set(episodes.map(e => e.season_number))].sort();
                        return seasons.length > 1 ? (
                          <div className="flex items-center gap-2 mb-4">
                            {seasons.map(s => (
                              <button
                                key={s}
                                onClick={() => setSelectedSeason(s)}
                                className={`px-3 py-1.5 rounded-xl text-xs font-semibold tracking-wide border transition-all ${
                                  selectedSeason === s
                                    ? "bg-purple-500/20 text-purple-300 border-purple-500/35"
                                    : "bg-white/5 border-white/5 hover:bg-white/10 text-slate-400"
                                }`}
                              >
                                Season {s}
                              </button>
                            ))}
                          </div>
                        ) : null;
                      })()}

                      {/* Episode Cards */}
                      <div className="flex flex-col gap-2">
                        {episodes
                          .filter(e => e.season_number === selectedSeason)
                          .sort((a, b) => a.episode_number - b.episode_number)
                          .map((ep) => {
                            const isCurrent = currentEpisode?.id === ep.id;
                            return (
                              <div
                                key={ep.id}
                                onClick={() => playEpisode(ep)}
                                className={`flex items-center gap-4 p-3 rounded-xl cursor-pointer transition-all ${
                                  isCurrent
                                    ? "bg-purple-500/15 border border-purple-500/30"
                                    : "glass-panel border border-white/5 hover:border-white/15 hover:bg-white/5"
                                }`}
                              >
                                <div className="flex items-center justify-center w-8 h-8 shrink-0 rounded-lg bg-slate-800/80 text-xs font-mono font-bold text-slate-300">
                                  {isCurrent ? (
                                    <Play className="w-3.5 h-3.5 text-purple-400 fill-current" />
                                  ) : (
                                    ep.episode_number
                                  )}
                                </div>
                                <div className="flex flex-col min-w-0 flex-grow">
                                  <span className={`text-xs font-semibold truncate ${
                                    isCurrent ? "text-purple-300" : "text-slate-200"
                                  }`}>
                                    {ep.name || `Episode ${ep.episode_number}`}
                                  </span>
                                  <div className="flex items-center gap-2 mt-0.5">
                                    <span className="text-[9px] font-mono text-slate-500">
                                      S{ep.season_number} E{ep.episode_number}
                                    </span>
                                    {ep.drm_type && (
                                      <span className="text-[8px] font-mono text-red-400 bg-red-500/10 px-1.5 py-0.5 rounded border border-red-500/20">
                                        DRM {ep.drm_type}
                                      </span>
                                    )}
                                    {isCurrent && (
                                      <span className="text-[8px] font-mono text-purple-400 animate-pulse">
                                        ▶ NOW PLAYING
                                      </span>
                                    )}
                                  </div>
                                </div>
                              </div>
                            );
                          })}
                      </div>
                    </>
                  )}
                </div>
              )}
            </motion.div>
          ) : (
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
          {/* Header tabs row */}
          <div className="flex flex-col md:flex-row md:items-center justify-between gap-4 border-b border-white/5 pb-4">
            
            {/* View selectors */}
            <div className="flex flex-wrap items-center gap-1.5 p-1 bg-slate-950/40 rounded-xl border border-white/5 w-fit">
              <button
                onClick={() => { setActiveTab("browse"); setSelectedCategory("All"); }}
                className={`px-3.5 md:px-4 py-2 rounded-lg text-xs font-semibold tracking-wide transition-all flex items-center gap-1.5 cursor-pointer ${
                  activeTab === "browse" 
                    ? "bg-orange-600/25 border border-orange-500/30 text-orange-300 font-bold"
                    : "text-slate-405 hover:text-slate-200 border border-transparent"
                }`}
              >
                <Compass className="w-4 h-4" />
                Browse Catalog
              </button>

              <button
                onClick={() => { setActiveTab("iptv"); setSelectedIptvCategory("All"); }}
                className={`px-3.5 md:px-4 py-2 rounded-lg text-xs font-semibold tracking-wide transition-all flex items-center gap-1.5 cursor-pointer ${
                  activeTab === "iptv" 
                    ? "bg-orange-600/25 border border-orange-500/30 text-orange-300 font-bold"
                    : "text-slate-405 hover:text-slate-200 border border-transparent"
                }`}
              >
                <Tv className="w-4 h-4" />
                Live IPTV
              </button>
              
              <button
                onClick={() => setActiveTab("watchlist")}
                className={`px-3.5 md:px-4 py-2 rounded-lg text-xs font-semibold tracking-wide transition-all relative flex items-center gap-1.5 cursor-pointer ${
                  activeTab === "watchlist"
                    ? "bg-orange-600/25 border border-orange-500/30 text-orange-300 font-bold"
                    : "text-slate-405 hover:text-slate-200 border border-transparent"
                }`}
              >
                <Heart className="w-4 h-4" />
                My Watchlist
                {watchlist.length > 0 && <span className="absolute -top-0.5 -right-0.5 w-1.5 h-1.5 bg-orange-400 rounded-full" />}
              </button>
 
              <button
                onClick={() => setActiveTab("history")}
                className={`px-3.5 md:px-4 py-2 rounded-lg text-xs font-semibold tracking-wide transition-all flex items-center gap-1.5 cursor-pointer ${
                  activeTab === "history"
                    ? "bg-orange-600/25 border border-orange-500/30 text-orange-300 font-bold"
                    : "text-slate-405 hover:text-slate-200 border border-transparent"
                }`}
              >
                <History className="w-4 h-4" />
                Watch History
              </button>
            </div>

            {/* SEAMLESS GLASS CATEGORY FILTER BAR */}
            {activeTab === "browse" && (
              <div className="flex items-center gap-1.5 overflow-x-auto no-scrollbar py-0.5 max-w-full">
                <span className="text-[10px] font-mono text-slate-500 uppercase tracking-widest mr-2 flex items-center gap-1">
                  <ListFilter className="w-3.5 h-3.5" />
                  Filter
                </span>
                {categories.map((cat) => (
                  <button
                    key={cat}
                    onClick={() => setSelectedCategory(cat)}
                    className={`px-3 py-1.5 rounded-xl text-xs font-semibold tracking-wide whitespace-nowrap border transition-all cursor-pointer ${
                      selectedCategory === cat
                        ? "bg-orange-500/20 text-orange-300 border-orange-500/35 font-semibold"
                        : "bg-white/5 border-white/5 hover:bg-white/10 hover:border-white/10 text-slate-400 hover:text-slate-200"
                    }`}
                  >
                    {cat}
                  </button>
                ))}
              </div>
            )}

            {activeTab === "iptv" && (
              <div className="flex items-center gap-1.5 overflow-x-auto no-scrollbar py-0.5 max-w-full">
                <span className="text-[10px] font-mono text-slate-500 uppercase tracking-widest mr-2 flex items-center gap-1">
                  <ListFilter className="w-3.5 h-3.5" />
                  Feed Type
                </span>
                {IPTV_CATEGORIES.map((cat) => (
                  <button
                    key={cat}
                    onClick={() => setSelectedIptvCategory(cat)}
                    className={`px-3 py-1.5 rounded-xl text-xs font-semibold tracking-wide whitespace-nowrap border transition-all cursor-pointer ${
                      selectedIptvCategory === cat
                        ? "bg-orange-500/20 text-orange-300 border-orange-500/35 font-semibold"
                        : "bg-white/5 border-white/5 hover:bg-white/10 hover:border-white/10 text-slate-400 hover:text-slate-200"
                    }`}
                  >
                    {cat}
                  </button>
                ))}
              </div>
            )}
          </div>

          {/* DYNAMIC LIST FEED BY SELECTED TABS */}
          <div className="w-full">
            {isLoading ? (
              <div className="flex flex-col gap-4 py-20 justify-center items-center w-full">
                <SpinnerOverlay />
                <span className="text-xs font-mono text-violet-400 animate-pulse uppercase tracking-widest">Constructing glass lattice pipelines...</span>
              </div>
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
                          ({IPTV_CHANNELS.filter(ch => selectedIptvCategory === "All" || ch.category === selectedIptvCategory).length} channels live)
                        </span>
                      </div>
                      <span className="inline-block w-fit px-3 py-1 text-[10px] font-mono font-bold text-emerald-400 bg-emerald-500/10 border border-emerald-500/25 rounded-md animate-pulse">
                        ● ALL NETWORKS OPERATIONAL
                      </span>
                    </div>

                    {/* LIVE CHANNELS GRID */}
                    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-6">
                      {IPTV_CHANNELS.filter(ch => selectedIptvCategory === "All" || ch.category === selectedIptvCategory).map((channel) => {
                        const isCurrentlyPlaying = selectedChannel?.id === channel.id;
                        return (
                          <div 
                            key={channel.id}
                            onClick={() => triggerPlayChannel(channel)}
                            className={`group relative glass-panel rounded-2xl overflow-hidden border transition-all duration-300 transform hover:-translate-y-1 hover:shadow-2xl cursor-pointer flex flex-col justify-between ${
                              isCurrentlyPlaying 
                                ? "border-orange-500/50 shadow-[0_0_20px_rgba(249,115,22,0.15)] bg-orange-600/5" 
                                : "border-white/5 hover:border-white/10 hover:bg-white/5"
                            }`}
                          >
                            {/* Thumbnail / Cover Art with overlay state */}
                            <div className="relative aspect-video w-full overflow-hidden bg-slate-950">
                              <img 
                                src={channel?.thumbnailUrl} 
                                alt={channel.title} 
                                className="w-full h-full object-cover transition-transform duration-750 group-hover:scale-105 brightness-[0.70] group-hover:brightness-[0.9]"
                                referrerPolicy="no-referrer"
                              />

                              {/* VIGNETTE GRADIENTS */}
                              <div className="absolute inset-x-0 bottom-0 h-16 bg-gradient-to-t from-slate-950/80 to-transparent" />

                              {/* Live spectator count */}
                              <div className="absolute top-3 left-3 bg-[#0c0c0c]/85 border border-white/10 px-2 py-1 rounded-md text-[9px] font-mono text-slate-200 flex items-center gap-1.5 font-bold shadow-md z-10 backdrop-blur-md">
                                <span className="w-1.5 h-1.5 rounded-full bg-red-600 animate-ping" />
                                <span>LIVE</span>
                                <span className="text-slate-500">|</span>
                                <span className="text-orange-400 flex items-center gap-0.5">
                                  <Users className="w-2.5 h-2.5 text-orange-400 inline" />
                                  {channel.viewers}
                                </span>
                              </div>

                              {/* Category pill */}
                              <span className="absolute top-3 right-3 bg-orange-600/20 text-orange-300 border border-orange-500/25 px-2 py-0.5 rounded text-[9px] font-mono tracking-wider uppercase font-extrabold z-10 shadow-md">
                                {channel.category}
                              </span>

                              {/* Play Screen Overlay HUD */}
                              <div className="absolute inset-0 bg-slate-950/15 group-hover:bg-slate-950/0 flex items-center justify-center transition-all duration-300">
                                <span className={`w-11 h-11 sm:w-12 sm:h-12 rounded-full flex items-center justify-center transition-all duration-300 scale-90 opacity-0 group-hover:scale-100 group-hover:opacity-100 ${
                                  isCurrentlyPlaying ? "bg-amber-400 text-slate-950" : "bg-orange-500 text-slate-100"
                                } shadow-lg`}>
                                  <Play className="w-5 h-5 fill-current ml-0.5" />
                                </span>
                              </div>
                            </div>

                            {/* Info card footer */}
                            <div className="p-4 flex flex-col gap-2 relative bg-slate-955/20 flex-grow justify-between">
                              <div className="flex flex-col gap-1">
                                <div className="flex items-center justify-between">
                                  <span className="text-[9.5px] font-mono text-slate-500 font-semibold tracking-wide uppercase">Broadcast Station</span>
                                  <span className="text-[9px] font-mono text-emerald-400 border border-emerald-500/10 bg-emerald-500/5 px-1.5 py-0.5 rounded">High Bitrate</span>
                                </div>
                                <h4 className={`text-sm font-bold tracking-wide transition-colors ${isCurrentlyPlaying ? "text-orange-400 font-semibold" : "text-slate-100 group-hover:text-amber-400"}`}>
                                  {channel.title}
                                </h4>
                                <p className="text-xs text-slate-400 line-clamp-2 leading-relaxed mt-1 font-light">
                                  {channel.description}
                                </p>
                              </div>

                              {/* Live program progress slider placeholder/simulation */}
                              <div className="border-t border-white/5 pt-3 mt-2 flex flex-col gap-2">
                                <div className="flex items-center justify-between text-[10px] font-mono">
                                  <span className="text-orange-355 font-medium uppercase tracking-wider">NOW BROADCASTING:</span>
                                  <span className="text-[10px] text-slate-400 bg-white/5 border border-white/5 px-2 py-0.5 rounded">HLS Multi-Bitrate</span>
                                </div>
                                <div className="text-xs font-semibold text-slate-200 flex items-center gap-1.5">
                                  <Sparkles className="w-3.5 h-3.5 text-amber-400 animate-pulse" />
                                  <span className="line-clamp-1">{channel.epg.find(e => e.active)?.program || "Regular Network Feed"}</span>
                                </div>
                              </div>
                            </div>
                          </div>
                        );
                      })}
                    </div>
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
