import AsyncStorage from '@react-native-async-storage/async-storage';

import { MusicApi } from '../api';
import { YTMusicAPI } from '../ytmusic-api';
import type { SearchParams, SearchResponse, Track } from '../../types/music';

export type ProviderId = string;
export type ProviderCapability = 'search' | 'stream' | 'download';

export interface ProviderAdapter {
  id: ProviderId;
  displayName: string;
  capabilities: ReadonlySet<ProviderCapability>;
  search(params: SearchParams): Promise<SearchResponse>;
  searchTracks(query: string, offset?: number, limit?: number): Promise<SearchResponse>;
  getStreamUrl(trackId: string, track?: Track): Promise<string>;
  getDownloadUrl?(trackId: string, track?: Track): Promise<string>;
}

export interface ProviderResolveResult {
  providerId: ProviderId;
  url: string;
  track: Track;
  fallbackUsed: boolean;
}

export type ProviderRegistryEvent =
  | { type: 'resolve_start'; providerId: ProviderId; trackId: string }
  | { type: 'resolve_success'; providerId: ProviderId; trackId: string; fallbackUsed: boolean }
  | { type: 'resolve_failed'; providerId: ProviderId; trackId: string; error: string }
  | { type: 'fallback'; fromProviderId: ProviderId | null; toProviderId: ProviderId; trackId: string };

const LEGACY_PROVIDER_KEY = 'openspot_provider_v1';
const PROVIDER_PRIORITY_KEY = 'openspot_provider_priority_v2';

function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message;
  return String(error);
}

function normalizeText(value: string | undefined | null): string {
  return (value || '')
    .toLowerCase()
    .normalize('NFD')
    .replace(/[\u0300-\u036f]/g, '')
    .replace(/[^a-z0-9]+/g, ' ')
    .trim();
}

function trackMatchScore(source: Track, candidate: Track): number {
  if (source.isrc && candidate.isrc && source.isrc.toLowerCase() === candidate.isrc.toLowerCase()) {
    return 1000;
  }

  const sourceTitle = normalizeText(source.title);
  const candidateTitle = normalizeText(candidate.title);
  const sourceArtist = normalizeText(source.artist);
  const candidateArtist = normalizeText(candidate.artist);

  let score = 0;
  if (sourceTitle === candidateTitle) score += 500;
  else if (sourceTitle && candidateTitle && (sourceTitle.includes(candidateTitle) || candidateTitle.includes(sourceTitle))) score += 250;

  if (sourceArtist === candidateArtist) score += 300;
  else if (sourceArtist && candidateArtist && (sourceArtist.includes(candidateArtist) || candidateArtist.includes(sourceArtist))) score += 120;

  if (source.duration > 0 && candidate.duration > 0) {
    const deltaMs = Math.abs(source.duration - candidate.duration);
    if (deltaMs <= 2_500) score += 100;
    else if (deltaMs <= 7_500) score += 40;
  }

  return score;
}

const saavnProvider: ProviderAdapter = {
  id: 'saavn',
  displayName: 'Saavn',
  capabilities: new Set<ProviderCapability>(['search', 'stream', 'download']),
  search: (params) => MusicApi.search(params),
  searchTracks: (query, offset = 0, limit = 20) => MusicApi.searchTracks(query, offset, limit),
  getStreamUrl: (trackId) => MusicApi.getStreamUrl(trackId),
  getDownloadUrl: (trackId) => MusicApi.getStreamUrl(trackId),
};

const youtubeProvider: ProviderAdapter = {
  id: 'ytmusic',
  displayName: 'YouTube Music',
  capabilities: new Set<ProviderCapability>(['search', 'stream', 'download']),
  search: async (params) => {
    if (params.type && params.type !== 'track') {
      throw new Error(`Provider ytmusic does not support search type: ${params.type}`);
    }
    return YTMusicAPI.search({ q: params.q, type: params.type });
  },
  searchTracks: (query) => YTMusicAPI.search({ q: query, type: 'track' }),
  getStreamUrl: (trackId) => YTMusicAPI.getStreamUrl(trackId),
  getDownloadUrl: (trackId) => YTMusicAPI.getDownloadUrl(trackId),
};

