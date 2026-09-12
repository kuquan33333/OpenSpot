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
import { decodeExtensionCoreResponse } from './extension-core-response';

type NativeCall = (...args: unknown[]) => unknown;
type NativeModuleShape = Record<string, NativeCall | undefined> & { call?: NativeCall };

export class ExtensionCoreUnavailableError extends Error {
  constructor(message = 'Extension Core native module is not installed') {
    super(message);
    this.name = 'ExtensionCoreUnavailableError';
  }
}

function resolveNativeModule(): NativeModuleShape | null {
  const modules = NativeModules as unknown as Record<string, NativeModuleShape | undefined>;
  const linkedModule = modules.OpenSpotExtensionCore ?? modules.Gobackend;
  if (linkedModule) return linkedModule;

  // Expo Modules are exposed through requireNativeModule on newer runtimes;
  // keep the NativeModules path above for classic and existing builds.
  try {
    const modulesCore = require('expo-modules-core') as { requireNativeModule?: (name: string) => NativeModuleShape };
    return modulesCore.requireNativeModule?.('OpenSpotExtensionCore') ?? null;
  } catch {
    return null;
  }
}

function methodName(name: string): string {
  return `Gobackend${name}`;
}

function resolveMethod(name: string): NativeCall | null {
  const module = resolveNativeModule();
  if (!module) return null;
  return module[name] ?? module[methodName(name)] ?? null;
}

function stringArray(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string' && item.trim().length > 0) : [];
}

async function callNative<T>(name: string, ...args: unknown[]): Promise<T> {
  const module = resolveNativeModule();
  const genericCall = module?.call;
  if (genericCall) {
    return decodeExtensionCoreResponse<T>(name, await Reflect.apply(genericCall, module, [name, JSON.stringify(args)]));
  }
  const method = resolveMethod(name);
  if (!method) throw new ExtensionCoreUnavailableError();
  return decodeExtensionCoreResponse<T>(name, await method(...args));
}

export const isExtensionCoreAvailable = (): boolean => Boolean(resolveNativeModule());

export const extensionCoreBridge = {
  isAvailable: isExtensionCoreAvailable,
  setAppVersion: (version: string) => callNative<void>('SetAppVersion', version),
  initialize: (extensionsDir: string, dataDir: string) => callNative<void>('InitExtensionSystem', extensionsDir, dataDir),
  loadFromDirectory: async (directory: string) => {
    const result = await callNative<{ loaded?: unknown; errors?: unknown }>('LoadExtensionsFromDir', directory);
    return { loaded: typeof result?.loaded === 'number' ? result.loaded : 0, errors: stringArray(result?.errors) };
  },
  loadFromPath: (filePath: string) => callNative<InstalledExtension>('LoadExtensionFromPath', filePath),
  remove: (extensionId: string) => callNative<void>('RemoveExtensionByID', extensionId),
  upgradeFromPath: (filePath: string) => callNative<InstalledExtension>('UpgradeExtensionFromPath', filePath),
  checkUpgradeFromPath: (filePath: string) => callNative<{ extension_id: string; new_version: string; current_version?: string; is_installed: boolean; can_upgrade: boolean }>('CheckExtensionUpgradeFromPath', filePath),
  getInstalled: async () => {
    const result = await callNative<unknown>('GetInstalledExtensions');
    return (Array.isArray(result) ? result : []).filter((item): item is InstalledExtension => Boolean(item && typeof item === 'object'));
  },
  setEnabled: (extensionId: string, enabled: boolean) => callNative<void>('SetExtensionEnabledByID', extensionId, enabled),
  getSettings: (extensionId: string) => callNative<Record<string, unknown>>('GetExtensionSettingsJSON', extensionId),
  setSettings: (extensionId: string, settings: Record<string, unknown>) =>
    callNative<void>('SetExtensionSettingsJSON', extensionId, JSON.stringify(settings)),
  getProviderPriority: async () => stringArray(await callNative<unknown>('GetProviderPriorityJSON')),
  setProviderPriority: (priority: string[]) => callNative<void>('SetProviderPriorityJSON', JSON.stringify(priority)),
  getMetadataProviderPriority: async () => stringArray(await callNative<unknown>('GetMetadataProviderPriorityJSON')),
  setMetadataProviderPriority: (priority: string[]) => callNative<void>('SetMetadataProviderPriorityJSON', JSON.stringify(priority)),
  searchMetadata: (query: string, limit = 20, includeExtensions = true) =>
    callNative<Array<Record<string, unknown>>>('SearchTracksWithMetadataProvidersJSON', query, limit, includeExtensions),
  searchMetadataProvider: (providerId: string, query: string, limit = 20) =>
    callNative<Array<Record<string, unknown>>>('SearchTracksWithMetadataProviderJSON', providerId, query, limit),
  getProviderMetadata: (providerId: string, resourceType: string, resourceId: string) =>
    callNative<Record<string, unknown>>('GetProviderMetadataJSON', providerId, resourceType, resourceId),
  getFallbackProviderIds: () => callNative<string[] | null>('GetExtensionFallbackProviderIDsJSON'),
  setFallbackProviderIds: (providerIds: string[]) =>
    callNative<void>('SetExtensionFallbackProviderIDsJSON', JSON.stringify(providerIds)),
  initRepository: (cacheDir: string) => callNative<void>('InitExtensionRepoJSON', cacheDir),
  setRepositoryURL: (url: string) => callNative<void>('SetRepoRegistryURLJSON', url),
  getRepositoryURL: () => callNative<string>('GetRepoRegistryURLJSON'),
  listRepository: async (forceRefresh = false) => {
    const result = await callNative<unknown>('GetRepoExtensionsJSON', forceRefresh);
    return (Array.isArray(result) ? result : []) as RepositoryExtension[];
  },
  searchRepository: async (query: string, category = '') => {
    const result = await callNative<unknown>('SearchRepoExtensionsJSON', query, category);
    return (Array.isArray(result) ? result : []) as RepositoryExtension[];
  },
  getRepositoryCategories: async () => stringArray(await callNative<unknown>('GetRepoCategoriesJSON')),
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
