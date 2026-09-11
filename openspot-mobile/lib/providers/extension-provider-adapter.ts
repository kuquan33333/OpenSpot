import type { Album, Artist, PlaylistSearchItem, SearchParams, SearchResponse, Track } from '@/types/music';
import { extensionCoreBridge } from '@/lib/extensions/extension-core-bridge';
import type { InstalledExtension } from '@/lib/extensions/extension-types';
import { ProviderRegistry, type ProviderAdapter } from './provider-registry';

type LooseRecord = Record<string, unknown>;
const stringValue = (record: LooseRecord, ...keys: string[]): string => {
  for (const key of keys) if (typeof record[key] === 'string' && record[key]) return record[key] as string;
  return '';
};
const numberValue = (record: LooseRecord, ...keys: string[]): number => {
  for (const key of keys) if (typeof record[key] === 'number' && Number.isFinite(record[key])) return record[key] as number;
  return 0;
};
const imagesFor = (record: LooseRecord): Track['images'] => {
  const raw = record.images ?? record.image ?? record.cover_url ?? record.coverUrl;
  const url = typeof raw === 'string' ? raw : '';
  if (raw && typeof raw === 'object' && Array.isArray(raw)) {
    const first = raw.find((entry) => entry && typeof entry === 'object' && typeof (entry as LooseRecord).url === 'string') as LooseRecord | undefined;
    return imagesFor({ cover_url: first ? first.url : '' });
  }
  return { small: url, thumbnail: url, large: url, back: null };
};

export function normalizeExtensionTrack(raw: unknown, providerId: string): Track {
  const record = raw && typeof raw === 'object' ? raw as LooseRecord : {};
  const artists = Array.isArray(record.artists) ? record.artists : [];
  const artist = typeof record.artists === 'string' ? record.artists : artists.map((entry) => typeof entry === 'string' ? entry : stringValue((entry ?? {}) as LooseRecord, 'name')).filter(Boolean).join(', ');
  const images = imagesFor(record);
  const duration = numberValue(record, 'duration_ms', 'durationMs', 'duration');
  return {
    id: stringValue(record, 'id', 'provider_track_id', 'track_id') || `${providerId}:${stringValue(record, 'name', 'title')}`,
    provider: providerId,
    title: stringValue(record, 'name', 'title') || 'Untitled',
    artist: artist || stringValue(record, 'artist', 'artist_name') || 'Unknown artist',
    artistId: numberValue(record, 'artist_id'),
    albumTitle: stringValue(record, 'album_name', 'albumTitle', 'album'),
    albumCover: images.large,
    albumId: stringValue(record, 'album_id'),
    releaseDate: stringValue(record, 'release_date'),
    genre: stringValue(record, 'genre'),
    duration,
    audioQuality: { maximumBitDepth: 0, maximumSamplingRate: 0, isHiRes: false },
    version: null, label: stringValue(record, 'label'), labelId: 0, upc: stringValue(record, 'upc'), mediaCount: 1,
    parental_warning: Boolean(record.explicit), streamable: true, purchasable: false, previewable: false, genreId: 0,
    genreSlug: '', genreColor: '', releaseDateStream: stringValue(record, 'release_date'), releaseDateDownload: '', maximumChannelCount: 2,
    images, isrc: stringValue(record, 'isrc'),
  };
}

function normalizeResponse(raw: unknown, providerId: string): SearchResponse {
  const tracks = Array.isArray(raw) ? raw.map((item) => normalizeExtensionTrack(item, providerId)) : [];
  return { tracks, albums: [] as Album[], artists: [] as Artist[], playlists: [] as PlaylistSearchItem[], pagination: { offset: 0, total: tracks.length, hasMore: false } };
}

function getUrl(raw: unknown): string {
  const record = raw && typeof raw === 'object' ? raw as LooseRecord : {};
  const track = record.track && typeof record.track === 'object' ? record.track as LooseRecord : record;
  return stringValue(track, 'stream_url', 'audio_url', 'url', 'preview_url');
}

export function createExtensionProviderAdapter(extension: InstalledExtension): ProviderAdapter {
  const id = extension.id;
  return {
    id,
    displayName: extension.display_name || extension.name || id,
    capabilities: new Set(['search']),
    search: async (params: SearchParams) => params.type && params.type !== 'track' ? normalizeResponse([], id) : normalizeResponse(await extensionCoreBridge.searchMetadata(params.q, 20, true), id),
    searchTracks: async (query: string, _page = 1, limit = 20) => normalizeResponse(await extensionCoreBridge.searchMetadataProvider(id, query, limit), id),
    getStreamUrl: async (trackId: string) => {
      const url = getUrl(await extensionCoreBridge.getProviderMetadata(id, 'track', trackId));
      if (!url) throw new Error(`Extension provider ${id} returned no stream URL`);
      return url;
    },
    getDownloadUrl: async (trackId: string) => {
      const url = getUrl(await extensionCoreBridge.getProviderMetadata(id, 'track', trackId));
      if (!url) throw new Error(`Extension provider ${id} returned no download URL`);
      return url;
    },
  };
}

const registeredExtensionProviders = new Set<string>();

export async function syncExtensionProviders(): Promise<void> {
  if (!extensionCoreBridge.isAvailable()) return;
  const installed = await extensionCoreBridge.getInstalled();
  for (const providerId of registeredExtensionProviders) ProviderRegistry.unregister(providerId);
  registeredExtensionProviders.clear();
  for (const extension of installed) {
    const types = new Set([...(extension.types ?? []), ...(extension.capabilities ?? [])]);
    if (!extension.enabled || !types.has('metadata_provider')) continue;
    ProviderRegistry.register(createExtensionProviderAdapter(extension));
    registeredExtensionProviders.add(extension.id);
  }
}
