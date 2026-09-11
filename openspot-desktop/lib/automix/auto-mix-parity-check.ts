import {
  AUTO_FALLBACK_DURATION_MS,
  calculateBpmSpeedRatio,
  camelotDistance,
  equalPowerGains,
  exponentialInterpolate,
  keyToCamelot,
  planAutoMixTransition,
  resolveAutoCrossfadeDuration,
  DEFAULT_AUTO_MIX_SETTINGS,
} from './auto-mix-algorithms';

export interface AutoMixParityCheckResult { passed: boolean; checks: Record<string, boolean>; }
const assertClose = (left: number, right: number, epsilon = 0.0001): boolean => Math.abs(left - right) <= epsilon;

export function runAutoMixParityChecks(): AutoMixParityCheckResult {
  const current = { id: 'current', durationMs: 180_000, audioMeta: { bpm: 100, key: 'C', keyScale: 'major' } };
  const next = { id: 'next', durationMs: 180_000, audioMeta: { bpm: 110, key: 'G', keyScale: 'major' } };
  const auto = resolveAutoCrossfadeDuration(current.audioMeta, next.audioMeta);
  const checks = {
    equal_power_start: assertClose(equalPowerGains(0).outgoing, 1) && assertClose(equalPowerGains(0).incoming, 0),
    equal_power_middle: assertClose(equalPowerGains(0.5).outgoing, Math.SQRT1_2) && assertClose(equalPowerGains(0.5).incoming, Math.SQRT1_2),
    equal_power_end: assertClose(equalPowerGains(1).outgoing, 0) && assertClose(equalPowerGains(1).incoming, 1),
    bpm_half_time: assertClose(calculateBpmSpeedRatio(70, 140), 1),
    bpm_safe_quantized: assertClose(calculateBpmSpeedRatio(100, 110), 1.1),
    bpm_unsafe_skipped: assertClose(calculateBpmSpeedRatio(100, 140), 1),
    camelot_major: keyToCamelot('C', 'major')?.number === 8,
    camelot_minor: keyToCamelot('A', 'minor')?.number === 8,
    camelot_distance: camelotDistance({ number: 12, isMinor: false }, { number: 1, isMinor: false }) === 1,
    auto_fallback: resolveAutoCrossfadeDuration(null, next.audioMeta).durationMs === AUTO_FALLBACK_DURATION_MS,
    auto_clamped: auto.durationMs >= 20_000 && auto.durationMs <= 45_000,
    filter_interpolation: assertClose(exponentialInterpolate(20_000, 200, 0), 20_000) && assertClose(exponentialInterpolate(20_000, 200, 1), 200),
    video_guard: planAutoMixTransition({ ...current, isVideo: true }, next, { ...DEFAULT_AUTO_MIX_SETTINGS, mode: 'crossfade', durationMs: 5_000 }).reason === 'current_video',
    album_guard: planAutoMixTransition(current, next, { ...DEFAULT_AUTO_MIX_SETTINGS, mode: 'crossfade', durationMs: 5_000, skipSameAlbum: true }, new Set(['current', 'next'])).reason === 'same_album',
    short_track_guard: planAutoMixTransition({ ...current, durationMs: 20_000 }, next, { ...DEFAULT_AUTO_MIX_SETTINGS, mode: 'crossfade', durationMs: 5_000 }).reason === 'current_track_too_short',
  };
  return { passed: Object.values(checks).every(Boolean), checks };
}
