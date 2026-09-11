import type { AutoMixSettings, AutoMixTrack, AutoMixTransitionPlan, CamelotCode, EqualPowerGains, FilterCutoffs, SongAudioMeta } from './auto-mix-types';

export const AUTO_FALLBACK_DURATION_MS = 30_000;
export const AUTO_MIN_DURATION_MS = 20_000;
export const AUTO_MAX_DURATION_MS = 45_000;
export const MIN_CROSSFADE_TRACK_MS = 20_000;
export const BEAT_COUNT_OPTIONS = [8, 16, 24, 32, 40, 48, 64, 80, 96] as const;
export const DEFAULT_BEAT_COUNT = 32;
export const BPM_RATIO_MIN = 0.75;
export const BPM_RATIO_MAX = 1.25;
export const SPEED_PITCH_STEP = 0.02;
export const BPM_RAMP_PORTION = 0.6;
export const DJ_FILTER_SIGMOID_K = 6;
export const LPF_START_HZ = 20_000;
export const LPF_END_HZ = 200;
export const HPF_START_HZ = 2_000;
export const HPF_END_HZ = 20;

const MINOR_CAMELOT_BY_PITCH = [5, 12, 7, 2, 9, 4, 11, 6, 1, 8, 3, 10];
const MAJOR_CAMELOT_BY_PITCH = [8, 3, 10, 5, 12, 7, 2, 9, 4, 11, 6, 1];

function finitePositive(value: number | null | undefined): number | null {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : null;
}

export function quantize(value: number, step = SPEED_PITCH_STEP): number {
  if (!Number.isFinite(value)) return 1;
  return Math.round(value / step) * step;
}

export function normalizeBpmRatio(currentBpm: number, nextBpm: number): number {
  const current = finitePositive(currentBpm);
  const next = finitePositive(nextBpm);
  if (current === null || next === null) return 1;
  let ratio = next / current;
  while (ratio > 1.5) ratio /= 2;
  while (ratio < 0.67) ratio *= 2;
  return ratio;
}

export function calculateBpmSpeedRatio(currentBpm: number, nextBpm: number): number {
  const ratio = normalizeBpmRatio(currentBpm, nextBpm);
  return ratio >= BPM_RATIO_MIN && ratio <= BPM_RATIO_MAX ? quantize(ratio) : 1;
}

export function calculateBpmGapDurationFactor(currentBpm: number, nextBpm: number): number {
  const current = finitePositive(currentBpm);
  const next = finitePositive(nextBpm);
  if (current === null || next === null) return 1;
  return 1 + Math.abs(1 - normalizeBpmRatio(current, next)) * 2;
}

export function getAutoTargetDurationMs(bpm: number): number {
  const clampedBpm = Math.min(170, Math.max(70, bpm));
  return 30_000 - (clampedBpm - 70) * 230;
}

export function keyToSemitone(key: string | null | undefined): number {
  if (!key) return -1;
  const normalized = key.trim().replace(/Sharp/gi, '#').replace(/Flat/gi, 'b').replace(/^./, (value) => value.toUpperCase());
  switch (normalized) {
    case 'C': return 0;
    case 'C#': case 'Db': return 1;
    case 'D': return 2;
    case 'D#': case 'Eb': return 3;
    case 'E': return 4;
    case 'F': return 5;
    case 'F#': case 'Gb': return 6;
    case 'G': return 7;
    case 'G#': case 'Ab': return 8;
    case 'A': return 9;
    case 'A#': case 'Bb': return 10;
    case 'B': return 11;
    default: return -1;
  }
}

export function keyToCamelot(key: string | null | undefined, keyScale?: string | null): CamelotCode | null {
  const semitone = keyToSemitone(key);
  if (semitone < 0) return null;
  const isMinor = keyScale?.toUpperCase().includes('MIN') === true;
  return { number: (isMinor ? MINOR_CAMELOT_BY_PITCH : MAJOR_CAMELOT_BY_PITCH)[semitone], isMinor };
}

export function camelotDistance(a: CamelotCode, b: CamelotCode): number {
  const numberDiff = Math.abs(a.number - b.number);
  return Math.min(numberDiff, 12 - numberDiff) + (a.isMinor === b.isMinor ? 0 : 1);
}

export function calculateKeyGapDurationFactor(current: SongAudioMeta, next: SongAudioMeta): number {
  const currentCamelot = keyToCamelot(current.key, current.keyScale);
  const nextCamelot = keyToCamelot(next.key, next.keyScale);
  if (!currentCamelot || !nextCamelot) return 1.25;
  const distance = camelotDistance(currentCamelot, nextCamelot);
  if (distance <= 1) return 1;
  if (distance === 2) return 1.1;
  if (distance <= 4) return 1.25;
  return 1.4;
}

export function calculateKeyPitchRatio(current: SongAudioMeta, next: SongAudioMeta): number {
  const currentCamelot = keyToCamelot(current.key, current.keyScale);
  const nextCamelot = keyToCamelot(next.key, next.keyScale);
  if (!currentCamelot || !nextCamelot || camelotDistance(currentCamelot, nextCamelot) <= 1) return 1;
  const currentSemitone = keyToSemitone(current.key);
  if (currentSemitone < 0) return 1;
  for (const shift of [-1, 1, -2, 2]) {
    const shiftedSemitone = (currentSemitone + shift + 12) % 12;
    const shiftedCamelot: CamelotCode = { number: (currentCamelot.isMinor ? MINOR_CAMELOT_BY_PITCH : MAJOR_CAMELOT_BY_PITCH)[shiftedSemitone], isMinor: currentCamelot.isMinor };
    if (camelotDistance(shiftedCamelot, nextCamelot) <= 1) return Math.pow(2, shift / 12);
  }
  return 1;
}

