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

export class ExtensionCoreUnavailableError extends Error {
  constructor(message = 'Extension Core Tauri bridge is unavailable') {
    super(message);
    this.name = 'ExtensionCoreUnavailableError';
  }
}

async function callCore<T>(operation: string, payload: Record<string, unknown> = {}): Promise<T> {
  if (!isTauriRuntime()) throw new ExtensionCoreUnavailableError();
  const response = await invoke<string>('extension_core_call', { operation, payload });
  if (typeof response === 'string') return JSON.parse(response) as T;
  return response as T;
}

export const isExtensionCoreAvailable = (): boolean => isTauriRuntime();

export const extensionCoreBridge = {
  isAvailable: isExtensionCoreAvailable,
  initialize: (extensionsDir: string, dataDir: string) => callCore<void>('init', { extensionsDir, dataDir }),
  loadFromDirectory: (directory: string) => callCore<{ loaded: number; errors: string[] }>('load_from_directory', { directory }),
  getInstalled: () => callCore<InstalledExtension[]>('get_installed'),
  setEnabled: (extensionId: string, enabled: boolean) => callCore<void>('set_enabled', { extensionId, enabled }),
  getSettings: (extensionId: string) => callCore<Record<string, unknown>>('get_settings', { extensionId }),
  setSettings: (extensionId: string, settings: Record<string, unknown>) => callCore<void>('set_settings', { extensionId, settings }),
  getProviderPriority: () => callCore<string[]>('get_provider_priority'),
  setProviderPriority: (priority: string[]) => callCore<void>('set_provider_priority', { priority }),
  getMetadataProviderPriority: () => callCore<string[]>('get_metadata_provider_priority'),
  setMetadataProviderPriority: (priority: string[]) => callCore<void>('set_metadata_provider_priority', { priority }),
  searchMetadata: (query: string, limit = 20, includeExtensions = true) =>
    callCore<Array<Record<string, unknown>>>('search_metadata', { query, limit, includeExtensions }),
  searchMetadataProvider: (providerId: string, query: string, limit = 20) =>
    callCore<Array<Record<string, unknown>>>('search_metadata_provider', { providerId, query, limit }),
  getProviderMetadata: (providerId: string, resourceType: string, resourceId: string) =>
    callCore<Record<string, unknown>>('get_provider_metadata', { providerId, resourceType, resourceId }),
  getFallbackProviderIds: () => callCore<string[]>('get_fallback_provider_ids'),
  setFallbackProviderIds: (providerIds: string[]) => callCore<void>('set_fallback_provider_ids', { providerIds }),
  initRepository: (cacheDir: string) => callCore<void>('init_repository', { cacheDir }),
  setRepositoryURL: (url: string) => callCore<void>('set_repository_url', { url }),
  getRepositoryURL: () => callCore<string>('get_repository_url'),
  listRepository: (forceRefresh = false) => callCore<RepositoryExtension[]>('list_repository', { forceRefresh }),
  searchRepository: (query: string, category = '') => callCore<RepositoryExtension[]>('search_repository', { query, category }),
  getRepositoryCategories: () => callCore<string[]>('repository_categories'),
  downloadRepositoryExtension: (extensionId: string, destination: string) =>
    callCore<string>('download_repository_extension', { extensionId, destination }),
  clearRepositoryCache: () => callCore<void>('clear_repository_cache'),
  checkHealth: (extensionId: string) => callCore<ExtensionHealthResult>('check_health', { extensionId }),
  getPendingAuth: (extensionId: string) => callCore<ExtensionAuthChallenge | null>('pending_auth', { extensionId }),
  setAuthCode: (extensionId: string, code: string) => callCore<void>('set_auth_code', { extensionId, code }),
  setSessionGrant: (extensionId: string, grant: string) => callCore<void>('set_session_grant', { extensionId, grant }),
  clearPendingAuth: (extensionId: string) => callCore<void>('clear_pending_auth', { extensionId }),
  getPendingFFmpeg: (commandId: string) => callCore<ExtensionFFmpegCommand | null>('pending_ffmpeg', { commandId }),
  waitForFFmpeg: (timeoutMillis: number) => callCore<ExtensionFFmpegCommand[]>('wait_ffmpeg', { timeoutMillis }),
  setFFmpegResult: (commandId: string, success: boolean, output: string, error = '') =>
    callCore<void>('set_ffmpeg_result', { commandId, success, output, error }),
  handleURL: (url: string) => callCore<Record<string, unknown>>('handle_url', { url }),
  findURLHandler: (url: string) => callCore<string>('find_url_handler', { url }),
  runPostProcessing: (input: Record<string, unknown>, metadata: Record<string, unknown>) =>
    callCore<Record<string, unknown>>('post_process', { input, metadata }),
  downloadWithFallback: (request: ExtensionDownloadRequest) => callCore<ExtensionFallbackResult>('download_with_fallback', { request }),
  invokeAction: (extensionId: string, actionName: string) => callCore<Record<string, unknown>>('invoke_action', { extensionId, actionName }),
};
