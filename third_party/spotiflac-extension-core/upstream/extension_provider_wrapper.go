package gobackend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dop251/goja"
)

type extensionProviderWrapper struct {
	extension *loadedExtension
	vm        *goja.Runtime
}

func newExtensionProviderWrapper(ext *loadedExtension) *extensionProviderWrapper {
	return &extensionProviderWrapper{
		extension: ext,
		vm:        ext.VM,
	}
}

func (p *extensionProviderWrapper) lockReadyVM() error {
	vm, err := p.extension.lockReadyVM()
	if err != nil {
		return err
	}
	p.vm = vm
	return nil
}

// extCallOpts configures a shared extension invocation. It covers the
// skeleton common to most extensionProviderWrapper methods: perf tracking, VM
// locking, optional download/request cancellation binding, and translating
// timeouts/cancellation into the right error.
type extCallOpts struct {
	perfName  string
	invoke    func(vm *goja.Runtime) (goja.Value, error)
	timeout   time.Duration
	itemID    string          // optional: binds download-cancel + active-item tracking
	requestID string          // optional: binds request-cancel via context (customSearch only)
	context   context.Context // optional: caller lifecycle for non-download work
	// beforeRun runs after lock+cancel setup, right before the invocation. Its
	// returned cleanup, if any, runs after the call.
	beforeRun func() func()
	// timeoutMessage overrides the default "<perfName> timeout: extension took
	// too long to respond".
	timeoutMessage string
	// rawError returns non-timeout script errors unwrapped instead of
	// "<perfName> failed: %w".
	rawError bool
}

// callExtension locks the extension's VM, runs opts.invoke, and hands
// the raw result to parse while the VM lock is still held. parse is where
// each caller does its type-specific parsing, perf.recordParse/setItems, and
// any ProviderID stamping.
func callExtension[T any](p *extensionProviderWrapper, opts extCallOpts, parse func(perf *extensionCallPerf, result goja.Value) (T, error)) (T, error) {
	var zero T
	ctx := opts.context
	if ctx == nil {
		ctx = context.Background()
	}

	perf := newExtensionCallPerf(p.extension.ID, opts.perfName)
	defer perf.finish()
	initStartedAt := time.Now()
	if err := p.lockReadyVM(); err != nil {
		return zero, err
	}
	perf.recordInit(time.Since(initStartedAt))
	defer p.extension.VMMu.Unlock()

	if opts.itemID != "" {
		if p.extension.runtime != nil {
			p.extension.runtime.setActiveDownloadItemID(opts.itemID)
			defer p.extension.runtime.clearActiveDownloadItemID()
		}
		ctx = initDownloadCancel(opts.itemID)
		defer clearDownloadCancel(opts.itemID)
		if isDownloadCancelled(opts.itemID) {
			return zero, ErrDownloadCancelled
		}
	}

	if opts.requestID != "" {
		if p.extension.runtime != nil {
			p.extension.runtime.setActiveRequestID(opts.requestID)
			defer p.extension.runtime.clearActiveRequestID()
		}
		ctx = initExtensionRequestCancel(opts.requestID)
		defer clearExtensionRequestCancel(opts.requestID)
		if isExtensionRequestCancelled(opts.requestID) {
			return zero, ErrExtensionRequestCancelled
		}
	}

	if opts.beforeRun != nil {
		if cleanup := opts.beforeRun(); cleanup != nil {
			defer cleanup()
		}
	}

	jsStartedAt := time.Now()
	result, err := runGojaCallWithTimeoutContextAndRecover(ctx, p.vm, func() (goja.Value, error) {
		result, err := opts.invoke(p.vm)
		if err != nil {
			err = p.normalizePendingVerificationError(err)
		}
		return result, err
	}, opts.timeout)
	perf.recordJS(time.Since(jsStartedAt))
	perf.recordPayload(result)
	if err != nil {
		if IsRuntimeUnsafeError(err) {
			quarantineRuntimeLocked(p.extension, p.vm, err)
		}
		if opts.requestID != "" && isExtensionRequestCancelled(opts.requestID) {
			return zero, ErrExtensionRequestCancelled
		}
		if opts.itemID != "" && isDownloadCancelled(opts.itemID) {
			return zero, ErrDownloadCancelled
		}
		if opts.requestID != "" && errors.Is(err, ErrExtensionRequestCancelled) {
			return zero, ErrExtensionRequestCancelled
		}
		if IsTimeoutError(err) {
			if opts.timeoutMessage != "" {
				return zero, errors.New(opts.timeoutMessage)
			}
			return zero, fmt.Errorf("%s timeout: extension took too long to respond", opts.perfName)
		}
		if opts.rawError {
			return zero, err
		}
		return zero, fmt.Errorf("%s failed: %w", opts.perfName, err)
	}
	if opts.itemID != "" && isDownloadCancelled(opts.itemID) {
		return zero, ErrDownloadCancelled
	}
	if opts.requestID != "" && isExtensionRequestCancelled(opts.requestID) {
		return zero, ErrExtensionRequestCancelled
	}

	return parse(perf, result)
}

