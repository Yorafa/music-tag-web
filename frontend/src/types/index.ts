export interface SongInfo {
  id: string;
  name: string;
  artist: string;
  artist_id: string;
  album: string;
  album_id: string;
  album_img: string;
  year: string;
  resource?: string;
  score?: number;
}

export interface FileNode {
  id: number;
  name: string;
  title: string;
  icon: string;
  state: string;
  children?: FileNode[];
  expanded?: boolean;
}

export interface MusicTagInfo {
  title: string;
  filename: string;
  artist: string;
  album: string;
  albumartist: string;
  genre: string;
  language: string;
  year: string;
  lyrics: string;
  comment: string;
  album_img: string;
  album_type: string;
  discnumber: string;
  tracknumber: string;
  duration: string;
  bit_rate: string;
  size: string;
  artwork: string;
  artwork_w: number;
  artwork_h: number;
  artwork_size: number;
  is_save_lyrics_file: boolean;
  is_save_album_cover: boolean;
}

export interface LyricResult {
  lyric: string;
  cover?: string;
}

export type MusicSource = 'netease' | 'qmusic' | 'kugou' | 'kuwo' | 'migu' | 'musicbrainz' | 'acoustid' | 'smart_tag';
export type SelectMode = 'simple' | 'hard';
