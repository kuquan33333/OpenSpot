import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  ActivityIndicator,
  Image,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';
import { Ionicons } from '@expo/vector-icons';
import { useRouter } from 'expo-router';
import { useTranslation } from 'react-i18next';
import * as FileSystem from 'expo-file-system';

import { useColorScheme } from '@/hooks/useColorScheme';
import { extensionCoreBridge } from '@/lib/extensions/extension-core-bridge';
import type { ExtensionHealthResult, InstalledExtension, RepositoryExtension } from '@/lib/extensions/extension-types';

type ExtensionPage = 'store' | 'installed' | 'priority' | 'fallback';

const DEFAULT_REPOSITORY_URL = process.env.EXPO_PUBLIC_EXTENSION_REGISTRY_URL?.trim() ?? '';

function extensionTitle(extension: InstalledExtension | RepositoryExtension): string {
  return ('display_name' in extension && extension.display_name) || extension.name || extension.id;
}

function extensionTypes(extension: InstalledExtension): string {
  return extension.types?.join(' · ') || 'Extension';
}

function healthColor(status: ExtensionHealthResult['status'] | undefined, accent: string): string {
  switch (status) {
    case 'online': return accent;
    case 'degraded': return '#d79a2b';
    case 'offline': return '#d64a4a';
    default: return '#8d8d8d';
  }
}

