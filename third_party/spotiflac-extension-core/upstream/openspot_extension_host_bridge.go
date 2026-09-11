package gobackend

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// GetExtensionPendingAuthJSON returns the short-lived auth challenge that a
// native host may open for an extension. Access and refresh tokens are kept in
// the runtime auth state and are never serialized by this bridge.
func GetExtensionPendingAuthJSON(extensionID string) (string, error) {
	request, err := ensureExtensionPendingAuthRequest(extensionID)
	if err != nil {
		return "", err
	}
	if request == nil {
		return "", nil
	}

	return marshalExtensionJSON(map[string]any{
		"extension_id": request.ExtensionID,
		"auth_url":     request.AuthURL,
		"callback_url": request.CallbackURL,
	})
}

func ensureExtensionPendingAuthRequest(extensionID string) (*PendingAuthRequest, error) {
	extensionID = strings.TrimSpace(extensionID)
	if extensionID == "" {
		return nil, nil
	}

	if request := GetPendingAuthRequest(extensionID); request != nil {
		if time.Since(request.CreatedAt) < pendingAuthRequestTTL {
			return request, nil
		}
		ClearPendingAuthRequest(extensionID)
	}

	manager := getExtensionManager()
	ext, err := manager.GetExtension(extensionID)
	if err != nil || ext == nil || !ext.Enabled || ext.Manifest == nil || ext.Manifest.SignedSession == nil {
		return nil, nil
	}
	if err := ext.ensureRuntimeReady(); err != nil {
		return nil, err
	}
	if ext.runtime == nil {
		return nil, fmt.Errorf("extension '%s' runtime is unavailable", extensionID)
	}

	verificationRequired, err := ext.runtime.preflightSignedSession()
	if err != nil {
		return nil, err
	}
	if !verificationRequired {
		return nil, nil
	}
	return GetPendingAuthRequest(extensionID), nil
}

// SetExtensionAuthCodeByID hands an OAuth authorization code to the runtime.
// The native host must follow this with the extension's auth action.
func SetExtensionAuthCodeByID(extensionID, authCode string) {
	SetExtensionAuthCode(extensionID, authCode)
}

// SetExtensionSessionGrantByID hands a signed-session grant to the runtime.
// The grant is consumed only by the extension's completeGrant action.
func SetExtensionSessionGrantByID(extensionID, grant string) {
	setPendingSignedSessionGrant(extensionID, grant)
}

func SetExtensionTokensByID(extensionID, accessToken, refreshToken string, expiresIn int) {
	var expiresAt time.Time
	if expiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second)
	}
	SetExtensionTokens(extensionID, accessToken, refreshToken, expiresAt)
}

func ClearExtensionPendingAuthByID(extensionID string) {
	ClearPendingAuthRequest(extensionID)
}

func IsExtensionAuthenticatedByID(extensionID string) bool {
	extensionAuthStateMu.RLock()
	defer extensionAuthStateMu.RUnlock()

	state, exists := extensionAuthState[extensionID]
	if !exists || !state.IsAuthenticated {
		return false
	}
	return state.ExpiresAt.IsZero() || time.Now().Before(state.ExpiresAt)
}

// GetAllPendingAuthRequestsJSON returns only challenge metadata. Sort by
// extension ID so native hosts receive stable output despite map iteration.
func GetAllPendingAuthRequestsJSON() (string, error) {
	pendingAuthRequestsMu.RLock()
	ids := make([]string, 0, len(pendingAuthRequests))
	for extensionID := range pendingAuthRequests {
		ids = append(ids, extensionID)
	}
	sort.Strings(ids)
	requests := make([]map[string]any, 0, len(ids))
	for _, extensionID := range ids {
		request := pendingAuthRequests[extensionID]
		if request == nil {
			continue
		}
		requests = append(requests, map[string]any{
			"extension_id": request.ExtensionID,
			"auth_url":     request.AuthURL,
			"callback_url": request.CallbackURL,
		})
	}
	pendingAuthRequestsMu.RUnlock()

	return marshalExtensionJSON(requests)
}

func extensionFFmpegCommandJSON(commandID string, command *FFmpegCommand, includePaths bool) map[string]any {
	result := map[string]any{
		"command_id":   commandID,
		"extension_id": command.ExtensionID,
		"arguments":    append([]string(nil), command.Arguments...),
	}
	if includePaths {
		result["input_path"] = command.InputPath
		result["output_path"] = command.OutputPath
	}
	return result
}

func GetPendingFFmpegCommandJSON(commandID string) (string, error) {
	ffmpegCommandsMu.RLock()
	command := ffmpegCommands[commandID]
	if command == nil {
		ffmpegCommandsMu.RUnlock()
		return "", nil
	}
	result := extensionFFmpegCommandJSON(commandID, command, true)
	ffmpegCommandsMu.RUnlock()
	return marshalExtensionJSON(result)
}

func SetFFmpegCommandResultByID(commandID string, success bool, output, errorMsg string) {
	SetFFmpegCommandResult(commandID, success, output, errorMsg)
}

