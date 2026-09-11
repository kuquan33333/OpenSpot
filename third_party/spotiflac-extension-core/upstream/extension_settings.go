package gobackend

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type ExtensionSettingsStore struct {
	mu       sync.RWMutex
	dataDir  string
	settings map[string]map[string]any // extensionID -> settings
}

var (
	globalSettingsStore     *ExtensionSettingsStore
	globalSettingsStoreOnce sync.Once
)

func GetExtensionSettingsStore() *ExtensionSettingsStore {
	globalSettingsStoreOnce.Do(func() {
		globalSettingsStore = &ExtensionSettingsStore{
			settings: make(map[string]map[string]any),
		}
	})
	return globalSettingsStore
}

func (s *ExtensionSettingsStore) SetDataDir(dataDir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !extensionStorageKeyConfigured() {
		return fmt.Errorf("extension storage master key is not configured")
	}

	s.dataDir = dataDir
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("failed to create settings directory: %w", err)
	}

	return s.loadAllSettings()
}

func (s *ExtensionSettingsStore) getSettingsPath(extensionID string) string {
	return filepath.Join(s.dataDir, extensionID, "settings.enc")
}

func (s *ExtensionSettingsStore) getLegacySettingsPath(extensionID string) string {
	return filepath.Join(s.dataDir, extensionID, "settings.json")
}

func (s *ExtensionSettingsStore) loadAllSettings() error {
	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			extensionID := entry.Name()
			settings, err := s.loadSettings(extensionID)
			if err != nil {
				GoLog("[ExtensionSettings] Failed to load settings for %s: %v\n", extensionID, err)
				continue
			}
			s.settings[extensionID] = settings
		}
	}

	return nil
}

func (s *ExtensionSettingsStore) loadSettings(extensionID string) (map[string]any, error) {
	settingsPath := s.getSettingsPath(extensionID)
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return s.migrateLegacySettings(extensionID)
		}
		return nil, err
	}
	key, err := deriveExtensionStorageKey(extensionID, "settings")
	if err != nil {
		return nil, err
	}
	data, err = decryptAES(data, key)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt settings: %w", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, err
	}

	return settings, nil
}

func (s *ExtensionSettingsStore) migrateLegacySettings(extensionID string) (map[string]any, error) {
	legacyPath := s.getLegacySettingsPath(extensionID)
	data, err := os.ReadFile(legacyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]any), nil
		}
		return nil, err
	}
	settings := make(map[string]any)
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("failed to read legacy settings: %w", err)
	}
	if err := s.saveSettings(extensionID, settings); err != nil {
		return nil, fmt.Errorf("failed to encrypt legacy settings: %w", err)
	}
	if err := os.Remove(legacyPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to remove legacy settings: %w", err)
	}
	return settings, nil
}

func (s *ExtensionSettingsStore) saveSettings(extensionID string, settings map[string]any) error {
	settingsPath := s.getSettingsPath(extensionID)

	dir := filepath.Dir(settingsPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	key, err := deriveExtensionStorageKey(extensionID, "settings")
	if err != nil {
		return err
	}
	data, err = encryptAES(data, key)
	if err != nil {
		return err
	}
	fileMu := extensionFileMu(settingsPath)
	fileMu.Lock()
	err = writeExtensionFileLocked(settingsPath, data)
	fileMu.Unlock()
	return err
}

func (s *ExtensionSettingsStore) Get(extensionID, key string) (any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	extSettings, exists := s.settings[extensionID]
	if !exists {
		return nil, fmt.Errorf("extension '%s' settings not found", extensionID)
	}

	value, exists := extSettings[key]
	if !exists {
		return nil, fmt.Errorf("setting '%s' not found for extension '%s'", key, extensionID)
	}
	return value, nil
}

func (s *ExtensionSettingsStore) GetAll(extensionID string) map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()

	extSettings, exists := s.settings[extensionID]
	if !exists {
		return make(map[string]any)
	}

	result := make(map[string]any)
	for k, v := range extSettings {
		result[k] = v
	}
	return result
}

func (s *ExtensionSettingsStore) Set(extensionID, key string, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.settings[extensionID]; !exists {
		s.settings[extensionID] = make(map[string]any)
	}

	s.settings[extensionID][key] = value

	return s.saveSettings(extensionID, s.settings[extensionID])
}

func (s *ExtensionSettingsStore) SetAll(extensionID string, settings map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.settings[extensionID] = settings

	return s.saveSettings(extensionID, settings)
}

func (s *ExtensionSettingsStore) Remove(extensionID, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	extSettings, exists := s.settings[extensionID]
	if !exists {
		return nil
	}

	delete(extSettings, key)

	return s.saveSettings(extensionID, extSettings)
}

func (s *ExtensionSettingsStore) RemoveAll(extensionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.settings, extensionID)

	settingsPath := s.getSettingsPath(extensionID)
	if err := os.Remove(settingsPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	legacyPath := s.getLegacySettingsPath(extensionID)
	if err := os.Remove(legacyPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove legacy extension settings: %w", err)
	}

	return nil
}

func (s *ExtensionSettingsStore) GetAllExtensionSettingsJSON() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := json.Marshal(s.settings)
	if err != nil {
		return "", err
	}

	return string(data), nil
}