func (p *extensionProviderWrapper) normalizePendingVerificationError(err error) error {
	var exception *goja.Exception
	if !errors.As(err, &exception) {
		return err
	}
	// A script may rethrow an earlier challenge after the per-call runtime
	// marker was cleared. Only trust an existing, fresh challenge for this
	// extension; the exception alone must not start verification.
	pending := GetPendingAuthRequest(p.extension.ID)
	if pending == nil || pending.ExtensionID != p.extension.ID || strings.TrimSpace(pending.AuthURL) == "" {
		return err
	}
	if age := time.Since(pending.CreatedAt); age < 0 || age >= pendingAuthRequestTTL {
		return err
	}
	value := exception.Value()
	if gojaValueIsEmpty(value) {
		return err
	}
	var message string
	if extractionErr := p.vm.Try(func() {
		if object, ok := value.(*goja.Object); ok {
			if field := object.Get("message"); !gojaValueIsEmpty(field) {
				message = field.String()
			}
		} else {
			message = value.String()
		}
	}); extractionErr != nil || strings.TrimSpace(message) != "VERIFY_REQUIRED" {
		return err
	}
	return fmt.Errorf("verification_required: extension '%s' needs signed-session verification: %w", p.extension.ID, err)
}

func invokeExtensionMethod(vm *goja.Runtime, method string, args ...any) (goja.Value, error) {
	extensionValue := vm.Get("extension")
	if gojaValueIsEmpty(extensionValue) {
		return goja.Null(), nil
	}

	extensionObject := extensionValue.ToObject(vm)
	callable, ok := goja.AssertFunction(extensionObject.Get(method))
	if !ok {
		return goja.Null(), nil
	}

	return callable(extensionObject, gojaArgumentValues(vm, args)...)
}

func gojaArgumentValues(vm *goja.Runtime, args []any) []goja.Value {
	values := make([]goja.Value, len(args))
	for i, arg := range args {
		values[i] = gojaArgumentValue(vm, arg)
	}
	return values
}

func gojaArgumentValue(vm *goja.Runtime, value any) goja.Value {
	switch typed := value.(type) {
	case goja.Value:
		return typed
	case map[string]any:
		obj := vm.NewObject()
		for key, child := range typed {
			_ = obj.Set(key, gojaArgumentValue(vm, child))
		}
		return obj
	case map[string]string:
		obj := vm.NewObject()
		for key, child := range typed {
			_ = obj.Set(key, child)
		}
		return obj
	case []any:
		children := make([]any, len(typed))
		for i, child := range typed {
			children[i] = gojaArgumentValue(vm, child)
		}
		return vm.NewArray(children...)
	case []string:
		children := make([]any, len(typed))
		for i, child := range typed {
			children[i] = child
		}
		return vm.NewArray(children...)
	default:
		return vm.ToValue(value)
	}
}

func extensionMethodInvocation(method string, args ...any) func(*goja.Runtime) (goja.Value, error) {
	return func(vm *goja.Runtime) (goja.Value, error) {
		return invokeExtensionMethod(vm, method, args...)
	}
}

func hasExtensionMethod(vm *goja.Runtime, method string) bool {
	extensionValue := vm.Get("extension")
	if gojaValueIsEmpty(extensionValue) {
		return false
	}
	_, ok := goja.AssertFunction(extensionValue.ToObject(vm).Get(method))
	return ok
}

func invokeExtensionOrGlobal(vm *goja.Runtime, method string, args ...any) (goja.Value, error) {
	if hasExtensionMethod(vm, method) {
		return invokeExtensionMethod(vm, method, args...)
	}
	callable, ok := goja.AssertFunction(vm.Get(method))
	if !ok {
		return goja.Null(), nil
	}
	return callable(vm.GlobalObject(), gojaArgumentValues(vm, args)...)
}

func extensionTrackInput(track *ExtTrackMetadata) map[string]any {
	if track == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":             track.ID,
		"name":           track.Name,
		"artists":        track.Artists,
		"album_name":     track.AlbumName,
		"album_artist":   track.AlbumArtist,
		"album_id":       track.AlbumID,
		"album_url":      track.AlbumURL,
		"artist_id":      track.ArtistID,
		"artist_url":     track.ArtistURL,
		"external_urls":  track.ExternalURL,
		"duration_ms":    track.DurationMS,
		"cover_url":      track.CoverURL,
		"preview_url":    track.PreviewURL,
		"images":         track.Images,
		"release_date":   track.ReleaseDate,
		"track_number":   track.TrackNumber,
		"total_tracks":   track.TotalTracks,
		"disc_number":    track.DiscNumber,
		"total_discs":    track.TotalDiscs,
		"isrc":           track.ISRC,
		"provider_id":    track.ProviderID,
		"item_type":      track.ItemType,
		"album_type":     track.AlbumType,
		"explicit":       track.Explicit,
		"tidal_id":       track.TidalID,
		"qobuz_id":       track.QobuzID,
		"deezer_id":      track.DeezerID,
		"spotify_id":     track.SpotifyID,
		"external_links": track.ExternalLinks,
		"label":          track.Label,
		"copyright":      track.Copyright,
		"genre":          track.Genre,
		"composer":       track.Composer,
		"comment":        track.Comment,
		"audio_quality":  track.AudioQuality,
		"audio_modes":    track.AudioModes,
		"upc":            track.UPC,
	}
}

