import TrackPlayer from 'react-native-track-player';

import type { AutoMixAudioPlayer } from './auto-mix-types';

export function createTrackPlayerAutoMixPlayer(): AutoMixAudioPlayer {
  return {
    async load() {},
    async play() { await TrackPlayer.play(); },
    async pause() { await TrackPlayer.pause(); },
    async stop() { await TrackPlayer.stop(); },
    async release() {},
    async setVolume(volume) { await TrackPlayer.setVolume(Math.min(1, Math.max(0, volume))); },
    async setRate(rate) { await TrackPlayer.setRate(rate); },
    async getPositionMs() { return (await TrackPlayer.getProgress()).position * 1000; },
    async getDurationMs() { return (await TrackPlayer.getProgress()).duration * 1000; },
    async seekToMs(positionMs) { await TrackPlayer.seekTo(Math.max(0, positionMs) / 1000); },
  };
}
