import React, { useState, useEffect, useRef, useCallback, useMemo } from 'react';
import {
  View,
  Text,
  TouchableOpacity,
  StyleSheet,
  Alert,
  Animated,
} from 'react-native';
import TrackPlayer, { Event, State, useProgress, RepeatMode } from 'react-native-track-player';
import { Image } from 'expo-image';
import { Ionicons } from '@expo/vector-icons';
import * as FileSystem from 'expo-file-system';
import * as Haptics from 'expo-haptics';

import { Track } from '../types/music';
import { MusicAPI } from '../lib/music-api';
import AsyncStorage from '@react-native-async-storage/async-storage';
import { FullScreenPlayer } from './FullScreenPlayer';
import { useLikedSongs } from '../hooks/useLikedSongs';
import { useColorScheme } from '../hooks/useColorScheme';
import { useTranslation } from 'react-i18next';
import { ensureTrackPlayerReady, releaseTrackPlayer, withPlaybackRetry } from '@/lib/playback/track-player-runtime';
import { getPlayableOfflineUri, isTauriRuntime } from '@/lib/tauri-offline';
import { isRecord, parseStoredJSON } from '@/lib/storage-validation';
import { useAutoMixPlayback } from '@/lib/automix/use-auto-mix-playback';

async function resolveDesktopTrackUrl(t: Track): Promise<string> {
  const offlineUri = (t as Track & { offlineUri?: string }).offlineUri;
  if (offlineUri) {
    if (isTauriRuntime()) return getPlayableOfflineUri(offlineUri, 'audio/mpeg');
    try {
      const info = await FileSystem.getInfoAsync(offlineUri);
      if (info.exists) return offlineUri;
    } catch {}
  }
  try {
    const offlineData = await AsyncStorage.getItem(`offline_${t.id}`);
    if (offlineData) {
      const { fileUri } = JSON.parse(offlineData);
      if (fileUri) {
        if (isTauriRuntime()) return getPlayableOfflineUri(fileUri, 'audio/mpeg');
        const info = await FileSystem.getInfoAsync(fileUri);
        if (info.exists) return fileUri;
      }
    }
  } catch {}
  return withPlaybackRetry('resolve_stream', () => MusicAPI.getStreamUrl(t.id.toString(), t));
}

interface PlayerProps {
  track: Track | null;
  isPlaying: boolean;
  onPlayingChange: (playing: boolean) => void;
  musicQueue: any;
  onQueueToggle: () => void;
  pendingAutoPlayRef?: React.MutableRefObject<boolean>;
  showToast?: (message: string, type: 'success' | 'error') => void;
}

