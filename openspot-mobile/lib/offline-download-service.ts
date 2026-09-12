import AsyncStorage from '@react-native-async-storage/async-storage';
import * as FileSystem from 'expo-file-system';

import { MusicAPI } from '@/lib/music-api';
import { isRecord, normalizeStoredTrack, parseStoredJSON } from '@/lib/storage-validation';
import { OFFLINE_PLAYLIST_NAME, PlaylistStorage } from '@/lib/playlist-storage';
import type { Track } from '@/types/music';

export interface OfflineDownloadMetadata {
  fileUri: string;
  thumbUri: string | null;
  trackData: Track;
  downloadedAt: string;
}

export interface OfflineDownloadResult {
  fileUri: string;
  metadata: OfflineDownloadMetadata;
}

const metadataKey = (trackId: string) => `offline_${trackId}`;
const audioUri = (trackId: string) => `${FileSystem.documentDirectory}offline_${trackId}.mp3`;
const thumbUri = (trackId: string) => `${FileSystem.documentDirectory}offline_${trackId}.jpg`;
const tempAudioUri = (trackId: string) => `${FileSystem.documentDirectory}offline_${trackId}.mp3.part`;
const tempThumbUri = (trackId: string) => `${FileSystem.documentDirectory}offline_${trackId}.jpg.part`;

function hasContent(info: FileSystem.FileInfo): boolean {
  return info.exists && typeof info.size === 'number' && info.size > 0;
}

function normalizeMetadata(value: unknown): OfflineDownloadMetadata | null {
  if (!isRecord(value) || typeof value.fileUri !== 'string' || !value.fileUri.trim()) return null;
  const trackData = normalizeStoredTrack(value.trackData);
  if (!trackData) return null;
  return {
    fileUri: value.fileUri,
    thumbUri: typeof value.thumbUri === 'string' && value.thumbUri.trim() ? value.thumbUri : null,
    trackData,
    downloadedAt: typeof value.downloadedAt === 'string' ? value.downloadedAt : new Date(0).toISOString(),
  };
}

async function removeIfExists(uri: string | null | undefined): Promise<void> {
  if (!uri) return;
  await FileSystem.deleteAsync(uri, { idempotent: true }).catch(() => {});
}

async function replaceFile(sourceUri: string, destinationUri: string): Promise<void> {
  await removeIfExists(destinationUri);
  await FileSystem.moveAsync({ from: sourceUri, to: destinationUri });
}

async function writeMetadataAtomically(trackId: string, metadata: OfflineDownloadMetadata): Promise<void> {
  const key = metadataKey(trackId);
  const stagingKey = `${key}_staging`;
  const serialized = JSON.stringify(metadata);
  await AsyncStorage.setItem(stagingKey, serialized);
  await AsyncStorage.setItem(key, serialized);
  await AsyncStorage.removeItem(stagingKey);
}

export async function getOfflineDownload(trackId: string): Promise<OfflineDownloadMetadata | null> {
  const key = metadataKey(trackId);
  try {
    const raw = await AsyncStorage.getItem(key);
    const metadata = normalizeMetadata(parseStoredJSON<unknown>(raw, key, null));
    if (!metadata) return null;
    const info = await FileSystem.getInfoAsync(metadata.fileUri);
    if (!hasContent(info)) return null;
    if (metadata.thumbUri) {
      const thumbInfo = await FileSystem.getInfoAsync(metadata.thumbUri);
      if (!thumbInfo.exists) metadata.thumbUri = null;
    }
    return metadata;
  } catch (error) {
    console.warn(`[OfflineDownload] invalid metadata for ${trackId}:`, error);
    return null;
  }
}

export async function downloadTrack(
  track: Track,
  onProgress?: (progress: number) => void,
): Promise<OfflineDownloadResult> {
  const trackId = String(track.id);
  const documentDirectory = FileSystem.documentDirectory;
  if (!documentDirectory) throw new Error('Offline storage directory is unavailable');

  const existing = await getOfflineDownload(trackId);
  if (existing) return { fileUri: existing.fileUri, metadata: existing };

  const finalAudio = audioUri(trackId);
  const stagingAudio = tempAudioUri(trackId);
  const finalThumb = thumbUri(trackId);
  const stagingThumb = tempThumbUri(trackId);
  await removeIfExists(stagingAudio);
  await removeIfExists(stagingThumb);

  try {
    const sourceUrl = await MusicAPI.getDownloadUrl(trackId, track);
    const result = await FileSystem.downloadAsync(sourceUrl, stagingAudio);
    if (!result?.uri) throw new Error('Audio download returned no file');
    const audioInfo = await FileSystem.getInfoAsync(stagingAudio);
    if (!hasContent(audioInfo)) throw new Error('Downloaded audio file is empty');
    onProgress?.(0.85);
    await replaceFile(stagingAudio, finalAudio);

    let savedThumb: string | null = null;
    if (track.images?.large) {
      try {
        const thumbResult = await FileSystem.downloadAsync(track.images.large, stagingThumb);
        const downloadedThumb = await FileSystem.getInfoAsync(thumbResult.uri);
        if (hasContent(downloadedThumb)) {
          await replaceFile(stagingThumb, finalThumb);
          savedThumb = finalThumb;
        }
      } catch (error) {
        console.warn('[OfflineDownload] thumbnail unavailable; audio remains usable:', error);
      }
    }

    const metadata: OfflineDownloadMetadata = {
      fileUri: finalAudio,
      thumbUri: savedThumb,
      trackData: track,
      downloadedAt: new Date().toISOString(),
    };
    await writeMetadataAtomically(trackId, metadata);
    await PlaylistStorage.addTrackToPlaylists(track, [OFFLINE_PLAYLIST_NAME]);
    const verified = await getOfflineDownload(trackId);
    if (!verified) throw new Error('Offline metadata verification failed');
    onProgress?.(1);
    return { fileUri: verified.fileUri, metadata: verified };
  } catch (error) {
    await removeIfExists(stagingAudio);
    await removeIfExists(stagingThumb);
    throw error;
  }
}

export async function removeOfflineDownload(trackId: string): Promise<void> {
  const metadata = await getOfflineDownload(trackId);
  await removeIfExists(metadata?.fileUri ?? audioUri(trackId));
  await removeIfExists(metadata?.thumbUri ?? thumbUri(trackId));
  await removeIfExists(tempAudioUri(trackId));
  await removeIfExists(tempThumbUri(trackId));
  await AsyncStorage.removeItem(metadataKey(trackId));
  await AsyncStorage.removeItem(`${metadataKey(trackId)}_staging`);
  await PlaylistStorage.removeTrackFromPlaylist(trackId, OFFLINE_PLAYLIST_NAME);
}

export async function listOfflineDownloads(): Promise<OfflineDownloadResult[]> {
  const playlists = await PlaylistStorage.getPlaylists();
  const offline = playlists.find((playlist) => playlist.name === OFFLINE_PLAYLIST_NAME);
  if (!offline) return [];

  const entries: OfflineDownloadResult[] = [];
  const invalidIds: string[] = [];
  for (const trackId of offline.trackIds) {
    const metadata = await getOfflineDownload(trackId);
    if (metadata) entries.push({ fileUri: metadata.fileUri, metadata });
    else invalidIds.push(trackId);
  }
  if (invalidIds.length > 0) {
    const invalid = new Set(invalidIds);
    await PlaylistStorage.savePlaylists(playlists.map((playlist) => playlist.name === OFFLINE_PLAYLIST_NAME
      ? { ...playlist, trackIds: playlist.trackIds.filter((id) => !invalid.has(id)) }
      : playlist));
  }
  return entries;
}