func (p *extensionProviderWrapper) SearchTracks(query string, limit int) (*ExtSearchResult, error) {
	return p.SearchTracksForItemID(query, limit, "")
}

func (p *extensionProviderWrapper) SearchTracksForItemID(query string, limit int, itemID string) (*ExtSearchResult, error) {
	if !p.extension.Manifest.IsMetadataProvider() {
		return nil, fmt.Errorf("extension '%s' is not a metadata provider", p.extension.ID)
	}
	if !p.extension.Enabled {
		return nil, fmt.Errorf("extension '%s' is disabled", p.extension.ID)
	}

	return callExtension(p, extCallOpts{
		perfName: "searchTracks",
		invoke:   extensionMethodInvocation("searchTracks", query, limit),
		timeout:  DefaultJSTimeout,
		itemID:   itemID,
	}, func(perf *extensionCallPerf, result goja.Value) (*ExtSearchResult, error) {
		if result == nil || goja.IsUndefined(result) || goja.IsNull(result) {
			return nil, fmt.Errorf("searchTracks returned null")
		}
		parseStartedAt := time.Now()
		searchResult, err := parseExtensionSearchResult(p.vm, result)
		perf.recordParse(time.Since(parseStartedAt))
		if err != nil {
			return nil, fmt.Errorf("failed to parse search result: %w", err)
		}
		perf.setItems(len(searchResult.Tracks))

		for i := range searchResult.Tracks {
			searchResult.Tracks[i].ProviderID = p.extension.ID
		}

		return &searchResult, nil
	})
}

func (p *extensionProviderWrapper) GetTrack(trackID string) (*ExtTrackMetadata, error) {
	if !p.extension.Manifest.IsMetadataProvider() {
		return nil, fmt.Errorf("extension '%s' is not a metadata provider", p.extension.ID)
	}
	if !p.extension.Enabled {
		return nil, fmt.Errorf("extension '%s' is disabled", p.extension.ID)
	}

	return callExtension(p, extCallOpts{
		perfName: "getTrack",
		invoke:   extensionMethodInvocation("getTrack", trackID),
		timeout:  DefaultJSTimeout,
	}, func(perf *extensionCallPerf, result goja.Value) (*ExtTrackMetadata, error) {
		if result == nil || goja.IsUndefined(result) || goja.IsNull(result) {
			return nil, fmt.Errorf("getTrack returned null")
		}
		parseStartedAt := time.Now()
		track := parseExtensionTrackValue(p.vm, result)
		perf.recordParse(time.Since(parseStartedAt))
		perf.setItems(1)
		track.ProviderID = p.extension.ID
		return &track, nil
	})
}

func (p *extensionProviderWrapper) GetAlbum(albumID string) (*ExtAlbumMetadata, error) {
	if !p.extension.Manifest.IsMetadataProvider() {
		return nil, fmt.Errorf("extension '%s' is not a metadata provider", p.extension.ID)
	}
	if !p.extension.Enabled {
		return nil, fmt.Errorf("extension '%s' is disabled", p.extension.ID)
	}

	return callExtension(p, extCallOpts{
		perfName: "getAlbum",
		invoke:   extensionMethodInvocation("getAlbum", albumID),
		timeout:  DefaultJSTimeout,
	}, func(perf *extensionCallPerf, result goja.Value) (*ExtAlbumMetadata, error) {
		if result == nil || goja.IsUndefined(result) || goja.IsNull(result) {
			return nil, fmt.Errorf("getAlbum returned null")
		}
		parseStartedAt := time.Now()
		album, err := parseExtensionAlbumValue(p.vm, result)
		perf.recordParse(time.Since(parseStartedAt))
		if err != nil {
			return nil, fmt.Errorf("failed to parse album: %w", err)
		}
		perf.setItems(len(album.Tracks))

		album.ProviderID = p.extension.ID
		for i := range album.Tracks {
			album.Tracks[i].ProviderID = p.extension.ID
		}
		return &album, nil
	})
}

func (p *extensionProviderWrapper) GetPlaylist(playlistID string) (*ExtAlbumMetadata, error) {
	if !p.extension.Manifest.IsMetadataProvider() {
		return nil, fmt.Errorf("extension '%s' is not a metadata provider", p.extension.ID)
	}
	if !p.extension.Enabled {
		return nil, fmt.Errorf("extension '%s' is disabled", p.extension.ID)
	}

	return callExtension(p, extCallOpts{
		perfName: "getPlaylist",
		invoke: func(vm *goja.Runtime) (goja.Value, error) {
			if hasExtensionMethod(vm, "getPlaylist") {
				return invokeExtensionMethod(vm, "getPlaylist", playlistID)
			}
			return invokeExtensionMethod(vm, "getAlbum", playlistID)
		},
		timeout: DefaultJSTimeout,
	}, func(perf *extensionCallPerf, result goja.Value) (*ExtAlbumMetadata, error) {
		if result == nil || goja.IsUndefined(result) || goja.IsNull(result) {
			return nil, fmt.Errorf("getPlaylist returned null")
		}
		parseStartedAt := time.Now()
		playlist, err := parseExtensionAlbumValue(p.vm, result)
		perf.recordParse(time.Since(parseStartedAt))
		if err != nil {
			return nil, fmt.Errorf("failed to parse playlist: %w", err)
		}
		perf.setItems(len(playlist.Tracks))

		playlist.ProviderID = p.extension.ID
		for i := range playlist.Tracks {
			playlist.Tracks[i].ProviderID = p.extension.ID
		}
		return &playlist, nil
	})
}

