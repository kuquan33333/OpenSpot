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

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func normalizeExtensionTrackMetadataMap(track ExtTrackMetadata, fallbackCover string, fallbackTrackNumber int) map[string]any {
	coverURL := track.ResolvedCoverURL()
	if coverURL == "" {
		coverURL = fallbackCover
	}
	trackNum := track.TrackNumber
	if trackNum == 0 && fallbackTrackNumber > 0 {
		trackNum = fallbackTrackNumber
	}
	return map[string]any{
		"id": track.ID, "name": track.Name, "artists": track.Artists,
		"album_name": track.AlbumName, "album_artist": track.AlbumArtist,
		"album_id": track.AlbumID, "album_url": track.AlbumURL,
		"artist_id": track.ArtistID, "artist_url": track.ArtistURL,
		"external_urls": track.ExternalURL, "duration_ms": track.DurationMS,
		"images": coverURL, "cover_url": coverURL, "preview_url": track.PreviewURL,
		"release_date": track.ReleaseDate, "track_number": trackNum,
		"total_tracks": track.TotalTracks, "disc_number": track.DiscNumber,
		"total_discs": track.TotalDiscs, "isrc": track.ISRC,
		"provider_id": track.ProviderID, "item_type": track.ItemType,
		"album_type": track.AlbumType, "spotify_id": track.SpotifyID,
		"external_links": track.ExternalLinks, "genre": track.Genre,
		"label": track.Label, "copyright": track.Copyright,
		"composer": track.Composer, "comment": track.Comment,
		"audio_quality": track.AudioQuality, "audio_modes": track.AudioModes,
		"explicit": track.Explicit, "upc": track.UPC,
	}
}

func normalizeExtensionAlbumInfoMap(album *ExtAlbumMetadata) map[string]any {
	if album == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id": album.ID, "name": album.Name, "artists": album.Artists,
		"artist_id": album.ArtistID, "images": album.CoverURL,
		"cover_url": album.CoverURL, "header_image": album.HeaderImage,
		"header_video": album.HeaderVideo, "release_date": album.ReleaseDate,
		"total_tracks": album.TotalTracks, "album_type": album.AlbumType,
		"audio_traits": album.AudioTraits, "provider_id": album.ProviderID,
	}
}

func normalizeExtensionArtistAlbumMap(album ExtAlbumMetadata) map[string]any {
	return map[string]any{
		"id": album.ID, "name": album.Name, "artists": album.Artists,
		"images": album.CoverURL, "cover_url": album.CoverURL,
		"release_date": album.ReleaseDate, "total_tracks": album.TotalTracks,
		"album_type": album.AlbumType, "provider_id": album.ProviderID,
	}
}

