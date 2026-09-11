import TrackPlayer, { Capability } from 'react-native-track-player';

export type PlaybackDiagnosticType =
  | 'PLAYER_SETUP_START'
  | 'PLAYER_SETUP_READY'
  | 'PLAYER_SETUP_ERROR'
  | 'PLAYER_COMMAND_ERROR'
  | 'PLAYER_RETRY'
  | 'PLAYER_RELEASED';

export interface PlaybackDiagnosticEvent {
  type: PlaybackDiagnosticType;
  at: number;
  message?: string;
  data?: Record<string, unknown>;
}

const MAX_DIAGNOSTIC_EVENTS = 120;
const diagnostics: PlaybackDiagnosticEvent[] = [];
let setupPromise: Promise<void> | null = null;
let initialized = false;

function pushDiagnostic(event: Omit<PlaybackDiagnosticEvent, 'at'>): void {
  diagnostics.push({ ...event, at: Date.now() });
  if (diagnostics.length > MAX_DIAGNOSTIC_EVENTS) {
    diagnostics.splice(0, diagnostics.length - MAX_DIAGNOSTIC_EVENTS);
  }
  if (__DEV__) console.debug('[PlaybackRuntime]', event.type, event.message || '', event.data || '');
}

function isAlreadyInitializedError(error: unknown): boolean {
  const message = error instanceof Error ? error.message : String(error);
  return /already (been )?initialized/i.test(message) || /player.*initialized/i.test(message);
}

export function getPlaybackDiagnostics(): PlaybackDiagnosticEvent[] {
  return [...diagnostics];
}

export function clearPlaybackDiagnostics(): void {
  diagnostics.length = 0;
}

export function isTrackPlayerReady(): boolean {
  return initialized;
}

export async function ensureTrackPlayerReady(initialVolume = 1): Promise<void> {
  if (initialized) return;
  if (setupPromise) return setupPromise;

  pushDiagnostic({ type: 'PLAYER_SETUP_START' });

  setupPromise = (async () => {
    try {
      try {
        await TrackPlayer.setupPlayer();
      } catch (error) {
        if (!isAlreadyInitializedError(error)) throw error;
      }

      await TrackPlayer.updateOptions({
        capabilities: [
          Capability.Play,
          Capability.Pause,
          Capability.SkipToNext,
          Capability.SkipToPrevious,
          Capability.SeekTo,
          Capability.Stop,
        ],
        compactCapabilities: [Capability.Play, Capability.Pause, Capability.SkipToNext],
      });
      await TrackPlayer.setVolume(initialVolume);
      initialized = true;
      pushDiagnostic({ type: 'PLAYER_SETUP_READY' });
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      pushDiagnostic({ type: 'PLAYER_SETUP_ERROR', message });
      throw error;
    } finally {
      setupPromise = null;
    }
  })();

  return setupPromise;
}

export async function runPlayerCommand<T>(name: string, command: () => Promise<T>): Promise<T> {
  try {
    await ensureTrackPlayerReady();
    return await command();
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    pushDiagnostic({ type: 'PLAYER_COMMAND_ERROR', message, data: { command: name } });
    throw error;
  }
}

function isRetryablePlaybackError(error: unknown): boolean {
  const message = error instanceof Error ? error.message : String(error);
  return /401|403|404|408|425|429|5\d\d|timeout|network|expired|fetch|socket/i.test(message);
}

export async function withPlaybackRetry<T>(name: string, command: () => Promise<T>, maxRetries = 2): Promise<T> {
  let lastError: unknown;
  for (let attempt = 0; attempt <= maxRetries; attempt += 1) {
    try { return await command(); }
    catch (error) {
      lastError = error;
      if (attempt >= maxRetries || !isRetryablePlaybackError(error)) throw error;
      pushDiagnostic({ type: 'PLAYER_RETRY', message: error instanceof Error ? error.message : String(error), data: { command: name, attempt: attempt + 1 } });
      await new Promise((resolve) => setTimeout(resolve, 250 * (attempt + 1)));
    }
  }
  throw lastError instanceof Error ? lastError : new Error(`Playback command failed: ${name}`);
}

export async function releaseTrackPlayer(): Promise<void> {
  try {
    await TrackPlayer.stop();
  } catch {}
  try {
    await TrackPlayer.reset();
  } catch {}
  initialized = false;
  setupPromise = null;
  pushDiagnostic({ type: 'PLAYER_RELEASED' });
}
