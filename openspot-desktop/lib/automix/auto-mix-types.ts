export type AutoMixMode = 'off' | 'crossfade' | 'automix';

export type AutoMixDuration = 'auto' | number;

export interface SongAudioMeta {
  bpm?: number | null;
  key?: string | null;
  keyScale?: string | null;
  source?: 'provider' | 'cache' | 'local' | 'unknown';
}

export interface AutoMixTrack {
  id: string;
  durationMs?: number | null;
  albumId?: string | null;
  isVideo?: boolean;
  audioMeta?: SongAudioMeta | null;
}

export interface AutoMixSettings {
  mode: AutoMixMode;
  durationMs: AutoMixDuration;
  djMode: boolean;
  bpmMatching: boolean;
  harmonicMatching: boolean;
  skipSameAlbum: boolean;
  showMeta: boolean;
}

export interface CamelotCode {
  number: number;
  isMinor: boolean;
}

export interface EqualPowerGains {
  outgoing: number;
  incoming: number;
}

export interface FilterCutoffs {
  outgoingLowPassHz: number;
  incomingHighPassHz: number;
}

export interface AutoMixTransitionPlan {
  enabled: boolean;
  reason: string;
  durationMs: number;
  beatCount: number | null;
  bpmRatio: number;
  pitchRatio: number;
  currentCamelot: CamelotCode | null;
  nextCamelot: CamelotCode | null;
  keyDistance: number | null;
  djMode: boolean;
}

export type AutoMixPlayerState = 'IDLE' | 'PREPARING' | 'READY' | 'PLAYING' | 'PAUSED' | 'ENDED' | 'ERROR';

export interface AutoMixAudioPlayer {
  load(url: string): Promise<void>;
  play(): Promise<void>;
  pause(): Promise<void>;
  stop(): Promise<void>;
  release(): Promise<void>;
  setVolume(volume: number): Promise<void>;
  setRate(rate: number, shouldCorrectPitch?: boolean): Promise<void>;
  getPositionMs?(): Promise<number>;
  getDurationMs?(): Promise<number>;
  seekToMs?(positionMs: number): Promise<void>;
  setPitch?(ratio: number): Promise<void>;
  setFilter?(kind: 'low-pass' | 'high-pass', cutoffHz: number): Promise<void>;
}

export interface AutoMixTakeover<T extends AutoMixTrack> {
  track: T;
  player: AutoMixAudioPlayer;
  plan: AutoMixTransitionPlan;
}