export function Player({
  track,
  isPlaying,
  onPlayingChange,
  musicQueue,
  onQueueToggle,
  pendingAutoPlayRef: externalPendingAutoPlayRef,
  showToast,
}: PlayerProps) {
  const playerReadyRef = useRef(false);
  const [playerReady, setPlayerReady] = useState(false);
  const internalPendingAutoPlayRef = useRef(false);
  const pendingAutoPlayRef = externalPendingAutoPlayRef || internalPendingAutoPlayRef;
  const lastQueueSignatureRef = useRef<string | null>(null);
  const lastTrackIdRef = useRef<string | number | null>(null);
  const colorScheme = useColorScheme();
  const isDark = colorScheme !== 'light';
  const theme = useMemo(
    () => ({
      card: isDark ? '#181a1f' : '#fffaf2',
      cardSubtle: isDark ? '#222733' : '#efe4d6',
      textPrimary: isDark ? '#ffffff' : '#2d2219',
      textSecondary: isDark ? '#b9c0d6' : '#7a6251',
      icon: isDark ? '#ffffff' : '#2d2219',
      accent: isDark ? '#1DB954' : '#167c3a',
      progressBg: isDark ? 'rgba(255,255,255,0.18)' : 'rgba(45,34,25,0.18)',
      progressFill: isDark ? '#ffffff' : '#2d2219',
      border: isDark ? '#2a2f3a' : '#e4d5c5',
    }),
    [isDark]
  );
  const [position, setPosition] = useState(0);
  const [duration, setDuration] = useState(0);
  const [volume, setVolume] = useState(1.0);
  const [isMuted, setIsMuted] = useState(false);
  const [isFullScreenOpen, setIsFullScreenOpen] = useState(false);
  const [isSeeking] = useState(false);

  const { isLiked, toggleLike } = useLikedSongs();
  const { t } = useTranslation();
  const rotationValue = useRef(new Animated.Value(0)).current;
  const rotationAnimationRef = useRef<Animated.CompositeAnimation | null>(null);
  const isMountedRef = useRef(true);
  const currentTrackIdRef = useRef<string | number | null>(null);
  const queueBuildAbortRef = useRef<AbortController | null>(null);
  const queueBuildGenRef = useRef(0);
  const queueBuildDebounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const isInternalChangeRef = useRef(false);
  const handledEndedTrackRef = useRef<string | number | null>(null);
  const lastAutoAdvanceAtRef = useRef(0);
  const { position: tpPosition, duration: tpDuration } = useProgress(250);
  const autoMixNextTrack = musicQueue?.tracks?.[musicQueue.currentIndex + 1] ?? null;
  const autoMixRuntime = useAutoMixPlayback({
    track,
    nextTrack: autoMixNextTrack,
    queue: musicQueue,
    isPlaying,
    resolveUrl: resolveDesktopTrackUrl,
    onPlayingChange,
  });

  useEffect(() => {
    if (!autoMixRuntime.active) return;
    lastQueueSignatureRef.current = null;
    void TrackPlayer.pause().catch(() => {});
    void TrackPlayer.reset().catch(() => {});
  }, [autoMixRuntime.active]);

  
  useEffect(() => {
    const setupPlayer = async () => {
      try {
        if (!playerReadyRef.current) {
          await ensureTrackPlayerReady(volume);
          playerReadyRef.current = true;
          if (isMountedRef.current) {
            setPlayerReady(true);
          }
        }
      } catch (error) {
        console.error('Failed to setup TrackPlayer:', error);
      }
    };

    setupPlayer();

    return () => {
      isMountedRef.current = false;
      currentTrackIdRef.current = null;
      if (queueBuildAbortRef.current) {
        queueBuildAbortRef.current.abort();
      }
      if (queueBuildDebounceRef.current) {
        clearTimeout(queueBuildDebounceRef.current);
      }
      stopRotation();
      releaseTrackPlayer().catch(() => {});
    };
  }, [volume]);

  
  useEffect(() => {
    if (autoMixRuntime.active) {
      setPosition(autoMixRuntime.positionMs);
      setDuration(autoMixRuntime.durationMs);
      return;
    }
    if (!isSeeking) {
      setPosition(tpPosition * 1000);
    }
    setDuration(tpDuration * 1000);
  }, [autoMixRuntime.active, autoMixRuntime.durationMs, autoMixRuntime.positionMs, tpPosition, tpDuration, isSeeking]);

  
  useEffect(() => {
    if (!playerReady) return;
    if (autoMixRuntime.active) {
      void (isPlaying ? autoMixRuntime.play() : autoMixRuntime.pause()).catch(() => {});
      return;
    }
    isInternalChangeRef.current = true;
    if (isPlaying) {
      pendingAutoPlayRef.current = true;
      TrackPlayer.play().catch(() => {});
    } else {
      TrackPlayer.pause().catch(() => {});
    }
    const timer = setTimeout(() => { isInternalChangeRef.current = false; }, 800);
    return () => clearTimeout(timer);
  }, [autoMixRuntime.active, autoMixRuntime.pause, autoMixRuntime.play, isPlaying, playerReady, pendingAutoPlayRef]);

  
  const startRotation = useCallback(() => {
    if (rotationAnimationRef.current) {
      rotationAnimationRef.current.stop();
    }
    rotationAnimationRef.current = Animated.loop(
      Animated.timing(rotationValue, {
        toValue: 1,
        duration: 10000,
        useNativeDriver: true,
      }),
      { iterations: -1 }
    );
    rotationAnimationRef.current.start();
  }, [rotationValue]);

  const stopRotation = useCallback(() => {
    if (rotationAnimationRef.current) {
      rotationAnimationRef.current.stop();
      rotationAnimationRef.current = null;
    }
  }, []);

  useEffect(() => {
    if (isPlaying) {
      startRotation();
    } else {
      stopRotation();
    }
    return () => stopRotation();
  }, [isPlaying, startRotation]);

  
  const resolveTrackUrl = async (t: Track & { offlineUri?: string }): Promise<string> => {
    const offlineUri = t.offlineUri;
    if (offlineUri) {
      if (isTauriRuntime()) return getPlayableOfflineUri(offlineUri, 'audio/mpeg');
      try {
        const info = await FileSystem.getInfoAsync(offlineUri);
        if (info.exists) return offlineUri;
      } catch {}
    }
    try {
      const offlineData = await AsyncStorage.getItem(`offline_${t.id}`);
      if (offlineData) {
        const parsed = parseStoredJSON<unknown>(offlineData, `offline_${t.id}`, null);
        const fileUri = isRecord(parsed) && typeof parsed.fileUri === 'string' ? parsed.fileUri : '';
        if (fileUri) {
          if (isTauriRuntime()) return getPlayableOfflineUri(fileUri, 'audio/mpeg');
          const info = await FileSystem.getInfoAsync(fileUri);
          if (info.exists) return fileUri;
        }
      }
    } catch {}
    return withPlaybackRetry('resolve_stream', () => MusicAPI.getStreamUrl(t.id.toString(), t));
  };

  
  const musicQueueRef = useRef(musicQueue);
  useEffect(() => {
    musicQueueRef.current = musicQueue;
  }, [musicQueue]);

  const playNextTrack = useCallback((withHaptics = false) => {
    if (withHaptics) Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
    lastAutoAdvanceAtRef.current = Date.now();
    const queue = musicQueueRef.current;
    if (!queue?.playNext) {
      onPlayingChange(false);
      return;
    }
    const nextTrack = queue.playNext();
    if (nextTrack) {
      isInternalChangeRef.current = true;
      pendingAutoPlayRef.current = true;
      onPlayingChange(true);
      setTimeout(() => { isInternalChangeRef.current = false; }, 500);
    } else {
      onPlayingChange(false);
    }
  }, [onPlayingChange, pendingAutoPlayRef]);

  const handleNext = useCallback(() => {
    playNextTrack(true);
  }, [playNextTrack]);

  const handlePrevious = useCallback(() => {
    Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
    const queue = musicQueueRef.current;
    const prevTrack = queue.playPrevious();
    if (prevTrack) {
      
      isInternalChangeRef.current = true;
      pendingAutoPlayRef.current = true;
      onPlayingChange(true);
      setTimeout(() => { isInternalChangeRef.current = false; }, 500);
    }
  }, [pendingAutoPlayRef, onPlayingChange]);

  
  useEffect(() => {
    const sub = TrackPlayer.addEventListener(Event.PlaybackQueueEnded, async () => {
      if (autoMixRuntime.active) return;
      const mode = await TrackPlayer.getRepeatMode();
      if (mode === RepeatMode.Off) {
        playNextTrack(false);
      }
    });
    return () => sub.remove();
  }, [autoMixRuntime.active, playNextTrack]);

  useEffect(() => {
    if (autoMixRuntime.active) return;
    if (!track || !isPlaying || duration <= 0) return;

    const cooldownElapsed = Date.now() - lastAutoAdvanceAtRef.current;
    if (cooldownElapsed < 1000) return;

    const remaining = duration - position;
    if (position < 1000 || remaining > 750) {
      if (remaining > 1500) handledEndedTrackRef.current = null;
      return;
    }

    if (handledEndedTrackRef.current === track.id) return;
    handledEndedTrackRef.current = track.id;
    TrackPlayer.getRepeatMode().then(async mode => {
      if (mode === RepeatMode.Off) {
        playNextTrack(false);
      } else if (mode === RepeatMode.Track) {
        lastAutoAdvanceAtRef.current = Date.now();
        await TrackPlayer.seekTo(0);
        await TrackPlayer.play();
      } else if (mode === RepeatMode.Queue) {
        const queue = musicQueueRef.current;
        if (queue?.tracks?.length && queue.setCurrentIndex) {
          const isLastTrack = queue.currentIndex === queue.tracks.length - 1;
          if (isLastTrack) {
            lastAutoAdvanceAtRef.current = Date.now();
            queue.setCurrentIndex(0);
          } else {
            playNextTrack(false);
          }
        }
      }
    });
  }, [autoMixRuntime.active, duration, isPlaying, playNextTrack, position, track]);

  
  useEffect(() => {
    const sub = TrackPlayer.addEventListener(Event.PlaybackState, (event) => {
      if (autoMixRuntime.active) return;
      
      if (isInternalChangeRef.current) return;

      
      if (event.state === State.Playing) {
        if (!isPlaying) onPlayingChange(true);
      } else if (event.state === State.Paused) {
        if (isPlaying) onPlayingChange(false);
      }
    });
    return () => sub.remove();
  }, [autoMixRuntime.active, isPlaying, onPlayingChange]);
  
  useEffect(() => {
    const sub = TrackPlayer.addEventListener(Event.PlaybackActiveTrackChanged, async (event) => {
      if (autoMixRuntime.active) return;
      if (isInternalChangeRef.current) return;
      const activeTrack = event.track;
      if (activeTrack?.id && musicQueueRef.current?.tracks?.length) {
        const queueIndex = musicQueueRef.current.tracks.findIndex(
          (t: Track) => t.id.toString() === activeTrack.id
        );
        if (queueIndex >= 0 && musicQueueRef.current.setCurrentIndex) {
          musicQueueRef.current.setCurrentIndex(queueIndex);
        }
      }
    });
    return () => sub.remove();
  }, [autoMixRuntime.active]);

  
  const syncTrackPlayerQueue = useCallback(async () => {
    if (autoMixRuntime.active) return;
    if (!playerReady) return;
    if (!musicQueue?.tracks?.length || musicQueue.currentIndex < 0) return;
    if (!track) return;

    if (queueBuildDebounceRef.current) {
      clearTimeout(queueBuildDebounceRef.current);
    }
    if (queueBuildAbortRef.current) {
      queueBuildAbortRef.current.abort();
      queueBuildAbortRef.current = null;
    }

    queueBuildGenRef.current = -(Math.abs(queueBuildGenRef.current) + 1);
    const myGen = Math.abs(queueBuildGenRef.current);

    queueBuildDebounceRef.current = setTimeout(async () => {
      queueBuildDebounceRef.current = null;
      if (Math.abs(queueBuildGenRef.current) !== myGen) return;

      const queueTracks = musicQueue.tracks as Track[];
      const startIndex = musicQueue.currentIndex as number;
      const orderHash = queueTracks.map(t => t.id).join(',');
      const currentTrack = queueTracks[startIndex];
      const signature = `${currentTrack?.id}|${queueTracks.length}|${startIndex}|${orderHash}`;
      const isSameSignature = lastQueueSignatureRef.current === signature;

      const currentTrackId = String(currentTrack?.id);
      if (lastTrackIdRef.current !== currentTrackId) {
        lastTrackIdRef.current = currentTrackId;
        lastQueueSignatureRef.current = null;
      }

      if (isSameSignature) {
        if (pendingAutoPlayRef.current || isPlaying) {
          pendingAutoPlayRef.current = false;
          await TrackPlayer.play();
        } else {
          await TrackPlayer.pause();
        }
        return;
      }

      lastQueueSignatureRef.current = signature;
      currentTrackIdRef.current = currentTrack?.id;

      const current = queueTracks[startIndex];
      if (!current) return;

      const shouldPlayNow = pendingAutoPlayRef.current || isPlaying;
      pendingAutoPlayRef.current = false;

      
      isInternalChangeRef.current = true;
      const suppressTimer = setTimeout(() => { isInternalChangeRef.current = false; }, 1500);

      const activeTrack = await TrackPlayer.getActiveTrack();
      const isSameTrack = activeTrack?.id === current.id.toString();

      try {
        if (isSameTrack) {
          
          const tpQueue = await TrackPlayer.getQueue();
          const currentIndexInTp = tpQueue.findIndex(item => item.id === current.id.toString());
          if (currentIndexInTp !== -1) {
            for (let i = tpQueue.length - 1; i >= 0; i--) {
              if (i !== currentIndexInTp) {
                try {
                  await TrackPlayer.remove(i);
                } catch {}
              }
            }
          }
        } else {
          let currentUrl: string;
          try {
            currentUrl = await resolveTrackUrl(current);
          } catch (streamError) {
            console.error('[Player] Failed to resolve stream URL:', streamError);
            await TrackPlayer.pause();
            onPlayingChange(false);
            if (showToast) {
              showToast(t('player.stream_error_message'), 'error');
            } else {
              Alert.alert(
                t('player.stream_error_title'),
                t('player.stream_error_message')
              );
            }
            return;
          }
          const currentItem = {
            id: current.id.toString(),
            url: currentUrl,
            title: current.title,
            artist: current.artist,
            artwork: (current as any).offlineThumbUri || MusicAPI.getOptimalImage(current.images),
            duration: current.duration ? Math.floor(current.duration / 1000) : undefined,
          };
          await TrackPlayer.reset();
          await TrackPlayer.add([currentItem]);
          if (shouldPlayNow) await TrackPlayer.play();
          else await TrackPlayer.pause();
        }
      } finally {
        clearTimeout(suppressTimer);
        
        setTimeout(() => { isInternalChangeRef.current = false; }, 800);
      }

      if (queueBuildAbortRef.current) {
        queueBuildAbortRef.current.abort();
      }
      queueBuildAbortRef.current = new AbortController();

      void (async () => {
        const signal = queueBuildAbortRef.current?.signal;
        if (!signal) return;
        try {
          for (let i = startIndex + 1; i < queueTracks.length; i++) {
            if (signal.aborted) break;
            const t = queueTracks[i];
            try {
              const url = await resolveTrackUrl(t);
              await TrackPlayer.add([
                {
                  id: t.id.toString(),
                  url,
                  title: t.title,
                  artist: t.artist,
                  artwork: (t as any).offlineThumbUri || MusicAPI.getOptimalImage(t.images),
                  duration: t.duration ? Math.floor(t.duration / 1000) : undefined,
                },
              ]);
            } catch {}
          }
          for (let i = startIndex - 1; i >= 0; i--) {
            if (signal.aborted) break;
            const t = queueTracks[i];
            try {
              const url = await resolveTrackUrl(t);
              await TrackPlayer.add(
                [
                  {
                    id: t.id.toString(),
                    url,
                    title: t.title,
                    artist: t.artist,
                    artwork: (t as any).offlineThumbUri || MusicAPI.getOptimalImage(t.images),
                    duration: t.duration ? Math.floor(t.duration / 1000) : undefined,
                  },
                ],
                0
              );
            } catch (e) {console.error(e)}
          }
        } catch (error) {
          console.error('[Player] Queue build error (gen:', myGen, '):', error);
        } finally {
          if (Math.abs(queueBuildGenRef.current) === myGen) {
            queueBuildGenRef.current = myGen;
          }
          if (queueBuildAbortRef.current?.signal === signal) {
            queueBuildAbortRef.current = null;
          }
        }
      })();
    }, 50);
  }, [autoMixRuntime.active, playerReady, musicQueue?.tracks, musicQueue?.currentIndex, track, isPlaying, onPlayingChange, pendingAutoPlayRef, t]);

  
  useEffect(() => {
    if (!playerReady) return;
    void syncTrackPlayerQueue();
  }, [playerReady, syncTrackPlayerQueue]);

  
  const handlePlayPause = useCallback(async () => {
    Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
    try {
      
      isInternalChangeRef.current = true;
      if (autoMixRuntime.active) {
        if (isPlaying) {
          await autoMixRuntime.pause();
          onPlayingChange(false);
        } else {
          await autoMixRuntime.play();
          onPlayingChange(true);
        }
        isInternalChangeRef.current = false;
        return;
      }
      
      if (isPlaying) {
        await TrackPlayer.pause();
        onPlayingChange(false);
      } else {
        pendingAutoPlayRef.current = true;
        await TrackPlayer.play();
        onPlayingChange(true);
      }
      
      
      setTimeout(() => {
        isInternalChangeRef.current = false;
      }, 500);
    } catch (error) {
      console.error('Error in handlePlayPause:', error);
      isInternalChangeRef.current = false;
    }
  }, [autoMixRuntime.active, autoMixRuntime.pause, autoMixRuntime.play, isPlaying, onPlayingChange, pendingAutoPlayRef]);

  const handleSeek = async (value: number) => {
    try {
      setPosition(value);
      if (autoMixRuntime.active) await autoMixRuntime.seekTo(value);
      else await TrackPlayer.seekTo(value / 1000);
    } catch (error) {
      console.error('Error seeking:', error);
    }
  };

  const handleVolumeChange = async (value: number) => {
    setVolume(value);
    if (autoMixRuntime.active) await autoMixRuntime.setVolume(isMuted ? 0 : value).catch(() => {});
    else await TrackPlayer.setVolume(isMuted ? 0 : value).catch(() => {});
  };
  const handleMute = async () => {
    Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
    const newMutedState = !isMuted;
    setIsMuted(newMutedState);
    if (autoMixRuntime.active) await autoMixRuntime.setVolume(newMutedState ? 0 : volume).catch(() => {});
    else await TrackPlayer.setVolume(newMutedState ? 0 : volume).catch(() => {});
  };
  const handleShuffle = () => musicQueue.toggleShuffle();

  if (!track) return null;

  
  return (
    <>
      <View style={styles.cardroot}>
        <View style={[styles.cardContainer, { backgroundColor: theme.card, borderColor: theme.border }]}>
          <TouchableOpacity
            style={styles.cardTouchable}
            activeOpacity={0.85}
            onPress={() => setIsFullScreenOpen(true)}
          >
            <Image
              source={{ uri: (track as any).offlineThumbUri || MusicAPI.getOptimalImage(track.images) }}
              style={styles.cardAlbumArt}
              contentFit="cover"
            />
            <View style={styles.cardInfoArea}>
              <Text style={[styles.cardTitle, { color: theme.textPrimary }]} numberOfLines={1}>
                {track.title}
              </Text>
              <Text style={[styles.cardArtist, { color: theme.textSecondary }]} numberOfLines={1}>
                {track.artist}
              </Text>
            </View>
          </TouchableOpacity>
          <TouchableOpacity style={styles.cardIconButton} onPress={() => toggleLike(track)} activeOpacity={0.7}>
            <Ionicons
              name={isLiked(track.id) ? 'heart' : 'heart-outline'}
              size={24}
              color={isLiked(track.id) ? theme.accent : theme.icon}
            />
          </TouchableOpacity>
          <TouchableOpacity style={styles.cardIconButton} onPress={() => { setIsFullScreenOpen(false); onQueueToggle(); }} activeOpacity={0.7}>
            <Ionicons name="list" size={24} color={theme.icon} />
          </TouchableOpacity>
          <TouchableOpacity style={styles.cardIconButton} onPress={handlePlayPause} activeOpacity={0.7}>
            <Ionicons name={isPlaying ? 'pause' : 'play'} size={28} color={theme.icon} />
          </TouchableOpacity>
        </View>
        <View style={[styles.whiteProgressBarBg, { backgroundColor: theme.progressBg }]}>
          <View
            style={[
              styles.whiteProgressBarFill,
              {
                backgroundColor: theme.progressFill,
                width: duration > 0 ? `${(position / duration) * 100}%` : '0%',
              },
            ]}
          />
        </View>
      </View>

      <FullScreenPlayer
        isOpen={isFullScreenOpen}
        onClose={() => setIsFullScreenOpen(false)}
        track={track}
        isPlaying={isPlaying}
        onPlayingChange={onPlayingChange}
        position={position}
        duration={duration}
        onSeek={handleSeek}
        volume={volume}
        onVolumeChange={handleVolumeChange}
        isMuted={isMuted}
        onMuteToggle={handleMute}
        onNext={handleNext}
        onPrevious={handlePrevious}
        onShuffle={handleShuffle}
        musicQueue={musicQueue}
        onQueueToggle={() => {
          setIsFullScreenOpen(false);
          onQueueToggle();
        }}
      />
    </>
  );
}


