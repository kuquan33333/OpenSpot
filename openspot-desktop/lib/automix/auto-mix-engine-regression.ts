import { AutoMixEngine } from './auto-mix-engine';
import type { AutoMixAudioPlayer, AutoMixTrack } from './auto-mix-types';

class FakePlayer implements AutoMixAudioPlayer {
  loadCount = 0;
  playCount = 0;
  pauseCount = 0;
  stopCount = 0;
  releaseCount = 0;
  volume = 1;

  async load(_url: string): Promise<void> { this.loadCount += 1; }
  async play(): Promise<void> { this.playCount += 1; }
  async pause(): Promise<void> { this.pauseCount += 1; }
  async stop(): Promise<void> { this.stopCount += 1; }
  async release(): Promise<void> { this.releaseCount += 1; }
  async setVolume(volume: number): Promise<void> { this.volume = volume; }
  async setRate(_rate: number, _shouldCorrectPitch?: boolean): Promise<void> {}
}

const assert = (condition: unknown, message: string): void => {
  if (!condition) throw new Error(`AutoMix regression failed: ${message}`);
};

export async function runAutoMixEngineRegression(): Promise<void> {
  const current: AutoMixTrack = { id: 'current', durationMs: 180_000 };
  const next: AutoMixTrack = { id: 'next', durationMs: 180_000 };
  const currentPlayer = new FakePlayer();
  const createdPlayers: FakePlayer[] = [];
  let callbackTrackId: string | null = null;
  let callbackPlayer: AutoMixAudioPlayer | null = null;

  const engine = new AutoMixEngine<AutoMixTrack>({
    createPlayer: async () => {
      const player = new FakePlayer();
      createdPlayers.push(player);
      return player;
    },
    resolveUrl: async (track) => `https://example.invalid/${track.id}.mp3`,
    onTakeover: ({ track, player }) => {
      callbackTrackId = track.id;
      callbackPlayer = player;
      assert(engine.getCurrentTrack()?.id === 'next', 'current track must be promoted before UI callback');
      assert(engine.getCurrentPlayer() === player, 'current player must be promoted before UI callback');
    },
  }, {
    mode: 'crossfade',
    durationMs: 1_000,
    djMode: false,
    bpmMatching: true,
    harmonicMatching: true,
    skipSameAlbum: false,
    showMeta: true,
  });

  await currentPlayer.load('https://example.invalid/current.mp3');
  engine.attachCurrent(current, currentPlayer, true);
  assert(await engine.prepareNext(next), 'next track should be prepared');
  const nextPlayer = createdPlayers[0];
  assert(nextPlayer !== undefined, 'prepareNext must create an incoming player');
  assert(await engine.maybeStartTransition(179_000, 180_000, next), 'transition should complete');

  assert(callbackTrackId === 'next', 'takeover callback must receive the next track');
  assert(callbackPlayer === nextPlayer, 'takeover callback must receive the preloaded player');
  assert(engine.getCurrentTrack()?.id === 'next', 'next track must remain current after takeover');
  assert(engine.getCurrentPlayer() === nextPlayer, 'preloaded player must remain current after takeover');
  assert(nextPlayer.loadCount === 1, 'incoming player must not be reloaded at position zero');
  assert(nextPlayer.playCount === 1, 'incoming player must play exactly once');
  assert(currentPlayer.stopCount === 1, 'outgoing player must stop after takeover');
  assert(currentPlayer.releaseCount === 1, 'outgoing player must release after takeover');

  await engine.dispose();
}

if (typeof require !== 'undefined' && require.main === module) {
  runAutoMixEngineRegression()
    .then(() => process.stdout.write('AutoMix takeover regression: PASS\n'))
    .catch((error: unknown) => {
      process.stderr.write(`${String(error)}\n`);
      process.exitCode = 1;
    });
}
