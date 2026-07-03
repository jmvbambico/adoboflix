import { Video, Episode } from "../types";

// In dev, Vite proxies /api to the Go backend (see vite.config.ts).
// In production the Go server serves both the static client and the API
// from the same origin, so a relative base works everywhere.
const API_BASE = "/api/v1";

// Raw shape returned by the Go backend (maps public.vod_assets).
export interface BackendEntry {
  id: string;
  name: string;
  type: string; // "Movie" | "Series"
  category?: string;
  poster?: string;
  background_image?: string;
  plot?: string;
  cast_members?: string[];
  directors?: string[];
  release_year?: number;
  rating?: string;
  status?: string;
  stream_url?: string;
  provider?: string;
  drm_type?: string;
  drm_k?: string;
  license_url?: string;
  duration?: number;
  tmdb_id?: number;
  episode_count?: number;
}

interface EntriesResponse {
  entries: BackendEntry[];
  total: number;
  page: number;
  has_more: boolean;
}

interface SearchResponse {
  results: BackendEntry[];
  total: number;
  page: number;
  has_more: boolean;
}

export interface ResolvedStream {
  url: string;
  provider: string;
  drm_type: string;
  drm_k: string;
  license_url: string;
  user_agent?: string;
  referer?: string;
}

interface EpisodesResponse {
  episodes: Episode[];
  seasons: number[];
}

const FALLBACK_POSTER =
  "https://images.unsplash.com/photo-1489599849927-2ee91cede3ba?w=800&auto=format&fit=crop&q=80";

