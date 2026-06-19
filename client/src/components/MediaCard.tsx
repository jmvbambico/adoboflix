/**
 * @license
 * SPDX-License-Identifier: Apache-2.0
 */

import React from "react";
import { Video } from "../types";
import { Play, Star, Plus, Check, Eye, Heart } from "lucide-react";
import { motion } from "motion/react";

interface MediaCardProps {
  video: Video;
  isWatchlisted: boolean;
  onSelect: () => void;
  onToggleWatchlist: (e: React.MouseEvent) => void;
}

export default function MediaCard({
  video,
  isWatchlisted,
  onSelect,
  onToggleWatchlist
}: MediaCardProps) {
  // Safe format views
  const formatViews = (views: number) => {
    if (views >= 1000000) {
      return `${(views / 1000000).toFixed(1)}M`;
    }
    if (views >= 1000) {
      return `${(views / 1000).toFixed(0)}K`;
    }
    return views.toString();
  };

  return (
    <motion.div
      layoutId={`card-${video.id}`}
      onClick={onSelect}
      className="group relative flex flex-col rounded-2xl overflow-hidden glass-card cursor-pointer select-none aspect-video sm:aspect-square md:aspect-[3/4]"
      whileHover={{ y: -8, scale: 1.015 }}
      transition={{ duration: 0.35, ease: "easeOut" }}
    >
      {/* 1. Base Wallpaper Image */}
      <div className="absolute inset-0 w-full h-full overflow-hidden z-0">
        <img
          src={video.thumbnailUrl}
          alt={video.title}
          className="w-full h-full object-cover transition-transform duration-700 ease-out group-hover:scale-108 group-hover:rotate-0.5 brightness-[0.75] group-hover:brightness-[0.45]"
          referrerPolicy="no-referrer"
          loading="lazy"
        />
        <div className="absolute inset-0 bg-gradient-to-t from-slate-950 via-slate-950/40 to-slate-950/10 opacity-70 group-hover:opacity-90 transition-all duration-300" />
      </div>

      {/* 2. Rating & Quick watchlist control markers - Top Bar */}
      <div className="absolute top-3.5 inset-x-3.5 flex items-center justify-between z-10 pointer-events-auto">
        <span className="text-[10px] bg-slate-950/70 border border-white/10 text-slate-300 font-mono px-2 py-0.5 rounded-full flex items-center gap-1 backdrop-blur-md">
          <Star className="w-3 h-3 text-amber-400 fill-current" />
          {video.imdbRating.toFixed(1)}
        </span>

        {/* Watchlist Quick Circle Button */}
        <button
          onClick={onToggleWatchlist}
          className={`p-2 rounded-xl backdrop-blur-md border transition-all duration-300 text-white ${
            isWatchlisted 
              ? "bg-orange-600/30 border-orange-500/40 text-orange-400 hover:bg-orange-600/50" 
              : "bg-slate-950/40 border-white/5 hover:bg-orange-600/20 hover:border-orange-500/35"
          }`}
          title={isWatchlisted ? "Remove from Watchlist" : "Add to Watchlist"}
        >
          {isWatchlisted ? (
            <Check className="w-3.5 h-3.5 stroke-[3]" />
          ) : (
            <Plus className="w-3.5 h-3.5 stroke-[2.5]" />
          )}
        </button>
      </div>

      {/* 3. Center Cinematic Play Pulsar Icon */}
      <div className="absolute inset-0 flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity duration-300 z-10 pointer-events-none">
        <div className="p-4 bg-orange-600 border border-orange-400/30 text-white rounded-full shadow-2xl scale-75 group-hover:scale-100 transition-transform duration-350 flex items-center justify-center animate-[pulse_2s_infinite]">
          <Play className="w-6 h-6 fill-current pl-0.5" />
        </div>
      </div>

      {/* 4. Details Container at bottom (Visible always but shifts & expands slightly on focus) */}
      <div className="mt-auto relative z-10 p-4 md:p-5 flex flex-col gap-1.5 w-full">
        {/* Release year and duration tags */}
        <div className="flex items-center gap-2 text-[10px] font-mono text-slate-400 group-hover:text-orange-300 transition-colors">
          <span>{video.year}</span>
          <span className="w-1 h-1 rounded-full bg-slate-600" />
          <span>{video.category}</span>
          <span className="w-1 h-1 rounded-full bg-slate-600 animate-pulse" />
          <span>{video.duration}</span>
        </div>

        {/* Core Title */}
        <h4 className="font-display font-semibold text-sm md:text-base text-slate-100 group-hover:text-white leading-snug tracking-wide group-hover:translate-x-1 transition-transform duration-300 truncate">
          {video.title}
        </h4>

        {/* Subtle expandable synopsis excerpt */}
        <p className="text-[11px] text-slate-400 leading-relaxed font-normal line-clamp-2 h-0 opacity-0 group-hover:h-8 group-hover:opacity-100 transition-all duration-300 overflow-hidden mt-0.5">
          {video.description}
        </p>

        {/* Bottom indicators: Views & Rating code */}
        <div className="flex items-center justify-between text-[11px] text-slate-500 pt-1 border-t border-white/5 opacity-80 group-hover:opacity-100 transition-opacity">
          <span className="flex items-center gap-1">
            <Eye className="w-3.5 h-3.5" />
            {formatViews(video.views)}
          </span>
          <span className="font-mono text-[10px] text-slate-400 bg-white/5 px-1.5 py-0.5 rounded border border-white/5">
            {video.id === "tears-of-steel" ? "1080p" : "HD"}
          </span>
        </div>
      </div>
    </motion.div>
  );
}