func (p *extensionProviderWrapper) GetArtist(artistID string) (*ExtArtistMetadata, error) {
	if !p.extension.Manifest.IsMetadataProvider() {
		return nil, fmt.Errorf("extension '%s' is not a metadata provider", p.extension.ID)
	}
	if !p.extension.Enabled {
		return nil, fmt.Errorf("extension '%s' is disabled", p.extension.ID)
	}

	return callExtension(p, extCallOpts{
		perfName: "getArtist",
		invoke:   extensionMethodInvocation("getArtist", artistID),
		timeout:  DefaultJSTimeout,
	}, func(perf *extensionCallPerf, result goja.Value) (*ExtArtistMetadata, error) {
		if result == nil || goja.IsUndefined(result) || goja.IsNull(result) {
			return nil, fmt.Errorf("getArtist returned null")
		}
		parseStartedAt := time.Now()
		artist, err := parseExtensionArtistValue(p.vm, result)
		perf.recordParse(time.Since(parseStartedAt))
		if err != nil {
			return nil, fmt.Errorf("failed to parse artist: %w", err)
		}
		perf.setItems(len(artist.Albums) + len(artist.Releases) + len(artist.TopTracks))

		artist.ProviderID = p.extension.ID
		for i := range artist.Releases {
			artist.Releases[i].ProviderID = p.extension.ID
			for j := range artist.Releases[i].Tracks {
				artist.Releases[i].Tracks[j].ProviderID = p.extension.ID
			}
		}
		return &artist, nil
	})
}

func (p *extensionProviderWrapper) EnrichTrack(track *ExtTrackMetadata) (*ExtTrackMetadata, error) {
	return p.EnrichTrackForItemID(track, "")
}

// EnrichTrackForItemID is excluded from the shared callExtension helper:
// unlike the other providers it must return the original track (not an error)
// on every failure path, which doesn't fit the helper's error-returning shape.
func (p *extensionProviderWrapper) EnrichTrackForItemID(track *ExtTrackMetadata, itemID string) (*ExtTrackMetadata, error) {
	if !p.extension.Manifest.IsMetadataProvider() {
		return track, nil
	}

	if !p.extension.Enabled {
		return track, nil
	}
	perf := newExtensionCallPerf(p.extension.ID, "enrichTrack")
	defer perf.finish()
	initStartedAt := time.Now()
	if err := p.lockReadyVM(); err != nil {
		GoLog("[Extension] EnrichTrack init error for %s: %v\n", p.extension.ID, err)
		return track, nil
	}
	perf.recordInit(time.Since(initStartedAt))
	defer p.extension.VMMu.Unlock()
	downloadCtx := context.Background()
	if itemID != "" {
		if p.extension.runtime != nil {
			p.extension.runtime.setActiveDownloadItemID(itemID)
			defer p.extension.runtime.clearActiveDownloadItemID()
		}
		downloadCtx = initDownloadCancel(itemID)
		defer clearDownloadCancel(itemID)
		if isDownloadCancelled(itemID) {
			return track, ErrDownloadCancelled
		}
	}

	jsStartedAt := time.Now()
	result, err := runGojaCallWithTimeoutContextAndRecover(downloadCtx, p.vm, func() (goja.Value, error) {
		return invokeExtensionMethod(p.vm, "enrichTrack", extensionTrackInput(track))
	}, DefaultJSTimeout)
	perf.recordJS(time.Since(jsStartedAt))
	perf.recordPayload(result)
	if err != nil {
		if IsRuntimeUnsafeError(err) {
			quarantineRuntimeLocked(p.extension, p.vm, err)
		}
		if isDownloadCancelled(itemID) {
			return track, ErrDownloadCancelled
		}
		if IsTimeoutError(err) {
			GoLog("[Extension] EnrichTrack timeout for %s\n", p.extension.ID)
		} else {
			GoLog("[Extension] EnrichTrack error for %s: %v\n", p.extension.ID, err)
		}
		return track, nil
	}
	if isDownloadCancelled(itemID) {
		return track, ErrDownloadCancelled
	}

	if result == nil || goja.IsUndefined(result) || goja.IsNull(result) {
		return track, nil
	}

	parseStartedAt := time.Now()
	enrichedTrack := parseExtensionTrackValue(p.vm, result)
	perf.recordParse(time.Since(parseStartedAt))
	perf.setItems(1)
	enrichedTrack.ProviderID = track.ProviderID

	return &enrichedTrack, nil
}

