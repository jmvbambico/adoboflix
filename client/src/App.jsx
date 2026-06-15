import React, { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Search, Menu, X, Play, ChevronLeft, ChevronRight } from "lucide-react";

// API fetch functions
const fetchStats = () => fetch("/api/v1/stats").then((r) => r.json());
const fetchEntries = (params) => fetch(`/api/v1/entries?${params.toString()}`).then((r) => r.json());
const fetchProviders = () => fetch("/api/v1/providers").then((r) => r.json());
const fetchGenres = () => fetch("/api/v1/genres").then((r) => r.json());

export default function App() {
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [searchQuery, setSearchQuery] = useState("");
  const [selectedProvider, setSelectedProvider] = useState("");
  const [selectedGenre, setSelectedGenre] = useState("");
  const [selectedType, setSelectedType] = useState("");
  const [currentEntry, setCurrentEntry] = useState(null);

  // Data fetching
  const { data: stats } = useQuery({ queryKey: ["stats"], queryFn: fetchStats });
  const { data: providers } = useQuery({ queryKey: ["providers"], queryFn: fetchProviders });
  const { data: genres } = useQuery({ queryKey: ["genres"], queryFn: fetchGenres });
  
  const searchParams = new URLSearchParams();
  if (searchQuery) searchParams.set("q", searchQuery);
  if (selectedProvider) searchParams.set("provider", selectedProvider);
  if (selectedGenre) searchParams.set("genre", selectedGenre);
  if (selectedType) searchParams.set("type", selectedType);
  
  const { data: entries, isLoading } = useQuery({
    queryKey: ["entries", searchParams.toString()],
    queryFn: () => fetchEntries(searchParams),
  });

  return (
    <div className="min-h-screen flex flex-col">
      {/* Header */}
      <header className="h-12 flex items-center justify-between px-5 border-b"
        style={{ background: "var(--bg-1)", borderColor: "var(--b1)" }}>
        <div className="flex items-center gap-2.5">
          <button 
            onClick={() => setSidebarOpen(!sidebarOpen)}
            className="p-2 rounded"
            style={{ color: "var(--t3)" }}
          >
            {sidebarOpen ? <X size={18} /> : <Menu size={18} />}
          </button>
          <div className="font-bold text-[15px] tracking-tight" style={{ color: "var(--t1)" }}>
            AdoboFlix
          </div>
        </div>
        
        <div className="flex-1 max-w-[400px] mx-5">
          <div className="relative">
            <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2" style={{ color: "var(--t4)" }} />
            <input
              type="text"
              placeholder={`Search ${stats?.total_titles || 1700}+ titles...`}
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              className="w-full py-2 pl-9 pr-4 rounded-md text-sm outline-none"
              style={{ 
                background: "var(--bg-0)", 
                border: "1px solid var(--b1)", 
                color: "var(--t1)" 
              }}
            />
          </div>
        </div>
        
        <div className="text-xs" style={{ color: "var(--t3)", fontFamily: "var(--mono)" }}>
          {stats?.total_titles || 1700} titles
        </div>
      </header>

      <div className="flex flex-1">
        {/* Sidebar */}
        <aside 
          className="w-60 flex-shrink-0 p-5 border-r transition-all duration-300"
          style={{ 
            background: "var(--bg-1)", 
            borderColor: "var(--b1)",
            marginLeft: sidebarOpen ? 0 : "-240px"
          }}
        >
          <FilterSection 
            title="Provider"
            options={providers || ["Miruro", "ReelPipe", "VidStreaming", "VidCloud9"]}
            selected={selectedProvider}
            onSelect={setSelectedProvider}
          />
          <FilterSection 
            title="Genre"
            options={genres || ["Action", "Drama", "Comedy", "Thriller", "Sci-Fi", "Horror", "Fantasy"]}
            selected={selectedGenre}
            onSelect={setSelectedGenre}
          />
          <FilterSection 
            title="Type"
            options={["movie", "tv"]}
            selected={selectedType}
            onSelect={setSelectedType}
          />
        </aside>

        {/* Main Content */}
        <main className="flex-1 p-5 overflow-auto">
          {/* Player Bar */}
          {currentEntry && (
            <PlayerBar entry={currentEntry} />
          )}

          {/* Bento Grid - Featured Content */}
          <div className="mb-5">
            <h2 className="text-base font-semibold mb-4 flex items-center gap-2" style={{ color: "var(--t1)" }}>
              <span className="w-[3px] h-5 rounded" style={{ background: "var(--accent)" }} />
              Featured
            </h2>
            <div className="bento-grid">
              {(entries?.entries || []).slice(0, 14).map((entry, i) => (
                <BentoItem 
                  key={entry.id}
                  entry={entry}
                  featured={i < 2}
                  onClick={() => setCurrentEntry(entry)}
                />
              ))}
            </div>
          </div>

          {/* Recently Added */}
          <h2 className="text-base font-semibold mb-4 flex items-center gap-2" style={{ color: "var(--t1)" }}>
            <span className="w-[3px] h-5 rounded" style={{ background: "var(--accent)" }} />
            Recently Added
          </h2>
          <div className="grid gap-4" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(140px, 1fr))" }}>
            {(entries?.entries || []).slice(14, 40).map((entry) => (
              <PosterCard 
                key={entry.id}
                entry={entry}
                onClick={() => setCurrentEntry(entry)}
              />
            ))}
          </div>
        </main>
      </div>
    </div>
  );
}

