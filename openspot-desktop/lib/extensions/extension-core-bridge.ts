import { invoke } from '@tauri-apps/api/core';

import { isTauriRuntime } from '@/lib/tauri-offline';
import type {
  ExtensionAuthChallenge,
  ExtensionDownloadRequest,
  ExtensionFallbackResult,
  ExtensionFFmpegCommand,
  ExtensionHealthResult,
  InstalledExtension,
  RepositoryExtension,
} from './extension-types';
import { decodeExtensionCoreResponse } from './extension-core-response';

export class ExtensionCoreUnavailableError extends Error {
  constructor(message = 'Extension Core Tauri bridge is unavailable') {
    super(message);
    this.name = 'ExtensionCoreUnavailableError';
  }
}

async function callCore<T>(operation: string, payload: unknown[] = []): Promise<T> {
  if (!isTauriRuntime()) throw new ExtensionCoreUnavailableError();
  const response = await invoke<unknown>('extension_core_call', { operation, payload });
  return decodeExtensionCoreResponse<T>(operation, response);
}

export const isExtensionCoreAvailable = (): boolean => isTauriRuntime();

export const extensionCoreBridge = {
  isAvailable: isExtensionCoreAvailable,
  initialize: () => callCore<void>('init'),
  loadFromDirectory: (directory: string) => callCore<{ loaded: number; errors: string[] }>('LoadExtensionsFromDir', [directory]),
  loadFromPath: (filePath: string) => callCore<InstalledExtension>('LoadExtensionFromPath', [filePath]),
  remove: (extensionId: string) => callCore<void>('RemoveExtensionByID', [extensionId]),
  upgradeFromPath: (filePath: string) => callCore<InstalledExtension>('UpgradeExtensionFromPath', [filePath]),
  checkUpgradeFromPath: (filePath: string) => callCore<{ extension_id: string; new_version: string; current_version?: string; is_installed: boolean; can_upgrade: boolean }>('CheckExtensionUpgradeFromPath', [filePath]),
  getInstalled: () => callCore<InstalledExtension[]>('GetInstalledExtensions'),
  setEnabled: (extensionId: string, enabled: boolean) => callCore<void>('SetExtensionEnabledByID', [extensionId, enabled]),
  getSettings: (extensionId: string) => callCore<Record<string, unknown>>('GetExtensionSettingsJSON', [extensionId]),
  setSettings: (extensionId: string, settings: Record<string, unknown>) => callCore<void>('SetExtensionSettingsJSON', [extensionId, JSON.stringify(settings)]),
  getProviderPriority: () => callCore<string[]>('GetProviderPriorityJSON'),
  setProviderPriority: (priority: string[]) => callCore<void>('SetProviderPriorityJSON', [JSON.stringify(priority)]),
  getMetadataProviderPriority: () => callCore<string[]>('GetMetadataProviderPriorityJSON'),
  setMetadataProviderPriority: (priority: string[]) => callCore<void>('SetMetadataProviderPriorityJSON', [JSON.stringify(priority)]),
  searchMetadata: (query: string, limit = 20, includeExtensions = true) =>
    callCore<Array<Record<string, unknown>>>('SearchTracksWithMetadataProvidersJSON', [query, limit, includeExtensions]),
  searchMetadataProvider: (providerId: string, query: string, limit = 20) =>
    callCore<Array<Record<string, unknown>>>('SearchTracksWithMetadataProviderJSON', [providerId, query, limit]),
  getProviderMetadata: (providerId: string, resourceType: string, resourceId: string) =>
    callCore<Record<string, unknown>>('GetProviderMetadataJSON', [providerId, resourceType, resourceId]),
  getFallbackProviderIds: () => callCore<string[] | null>('GetExtensionFallbackProviderIDsJSON'),
  setFallbackProviderIds: (providerIds: string[]) => callCore<void>('SetExtensionFallbackProviderIDsJSON', [JSON.stringify(providerIds)]),
  initRepository: () => callCore<void>('InitExtensionRepoJSON', []),
  setRepositoryURL: (url: string) => callCore<void>('SetRepoRegistryURLJSON', [url]),
  getRepositoryURL: () => callCore<string>('GetRepoRegistryURLJSON'),
  listRepository: (forceRefresh = false) => callCore<RepositoryExtension[]>('GetRepoExtensionsJSON', [forceRefresh]),
  searchRepository: (query: string, category = '') => callCore<RepositoryExtension[]>('SearchRepoExtensionsJSON', [query, category]),
  getRepositoryCategories: () => callCore<string[]>('GetRepoCategoriesJSON'),
  downloadRepositoryExtension: (extensionId: string, destination: string) =>
    callCore<string>('DownloadRepoExtensionJSON', [extensionId, destination]),
  clearRepositoryCache: () => callCore<void>('ClearRepoCacheJSON'),
  checkHealth: (extensionId: string) => callCore<ExtensionHealthResult>('CheckExtensionHealthJSON', [extensionId]),
  getPendingAuth: (extensionId: string) => callCore<ExtensionAuthChallenge | null>('GetExtensionPendingAuthJSON', [extensionId]),
  setAuthCode: (extensionId: string, code: string) => callCore<void>('SetExtensionAuthCodeByID', [extensionId, code]),
  setSessionGrant: (extensionId: string, grant: string) => callCore<void>('SetExtensionSessionGrantByID', [extensionId, grant]),
  clearPendingAuth: (extensionId: string) => callCore<void>('ClearExtensionPendingAuthByID', [extensionId]),
  getPendingFFmpeg: (commandId: string) => callCore<ExtensionFFmpegCommand | null>('GetPendingFFmpegCommandJSON', [commandId]),
  waitForFFmpeg: (timeoutMillis: number) => callCore<ExtensionFFmpegCommand[]>('WaitForPendingFFmpegCommandsJSON', [timeoutMillis]),
  setFFmpegResult: (commandId: string, success: boolean, output: string, error = '') =>
    callCore<void>('SetFFmpegCommandResultByID', [commandId, success, output, error]),
  handleURL: (url: string) => callCore<Record<string, unknown>>('HandleURLWithExtensionJSON', [url]),
  findURLHandler: (url: string) => callCore<string>('FindURLHandlerJSON', [url]),
  runPostProcessing: (input: Record<string, unknown>, metadata: Record<string, unknown>) =>
    callCore<Record<string, unknown>>('RunPostProcessingV2JSON', [JSON.stringify(input), JSON.stringify(metadata)]),
  downloadWithFallback: (request: ExtensionDownloadRequest) => callCore<ExtensionFallbackResult>('DownloadExtensionWithFallbackJSON', [JSON.stringify(request)]),
  invokeAction: (extensionId: string, actionName: string) => callCore<Record<string, unknown>>('InvokeExtensionActionJSON', [extensionId, actionName]),
  removeDownloadedPackage: (filePath: string) => invoke<void>('delete_extension_package', { path: filePath }),
};