func (p *extensionProviderWrapper) CheckAvailabilityForItemID(isrc, trackName, artistName, spotifyID, deezerID, tidalID, qobuzID string, durationMS int, itemID string, trackContexts ...map[string]any) (*ExtAvailabilityResult, error) {
	if !p.extension.Manifest.IsDownloadProvider() {
		return nil, fmt.Errorf("extension '%s' is not a download provider", p.extension.ID)
	}
	if !p.extension.Enabled {
		return nil, fmt.Errorf("extension '%s' is disabled", p.extension.ID)
	}

	availabilityOptions := map[string]any{
		"spotify_id":  spotifyID,
		"deezer_id":   deezerID,
		"tidal_id":    tidalID,
		"qobuz_id":    qobuzID,
		"duration_ms": durationMS,
	}
	if len(trackContexts) > 0 && len(trackContexts[0]) > 0 {
		availabilityOptions["track"] = trackContexts[0]
	}

	var availabilityRuntime *extensionRuntime
	consumeVerificationError := func() error {
		if availabilityRuntime != nil && availabilityRuntime.consumeVerificationRequired() != "" {
			return fmt.Errorf(
				"verification_required: extension '%s' needs signed-session verification",
				p.extension.ID,
			)
		}
		return nil
	}

	return callExtension(p, extCallOpts{
		perfName: "checkAvailability",
		invoke: func(vm *goja.Runtime) (goja.Value, error) {
			result, err := invokeExtensionMethod(vm, "checkAvailability", isrc, trackName, artistName, availabilityOptions)
			// A thrown JS error must preserve the same canonical runtime evidence
			// as a returned unavailable result. Cancellation and timeout still
			// take precedence in callExtension.
			if err != nil {
				if verificationErr := consumeVerificationError(); verificationErr != nil {
					return nil, verificationErr
				}
			}
			return result, err
		},
		timeout: DefaultJSTimeout,
		itemID:  itemID,
		beforeRun: func() func() {
			// Drop any stale flag so the post-run check below only sees
			// verification requested by THIS call.
			availabilityRuntime = p.extension.runtime
			if availabilityRuntime != nil {
				availabilityRuntime.consumeVerificationRequired()
			}
			return nil
		},
	}, func(perf *extensionCallPerf, result goja.Value) (*ExtAvailabilityResult, error) {
		if result == nil || goja.IsUndefined(result) || goja.IsNull(result) {
			if err := consumeVerificationError(); err != nil {
				return nil, err
			}
			return &ExtAvailabilityResult{Available: false, Reason: "not implemented"}, nil
		}
		parseStartedAt := time.Now()
		availability := parseExtensionAvailabilityValue(p.vm, result)
		perf.recordParse(time.Since(parseStartedAt))
		perf.setItems(1)
		// A signed-session call inside checkAvailability required
		// verification. Extensions often swallow that and report a plain
		// "not available", which would silently skip this provider's
		// challenge; surface it as an error so the fallback loop pauses and
		// opens the challenge instead.
		if !availability.Available {
			if err := consumeVerificationError(); err != nil {
				return nil, err
			}
		}
		return &availability, nil
	})
}

const ExtDownloadTimeout = DownloadTimeout

// Download is excluded from the shared callExtension helper: it runs in
// an isolated VM/runtime (not p.vm/p.extension.VMMu) with a progress
// callback, which the helper's lock+perf model doesn't cover.
func (p *extensionProviderWrapper) Download(trackID, quality, outputPath, itemID string, onProgress func(percent int)) (*ExtDownloadResult, error) {
	return p.DownloadPrepared(
		trackID,
		quality,
		outputPath,
		itemID,
		nil,
		onProgress,
	)
}

