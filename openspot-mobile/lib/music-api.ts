import { SearchResponse, SearchParams, Track } from '../types/music';
import { MusicApi } from './api';
import { ProviderRegistry, type ProviderId } from './providers/provider-registry';
import AsyncStorage from '@react-native-async-storage/async-storage';

export class MusicAPI {
  private static searchCache = new Map<string, Promise<SearchResponse>>();
  private static streamCache = new Map<string, Promise<string>>();
  private static recentlyPlayedStorageKey = 'openspot_recently_played_tracks_v1';
  private static recentlyPlayedLimit = 30;

  private static resolveProviderHint(trackOrProvider?: Track | ProviderId): ProviderId | null {
    if (!trackOrProvider) return null;
    if (typeof trackOrProvider === 'string') return trackOrProvider;
    return trackOrProvider.provider || null;
  }

  static async search(params: SearchParams): Promise<SearchResponse> {
    return ProviderRegistry.search(params);
  }

  static async searchTracks(query: string, page: number = 1, limit: number = 20): Promise<SearchResponse> {
    return ProviderRegistry.searchTracks(query, page, limit);
  }

  static async getStreamUrl(trackId: string, trackOrProvider?: Track | ProviderId): Promise<string> {
    if (trackOrProvider && typeof trackOrProvider !== 'string') {
      const result = await ProviderRegistry.resolveStream(trackOrProvider);
      return result.url;
    }

    const providerHint = this.resolveProviderHint(trackOrProvider);
    if (providerHint) {
      const provider = ProviderRegistry.get(providerHint);
      if (provider?.capabilities.has('stream')) {
        return provider.getStreamUrl(trackId);
      }
    }

    const priority = await ProviderRegistry.getPriority();
    let lastError: unknown = null;
    for (const providerId of priority) {
      const provider = ProviderRegistry.get(providerId);
      if (!provider?.capabilities.has('stream')) continue;
      try {
        return await provider.getStreamUrl(trackId);
      } catch (error) {
        lastError = error;
      }
    }

    throw lastError instanceof Error ? lastError : new Error('No stream provider is currently available');
  }

  static async getDownloadUrl(trackId: string, trackOrProvider?: Track | ProviderId): Promise<string> {
    if (trackOrProvider && typeof trackOrProvider !== 'string') {
      const result = await ProviderRegistry.resolveDownload(trackOrProvider);
      return result.url;
    }

    const providerHint = this.resolveProviderHint(trackOrProvider);
    if (providerHint) {
      const provider = ProviderRegistry.get(providerHint);
      if (provider?.capabilities.has('download')) {
        const resolver = provider.getDownloadUrl || provider.getStreamUrl;
        return resolver(trackId);
      }
    }

    const priority = await ProviderRegistry.getPriority();
    let lastError: unknown = null;
    for (const providerId of priority) {
      const provider = ProviderRegistry.get(providerId);
      if (!provider?.capabilities.has('download')) continue;
      try {
        const resolver = provider.getDownloadUrl || provider.getStreamUrl;
        return await resolver(trackId);
      } catch (error) {
        lastError = error;
      }
    }

    throw lastError instanceof Error ? lastError : new Error('No download provider is currently available');
  }

  static async getPopularTracks(): Promise<Track[]> {
    return MusicApi.getPopularTracks();
  }

  static async getAlbumSongs(albumId: string): Promise<Track[]> {
    return MusicApi.getAlbumSongs(albumId);
  }

  static async getArtistSongs(artistId: string, page: number = 0): Promise<{ tracks: Track[]; total: number }> {
    return MusicApi.getArtistSongs(artistId, page);
  }

  static async getPlaylistSongs(playlistId: string): Promise<Track[]> {
    return MusicApi.getPlaylistSongs(playlistId);
  }

  static async getPlaylistSongsPaginated(playlistId: string, page = 0): Promise<{ tracks: Track[]; total: number }> {
    return MusicApi.getPlaylistSongsPaginated(playlistId, page);
  }

  static async getRecentlyPlayed(): Promise<Track[]> {
    try {
      const stored = await AsyncStorage.getItem(this.recentlyPlayedStorageKey);
      if (!stored) return [];
      const parsed = JSON.parse(stored) as Track[];
      return Array.isArray(parsed) ? parsed : [];
    } catch (error) {
      console.error('Failed to read recently played tracks:', error);
      return [];
    }
  }

  static async addToRecentlyPlayed(track: Track): Promise<void> {
    try {
      const existing = await this.getRecentlyPlayed();
      const deduped = existing.filter((item) => item.id.toString() !== track.id.toString());
      const next = [track, ...deduped].slice(0, this.recentlyPlayedLimit);
      await AsyncStorage.setItem(this.recentlyPlayedStorageKey, JSON.stringify(next));
    } catch (error) {
      console.error('Failed to save recently played track:', error);
    }
  }

  static async clearRecentlyPlayed(): Promise<void> {
    try {
      await AsyncStorage.removeItem(this.recentlyPlayedStorageKey);
    } catch (error) {
      console.error('Failed to clear recently played tracks:', error);
    }
  }

  static async getMadeForYou(): Promise<Track[]> {
    return MusicApi.getMadeForYou();
  }

  static async resolveTrackById(trackId: string, preferredProvider?: ProviderId): Promise<Track | null> {
    try {
      const response = await ProviderRegistry.searchTracks(trackId, 1, 20, preferredProvider || null);
      return response.tracks[0] || null;
    } catch {
      return null;
    }
  }

  static formatDuration(duration: number): string {
    const seconds = Math.floor(duration);
    const minutes = Math.floor(seconds / 60);
    const remainingSeconds = seconds % 60;
    const hours = Math.floor(minutes / 60);
    const remainingMinutes = minutes % 60;
    if (hours > 0) {
      return `${hours}:${remainingMinutes.toString().padStart(2, '0')}:${remainingSeconds.toString().padStart(2, '0')}`;
    }
    return `${minutes}:${remainingSeconds.toString().padStart(2, '0')}`;
  }

  static getOptimalImage(images: { small: string; thumbnail: string; large: string }): string {
    return images.large || images.small || images.thumbnail;
  }

  static isHighQuality(track: Track): boolean {
    return track.audioQuality.isHiRes || track.audioQuality.maximumBitDepth >= 24;
  }

  static getQualityBadge(track: Track): string | null {
    if (track.audioQuality.isHiRes) return 'Hi-Res';
    if (track.audioQuality.maximumBitDepth >= 24) return 'HD';
    return null;
  }

  static clearCache(): void {
    this.searchCache.clear();
    this.streamCache.clear();
  }

  static clearSearchCache(): void {
    this.searchCache.clear();
  }

  static clearStreamCache(): void {
    this.streamCache.clear();
  }
}