function FilterSection({ title, options, selected, onSelect }) {
  return (
    <div className="mb-6">
      <h3 className="text-[11px] font-semibold uppercase tracking-wider mb-2.5" style={{ color: "var(--t4)" }}>
        {title}
      </h3>
      <div className="space-y-1">
        <FilterChip 
          label={`All ${title}s`}
          active={!selected}
          onClick={() => onSelect("")}
        />
        {options.map((opt) => (
          <FilterChip 
            key={opt}
            label={opt}
            active={selected === opt}
            onClick={() => onSelect(opt)}
          />
        ))}
      </div>
    </div>
  );
}

function FilterChip({ label, active, onClick }) {
  return (
    <button
      className={`filter-chip ${active ? "active" : ""}`}
      onClick={onClick}
    >
      {label}
    </button>
  );
}

function BentoItem({ entry, featured, onClick }) {
  const posterStyle = featured
    ? { gridRow: "span 2", gridColumn: "span 2" }
    : {};

  return (
    <div 
      className={`bento-item ${featured ? "featured" : ""}`}
      style={posterStyle}
      onClick={onClick}
    >
      <div className="bento-play"><Play size={20} /></div>
      <div className="bento-overlay">
        <div className="bento-title">{entry.name}</div>
        <div className="bento-subtitle">
          {entry.release_year || "2024"} • {entry.category || "Movie"}
        </div>
      </div>
      {entry.poster && (
        <img 
          src={entry.poster} 
          alt={entry.name}
          className="absolute inset-0 w-full h-full object-cover"
          loading="lazy"
        />
      )}
    </div>
  );
}

function PosterCard({ entry, onClick }) {
  return (
    <div 
      className="rounded-md overflow-hidden cursor-pointer transition-all hover:-translate-y-1 hover:shadow-lg"
      style={{ 
        background: "var(--bg-2)", 
        border: "1px solid var(--b1)" 
      }}
      onClick={onClick}
    >
      <div 
        className="aspect-[2/3] relative"
        style={{ background: "var(--bg-3)" }}
      >
        {entry.poster && (
          <img 
            src={entry.poster} 
            alt={entry.name}
            className="absolute inset-0 w-full h-full object-cover"
            loading="lazy"
          />
        )}
      </div>
      <div className="p-3">
        <div className="text-xs font-semibold truncate" style={{ color: "var(--t1)" }}>
          {entry.name}
        </div>
        <div className="text-[10px] mt-1 truncate" style={{ color: "var(--t3)" }}>
          {entry.release_year || "2024"} • {entry.category || "Movie"}
        </div>
      </div>
    </div>
  );
}

function PlayerBar({ entry }) {
  return (
    <div 
      className="flex items-center gap-4 p-4 rounded-lg mb-5"
      style={{ background: "var(--bg-2)", border: "1px solid var(--b1)" }}
    >
      <div 
        className="w-[60px] h-[90px] rounded flex-shrink-0"
        style={{ 
          background: "var(--bg-3)",
          backgroundImage: entry.poster ? `url(${entry.poster})` : "none",
          backgroundSize: "cover",
          backgroundPosition: "center"
        }}
      />
      <div className="flex-1">
        <div className="text-sm font-semibold" style={{ color: "var(--t1)" }}>
          {entry.name}
        </div>
        <div className="text-xs mt-0.5" style={{ color: "var(--t3)" }}>
          {entry.release_year || "2024"} • {entry.category || "Movie"} • {entry.type || "movie"}
        </div>
      </div>
      <div className="flex items-center gap-3">
        <button className="control-btn">⏮</button>
        <button className="control-btn play">▶</button>
        <button className="control-btn">⏭</button>
      </div>
    </div>
  );
}
