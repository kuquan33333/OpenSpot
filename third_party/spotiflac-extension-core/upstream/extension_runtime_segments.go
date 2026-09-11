package gobackend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
)

type segmentTransferSpec struct {
	Index   int
	URL     string
	Headers map[string]string
}

type segmentTransferResult struct {
	Index    int
	Path     string
	Size     int64
	Attempts int
	Failure  *transferFailure
}

type segmentTransferCheckpoint struct {
	Version     int    `json:"version"`
	Fingerprint string `json:"fingerprint"`
	NextIndex   int    `json:"next_index"`
	Bytes       int64  `json:"bytes"`
	UpdatedAt   int64  `json:"updated_at"`
}

func parseStringHeaders(value any) map[string]string {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	headers := make(map[string]string, len(raw))
	for key, entry := range raw {
		headers[key] = fmt.Sprintf("%v", entry)
	}
	return headers
}

func mergeStringHeaders(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	merged := make(map[string]string, len(base)+len(override))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range override {
		merged[key] = value
	}
	return merged
}

func parseSegmentTransferSpecs(value any, commonHeaders map[string]string) ([]segmentTransferSpec, error) {
	rawSegments, ok := value.([]any)
	if !ok || len(rawSegments) == 0 {
		return nil, fmt.Errorf("segments must be a non-empty array")
	}
	segments := make([]segmentTransferSpec, 0, len(rawSegments))
	for index, raw := range rawSegments {
		var rawURL string
		var headers map[string]string
		switch typed := raw.(type) {
		case string:
			rawURL = typed
		case map[string]any:
			rawURL, _ = typed["url"].(string)
			headers = parseStringHeaders(typed["headers"])
		default:
			return nil, fmt.Errorf("segment %d must be a URL string or object", index)
		}
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			return nil, fmt.Errorf("segment %d URL is empty", index)
		}
		segments = append(segments, segmentTransferSpec{
			Index:   index,
			URL:     rawURL,
			Headers: mergeStringHeaders(commonHeaders, headers),
		})
	}
	return segments, nil
}