// DownloadPrepared passes the opaque context returned by checkAvailability to
// the isolated download runtime. Existing extensions remain compatible because
// JavaScript ignores the additional options argument; extensions that opt in
// can reuse already-resolved metadata or stream preparation.
func (p *extensionProviderWrapper) DownloadPrepared(
	trackID, quality, outputPath, itemID string,
	preparedContext map[string]any,
	onProgress func(percent int),
) (*ExtDownloadResult, error) {
	if !p.extension.Manifest.IsDownloadProvider() {
		return nil, fmt.Errorf("extension '%s' is not a download provider", p.extension.ID)
	}

	if !p.extension.Enabled {
		return nil, fmt.Errorf("extension '%s' is disabled", p.extension.ID)
	}
	perf := newExtensionCallPerf(p.extension.ID, "download")
	defer perf.finish()
	initStartedAt := time.Now()
	vm, runtime, err := acquireIsolatedExtensionRuntime(p.extension)
	perf.recordInit(time.Since(initStartedAt))
	if err != nil {
		return &ExtDownloadResult{
			Success:      false,
			ErrorMessage: err.Error(),
			ErrorType:    "init_error",
		}, nil
	}
	vmHealthy := false
	cleanupSafe := true
	var unsafeDone <-chan struct{}
	defer func() {
		releaseIsolatedExtensionRuntime(
			p.extension,
			vm,
			runtime,
			vmHealthy,
			cleanupSafe,
			unsafeDone,
		)
	}()
	if runtime != nil {
		runtime.setActiveDownloadItemID(itemID)
		defer runtime.clearActiveDownloadItemID()
	}
	downloadCtx := context.Background()
	if itemID != "" {
		downloadCtx = initDownloadCancel(itemID)
		defer clearDownloadCancel(itemID)
		SetItemPreparing(itemID)
	}

	if runtime != nil {
		var finishResolution func()
		downloadCtx, finishResolution = runtime.beginResolutionBudget(downloadCtx, extensionResolutionTimeout)
		defer finishResolution()
	}

	progressCallback := vm.ToValue(func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) > 0 {
			percent := int(call.Arguments[0].ToInteger())
			if percent < 0 {
				percent = 0
			}
			if percent > 100 {
				percent = 100
			}
			if onProgress != nil {
				onProgress(percent)
			}
		}
		return goja.Undefined()
	})

	if runtime != nil {
		// Drop any stale flag (pooled runtimes survive across downloads) so
		// the post-run check only sees verification from THIS call.
		runtime.consumeVerificationRequired()
	}

	jsStartedAt := time.Now()
	downloadOptions := map[string]any{"resolutionTimeoutMs": extensionResolutionTimeout.Milliseconds()}
	if len(preparedContext) > 0 {
		downloadOptions["preparedContext"] = preparedContext
	}
	result, err := runGojaCallWithTimeoutContextAndRecover(downloadCtx, vm, func() (goja.Value, error) {
		return invokeExtensionMethod(
			vm,
			"download",
			trackID,
			quality,
			outputPath,
			progressCallback,
			downloadOptions,
		)
	}, ExtDownloadTimeout)
	perf.recordJS(time.Since(jsStartedAt))
	perf.recordPayload(result)
	vmHealthy = err == nil
	cleanupSafe = !IsRuntimeUnsafeError(err)
	unsafeDone = runtimeCompletion(err)
	if err != nil {
		if itemID != "" && isDownloadCancelled(itemID) {
			return nil, ErrDownloadCancelled
		}
		errMsg := err.Error()
		errType := "script_error"
		if IsTimeoutError(err) {
			errMsg = "download timeout: extension took too long to complete"
			if context.Cause(downloadCtx) == context.DeadlineExceeded {
				errMsg = "stream resolution timeout: extension took too long to resolve an audio stream"
			}
			errType = "timeout"
		}
		return &ExtDownloadResult{
			Success:      false,
			ErrorMessage: errMsg,
			ErrorType:    errType,
		}, nil
	}

	if result == nil || goja.IsUndefined(result) || goja.IsNull(result) {
		return &ExtDownloadResult{
			Success:      false,
			ErrorMessage: "download returned null",
			ErrorType:    "not_implemented",
		}, nil
	}

	parseStartedAt := time.Now()
	downloadResult := parseExtensionDownloadResultValue(vm, result)
	perf.recordParse(time.Since(parseStartedAt))
	perf.setItems(1)
	downloadResult.Decryption = normalizeDownloadDecryptionInfo(
		downloadResult.Decryption,
		downloadResult.DecryptionKey,
	)
	downloadResult.DecryptionKey = normalizedDownloadDecryptionKey(
		downloadResult.Decryption,
		downloadResult.DecryptionKey,
	)

	// A signed-session call inside download() required verification but the
	// script reported a generic failure; tag the result so the fallback loop
	// pauses and opens this provider's challenge instead of skipping it.
	if runtime != nil && !downloadResult.Success {
		if runtime.consumeVerificationRequired() != "" &&
			!strings.EqualFold(downloadResult.ErrorType, "verification_required") {
			downloadResult.ErrorType = "verification_required"
			if downloadResult.ErrorMessage == "" {
				downloadResult.ErrorMessage = "Verification required"
			}
		}
	}

	return &downloadResult, nil
}

func (p *extensionProviderWrapper) CustomSearch(query string, options map[string]any) ([]ExtTrackMetadata, error) {
	return p.customSearch(query, options, "", "")
}

func (p *extensionProviderWrapper) CustomSearchForRequestID(query string, options map[string]any, requestID string) ([]ExtTrackMetadata, error) {
	return p.customSearch(query, options, "", requestID)
}

func (p *extensionProviderWrapper) customSearch(query string, options map[string]any, itemID, requestID string) ([]ExtTrackMetadata, error) {
	if !p.extension.Manifest.HasCustomSearch() {
		return nil, fmt.Errorf("extension '%s' does not support custom search", p.extension.ID)
	}
	if !p.extension.Enabled {
		return nil, fmt.Errorf("extension '%s' is disabled", p.extension.ID)
	}
	if options == nil {
		options = map[string]any{}
	}

	return callExtension(p, extCallOpts{
		perfName:  "customSearch",
		invoke:    extensionMethodInvocation("customSearch", query, options),
		timeout:   DefaultJSTimeout,
		itemID:    itemID,
		requestID: requestID,
	}, func(perf *extensionCallPerf, result goja.Value) ([]ExtTrackMetadata, error) {
		if result == nil || goja.IsUndefined(result) || goja.IsNull(result) {
			return []ExtTrackMetadata{}, nil
		}
		parseStartedAt := time.Now()
		tracks, err := parseExtensionTrackArray(p.vm, result)
		perf.recordParse(time.Since(parseStartedAt))
		if err != nil {
			return nil, fmt.Errorf("failed to parse search result: %w", err)
		}
		perf.setItems(len(tracks))

		for i := range tracks {
			tracks[i].ProviderID = p.extension.ID
		}

		return tracks, nil
	})
}

