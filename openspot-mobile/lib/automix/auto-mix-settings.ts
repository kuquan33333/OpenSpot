import AsyncStorage from '@react-native-async-storage/async-storage';

import { DEFAULT_AUTO_MIX_SETTINGS } from './auto-mix-algorithms';
import type { AutoMixSettings } from './auto-mix-types';

export const AUTO_MIX_SETTINGS_KEY = 'openspot_automix_settings_v1';

type AutoMixSettingsListener = (settings: AutoMixSettings) => void;
const listeners = new Set<AutoMixSettingsListener>();

function isMode(value: unknown): value is AutoMixSettings['mode'] {
  return value === 'off' || value === 'crossfade' || value === 'automix';
}

function normalize(value: unknown): AutoMixSettings {
  const candidate = value && typeof value === 'object' ? value as Partial<AutoMixSettings> : {};
  const duration = candidate.durationMs === 'auto' || typeof candidate.durationMs === 'number'
    ? candidate.durationMs
    : DEFAULT_AUTO_MIX_SETTINGS.durationMs;
  return {
    ...DEFAULT_AUTO_MIX_SETTINGS,
    ...candidate,
    mode: isMode(candidate.mode) ? candidate.mode : DEFAULT_AUTO_MIX_SETTINGS.mode,
    durationMs: duration,
    djMode: candidate.djMode === true,
    bpmMatching: candidate.bpmMatching !== false,
    harmonicMatching: candidate.harmonicMatching !== false,
    skipSameAlbum: candidate.skipSameAlbum === true,
    showMeta: candidate.showMeta !== false,
  };
}

export async function loadAutoMixSettings(): Promise<AutoMixSettings> {
  try {
    const raw = await AsyncStorage.getItem(AUTO_MIX_SETTINGS_KEY);
    return raw ? normalize(JSON.parse(raw)) : DEFAULT_AUTO_MIX_SETTINGS;
  } catch {
    return DEFAULT_AUTO_MIX_SETTINGS;
  }
}

export async function saveAutoMixSettings(settings: AutoMixSettings): Promise<void> {
  const next = normalize(settings);
  await AsyncStorage.setItem(AUTO_MIX_SETTINGS_KEY, JSON.stringify(next));
  listeners.forEach((listener) => listener(next));
}

export function subscribeAutoMixSettings(listener: AutoMixSettingsListener): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export { normalize as normalizeAutoMixSettings };
