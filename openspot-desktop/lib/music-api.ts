import { SearchResponse, SearchParams, Track } from '../types/music';
import { ProviderRegistry, type ProviderId, type ProviderHomeSection } from './providers/provider-registry';
import { HOME_SECTION_DEFINITIONS } from './providers/home-sections';
import { extensionCoreBridge } from './extensions/extension-core-bridge';
import { normalizeExtensionTrack } from './providers/extension-provider-adapter';
import AsyncStorage from '@react-native-async-storage/async-storage';

export class MusicAPI {
  private static readonly searchCacheTtlMs = 5 * 60 * 1000;
  private static readonly streamCacheTtlMs = 5 * 60 * 1000;
  private static searchCache = new Map<string, { expiresAt: number; promise: Promise<SearchResponse> }>();
  private static streamCache = new Map<string, { expiresAt: number; promise: Promise<string> }>();
  private static recentlyPlayedStorageKey = 'openspot_recently_played_tracks_v1';
  private static recentlyPlayedLimit = 30;

  private static cache<T>(
    cache: Map<string, { expiresAt: number; promise: Promise<T> }>,
    key: string,
    ttlMs: number,
    loader: () => Promise<T>,
  ): Promise<T> {
    const existing = cache.get(key);
    if (existing && existing.expiresAt > Date.now()) return existing.promise;
    if (existing) cache.delete(key);

    let promise: Promise<T>;
    promise = loader().catch((error) => {
      if (cache.get(key)?.promise === promise) cache.delete(key);
      throw error;
    });
    cache.set(key, { expiresAt: Date.now() + ttlMs, promise });
    return promise;
  }

  private static searchKey(params: SearchParams): string {
    return JSON.stringify(params);
  }

  private static streamKey(trackId: string, trackOrProvider?: Track | ProviderId): string {
    if (typeof trackOrProvider === 'string') return `provider:${trackOrProvider}:${trackId}`;
    if (trackOrProvider) return `track:${trackOrProvider.provider || 'unknown'}:${trackOrProvider.id}`;
    return `auto:${trackId}`;
  }

  private static resolveProviderHint(trackOrProvider?: Track | ProviderId): ProviderId | null {
    if (!trackOrProvider) return null;
    if (typeof trackOrProvider === 'string') return trackOrProvider;
    return trackOrProvider.provider || null;
  }

  static async search(params: SearchParams): Promise<SearchResponse> {
    return this.cache(this.searchCache, this.searchKey(params), this.searchCacheTtlMs, () => ProviderRegistry.search(params));
  }

  static async searchTracks(query: string, offset: number = 0, limit: number = 20): Promise<SearchResponse> {
    const key = JSON.stringify(['tracks', query, offset, limit]);
    return this.cache(this.searchCache, key, this.searchCacheTtlMs, () => ProviderRegistry.searchTracks(query, offset, limit));
  }

  static async getStreamUrl(trackId: string, trackOrProvider?: Track | ProviderId): Promise<string> {
    if (trackOrProvider && typeof trackOrProvider !== 'string') {
      const key = this.streamKey(trackId, trackOrProvider);
      return this.cache(this.streamCache, key, this.streamCacheTtlMs, async () => (await ProviderRegistry.resolveStream(trackOrProvider)).url);
    }

    const providerHint = this.resolveProviderHint(trackOrProvider);
    const key = this.streamKey(trackId, trackOrProvider);
    return this.cache(this.streamCache, key, this.streamCacheTtlMs, async () => {
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
    });
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
    const sections = await this.getHomeSections();
    return sections.find((section) => section.id === 'trending')?.tracks ?? [];
  }

  static async getAlbumSongs(albumId: string): Promise<Track[]> {
    return this.getCollectionTracks('album', albumId);
  }

  static async getArtistSongs(artistId: string, page: number = 0): Promise<{ tracks: Track[]; total: number }> {
    const tracks = await this.getCollectionTracks('artist', artistId);
    const pageSize = 20;
    return { tracks: tracks.slice(page * pageSize, (page + 1) * pageSize), total: tracks.length };
  }

  static async getPlaylistSongs(playlistId: string): Promise<Track[]> {
    return this.getCollectionTracks('playlist', playlistId);
  }

  static async getPlaylistSongsPaginated(playlistId: string, page = 0): Promise<{ tracks: Track[]; total: number }> {
    const tracks = await this.getCollectionTracks('playlist', playlistId);
    const pageSize = 20;
    return { tracks: tracks.slice(page * pageSize, (page + 1) * pageSize), total: tracks.length };
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
    const sections = await this.getHomeSections();
    return sections.find((section) => section.id === 'chill')?.tracks ?? [];
  }

  static async getHomeSections(): Promise<ProviderHomeSection[]> {
    return ProviderRegistry.getHomeSections(HOME_SECTION_DEFINITIONS);
  }

  private static async getCollectionTracks(resourceType: 'album' | 'artist' | 'playlist', resourceId: string): Promise<Track[]> {
    let lastError: unknown = null;
    for (const provider of ProviderRegistry.list()) {
      if (!provider.capabilities.has('search')) continue;
      try {
        const payload = await extensionCoreBridge.getProviderMetadata(provider.id, resourceType, resourceId);
        const record = payload && typeof payload === 'object' ? payload as Record<string, unknown> : {};
        const rawTracks: unknown[] = [];
        const append = (value: unknown) => { if (Array.isArray(value)) rawTracks.push(...value); };
        append(record.track_list);
        append(record.tracks);
        if (resourceType === 'artist') {
          append(record.top_tracks);
          if (Array.isArray(record.releases)) {
            for (const release of record.releases) {
              if (release && typeof release === 'object') append((release as Record<string, unknown>).tracks);
            }
          }
        }
        const tracks = rawTracks.map((item) => normalizeExtensionTrack(item, provider.id));
        if (tracks.length > 0) return tracks;
      } catch (error) {
        lastError = error;
      }
    }
    throw lastError instanceof Error ? lastError : new Error(`No Extension provider could load ${resourceType}`);
  }

  static async resolveTrackById(trackId: string, preferredProvider?: ProviderId): Promise<Track | null> {
    try {
      const response = await ProviderRegistry.searchTracks(trackId, 0, 20, preferredProvider || null);
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
