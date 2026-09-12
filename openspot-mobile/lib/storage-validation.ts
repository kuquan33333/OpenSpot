import type { Track } from '@/types/music';

export type JSONRecord = Record<string, unknown>;

export function isRecord(value: unknown): value is JSONRecord {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function stringValue(value: unknown, fallback = ''): string {
  return typeof value === 'string' ? value : fallback;
}

function numberValue(value: unknown, fallback = 0): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback;
}

function booleanValue(value: unknown, fallback = false): boolean {
  return typeof value === 'boolean' ? value : fallback;
}

function idValue(value: unknown): string | number | null {
  if (typeof value === 'number' && Number.isFinite(value)) return value;
  if (typeof value === 'string' && value.trim()) return value;
  return null;
}

function normalizeImages(value: unknown, fallbackCover = ''): Track['images'] {
  const images = isRecord(value) ? value : {};
  const small = stringValue(images.small, fallbackCover);
  const thumbnail = stringValue(images.thumbnail, small);
  const large = stringValue(images.large, thumbnail);
  const back = images.back === null ? null : typeof images.back === 'string' ? images.back : null;
  return { small, thumbnail, large, back };
}

/**
 * Converts persisted/provider data into the complete Track shape before it is
 * handed to playback or rendered. Old installs can contain partial records;
 * missing optional metadata receives safe defaults instead of crashing a tab.
 */
export function normalizeStoredTrack(value: unknown): Track | null {
  if (!isRecord(value)) return null;
  const id = idValue(value.id);
  const title = stringValue(value.title);
  const artist = stringValue(value.artist);
  if (id === null || !title.trim() || !artist.trim()) return null;

  const albumCover = stringValue(value.albumCover);
  const audioQuality = isRecord(value.audioQuality) ? value.audioQuality : {};
  const rawMeta = isRecord(value.audioMeta) ? value.audioMeta : null;
  const audioMeta = rawMeta
    ? {
        bpm: rawMeta.bpm === null ? null : numberValue(rawMeta.bpm, 0) || null,
        key: rawMeta.key === null ? null : stringValue(rawMeta.key) || null,
        keyScale: rawMeta.keyScale === null ? null : stringValue(rawMeta.keyScale) || null,
        source: ['provider', 'cache', 'local', 'unknown'].includes(String(rawMeta.source))
          ? (String(rawMeta.source) as NonNullable<Track['audioMeta']>['source'])
          : 'unknown',
      }
    : null;

  return {
    id,
    provider: stringValue(value.provider) || undefined,
    title,
    artist,
    artistId: numberValue(value.artistId),
    albumTitle: stringValue(value.albumTitle),
    albumCover,
    albumId: stringValue(value.albumId),
    releaseDate: stringValue(value.releaseDate),
    genre: stringValue(value.genre),
    duration: numberValue(value.duration),
    audioQuality: {
      maximumBitDepth: numberValue(audioQuality.maximumBitDepth, 16),
      maximumSamplingRate: numberValue(audioQuality.maximumSamplingRate, 44100),
      isHiRes: booleanValue(audioQuality.isHiRes),
    },
    version: value.version === null ? null : stringValue(value.version) || null,
    label: stringValue(value.label),
    labelId: numberValue(value.labelId),
    upc: stringValue(value.upc),
    mediaCount: numberValue(value.mediaCount, 1),
    parental_warning: booleanValue(value.parental_warning),
    streamable: booleanValue(value.streamable, true),
    purchasable: booleanValue(value.purchasable),
    previewable: booleanValue(value.previewable, true),
    genreId: numberValue(value.genreId),
    genreSlug: stringValue(value.genreSlug),
    genreColor: stringValue(value.genreColor),
    releaseDateStream: stringValue(value.releaseDateStream),
    releaseDateDownload: stringValue(value.releaseDateDownload),
    maximumChannelCount: numberValue(value.maximumChannelCount, 2),
    images: normalizeImages(value.images, albumCover),
    isrc: stringValue(value.isrc),
    audioMeta,
    isVideo: typeof value.isVideo === 'boolean' ? value.isVideo : undefined,
  };
}

export function parseStoredJSON<T>(raw: string | null, key: string, fallback: T): T {
  if (!raw) return fallback;
  try {
    return JSON.parse(raw) as T;
  } catch (error) {
    console.warn(`[Storage] Ignoring invalid JSON for ${key}`, error);
    return fallback;
  }
}