type ExtURLHandleResult struct {
	Type string `json:"type"`
	// ID identifies the handled resource itself (e.g. a playlist ID). Track,
	// album, and artist results already carry their own ID inside their
	// nested metadata; this covers result types with no such nested object.
	ID          string             `json:"id,omitempty"`
	Track       *ExtTrackMetadata  `json:"track,omitempty"`
	Tracks      []ExtTrackMetadata `json:"tracks,omitempty"`
	Album       *ExtAlbumMetadata  `json:"album,omitempty"`
	Artist      *ExtArtistMetadata `json:"artist,omitempty"`
	Name        string             `json:"name,omitempty"`
	CoverURL    string             `json:"cover_url,omitempty"`
	HeaderImage string             `json:"header_image,omitempty"`
	HeaderVideo string             `json:"header_video,omitempty"`
}

func (p *extensionProviderWrapper) HandleURL(url string) (*ExtURLHandleResult, error) {
	if !p.extension.Manifest.HasURLHandler() {
		return nil, fmt.Errorf("extension '%s' does not support URL handling", p.extension.ID)
	}
	if !p.extension.Enabled {
		return nil, fmt.Errorf("extension '%s' is disabled", p.extension.ID)
	}

	return callExtension(p, extCallOpts{
		perfName: "handleUrl",
		invoke:   extensionMethodInvocation("handleUrl", url),
		timeout:  DefaultJSTimeout,
	}, func(perf *extensionCallPerf, result goja.Value) (*ExtURLHandleResult, error) {
		if result == nil || goja.IsUndefined(result) || goja.IsNull(result) {
			return nil, fmt.Errorf("handleUrl returned null - URL not recognized")
		}
		parseStartedAt := time.Now()
		handleResult, err := parseExtensionURLHandleValue(p.vm, result)
		perf.recordParse(time.Since(parseStartedAt))
		if err != nil {
			return nil, fmt.Errorf("failed to parse URL handle result: %w", err)
		}
		urlItems := len(handleResult.Tracks)
		if handleResult.Track != nil {
			urlItems++
		}
		if handleResult.Album != nil {
			urlItems += 1 + len(handleResult.Album.Tracks)
		}
		if handleResult.Artist != nil {
			urlItems += 1 + len(handleResult.Artist.Albums) + len(handleResult.Artist.Releases) + len(handleResult.Artist.TopTracks)
		}
		perf.setItems(urlItems)

		if handleResult.Track != nil {
			handleResult.Track.ProviderID = p.extension.ID
		}
		for i := range handleResult.Tracks {
			handleResult.Tracks[i].ProviderID = p.extension.ID
		}
		if handleResult.Album != nil {
			handleResult.Album.ProviderID = p.extension.ID
			for i := range handleResult.Album.Tracks {
				handleResult.Album.Tracks[i].ProviderID = p.extension.ID
			}
		}
		if handleResult.Artist != nil {
			handleResult.Artist.ProviderID = p.extension.ID
			for i := range handleResult.Artist.Albums {
				handleResult.Artist.Albums[i].ProviderID = p.extension.ID
				for j := range handleResult.Artist.Albums[i].Tracks {
					handleResult.Artist.Albums[i].Tracks[j].ProviderID = p.extension.ID
				}
			}
			for i := range handleResult.Artist.Releases {
				handleResult.Artist.Releases[i].ProviderID = p.extension.ID
				for j := range handleResult.Artist.Releases[i].Tracks {
					handleResult.Artist.Releases[i].Tracks[j].ProviderID = p.extension.ID
				}
			}
			for i := range handleResult.Artist.TopTracks {
				handleResult.Artist.TopTracks[i].ProviderID = p.extension.ID
			}
		}

		return &handleResult, nil
	})
}

type PostProcessResult struct {
	Success     bool   `json:"success"`
	NewFilePath string `json:"new_file_path,omitempty"`
	NewFileURI  string `json:"new_file_uri,omitempty"`
	Error       string `json:"error,omitempty"`
	BitDepth    int    `json:"bit_depth,omitempty"`
	SampleRate  int    `json:"sample_rate,omitempty"`
}