func segmentListFingerprint(segments []segmentTransferSpec) string {
	hash := sha256.New()
	for _, segment := range segments {
		// Segmented checkpoints have no per-segment ETag or Last-Modified
		// validator. Include the complete URL, including its query, so a
		// different media object served from the same CDN path can never reuse
		// already-assembled bytes. Ordinary single-file checkpoints may ignore
		// rotating query credentials because their validator still protects
		// integrity.
		hash.Write([]byte(segment.URL))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func loadSegmentCheckpoint(path, fingerprint string) (segmentTransferCheckpoint, bool) {
	var checkpoint segmentTransferCheckpoint
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &checkpoint) != nil {
		return segmentTransferCheckpoint{}, false
	}
	if checkpoint.Version != transferCheckpointVersion ||
		checkpoint.Fingerprint != fingerprint ||
		checkpoint.NextIndex < 0 || checkpoint.Bytes < 0 ||
		(checkpoint.NextIndex == 0 && checkpoint.Bytes != 0) ||
		(checkpoint.NextIndex > 0 && checkpoint.Bytes == 0) {
		return segmentTransferCheckpoint{}, false
	}
	return checkpoint, true
}

func saveSegmentCheckpoint(path string, checkpoint segmentTransferCheckpoint) error {
	checkpoint.Version = transferCheckpointVersion
	checkpoint.UpdatedAt = time.Now().UnixMilli()
	data, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	tempPath := path + ".tmp"
	file, err := os.OpenFile(tempPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(tempPath)
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		os.Remove(tempPath)
		return err
	}
	return nil
}

func segmentTempPath(stagedPath string, index int) string {
	return fmt.Sprintf("%s.segment.%06d", stagedPath, index)
}

func (r *extensionRuntime) fetchSegmentToTemp(
	ctx context.Context,
	client *http.Client,
	spec segmentTransferSpec,
	tempPath string,
	policy DownloadTransferPolicy,
	received *atomic.Int64,
	completed *atomic.Bool,
	itemProgressReporter *ItemTransferProgressReporter,
) segmentTransferResult {
	config := transferRetryConfig(policy)
	retryDelay := config.InitialDelay
	var lastFailure transferFailure

	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		if ctx.Err() != nil {
			return segmentTransferResult{
				Index: spec.Index,
				Failure: &transferFailure{
					ErrorType: "cancelled",
					Message:   "download cancelled",
					Attempts:  attempt,
				},
			}
		}
		os.Remove(tempPath)
		output, err := os.OpenFile(tempPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			return segmentTransferResult{
				Index: spec.Index,
				Failure: &transferFailure{
					ErrorType: "storage_error",
					Message:   fmt.Sprintf("failed to create segment file: %v", err),
					Attempts:  attempt,
				},
			}
		}

		req, err := http.NewRequestWithContext(ctx, "GET", spec.URL, nil)
		if err != nil {
			output.Close()
			return segmentTransferResult{
				Index: spec.Index,
				Failure: &transferFailure{
					ErrorType: "invalid_request",
					Message:   err.Error(),
					Attempts:  attempt,
				},
			}
		}
		for key, value := range spec.Headers {
			req.Header.Set(key, value)
		}
		if req.Header.Get("User-Agent") == "" {
			req.Header.Set("User-Agent", appUserAgent())
		}
		req, watchdog := bindStallWatchdog(req, downloadStallTimeout)
		resp, err := r.doResolutionTransfer(client, req, attempt == 1 && completed.Load())
		if err != nil {
			stalled := watchdog.stalled.Load()
			watchdog.stop()
			output.Close()
			message := err.Error()
			if stalled {
				message = fmt.Sprintf(
					"segment %d stalled for %ds",
					spec.Index,
					int(downloadStallTimeout.Seconds()),
				)
			}
			lastFailure = transferFailure{
				ErrorType: "transient_network",
				Message:   message,
				Attempts:  attempt,
			}
			if attempt == policy.MaxAttempts || ctx.Err() != nil {
				if ctx.Err() != nil {
					lastFailure.ErrorType = "cancelled"
					lastFailure.Message = "download cancelled"
				}
				return segmentTransferResult{Index: spec.Index, Failure: &lastFailure}
			}
			if r.waitResolutionRetry(ctx, retryDelay) != nil {
				lastFailure.ErrorType = "cancelled"
				lastFailure.Message = "download cancelled"
				return segmentTransferResult{Index: spec.Index, Failure: &lastFailure}
			}
			retryDelay = calculateNextDelay(retryDelay, config)
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			retryAfter := retryAfterSeconds(resp)
			io.Copy(io.Discard, io.LimitReader(resp.Body, 32*1024))
			resp.Body.Close()
			watchdog.stop()
			output.Close()
			lastFailure = transferFailure{
				ErrorType:         transferErrorTypeForStatus(resp.StatusCode, policy),
				Message:           fmt.Sprintf("segment %d HTTP error: %d", spec.Index, resp.StatusCode),
				HTTPStatus:        resp.StatusCode,
				RetryAfterSeconds: retryAfter,
				Attempts:          attempt,
			}
			if !retryableTransferStatus(resp.StatusCode) || attempt == policy.MaxAttempts {
				return segmentTransferResult{Index: spec.Index, Failure: &lastFailure}
			}
			delay := retryDelay
			if retryAfter > 0 {
				delay = time.Duration(retryAfter) * time.Second
			}
			if r.waitResolutionRetry(ctx, delay) != nil {
				lastFailure.ErrorType = "cancelled"
				lastFailure.Message = "download cancelled"
				return segmentTransferResult{Index: spec.Index, Failure: &lastFailure}
			}
			retryDelay = calculateNextDelay(retryDelay, config)
			continue
		}

		buffer := make([]byte, 64*1024)
		var size int64
		var readErr error
		for {
			readCount, bodyErr := resp.Body.Read(buffer)
			if readCount > 0 {
				watchdog.reset()
				writeCount, writeErr := output.Write(buffer[:readCount])
				size += int64(writeCount)
				received.Add(int64(writeCount))
				itemProgressReporter.Report(received.Load(), 0)
				if writeErr != nil || writeCount != readCount {
					if writeErr == nil {
						writeErr = io.ErrShortWrite
					}
					readErr = writeErr
					break
				}
			}
			if bodyErr != nil {
				if bodyErr != io.EOF {
					readErr = bodyErr
				}
				break
			}
		}
		resp.Body.Close()
		stalled := watchdog.stalled.Load()
		watchdog.stop()
		closeErr := output.Close()
		if readErr == nil {
			readErr = closeErr
		}
		if readErr == nil && resp.ContentLength > 0 && size != resp.ContentLength {
			readErr = io.ErrUnexpectedEOF
		}
		if readErr == nil && size > 0 {
			completed.Store(true)
			return segmentTransferResult{
				Index:    spec.Index,
				Path:     tempPath,
				Size:     size,
				Attempts: attempt,
			}
		}
		if size > 0 {
			received.Add(-size)
		}

		message := fmt.Sprintf("failed to read segment %d: %v", spec.Index, readErr)
		if size == 0 && readErr == nil {
			message = fmt.Sprintf("segment %d response was empty", spec.Index)
		}
		if stalled {
			message = fmt.Sprintf(
				"segment %d stalled for %ds",
				spec.Index,
				int(downloadStallTimeout.Seconds()),
			)
		}
		lastFailure = transferFailure{
			ErrorType: "transient_network",
			Message:   message,
			Attempts:  attempt,
		}
		if attempt == policy.MaxAttempts {
			return segmentTransferResult{Index: spec.Index, Failure: &lastFailure}
		}
		if r.waitResolutionRetry(ctx, retryDelay) != nil {
			lastFailure.ErrorType = "cancelled"
			lastFailure.Message = "download cancelled"
			return segmentTransferResult{Index: spec.Index, Failure: &lastFailure}
		}
		retryDelay = calculateNextDelay(retryDelay, config)
	}

	return segmentTransferResult{
		Index: spec.Index,
		Failure: &transferFailure{
			ErrorType: "transient_network",
			Message:   fmt.Sprintf("segment %d exhausted retry budget", spec.Index),
			Attempts:  policy.MaxAttempts,
		},
	}
}

