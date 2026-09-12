import AsyncStorage from '@react-native-async-storage/async-storage';

export type ExtensionCapability = 'search' | 'home' | 'stream' | 'download';
export type ExtensionCapabilityOverrides = Record<string, Partial<Record<ExtensionCapability, boolean>>>;

const STORAGE_KEY = 'openspot_extension_capability_overrides_v1';

export async function getExtensionCapabilityOverrides(): Promise<ExtensionCapabilityOverrides> {
  const raw = await AsyncStorage.getItem(STORAGE_KEY);
  if (!raw) return {};
  try {
    const value: unknown = JSON.parse(raw);
    if (!value || typeof value !== 'object' || Array.isArray(value)) return {};
    return value as ExtensionCapabilityOverrides;
  } catch {
    return {};
  }
}

export async function setExtensionCapabilityEnabled(
  extensionId: string,
  capability: ExtensionCapability,
  enabled: boolean,
): Promise<ExtensionCapabilityOverrides> {
  const overrides = await getExtensionCapabilityOverrides();
  overrides[extensionId] = { ...(overrides[extensionId] ?? {}), [capability]: enabled };
  await AsyncStorage.setItem(STORAGE_KEY, JSON.stringify(overrides));
  return overrides;
}
