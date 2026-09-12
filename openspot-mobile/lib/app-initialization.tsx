import AsyncStorage from '@react-native-async-storage/async-storage';
import React, { createContext, useContext, useEffect, useState } from 'react';

import i18n from '@/lib/i18n';

const LANGUAGE_KEY = 'openspot_language_v1';
const SUPPORTED_LANGUAGES = new Set(['en', 'vi', 'hi', 'es', 'zh', 'de', 'fr', 'ru', 'he', 'tr', 'ko']);

interface AppInitializationContextValue {
  ready: boolean;
}

const AppInitializationContext = createContext<AppInitializationContextValue>({ ready: false });

export function AppInitializationProvider({ children }: { children: React.ReactNode }) {
  const [ready, setReady] = useState(false);

  useEffect(() => {
    let mounted = true;
    const initialize = async () => {
      try {
        const storedLanguage = await AsyncStorage.getItem(LANGUAGE_KEY);
        if (storedLanguage && SUPPORTED_LANGUAGES.has(storedLanguage)) {
          await i18n.changeLanguage(storedLanguage);
        }
      } catch (error) {
        // Initialization must never leave the app behind a blocked splash.
        console.error('[AppInitialization] non-critical state restore failed:', error);
      } finally {
        if (mounted) setReady(true);
      }
    };
    void initialize();
    return () => {
      mounted = false;
    };
  }, []);

  return <AppInitializationContext.Provider value={{ ready }}>{children}</AppInitializationContext.Provider>;
}

export function useAppInitialization(): AppInitializationContextValue {
  return useContext(AppInitializationContext);
}
