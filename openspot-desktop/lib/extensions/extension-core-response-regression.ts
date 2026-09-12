import { decodeExtensionCoreResponse } from './extension-core-response';
import { extensionCapabilityNames, getExtensionCompatibilityError, hasExtensionCapability, compareExtensionVersions } from './extension-types';

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
  decodeExtensionCoreResponse<string>('DownloadRepoExtensionJSON', 'C:\\Users\\OpenSpot\\spotify-web.sflx'),
  'C:\\Users\\OpenSpot\\spotify-web.sflx',
  'raw package path',
);
assertEqual(
  decodeExtensionCoreResponse<string>('DownloadRepoExtensionJSON', JSON.stringify('C:\\Users\\OpenSpot\\spotify-web.sflx')),
  'C:\\Users\\OpenSpot\\spotify-web.sflx',
  'JSON-quoted package path',
);
const repository = decodeExtensionCoreResponse<{ id: string }[]>(
  'GetRepoExtensionsJSON',
  '[{"id":"spotify-web"}]',
);
assertEqual(repository[0]?.id, 'spotify-web', 'JSON repository payload');

const mapCapabilities = { metadata_provider: true, download_provider: true };
assertEqual(extensionCapabilityNames(mapCapabilities).join(','), 'metadata_provider,download_provider', 'map capabilities');
assertEqual(
  hasExtensionCapability({ id: 'soundcloud', capabilities: mapCapabilities }, 'metadata_provider'),
  true,
  'map capability lookup',
);
assertEqual(compareExtensionVersions('4.9.1', '4.9.1'), 0, 'equal app versions');
assertEqual(compareExtensionVersions('4.9.0', '4.9.1'), -1, 'older app version');
assertEqual(
  getExtensionCompatibilityError({ id: 'soundcloud', min_app_version: '4.9.1' }, '4.9.0'),
  'requires app 4.9.1 or later (installed: 4.9.0)',
  'extension app-version gate',
);
assertEqual(
  getExtensionCompatibilityError({ id: 'soundcloud', min_app_version: '4.9.1' }, '4.9.1'),
  null,
  'compatible extension app version',
);

console.log('Extension bridge response regression: PASS');
