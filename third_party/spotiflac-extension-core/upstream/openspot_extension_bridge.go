package gobackend

import (
	"encoding/json"
	"fmt"
	"strings"
)

// This file is the OpenSpot host bridge over the pinned SpotiFLAC Extension
// Engine. Application-level SpotiFLAC services (SongLink, built-in Deezer,
// lyrics cache, metadata embedding and download orchestration) intentionally
// remain outside this boundary.

func marshalExtensionJSON(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func InitExtensionSystem(extensionsDir, dataDir string) error {
	if !extensionStorageKeyConfigured() {
		return fmt.Errorf("extension storage master key is not configured")
	}
	manager := getExtensionManager()
	if err := manager.SetDirectories(extensionsDir, dataDir); err != nil {
		return err
	}
	return GetExtensionSettingsStore().SetDataDir(dataDir)
}

func LoadExtensionsFromDir(dirPath string) (string, error) {
	loaded, loadErrors := getExtensionManager().LoadExtensionsFromDirectory(dirPath)
	messages := make([]string, len(loadErrors))
	for i, err := range loadErrors {
		messages[i] = err.Error()
	}
	return marshalExtensionJSON(map[string]any{"loaded": loaded, "errors": messages})
}

func LoadExtensionFromPath(filePath string) (string, error) {
	ext, err := getExtensionManager().LoadExtensionFromFile(filePath)
	if err != nil {
		return "", err
	}
	return marshalExtensionJSON(map[string]any{
		"id":           ext.ID,
		"name":         ext.Manifest.Name,
		"display_name": ext.Manifest.DisplayName,
		"version":      ext.Manifest.Version,
		"enabled":      ext.Enabled,
	})
}

func UnloadExtensionByID(extensionID string) error {
	return getExtensionManager().UnloadExtension(extensionID)
}

func RemoveExtensionByID(extensionID string) error {
	return getExtensionManager().RemoveExtension(extensionID)
}

func UpgradeExtensionFromPath(filePath string) (string, error) {
	ext, err := getExtensionManager().UpgradeExtension(filePath)
	if err != nil {
		return "", err
	}
	return marshalExtensionJSON(map[string]any{
		"id":           ext.ID,
		"display_name": ext.Manifest.DisplayName,
		"version":      ext.Manifest.Version,
		"enabled":      ext.Enabled,
	})
}

func CheckExtensionUpgradeFromPath(filePath string) (string, error) {
	return getExtensionManager().CheckExtensionUpgradeJSON(filePath)
}

func GetInstalledExtensions() (string, error) {
	return getExtensionManager().GetInstalledExtensionsJSON()
}

func SetExtensionEnabledByID(extensionID string, enabled bool) error {
	return getExtensionManager().SetExtensionEnabled(extensionID, enabled)
}

func SetProviderPriorityJSON(priorityJSON string) error {
	var priority []string
	if err := json.Unmarshal([]byte(priorityJSON), &priority); err != nil {
		return err
	}
	SetProviderPriority(priority)
	return nil
}

func GetProviderPriorityJSON() (string, error) {
	return marshalExtensionJSON(GetProviderPriority())
}

func SetExtensionFallbackProviderIDsJSON(providerIDsJSON string) error {
	if strings.TrimSpace(providerIDsJSON) == "" {
		SetExtensionFallbackProviderIDs(nil)
		return nil
	}
	var providerIDs []string
	if err := json.Unmarshal([]byte(providerIDsJSON), &providerIDs); err != nil {
		return err
	}
	SetExtensionFallbackProviderIDs(providerIDs)
	return nil
}

func GetExtensionFallbackProviderIDsJSON() (string, error) {
	return marshalExtensionJSON(GetExtensionFallbackProviderIDs())
}

func SetMetadataProviderPriorityJSON(priorityJSON string) error {
	var priority []string
	if err := json.Unmarshal([]byte(priorityJSON), &priority); err != nil {
		return err
	}
	SetMetadataProviderPriority(priority)
	return nil
}

func GetMetadataProviderPriorityJSON() (string, error) {
	return marshalExtensionJSON(GetMetadataProviderPriority())
}

func GetExtensionSettingsJSON(extensionID string) (string, error) {
	return marshalExtensionJSON(GetExtensionSettingsStore().GetAll(extensionID))
}

func SetExtensionSettingsJSON(extensionID, settingsJSON string) error {
	var settings map[string]any
	if err := json.Unmarshal([]byte(settingsJSON), &settings); err != nil {
		return err
	}
	store := GetExtensionSettingsStore()
	if err := store.SetAll(extensionID, settings); err != nil {
		return err
	}
	return getExtensionManager().InitializeExtension(extensionID, settings)
}

func SearchTracksWithMetadataProvidersJSON(query string, limit int, includeExtensions bool) (string, error) {
	tracks, err := getExtensionManager().SearchTracksWithMetadataProviders(query, limit, includeExtensions)
	if err != nil {
		return "", err
	}
	return marshalExtensionJSON(tracks)
}

func SearchTracksWithMetadataProviderJSON(providerID, query string, limit int) (string, error) {
	tracks, err := getExtensionManager().SearchTracksWithMetadataProvider(providerID, query, limit)
	if err != nil {
		return "", err
	}
	return marshalExtensionJSON(tracks)
}

func GetProviderMetadataJSON(providerID, resourceType, resourceID string) (string, error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return "", fmt.Errorf("empty provider ID")
	}
	ext, err := getExtensionManager().GetExtension(providerID)
	if err != nil {
		return "", err
	}
	if ext.Manifest == nil || !ext.Manifest.IsMetadataProvider() {
		return "", fmt.Errorf("extension '%s' is not a metadata provider", providerID)
	}
	if !ext.Enabled {
		return "", fmt.Errorf("extension '%s' is disabled", providerID)
	}

	provider := newExtensionProviderWrapper(ext)
	switch strings.ToLower(strings.TrimSpace(resourceType)) {
	case "track":
		value, err := provider.GetTrack(resourceID)
		if err != nil {
			return "", err
		}
		return marshalExtensionJSON(map[string]any{"track": value})
	case "album":
		value, err := provider.GetAlbum(resourceID)
		if err != nil {
			return "", err
		}
		return marshalExtensionJSON(map[string]any{"album": value})
	case "playlist":
		value, err := provider.GetPlaylist(resourceID)
		if err != nil {
			return "", err
		}
		return marshalExtensionJSON(map[string]any{"playlist": value})
	case "artist":
		value, err := provider.GetArtist(resourceID)
		if err != nil {
			return "", err
		}
		return marshalExtensionJSON(map[string]any{"artist": value})
	default:
		return "", fmt.Errorf("unsupported provider resource type: %s", resourceType)
	}
}

func CleanupExtensions() {
	getExtensionManager().UnloadAllExtensions()
}

func InvokeExtensionActionJSON(extensionID, actionName string) (string, error) {
	result, err := getExtensionManager().InvokeAction(extensionID, actionName)
	if err != nil {
		return "", err
	}
	return marshalExtensionJSON(result)
}
