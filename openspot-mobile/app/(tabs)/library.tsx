import React, { useState, useContext, useCallback } from 'react';
import { View, StyleSheet, StatusBar, ScrollView, TouchableOpacity, Text, Modal, TextInput, FlatList, Alert } from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { PlaylistList } from '@/components/PlaylistList';
import { PlaylistCard } from '@/components/PlaylistCard';
import { useLikedSongs } from '@/hooks/useLikedSongs';
import { MusicPlayerContext } from './_layout';
import { Ionicons } from '@expo/vector-icons';
import { PlaylistStorage, Playlist } from '@/lib/playlist-storage';
import { MusicAPI } from '@/lib/music-api';
import { useFocusEffect, useRouter } from 'expo-router';
import { useColorScheme } from '@/hooks/useColorScheme';
import { useTranslation } from 'react-i18next';
import AsyncStorage from '@react-native-async-storage/async-storage';
import { Image } from 'expo-image';
import { importSpotifyPlaylist } from '@/lib/spotify-import';
import { ErrorBoundary } from '@/components/ErrorBoundary';
import { isRecord, parseStoredJSON } from '@/lib/storage-validation';

interface SavedMediaItem {
  type: 'album' | 'artist' | 'playlist';
  id: string;
  title: string;
  image: string;
  totalSongs?: number;
}

function normalizeSavedMedia(value: unknown): SavedMediaItem | null {
  if (!isRecord(value)) return null;
  const type = value.type;
  if (type !== 'album' && type !== 'artist' && type !== 'playlist') return null;
  const id = typeof value.id === 'string' || typeof value.id === 'number' ? String(value.id) : '';
  const title = typeof value.title === 'string' ? value.title : '';
  if (!id || !title.trim()) return null;
  return {
    type,
    id,
    title,
    image: typeof value.image === 'string' ? value.image : '',
    totalSongs: typeof value.totalSongs === 'number' && Number.isFinite(value.totalSongs) ? value.totalSongs : undefined,
  };
}