export default function ExtensionsScreen() {
  const router = useRouter();
  const { t } = useTranslation();
  const isDark = useColorScheme() !== 'light';
  const [page, setPage] = useState<ExtensionPage>('installed');
  const [installed, setInstalled] = useState<InstalledExtension[]>([]);
  const [store, setStore] = useState<RepositoryExtension[]>([]);
  const [health, setHealth] = useState<Record<string, ExtensionHealthResult>>({});
  const [priority, setPriority] = useState<string[]>([]);
  const [fallback, setFallback] = useState<string[]>([]);
  const [repositoryURL, setRepositoryURL] = useState(DEFAULT_REPOSITORY_URL);
  const [query, setQuery] = useState('');
  const [category, setCategory] = useState('');
  const [categories, setCategories] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [busyID, setBusyID] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const theme = useMemo(() => ({
    background: isDark ? '#050505' : '#f5efe6',
    surface: isDark ? '#121212' : '#fffaf2',
    elevated: isDark ? '#1b1b1b' : '#efe4d6',
    text: isDark ? '#fff' : '#2d2219',
    secondary: isDark ? '#a9a9a9' : '#7a6251',
    border: isDark ? '#2b2b2b' : '#e4d5c5',
    accent: isDark ? '#1DB954' : '#167c3a',
  }), [isDark]);

  const text = useCallback((key: string, fallbackText: string) => t(key, { defaultValue: fallbackText }), [t]);

  const initializeCore = useCallback(async () => {
    if (!extensionCoreBridge.isAvailable()) return;
    const documentDirectory = FileSystem.documentDirectory;
    if (!documentDirectory) throw new Error('Extension storage directory is unavailable');
    const cacheDirectory = FileSystem.cacheDirectory ?? documentDirectory;
    await extensionCoreBridge.initialize(
      `${documentDirectory}extensions`,
      `${documentDirectory}extension-data`,
    );
    await extensionCoreBridge.initRepository(`${cacheDirectory}extension-repository`);
  }, []);

  const loadInstalled = useCallback(async () => {
    if (!extensionCoreBridge.isAvailable()) {
      setError(text('extensions.native_unavailable', 'Extension Core is not available in this build yet.'));
      return;
    }
    const [items, configuredPriority, configuredFallback] = await Promise.all([
      extensionCoreBridge.getInstalled(),
      extensionCoreBridge.getProviderPriority(),
      extensionCoreBridge.getFallbackProviderIds(),
    ]);
    setInstalled(items);
    setPriority(configuredPriority);
    setFallback(configuredFallback);
  }, [text]);

  const loadRepository = useCallback(async (forceRefresh = false) => {
    if (!extensionCoreBridge.isAvailable()) return;
    const items = await extensionCoreBridge.listRepository(forceRefresh);
    setStore(items);
  }, []);

  const loadRepositoryConfig = useCallback(async () => {
    if (!extensionCoreBridge.isAvailable()) return;
    try {
      const currentURL = await extensionCoreBridge.getRepositoryURL();
      if (currentURL) setRepositoryURL(currentURL);
      setCategories(await extensionCoreBridge.getRepositoryCategories());
    } catch {
      // The store remains usable after a first-run empty configuration.
    }
  }, []);

  const refresh = useCallback(async () => {
    setBusy(true);
    setError(null);
    try {
      await initializeCore();
      await loadInstalled();
      await loadRepositoryConfig();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusy(false);
    }
  }, [initializeCore, loadInstalled, loadRepositoryConfig]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const selectPage = async (nextPage: ExtensionPage) => {
    setPage(nextPage);
    if (nextPage === 'store' && store.length === 0 && repositoryURL) {
      setBusy(true);
      setError(null);
      try {
        await loadRepository();
      } catch (cause) {
        setError(cause instanceof Error ? cause.message : String(cause));
      } finally {
        setBusy(false);
      }
    }
  };

  const saveRepositoryURL = async () => {
    setBusy(true);
    setError(null);
    try {
      await extensionCoreBridge.setRepositoryURL(repositoryURL.trim());
      await loadRepository(true);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusy(false);
    }
  };

  const toggleExtension = async (extension: InstalledExtension) => {
    setBusyID(extension.id);
    setError(null);
    try {
      await extensionCoreBridge.setEnabled(extension.id, !extension.enabled);
      await loadInstalled();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusyID(null);
    }
  };

  const checkExtensionHealth = async (extension: InstalledExtension) => {
    setBusyID(extension.id);
    setError(null);
    try {
      const result = await extensionCoreBridge.checkHealth(extension.id);
      setHealth((current) => ({ ...current, [extension.id]: result }));
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusyID(null);
    }
  };

  const visibleStore = store.filter((extension) => {
    const needle = query.trim().toLowerCase();
    const matchesQuery = !needle || [extension.name, extension.display_name, extension.description, ...(extension.tags ?? [])]
      .join(' ').toLowerCase().includes(needle);
    return matchesQuery && (!category || extension.category === category);
  });

  const orderedPriority = [...new Set([...priority, ...installed.map((extension) => extension.id)])];
  const movePriority = async (extensionID: string, direction: -1 | 1) => {
    const index = orderedPriority.indexOf(extensionID);
    const nextIndex = index + direction;
    if (index < 0 || nextIndex < 0 || nextIndex >= orderedPriority.length) return;
    const next = [...orderedPriority];
    [next[index], next[nextIndex]] = [next[nextIndex], next[index]];
    setPriority(next);
    try {
      await extensionCoreBridge.setProviderPriority(next);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    }
  };

  const toggleFallback = async (extensionID: string) => {
    const next = fallback.includes(extensionID)
      ? fallback.filter((id) => id !== extensionID)
      : [...fallback, extensionID];
    setFallback(next);
    try {
      await extensionCoreBridge.setFallbackProviderIds(next);
    } catch (cause) {
      setFallback(fallback);
      setError(cause instanceof Error ? cause.message : String(cause));
    }
  };

  const renderInstalled = () => (
    <View style={styles.listGap}>
      {installed.length === 0 ? (
        <EmptyState theme={theme} text={text('extensions.empty_installed', 'No extensions installed yet.')} />
      ) : installed.map((extension) => {
        const currentHealth = health[extension.id];
        return (
          <View key={extension.id} style={[styles.card, { backgroundColor: theme.surface, borderColor: theme.border }]}>
            <View style={styles.rowStart}>
              <View style={[styles.extensionIcon, { backgroundColor: theme.elevated }]}>
                {extension.icon_path ? <Image source={{ uri: extension.icon_path }} style={styles.iconImage} /> : <Ionicons name="extension-puzzle-outline" size={24} color={theme.accent} />}
              </View>
              <View style={styles.flexOne}>
                <Text style={[styles.cardTitle, { color: theme.text }]} numberOfLines={1}>{extensionTitle(extension)}</Text>
                <Text style={[styles.meta, { color: theme.secondary }]}>{extension.id} · v{extension.version || '?'}</Text>
                <Text style={[styles.meta, { color: theme.secondary }]}>{extensionTypes(extension)}</Text>
              </View>
              <Pressable
                accessibilityRole="switch"
                accessibilityState={{ checked: Boolean(extension.enabled) }}
                style={[styles.switch, { backgroundColor: extension.enabled ? theme.accent : theme.elevated }]}
                onPress={() => void toggleExtension(extension)}
                disabled={busyID === extension.id}
              >
                {busyID === extension.id ? <ActivityIndicator color="#fff" size="small" /> : <View style={[styles.switchThumb, extension.enabled && styles.switchThumbOn]} />}
              </Pressable>
            </View>
            {!!extension.description && <Text style={[styles.body, { color: theme.secondary }]}>{extension.description}</Text>}
            {!!extension.error && <Text style={styles.errorText}>{extension.error}</Text>}
            <View style={styles.actionRow}>
              <Pressable style={[styles.secondaryButton, { borderColor: theme.border }]} onPress={() => void checkExtensionHealth(extension)} disabled={busyID === extension.id}>
                <Ionicons name="pulse-outline" size={16} color={theme.accent} />
                <Text style={[styles.buttonText, { color: theme.text }]}>{currentHealth?.status || text('extensions.check_health', 'Check health')}</Text>
              </Pressable>
              <View style={styles.healthDotWrap}>
                <View style={[styles.healthDot, { backgroundColor: healthColor(currentHealth?.status, theme.accent) }]} />
                <Text style={[styles.meta, { color: theme.secondary }]}>{currentHealth?.status || text('extensions.not_checked', 'Not checked')}</Text>
              </View>
            </View>
          </View>
        );
      })}
    </View>
  );

  const renderStore = () => (
    <View style={styles.listGap}>
      <View style={[styles.card, { backgroundColor: theme.surface, borderColor: theme.border }]}>
        <Text style={[styles.cardTitle, { color: theme.text }]}>{text('extensions.repository', 'Extension repository')}</Text>
        <Text style={[styles.body, { color: theme.secondary }]}>{text('extensions.repository_hint', 'Use a trusted HTTPS registry. Packages are verified by the core before installation.')}</Text>
        <TextInput
          value={repositoryURL}
          onChangeText={setRepositoryURL}
          autoCapitalize="none"
          autoCorrect={false}
          placeholder="https://example.com/registry.json"
          placeholderTextColor={theme.secondary}
          style={[styles.input, { color: theme.text, borderColor: theme.border, backgroundColor: theme.elevated }]}
        />
        <View style={styles.actionRow}>
          <Pressable style={[styles.primaryButton, { backgroundColor: theme.accent }]} onPress={() => void saveRepositoryURL()} disabled={busy}>
            <Text style={styles.primaryButtonText}>{text('common.save', 'Save')}</Text>
          </Pressable>
          <Pressable style={[styles.secondaryButton, { borderColor: theme.border }]} onPress={() => void loadRepository(true)} disabled={busy}>
            <Ionicons name="refresh" size={16} color={theme.accent} />
            <Text style={[styles.buttonText, { color: theme.text }]}>{text('common.retry', 'Refresh')}</Text>
          </Pressable>
        </View>
      </View>
      <View style={styles.filterRow}>
        <TextInput value={query} onChangeText={setQuery} placeholder={text('common.search', 'Search')} placeholderTextColor={theme.secondary} style={[styles.searchInput, { color: theme.text, borderColor: theme.border, backgroundColor: theme.surface }]} />
        <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={styles.categoryRow}>
          <Pressable style={[styles.categoryPill, { borderColor: theme.border }, !category && { backgroundColor: theme.accent, borderColor: theme.accent }]} onPress={() => setCategory('')}><Text style={[styles.meta, { color: category ? theme.secondary : '#fff' }]}>{text('extensions.all', 'All')}</Text></Pressable>
          {categories.map((item) => <Pressable key={item} style={[styles.categoryPill, { borderColor: theme.border }, category === item && { backgroundColor: theme.accent, borderColor: theme.accent }]} onPress={() => setCategory(item)}><Text style={[styles.meta, { color: category === item ? '#fff' : theme.secondary }]}>{item}</Text></Pressable>)}
        </ScrollView>
      </View>
      {visibleStore.length === 0 ? <EmptyState theme={theme} text={repositoryURL ? text('extensions.empty_store', 'No repository extensions match this search.') : text('extensions.configure_store', 'Add a repository URL to browse extensions.')} /> : visibleStore.map((extension) => (
        <View key={extension.id} style={[styles.card, { backgroundColor: theme.surface, borderColor: theme.border }]}>
          <Text style={[styles.cardTitle, { color: theme.text }]}>{extensionTitle(extension)}</Text>
          <Text style={[styles.meta, { color: theme.secondary }]}>{extension.id} · v{extension.version} · {extension.category}</Text>
          <Text style={[styles.body, { color: theme.secondary }]}>{extension.description || text('extensions.no_description', 'No description provided.')}</Text>
          <View style={styles.actionRow}>
            <View style={styles.flexOne}><Text style={[styles.meta, { color: theme.secondary }]}>{extension.has_update ? text('extensions.update_available', 'Update available') : extension.is_installed ? text('extensions.installed', 'Installed') : ''}</Text></View>
            {!!extension.sha256 && <Text style={[styles.checksum, { color: theme.secondary }]}>{extension.sha256.slice(0, 12)}…</Text>}
          </View>
        </View>
      ))}
    </View>
  );

  const renderPriority = () => (
    <View style={styles.listGap}>
      <Text style={[styles.body, { color: theme.secondary }]}>{text('extensions.priority_hint', 'The first eligible provider is tried first. Explicit track providers still keep their requested position.')}</Text>
      {orderedPriority.map((extensionID, index) => (
        <View key={extensionID} style={[styles.priorityRow, { backgroundColor: theme.surface, borderColor: theme.border }]}>
          <Text style={[styles.priorityNumber, { color: theme.accent }]}>{index + 1}</Text>
          <Text style={[styles.cardTitle, styles.flexOne, { color: theme.text }]}>{extensionID}</Text>
          <Pressable onPress={() => void movePriority(extensionID, -1)} disabled={index === 0}><Ionicons name="chevron-up" size={20} color={index === 0 ? theme.border : theme.accent} /></Pressable>
          <Pressable onPress={() => void movePriority(extensionID, 1)} disabled={index === orderedPriority.length - 1}><Ionicons name="chevron-down" size={20} color={index === orderedPriority.length - 1 ? theme.border : theme.accent} /></Pressable>
        </View>
      ))}
      {orderedPriority.length === 0 && <EmptyState theme={theme} text={text('extensions.no_providers', 'Install an extension provider to configure priority.')} />}
    </View>
  );

  const renderFallback = () => (
    <View style={styles.listGap}>
      <Text style={[styles.body, { color: theme.secondary }]}>{text('extensions.fallback_hint', 'Allow these providers to receive the next attempt after a provider failure. The core still honors verification and cancellation stops.')}</Text>
      {installed.filter((extension) => extension.enabled).map((extension) => {
        const selected = fallback.includes(extension.id);
        return <Pressable key={extension.id} style={[styles.fallbackRow, { backgroundColor: theme.surface, borderColor: selected ? theme.accent : theme.border }]} onPress={() => void toggleFallback(extension.id)}><Ionicons name={selected ? 'checkmark-circle' : 'ellipse-outline'} size={22} color={selected ? theme.accent : theme.secondary} /><View style={styles.flexOne}><Text style={[styles.cardTitle, { color: theme.text }]}>{extensionTitle(extension)}</Text><Text style={[styles.meta, { color: theme.secondary }]}>{extension.id}</Text></View></Pressable>;
      })}
      {installed.length === 0 && <EmptyState theme={theme} text={text('extensions.no_providers', 'Install an extension provider to configure fallback.')} />}
    </View>
  );

  const pageTitle = page === 'store' ? text('extensions.store', 'Extension Store') : page === 'priority' ? text('extensions.priority', 'Provider Priority') : page === 'fallback' ? text('extensions.fallback', 'Provider Fallback') : text('extensions.installed', 'Installed Extensions');

  return (
    <View style={[styles.container, { backgroundColor: theme.background }]}>
      <View style={styles.header}>
        <Pressable onPress={() => router.back()} style={styles.backButton}><Ionicons name="chevron-back" size={24} color={theme.text} /></Pressable>
        <View style={styles.flexOne}><Text style={[styles.title, { color: theme.text }]}>{text('extensions.title', 'Extensions')}</Text><Text style={[styles.meta, { color: theme.secondary }]}>{pageTitle}</Text></View>
        <Pressable onPress={() => void refresh()} disabled={busy}><Ionicons name="refresh" size={22} color={busy ? theme.border : theme.accent} /></Pressable>
      </View>
      <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={styles.pageTabs}>
        {([['installed', text('extensions.installed', 'Installed')], ['store', text('extensions.store', 'Store')], ['priority', text('extensions.priority', 'Priority')], ['fallback', text('extensions.fallback', 'Fallback')]] as const).map(([value, label]) => <Pressable key={value} onPress={() => void selectPage(value)} style={[styles.pageTab, { borderColor: theme.border }, page === value && { backgroundColor: theme.accent, borderColor: theme.accent }]}><Text style={[styles.pageTabText, { color: page === value ? '#fff' : theme.secondary }]}>{label}</Text></Pressable>)}
      </ScrollView>
      {error && <View style={[styles.errorBanner, { backgroundColor: isDark ? '#32191b' : '#f7dedd', borderColor: '#d64a4a' }]}><Ionicons name="alert-circle-outline" size={18} color="#d64a4a" /><Text style={styles.errorBannerText}>{error}</Text></View>}
      {busy && installed.length === 0 && <ActivityIndicator color={theme.accent} style={styles.loader} />}
      <ScrollView contentContainerStyle={styles.content} showsVerticalScrollIndicator={false}>
        {page === 'installed' && renderInstalled()}
        {page === 'store' && renderStore()}
        {page === 'priority' && renderPriority()}
        {page === 'fallback' && renderFallback()}
      </ScrollView>
    </View>
  );
}

function EmptyState({ theme, text }: { theme: { surface: string; border: string; secondary: string }; text: string }) {
  return <View style={[styles.empty, { backgroundColor: theme.surface, borderColor: theme.border }]}><Ionicons name="extension-puzzle-outline" size={28} color={theme.secondary} /><Text style={[styles.body, { color: theme.secondary, textAlign: 'center' }]}>{text}</Text></View>;
}

const styles = StyleSheet.create({
  container: { flex: 1 },
  header: { flexDirection: 'row', alignItems: 'center', paddingHorizontal: 16, paddingTop: 18, paddingBottom: 8, gap: 10 },
  backButton: { width: 34, height: 34, alignItems: 'center', justifyContent: 'center' },
  title: { fontSize: 27, fontWeight: '800' },
  pageTabs: { paddingHorizontal: 16, paddingVertical: 8, gap: 8 },
  pageTab: { borderWidth: 1, borderRadius: 18, paddingHorizontal: 14, paddingVertical: 9 },
  pageTabText: { fontSize: 13, fontWeight: '700' },
  content: { padding: 16, paddingBottom: 48 },
  listGap: { gap: 12 },
  card: { borderWidth: 1, borderRadius: 16, padding: 14, gap: 9 },
  rowStart: { flexDirection: 'row', alignItems: 'center', gap: 11 },
  flexOne: { flex: 1 },
  extensionIcon: { width: 48, height: 48, borderRadius: 13, alignItems: 'center', justifyContent: 'center', overflow: 'hidden' },
  iconImage: { width: 48, height: 48 },
  cardTitle: { fontSize: 15, fontWeight: '700' },
  meta: { fontSize: 12 },
  body: { fontSize: 14, lineHeight: 20 },
  actionRow: { flexDirection: 'row', alignItems: 'center', gap: 8 },
  secondaryButton: { minHeight: 38, borderWidth: 1, borderRadius: 11, paddingHorizontal: 12, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: 7 },
  buttonText: { fontSize: 13, fontWeight: '700' },
  primaryButton: { minHeight: 38, borderRadius: 11, paddingHorizontal: 15, alignItems: 'center', justifyContent: 'center' },
  primaryButtonText: { color: '#fff', fontSize: 13, fontWeight: '700' },
  switch: { width: 48, height: 28, borderRadius: 16, justifyContent: 'center', paddingHorizontal: 3 },
  switchThumb: { width: 22, height: 22, borderRadius: 11, backgroundColor: '#888' },
  switchThumbOn: { alignSelf: 'flex-end', backgroundColor: '#fff' },
  healthDotWrap: { flexDirection: 'row', alignItems: 'center', gap: 6 },
  healthDot: { width: 8, height: 8, borderRadius: 4 },
  input: { minHeight: 44, borderWidth: 1, borderRadius: 11, paddingHorizontal: 12, fontSize: 13 },
  filterRow: { gap: 8 },
  searchInput: { minHeight: 42, borderWidth: 1, borderRadius: 11, paddingHorizontal: 12, fontSize: 14 },
  categoryRow: { gap: 7 },
  categoryPill: { borderWidth: 1, borderRadius: 16, paddingHorizontal: 11, paddingVertical: 7 },
  checksum: { fontSize: 11, fontFamily: 'SpaceMono' },
  priorityRow: { borderWidth: 1, borderRadius: 13, minHeight: 52, paddingHorizontal: 12, flexDirection: 'row', alignItems: 'center', gap: 10 },
  priorityNumber: { width: 20, fontSize: 16, fontWeight: '800', textAlign: 'center' },
  fallbackRow: { borderWidth: 1, borderRadius: 13, minHeight: 60, paddingHorizontal: 13, flexDirection: 'row', alignItems: 'center', gap: 11 },
  empty: { borderWidth: 1, borderRadius: 16, padding: 26, alignItems: 'center', gap: 12 },
  errorText: { color: '#d64a4a', fontSize: 13 },
  errorBanner: { marginHorizontal: 16, borderWidth: 1, borderRadius: 12, padding: 11, flexDirection: 'row', gap: 8, alignItems: 'flex-start' },
  errorBannerText: { flex: 1, color: '#a92f2f', fontSize: 13, lineHeight: 18 },
  loader: { marginTop: 15 },
});
