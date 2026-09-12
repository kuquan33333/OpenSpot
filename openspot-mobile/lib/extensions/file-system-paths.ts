/**
 * Expo FileSystem exposes file:// URIs while the Go Extension Core accepts
 * native filesystem paths. Keep the conversion at the host boundary so the
 * runtime never has to know about JavaScript URI formats.
 */
export function fileUriToPath(value: string): string {
  if (!value.startsWith('file://')) return value;
  try {
    return decodeURIComponent(new URL(value).pathname);
  } catch {
    return value.replace(/^file:\/\//, '');
  }
}

export function pathToFileUri(value: string): string {
  if (value.startsWith('file://')) return value;
  const encodedPath = value.split('/').map((segment) => encodeURIComponent(segment)).join('/');
  return `file://${encodedPath}`;
}
