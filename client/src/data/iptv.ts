/**
 * @license
 * SPDX-License-Identifier: Apache-2.0
 */

export interface EPGProgram {
  time: string;
  program: string;
  active: boolean;
}

export interface IPTVChannel {
  id: string;
  title: string;
  category: string;
  videoUrl: string;
  thumbnailUrl: string;
  description: string;
  viewers: string;
  isLive: boolean;
  nowPlaying: string;
  nowPlayingDescription: string;
  nowPlayingTime: string;
  epg: EPGProgram[];
}

export const IPTV_CATEGORIES = [
  "All",
  "Cinema",
  "Nature & Space",
  "Cyber News",
  "Music & Ambient"
];

export const IPTV_CHANNELS: IPTVChannel[] = [
  {
    id: "live-neon-cinema",
    title: "NEON CINEMA HD",
    category: "Cinema",
    videoUrl: "https://commondatastorage.googleapis.com/gtv-videos-bucket/sample/TearsOfSteel.mp4",
    thumbnailUrl: "https://images.unsplash.com/photo-1536440136628-849c177e76a1?w=800&auto=format&fit=crop&q=70",
    description: "Our flagship cinematic station broadcasting next-generation cyberpunk features, VFX masterpieces, and CGI classics 24/7 on loop.",
    viewers: "12,840 viewers",
    isLive: true,
    nowPlaying: "Tears of Steel (2025 VFX Cut)",
    nowPlayingDescription: "A group of scientists perform an immersive digital re-enactment of their past in a dystopian world run by giant robots.",
    nowPlayingTime: "08:00 AM - 10:15 AM",
    epg: [
      { time: "08:00 AM - 10:15 AM", program: "Tears of Steel (2025 VFX Cut)", active: true },
      { time: "10:15 AM - 12:00 PM", program: "Neo-Amsterdam Drone Racing Finals", active: false },
      { time: "12:00 PM - 02:30 PM", program: "Sintel: Extended Fantasy Chronicles", active: false },
      { time: "02:30 PM - 04:00 PM", program: "The Digital Puppeteer: Secrets of CGI", active: false }
    ]
  },
  {
    id: "live-nature-orbit",
    title: "NATURE ORBIT LIVE",
    category: "Nature & Space",
    videoUrl: "https://commondatastorage.googleapis.com/gtv-videos-bucket/sample/BigBuckBunny.mp4",
    thumbnailUrl: "https://images.unsplash.com/photo-1470240731273-7821a6eeb6bd?w=800&auto=format&fit=crop&q=70",
    description: "Immerse yourself in spectacular green habitats, deep woodland chronicles, and hilarious animated wilderness antics. Broadened ecosystem coverage.",
    viewers: "4,320 viewers",
    isLive: true,
    nowPlaying: "Big Buck Bunny: Forest Rebirth",
    nowPlayingDescription: "A large, warm-hearted forest rabbit sets up high-fidelity, hilarious traps for pesky squirrels in his peaceful routines.",
    nowPlayingTime: "08:45 AM - 10:30 AM",
    epg: [
      { time: "07:00 AM - 08:45 AM", program: "Arctic Melt Down: Eco Series V", active: false },
      { time: "08:45 AM - 10:30 AM", program: "Big Buck Bunny: Forest Rebirth", active: true },
      { time: "10:30 AM - 11:45 AM", program: "Mammoth Valley Geo Tracking", active: false },
      { time: "11:45 AM - 01:30 PM", program: "Microhabitats of the Pacific Northwest", active: false }
    ]
  },
  {
    id: "live-cyber-bulletin",
    title: "CYBER BULLETIN 24",
    category: "Cyber News",
    videoUrl: "https://commondatastorage.googleapis.com/gtv-videos-bucket/sample/ForBiggerEscapes.mp4",
    thumbnailUrl: "https://images.unsplash.com/photo-1522163182402-834f871fd851?w=800&auto=format&fit=crop&q=70",
    description: "Live news, radical sports, technical telemetry summits, extreme weather tracking, and deep glacier expeditions across the globe.",
    viewers: "9,120 viewers",
    isLive: true,
    nowPlaying: "Yosemite Vertical Climbs Tour",
    nowPlayingDescription: "Extreme rock climbing teams traverse the sheer granite faces of Yosemite Valley. Capturing raw vertical adventures.",
    nowPlayingTime: "09:00 AM - 11:30 AM",
    epg: [
      { time: "09:00 AM - 11:30 AM", program: "Yosemite Vertical Climbs Tour", active: true },
      { time: "11:30 AM - 01:00 PM", program: "Thermal Blazes: Volcanology Today", active: false },
      { time: "01:00 PM - 02:30 PM", program: "Global Tech Summit: Quantum Network", active: false },
      { time: "02:30 PM - 04:00 PM", program: "Climate Shift: Glacier Meltdowns Update", active: false }
    ]
  },
  {
    id: "live-sublime-loops",
    title: "SUBLIME AMBIENT LOOPS",
    category: "Music & Ambient",
    videoUrl: "https://commondatastorage.googleapis.com/gtv-videos-bucket/sample/ElephantsDream.mp4",
    thumbnailUrl: "https://images.unsplash.com/photo-1578632767115-351597cf2477?w=800&auto=format&fit=crop&q=70",
    description: "Relaxing mechanical clocks, surreal steampunk textures, ambient electronic chords, and neon loops designed for focus and tranquility.",
    viewers: "2,150 viewers",
    isLive: true,
    nowPlaying: "Elephant's Dreamscape Echoes",
    nowPlayingDescription: "Tranquil mechanical playgrounds, glowing neon clock towers, and lo-fi digital synths merging.",
    nowPlayingTime: "07:30 AM - 11:00 AM",
    epg: [
      { time: "07:30 AM - 11:00 AM", program: "Elephant's Dreamscape Echoes", active: true },
      { time: "11:00 AM - 01:00 PM", program: "Sunset Highway Speed Laps", active: false },
      { time: "01:00 PM - 03:00 PM", program: "Voxel Land Neon Playlists", active: false },
      { time: "03:00 PM - 06:00 PM", program: "Deep Space Cosmos Visualizer", active: false }
    ]
  }
];
