import React, { useContext, useEffect, useMemo, useState } from 'react';
import { View, StyleSheet, StatusBar, Text, TouchableOpacity, ScrollView, Modal, ActivityIndicator, FlatList } from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { useSearch } from '@/hooks/useSearch';
import { TopBar } from '@/components/TopBar';
import { MusicPlayerContext } from './_layout';
import { MusicAPI } from '@/lib/music-api';
import { Track } from '@/types/music';
import { Ionicons } from '@expo/vector-icons';
import { useLikedSongs } from '@/hooks/useLikedSongs';
import { HorizontalTrackList } from '@/components/HorizontalTrackList';
import { GreetingHeader } from '@/components/GreetingHeader';
import { QuickActions } from '@/components/QuickActions';
import { SectionHeader } from '@/components/SectionHeader';
import { useRouter, useFocusEffect } from 'expo-router';
import AsyncStorage from '@react-native-async-storage/async-storage';
import { useColorScheme } from '@/hooks/useColorScheme';
import { useTranslation } from 'react-i18next';
import { useThemeMode, ThemeMode } from '@/hooks/theme-mode';
const REGION_OVERRIDE_KEY = 'openspot_region_override_v1';
const LANGUAGE_KEY = 'openspot_language_v1';
const FIRST_RUN_SETUP_KEY = 'openspot_first_run_setup_done_v1';