class ProviderRegistryImpl {
  private readonly providers = new Map<ProviderId, ProviderAdapter>();
  private readonly listeners = new Set<(event: ProviderRegistryEvent) => void>();

  constructor() {
    this.register(saavnProvider);
    this.register(youtubeProvider);
  }

  register(provider: ProviderAdapter): void {
    if (!provider.id.trim()) throw new Error('Provider id must not be empty');
    this.providers.set(provider.id, provider);
  }

  unregister(providerId: ProviderId): void {
    this.providers.delete(providerId);
  }

  list(): ProviderAdapter[] {
    return [...this.providers.values()];
  }

  get(providerId: ProviderId): ProviderAdapter | undefined {
    return this.providers.get(providerId);
  }

  subscribe(listener: (event: ProviderRegistryEvent) => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  private emit(event: ProviderRegistryEvent): void {
    for (const listener of this.listeners) listener(event);
    if (__DEV__) console.debug('[ProviderRegistry]', event);
  }

  async getPriority(): Promise<ProviderId[]> {
    const available = new Set(this.providers.keys());
    let stored: ProviderId[] = [];

    try {
      const raw = await AsyncStorage.getItem(PROVIDER_PRIORITY_KEY);
      if (raw) {
        const parsed = JSON.parse(raw);
        if (Array.isArray(parsed)) stored = parsed.filter((id): id is string => typeof id === 'string');
      }
    } catch {}

    let legacyPreferred: ProviderId | null = null;
    try {
      const legacy = await AsyncStorage.getItem(LEGACY_PROVIDER_KEY);
      if (legacy && available.has(legacy)) legacyPreferred = legacy;
    } catch {}

    const result: ProviderId[] = [];
    const add = (id: ProviderId | null) => {
      if (id && available.has(id) && !result.includes(id)) result.push(id);
    };

    add(legacyPreferred);
    for (const id of stored) add(id);
    for (const id of available) add(id);
    return result;
  }

  async setPriority(priority: ProviderId[]): Promise<void> {
    const known = new Set(this.providers.keys());
    const normalized = priority.filter((id, index) => known.has(id) && priority.indexOf(id) === index);
    for (const id of known) if (!normalized.includes(id)) normalized.push(id);
    await AsyncStorage.setItem(PROVIDER_PRIORITY_KEY, JSON.stringify(normalized));
  }

  private async orderedProviders(hint?: ProviderId | null): Promise<ProviderAdapter[]> {
    const priority = await this.getPriority();
    const ids = hint ? [hint, ...priority.filter((id) => id !== hint)] : priority;
    return ids.map((id) => this.providers.get(id)).filter((provider): provider is ProviderAdapter => Boolean(provider));
  }

  async search(params: SearchParams, providerHint?: ProviderId | null): Promise<SearchResponse> {
    const providers = await this.orderedProviders(providerHint);
    let lastError: unknown = null;
    let emptyResult: SearchResponse | null = null;

    for (const provider of providers) {
      if (!provider.capabilities.has('search')) continue;
      try {
        const result = await provider.search(params);
        if (!emptyResult) emptyResult = result;
        const hasResults = result.tracks.length || result.albums.length || result.artists.length || result.playlists.length;
        if (hasResults) return result;
      } catch (error) {
        lastError = error;
      }
    }

    if (emptyResult) return emptyResult;
    throw lastError instanceof Error ? lastError : new Error('No search provider is currently available');
  }

  async searchTracks(query: string, offset = 0, limit = 20, providerHint?: ProviderId | null): Promise<SearchResponse> {
    const providers = await this.orderedProviders(providerHint);
    let lastError: unknown = null;
    let emptyResult: SearchResponse | null = null;

    for (const provider of providers) {
      if (!provider.capabilities.has('search')) continue;
      try {
        const result = await provider.searchTracks(query, offset, limit);
        if (!emptyResult) emptyResult = result;
        if (result.tracks.length) return result;
      } catch (error) {
        lastError = error;
      }
    }

    if (emptyResult) return emptyResult;
    throw lastError instanceof Error ? lastError : new Error('No track search provider is currently available');
  }

  private async findEquivalentTrack(provider: ProviderAdapter, sourceTrack: Track): Promise<Track | null> {
    const query = `${sourceTrack.title} ${sourceTrack.artist}`.trim();
    if (!query) return null;
    const response = await provider.searchTracks(query, 0, 10);
    if (!response.tracks.length) return null;

    const ranked = response.tracks
      .map((track) => ({ track, score: trackMatchScore(sourceTrack, track) }))
      .sort((a, b) => b.score - a.score);

    return ranked[0]?.score > 0 ? ranked[0].track : response.tracks[0];
  }

  async resolveStream(track: Track): Promise<ProviderResolveResult> {
    const hintedProvider = track.provider || null;
    const providers = await this.orderedProviders(hintedProvider);
    let lastError: unknown = null;
    let previousProvider: ProviderId | null = hintedProvider;

    for (let index = 0; index < providers.length; index += 1) {
      const provider = providers[index];
      if (!provider.capabilities.has('stream')) continue;

      let candidate = track;
      const isOriginalProvider = provider.id === hintedProvider || (!hintedProvider && index === 0);
      if (!isOriginalProvider) {
        this.emit({ type: 'fallback', fromProviderId: previousProvider, toProviderId: provider.id, trackId: String(track.id) });
        try {
          const matched = await this.findEquivalentTrack(provider, track);
          if (!matched) {
            previousProvider = provider.id;
            continue;
          }
          candidate = matched;
        } catch (error) {
          lastError = error;
          previousProvider = provider.id;
          continue;
        }
      }

      this.emit({ type: 'resolve_start', providerId: provider.id, trackId: String(candidate.id) });
      try {
        const url = await provider.getStreamUrl(String(candidate.id), candidate);
        if (!url || (!/^https?:\/\//i.test(url) && !url.startsWith('file:') && !url.startsWith('asset:'))) {
          throw new Error('Provider returned an invalid stream URL');
        }
        const fallbackUsed = !isOriginalProvider;
        this.emit({ type: 'resolve_success', providerId: provider.id, trackId: String(candidate.id), fallbackUsed });
        return { providerId: provider.id, url, track: { ...candidate, provider: provider.id }, fallbackUsed };
      } catch (error) {
        lastError = error;
        this.emit({ type: 'resolve_failed', providerId: provider.id, trackId: String(candidate.id), error: errorMessage(error) });
        previousProvider = provider.id;
      }
    }

    throw lastError instanceof Error ? lastError : new Error('No stream provider could resolve this track');
  }

  async resolveDownload(track: Track): Promise<ProviderResolveResult> {
    const hintedProvider = track.provider || null;
    const providers = await this.orderedProviders(hintedProvider);
    let lastError: unknown = null;

    for (let index = 0; index < providers.length; index += 1) {
      const provider = providers[index];
      if (!provider.capabilities.has('download')) continue;

      let candidate = track;
      const isOriginalProvider = provider.id === hintedProvider || (!hintedProvider && index === 0);
      if (!isOriginalProvider) {
        try {
          const matched = await this.findEquivalentTrack(provider, track);
          if (!matched) continue;
          candidate = matched;
        } catch (error) {
          lastError = error;
          continue;
        }
      }

      try {
        const resolver = provider.getDownloadUrl || provider.getStreamUrl;
        const url = await resolver(String(candidate.id), candidate);
        if (!url) throw new Error('Provider returned an empty download URL');
        return { providerId: provider.id, url, track: { ...candidate, provider: provider.id }, fallbackUsed: !isOriginalProvider };
      } catch (error) {
        lastError = error;
      }
    }

    throw lastError instanceof Error ? lastError : new Error('No download provider could resolve this track');
  }
}

export const ProviderRegistry = new ProviderRegistryImpl();
export { PROVIDER_PRIORITY_KEY };
