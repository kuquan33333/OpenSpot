import { useFonts } from 'expo-font';
import { Stack } from 'expo-router';
import * as SplashScreen from 'expo-splash-screen';
import { StatusBar } from 'expo-status-bar';
import { useEffect } from 'react';
import { ActivityIndicator, View } from 'react-native';
import { SafeAreaProvider } from 'react-native-safe-area-context';

import { useThemeMode, ThemeModeProvider } from '@/hooks/theme-mode';
import { AppInitializationProvider, useAppInitialization } from '@/lib/app-initialization';
import { LikedSongsProvider } from '@/hooks/useLikedSongs';
import { ErrorBoundary } from '@/components/ErrorBoundary';
import '@/lib/i18n';

SplashScreen.preventAutoHideAsync();

function AppNavigation() {
  const { resolvedScheme, ready: themeReady } = useThemeMode();
  const { ready: appReady } = useAppInitialization();
  if (!appReady || !themeReady) {
    return (
      <View style={{ flex: 1, alignItems: 'center', justifyContent: 'center', backgroundColor: resolvedScheme === 'dark' ? '#050505' : '#f5efe6' }}>
        <ActivityIndicator color={resolvedScheme === 'dark' ? '#1DB954' : '#167c3a'} />
      </View>
    );
  }

  return (
    <LikedSongsProvider>
      <Stack>
        <Stack.Screen name="(tabs)" options={{ headerShown: false }} />
        <Stack.Screen name="+not-found" />
      </Stack>
      <StatusBar style={resolvedScheme === 'dark' ? 'light' : 'dark'} />
    </LikedSongsProvider>
  );
}

export default function RootLayout() {
  const [loaded] = useFonts({
    SpaceMono: require('../assets/fonts/SpaceMono-Regular.ttf'),
  });

  useEffect(() => {
    if (loaded) {
      SplashScreen.hideAsync();
    }
  }, [loaded]);

  if (!loaded) {
    return null;
  }

  return (
    <ErrorBoundary>
      <SafeAreaProvider>
        <AppInitializationProvider>
          <ThemeModeProvider>
            <AppNavigation />
          </ThemeModeProvider>
        </AppInitializationProvider>
      </SafeAreaProvider>
    </ErrorBoundary>
  );
}
