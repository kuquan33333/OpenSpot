const RAW_STRING_OPERATIONS = new Set([
  'GetRepoRegistryURLJSON',
  'DownloadRepoExtensionJSON',
]);

/**
 * Native hosts historically returned repository URLs/package paths both as
 * JSON-quoted strings and as raw strings. Only JSON payload operations should
 * be unconditionally parsed.
 */
export function decodeExtensionCoreResponse<T>(operation: string, value: unknown): T {
  if (typeof value !== 'string') return value as T;

  const payload = value.trim();
  if (RAW_STRING_OPERATIONS.has(operation)) {
    if (!payload) return value as T;
    try {
      const decoded = JSON.parse(payload) as unknown;
      return (typeof decoded === 'string' ? decoded : value) as T;
    } catch {
      return value as T;
    }
  }

  return JSON.parse(payload) as T;
}
