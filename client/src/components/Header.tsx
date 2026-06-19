/**
 * @license
 * SPDX-License-Identifier: Apache-2.0
 */

import React from "react";
import { Sparkles, Library, PlayCircle, Clock, Heart, Search, User, Compass } from "lucide-react";

interface HeaderProps {
  searchQuery: string;
  setSearchQuery: (q: string) => void;
  watchlistCount: number;
  historyCount: number;
  onNavigateToWatchlist: () => void;
  onNavigateToLibrary: () => void;
}

export default function Header({
  searchQuery,
  setSearchQuery,
  watchlistCount,
  historyCount,
  onNavigateToWatchlist,
  onNavigateToLibrary
}: HeaderProps) {
  return (
    <header className="sticky top-0 z-40 w-full px-4 md:px-8 py-4 bg-[#050505]/70 backdrop-blur-md border-b border-white/5 transition-all">
      <div className="max-w-7xl mx-auto flex flex-col md:flex-row items-center justify-between gap-4">
        {/* Brand Logo with cinematic neon pulse styling */}
        <div 
          onClick={onNavigateToLibrary}
          className="flex items-center gap-2.5 cursor-pointer group"
        >
          <div className="relative flex items-center justify-center w-10 h-10 rounded-xl bg-gradient-to-tr from-orange-600 to-amber-400 p-[1px] shadow-[0_0_20px_-3px_rgba(249,115,22,0.5)]">
            <div className="w-full h-full bg-[#050505] rounded-[11px] flex items-center justify-center group-hover:bg-transparent transition-all duration-300">
              <Sparkles className="w-5 h-5 text-orange-400 group-hover:text-slate-950 transition-colors" />
            </div>
          </div>
          <div className="flex flex-col">
            <span className="font-display font-extrabold text-lg tracking-wider text-transparent bg-clip-text bg-gradient-to-r from-white via-slate-100 to-slate-400">
              GLASS<span className="text-orange-500">STREAM</span>
            </span>
            <span className="text-[9px] text-orange-400 font-mono tracking-widest -mt-1 uppercase">Ambient Lab v1.0</span>
          </div>
        </div>

        {/* Cinematic Unified Search Bar */}
        <div className="w-full md:w-96 relative">
          <div className="absolute inset-y-0 left-3 flex items-center pointer-events-none text-slate-400">
            <Search className="w-4 h-4" />
          </div>
          <input
            type="text"
            placeholder="Search cyber titles, categories, directors..."
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            className="w-full bg-[#0f0f0f]/80 text-slate-200 placeholder-slate-500 text-xs py-2.5 pl-10 pr-4 rounded-xl border border-white/5 focus:border-orange-500/50 focus:ring-1 focus:ring-orange-500/20 backdrop-blur-sm transition-all focus:outline-none"
          />
        </div>

        {/* Dynamic Action Badges */}
        <div className="flex items-center gap-4">
          <button 
            onClick={onNavigateToLibrary}
            className="text-xs text-slate-400 hover:text-white transition-all flex items-center gap-1.5"
          >
            <Compass className="w-4 h-4" />
            <span className="hidden sm:inline">Browse</span>
          </button>

          <button 
            onClick={onNavigateToWatchlist}
            className="relative p-2 rounded-xl bg-white/5 border border-white/5 hover:border-orange-500/20 hover:bg-orange-600/10 text-slate-300 hover:text-orange-400 transition-all flex items-center gap-1.5"
            title="My Watchlist"
          >
            <Heart className="w-4 h-4 fill-current text-transparent hover:text-orange-400 transition-colors" />
            <span className="text-xs hidden sm:inline font-medium">Watchlist</span>
            {watchlistCount > 0 && (
              <span className="absolute -top-1.5 -right-1.5 min-w-4 h-4 bg-orange-600 text-white font-mono text-[9px] px-1 rounded-full flex items-center justify-center font-bold shadow-lg animate-pulse">
                {watchlistCount}
              </span>
            )}
          </button>

          <span className="h-6 w-px bg-white/5 hidden sm:inline" />

          {/* User Profile Shield card */}
          <div className="flex items-center gap-3.5 pl-2">
            <div className="flex flex-col items-end text-right hidden lg:flex">
              <span className="text-xs font-semibold text-slate-100 flex items-center gap-1">
                Aether Voyager
                <span className="w-2 h-2 rounded-full bg-orange-500 animate-pulse" />
              </span>
              <span className="text-[9px] text-orange-500 font-mono tracking-wider font-semibold uppercase">Diamond Elite Premium</span>
            </div>
            
            <div className="relative">
              <div className="w-9 h-9 rounded-xl bg-slate-800 border border-white/10 overflow-hidden flex items-center justify-center text-slate-300 shadow bg-gradient-to-tr from-orange-600/30 to-indigo-500/30">
                <User className="w-4 h-4" />
              </div>
              <div className="absolute bottom-0 right-0 w-2.5 h-2.5 bg-orange-500 border-2 border-[#050505] rounded-full" />
            </div>
          </div>
        </div>
      </div>
    </header>
  );
}
