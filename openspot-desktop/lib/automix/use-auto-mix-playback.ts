import { useCallback, useEffect, useRef, useState } from 'react';

import type { Track } from '@/types/music';
import { recordDiagnostic } from '@/lib/diagnostics';
import { DEFAULT_AUTO_MIX_SETTINGS } from './auto-mix-algorithms';
import { AutoMixEngine } from './auto-mix-engine';
import { loadAutoMixSettings, subscribeAutoMixSettings } from './auto-mix-settings';
import { createWebAutoMixPlayer } from './web-auto-mix-player';
import type { AutoMixPlayerState, AutoMixSettings, AutoMixTrack } from './auto-mix-types';

interface AutoMixQueue {
  tracks: Track[];
  currentIndex: number;
  setCurrentIndex: (index: number) => void;
}

interface UseAutoMixPlaybackOptions {
  track: Track | null;
  nextTrack: Track | null;
  queue: AutoMixQueue;
  isPlaying: boolean;
  resolveUrl: (track: Track) => Promise<string>;
  onPlayingChange: (playing: boolean) => void;
}

interface AutoMixPlaybackRuntime {
  active: boolean;
  settingsReady: boolean;
  state: AutoMixPlayerState;
  positionMs: number;
  durationMs: number;
  setVolume: (volume: number) => Promise<void>;
  play: () => Promise<void>;
  pause: () => Promise<void>;
  seekTo: (positionMs: number) => Promise<void>;
}

function toAutoMixTrack(track: Track | null): AutoMixTrack | null {
  if (!track) return null;
  return { id: String(track.id), durationMs: track.duration, albumId: track.albumId, isVideo: track.isVideo === true, audioMeta: track.audioMeta ?? null };
}

export function useAutoMixPlayback(options: UseAutoMixPlaybackOptions): AutoMixPlaybackRuntime {
  const { track, nextTrack, queue, isPlaying, resolveUrl, onPlayingChange } = options;
  const [settings, setSettings] = useState<AutoMixSettings>(DEFAULT_AUTO_MIX_SETTINGS);
  const [settingsReady, setSettingsReady] = useState(false);
  const [state, setState] = useState<AutoMixPlayerState>('IDLE');
  const [positionMs, setPositionMs] = useState(0);
  const [durationMs, setDurationMs] = useState(0);
  const queueRef = useRef(queue);
  const resolveUrlRef = useRef(resolveUrl);
  const trackMapRef = useRef(new Map<string, Track>());
  const engineRef = useRef<AutoMixEngine<AutoMixTrack> | null>(null);

  queueRef.current = queue;
  resolveUrlRef.current = resolveUrl;
  for (const item of queue.tracks) trackMapRef.current.set(String(item.id), item);

  if (!engineRef.current) {
    engineRef.current = new AutoMixEngine<AutoMixTrack>({
      createPlayer: async () => createWebAutoMixPlayer(),
      resolveUrl: async (autoTrack) => {
        const source = trackMapRef.current.get(autoTrack.id);
        if (!source) throw new Error(`AutoMix track ${autoTrack.id} is not in the queue`);
        return resolveUrlRef.current(source);
      },
      onStateChange: setState,
      onTakeover: async ({ track: incoming }) => {
        const index = queueRef.current.tracks.findIndex((item) => String(item.id) === incoming.id);
        if (index >= 0 && index !== queueRef.current.currentIndex) queueRef.current.setCurrentIndex(index);
        onPlayingChange(true);
      },
      onDiagnostic: (event) => recordDiagnostic({ category: 'automix', type: event.type, data: event.detail }),
    }, DEFAULT_AUTO_MIX_SETTINGS);
  }

  const engine = engineRef.current;
  const active = settingsReady && settings.mode !== 'off' && Boolean(track);

  useEffect(() => {
    let mounted = true;
    void loadAutoMixSettings().then((loaded) => {
      if (!mounted) return;
      setSettings(loaded);
      engine.setSettings(loaded);
      setSettingsReady(true);
    });
    const unsubscribe = subscribeAutoMixSettings((next) => {
      setSettings(next);
      engine.setSettings(next);
      if (next.mode === 'off') void engine.dispose();
    });
    return () => {
      mounted = false;
      unsubscribe();
      void engine.dispose();
    };
  }, [engine]);

  useEffect(() => {
    if (!active || !track) return;
    let cancelled = false;
    const current = toAutoMixTrack(track);
    if (!current || engine.getCurrentTrack()?.id === current.id) return;
    void (async () => {
      await engine.dispose();
      if (cancelled) return;
       const player = await createWebAutoMixPlayer();
      try {
        await player.load(await resolveUrlRef.current(track));
        await player.setVolume(1);
        if (isPlaying) await player.play();
        if (!cancelled) {
          engine.attachCurrent(current, player, isPlaying);
          const incoming = toAutoMixTrack(nextTrack);
          if (incoming) void engine.prepareNext(incoming);
        }
        else await player.release();
      } catch (error) {
        await player.release().catch(() => {});
        if (!cancelled) {
          setState('ERROR');
          recordDiagnostic({ category: 'automix', type: 'current_load_failed', data: { trackId: current.id, error: String(error) } });
        }
      }
    })();
    return () => { cancelled = true; };
  }, [active, engine, isPlaying, track?.id]);

  useEffect(() => {
    if (!active || !track) return;
    const current = toAutoMixTrack(track);
    const incoming = toAutoMixTrack(nextTrack);
    if (!current || !incoming) return;
    engine.setAlbumTrackIds(queue.tracks.filter((item) => item.albumId && item.albumId === track.albumId).map((item) => String(item.id)));
    void engine.prepareNext(incoming);
  }, [active, engine, nextTrack?.id, queue.tracks, track?.albumId, track?.id]);

  useEffect(() => {
    if (!active || !track) return;
    void engine.setPlaying(isPlaying).catch((error) => recordDiagnostic({ category: 'automix', type: 'playback_control_failed', data: { error: String(error) } }));
  }, [active, engine, isPlaying, track?.id]);

  useEffect(() => {
    if (!active || !track) {
      setPositionMs(0);
      setDurationMs(0);
      return;
    }
    let cancelled = false;
    const tick = async () => {
      if (cancelled) return;
      const player = engine.getCurrentPlayer();
      if (player) {
        const [position, duration] = await Promise.all([player.getPositionMs?.(), player.getDurationMs?.()]);
        if (typeof position === 'number') setPositionMs(position);
        if (typeof duration === 'number') setDurationMs(duration);
        if (typeof position === 'number' && typeof duration === 'number' && nextTrack) await engine.maybeStartTransition(position, duration, toAutoMixTrack(nextTrack));
      }
    };
    const timer = setInterval(() => { void tick(); }, 250);
    void tick();
    return () => { cancelled = true; clearInterval(timer); };
  }, [active, engine, nextTrack?.id, track?.id]);

  useEffect(() => {
    if (!active) return;
    return () => { void engine.cancelTransition(false); };
  }, [active, engine, track?.id]);

  const setVolume = useCallback(async (volume: number) => {
    const player = engine.getCurrentPlayer();
    if (player) await player.setVolume(Math.min(1, Math.max(0, volume)));
  }, [engine]);
  const play = useCallback(() => engine.setPlaying(true), [engine]);
  const pause = useCallback(() => engine.setPlaying(false), [engine]);
  const seekTo = useCallback(async (value: number) => { await engine.getCurrentPlayer()?.seekToMs?.(Math.max(0, value)); }, [engine]);

  return { active, settingsReady, state, positionMs, durationMs, setVolume, play, pause, seekTo };
}