func (r *extensionRuntime) fileDownloadSegments(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 2 {
		return r.jsTransferError(transferFailure{
			ErrorType: "invalid_request",
			Message:   "segments and output path are required",
		})
	}

	var commonHeaders map[string]string
	var onProgress goja.Callable
	var maxParallelOption int
	var maxAttemptsOption int
	var persistentCheckpointOption *bool
	if len(call.Arguments) > 2 &&
		!goja.IsUndefined(call.Arguments[2]) &&
		!goja.IsNull(call.Arguments[2]) {
		if options, ok := call.Arguments[2].Export().(map[string]any); ok {
			commonHeaders = parseStringHeaders(options["headers"])
			if progressValue, ok := options["onProgress"]; ok {
				if callable, ok := goja.AssertFunction(r.vm.ToValue(progressValue)); ok {
					onProgress = callable
				}
			}
			maxParallelOption = capabilityInt(options["maxParallel"], 0)
			maxAttemptsOption = capabilityInt(options["maxAttempts"], 0)
			if checkpoint, ok := options["persistentCheckpoint"].(bool); ok {
				persistentCheckpointOption = &checkpoint
			}
		}
	}

	segments, err := parseSegmentTransferSpecs(call.Arguments[0].Export(), commonHeaders)
	if err != nil {
		return r.jsTransferError(transferFailure{
			ErrorType: "invalid_request",
			Message:   err.Error(),
		})
	}
	for _, segment := range segments {
		if err := r.validateDomain(segment.URL); err != nil {
			return r.jsTransferError(transferFailure{
				ErrorType: "permission",
				Message:   err.Error(),
			})
		}
	}
	fullPath, err := r.validatePath(call.Arguments[1].String())
	if err != nil {
		return r.jsTransferError(transferFailure{
			ErrorType: "permission",
			Message:   err.Error(),
		})
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return r.jsTransferError(transferFailure{
			ErrorType: "storage_error",
			Message:   fmt.Sprintf("failed to create output directory: %v", err),
		})
	}

	policy := r.manifest.DownloadTransferPolicy()
	if maxParallelOption > 0 {
		policy.MaxParallelSegments = clampInt(maxParallelOption, 1, maxParallelSegments)
	}
	if maxAttemptsOption > 0 {
		policy.MaxAttempts = clampInt(maxAttemptsOption, 1, 8)
	}
	persistentCheckpoint := policy.PersistentCheckpoint
	if persistentCheckpointOption != nil {
		persistentCheckpoint = *persistentCheckpointOption
	}

	client := r.downloadClient
	if client == nil {
		client = r.httpClient
	}
	unlock := lockDownloadOutputPath(fullPath)
	defer unlock()

	stagedPath := stagedDownloadPath(fullPath)
	checkpointPath := transferCheckpointPath(stagedPath) + ".segments"
	fingerprint := segmentListFingerprint(segments)
	checkpoint, checkpointOK := loadSegmentCheckpoint(checkpointPath, fingerprint)
	if !persistentCheckpoint || !checkpointOK || checkpoint.NextIndex > len(segments) {
		checkpoint = segmentTransferCheckpoint{Fingerprint: fingerprint}
		checkpointOK = false
		os.Remove(stagedPath)
		os.Remove(checkpointPath)
	}

	output, err := os.OpenFile(stagedPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return r.jsTransferError(transferFailure{
			ErrorType: "storage_error",
			Message:   fmt.Sprintf("failed to create segmented output: %v", err),
		})
	}
	promoted := false
	defer func() {
		output.Close()
		for index := range segments {
			os.Remove(segmentTempPath(stagedPath, index))
		}
		if promoted {
			os.Remove(checkpointPath)
		} else if !persistentCheckpoint {
			os.Remove(stagedPath)
			os.Remove(checkpointPath)
		}
	}()

	nextIndex := 0
	var totalWritten int64
	if checkpointOK {
		if info, statErr := output.Stat(); statErr == nil && info.Size() >= checkpoint.Bytes {
			totalWritten = checkpoint.Bytes
			nextIndex = checkpoint.NextIndex
		} else {
			// Segment boundaries cannot be reconstructed from a shorter file.
			// Restart instead of skipping segments named by a stale checkpoint.
			checkpointOK = false
			checkpoint = segmentTransferCheckpoint{Fingerprint: fingerprint}
			os.Remove(checkpointPath)
		}
	}
	if err := output.Truncate(totalWritten); err != nil {
		return r.jsTransferError(transferFailure{
			ErrorType: "storage_error",
			Message:   fmt.Sprintf("failed to restore segmented output: %v", err),
		})
	}
	if _, err := output.Seek(totalWritten, io.SeekStart); err != nil {
		return r.jsTransferError(transferFailure{
			ErrorType: "storage_error",
			Message:   fmt.Sprintf("failed to seek segmented output: %v", err),
		})
	}

	activeItemID := r.getActiveDownloadItemID()
	if activeItemID != "" {
		SetItemDownloading(activeItemID)
		SetItemProgress(
			activeItemID,
			float64(nextIndex)/float64(len(segments)),
			totalWritten,
			0,
		)
	}
	if nextIndex == len(segments) && totalWritten > 0 {
		if err := output.Sync(); err != nil {
			return r.jsTransferError(transferFailure{
				ErrorType: "storage_error",
				Message:   fmt.Sprintf("failed to sync restored segmented output: %v", err),
			})
		}
		if err := output.Close(); err != nil {
			return r.jsTransferError(transferFailure{
				ErrorType: "storage_error",
				Message:   fmt.Sprintf("failed to close restored segmented output: %v", err),
			})
		}
		if err := os.Rename(stagedPath, fullPath); err != nil {
			return r.jsTransferError(transferFailure{
				ErrorType: "storage_error",
				Message:   fmt.Sprintf("failed to publish restored segmented output: %v", err),
			})
		}
		promoted = true
		os.Remove(checkpointPath)
		syncDir(filepath.Dir(fullPath))
		if activeItemID != "" {
			SetItemProgress(activeItemID, 1, totalWritten, totalWritten)
		}
		return r.jsSuccess(map[string]any{
			"path":     fullPath,
			"size":     totalWritten,
			"segments": len(segments),
			"resumed":  true,
		})
	}

	baseRequest, requestErr := http.NewRequest("GET", segments[nextIndex].URL, nil)
	if requestErr != nil {
		return r.jsTransferError(transferFailure{
			ErrorType: "invalid_request",
			Message:   requestErr.Error(),
		})
	}
	baseRequest = r.bindDownloadCancelContext(baseRequest)
	ctx, cancel := context.WithCancel(baseRequest.Context())
	defer cancel()

	jobs := make(chan segmentTransferSpec)
	results := make(chan segmentTransferResult, policy.MaxParallelSegments)
	var received atomic.Int64
	var completed atomic.Bool
	received.Store(totalWritten)
	itemProgressReporter := NewItemTransferProgressReporter(activeItemID, totalWritten, 0)
	var workers sync.WaitGroup
	workerCount := min(policy.MaxParallelSegments, len(segments)-nextIndex)
	for workerIndex := 0; workerIndex < workerCount; workerIndex++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for spec := range jobs {
				result := r.fetchSegmentToTemp(
					ctx,
					client,
					spec,
					segmentTempPath(stagedPath, spec.Index),
					policy,
					&received,
					&completed,
					itemProgressReporter,
				)
				select {
				case results <- result:
				case <-ctx.Done():
					return
				}
				if result.Failure != nil {
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for index := nextIndex; index < len(segments); index++ {
			select {
			case jobs <- segments[index]:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()

	pending := make(map[int]segmentTransferResult)
	completedSegments := nextIndex
	lastCheckpointBytes := totalWritten
	lastCheckpointAt := time.Now()
	var firstFailure *transferFailure
	for result := range results {
		if result.Failure != nil {
			if firstFailure == nil {
				failureCopy := *result.Failure
				firstFailure = &failureCopy
				cancel()
			}
			continue
		}
		pending[result.Index] = result
		for {
			ready, ok := pending[nextIndex]
			if !ok {
				break
			}
			segmentFile, openErr := os.Open(ready.Path)
			if openErr != nil {
				firstFailure = &transferFailure{
					ErrorType: "storage_error",
					Message:   fmt.Sprintf("failed to open downloaded segment %d: %v", nextIndex, openErr),
					Attempts:  ready.Attempts,
				}
				cancel()
				break
			}
			copied, copyErr := io.CopyBuffer(output, segmentFile, make([]byte, 128*1024))
			segmentFile.Close()
			if copyErr != nil || copied != ready.Size {
				if copyErr == nil {
					copyErr = io.ErrShortWrite
				}
				firstFailure = &transferFailure{
					ErrorType: "storage_error",
					Message:   fmt.Sprintf("failed to append segment %d: %v", nextIndex, copyErr),
					Attempts:  ready.Attempts,
				}
				cancel()
				break
			}
			totalWritten += copied
			os.Remove(ready.Path)
			delete(pending, nextIndex)
			nextIndex++
			completedSegments++
			if persistentCheckpoint &&
				(totalWritten-lastCheckpointBytes >= transferCheckpointBytes ||
					time.Since(lastCheckpointAt) >= transferCheckpointPeriod) {
				if output.Sync() == nil && saveSegmentCheckpoint(
					checkpointPath,
					segmentTransferCheckpoint{
						Fingerprint: fingerprint,
						NextIndex:   nextIndex,
						Bytes:       totalWritten,
					},
				) == nil {
					lastCheckpointBytes = totalWritten
					lastCheckpointAt = time.Now()
				}
			}
			if activeItemID != "" {
				SetItemProgress(
					activeItemID,
					float64(completedSegments)/float64(len(segments)),
					received.Load(),
					0,
				)
			}
			if onProgress != nil {
				func() {
					if b := r.currentResolutionBudget(); b != nil {
						defer b.charge()()
					}
					_, _ = onProgress(
						goja.Undefined(),
						r.vm.ToValue(received.Load()),
						r.vm.ToValue(int64(0)),
						r.vm.ToValue(completedSegments),
						r.vm.ToValue(len(segments)),
					)
				}()
			}
		}
	}
	if firstFailure != nil {
		if persistentCheckpoint && nextIndex > 0 && totalWritten > 0 && output.Sync() == nil {
			_ = saveSegmentCheckpoint(checkpointPath, segmentTransferCheckpoint{
				Fingerprint: fingerprint,
				NextIndex:   nextIndex,
				Bytes:       totalWritten,
			})
		}
		return r.jsTransferError(*firstFailure)
	}
	if nextIndex != len(segments) || totalWritten <= 0 {
		return r.jsTransferError(transferFailure{
			ErrorType: "integrity_failed",
			Message: fmt.Sprintf(
				"segmented transfer incomplete: assembled %d of %d segments",
				nextIndex,
				len(segments),
			),
		})
	}
	if err := output.Sync(); err != nil {
		return r.jsTransferError(transferFailure{
			ErrorType: "storage_error",
			Message:   fmt.Sprintf("failed to sync segmented output: %v", err),
		})
	}
	if err := output.Close(); err != nil {
		return r.jsTransferError(transferFailure{
			ErrorType: "storage_error",
			Message:   fmt.Sprintf("failed to close segmented output: %v", err),
		})
	}
	if err := os.Rename(stagedPath, fullPath); err != nil {
		return r.jsTransferError(transferFailure{
			ErrorType: "storage_error",
			Message:   fmt.Sprintf("failed to publish segmented output: %v", err),
		})
	}
	promoted = true
	os.Remove(checkpointPath)
	syncDir(filepath.Dir(fullPath))
	if activeItemID != "" {
		SetItemProgress(activeItemID, 1, totalWritten, totalWritten)
	}
	return r.jsSuccess(map[string]any{
		"path":     fullPath,
		"size":     totalWritten,
		"segments": len(segments),
	})
}
