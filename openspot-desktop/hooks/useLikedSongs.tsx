import { useState, useEffect, useCallback, createContext, useContext, ReactNode } from 'react';
import AsyncStorage from '@react-native-async-storage/async-storage';
import { Track } from '../types/music';
import { isRecord, parseStoredJSON } from '@/lib/storage-validation';

const LIKED_SONGS_STORAGE_KEY = 'openspot_liked_songs';

function normalizeLikedSong(value: unknown): LikedSong | null {
  if (!isRecord(value)) return null;
  const id = typeof value.id === 'string' || (typeof value.id === 'number' && Number.isFinite(value.id)) ? value.id : null;
  if (id === null || typeof value.title !== 'string' || typeof value.artist !== 'string') return null;
  const images = isRecord(value.images) ? value.images : {};
  return {
    id,
    provider: typeof value.provider === 'string' ? value.provider : undefined,
    title: value.title,
    artist: value.artist,
    albumTitle: typeof value.albumTitle === 'string' ? value.albumTitle : '',
    duration: typeof value.duration === 'number' && Number.isFinite(value.duration) ? value.duration : 0,
    images: {
      small: typeof images.small === 'string' ? images.small : '',
      thumbnail: typeof images.thumbnail === 'string' ? images.thumbnail : '',
      large: typeof images.large === 'string' ? images.large : '',
      back: images.back === null ? null : typeof images.back === 'string' ? images.back : null,
    },
    likedAt: typeof value.likedAt === 'string' ? value.likedAt : new Date(0).toISOString(),
  };
}

interface LikedSong {
  id: string | number;
  provider?: string;
  title: string;
  artist: string;
  albumTitle?: string;
  duration?: number;
  images: {
    small: string;
    thumbnail: string;
    large: string;
    back: string | null;
  };
  likedAt: string; 
}

interface LikedSongsContextType {
  likedSongs: LikedSong[];
  isLoading: boolean;
  isLiked: (trackId: string | number) => boolean;
  likeSong: (track: Track) => void;
  unlikeSong: (trackId: string | number) => void;
  toggleLike: (track: Track) => void;
  likedCount: number;
  recentlyLiked: LikedSong[];
  clearAllLiked: () => void;
  getLikedSongsAsTrack: () => Track[];
}

const LikedSongsContext = createContext<LikedSongsContextType | undefined>(undefined);

interface LikedSongsProviderProps {
  children: ReactNode;
}

export function LikedSongsProvider({ children }: LikedSongsProviderProps) {
  const [likedSongs, setLikedSongs] = useState<LikedSong[]>([]);
  const [isLoading, setIsLoading] = useState(true);

  
  useEffect(() => {
    const loadLikedSongs = async () => {
      try {
        const savedLikedSongs = await AsyncStorage.getItem(LIKED_SONGS_STORAGE_KEY);
        if (savedLikedSongs) {
          const parsed = parseStoredJSON<unknown>(savedLikedSongs, LIKED_SONGS_STORAGE_KEY, []);
          setLikedSongs(Array.isArray(parsed) ? parsed.flatMap((value) => {
            const song = normalizeLikedSong(value);
            return song ? [song] : [];
          }) : []);
        }
      } catch (error) {
        console.error('Failed to load liked songs from AsyncStorage:', error);
        setLikedSongs([]);
      } finally {
        setIsLoading(false);
      }
    };

    loadLikedSongs();
  }, []);

  
  const saveLikedSongs = useCallback(async (songs: LikedSong[]) => {
    try {
      await AsyncStorage.setItem(LIKED_SONGS_STORAGE_KEY, JSON.stringify(songs));
    } catch (error) {
      console.error('Failed to save liked songs to AsyncStorage:', error);
    }
  }, []);

  
  const isLiked = useCallback((trackId: string | number): boolean => {
    return likedSongs.some(song => song.id === trackId);
  }, [likedSongs]);

  
  const likeSong = useCallback((track: Track) => {
    if (isLiked(track.id)) {
      return;
    }

    const likedSong: LikedSong = {
      id: track.id,
      provider: track.provider,
      title: track.title,
      artist: track.artist,
      albumTitle: track.albumTitle,
      duration: track.duration,
      images: track.images,
      likedAt: new Date().toISOString()
    };

    const updatedLikedSongs = [likedSong, ...likedSongs]; 
    setLikedSongs(updatedLikedSongs);
    saveLikedSongs(updatedLikedSongs);
  }, [likedSongs, isLiked, saveLikedSongs]);

  
  const unlikeSong = useCallback((trackId: string | number) => {
    const songToUnlike = likedSongs.find(song => song.id === trackId);
    if (!songToUnlike) {
      return;
    }

    const updatedLikedSongs = likedSongs.filter(song => song.id !== trackId);
    setLikedSongs(updatedLikedSongs);
    saveLikedSongs(updatedLikedSongs);
  }, [likedSongs, saveLikedSongs]);

  
  const toggleLike = useCallback((track: Track) => {
    if (isLiked(track.id)) {
      unlikeSong(track.id);
    } else {
      likeSong(track);
    }
  }, [isLiked, likeSong, unlikeSong]);

  
  const likedCount = likedSongs.length;

  
  const recentlyLiked = likedSongs.slice(0, 10);

  
  const clearAllLiked = useCallback(() => {
    setLikedSongs([]);
    saveLikedSongs([]);
  }, [saveLikedSongs]);

  
  const getLikedSongsAsTrack = useCallback((): Track[] => {
    return likedSongs.map(song => ({
      id: song.id,
      provider: song.provider,
      title: song.title,
      artist: song.artist,
      artistId: 0, 
      albumTitle: song.albumTitle || '',
      albumCover: song.images.large,
      albumId: '',
      releaseDate: '',
      genre: '',
      duration: song.duration || 0,
      audioQuality: {
        maximumBitDepth: 16,
        maximumSamplingRate: 44100,
        isHiRes: false
      },
      version: null,
      label: '',
      labelId: 0,
      upc: '',
      mediaCount: 1,
      parental_warning: false,
      streamable: true,
      purchasable: false,
      previewable: true,
      genreId: 0,
      genreSlug: '',
      genreColor: '',
      releaseDateStream: '',
      releaseDateDownload: '',
      maximumChannelCount: 2,
      images: song.images,
      isrc: ''
    }));
  }, [likedSongs]);

  const contextValue: LikedSongsContextType = {
    likedSongs,
    isLoading,
    isLiked,
    likeSong,
    unlikeSong,
    toggleLike,
    likedCount,
    recentlyLiked,
    clearAllLiked,
    getLikedSongsAsTrack
  };

  return (
    <LikedSongsContext.Provider value={contextValue}>
      {children}
    </LikedSongsContext.Provider>
  );
}

export function useLikedSongs(): LikedSongsContextType {
  const context = useContext(LikedSongsContext);
  if (context === undefined) {
    throw new Error('useLikedSongs must be used within a LikedSongsProvider');
  }
  return context;
}