export default function HomeScreen() {
  const router = useRouter();
  const colorScheme = useColorScheme();
  const { t, i18n } = useTranslation();
  const { mode, setMode } = useThemeMode();
  const isDark = colorScheme !== 'light';
  const theme = useMemo(
    () => ({
      background: isDark ? '#050505' : '#f5efe6',
      surface: isDark ? '#121212' : '#fffaf2',
      surfaceElevated: isDark ? '#1b1b1b' : '#efe4d6',
      textPrimary: isDark ? '#ffffff' : '#2d2219',
      textSecondary: isDark ? '#a9a9a9' : '#7a6251',
      border: isDark ? '#272727' : '#e4d5c5',
      accent: isDark ? '#1DB954' : '#167c3a',
    }),
    [isDark]
  );

  const [currentView, setCurrentView] = React.useState<'home' | 'search'>('home');
  const searchState = useSearch();
  const { clearResults } = searchState;
  const { handleTrackSelect, isPlaying, currentTrack } = useContext(MusicPlayerContext);
  const [homeSections, setHomeSections] = useState<Awaited<ReturnType<typeof MusicAPI.getHomeSections>>>([]);
  const [homeLoading, setHomeLoading] = useState(true);
  const { getLikedSongsAsTrack } = useLikedSongs();
  const likedTracks = getLikedSongsAsTrack();
  const [recentlyPlayedTracks, setRecentlyPlayedTracks] = useState<Track[]>([]);
  const [showFirstRunSetup, setShowFirstRunSetup] = useState(false);
  const [setupRegion, setSetupRegion] = useState<string>('auto');
  const [setupLanguage, setSetupLanguage] = useState<string>('en');
  const [setupTheme, setSetupTheme] = useState<ThemeMode>(mode);
  const [isSavingSetup, setIsSavingSetup] = useState(false);
  const [isLanguageModalOpen, setIsLanguageModalOpen] = useState(false);
  const [isRegionModalOpen, setIsRegionModalOpen] = useState(false);

  const languageOptions: { label: string; value: string; nativeLabel: string }[] = [
    { label: 'English', value: 'en', nativeLabel: 'English' },
    { label: 'Vietnamese', value: 'vi', nativeLabel: 'Tiếng Việt' },
    { label: 'Hindi', value: 'hi', nativeLabel: 'Hindi' },
    { label: 'Spanish', value: 'es', nativeLabel: 'Espanol' },
    { label: 'Chinese', value: 'zh', nativeLabel: 'Zhongwen' },
    { label: 'German', value: 'de', nativeLabel: 'Deutsch' },
    { label: 'French', value: 'fr', nativeLabel: 'Francais' },
    { label: 'Russian', value: 'ru', nativeLabel: 'Russkiy' },
    { label: 'Hebrew', value: 'he', nativeLabel: 'Ivrit' },
    { label: 'Turkish', value: 'tr', nativeLabel: 'Türkçe' },
    { label: 'Korean', value: 'ko', nativeLabel: '한국어' },
  ];

  
  useEffect(() => {
    void (async () => {
      try {
        const [done] = await Promise.all([
          AsyncStorage.getItem(FIRST_RUN_SETUP_KEY),
        ]);
        if (!done) setShowFirstRunSetup(true);
      } catch (error) {
        console.error('Failed to restore local setup state:', error);
        setShowFirstRunSetup(true);
      }
    })();
  }, []);

  useEffect(() => {
    setSetupTheme(mode);
  }, [mode]);

  const loadRecentlyPlayed = React.useCallback(async () => {
    try {
      const recent = await MusicAPI.getRecentlyPlayed();
      setRecentlyPlayedTracks(recent);
    } catch (error) {
      console.error('Failed to load recently played tracks:', error);
      setRecentlyPlayedTracks([]);
    }
  }, []);

  useEffect(() => {
    void loadRecentlyPlayed();
  }, [loadRecentlyPlayed]);

  useFocusEffect(
    React.useCallback(() => {
      void loadRecentlyPlayed();
    }, [loadRecentlyPlayed])
  );
  useEffect(() => {
    let mounted = true;
    void (async () => {
      setHomeLoading(true);
      try {
        const sections = await MusicAPI.getHomeSections();
        if (mounted) setHomeSections(sections);
      } catch (error) {
        console.error('[Home] Extension recommendations unavailable:', error);
        if (mounted) setHomeSections([]);
      } finally {
        if (mounted) setHomeLoading(false);
      }
    })();
    return () => { mounted = false; };
  }, []);

  const handleViewChange = (view: 'home' | 'search') => {
    setCurrentView(view);
    if (view === 'home') {
      clearResults();
    }
  };

  const handleSearchClick = () => {
    router.push('/search');
  };

  const handleSearchStart = () => {
    setCurrentView('search');
  };

  const saveFirstRunSetup = async () => {
    setIsSavingSetup(true);
    try {
      await AsyncStorage.setItem(REGION_OVERRIDE_KEY, setupRegion);
      await AsyncStorage.setItem(LANGUAGE_KEY, setupLanguage);
      await AsyncStorage.setItem(FIRST_RUN_SETUP_KEY, '1');
      await i18n.changeLanguage(setupLanguage);
      setMode(setupTheme);
      setShowFirstRunSetup(false);
    } catch (error) {
      console.error('Failed to save first run setup:', error);
    } finally {
      setIsSavingSetup(false);
    }
  };

  const handleHomeTrackSelect = React.useCallback(
    (track: Track, trackList?: Track[], startIndex?: number) => {
      handleTrackSelect(track, trackList, startIndex);
      setRecentlyPlayedTracks((prev) => {
        const withoutCurrent = prev.filter((item) => item.id.toString() !== track.id.toString());
        return [track, ...withoutCurrent].slice(0, 30);
      });
    },
    [handleTrackSelect]
  );

  const handleShuffleLiked = React.useCallback(() => {
    if (likedTracks.length > 0) {
      const shuffled = [...likedTracks].sort(() => Math.random() - 0.5);
      handleTrackSelect(shuffled[0], shuffled, 0);
    }
  }, [likedTracks, handleTrackSelect]);

  const handleDownloadsNav = React.useCallback(() => {
    router.push('/downloads');
  }, [router]);

  const handleLibraryNav = React.useCallback(() => {
    router.push('/library');
  }, [router]);


  return (
    <SafeAreaView style={[styles.container, { backgroundColor: theme.background }]}>
      <StatusBar barStyle={isDark ? 'light-content' : 'dark-content'} backgroundColor={theme.background} translucent={false} />
      <TopBar
        currentView={currentView}
        onViewChange={handleViewChange}
        onSearchClick={handleSearchClick}
        onSearchStart={handleSearchStart}
        searchState={searchState}
      />
      <View style={styles.mainContent}>
        {currentView === 'home' ? (
          <ScrollView showsVerticalScrollIndicator={false} contentContainerStyle={styles.scrollContent}>
            <GreetingHeader />
            <QuickActions
              onShuffleLiked={handleShuffleLiked}
              onDownloads={handleDownloadsNav}
              onLibrary={handleLibraryNav}
            />
            {homeLoading && <Text style={{ color: theme.textSecondary, textAlign: 'center', marginTop: 8, marginBottom: 16 }}>{t('home.loading_recommendations', { defaultValue: 'Loading recommendations from Extensions…' })}</Text>}
            {!homeLoading && homeSections.length === 0 && (
              <View style={[styles.emptyBox, { backgroundColor: theme.surface, borderColor: theme.border }]}>
                <Ionicons name="extension-puzzle-outline" size={24} color={theme.textSecondary} />
                <Text style={[styles.emptyText, { color: theme.textSecondary }]}>{t('home.no_extension_recommendations', { defaultValue: 'Enable an Extension with search and home capabilities to see recommendations.' })}</Text>
                <TouchableOpacity onPress={() => router.push('/extensions')}><Text style={{ color: theme.accent, fontWeight: '700', marginTop: 8 }}>{t('common.open', { defaultValue: 'Open Extensions' })}</Text></TouchableOpacity>
              </View>
            )}
            {homeSections.map((section) => (
              <View key={`${section.providerId}:${section.id}`} style={{ marginTop: 16 }}>
                <SectionHeader title={t(`home.section_${section.id}`, { defaultValue: section.id })} />
                <HorizontalTrackList title="" tracks={section.tracks} onTrackSelect={handleHomeTrackSelect} isPlaying={isPlaying} currentTrack={currentTrack} />
              </View>
            ))}

            <View style={{ marginTop: 16 }}>
              <SectionHeader title={t('home.liked_songs')} onSeeAll={handleLibraryNav} />
              {likedTracks.length > 0 ? (
                <HorizontalTrackList
                  title=""
                  tracks={likedTracks}
                  onTrackSelect={handleHomeTrackSelect}
                  isPlaying={isPlaying}
                  currentTrack={currentTrack}
                />
              ) : (
                <View style={[styles.emptyBox, { backgroundColor: theme.surface, borderColor: theme.border }]}>
                  <Ionicons name="heart-outline" size={24} color={theme.textSecondary} />
                  <Text style={[styles.emptyText, { color: theme.textSecondary }]}>
                    {t('home.empty_liked')}
                  </Text>
                </View>
              )}
            </View>

            <View style={{ marginTop: 16 }}>
              <SectionHeader title={t('home.continue_listening')} />
              {recentlyPlayedTracks.length > 0 ? (
                <HorizontalTrackList
                  title=""
                  tracks={recentlyPlayedTracks.slice(0, 10)}
                  onTrackSelect={handleHomeTrackSelect}
                  isPlaying={isPlaying}
                  currentTrack={currentTrack}
                />
              ) : (
                <View style={[styles.emptyBox, { backgroundColor: theme.surface, borderColor: theme.border }]}>
                  <Ionicons name="time-outline" size={24} color={theme.textSecondary} />
                  <Text style={[styles.emptyText, { color: theme.textSecondary }]}>
                    {t('home.empty_recent')}
                  </Text>
                </View>
              )}
            </View>
            <View style={{ height: 140 }} />
          </ScrollView>
        ) : (
          <></>
        )}
      </View>
      <Modal visible={showFirstRunSetup} transparent animationType="fade">
        <View style={styles.setupOverlay}>
          <View style={[styles.setupCard, { backgroundColor: theme.surface, borderColor: theme.border }]}>
            <Text style={[styles.setupTitle, { color: theme.textPrimary }]}>Welcome to OpenSpot</Text>
            <Text style={[styles.setupSubtitle, { color: theme.textSecondary }]}>
              Set your preferences once. Trending loads in the background.
            </Text>

            <Text style={[styles.setupSectionTitle, { color: theme.textPrimary }]}>Region</Text>
            <TouchableOpacity
              style={[styles.setupDropdownButton, { backgroundColor: theme.surfaceElevated, borderColor: theme.border }]}
              onPress={() => setIsRegionModalOpen(true)}
            >
              <Text style={[styles.setupDropdownButtonText, { color: theme.textPrimary }]}>
                {setupRegion === 'auto' ? 'Auto' : setupRegion}
              </Text>
              <Ionicons name="chevron-down" size={16} color={theme.textSecondary} />
            </TouchableOpacity>

            <Text style={[styles.setupSectionTitle, { color: theme.textPrimary }]}>Language</Text>
            <TouchableOpacity
              style={[styles.setupDropdownButton, { backgroundColor: theme.surfaceElevated, borderColor: theme.border }]}
              onPress={() => setIsLanguageModalOpen(true)}
            >
              <Text style={[styles.setupDropdownButtonText, { color: theme.textPrimary }]}>
                {languageOptions.find((option) => option.value === setupLanguage)?.label || 'English'}
              </Text>
              <Ionicons name="chevron-down" size={16} color={theme.textSecondary} />
            </TouchableOpacity>

            <Text style={[styles.setupSectionTitle, { color: theme.textPrimary }]}>Theme</Text>
            <View style={styles.setupRow}>
              {[
                { label: t('components.theme_light'), value: 'light' as ThemeMode },
                { label: t('components.theme_dark'), value: 'dark' as ThemeMode },
                { label: t('components.theme_auto'), value: 'auto' as ThemeMode },
              ].map((themeOption) => {
                const active = setupTheme === themeOption.value;
                return (
                  <TouchableOpacity
                    key={`setup-theme-${themeOption.value}`}
                    style={[
                      styles.setupSegment,
                      { borderColor: theme.border, backgroundColor: theme.surfaceElevated },
                      active && { backgroundColor: theme.accent, borderColor: theme.accent },
                    ]}
                    onPress={() => setSetupTheme(themeOption.value)}
                  >
                    <Text style={[styles.setupSegmentText, { color: active ? '#fff' : theme.textSecondary }]}>{themeOption.label}</Text>
                  </TouchableOpacity>
                );
              })}
            </View>

            <TouchableOpacity
              style={[styles.setupContinueButton, { backgroundColor: theme.accent }]}
              onPress={saveFirstRunSetup}
              disabled={isSavingSetup}
            >
              {isSavingSetup ? <ActivityIndicator color="#fff" /> : <Text style={styles.setupContinueText}>Continue</Text>}
            </TouchableOpacity>
          </View>
        </View>
      </Modal>

      <Modal
        visible={isLanguageModalOpen}
        transparent
        animationType="fade"
        onRequestClose={() => setIsLanguageModalOpen(false)}
      >
        <View style={styles.setupModalOverlay}>
          <View style={[styles.setupLanguageModalCard, { backgroundColor: theme.surface, borderColor: theme.border }]}>
            <Text style={[styles.setupSectionTitle, { color: theme.textPrimary, marginBottom: 12 }]}>Language</Text>
            <FlatList
              data={languageOptions}
              keyExtractor={(item) => item.value}
              renderItem={({ item }) => {
                const active = setupLanguage === item.value;
                return (
                  <TouchableOpacity
                    style={[
                      styles.setupLanguageOptionRow,
                      { borderColor: theme.border, backgroundColor: theme.surfaceElevated },
                      active && { borderColor: theme.accent },
                    ]}
                    onPress={() => {
                      setSetupLanguage(item.value);
                      setIsLanguageModalOpen(false);
                    }}
                  >
                    <View>
                      <Text style={[styles.setupLanguageOptionTitle, { color: theme.textPrimary }]}>{item.label}</Text>
                      <Text style={[styles.setupLanguageOptionSubtitle, { color: theme.textSecondary }]}>{item.nativeLabel}</Text>
                    </View>
                    {active && <Ionicons name="checkmark-circle" size={18} color={theme.accent} />}
                  </TouchableOpacity>
                );
              }}
              ItemSeparatorComponent={() => <View style={{ height: 8 }} />}
            />
            <TouchableOpacity style={styles.setupCancelButtonRow} onPress={() => setIsLanguageModalOpen(false)}>
              <Text style={{ color: theme.textPrimary, fontSize: 15 }}>Close</Text>
            </TouchableOpacity>
          </View>
        </View>
      </Modal>

      <Modal
        visible={isRegionModalOpen}
        transparent
        animationType="fade"
        onRequestClose={() => setIsRegionModalOpen(false)}
      >
        <View style={styles.setupModalOverlay}>
          <View style={[styles.setupLanguageModalCard, { backgroundColor: theme.surface, borderColor: theme.border }]}>
            <Text style={[styles.setupSectionTitle, { color: theme.textPrimary, marginBottom: 12 }]}>Region</Text>
            <FlatList
              data={['auto']}
              keyExtractor={(item) => item}
              renderItem={({ item }) => {
                const active = setupRegion === item;
                const label = item === 'auto' ? 'Auto' : item;
                return (
                  <TouchableOpacity
                    style={[
                      styles.setupLanguageOptionRow,
                      { borderColor: theme.border, backgroundColor: theme.surfaceElevated },
                      active && { borderColor: theme.accent },
                    ]}
                    onPress={() => {
                      setSetupRegion(item);
                      setIsRegionModalOpen(false);
                    }}
                  >
                    <Text style={[styles.setupLanguageOptionTitle, { color: theme.textPrimary }]}>{label}</Text>
                    {active && <Ionicons name="checkmark-circle" size={18} color={theme.accent} />}
                  </TouchableOpacity>
                );
              }}
              ItemSeparatorComponent={() => <View style={{ height: 8 }} />}
            />
            <TouchableOpacity style={styles.setupCancelButtonRow} onPress={() => setIsRegionModalOpen(false)}>
              <Text style={{ color: theme.textPrimary, fontSize: 15 }}>Close</Text>
            </TouchableOpacity>
          </View>
        </View>
      </Modal>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
  },
  mainContent: {
    paddingTop: 10,
    flex: 1,
  },
  setupOverlay: {
    flex: 1,
    backgroundColor: 'rgba(0,0,0,0.55)',
    justifyContent: 'center',
    paddingHorizontal: 16,
  },
  setupCard: {
    borderRadius: 16,
    borderWidth: 1,
    padding: 16,
  },
  setupTitle: {
    fontSize: 22,
    fontWeight: '800',
  },
  setupSubtitle: {
    marginTop: 6,
    fontSize: 13,
    lineHeight: 18,
  },
  setupSectionTitle: {
    marginTop: 14,
    marginBottom: 8,
    fontSize: 14,
    fontWeight: '700',
  },
  setupWrap: {
    flexDirection: 'row',
    flexWrap: 'wrap',
    gap: 8,
  },
  setupChip: {
    borderWidth: 1,
    borderRadius: 15,
    paddingHorizontal: 10,
    paddingVertical: 7,
  },
  setupChipText: {
    fontSize: 12,
    fontWeight: '600',
  },
  setupRow: {
    flexDirection: 'row',
    gap: 8,
  },
  setupSegment: {
    flex: 1,
    borderWidth: 1,
    borderRadius: 12,
    alignItems: 'center',
    justifyContent: 'center',
    minHeight: 40,
  },
  setupSegmentText: {
    fontSize: 13,
    fontWeight: '700',
  },
  setupDropdownButton: {
    minHeight: 44,
    borderWidth: 1,
    borderRadius: 12,
    paddingHorizontal: 12,
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
  },
  setupDropdownButtonText: {
    fontSize: 14,
    fontWeight: '600',
  },
  setupModalOverlay: {
    flex: 1,
    backgroundColor: 'rgba(0,0,0,0.55)',
    alignItems: 'center',
    justifyContent: 'center',
    paddingHorizontal: 16,
  },
  setupLanguageModalCard: {
    width: '88%',
    maxHeight: '70%',
    borderWidth: 1,
    borderRadius: 16,
    padding: 14,
  },
  setupLanguageOptionRow: {
    borderWidth: 1,
    borderRadius: 12,
    paddingVertical: 10,
    paddingHorizontal: 12,
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
  },
  setupLanguageOptionTitle: {
    fontSize: 14,
    fontWeight: '600',
  },
  setupLanguageOptionSubtitle: {
    fontSize: 12,
    marginTop: 2,
  },
  setupCancelButtonRow: {
    alignItems: 'center',
    marginTop: 12,
    paddingVertical: 8,
  },
  setupContinueButton: {
    marginTop: 18,
    borderRadius: 12,
    alignItems: 'center',
    justifyContent: 'center',
    minHeight: 44,
  },
  setupContinueText: {
    color: '#fff',
    fontSize: 15,
    fontWeight: '700',
  },
  scrollContent: {
    paddingBottom: 22,
  },
  emptyText: {
    marginTop: 8,
    fontSize: 13,
  },
  emptyBox: {
    marginHorizontal: 16,
    borderRadius: 14,
    paddingVertical: 18,
    paddingHorizontal: 14,
    borderWidth: 1,
    alignItems: 'center',
  },
});
