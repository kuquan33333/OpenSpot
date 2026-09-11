export type ExtensionHealthStatus = 'online' | 'degraded' | 'offline' | 'unknown' | 'unsupported';

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
  capabilities?: string[];
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
