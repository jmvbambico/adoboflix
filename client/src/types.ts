export interface Video {
  id: string;
  title: string;
  description: string;
  category: string;
  videoUrl: string;
  thumbnailUrl: string;
  duration: string; // e.g., "1h 58m" or "10m 30s"
  durationSeconds: number; // For progression tracking
  rating: string; // e.g., "PG-13", "R", "G"
  imdbRating: number; // e.g. 8.4
  year: number;
  views: number;
  likes: number;
  director: string;
  cast: string[];
  tags: string[];
  isFeatured?: boolean;
  // AdoboTV backend fields
  backgroundUrl?: string;
  type?: string; // "Movie" | "Series"
  provider?: string; // source_type: HLS | DRM
  drmType?: string;
  drmK?: string;
  licenseUrl?: string;
  streamUrl?: string;
}

export interface Episode {
  id: string;
  vod_id: string;
  season_number: number;
  episode_number: number;
  name: string;
  stream_url: string;
  source_type: string;
  drm_type: string;
  drm_k: string;
  license_url: string;
  user_agent: string;
  referer: string;
}

export interface Review {
  id: string;
  videoId: string;
  userName: string;
  userAvatar: string;
  rating: number;
  comment: string;
  timestamp: string;
}

export interface WatchlistState {
  videoIds: string[];
}

export interface WatchHistoryItem {
  videoId: string;
  watchedAt: string; // ISO date
  progress: number; // percentage completed
  currentTime: number; // current timestamp in seconds
}