function LibraryScreenContent() {
  const { t } = useTranslation();
  const router = useRouter();
  const colorScheme = useColorScheme();
  const isDark = colorScheme !== 'light';
  const theme = {
    background: isDark ? '#050505' : '#f5efe6',
    surface: isDark ? '#121212' : '#fffaf2',
    surfaceAlt: isDark ? '#1b1b1b' : '#efe4d6',
    textPrimary: isDark ? '#fff' : '#2d2219',
    textSecondary: isDark ? '#888' : '#7a6251',
    border: isDark ? '#272727' : '#e4d5c5',
    accent: isDark ? '#1DB954' : '#167c3a',
  };
  const { handleTrackSelect, currentTrack, isPlaying } = useContext(MusicPlayerContext);
  const { getLikedSongsAsTrack, isLiked, toggleLike } = useLikedSongs();
  const likedTracks = getLikedSongsAsTrack();

  const [playlists, setPlaylists] = useState<Playlist[]>([]);
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [newPlaylistName, setNewPlaylistName] = useState('');
  const [selectedPlaylist, setSelectedPlaylist] = useState<Playlist | null>(null);
  const [playlistTracks, setPlaylistTracks] = useState<any[]>([]);
  const [showLikedSongs, setShowLikedSongs] = useState(false);
  const [savedMedia, setSavedMedia] = useState<SavedMediaItem[]>([]);
  const [showImportModal, setShowImportModal] = useState(false);
  const [importUrl, setImportUrl] = useState('');
  const [importName, setImportName] = useState('');
  const [importStatus, setImportStatus] = useState<'idle' | 'fetching' | 'resolving' | 'done' | 'error'>('idle');
  const [importProgress, setImportProgress] = useState({ current: 0, total: 0 });
  const [playlistCovers, setPlaylistCovers] = useState<Record<string, string>>({});

  const fetchSavedMedia = useCallback(async () => {
    const allKeys = await AsyncStorage.getAllKeys();
    const savedKeys = allKeys.filter(key => key.startsWith('saved_'));
    const savedItems = await Promise.all(
      savedKeys.map(async (key) => {
        try {
          const data = await AsyncStorage.getItem(key);
          return normalizeSavedMedia(parseStoredJSON<unknown>(data, key, null));
        } catch (error) {
          console.warn(`[Library] Failed to load saved media ${key}`, error);
          return null;
        }
      })
    );
    setSavedMedia(savedItems.flatMap((item) => item ? [item] : []));
  }, []);

  const handleRemoveSavedMedia = async (key: string) => {
    try {
      await AsyncStorage.removeItem(key);
      await fetchSavedMedia();
    } catch (error) {
      console.warn(`[Library] Failed to remove saved media ${key}`, error);
    }
  };

  const handleSavedMediaPress = (item: SavedMediaItem) => {
    router.push(`/media/${item.type}/${item.id}?title=${encodeURIComponent(item.title)}&image=${encodeURIComponent(item.image)}`);
  };

  const refreshSelectedPlaylistTracks = async (playlistName: string) => {
    try {
      const updated = (await PlaylistStorage.getPlaylists()).find(pl => pl.name === playlistName);
      if (!updated) {
        setSelectedPlaylist(null);
        setPlaylistTracks([]);
        return;
      }
      setSelectedPlaylist(updated);
      const tracks = await PlaylistStorage.getPlaylistTracks(updated);
      setPlaylistTracks(tracks);
    } catch (error) {
      console.warn(`[Library] Failed to load playlist ${playlistName}`, error);
      setSelectedPlaylist(null);
      setPlaylistTracks([]);
    }
  };

  const fetchPlaylists = useCallback(async () => {
    try {
      const pls = await PlaylistStorage.getPlaylists();
      const filteredPlaylists = pls.filter(pl => pl.name !== 'offline');
      setPlaylists(filteredPlaylists);

      const covers: Record<string, string> = {};
      for (const pl of filteredPlaylists) {
        if (pl.trackIds.length > 0) {
          const track = await PlaylistStorage.getTrackData(pl.trackIds[0]);
          if (track && track.images) {
            covers[pl.name] = MusicAPI.getOptimalImage(track.images);
          }
        }
      }
      setPlaylistCovers(covers);
    } catch (error) {
      console.warn('[Library] Failed to load playlists', error);
      setPlaylists([]);
      setPlaylistCovers({});
    }
  }, []);

  useFocusEffect(
    React.useCallback(() => {
      let active = true;
      void Promise.all([fetchPlaylists(), fetchSavedMedia()]).catch((error) => {
        if (active) console.warn('[Library] Focus refresh failed', error);
      });
      return () => {
        active = false;
      };
    }, [fetchPlaylists, fetchSavedMedia])
  );

  const handlePlaylistPress = async (playlist: Playlist) => {
    await refreshSelectedPlaylistTracks(playlist.name);
  };

  const handleCreatePlaylist = async () => {
    setShowCreateModal(true);
  };

  const handleCreatePlaylistSubmit = async () => {
    if (!newPlaylistName.trim()) return;
    try {
      await PlaylistStorage.addPlaylist({
        name: newPlaylistName.trim(),
        cover: '',
        trackIds: [],
      });
      setNewPlaylistName('');
      setShowCreateModal(false);
      await fetchPlaylists();
    } catch (error) {
      console.warn('[Library] Failed to create playlist', error);
    }
  };

  const handleImportSpotify = async () => {
    if (!importUrl.trim() || !importName.trim()) return;
    setImportStatus('fetching');
    setImportProgress({ current: 0, total: 0 });
    try {
      const result = await importSpotifyPlaylist(
        importUrl.trim(),
        importName.trim(),
        (msg, current, total) => {
          if (current !== undefined && total !== undefined) {
            setImportProgress({ current, total });
            if (current < total) {
              setImportStatus('resolving');
            } else {
              setImportStatus('done');
            }
          }
        }
      );
      setImportStatus(result.success ? 'done' : 'error');
    } catch (e) {
      console.error('Spotify import failed:', e);
      setImportStatus('error');
    }
  };

  const handleBackToLibrary = () => {
    setSelectedPlaylist(null);
    setShowLikedSongs(false);
    setPlaylistTracks([]);
  };

  const handleRemoveTrackFromPlaylist = async (trackId: string, playlistName: string) => {
    try {
      await PlaylistStorage.removeTrackFromPlaylist(trackId, playlistName);
      await fetchPlaylists();
      if (selectedPlaylist) await refreshSelectedPlaylistTracks(playlistName);
    } catch (error) {
      console.warn(`[Library] Failed to remove track ${trackId}`, error);
    }
  };

  const handlePlaylistPlay = async (playlist: Playlist, shuffle = false) => {
    try {
      const tracks = await PlaylistStorage.getPlaylistTracks(playlist);
      if (tracks.length > 0) {
        let playTracks = tracks;
        if (shuffle) {
          playTracks = [...tracks].sort(() => Math.random() - 0.5);
        }
        handleTrackSelect(playTracks[0], playTracks, 0);
      }
    } catch (error) {
      console.warn(`[Library] Failed to play playlist ${playlist.name}`, error);
    }
  };

  const handleLikedSongsPlay = (shuffle = false) => {
    let playTracks = likedTracks;
    if (shuffle) {
      playTracks = [...likedTracks].sort(() => Math.random() - 0.5);
    }
    if (playTracks.length > 0) {
      handleTrackSelect(playTracks[0], playTracks, 0);
    }
  };

  const handleDeletePlaylist = async (playlist: Playlist) => {
    Alert.alert(
      'Delete Playlist',
      `Are you sure you want to delete the playlist "${playlist.name}"? This cannot be undone.`,
      [
        { text: 'Cancel', style: 'cancel' },
        {
          text: 'Delete', style: 'destructive', onPress: async () => {
            try {
              const all = await PlaylistStorage.getPlaylists();
              const updated = all.filter(pl => pl.name !== playlist.name);
              await PlaylistStorage.savePlaylists(updated);
              await fetchPlaylists();
            } catch (error) {
              console.warn(`[Library] Failed to delete playlist ${playlist.name}`, error);
            }
          }
        }
      ]
    );
  };

  return (
    <SafeAreaView style={[styles.container, { backgroundColor: theme.background }]}>
      <StatusBar barStyle={isDark ? "light-content" : "dark-content"} backgroundColor={theme.background} translucent={false} />
      {showLikedSongs ? (
        <View style={[styles.scrollContent, { flex: 1 }]}>
          <TouchableOpacity onPress={handleBackToLibrary} style={styles.backButton}>
            <Ionicons name="chevron-back" size={22} color={theme.textPrimary} />
            <Text style={{ color: theme.textPrimary, fontSize: 16, marginLeft: 4 }}>{t('components.back_to_library')}</Text>
          </TouchableOpacity>
          <PlaylistCard
            playlist={{
              name: t('library.liked_songs'),
              cover: likedTracks[0]?.images?.large || 'https://misc.scdn.co/liked-songs/liked-songs-640.png',
              trackCount: likedTracks.length,
            }}
            onPress={() => {}}
            onShuffle={() => handleLikedSongsPlay(true)}
            onPlay={() => handleLikedSongsPlay(false)}
            theme={{ surface: theme.surface, border: theme.border, textPrimary: theme.textPrimary, textSecondary: theme.textSecondary, accent: theme.accent, icon: theme.textPrimary }}
          />
          <Text style={[styles.sectionTitle, { color: theme.textPrimary }]}>{t('components.tracks')}</Text>
          <FlatList
            data={likedTracks}
            keyExtractor={(item) => item.id.toString()}
            renderItem={({ item, index }) => {
              const isActiveTrack = currentTrack?.id?.toString() === item.id?.toString();
              const isCurrentlyPlaying = isActiveTrack && isPlaying;
              return (
                <View
                  style={[
                    styles.playlistTrackRow,
                    { backgroundColor: theme.surface, borderColor: isActiveTrack ? theme.accent : theme.border },
                    isActiveTrack && { borderWidth: 1.5 },
                  ]}
                >
                  <TouchableOpacity
                    onPress={() => handleTrackSelect(item, likedTracks, index)}
                    activeOpacity={0.75}
                    style={styles.playlistTrackMain}
                  >
                    <View style={styles.playlistArtWrapper}>
                      <Image
                        source={{ uri: item.images?.large || item.albumCover }}
                        style={styles.playlistAlbumArt}
                        contentFit="cover"
                      />
                      {isActiveTrack && (
                        <View style={[styles.playlistArtOverlay, { backgroundColor: '#000000aa' }]}>
                          <Ionicons
                            name={isCurrentlyPlaying ? 'musical-notes' : 'pause'}
                            size={18}
                            color={theme.accent}
                          />
                        </View>
                      )}
                    </View>
                    <View style={styles.playlistTrackInfo}>
                      <Text
                        style={[styles.playlistTrackTitle, { color: isActiveTrack ? theme.accent : theme.textPrimary }]}
                        numberOfLines={1}
                      >
                        {item.title}
                      </Text>
                      <Text style={[styles.playlistTrackArtist, { color: theme.textSecondary }]} numberOfLines={1}>
                        {item.artist}
                      </Text>
                    </View>
                  </TouchableOpacity>
                  <View style={styles.playlistActionRow}>
                    <TouchableOpacity style={styles.playlistIconButton} onPress={() => toggleLike(item)}>
                      <Ionicons
                        name={isLiked(item.id) ? 'heart' : 'heart-outline'}
                        size={20}
                        color={isLiked(item.id) ? theme.accent : theme.textPrimary}
                      />
                    </TouchableOpacity>
                  </View>
                </View>
              );
            }}
            ListEmptyComponent={<Text style={{ color: theme.textSecondary, marginTop: 16 }}>{t('components.no_liked_songs')}</Text>}
            contentContainerStyle={{ paddingBottom: 120 }}
            removeClippedSubviews
            maxToRenderPerBatch={10}
            windowSize={8}
            initialNumToRender={12}
          />
        </View>
      ) : !selectedPlaylist ? (
        <ScrollView contentContainerStyle={styles.scrollContent} showsVerticalScrollIndicator={false}>
          <Text style={[styles.sectionTitle, { color: theme.textPrimary }]}>{t('components.your_library')}</Text>
          <PlaylistCard
            playlist={{
              name: t('library.liked_songs'),
              cover: likedTracks[0]?.images?.large || 'https://misc.scdn.co/liked-songs/liked-songs-640.png',
              trackCount: likedTracks.length,
            }}
            onPress={() => setShowLikedSongs(true)}
            onShuffle={() => handleLikedSongsPlay(true)}
            onPlay={() => handleLikedSongsPlay(false)}
            theme={{ surface: theme.surface, border: theme.border, textPrimary: theme.textPrimary, textSecondary: theme.textSecondary, accent: theme.accent, icon: theme.textPrimary }}
          />
          {savedMedia.length > 0 && (
            <>
              <Text style={[styles.sectionTitle, { color: theme.textPrimary }]}>{t('library.saved')}</Text>
              <View style={styles.savedMediaGrid}>
                {savedMedia.map((item) => (
                  <View
                    key={`saved_${item.type}_${item.id}`}
                    style={[styles.savedMediaItem, { backgroundColor: theme.surface, borderColor: theme.border }]}
                  >
                    <TouchableOpacity
                      style={styles.savedMediaContent}
                      onPress={() => handleSavedMediaPress(item)}
                      onLongPress={() => handleRemoveSavedMedia(`saved_${item.type}_${item.id}`)}
                    >
                      <Image source={{ uri: item.image }} style={styles.savedMediaImage} contentFit="cover" />
                      <Text style={[styles.savedMediaTitle, { color: theme.textPrimary }]} numberOfLines={2}>{item.title}</Text>
                      <Text style={[styles.savedMediaMeta, { color: theme.textSecondary }]}>{t(`media.${item.type}`)}</Text>
                    </TouchableOpacity>
                    <TouchableOpacity
                      style={styles.savedMediaRemove}
                      onPress={() => handleRemoveSavedMedia(`saved_${item.type}_${item.id}`)}
                    >
                      <Ionicons name="close-circle" size={20} color="#ff4444" />
                    </TouchableOpacity>
                  </View>
                ))}
              </View>
            </>
          )}
          <PlaylistList
            playlists={playlists.map(pl => ({
              ...pl,
              trackCount: pl.trackIds.length,
              cover: playlistCovers[pl.name] || 'https://misc.scdn.co/liked-songs/liked-songs-640.png',
            }))}
            onPlaylistPress={handlePlaylistPress}
            onPlaylistShuffle={pl => handlePlaylistPlay(pl, true)}
            onPlaylistPlay={pl => handlePlaylistPlay(pl, false)}
            onPlaylistLongPress={handleDeletePlaylist}
            onPlaylistDelete={handleDeletePlaylist}
            theme={{ surface: theme.surface, border: theme.border, textPrimary: theme.textPrimary, textSecondary: theme.textSecondary, accent: theme.accent, icon: theme.textPrimary }}
          />
          <TouchableOpacity style={[styles.createButton, { backgroundColor: theme.surface, borderColor: theme.border }]} onPress={handleCreatePlaylist}>
            <Ionicons name="add-circle" size={24} color={theme.accent} style={{ marginRight: 8 }} />
            <Text style={[styles.createButtonText, { color: theme.accent }]}>{t('components.create_playlist')}</Text>
          </TouchableOpacity>
          <TouchableOpacity style={[styles.createButton, { backgroundColor: theme.surface, borderColor: theme.border }]} onPress={() => setShowImportModal(true)}>
            <Ionicons name="cloud-download" size={24} color={theme.accent} style={{ marginRight: 8 }} />
            <Text style={[styles.createButtonText, { color: theme.accent }]}>{t('library.import_title')}</Text>
          </TouchableOpacity>
          <View style={{ height: 120 }} />
        </ScrollView>
      ) : (
        <View style={[styles.scrollContent, { flex: 1 }]}>
          <TouchableOpacity onPress={handleBackToLibrary} style={styles.backButton}>
            <Ionicons name="chevron-back" size={22} color={theme.textPrimary} />
            <Text style={{ color: theme.textPrimary, fontSize: 16, marginLeft: 4 }}>{t('components.back_to_library')}</Text>
          </TouchableOpacity>
          <PlaylistCard
            playlist={{
              ...selectedPlaylist,
              trackCount: selectedPlaylist.trackIds.length,
              cover: (() => {
                if (selectedPlaylist.trackIds.length > 0) {
                  const lastTrackId = selectedPlaylist.trackIds[selectedPlaylist.trackIds.length - 1];
                  const track = [...likedTracks, ...playlistTracks].find(t => t.id.toString() === lastTrackId);
                  if (track && track.images) {
                    return MusicAPI.getOptimalImage(track.images);
                  }
                }
                return 'https://misc.scdn.co/liked-songs/liked-songs-640.png';
              })(),
            }}
            onPress={() => {}}
            onShuffle={() => handlePlaylistPlay(selectedPlaylist, true)}
            onPlay={() => handlePlaylistPlay(selectedPlaylist, false)}
            theme={{ surface: theme.surface, border: theme.border, textPrimary: theme.textPrimary, textSecondary: theme.textSecondary, accent: theme.accent, icon: theme.textPrimary }}
          />
          <Text style={[styles.sectionTitle, { color: theme.textPrimary }]}>{t('components.tracks')}</Text>
          <FlatList
            data={playlistTracks}
            keyExtractor={(item) => item.id.toString()}
            renderItem={({ item, index }) => {
              const isActiveTrack = currentTrack?.id?.toString() === item.id?.toString();
              const isCurrentlyPlaying = isActiveTrack && isPlaying;
              return (
                <View
                  style={[
                    styles.playlistTrackRow,
                    { backgroundColor: theme.surface, borderColor: isActiveTrack ? theme.accent : theme.border },
                    isActiveTrack && { borderWidth: 1.5 },
                  ]}
                >
                  <TouchableOpacity
                    onPress={() => handleTrackSelect(item, playlistTracks, index)}
                    activeOpacity={0.75}
                    style={styles.playlistTrackMain}
                  >
                    <View style={styles.playlistArtWrapper}>
                      <Image
                        source={{ uri: item.images?.large || item.albumCover }}
                        style={styles.playlistAlbumArt}
                        contentFit="cover"
                      />
                      {isActiveTrack && (
                        <View style={[styles.playlistArtOverlay, { backgroundColor: '#000000aa' }]}>
                          <Ionicons
                            name={isCurrentlyPlaying ? 'musical-notes' : 'pause'}
                            size={18}
                            color={theme.accent}
                          />
                        </View>
                      )}
                    </View>
                    <View style={styles.playlistTrackInfo}>
                      <Text
                        style={[styles.playlistTrackTitle, { color: isActiveTrack ? theme.accent : theme.textPrimary }]}
                        numberOfLines={1}
                      >
                        {item.title}
                      </Text>
                      <Text style={[styles.playlistTrackArtist, { color: theme.textSecondary }]} numberOfLines={1}>
                        {item.artist}
                      </Text>
                    </View>
                  </TouchableOpacity>
                  <View style={styles.playlistActionRow}>
                    <TouchableOpacity style={styles.playlistIconButton} onPress={() => toggleLike(item)}>
                      <Ionicons
                        name={isLiked(item.id) ? 'heart' : 'heart-outline'}
                        size={20}
                        color={isLiked(item.id) ? theme.accent : theme.textPrimary}
                      />
                    </TouchableOpacity>
                    <TouchableOpacity style={styles.playlistIconButton} onPress={() => handleRemoveTrackFromPlaylist(item.id.toString(), selectedPlaylist.name)}>
                      <Ionicons name="trash" size={20} color="#ff4444" />
                    </TouchableOpacity>
                  </View>
                </View>
              );
            }}
            ListEmptyComponent={<Text style={{ color: theme.textSecondary, marginTop: 16 }}>{t('components.no_tracks_playlist')}</Text>}
            contentContainerStyle={{ paddingBottom: 120 }}
            extraData={playlistTracks}
            removeClippedSubviews
            maxToRenderPerBatch={10}
            windowSize={8}
            initialNumToRender={12}
          />
      </View>
      )}
      <Modal visible={showCreateModal} transparent animationType="fade" onRequestClose={() => setShowCreateModal(false)}>
        <View style={styles.modalOverlay}>
          <View style={[styles.modalBox, { backgroundColor: theme.surface, borderColor: theme.border }]}>
            <Text style={[styles.modalTitle, { color: theme.textPrimary }]}>{t('components.create_playlist')}</Text>
            <TextInput
              style={[styles.input, { backgroundColor: theme.surfaceAlt, color: theme.textPrimary, borderColor: theme.border }]}
              placeholder={t('components.playlist_name')}
              placeholderTextColor={theme.textSecondary}
              value={newPlaylistName}
              onChangeText={setNewPlaylistName}
            />
            <TouchableOpacity
              style={[styles.createButtonIconOnly, { marginTop: 18, backgroundColor: theme.surface, borderColor: theme.border }]}
              onPress={handleCreatePlaylistSubmit}
            >
              <Ionicons name="add-circle" size={32} color={theme.accent} />
            </TouchableOpacity>
            <TouchableOpacity style={styles.cancelButton} onPress={() => setShowCreateModal(false)}>
              <Text style={{ color: theme.textPrimary, fontSize: 15 }}>{t('components.cancel')}</Text>
            </TouchableOpacity>
          </View>
        </View>
      </Modal>

      <Modal visible={showImportModal} transparent animationType="fade" onRequestClose={() => setShowImportModal(false)}>
        <View style={styles.modalOverlay}>
          <View style={[styles.modalBox, { backgroundColor: theme.surface, borderColor: theme.border }]}>
            <Text style={[styles.modalTitle, { color: theme.textPrimary }]}>{t('library.import_title')}</Text>
            <TextInput
              style={[styles.input, { backgroundColor: theme.surfaceAlt, color: theme.textPrimary, borderColor: theme.border }]}
              placeholder={t('library.spotify_playlist_url')}
              placeholderTextColor={theme.textSecondary}
              value={importUrl}
              onChangeText={setImportUrl}
              autoCapitalize="none"
              autoCorrect={false}
            />
            <TextInput
              style={[styles.input, { backgroundColor: theme.surfaceAlt, color: theme.textPrimary, borderColor: theme.border }]}
              placeholder={t('library.new_playlist_name')}
              placeholderTextColor={theme.textSecondary}
              value={importName}
              onChangeText={setImportName}
            />
            {importStatus !== 'idle' && (
              <Text style={[styles.importStatusText, { color: theme.textSecondary }]}>
                {importStatus === 'fetching' && t('library.fetching_playlist')}
                {importStatus === 'resolving' && t('library.importing_track', { current: importProgress.current, total: importProgress.total })}
                {importStatus === 'done' && (
                  <Text style={{ color: theme.accent }}>
                    {t('library.done_tracks_matched', { count: importProgress.current, suffix: importProgress.current !== 1 ? 's' : '' })}
                  </Text>
                )}
                {importStatus === 'error' && (
                  <Text style={{ color: '#ff4444' }}>
                    {t('library.import_error')}
                  </Text>
                )}
              </Text>
            )}
            <View style={{ flexDirection: 'row', marginTop: 18, gap: 12 }}>
              <TouchableOpacity
                style={[styles.importModalButton, { backgroundColor: theme.surface, borderColor: theme.border }]}
                onPress={() => {
                  setShowImportModal(false);
                  if (importStatus !== 'fetching' && importStatus !== 'resolving') {
                    setImportStatus('idle');
                    setImportUrl('');
                    setImportName('');
                    fetchPlaylists();
                  }
                }}
              >
                <Text style={{ color: theme.textPrimary, fontSize: 15, fontWeight: '600' }}>
                  {importStatus === 'done' || importStatus === 'error' ? t('common.close') : t('common.cancel')}
                </Text>
              </TouchableOpacity>
              {importStatus === 'idle' && (
                <TouchableOpacity
                  style={[styles.importModalButton, { backgroundColor: theme.accent, borderColor: theme.accent }]}
                  onPress={handleImportSpotify}
                >
                  <Text style={{ color: '#fff', fontSize: 15, fontWeight: '600' }}>{t('library.import')}</Text>
                </TouchableOpacity>
              )}
            </View>
          </View>
        </View>
      </Modal>
    </SafeAreaView>
  );
}