func GetAllPendingFFmpegCommandsJSON() (string, error) {
	ffmpegCommandsMu.RLock()
	ids := make([]string, 0, len(ffmpegCommands))
	for commandID, command := range ffmpegCommands {
		if command != nil && !command.Completed && !command.Claimed {
			ids = append(ids, commandID)
		}
	}
	sort.Strings(ids)
	commands := make([]map[string]any, 0, len(ids))
	for _, commandID := range ids {
		command := ffmpegCommands[commandID]
		if command == nil || command.Completed || command.Claimed {
			continue
		}
		commands = append(commands, extensionFFmpegCommandJSON(commandID, command, false))
	}
	ffmpegCommandsMu.RUnlock()

	return marshalExtensionJSON(commands)
}

// WaitForPendingFFmpegCommandsJSON claims queued commands atomically so two
// native command pumps cannot execute the same request.
func WaitForPendingFFmpegCommandsJSON(timeoutMillis int64) (string, error) {
	if timeoutMillis < 0 {
		timeoutMillis = 0
	}
	deadline := time.NewTimer(time.Duration(timeoutMillis) * time.Millisecond)
	defer deadline.Stop()

	for {
		ffmpegCommandsMu.Lock()
		ids := make([]string, 0, len(ffmpegCommands))
		for commandID, command := range ffmpegCommands {
			if command != nil && !command.Completed && !command.Claimed {
				ids = append(ids, commandID)
			}
		}
		sort.Strings(ids)
		commands := make([]map[string]any, 0, len(ids))
		for _, commandID := range ids {
			command := ffmpegCommands[commandID]
			if command == nil || command.Completed || command.Claimed {
				continue
			}
			command.Claimed = true
			commands = append(commands, extensionFFmpegCommandJSON(commandID, command, false))
		}
		ffmpegCommandsMu.Unlock()

		if len(commands) > 0 {
			return marshalExtensionJSON(commands)
		}

		select {
		case <-ffmpegCommandQueued:
			continue
		case <-deadline.C:
			return "[]", nil
		}
	}
}

func HandleURLWithExtensionJSON(url string) (string, error) {
	handled, err := getExtensionManager().HandleURLWithExtension(url)
	if err != nil {
		return "", err
	}
	if handled == nil || handled.Result == nil {
		return "", fmt.Errorf("extension URL handler returned no result")
	}

	result := handled.Result
	response := map[string]any{
		"type":         result.Type,
		"id":           result.ID,
		"extension_id": handled.ExtensionID,
		"name":         result.Name,
		"cover_url":    result.CoverURL,
		"header_image": result.HeaderImage,
		"header_video": result.HeaderVideo,
	}
	if result.Track != nil {
		response["track"] = normalizeExtensionTrackMetadataMap(*result.Track, "", 0)
	}
	if len(result.Tracks) > 0 {
		tracks := make([]map[string]any, len(result.Tracks))
		for i, track := range result.Tracks {
			tracks[i] = normalizeExtensionTrackMetadataMap(track, "", 0)
		}
		response["tracks"] = tracks
	}
	if result.Album != nil {
		response["album"] = normalizeExtensionAlbumInfoMap(result.Album)
	}
	if result.Artist != nil {
		artist := result.Artist
		artistResponse := map[string]any{
			"id":           artist.ID,
			"name":         artist.Name,
			"image_url":    artist.ImageURL,
			"header_image": artist.HeaderImage,
			"header_video": artist.HeaderVideo,
			"listeners":    artist.Listeners,
			"provider_id":  artist.ProviderID,
		}
		if len(artist.Albums) > 0 {
			albums := make([]map[string]any, len(artist.Albums))
			for i, album := range artist.Albums {
				albums[i] = normalizeExtensionArtistAlbumMap(album)
				if albums[i]["album_type"] == "" {
					albums[i]["album_type"] = "album"
				}
			}
			artistResponse["albums"] = albums
		}
		if len(artist.Releases) > 0 {
			releases := make([]map[string]any, len(artist.Releases))
			for i, release := range artist.Releases {
				releases[i] = normalizeExtensionArtistAlbumMap(release)
				if releases[i]["album_type"] == "" {
					releases[i]["album_type"] = "album"
				}
			}
			artistResponse["releases"] = releases
		}
		if len(artist.TopTracks) > 0 {
			topTracks := make([]map[string]any, len(artist.TopTracks))
			for i, track := range artist.TopTracks {
				topTracks[i] = normalizeExtensionTrackMetadataMap(track, "", 0)
			}
			artistResponse["top_tracks"] = topTracks
		}
		response["artist"] = artistResponse
	}

	return marshalExtensionJSON(response)
}

func FindURLHandlerJSON(url string) string {
	handler := getExtensionManager().FindURLHandler(url)
	if handler == nil {
		return ""
	}
	return handler.extension.ID
}

func RunPostProcessingV2JSON(inputJSON, metadataJSON string) (string, error) {
	var input PostProcessInput
	if strings.TrimSpace(inputJSON) != "" {
		if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
			input = PostProcessInput{}
		}
	}

	var metadata map[string]any
	if strings.TrimSpace(metadataJSON) != "" {
		if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
			metadata = make(map[string]any)
		}
	}

	result, err := getExtensionManager().RunPostProcessingV2(input, metadata)
	if err != nil {
		return "", err
	}
	return marshalExtensionJSON(result)
}
