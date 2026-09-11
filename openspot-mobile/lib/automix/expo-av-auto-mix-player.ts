import { Audio } from 'expo-av';

import type { AutoMixAudioPlayer } from './auto-mix-types';

function requireLoaded(sound: Audio.Sound | null): Audio.Sound {
  if (!sound) throw new Error('AutoMix audio player is not loaded');
  return sound;
}

export async function configureAutoMixAudio(): Promise<void> {
  await Audio.setAudioModeAsync({
    playsInSilentModeIOS: true,
    staysActiveInBackground: true,
    shouldDuckAndroid: false,
    playThroughEarpieceAndroid: false,
  });
}

export function createExpoAvAutoMixPlayer(): AutoMixAudioPlayer {
  let sound: Audio.Sound | null = null;
  return {
    async load(url) {
      if (sound) await sound.unloadAsync().catch(() => {});
      const result = await Audio.Sound.createAsync({ uri: url }, { shouldPlay: false, volume: 0 });
      sound = result.sound;
    },
    async play() { await requireLoaded(sound).playAsync(); },
    async pause() { await requireLoaded(sound).pauseAsync(); },
    async stop() { await requireLoaded(sound).stopAsync(); },
    async release() {
      if (sound) await sound.unloadAsync().catch(() => {});
      sound = null;
    },
    async setVolume(volume) { await requireLoaded(sound).setVolumeAsync(Math.min(1, Math.max(0, volume))); },
    async setRate(rate, shouldCorrectPitch = true) {
      await requireLoaded(sound).setRateAsync(Math.min(2, Math.max(0.5, rate)), shouldCorrectPitch);
    },
  };
}
