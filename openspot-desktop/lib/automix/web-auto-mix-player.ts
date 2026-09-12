import type { AutoMixAudioPlayer } from './auto-mix-types';

type AudioContextConstructor = new () => AudioContext;

function getAudioContextConstructor(): AudioContextConstructor | null {
  if (typeof window === 'undefined') return null;
  const browserWindow = window as Window & { webkitAudioContext?: AudioContextConstructor };
  return window.AudioContext ?? browserWindow.webkitAudioContext ?? null;
}

export function createWebAutoMixPlayer(): AutoMixAudioPlayer {
  let element: HTMLAudioElement | null = null;
  let context: AudioContext | null = null;
  let source: MediaElementAudioSourceNode | null = null;
  let gain: GainNode | null = null;
  let filter: BiquadFilterNode | null = null;

  return {
    async load(url) {
      await this.release();
      element = new window.Audio();
      element.preload = 'auto';
      element.crossOrigin = 'anonymous';
      element.src = url;
      const Context = getAudioContextConstructor();
      if (Context) {
        context = new Context();
        source = context.createMediaElementSource(element);
        gain = context.createGain();
        filter = context.createBiquadFilter();
        filter.type = 'allpass';
        source.connect(filter).connect(gain).connect(context.destination);
      }
      await new Promise<void>((resolve, reject) => {
        const current = element;
        if (!current) return reject(new Error('AutoMix audio element was not created'));
        const onReady = () => { current.removeEventListener('canplay', onReady); current.removeEventListener('error', onError); resolve(); };
        const onError = () => { current.removeEventListener('canplay', onReady); current.removeEventListener('error', onError); reject(new Error('AutoMix audio source failed to load')); };
        current.addEventListener('canplay', onReady, { once: true });
        current.addEventListener('error', onError, { once: true });
        current.load();
      });
    },
    async play() {
      if (!element) throw new Error('AutoMix audio player is not loaded');
      if (context?.state === 'suspended') await context.resume();
      await element.play();
    },
    async pause() { if (!element) throw new Error('AutoMix audio player is not loaded'); element.pause(); },
    async stop() { if (!element) throw new Error('AutoMix audio player is not loaded'); element.pause(); element.currentTime = 0; },
    async release() {
      element?.pause();
      if (element) { element.removeAttribute('src'); element.load(); }
      source?.disconnect(); filter?.disconnect(); gain?.disconnect();
      source = null; filter = null; gain = null;
      if (context) await context.close().catch(() => {});
      context = null; element = null;
    },
    async setVolume(volume) {
      if (!element) throw new Error('AutoMix audio player is not loaded');
      const value = Math.min(1, Math.max(0, volume));
      if (gain) gain.gain.value = value;
      else element.volume = value;
    },
    async setRate(rate) { if (!element) throw new Error('AutoMix audio player is not loaded'); element.playbackRate = Math.min(2, Math.max(0.5, rate)); },
    async getPositionMs() { if (!element) throw new Error('AutoMix audio player is not loaded'); return element.currentTime * 1000; },
    async getDurationMs() { if (!element) throw new Error('AutoMix audio player is not loaded'); return Number.isFinite(element.duration) ? element.duration * 1000 : 0; },
    async seekToMs(positionMs) { if (!element) throw new Error('AutoMix audio player is not loaded'); element.currentTime = Math.max(0, positionMs) / 1000; },
    async setFilter(kind, cutoffHz) {
      if (!filter) return;
      filter.type = kind === 'low-pass' ? 'lowpass' : 'highpass';
      filter.frequency.value = Math.max(20, cutoffHz);
    },
  };
}