function formatDuration(seconds?: number): string {
  if (!seconds || seconds <= 0) return "—";
  const total = seconds > 100000 ? Math.round(seconds / 60) : seconds;
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m`;
  return `${total}s`;
}

function normalizeImdb(rating?: string): number {
  if (!rating) return 0;
  const n = parseFloat(rating);
  if (isNaN(n)) return 0;
  if (n > 10) return Math.round((n / 10) * 10) / 10;
  return Math.round(n * 10) / 10;
}

function contentRating(type: string): string {
  return type === "Series" ? "TV" : "Movie";
}

export function mapEntryToVideo(e: BackendEntry): Video {
  const poster = e.poster || e.background_image || FALLBACK_POSTER;
  const bg = e.background_image || e.poster || FALLBACK_POSTER;
  return {
    id: e.id,
    title: e.name,
    description: e.plot || "No description available.",
    category: e.category || "Uncategorized",
    videoUrl: "",
    thumbnailUrl: poster,
    backgroundUrl: bg,
    duration: formatDuration(e.duration),
    durationSeconds: e.duration && e.duration > 0 ? e.duration : 0,
    rating: contentRating(e.type),
    imdbRating: normalizeImdb(e.rating),
    year: e.release_year || 0,
    views: 0,
    likes: 0,
    director: (e.directors && e.directors[0]) || "Unknown",
    cast: e.cast_members || [],
    tags: [e.category, e.type, e.provider].filter(Boolean) as string[],
    type: e.type,
    provider: e.provider || "",
    drmType: e.drm_type || "",
    drmK: e.drm_k || "",
    licenseUrl: e.license_url || "",
    streamUrl: e.stream_url || "",
  };
}

async function getJSON<T>(url: string): Promise<T> {
  const res = await fetch(url);
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new Error(`API ${res.status} ${url}: ${body.slice(0, 200)}`);
  }
  return res.json() as Promise<T>;
}

export async function fetchVideos(params: {
  search?: string;
  category?: string;
  limit?: number;
}): Promise<Video[]> {
  const { search, category, limit = 200 } = params;
  const qs = new URLSearchParams();
  qs.set("limit", String(limit));
  if (category && category !== "All") qs.set("genre", category);

  if (search && search.trim()) {
    qs.set("q", search.trim());
    const data = await getJSON<SearchResponse>(`${API_BASE}/search?${qs}`);
    return data.results.map(mapEntryToVideo);
  }

  const data = await getJSON<EntriesResponse>(`${API_BASE}/entries?${qs}`);
  return data.entries.map(mapEntryToVideo);
}

export async function fetchCategories(): Promise<string[]> {
  const data = await getJSON<{ genres: string[] }>(`${API_BASE}/genres`);
  return ["All", ...data.genres];
}

export async function fetchProviders(): Promise<string[]> {
  const data = await getJSON<{ providers: string[] }>(`${API_BASE}/providers`);
  return data.providers;
}

export interface AppStats {
  total_titles: number;
  total_providers: number;
  total_genres: number;
}

export async function fetchStats(): Promise<AppStats> {
  return getJSON<AppStats>(`${API_BASE}/stats`);
}

// Resolve a playable, proxied stream URL for a given asset id.
export async function resolveStream(id: string): Promise<ResolvedStream> {
  return getJSON<ResolvedStream>(`${API_BASE}/resolve?id=${encodeURIComponent(id)}`);
}

// Fetch episodes for a series asset.
export async function fetchEpisodes(vodId: string): Promise<Episode[]> {
  const data = await getJSON<EpisodesResponse>(`${API_BASE}/episodes/${encodeURIComponent(vodId)}`);
  return data.episodes;
}

// Resolve a specific episode's stream.
export async function resolveEpisode(episodeId: string): Promise<ResolvedStream> {
  return getJSON<ResolvedStream>(`${API_BASE}/resolve/episode/${encodeURIComponent(episodeId)}`);
}

// ── IPTV Channel API ───────────────────────────────────────────────

export interface BackendChannel {
  id: string;
  name: string;
  logo?: string;
  category?: string;
  epg_source_id?: string;
  epg_channel_id?: string;
  status: string;
  created_at?: string;
  updated_at?: string;
}

interface ChannelsResponse {
  channels: BackendChannel[];
  total: number;
  page: number;
  has_more: boolean;
}

export interface ResolvedChannelStream {
  url: string;
  provider: string;
  source_type: string;
  drm_type: string;
  drm_k: string;
  license_url: string;
  user_agent: string;
  referer: string;
  label: string;
  resolution: string;
}

export async function fetchChannels(params: {
  category?: string;
  limit?: number;
  search?: string;
}): Promise<BackendChannel[]> {
  const { category, limit = 500, search } = params;
  const qs = new URLSearchParams();
  qs.set("limit", String(limit));
  if (category && category !== "All") qs.set("category", category);
  const data = await getJSON<ChannelsResponse>(`${API_BASE}/channels?${qs}`);
  let channels = data.channels;
  // Client-side name filter (backend doesn't support search yet)
  if (search && search.trim()) {
    const q = search.trim().toLowerCase();
    channels = channels.filter(c => c.name.toLowerCase().includes(q));
  }
  return channels;
}

export async function fetchChannelCategories(): Promise<string[]> {
  const data = await getJSON<{ categories: string[] }>(`${API_BASE}/channels/categories`);
  return ["All", ...data.categories];
}

export async function resolveChannelStream(channelId: string): Promise<ResolvedChannelStream> {
  return getJSON<ResolvedChannelStream>(`${API_BASE}/channels/${encodeURIComponent(channelId)}/resolve`);
}

// ── EPG (Electronic Programme Guide) ──────────────────────────────────────────

export interface EPGProgramme {
  channel_id: string;
  title: string;
  description?: string;
  start: string;       // XMLTV raw "20260624080000 +0800"
  stop: string;        // XMLTV raw
  start_unix: number;  // Unix timestamp
  stop_unix: number;   // Unix timestamp
}

export interface ChannelEPG {
  epg_channel_id: string;
  channel_name?: string;
  current?: EPGProgramme;
  next?: EPGProgramme;
  upcoming?: EPGProgramme[];
}

export async function fetchChannelEPG(channelId: string): Promise<ChannelEPG | null> {
  try {
    const res = await fetch(`${API_BASE}/channels/${encodeURIComponent(channelId)}/epg`);
    if (!res.ok) return null;
    const data = await res.json();
    return data.epg ?? null;
  } catch {
    return null;
  }
}