func getExtensionProviderMetadataResponse(providerID, resourceType, resourceID string) (map[string]any, error) {
	ext, err := getExtensionManager().GetExtension(providerID)
	if err != nil {
		return nil, err
	}
	if ext.Manifest == nil || !ext.Manifest.IsMetadataProvider() {
		return nil, fmt.Errorf("extension '%s' is not a metadata provider", providerID)
	}
	if !ext.Enabled {
		return nil, fmt.Errorf("extension '%s' is disabled", providerID)
	}
	provider := newExtensionProviderWrapper(ext)
	switch strings.ToLower(strings.TrimSpace(resourceType)) {
	case "track":
		track, err := provider.GetTrack(resourceID)
		if err != nil {
			return nil, err
		}
		if track == nil {
			return nil, fmt.Errorf("track not found")
		}
		return map[string]any{"track": normalizeExtensionTrackMetadataMap(*track, "", 0)}, nil
	case "album":
		album, err := provider.GetAlbum(resourceID)
		if err != nil {
			return nil, err
		}
		if album == nil {
			return nil, fmt.Errorf("album not found")
		}
		tracks := make([]map[string]any, len(album.Tracks))
		for i, track := range album.Tracks {
			tracks[i] = normalizeExtensionTrackMetadataMap(track, album.CoverURL, i+1)
		}
		return map[string]any{"album_info": normalizeExtensionAlbumInfoMap(album), "track_list": tracks}, nil
	case "playlist":
		playlist, err := provider.GetPlaylist(resourceID)
		if err != nil {
			return nil, err
		}
		if playlist == nil {
			return nil, fmt.Errorf("playlist not found")
		}
		tracks := make([]map[string]any, len(playlist.Tracks))
		for i, track := range playlist.Tracks {
			tracks[i] = normalizeExtensionTrackMetadataMap(track, playlist.CoverURL, i+1)
		}
		return map[string]any{
			"playlist_info": map[string]any{
				"id": playlist.ID, "name": playlist.Name, "images": playlist.CoverURL,
				"cover_url": playlist.CoverURL, "header_image": playlist.HeaderImage,
				"header_video": playlist.HeaderVideo, "provider_id": playlist.ProviderID,
				"owner": map[string]any{"name": playlist.Artists, "images": playlist.CoverURL},
			},
			"track_list": tracks,
		}, nil
	case "artist":
		artist, err := provider.GetArtist(resourceID)
		if err != nil {
			return nil, err
		}
		if artist == nil {
			return nil, fmt.Errorf("artist not found")
		}
		albums := make([]map[string]any, len(artist.Albums))
		for i, album := range artist.Albums {
			albums[i] = normalizeExtensionArtistAlbumMap(album)
		}
		response := map[string]any{
			"artist_info": map[string]any{
				"id": artist.ID, "name": artist.Name,
				"images":    firstNonEmptyTrimmed(artist.HeaderImage, artist.ImageURL),
				"cover_url": artist.ImageURL, "header_image": artist.HeaderImage,
				"header_video": artist.HeaderVideo, "provider_id": artist.ProviderID,
			},
			"albums": albums,
		}
		if len(artist.Releases) > 0 {
			releases := make([]map[string]any, len(artist.Releases))
			for i, release := range artist.Releases {
				releases[i] = normalizeExtensionArtistAlbumMap(release)
			}
			response["releases"] = releases
		}
		if artist.Listeners > 0 {
			response["artist_info"].(map[string]any)["listeners"] = artist.Listeners
		}
		if len(artist.TopTracks) > 0 {
			topTracks := make([]map[string]any, len(artist.TopTracks))
			for i, track := range artist.TopTracks {
				topTracks[i] = normalizeExtensionTrackMetadataMap(track, artist.ImageURL, i+1)
			}
			response["top_tracks"] = topTracks
		}
		return response, nil
	default:
		return nil, fmt.Errorf("unsupported provider resource type: %s", resourceType)
	}
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
	return marshalExtensionJSON(map[string]any{"id": ext.ID, "name": ext.Manifest.Name, "display_name": ext.Manifest.DisplayName, "version": ext.Manifest.Version, "enabled": ext.Enabled})
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
	return marshalExtensionJSON(map[string]any{"id": ext.ID, "display_name": ext.Manifest.DisplayName, "version": ext.Manifest.Version, "enabled": ext.Enabled})
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
func GetProviderPriorityJSON() (string, error) { return marshalExtensionJSON(GetProviderPriority()) }

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

func extensionCallArgs(payloadJSON string) ([]json.RawMessage, error) {
	if strings.TrimSpace(payloadJSON) == "" {
		return nil, nil
	}
	var args []json.RawMessage
	if err := json.Unmarshal([]byte(payloadJSON), &args); err != nil {
		return nil, fmt.Errorf("invalid extension call arguments: %w", err)
	}
	return args, nil
}

func extensionCallString(args []json.RawMessage, index int) (string, error) {
	if index < 0 || index >= len(args) {
		return "", fmt.Errorf("missing extension call argument %d", index)
	}
	var value string
	if err := json.Unmarshal(args[index], &value); err != nil {
		return "", fmt.Errorf("invalid string extension call argument %d: %w", index, err)
	}
	return value, nil
}

func extensionCallInt(args []json.RawMessage, index int) (int, error) {
	if index < 0 || index >= len(args) {
		return 0, fmt.Errorf("missing extension call argument %d", index)
	}
	var value int
	if err := json.Unmarshal(args[index], &value); err != nil {
		return 0, fmt.Errorf("invalid integer extension call argument %d: %w", index, err)
	}
	return value, nil
}

func extensionCallBool(args []json.RawMessage, index int) (bool, error) {
	if index < 0 || index >= len(args) {
		return false, fmt.Errorf("missing extension call argument %d", index)
	}
	var value bool
	if err := json.Unmarshal(args[index], &value); err != nil {
		return false, fmt.Errorf("invalid boolean extension call argument %d: %w", index, err)
	}
	return value, nil
}

// CallOpenSpotExtensionJSON is the single JSON entry point used by native
// hosts that cannot bind every Go export individually. Arguments are encoded
// as a JSON array in the same order as the typed bridge functions. Returning
// JSON from this boundary keeps the React Native and Tauri adapters identical
// and avoids leaking application-level SpotiFLAC facades into the host.
func CallOpenSpotExtensionJSON(operation, payloadJSON string) (string, error) {
	args, err := extensionCallArgs(payloadJSON)
	if err != nil {
		return "", err
	}
	stringArg := func(index int) (string, error) { return extensionCallString(args, index) }
	void := func(callErr error) (string, error) {
		if callErr != nil {
			return "", callErr
		}
		return "null", nil
	}

	switch operation {
	case "InitExtensionSystem":
		extensionsDir, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		dataDir, callErr := stringArg(1)
		if callErr != nil {
			return "", callErr
		}
		return void(InitExtensionSystem(extensionsDir, dataDir))
	case "LoadExtensionsFromDir":
		value, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		return LoadExtensionsFromDir(value)
	case "GetInstalledExtensions":
		return GetInstalledExtensions()
	case "SetExtensionEnabledByID":
		id, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		enabled, callErr := extensionCallBool(args, 1)
		if callErr != nil {
			return "", callErr
		}
		return void(SetExtensionEnabledByID(id, enabled))
	case "GetExtensionSettingsJSON":
		id, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		return GetExtensionSettingsJSON(id)
	case "SetExtensionSettingsJSON":
		id, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		settings, callErr := stringArg(1)
		if callErr != nil {
			return "", callErr
		}
		return void(SetExtensionSettingsJSON(id, settings))
	case "GetProviderPriorityJSON":
		return GetProviderPriorityJSON()
	case "SetProviderPriorityJSON":
		value, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		return void(SetProviderPriorityJSON(value))
	case "GetMetadataProviderPriorityJSON":
		return GetMetadataProviderPriorityJSON()
	case "SetMetadataProviderPriorityJSON":
		value, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		return void(SetMetadataProviderPriorityJSON(value))
	case "SearchTracksWithMetadataProvidersJSON":
		query, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		limit, callErr := extensionCallInt(args, 1)
		if callErr != nil {
			return "", callErr
		}
		includeExtensions, callErr := extensionCallBool(args, 2)
		if callErr != nil {
			return "", callErr
		}
		return SearchTracksWithMetadataProvidersJSON(query, limit, includeExtensions)
	case "SearchTracksWithMetadataProviderJSON":
		providerID, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		query, callErr := stringArg(1)
		if callErr != nil {
			return "", callErr
		}
		limit, callErr := extensionCallInt(args, 2)
		if callErr != nil {
			return "", callErr
		}
		return SearchTracksWithMetadataProviderJSON(providerID, query, limit)
	case "GetProviderMetadataJSON":
		providerID, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		resourceType, callErr := stringArg(1)
		if callErr != nil {
			return "", callErr
		}
		resourceID, callErr := stringArg(2)
		if callErr != nil {
			return "", callErr
		}
		return GetProviderMetadataJSON(providerID, resourceType, resourceID)
	case "GetExtensionFallbackProviderIDsJSON":
		return GetExtensionFallbackProviderIDsJSON()
	case "SetExtensionFallbackProviderIDsJSON":
		value, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		return void(SetExtensionFallbackProviderIDsJSON(value))
	case "InitExtensionRepoJSON":
		value, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		return void(InitExtensionRepoJSON(value))
	case "SetRepoRegistryURLJSON":
		value, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		return void(SetRepoRegistryURLJSON(value))
	case "GetRepoRegistryURLJSON":
		return GetRepoRegistryURLJSON()
	case "GetRepoExtensionsJSON":
		forceRefresh, callErr := extensionCallBool(args, 0)
		if callErr != nil {
			return "", callErr
		}
		return GetRepoExtensionsJSON(forceRefresh)
	case "SearchRepoExtensionsJSON":
		query, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		category, callErr := stringArg(1)
		if callErr != nil {
			return "", callErr
		}
		return SearchRepoExtensionsJSON(query, category)
	case "GetRepoCategoriesJSON":
		return GetRepoCategoriesJSON()
	case "DownloadRepoExtensionJSON":
		extensionID, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		destination, callErr := stringArg(1)
		if callErr != nil {
			return "", callErr
		}
		return DownloadRepoExtensionJSON(extensionID, destination)
	case "ClearRepoCacheJSON":
		return void(ClearRepoCacheJSON())
	case "CheckExtensionHealthJSON":
		id, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		return CheckExtensionHealthJSON(id)
	case "GetExtensionPendingAuthJSON":
		id, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		return GetExtensionPendingAuthJSON(id)
	case "SetExtensionAuthCodeByID":
		id, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		code, callErr := stringArg(1)
		if callErr != nil {
			return "", callErr
		}
		SetExtensionAuthCodeByID(id, code)
		return "null", nil
	case "SetExtensionSessionGrantByID":
		id, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		grant, callErr := stringArg(1)
		if callErr != nil {
			return "", callErr
		}
		SetExtensionSessionGrantByID(id, grant)
		return "null", nil
	case "ClearExtensionPendingAuthByID":
		id, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		ClearExtensionPendingAuthByID(id)
		return "null", nil
	case "GetPendingFFmpegCommandJSON":
		id, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		return GetPendingFFmpegCommandJSON(id)
	case "WaitForPendingFFmpegCommandsJSON":
		timeoutMillis, callErr := extensionCallInt(args, 0)
		if callErr != nil {
			return "", callErr
		}
		return WaitForPendingFFmpegCommandsJSON(int64(timeoutMillis))
	case "SetFFmpegCommandResultByID":
		id, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		success, callErr := extensionCallBool(args, 1)
		if callErr != nil {
			return "", callErr
		}
		output, callErr := stringArg(2)
		if callErr != nil {
			return "", callErr
		}
		errorMsg, callErr := stringArg(3)
		if callErr != nil {
			return "", callErr
		}
		SetFFmpegCommandResultByID(id, success, output, errorMsg)
		return "null", nil
	case "HandleURLWithExtensionJSON":
		value, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		return HandleURLWithExtensionJSON(value)
	case "FindURLHandlerJSON":
		value, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		return marshalExtensionJSON(FindURLHandlerJSON(value))
	case "RunPostProcessingV2JSON":
		inputJSON, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		metadataJSON, callErr := stringArg(1)
		if callErr != nil {
			return "", callErr
		}
		return RunPostProcessingV2JSON(inputJSON, metadataJSON)
	case "DownloadExtensionWithFallbackJSON":
		requestJSON, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		return DownloadExtensionWithFallbackJSON(requestJSON)
	case "InvokeExtensionActionJSON":
		id, callErr := stringArg(0)
		if callErr != nil {
			return "", callErr
		}
		action, callErr := stringArg(1)
		if callErr != nil {
			return "", callErr
		}
		return InvokeExtensionActionJSON(id, action)
	default:
		return "", fmt.Errorf("unknown extension operation: %s", operation)
	}
}

// DownloadExtensionWithFallbackJSON exposes the provider-only fallback
// coordinator to native hosts. Progress callbacks belong to the native
// binding; the JSON entry point returns the complete attempt trace instead.
func DownloadExtensionWithFallbackJSON(requestJSON string) (string, error) {
	var request DownloadRequest
	if err := json.Unmarshal([]byte(requestJSON), &request); err != nil {
		return "", fmt.Errorf("invalid extension download request: %w", err)
	}
	result, err := DownloadExtensionProviderFallback(request, nil)
	if err != nil {
		return "", err
	}
	return marshalExtensionJSON(result)
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
	response, err := getExtensionProviderMetadataResponse(providerID, resourceType, resourceID)
	if err != nil {
		return "", err
	}
	return marshalExtensionJSON(response)
}

func CleanupExtensions() { getExtensionManager().UnloadAllExtensions() }
func InvokeExtensionActionJSON(extensionID, actionName string) (string, error) {
	result, err := getExtensionManager().InvokeAction(extensionID, actionName)
	if err != nil {
		return "", err
	}
	return marshalExtensionJSON(result)
}
