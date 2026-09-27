/**
 * @license
 * SPDX-License-Identifier: Apache-2.0
 */

import React from "react";
import { Heart, Search, Compass } from "lucide-react";
import AccountMenu from "./AccountMenu";

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
      <div className="w-full flex flex-col md:flex-row items-center justify-between gap-4">
        {/* Brand Logo */}
        <div 
          onClick={onNavigateToLibrary}
          className="flex items-center gap-2.5 cursor-pointer group"
        >
          <img
            src="/logo.webp"
            alt="AdoboFlix"
            className="w-10 h-10 rounded-xl object-cover border border-white/10 group-hover:border-orange-500/30 transition-all duration-300"
          />
          <div className="flex flex-col">
            <span className="font-display font-extrabold text-lg tracking-wider text-transparent bg-clip-text bg-gradient-to-r from-white via-slate-100 to-slate-400">
              Adobo<span className="text-orange-500">Flix</span>
            </span>
            <span className="text-[9px] text-orange-400 font-mono tracking-widest -mt-1 uppercase">Premium Streaming</span>
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
            <Heart className="w-4 h-4 fill-current" />
            <span className="text-xs hidden sm:inline font-medium">Watchlist</span>
            {watchlistCount > 0 && (
              <span className="absolute -top-1.5 -right-1.5 min-w-4 h-4 bg-orange-600 text-white font-mono text-[9px] px-1 rounded-full flex items-center justify-center font-bold shadow-lg animate-pulse">
                {watchlistCount}
              </span>
            )}
          </button>

          <span className="h-6 w-px bg-white/5 hidden sm:inline" />

          {/* Account menu — the avatar opens the real account control: connect
              or disconnect a playlist code and read the active source's
              account facts. */}
          <AccountMenu />
        </div>
      </div>
    </header>
  );
}
