package gobackend

import (
	"errors"
	"sort"
	"strings"
)

// ExtensionDownloadAttempt records one provider decision without exposing the
// SpotiFLAC application download pipeline. It is intentionally limited to the
// extension-facing contract so native hosts can surface fallback diagnostics.
type ExtensionDownloadAttempt struct {
	ProviderID         string `json:"provider_id"`
	TrackID            string `json:"track_id,omitempty"`
	Available          bool   `json:"available"`
	Success            bool   `json:"success"`
	Error              string `json:"error,omitempty"`
	ErrorType          string `json:"error_type,omitempty"`
	RetryAfterSeconds  int    `json:"retry_after_seconds,omitempty"`
	StoppedProviderRun bool   `json:"stopped_provider_run,omitempty"`
}

// ExtensionFallbackResult is the Core-scoped result of trying enabled
// download-provider extensions in configured order. Result is the last
// extension response when all attempts fail, or the successful response.
type ExtensionFallbackResult struct {
	Success      bool                       `json:"success"`
	ProviderID   string                     `json:"provider_id,omitempty"`
	FallbackUsed bool                       `json:"fallback_used,omitempty"`
	Stopped      bool                       `json:"stopped,omitempty"`
	Result       *ExtDownloadResult         `json:"result,omitempty"`
	Attempts     []ExtensionDownloadAttempt `json:"attempts"`
}

func (m *extensionManager) orderedExtensionDownloadProviders(providerHint string, allowFallback bool) []*extensionProviderWrapper {
	if m == nil {
		return nil
	}

	providersByID := make(map[string]*extensionProviderWrapper)
	for _, provider := range m.GetDownloadProviders() {
		if provider == nil || provider.extension == nil {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(provider.extension.ID))
		if key == "" {
			continue
		}
		providersByID[key] = provider
	}

	ordered := make([]*extensionProviderWrapper, 0, len(providersByID))
	seen := make(map[string]struct{}, len(providersByID))
	appendProvider := func(id string, force bool) {
		key := strings.ToLower(strings.TrimSpace(id))
		if key == "" {
			return
		}
		if _, exists := seen[key]; exists {
			return
		}
		provider := providersByID[key]
		if provider == nil || (!force && !isExtensionFallbackAllowed(provider.extension.ID)) {
			return
		}
		seen[key] = struct{}{}
		ordered = append(ordered, provider)
	}

	providerHint = strings.TrimSpace(providerHint)
	if providerHint != "" {
		// The selected provider is an explicit request, not a fallback
		// candidate, so a fallback allow-list must not suppress it.
		appendProvider(providerHint, true)
	}
	if !allowFallback {
		return ordered
	}

	for _, providerID := range GetProviderPriority() {
		appendProvider(providerID, false)
	}

	remainingIDs := make([]string, 0, len(providersByID))
	for key := range providersByID {
		if _, exists := seen[key]; !exists {
			remainingIDs = append(remainingIDs, key)
		}
	}
	sort.Strings(remainingIDs)
	for _, providerID := range remainingIDs {
		appendProvider(providerID, false)
	}
	return ordered
}

func extensionFallbackRequestProvider(req DownloadRequest) string {
	for _, candidate := range []string{req.Service, req.Source, req.DownloadProvider} {
		if providerID := strings.TrimSpace(candidate); providerID != "" {
			return providerID
		}
	}
	return ""
}

func extensionFallbackTrackID(req DownloadRequest, availability *ExtAvailabilityResult) string {
	if availability != nil {
		if trackID := strings.TrimSpace(availability.TrackID); trackID != "" {
			return trackID
		}
	}
	for _, candidate := range []string{
		req.ProviderTrackID,
		req.SpotifyID,
		req.TidalID,
		req.QobuzID,
		req.DeezerID,
	} {
		if trackID := strings.TrimSpace(candidate); trackID != "" {
			return trackID
		}
	}
	return ""
}

func extensionFallbackAvailabilityContext(req DownloadRequest) map[string]any {
	return map[string]any{
		"id":           req.ProviderTrackID,
		"name":         req.TrackName,
		"artists":      req.ArtistName,
		"album_name":   req.AlbumName,
		"album_artist": req.AlbumArtist,
		"cover_url":    req.CoverURL,
		"release_date": req.ReleaseDate,
		"track_number": req.TrackNumber,
		"total_tracks": req.TotalTracks,
		"disc_number":  req.DiscNumber,
		"total_discs":  req.TotalDiscs,
		"duration_ms":  req.DurationMS,
		"isrc":         req.ISRC,
		"genre":        req.Genre,
		"label":        req.Label,
		"copyright":    req.Copyright,
		"composer":     req.Composer,
		"comment":      req.Comment,
		"explicit":     req.Explicit,
		"album_type":   req.AlbumType,
		"upc":          req.UPC,
	}
}