type PostProcessInput struct {
	Path     string `json:"path,omitempty"`
	URI      string `json:"uri,omitempty"`
	Name     string `json:"name,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Size     int64  `json:"size,omitempty"`
	IsSAF    bool   `json:"is_saf,omitempty"`
}

func postProcessInputMap(input PostProcessInput) map[string]any {
	result := make(map[string]any, 6)
	if input.Path != "" {
		result["path"] = input.Path
	}
	if input.URI != "" {
		result["uri"] = input.URI
	}
	if input.Name != "" {
		result["name"] = input.Name
	}
	if input.MimeType != "" {
		result["mime_type"] = input.MimeType
	}
	if input.Size != 0 {
		result["size"] = input.Size
	}
	if input.IsSAF {
		result["is_saf"] = true
	}
	return result
}

const PostProcessTimeout = 2 * time.Minute

// PostProcessV2 probes extension.postProcessV2 first, then falls back to the
// legacy extension.postProcess contract.
func (p *extensionProviderWrapper) PostProcessV2(input PostProcessInput, metadata map[string]any, hookID string) (*PostProcessResult, error) {
	if !p.extension.Manifest.HasPostProcessing() {
		return nil, fmt.Errorf("extension '%s' does not support post-processing", p.extension.ID)
	}
	if !p.extension.Enabled {
		return nil, fmt.Errorf("extension '%s' is disabled", p.extension.ID)
	}

	filePath := input.Path

	perfName := "postProcessV2"
	inputMap := postProcessInputMap(input)
	invoke := func(vm *goja.Runtime) (goja.Value, error) {
		if hasExtensionMethod(vm, "postProcessV2") {
			return invokeExtensionMethod(vm, "postProcessV2", inputMap, metadata, hookID)
		}
		return invokeExtensionMethod(vm, "postProcess", filePath, metadata, hookID)
	}

	result, err := callExtension(p, extCallOpts{
		perfName:       perfName,
		invoke:         invoke,
		timeout:        PostProcessTimeout,
		timeoutMessage: "postProcess timeout: extension took too long to complete",
		rawError:       true,
	}, func(perf *extensionCallPerf, value goja.Value) (*PostProcessResult, error) {
		if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
			return &PostProcessResult{Success: false, Error: "postProcess returned null"}, nil
		}
		parseStartedAt := time.Now()
		postResult := parseExtensionPostProcessValue(p.vm, value)
		perf.recordParse(time.Since(parseStartedAt))
		perf.setItems(1)
		return &postResult, nil
	})
	if err != nil {
		return &PostProcessResult{Success: false, Error: err.Error()}, nil
	}
	return result, nil
}

type ExtLyricsResult struct {
	Lines        []ExtLyricsLine `json:"lines"`
	SyncType     string          `json:"syncType"`
	Instrumental bool            `json:"instrumental"`
	PlainLyrics  string          `json:"plainLyrics"`
	Provider     string          `json:"provider"`
}

type ExtLyricsLine struct {
	StartTimeMs int64  `json:"startTimeMs"`
	Words       string `json:"words"`
	EndTimeMs   int64  `json:"endTimeMs"`
}

func (p *extensionProviderWrapper) FetchLyrics(trackName, artistName, albumName string, durationSec float64) (*LyricsResponse, error) {
	return p.FetchLyricsContext(context.Background(), trackName, artistName, albumName, durationSec)
}

func (p *extensionProviderWrapper) FetchLyricsContext(ctx context.Context, trackName, artistName, albumName string, durationSec float64) (*LyricsResponse, error) {
	if !p.extension.Manifest.IsLyricsProvider() {
		return nil, fmt.Errorf("extension '%s' is not a lyrics provider", p.extension.ID)
	}
	if !p.extension.Enabled {
		return nil, fmt.Errorf("extension '%s' is disabled", p.extension.ID)
	}

	return callExtension(p, extCallOpts{
		perfName: "fetchLyrics",
		invoke:   extensionMethodInvocation("fetchLyrics", trackName, artistName, albumName, durationSec),
		timeout:  DefaultJSTimeout,
		context:  ctx,
	}, func(perf *extensionCallPerf, result goja.Value) (*LyricsResponse, error) {
		if result == nil || goja.IsUndefined(result) || goja.IsNull(result) {
			return nil, fmt.Errorf("fetchLyrics returned null")
		}
		parseStartedAt := time.Now()
		extResult, err := parseExtensionLyricsValue(p.vm, result)
		perf.recordParse(time.Since(parseStartedAt))
		if err != nil {
			return nil, fmt.Errorf("failed to parse lyrics result: %w", err)
		}
		perf.setItems(len(extResult.Lines))

		response := &LyricsResponse{
			SyncType:     extResult.SyncType,
			Instrumental: extResult.Instrumental,
			PlainLyrics:  extResult.PlainLyrics,
			Provider:     extResult.Provider,
			Source:       "Extension: " + p.extension.ID,
		}

		if response.Provider == "" {
			response.Provider = p.extension.Manifest.DisplayName
		}

		for _, line := range extResult.Lines {
			response.Lines = append(response.Lines, LyricsLine(line))
		}

		if len(response.Lines) == 0 && response.PlainLyrics != "" && !response.Instrumental {
			response.SyncType = "UNSYNCED"
			for _, line := range strings.Split(response.PlainLyrics, "\n") {
				if strings.TrimSpace(line) != "" {
					response.Lines = append(response.Lines, LyricsLine{
						StartTimeMs: 0,
						Words:       line,
						EndTimeMs:   0,
					})
				}
			}
		}

		return response, nil
	})
}
