import { decodeExtensionCoreResponse } from './extension-core-response';

function assertEqual<T>(actual: T, expected: T, label: string) {
  if (actual !== expected) {
    throw new Error(`${label}: expected ${String(expected)}, received ${String(actual)}`);
  }
}

assertEqual(
  decodeExtensionCoreResponse<string>('GetRepoRegistryURLJSON', 'https://raw.githubusercontent.com/example/OpenSpot/main/registry.json'),
  'https://raw.githubusercontent.com/example/OpenSpot/main/registry.json',
  'raw registry URL',
);
assertEqual(
  decodeExtensionCoreResponse<string>('DownloadRepoExtensionJSON', '/var/mobile/Containers/Data/Application/OpenSpot/spotify-web.sflx'),
  '/var/mobile/Containers/Data/Application/OpenSpot/spotify-web.sflx',
  'raw package path',
);
assertEqual(
  decodeExtensionCoreResponse<string>('DownloadRepoExtensionJSON', '"/var/mobile/spotify-web.sflx"'),
  '/var/mobile/spotify-web.sflx',
  'JSON-quoted package path',
);
const repository = decodeExtensionCoreResponse<{ id: string }[]>(
  'GetRepoExtensionsJSON',
  '[{"id":"spotify-web"}]',
);
assertEqual(repository[0]?.id, 'spotify-web', 'JSON repository payload');

console.log('Extension bridge response regression: PASS');