export default function LibraryScreen() {
  return (
    <ErrorBoundary scope="Library">
      <LibraryScreenContent />
    </ErrorBoundary>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: '#000',
  },
  scrollContent: {
    padding: 16,
    paddingBottom: 60,
  },
  sectionTitle: {
    color: '#fff',
    fontSize: 22,
    fontWeight: 'bold',
    marginBottom: 18,
    marginLeft: 2,
  },
  createButton: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: '#181818',
    borderWidth: 1,
    borderColor: '#242424',
    borderRadius: 24,
    paddingVertical: 14,
    marginTop: 18,
    marginBottom: 8,
    shadowColor: '#000',
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.12,
    shadowRadius: 4,
    elevation: 2,
  },
  createButtonText: {
    color: '#1DB954',
    fontSize: 16,
    fontWeight: 'bold',
  },
  createButtonIconOnly: {
    alignItems: 'center',
    justifyContent: 'center',
    borderWidth: 1,
    borderRadius: 24,
    padding: 12,
    shadowColor: '#000',
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.12,
    shadowRadius: 4,
    elevation: 2,
  },
  modalOverlay: {
    flex: 1,
    backgroundColor: 'rgba(0,0,0,0.7)',
    justifyContent: 'center',
    alignItems: 'center',
  },
  modalBox: {
    backgroundColor: '#181818',
    borderWidth: 1,
    borderColor: '#242424',
    borderRadius: 16,
    padding: 24,
    width: '80%',
    alignItems: 'center',
  },
  modalTitle: {
    color: '#fff',
    fontSize: 18,
    fontWeight: 'bold',
    marginBottom: 18,
  },
  input: {
    backgroundColor: '#222',
    color: '#fff',
    borderWidth: 1,
    borderColor: '#2b2b2b',
    borderRadius: 8,
    padding: 12,
    width: '100%',
    marginBottom: 12,
    fontSize: 15,
  },
  cancelButton: {
    marginTop: 10,
  },
  backButton: {
    flexDirection: 'row',
    alignItems: 'center',
    marginBottom: 18,
    marginLeft: 2,
  },
  trackRow: {
    borderRadius: 8,
    padding: 14,
    marginBottom: 10,
  },
  trackRowTitle: {
    color: '#fff',
    fontSize: 14,
    fontWeight: 'bold',
    marginBottom: 1,
  },
  trackRowArtist: {
    color: '#888',
    fontSize: 12,
  },
  trackRowBox: {
    flexDirection: 'row',
    alignItems: 'center',
    borderRadius: 8,
    marginBottom: 4,
    paddingVertical: 2,
    paddingHorizontal: 6,
  },
  trackDivider: {
    height: 1,
    marginVertical: 2,
    marginLeft: 8,
    marginRight: 8,
  },
  iconButton: {
    marginHorizontal: 2,
    padding: 8,
    borderRadius: 16,
  },
  removeButton: {
    marginLeft: 6,
  },
  actionRow: {
    flexDirection: 'row',
    alignItems: 'center',
    marginLeft: 8,
  },
  playlistTrackRow: {
    flexDirection: 'row',
    alignItems: 'center',
    borderWidth: 1,
    borderRadius: 12,
    marginBottom: 10,
    padding: 10,
  },
  playlistTrackMain: {
    flex: 1,
    flexDirection: 'row',
    alignItems: 'center',
  },
  playlistArtWrapper: {
    position: 'relative',
    marginRight: 14,
  },
  playlistAlbumArt: {
    width: 54,
    height: 54,
    borderRadius: 8,
    backgroundColor: '#222',
  },
  playlistArtOverlay: {
    ...StyleSheet.absoluteFillObject,
    borderRadius: 8,
    alignItems: 'center',
    justifyContent: 'center',
  },
  playlistTrackInfo: {
    flex: 1,
    marginRight: 8,
  },
  playlistTrackTitle: {
    fontSize: 15,
    fontWeight: '600',
    marginBottom: 2,
  },
  playlistTrackArtist: {
    fontSize: 13,
  },
  playlistActionRow: {
    flexDirection: 'row',
    alignItems: 'center',
  },
  playlistIconButton: {
    padding: 6,
    borderRadius: 16,
  },
  savedMediaGrid: {
    flexDirection: 'row',
    flexWrap: 'wrap',
    gap: 12,
    marginBottom: 18,
  },
  savedMediaItem: {
    width: '48%',
    borderRadius: 12,
    borderWidth: 1,
    padding: 10,
    position: 'relative',
  },
  savedMediaContent: {
    flex: 1,
  },
  savedMediaImage: {
    width: '100%',
    aspectRatio: 1,
    borderRadius: 8,
    marginBottom: 8,
  },
  savedMediaTitle: {
    fontSize: 14,
    fontWeight: '600',
    marginBottom: 4,
  },
  savedMediaMeta: {
    fontSize: 12,
  },
  savedMediaRemove: {
    position: 'absolute',
    top: 8,
    right: 8,
    backgroundColor: 'rgba(0,0,0,0.5)',
    borderRadius: 12,
    padding: 4,
  },
  importStatusText: {
    fontSize: 14,
    marginVertical: 12,
    textAlign: 'center',
  },
  importModalButton: {
    flex: 1,
    alignItems: 'center',
    justifyContent: 'center',
    paddingVertical: 12,
    borderRadius: 24,
    borderWidth: 1,
  },
});
