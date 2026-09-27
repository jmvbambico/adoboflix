import { Video, Episode } from "../types";

// In dev, Vite proxies /api to the Go backend (see vite.config.ts).
// In production the Go server serves both the static client and the API
// from the same origin, so a relative base works everywhere.
const API_BASE = "/api/v1";

// A non-OK response from the Go backend, keeping the machine-readable "code"
// the handler attaches (internal/handler/source_error.go) so callers and
// components branch on a constant instead of substring-matching a sentence.
export class ApiError extends Error {
  readonly status: number;
  readonly code?: string;

  constructor(message: string, status: number, code?: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

export function isApiError(err: unknown): err is ApiError {
  return err instanceof ApiError;
}

// React Query retry policy. Only failures that can clear on their own are
// retried: a 4xx is a settled answer (a 403 gate needs a human to act, a 404
// is a miss), so retrying it is churn that delays the message reaching the
// user. 5xx and transport failures keep a small bounded retry.
const MAX_QUERY_RETRIES = 2;

export function shouldRetryQuery(failureCount: number, error: unknown): boolean {
  if (isApiError(error) && error.status >= 400 && error.status < 500) return false;
  return failureCount < MAX_QUERY_RETRIES;
}

// Parse a non-OK body into an ApiError. The backend writes
// {"error": "...", "code": "..."}; when the body is not that envelope (a
// plain-text or empty 5xx, a proxy error page) fall back to the previous
// string so the message is never empty.
export function apiErrorFromResponse(status: number, url: string, body: string): ApiError {
  let message = `API ${status} ${url}: ${body.slice(0, 200)}`;
  let code: string | undefined;
  try {
    const parsed: unknown = JSON.parse(body);
    if (parsed && typeof parsed === "object") {
      const envelope = parsed as { error?: unknown; code?: unknown };
      if (typeof envelope.error === "string" && envelope.error) message = envelope.error;
      if (typeof envelope.code === "string" && envelope.code) code = envelope.code;
    }
  } catch {
    // Not JSON — keep the fallback message.
  }
  return new ApiError(message, status, code);
}

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
    throw apiErrorFromResponse(res.status, url, body);
  }
  return res.json() as Promise<T>;
}

// Send a JSON body and decode a JSON reply, mapping a non-OK envelope through
// the same ApiError path as every other call. The body stays in the request
// body — never a query string — so a credential submitted here cannot leak into
// a URL, history, or a referer.
async function sendJSON<T>(method: string, url: string, body?: unknown): Promise<T> {
  const res = await fetch(url, {
    method,
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw apiErrorFromResponse(res.status, url, text);
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

// ── Source / playlist code API ─────────────────────────────────────

// What the active source is and whether it can serve content yet. The server
// never returns the code itself; status exposes only facts, never the
// credential.
//
// subscription_expires_at and user_message are additive: the server includes
// them only when the active source can supply account facts AND upstream
// reported them. Absent means "upstream did not say", which is normal for a
// source with no account concept (a local file, the development database) and
// for a non-subscription tier.
export interface SourceStatus {
  source: string;
  needs_playlist_code: boolean;
  playlist_code_configured: boolean;
  subscription_expires_at?: string; // RFC3339, e.g. "2030-01-01T00:00:00Z"
  user_message?: string;
}

export function fetchSourceStatus(): Promise<SourceStatus> {
  return getJSON<SourceStatus>(`${API_BASE}/source/status`);
}

// Submit a playlist code. The server validates it against the real upstream
// before persisting: a gate that proves the code valid (device pending, an
// inactive subscription) persists it, anything else persists nothing. The code
// rides in the request body only and is never echoed back.
export function setPlaylistCode(code: string): Promise<SourceStatus> {
  return sendJSON<SourceStatus>("POST", `${API_BASE}/source/playlist-code`, { code });
}

// Clear a stored code and reopen the source from whatever configuration
// remains (an environment fallback, or the unconfigured state).
export function clearPlaylistCode(): Promise<SourceStatus> {
  return sendJSON<SourceStatus>("DELETE", `${API_BASE}/source/playlist-code`);
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
