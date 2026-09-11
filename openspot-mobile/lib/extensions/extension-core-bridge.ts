import { NativeModules } from 'react-native';

import type {
  ExtensionAuthChallenge,
  ExtensionDownloadRequest,
  ExtensionFallbackResult,
  ExtensionFFmpegCommand,
  ExtensionHealthResult,
  InstalledExtension,
  RepositoryExtension,
} from './extension-types';

type NativeCall = (...args: unknown[]) => unknown;
type NativeModuleShape = Record<string, NativeCall | undefined>;

export class ExtensionCoreUnavailableError extends Error {
  constructor(message = 'Extension Core native module is not installed') {
    super(message);
    this.name = 'ExtensionCoreUnavailableError';
  }
}

function resolveNativeModule(): NativeModuleShape | null {
  const modules = NativeModules as unknown as Record<string, NativeModuleShape | undefined>;
  return modules.OpenSpotExtensionCore ?? modules.Gobackend ?? null;
}

function methodName(name: string): string {
  return `Gobackend${name}`;
}

function resolveMethod(name: string): NativeCall | null {
  const module = resolveNativeModule();
  if (!module) return null;
  return module[name] ?? module[methodName(name)] ?? null;
}

function decodeJSON<T>(value: unknown): T {
  if (typeof value === 'string') return JSON.parse(value) as T;
  return value as T;
}

async function callNative<T>(name: string, ...args: unknown[]): Promise<T> {
  const method = resolveMethod(name);
  if (!method) throw new ExtensionCoreUnavailableError();
  return decodeJSON<T>(await method(...args));
}

export const isExtensionCoreAvailable = (): boolean => Boolean(resolveNativeModule());

export const extensionCoreBridge = {
  isAvailable: isExtensionCoreAvailable,
  initialize: (extensionsDir: string, dataDir: string) => callNative<void>('InitExtensionSystem', extensionsDir, dataDir),
  loadFromDirectory: (directory: string) => callNative<{ loaded: number; errors: string[] }>('LoadExtensionsFromDir', directory),
  getInstalled: () => callNative<InstalledExtension[]>('GetInstalledExtensions'),
  setEnabled: (extensionId: string, enabled: boolean) => callNative<void>('SetExtensionEnabledByID', extensionId, enabled),
  getSettings: (extensionId: string) => callNative<Record<string, unknown>>('GetExtensionSettingsJSON', extensionId),
  setSettings: (extensionId: string, settings: Record<string, unknown>) =>
    callNative<void>('SetExtensionSettingsJSON', extensionId, JSON.stringify(settings)),
  getProviderPriority: () => callNative<string[]>('GetProviderPriorityJSON'),
  setProviderPriority: (priority: string[]) => callNative<void>('SetProviderPriorityJSON', JSON.stringify(priority)),
  getMetadataProviderPriority: () => callNative<string[]>('GetMetadataProviderPriorityJSON'),
  setMetadataProviderPriority: (priority: string[]) => callNative<void>('SetMetadataProviderPriorityJSON', JSON.stringify(priority)),
  searchMetadata: (query: string, limit = 20, includeExtensions = true) =>
    callNative<Array<Record<string, unknown>>>('SearchTracksWithMetadataProvidersJSON', query, limit, includeExtensions),
  searchMetadataProvider: (providerId: string, query: string, limit = 20) =>
    callNative<Array<Record<string, unknown>>>('SearchTracksWithMetadataProviderJSON', providerId, query, limit),
  getProviderMetadata: (providerId: string, resourceType: string, resourceId: string) =>
    callNative<Record<string, unknown>>('GetProviderMetadataJSON', providerId, resourceType, resourceId),
  getFallbackProviderIds: () => callNative<string[]>('GetExtensionFallbackProviderIDsJSON'),
  setFallbackProviderIds: (providerIds: string[]) =>
    callNative<void>('SetExtensionFallbackProviderIDsJSON', JSON.stringify(providerIds)),
  initRepository: (cacheDir: string) => callNative<void>('InitExtensionRepoJSON', cacheDir),
  setRepositoryURL: (url: string) => callNative<void>('SetRepoRegistryURLJSON', url),
  getRepositoryURL: () => callNative<string>('GetRepoRegistryURLJSON'),
  listRepository: (forceRefresh = false) => callNative<RepositoryExtension[]>('GetRepoExtensionsJSON', forceRefresh),
  searchRepository: (query: string, category = '') => callNative<RepositoryExtension[]>('SearchRepoExtensionsJSON', query, category),
  getRepositoryCategories: () => callNative<string[]>('GetRepoCategoriesJSON'),
  downloadRepositoryExtension: (extensionId: string, destination: string) =>
    callNative<string>('DownloadRepoExtensionJSON', extensionId, destination),
  clearRepositoryCache: () => callNative<void>('ClearRepoCacheJSON'),
  checkHealth: (extensionId: string) => callNative<ExtensionHealthResult>('CheckExtensionHealthJSON', extensionId),
  getPendingAuth: (extensionId: string) => callNative<ExtensionAuthChallenge | null>('GetExtensionPendingAuthJSON', extensionId),
  setAuthCode: (extensionId: string, code: string) => callNative<void>('SetExtensionAuthCodeByID', extensionId, code),
  setSessionGrant: (extensionId: string, grant: string) => callNative<void>('SetExtensionSessionGrantByID', extensionId, grant),
  clearPendingAuth: (extensionId: string) => callNative<void>('ClearExtensionPendingAuthByID', extensionId),
  getPendingFFmpeg: (commandId: string) => callNative<ExtensionFFmpegCommand | null>('GetPendingFFmpegCommandJSON', commandId),
  waitForFFmpeg: (timeoutMillis: number) => callNative<ExtensionFFmpegCommand[]>('WaitForPendingFFmpegCommandsJSON', timeoutMillis),
  setFFmpegResult: (commandId: string, success: boolean, output: string, error = '') =>
    callNative<void>('SetFFmpegCommandResultByID', commandId, success, output, error),
  handleURL: (url: string) => callNative<Record<string, unknown>>('HandleURLWithExtensionJSON', url),
  findURLHandler: (url: string) => callNative<string>('FindURLHandlerJSON', url),
  runPostProcessing: (input: Record<string, unknown>, metadata: Record<string, unknown>) =>
    callNative<Record<string, unknown>>('RunPostProcessingV2JSON', JSON.stringify(input), JSON.stringify(metadata)),
  downloadWithFallback: (request: ExtensionDownloadRequest) =>
    callNative<ExtensionFallbackResult>('DownloadExtensionWithFallbackJSON', JSON.stringify(request)),
  invokeAction: (extensionId: string, actionName: string) =>
    callNative<Record<string, unknown>>('InvokeExtensionActionJSON', extensionId, actionName),
};
