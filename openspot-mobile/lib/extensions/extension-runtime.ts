import * as FileSystem from 'expo-file-system';

import { extensionCoreBridge } from './extension-core-bridge';
import { fileUriToPath } from './file-system-paths';

let initializationPromise: Promise<void> | null = null;

/**
 * Initializes the native Extension Core once per JS runtime. Provider reads
 * must wait for this operation; otherwise the Home screen can query an empty
 * ProviderRegistry while the Extensions screen is still doing the first load.
 */
export function initializeExtensionRuntime(appVersion?: string): Promise<void> {
  if (!extensionCoreBridge.isAvailable()) return Promise.resolve();
  if (initializationPromise) return initializationPromise;

  initializationPromise = (async () => {
    const documentDirectory = FileSystem.documentDirectory;
    if (!documentDirectory) throw new Error('Extension storage directory is unavailable');

    const cacheDirectory = FileSystem.cacheDirectory ?? documentDirectory;
    const extensionsDirectory = `${documentDirectory}extensions/`;

    if (appVersion?.trim()) await extensionCoreBridge.setAppVersion(appVersion);
    await extensionCoreBridge.initialize(
      fileUriToPath(extensionsDirectory),
      fileUriToPath(`${documentDirectory}extension-data`),
    );

    const loadResult = await extensionCoreBridge.loadFromDirectory(fileUriToPath(extensionsDirectory));
    if (loadResult.errors.length > 0) {
      console.warn('[Extensions] some persisted packages could not be restored:', loadResult.errors);
    }

    await extensionCoreBridge.initRepository(fileUriToPath(`${cacheDirectory}extension-repository`));
  })().catch((error) => {
    // Allow a later screen-focus retry after a transient native/filesystem
    // failure instead of permanently caching a rejected promise.
    initializationPromise = null;
    throw error;
  });

  return initializationPromise;
}