const styles = StyleSheet.create({
  cardContainer: {
    width: '100%',
    backgroundColor: '#1a2341',
    borderRadius: 16,
    borderWidth: 1,
    flexDirection: 'row',
    alignItems: 'center',
    paddingVertical: 10,
    paddingHorizontal: 16,
    marginBottom: 8,
    shadowColor: '#000',
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.12,
    shadowRadius: 4,
    elevation: 2,
  },
  cardTouchable: {
    flexDirection: 'row',
    alignItems: 'center',
    flex: 1,
  },
  cardAlbumArt: {
    width: 54,
    height: 54,
    borderRadius: 8,
    marginRight: 14,
    backgroundColor: '#222',
    overflow: 'hidden',
    borderWidth: 1,
    borderColor: '#222',
  },
  cardInfoArea: {
    flex: 1,
    justifyContent: 'center',
  },
  cardTitle: {
    color: '#fff',
    fontSize: 18,
    fontWeight: 'bold',
    marginBottom: 2,
  },
  cardArtist: {
    color: '#bfc8e6',
    fontSize: 15,
    fontWeight: '400',
    marginBottom: 2,
  },
  cardIconButton: {
    marginLeft: 12,
    padding: 8,
    borderRadius: 16,
  },
  modalOverlay: {
    flex: 1,
    justifyContent: 'center',
    alignItems: 'center',
    backgroundColor: 'rgba(0,0,0,0.7)',
  },
  downloadModal: {
    width: '85%',
    maxWidth: 360,
    borderRadius: 10,
    overflow: 'hidden',
    elevation: 10,
    shadowColor: '#000',
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.25,
    shadowRadius: 4,
  },
  modalGradient: {
    padding: 20,
  },
  modalHeader: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    marginBottom: 15,
  },
  modalTitle: {
    fontSize: 20,
    fontWeight: 'bold',
    color: '#fff',
    flex: 1,
    textAlign: 'center',
  },
  closeButton: {
    padding: 5,
  },
  modalContent: {
    paddingVertical: 10,
  },
  trackInfo: {
    flexDirection: 'row',
    alignItems: 'center',
    marginBottom: 15,
  },
  modalAlbumArt: {
    width: 60,
    height: 60,
    borderRadius: 30,
    marginRight: 10,
  },
  trackDetails: {
    flex: 1,
  },
  trackTitle: {
    fontSize: 18,
    fontWeight: 'bold',
    color: '#fff',
    marginBottom: 2,
  },
  trackArtist: {
    fontSize: 14,
    color: '#888',
  },
  statusContainer: {
    alignItems: 'center',
    marginTop: 10,
  },
  statusText: {
    fontSize: 16,
    color: '#888',
    marginBottom: 10,
  },
  spinner: {
    marginBottom: 10,
  },
  downloadProgressContainer: {
    alignItems: 'center',
  },
  downloadProgressBar: {
    width: '100%',
    height: 8,
    backgroundColor: '#333',
    borderRadius: 4,
    overflow: 'hidden',
    marginBottom: 5,
  },
  progressFillBar: {
    height: '100%',
    backgroundColor: '#1DB954',
    borderRadius: 4,
  },
  progressText: {
    fontSize: 14,
    color: '#888',
  },
  successIcon: {
    marginBottom: 10,
  },
  successText: {
    fontSize: 20,
    fontWeight: 'bold',
    color: '#1DB954',
    marginBottom: 5,
  },
  successSubtext: {
    fontSize: 14,
    color: '#888',
    textAlign: 'center',
  },
  errorIcon: {
    marginBottom: 10,
  },
  errorText: {
    fontSize: 20,
    fontWeight: 'bold',
    color: '#ff4444',
    marginBottom: 5,
  },
  errorSubtext: {
    fontSize: 14,
    color: '#888',
    textAlign: 'center',
    marginBottom: 15,
  },
  retryButton: {
    backgroundColor: '#1DB954',
    paddingVertical: 10,
    paddingHorizontal: 20,
    borderRadius: 8,
  },
  retryButtonText: {
    color: '#fff',
    fontSize: 16,
    fontWeight: 'bold',
    textAlign: 'center',
  },
  visualProgressBarContainer: {
    width: '100%',
    height: 5,
    backgroundColor: 'transparent',
    margin: 0,
    padding: 0,
    position: 'absolute',
    top: 0,
    left: 0,
    right: 0,
    zIndex: 10,
  },
  visualProgressBarBg: {
    width: '100%',
    height: 5,
    backgroundColor: '#222',
    borderRadius: 0,
    overflow: 'hidden',
    margin: 0,
    padding: 0,
  },
  visualProgressBarFill: {
    height: 10,
    backgroundColor: '#1DB954',
    borderRadius: 0,
    margin: 0,
    padding: 0,
  },
  songTitleContainer: {
    width: '100%',
    alignItems: 'center',
    justifyContent: 'center',
    marginTop: 18,
    marginBottom: 8,
  },
  songTitleText: {
    color: '#fff',
    fontSize: 18,
    fontWeight: 'bold',
    textAlign: 'center',
    width: '100%',
    letterSpacing: 0.2,
  },
  whiteProgressBarBg: {
    marginHorizontal: 8,
    height: 4,
    backgroundColor: '#fff',
    opacity: 0.18,
    borderBottomLeftRadius: 16,
    borderBottomRightRadius: 16,
    overflow: 'hidden',
    marginTop: -4,
    marginBottom: 0,
  },
  whiteProgressBarFill: {
    height: 4,
    backgroundColor: '#fff',
    opacity: 1,
    borderBottomLeftRadius: 0,
    borderBottomRightRadius: 0,
  },
  cardroot: {
    width: '100%',
    backgroundColor: 'transparent',
    borderRadius: 16,
    flexDirection: 'column',
  },
});
