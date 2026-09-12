import AsyncStorage from '@react-native-async-storage/async-storage';

import type { Track } from '@/types/music';
import type { SongAudioMeta } from './auto-mix-types';
import { recordDiagnostic } from '@/lib/diagnostics';
import { isRecord, parseStoredJSON } from '@/lib/storage-validation';

const AUDIO_META_KEY_PREFIX = 'openspot_audio_meta_v1:';
export const AUDIO_META_CACHE_TTL_MS = 30 * 24 * 60 * 60 * 1000;

type StoredAudioMeta = { meta: SongAudioMeta; expiresAt: number };

function cacheKey(track: Pick<Track, 'provider' | 'id'>): string {
  return `${AUDIO_META_KEY_PREFIX}${track.provider || 'unknown'}:${String(track.id)}`;
}

function normalizeMeta(value: unknown, source: SongAudioMeta['source']): SongAudioMeta | null {
  if (!value || typeof value !== 'object') return null;
  const candidate = value as Record<string, unknown>;
  const bpm = typeof candidate.bpm === 'number' && Number.isFinite(candidate.bpm) && candidate.bpm > 0 ? candidate.bpm : null;
  const key = typeof candidate.key === 'string' && candidate.key.trim() ? candidate.key.trim() : null;
  const keyScale = typeof candidate.keyScale === 'string' && candidate.keyScale.trim() ? candidate.keyScale.trim() : null;
  return bpm || key ? { bpm, key, keyScale, source } : null;
}

export async function getCachedAudioMeta(track: Pick<Track, 'provider' | 'id'>): Promise<SongAudioMeta | null> {
  try {
    const raw = await AsyncStorage.getItem(cacheKey(track));
    if (!raw) return null;
    const stored = parseStoredJSON<unknown>(raw, cacheKey(track), null);
    if (!isRecord(stored)) return null;
    const expiresAt = typeof stored.expiresAt === 'number' ? stored.expiresAt : 0;
    if (!expiresAt || expiresAt <= Date.now()) {
      await AsyncStorage.removeItem(cacheKey(track));
      return null;
    }
    return normalizeMeta(stored.meta, 'cache');
  } catch {
    return null;
  }
}

export async function saveAudioMeta(track: Pick<Track, 'provider' | 'id'>, meta: SongAudioMeta): Promise<void> {
  const normalized = normalizeMeta(meta, meta.source || 'unknown');
  if (!normalized) return;
  await AsyncStorage.setItem(cacheKey(track), JSON.stringify({ meta: normalized, expiresAt: Date.now() + AUDIO_META_CACHE_TTL_MS } satisfies StoredAudioMeta));
}

export async function resolveAudioMeta(track: Track): Promise<SongAudioMeta | null> {
  const providerMeta = normalizeMeta(track.audioMeta, 'provider');
  if (providerMeta) {
    await saveAudioMeta(track, providerMeta).catch(() => {});
    return providerMeta;
  }
  return getCachedAudioMeta(track);
}

export async function clearAudioMetaCache(): Promise<void> {
  const keys = await AsyncStorage.getAllKeys();
  const metaKeys = keys.filter((key) => key.startsWith(AUDIO_META_KEY_PREFIX));
  if (metaKeys.length) await AsyncStorage.multiRemove(metaKeys);
  recordDiagnostic({ category: 'cache', type: 'clear', data: { scope: 'audio_meta', entries: metaKeys.length } });
}
