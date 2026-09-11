import {
  BPM_RAMP_PORTION,
  equalPowerGains,
  filterCutoffs,
  planAutoMixTransition,
  quantize,
} from './auto-mix-algorithms';
import type {
  AutoMixAudioPlayer,
  AutoMixPlayerState,
  AutoMixSettings,
  AutoMixTakeover,
  AutoMixTrack,
  AutoMixTransitionPlan,
} from './auto-mix-types';

export const AUTOMIX_PREPARE_LEAD_MS = 3_000;
export const AUTOMIX_STEP_COUNT = 50;

export interface AutoMixEngineOptions<T extends AutoMixTrack> {
  createPlayer: () => Promise<AutoMixAudioPlayer>;
  resolveUrl: (track: T, signal: AbortSignal) => Promise<string>;
  onStateChange?: (state: AutoMixPlayerState) => void;
  onTakeover?: (takeover: AutoMixTakeover<T>) => Promise<void> | void;
  onDiagnostic?: (event: { type: string; trackId?: string; detail?: Record<string, unknown> }) => void;
}

interface ActivePlayer<T extends AutoMixTrack> { track: T; player: AutoMixAudioPlayer; }

const delay = (ms: number, signal: AbortSignal): Promise<void> => new Promise((resolve, reject) => {
  if (signal.aborted) { reject(new Error('AutoMix transition cancelled')); return; }
  const cancel = () => {
    clearTimeout(timer);
    signal.removeEventListener('abort', cancel);
    reject(new Error('AutoMix transition cancelled'));
  };
  const timer = setTimeout(() => {
    signal.removeEventListener('abort', cancel);
    resolve();
  }, ms);
  signal.addEventListener('abort', cancel, { once: true });
});

export class AutoMixEngine<T extends AutoMixTrack> {
  private settings: AutoMixSettings;
  private current: ActivePlayer<T> | null = null;
  private secondary: ActivePlayer<T> | null = null;
  private precached = new Map<string, ActivePlayer<T>>();
  private prepareAbort: AbortController | null = null;
  private transitionAbort: AbortController | null = null;
  private transitionPromise: Promise<void> | null = null;
  private takeoverOnCancel = false;
  private state: AutoMixPlayerState = 'IDLE';
  private albumTrackIds = new Set<string>();

  constructor(private readonly options: AutoMixEngineOptions<T>, settings: AutoMixSettings) { this.settings = settings; }
  getState(): AutoMixPlayerState { return this.state; }
  getCurrentTrack(): T | null { return this.current?.track ?? null; }
  getNextTrack(): T | null { return this.secondary?.track ?? null; }

  setSettings(settings: AutoMixSettings): void { this.settings = settings; if (settings.mode === 'off') void this.cancelTransition(false); }
  setAlbumTrackIds(ids: Iterable<string>): void { this.albumTrackIds = new Set(ids); }

  attachCurrent(track: T, player: AutoMixAudioPlayer, playing: boolean): void {
    if (this.current && this.current.player !== player) void this.current.player.release().catch(() => {});
    this.current = { track, player };
    this.setState(playing ? 'PLAYING' : 'READY');
  }

  async setPlaying(playing: boolean): Promise<void> {
    if (!this.current || this.transitionPromise) return;
    if (playing) { await this.current.player.play(); this.setState('PLAYING'); }
    else { await this.current.player.pause(); this.setState('PAUSED'); }
  }

  async prepareNext(track: T): Promise<boolean> {
    if (!this.current || this.settings.mode === 'off') return false;
    if (this.secondary?.track.id === track.id) return true;
    this.prepareAbort?.abort();
    this.prepareAbort = new AbortController();
    const { signal } = this.prepareAbort;
    this.setState('PREPARING');
    await this.releaseSecondary();
    try {
      const cached = this.precached.get(track.id);
      if (cached) this.precached.delete(track.id);
      const player = cached?.player ?? await this.options.createPlayer();
      if (!cached) { const url = await this.options.resolveUrl(track, signal); await player.load(url); }
      await player.setVolume(0);
      await player.pause();
      if (signal.aborted) { await player.release(); return false; }
      this.secondary = { track, player };
      this.setState('READY');
      this.options.onDiagnostic?.({ type: 'precache_ready', trackId: track.id });
      return true;
    } catch (error) {
      if (signal.aborted) return false;
      this.setState('ERROR');
      this.options.onDiagnostic?.({ type: 'precache_failed', trackId: track.id, detail: { error: String(error) } });
      return false;
    }
  }

  async precache(track: T): Promise<boolean> {
    if (this.settings.mode === 'off' || this.precached.has(track.id)) return false;
    const controller = new AbortController();
    try {
      const player = await this.options.createPlayer();
      const url = await this.options.resolveUrl(track, controller.signal);
      await player.load(url);
      await player.setVolume(0);
      await player.pause();
      if (controller.signal.aborted) { await player.release(); return false; }
      this.precached.set(track.id, { track, player });
      this.options.onDiagnostic?.({ type: 'precache_ready', trackId: track.id });
      return true;
    } catch (error) {
      this.options.onDiagnostic?.({ type: 'precache_failed', trackId: track.id, detail: { error: String(error) } });
      return false;
    }
  }

  plan(next: T | null | undefined): AutoMixTransitionPlan | null {
    return this.current ? planAutoMixTransition(this.current.track, next, this.settings, this.albumTrackIds) : null;
  }

