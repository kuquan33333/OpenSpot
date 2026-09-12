export type ExtensionHealthStatus = 'online' | 'degraded' | 'offline' | 'unknown' | 'unsupported';
export type ExtensionCapabilities = string[] | Record<string, unknown>;

export interface InstalledExtension {
  id: string;
  name?: string;
  display_name?: string;
  version?: string;
  description?: string;
  homepage?: string;
  icon_path?: string;
  types?: string[];
  icon?: string;
  enabled?: boolean;
  error?: string;
  health?: ExtensionHealthStatus;
  min_app_version?: string;
  capabilities?: ExtensionCapabilities;
  permissions?: string[];
  has_metadata_provider?: boolean;
  has_download_provider?: boolean;
  has_lyrics_provider?: boolean;
}

export function extensionCapabilityNames(value: ExtensionCapabilities | undefined): string[] {
  if (Array.isArray(value)) return value.filter((item): item is string => typeof item === 'string' && item.trim().length > 0);
  if (value && typeof value === 'object') return Object.keys(value);
  return [];
}

export function hasExtensionCapability(extension: InstalledExtension, name: string): boolean {
  if (extension.types?.includes(name)) return true;
  const capabilities = extension.capabilities;
  if (Array.isArray(capabilities)) return capabilities.includes(name);
  return Boolean(capabilities && typeof capabilities[name] === 'boolean' && capabilities[name]);
}

function versionParts(value: string): number[] {
  const match = value.trim().match(/\d+(?:\.\d+)*/);
  return match ? match[0].split('.').map((part) => Number(part) || 0) : [];
}

export function compareExtensionVersions(left: string, right: string): number {
  const leftParts = versionParts(left);
  const rightParts = versionParts(right);
  for (let index = 0; index < Math.max(leftParts.length, rightParts.length); index += 1) {
    const leftPart = leftParts[index] ?? 0;
    const rightPart = rightParts[index] ?? 0;
    if (leftPart > rightPart) return 1;
    if (leftPart < rightPart) return -1;
  }
  return 0;
}

export function getExtensionCompatibilityError(extension: InstalledExtension, appVersion?: string): string | null {
  const minVersion = extension.min_app_version?.trim();
  const installedVersion = appVersion?.trim();
  if (minVersion && installedVersion && compareExtensionVersions(installedVersion, minVersion) < 0) {
    return `requires app ${minVersion} or later (installed: ${installedVersion})`;
  }
  const nativeError = extension.error?.trim();
  return nativeError || null;
}

export interface RepositoryExtension {
  id: string;
  name: string;
  display_name: string;
  version: string;
  description: string;
  download_url: string;
  icon_url?: string;
  category: string;
  tags?: string[];
  downloads?: number;
  updated_at?: string;
  min_app_version?: string;
  sha256?: string;
  is_installed?: boolean;
  installed_version?: string;
  has_update?: boolean;
}

export interface ExtensionHealthResult {
  extension_id: string;
  status: ExtensionHealthStatus;
  checked_at: string;
  checks: Array<{
    id: string;
    label?: string;
    url: string;
    method: string;
    service_key?: string;
    required: boolean;
    status: ExtensionHealthStatus;
    http_status?: number;
    latency_ms: number;
    message?: string;
    error?: string;
    checked_at: string;
  }>;
}

export interface ExtensionAuthChallenge {
  extension_id: string;
  auth_url: string;
  callback_url: string;
}

export interface ExtensionFFmpegCommand {
  command_id: string;
  extension_id: string;
  arguments: string[];
  input_path?: string;
  output_path?: string;
}

export interface ExtensionDownloadRequest {
  use_extensions?: boolean;
  use_fallback?: boolean;
  service?: string;
  source?: string;
  download_provider?: string;
  provider_track_id?: string;
  track_id?: string;
  item_id?: string;
  track_name?: string;
  artist_name?: string;
  album_name?: string;
  output_dir?: string;
  output_ext?: string;
  quality?: string;
  [key: string]: unknown;
}

export interface ExtensionFallbackResult {
  success: boolean;
  provider_id?: string;
  fallback_used?: boolean;
  stopped?: boolean;
  error?: string;
  attempts?: Array<{
    provider_id: string;
    available?: boolean;
    success?: boolean;
    error?: string;
    error_type?: string;
    retryable?: boolean;
    stopped?: boolean;
  }>;
  result?: unknown;
}