func extensionFallbackErrorType(result *ExtDownloadResult, err error) string {
	if result != nil {
		if errorType := strings.TrimSpace(result.ErrorType); errorType != "" {
			return errorType
		}
		if errorMessage := strings.TrimSpace(result.ErrorMessage); errorMessage != "" {
			return classifyDownloadErrorType(errorMessage)
		}
	}
	if err != nil {
		return classifyDownloadErrorType(err.Error())
	}
	return "extension_error"
}

func extensionFallbackErrorMessage(result *ExtDownloadResult, err error) string {
	if result != nil {
		if message := strings.TrimSpace(result.ErrorMessage); message != "" {
			return message
		}
	}
	if err != nil {
		return err.Error()
	}
	return "extension download failed without an error message"
}

func extensionFallbackResultFromFailure(errorType, message string, retryAfterSeconds int) *ExtDownloadResult {
	return &ExtDownloadResult{
		Success:           false,
		ErrorMessage:      message,
		ErrorType:         errorType,
		RetryAfterSeconds: retryAfterSeconds,
	}
}

// DownloadWithExtensionProviderFallback executes only the isolated extension
// provider contract. Host-level metadata enrichment, output naming, embedding,
// and built-in provider fallback remain outside this method by design.
func (m *extensionManager) DownloadWithExtensionProviderFallback(
	req DownloadRequest,
	onProgress func(providerID string, percent int),
) (*ExtensionFallbackResult, error) {
	providerHint := extensionFallbackRequestProvider(req)
	providers := m.orderedExtensionDownloadProviders(providerHint, req.UseFallback)
	result := &ExtensionFallbackResult{Attempts: make([]ExtensionDownloadAttempt, 0, len(providers))}
	if len(providers) == 0 {
		result.Result = extensionFallbackResultFromFailure(
			"not_found",
			"no eligible extension download providers available",
			0,
		)
		return result, nil
	}

	sourceManifest := requestedQualityManifest(req, m)
	var lastResult *ExtDownloadResult
	var lastProvider string
	var lastFallbackUsed bool

	for index, provider := range providers {
		if isDownloadCancelled(req.ItemID) {
			return nil, ErrDownloadCancelled
		}
		ext := provider.extension
		providerID := ext.ID
		attempt := ExtensionDownloadAttempt{ProviderID: providerID}
		isSelectedProvider := providerHint != "" && strings.EqualFold(providerHint, providerID)

		trackID := extensionFallbackTrackID(req, nil)
		var preparedContext map[string]any
		// A provider-specific source ID can be used directly for the selected
		// provider. Fallback providers first get a chance to resolve their own ID.
		if !isSelectedProvider || trackID == "" {
			availability, err := provider.CheckAvailabilityForItemID(
				req.ISRC,
				req.TrackName,
				req.ArtistName,
				req.SpotifyID,
				req.DeezerID,
				req.TidalID,
				req.QobuzID,
				req.DurationMS,
				req.ItemID,
				extensionFallbackAvailabilityContext(req),
			)
			if errors.Is(err, ErrDownloadCancelled) || errors.Is(err, ErrExtensionRequestCancelled) {
				return nil, err
			}
			if err != nil {
				attempt.Error = extensionFallbackErrorMessage(nil, err)
				attempt.ErrorType = extensionFallbackErrorType(nil, err)
				attempt.StoppedProviderRun = strings.EqualFold(attempt.ErrorType, "verification_required") || ext.Manifest.StopsProviderFallback()
				result.Attempts = append(result.Attempts, attempt)
				lastResult = extensionFallbackResultFromFailure(attempt.ErrorType, attempt.Error, 0)
				lastProvider = providerID
				lastFallbackUsed = index > 0
				if attempt.StoppedProviderRun {
					result.Stopped = true
					break
				}
				continue
			}

			if availability != nil {
				attempt.Available = availability.Available
				preparedContext = availability.PreparedContext
				if !availability.Available {
					reason := strings.TrimSpace(availability.Reason)
					// Older extensions may omit checkAvailability. If the host
					// supplied a usable ID, preserve compatibility and try download.
					if trackID == "" || !strings.EqualFold(reason, "not implemented") {
						if reason == "" {
							reason = "provider reported track unavailable"
						}
						attempt.Error = reason
						attempt.ErrorType = "not_found"
						attempt.StoppedProviderRun = availability.SkipFallback || ext.Manifest.StopsProviderFallback()
						result.Attempts = append(result.Attempts, attempt)
						lastResult = extensionFallbackResultFromFailure(attempt.ErrorType, attempt.Error, 0)
						lastProvider = providerID
						lastFallbackUsed = index > 0
						if attempt.StoppedProviderRun {
							result.Stopped = true
							break
						}
						continue
					}
				}
				trackID = extensionFallbackTrackID(req, availability)
			}
		}

		if trackID == "" {
			attempt.Error = "provider did not resolve a track ID"
			attempt.ErrorType = "not_found"
			attempt.StoppedProviderRun = ext.Manifest.StopsProviderFallback()
			result.Attempts = append(result.Attempts, attempt)
			lastResult = extensionFallbackResultFromFailure(attempt.ErrorType, attempt.Error, 0)
			lastProvider = providerID
			lastFallbackUsed = index > 0
			if attempt.StoppedProviderRun {
				result.Stopped = true
				break
			}
			continue
		}

		quality := strings.TrimSpace(req.Quality)
		if quality != "" && ext.Manifest != nil {
			resolvedQuality, err := resolveExtensionDownloadQuality(quality, sourceManifest, ext.Manifest)
			if err != nil {
				attempt.TrackID = trackID
				attempt.Error = err.Error()
				attempt.ErrorType = "quality_unavailable"
				attempt.StoppedProviderRun = ext.Manifest.StopsProviderFallback()
				result.Attempts = append(result.Attempts, attempt)
				lastResult = extensionFallbackResultFromFailure(attempt.ErrorType, attempt.Error, 0)
				lastProvider = providerID
				lastFallbackUsed = index > 0
				if attempt.StoppedProviderRun {
					result.Stopped = true
					break
				}
				continue
			}
			quality = resolvedQuality
		}

		attempt.TrackID = trackID
		providerResult, err := provider.DownloadPrepared(
			trackID,
			quality,
			req.OutputPath,
			req.ItemID,
			preparedContext,
			func(percent int) {
				if onProgress != nil {
					onProgress(providerID, percent)
				}
			},
		)
		if errors.Is(err, ErrDownloadCancelled) || errors.Is(err, ErrExtensionRequestCancelled) {
			return nil, err
		}

		attempt.Success = err == nil && providerResult != nil && providerResult.Success
		attempt.ErrorType = extensionFallbackErrorType(providerResult, err)
		if !attempt.Success {
			attempt.Error = extensionFallbackErrorMessage(providerResult, err)
			if providerResult != nil {
				attempt.RetryAfterSeconds = providerResult.RetryAfterSeconds
			}
			attempt.StoppedProviderRun = strings.EqualFold(attempt.ErrorType, "verification_required") || ext.Manifest.StopsProviderFallback()
		}
		result.Attempts = append(result.Attempts, attempt)
		lastResult = providerResult
		if lastResult == nil {
			lastResult = extensionFallbackResultFromFailure(attempt.ErrorType, attempt.Error, attempt.RetryAfterSeconds)
		}
		lastProvider = providerID
		lastFallbackUsed = index > 0

		if attempt.Success {
			result.Success = true
			result.ProviderID = providerID
			result.FallbackUsed = index > 0
			result.Result = providerResult
			return result, nil
		}
		if attempt.StoppedProviderRun {
			result.Stopped = true
			break
		}
	}

	result.ProviderID = lastProvider
	result.FallbackUsed = lastFallbackUsed
	result.Result = lastResult
	if result.Result == nil {
		result.Result = extensionFallbackResultFromFailure(
			"extension_error",
			"all extension download providers failed",
			0,
		)
	}
	return result, nil
}

// DownloadExtensionProviderFallback is the bridge-friendly alias used by native
// hosts. It keeps the longer method name available to Go callers that want to
// make the provider-only boundary explicit.
func DownloadExtensionProviderFallback(req DownloadRequest, onProgress func(providerID string, percent int)) (*ExtensionFallbackResult, error) {
	return getExtensionManager().DownloadWithExtensionProviderFallback(req, onProgress)
}