  async maybeStartTransition(positionMs: number, durationMs: number, next: T | null | undefined): Promise<boolean> {
    const plan = this.plan(next);
    if (!plan?.enabled || !next || !this.current || this.transitionPromise) return false;
    const remaining = durationMs - positionMs;
    if (remaining < 0 || remaining > plan.durationMs + AUTOMIX_PREPARE_LEAD_MS) return false;
    if (!this.secondary || this.secondary.track.id !== next.id) return false;
    this.transitionPromise = this.startTransition(next, plan).finally(() => { this.transitionPromise = null; });
    await this.transitionPromise;
    return true;
  }

  async cancelTransition(takeoverIncoming: boolean): Promise<void> {
    this.takeoverOnCancel = takeoverIncoming;
    this.transitionAbort?.abort();
    const pending = this.transitionPromise;
    if (pending) await pending.catch(() => {});
    this.takeoverOnCancel = false;
    if (takeoverIncoming && this.secondary) await this.promoteSecondary();
  }

  async dispose(): Promise<void> {
    this.prepareAbort?.abort();
    await this.cancelTransition(false);
    await this.releaseSecondary();
    for (const item of this.precached.values()) await item.player.release().catch(() => {});
    this.precached.clear();
    await this.current?.player.release().catch(() => {});
    this.current = null;
    this.setState('IDLE');
  }

  private async startTransition(next: T, plan: AutoMixTransitionPlan): Promise<void> {
    const incoming = this.secondary;
    const outgoing = this.current;
    if (!incoming || !outgoing || incoming.track.id !== next.id) return;
    const controller = new AbortController();
    this.transitionAbort = controller;
    this.setState('PLAYING');
    this.options.onDiagnostic?.({ type: 'automix_started', trackId: next.id, detail: { durationMs: plan.durationMs, beatCount: plan.beatCount, bpmRatio: plan.bpmRatio, pitchRatio: plan.pitchRatio } });
    try {
      await incoming.player.setVolume(0);
      await incoming.player.play();
      const stepDelay = Math.max(20, Math.trunc(plan.durationMs / AUTOMIX_STEP_COUNT));
      let lastRate = -1;
      let lastPitch = -1;
      for (let step = 0; step <= AUTOMIX_STEP_COUNT; step += 1) {
        await delay(stepDelay, controller.signal);
        const progress = step / AUTOMIX_STEP_COUNT;
        const gains = equalPowerGains(progress);
        await outgoing.player.setVolume(gains.outgoing);
        await incoming.player.setVolume(gains.incoming);
        if (plan.djMode) {
          const cutoffs = filterCutoffs(progress);
          await outgoing.player.setFilter?.('low-pass', cutoffs.outgoingLowPassHz);
          await incoming.player.setFilter?.('high-pass', cutoffs.incomingHighPassHz);
        }
        if (plan.bpmRatio !== 1 || plan.pitchRatio !== 1) {
          const linear = Math.min(1, progress / BPM_RAMP_PORTION);
          const ramp = linear * linear * (3 - 2 * linear);
          const rate = quantize(1 + (plan.bpmRatio - 1) * ramp);
          const pitch = quantize(1 + (plan.pitchRatio - 1) * ramp);
          if (rate !== lastRate) { await outgoing.player.setRate(rate, true); lastRate = rate; }
          if (plan.pitchRatio !== 1 && pitch !== lastPitch) { await outgoing.player.setPitch?.(pitch); lastPitch = pitch; }
        }
      }
      await incoming.player.setVolume(1);
      await outgoing.player.setVolume(0);
      await this.options.onTakeover?.({ track: next, player: incoming.player, plan });
      await outgoing.player.stop().catch(() => {});
      await outgoing.player.release().catch(() => {});
      this.current = incoming;
      this.secondary = null;
      this.options.onDiagnostic?.({ type: 'automix_finished', trackId: next.id });
      this.setState('PLAYING');
    } catch (error) {
      await outgoing.player.setVolume(1).catch(() => {});
      await incoming.player.setVolume(0).catch(() => {});
      if (controller.signal.aborted) {
        this.options.onDiagnostic?.({ type: 'automix_cancelled', trackId: next.id });
        if (this.takeoverOnCancel) await this.promoteSecondary();
        else { await incoming.player.pause().catch(() => {}); await incoming.player.release().catch(() => {}); this.secondary = null; this.setState('READY'); }
      } else {
        this.options.onDiagnostic?.({ type: 'automix_failed', trackId: next.id, detail: { error: String(error) } });
        await incoming.player.release().catch(() => {});
        this.secondary = null;
        this.setState('ERROR');
      }
    } finally { this.transitionAbort = null; }
  }

  private async promoteSecondary(): Promise<void> {
    const incoming = this.secondary;
    const outgoing = this.current;
    if (!incoming) return;
    await incoming.player.setVolume(1).catch(() => {});
    await incoming.player.pause().catch(() => {});
    await outgoing?.player.stop().catch(() => {});
    await outgoing?.player.release().catch(() => {});
    this.current = incoming;
    this.secondary = null;
    this.setState('PAUSED');
  }

  private async releaseSecondary(): Promise<void> {
    if (!this.secondary) return;
    await this.secondary.player.release().catch(() => {});
    this.secondary = null;
  }

  private setState(state: AutoMixPlayerState): void { this.state = state; this.options.onStateChange?.(state); }
}