export function equalPowerGains(progress: number): EqualPowerGains {
  const bounded = Math.min(1, Math.max(0, progress));
  const angle = bounded * Math.PI / 2;
  return { outgoing: Math.cos(angle), incoming: Math.sin(angle) };
}

export function sigmoid(progress: number, steepness = DJ_FILTER_SIGMOID_K): number {
  return 1 / (1 + Math.exp(-steepness * (progress - 0.5)));
}

export function exponentialInterpolate(start: number, end: number, progress: number): number {
  if (start <= 0 || end <= 0) return end;
  const bounded = Math.min(1, Math.max(0, progress));
  return Math.exp(Math.log(start) + (Math.log(end) - Math.log(start)) * bounded);
}

export function filterCutoffs(progress: number): FilterCutoffs {
  const filterProgress = sigmoid(Math.min(1, Math.max(0, progress)));
  return { outgoingLowPassHz: exponentialInterpolate(LPF_START_HZ, LPF_END_HZ, filterProgress), incomingHighPassHz: exponentialInterpolate(HPF_START_HZ, HPF_END_HZ, filterProgress) };
}

export function resolveAutoCrossfadeDuration(current: SongAudioMeta | null | undefined, next: SongAudioMeta | null | undefined): { durationMs: number; beatCount: number | null } {
  const currentBpm = finitePositive(current?.bpm);
  const nextBpm = finitePositive(next?.bpm);
  if (currentBpm === null || nextBpm === null) return { durationMs: AUTO_FALLBACK_DURATION_MS, beatCount: null };
  const beatMs = 60_000 / currentBpm;
  const target = getAutoTargetDurationMs(currentBpm) * calculateBpmGapDurationFactor(currentBpm, nextBpm) * calculateKeyGapDurationFactor(current ?? {}, next ?? {});
  let bestBeatCount = DEFAULT_BEAT_COUNT;
  let bestDistance = Number.POSITIVE_INFINITY;
  for (const beatCount of BEAT_COUNT_OPTIONS) {
    const distance = Math.abs(beatCount * beatMs - target);
    if (distance < bestDistance) { bestDistance = distance; bestBeatCount = beatCount; }
  }
  return { durationMs: Math.min(AUTO_MAX_DURATION_MS, Math.max(AUTO_MIN_DURATION_MS, Math.trunc(bestBeatCount * beatMs))), beatCount: bestBeatCount };
}

function manualDuration(duration: AutoMixSettings['durationMs']): number {
  return duration === 'auto' ? AUTO_FALLBACK_DURATION_MS : Math.max(1_000, Math.trunc(duration));
}

export function planAutoMixTransition(current: AutoMixTrack, next: AutoMixTrack | null | undefined, settings: AutoMixSettings, albumTrackIds: ReadonlySet<string> = new Set()): AutoMixTransitionPlan {
  const noTransition = (reason: string): AutoMixTransitionPlan => ({ enabled: false, reason, durationMs: manualDuration(settings.durationMs), beatCount: null, bpmRatio: 1, pitchRatio: 1, currentCamelot: null, nextCamelot: null, keyDistance: null, djMode: settings.djMode });
  if (settings.mode === 'off') return noTransition('mode_off');
  if (!next) return noTransition('no_next_track');
  if (current.isVideo) return noTransition('current_video');
  if (next.isVideo) return noTransition('next_video');
  if (settings.skipSameAlbum && albumTrackIds.has(current.id) && albumTrackIds.has(next.id)) return noTransition('same_album');
  const autoDuration = resolveAutoCrossfadeDuration(current.audioMeta, next.audioMeta);
  const durationMs = settings.durationMs === 'auto' ? autoDuration.durationMs : manualDuration(settings.durationMs);
  const currentDuration = finitePositive(current.durationMs);
  if (currentDuration !== null && currentDuration < Math.max(MIN_CROSSFADE_TRACK_MS, durationMs * 3)) return noTransition('current_track_too_short');
  const currentCamelot = keyToCamelot(current.audioMeta?.key, current.audioMeta?.keyScale);
  const nextCamelot = keyToCamelot(next.audioMeta?.key, next.audioMeta?.keyScale);
  const keyDistance = currentCamelot && nextCamelot ? camelotDistance(currentCamelot, nextCamelot) : null;
  const bpmRatio = settings.mode === 'automix' && settings.bpmMatching && current.audioMeta?.bpm && next.audioMeta?.bpm ? calculateBpmSpeedRatio(current.audioMeta.bpm, next.audioMeta.bpm) : 1;
  const pitchRatio = settings.mode === 'automix' && settings.harmonicMatching ? calculateKeyPitchRatio(current.audioMeta ?? {}, next.audioMeta ?? {}) : 1;
  return { enabled: true, reason: 'ready', durationMs, beatCount: settings.durationMs === 'auto' ? autoDuration.beatCount : null, bpmRatio, pitchRatio, currentCamelot, nextCamelot, keyDistance, djMode: settings.djMode };
}

export const DEFAULT_AUTO_MIX_SETTINGS: AutoMixSettings = { mode: 'off', durationMs: 'auto', djMode: false, bpmMatching: true, harmonicMatching: true, skipSameAlbum: false, showMeta: true };
