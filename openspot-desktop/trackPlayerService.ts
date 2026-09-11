import TrackPlayer, { Event } from 'react-native-track-player';
import { ensureTrackPlayerReady, runPlayerCommand } from './lib/playback/track-player-runtime';

export default async function trackPlayerService() {
  await ensureTrackPlayerReady().catch(() => {});

  TrackPlayer.addEventListener(Event.RemotePlay, () => {
    void runPlayerCommand('remote_play', () => TrackPlayer.play()).catch(() => {});
  });
  TrackPlayer.addEventListener(Event.RemotePause, () => {
    void runPlayerCommand('remote_pause', () => TrackPlayer.pause()).catch(() => {});
  });
  TrackPlayer.addEventListener(Event.RemoteStop, () => {
    void runPlayerCommand('remote_stop', () => TrackPlayer.stop()).catch(() => {});
  });

  TrackPlayer.addEventListener(Event.RemoteNext, async () => {
    try {
      await runPlayerCommand('remote_next', async () => {
        await TrackPlayer.skipToNext();
        await TrackPlayer.play();
      });
    } catch {}
  });

  TrackPlayer.addEventListener(Event.RemotePrevious, async () => {
    try {
      await runPlayerCommand('remote_previous', async () => {
        await TrackPlayer.skipToPrevious();
        await TrackPlayer.play();
      });
    } catch {}
  });

  TrackPlayer.addEventListener(Event.RemoteSeek, async (event) => {
    try {
      await runPlayerCommand('remote_seek', () => TrackPlayer.seekTo((event as any).position));
    } catch {}
  });
}
