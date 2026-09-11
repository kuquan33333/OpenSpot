package gobackend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"
)

const (
	transferCheckpointVersion = 1
	transferCheckpointBytes   = 8 * 1024 * 1024
	transferCheckpointPeriod  = 5 * time.Second
)

type transferCheckpoint struct {
	Version     int    `json:"version"`
	Fingerprint string `json:"fingerprint"`
	Validator   string `json:"validator"`
	Bytes       int64  `json:"bytes"`
	Total       int64  `json:"total,omitempty"`
	UpdatedAt   int64  `json:"updated_at"`
}

type transferFailure struct {
	ErrorType         string
	Message           string
	HTTPStatus        int
	RetryAfterSeconds int
	Attempts          int
}

func (r *extensionRuntime) jsTransferError(failure transferFailure) goja.Value {
	values := map[string]any{
		"success":    false,
		"error":      failure.Message,
		"error_type": failure.ErrorType,
		"attempts":   failure.Attempts,
	}
	if failure.HTTPStatus > 0 {
		values["http_status"] = failure.HTTPStatus
	}
	if failure.RetryAfterSeconds > 0 {
		values["retry_after_seconds"] = failure.RetryAfterSeconds
	}
	return r.vm.ToValue(values)
}

func transferURLFingerprint(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	// Query strings commonly contain short-lived CDN credentials. Excluding
	// them both avoids persisting a secret-derived value and permits a freshly
	// signed URL for the same object to continue a validator-protected partial.
	identity := strings.ToLower(parsed.Scheme) + "://" +
		strings.ToLower(parsed.Host) + parsed.EscapedPath()
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:])
}

func transferCheckpointPath(stagedPath string) string {
	return stagedPath + ".checkpoint.json"
}

func loadTransferCheckpoint(path, fingerprint string) (transferCheckpoint, bool) {
	var checkpoint transferCheckpoint
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &checkpoint) != nil {
		return transferCheckpoint{}, false
	}
	if checkpoint.Version != transferCheckpointVersion ||
		checkpoint.Fingerprint == "" ||
		checkpoint.Fingerprint != fingerprint ||
		checkpoint.Validator == "" ||
		checkpoint.Bytes <= 0 {
		return transferCheckpoint{}, false
	}
	return checkpoint, true
}

func saveTransferCheckpoint(path string, checkpoint transferCheckpoint) error {
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

func transferResponseValidator(headers http.Header) string {
	if etag := strings.TrimSpace(headers.Get("ETag")); etag != "" && !strings.HasPrefix(strings.ToUpper(etag), "W/") {
		return etag
	}
	return strings.TrimSpace(headers.Get("Last-Modified"))
}

func transferTotalLength(resp *http.Response, rangeFrom int64) int64 {
	if resp == nil {
		return 0
	}
	if resp.StatusCode == http.StatusPartialContent {
		contentRange := resp.Header.Get("Content-Range")
		if slash := strings.LastIndex(contentRange, "/"); slash >= 0 {
			if total, err := strconv.ParseInt(contentRange[slash+1:], 10, 64); err == nil {
				return total
			}
		}
		if resp.ContentLength > 0 {
			return rangeFrom + resp.ContentLength
		}
		return 0
	}
	return resp.ContentLength
}

func validTransferContentRange(resp *http.Response, rangeFrom int64) bool {
	if rangeFrom <= 0 || resp == nil || resp.StatusCode != http.StatusPartialContent {
		return true
	}
	want := fmt.Sprintf("bytes %d-", rangeFrom)
	return strings.HasPrefix(resp.Header.Get("Content-Range"), want)
}

func retryableTransferStatus(status int) bool {
	return status == http.StatusRequestTimeout ||
		status == http.StatusTooEarly ||
		status == http.StatusTooManyRequests ||
		status >= 500
}

func retryAfterSeconds(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	delay := getRetryAfterDuration(resp)
	if delay <= 0 {
		return 0
	}
	seconds := int(delay.Round(time.Second) / time.Second)
	if seconds < 1 {
		return 1
	}
	return seconds
}

func transferErrorTypeForStatus(status int, policy DownloadTransferPolicy) string {
	if status == http.StatusTooManyRequests {
		return "rate_limited"
	}
	if policy.RefreshStreamOnStatus[status] {
		return "expired_stream"
	}
	if status >= 500 || status == http.StatusRequestTimeout || status == http.StatusTooEarly {
		return "transient_network"
	}
	return "http_error"
}

func waitTransferRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	return sleepRetry(ctx, delay)
}

func transferRetryConfig(policy DownloadTransferPolicy) RetryConfig {
	return RetryConfig{
		MaxRetries:    max(0, policy.MaxAttempts-1),
		InitialDelay:  policy.InitialRetryDelay,
		MaxDelay:      policy.MaxRetryDelay,
		BackoffFactor: 2,
	}
}

func (r *extensionRuntime) reliableFileDownload(
	client *http.Client,
	urlStr string,
	fullPath string,
	headers map[string]string,
	onProgress goja.Callable,
	trackItemBytes bool,
	resumeDownload bool,
	persistentCheckpoint bool,
	policy DownloadTransferPolicy,
) goja.Value {
	unlock := lockDownloadOutputPath(fullPath)
	defer unlock()

	callerSetRange := false
	for key := range headers {
		if strings.EqualFold(key, "Range") {
			callerSetRange = true
			break
		}
	}
	if callerSetRange {
		// A caller-defined range describes a standalone output fragment. It
		// cannot safely be combined with a checkpoint owned by this engine.
		resumeDownload = false
		persistentCheckpoint = false
	}

	stagedPath := stagedDownloadPath(fullPath)
	checkpointPath := transferCheckpointPath(stagedPath)
	fingerprint := transferURLFingerprint(urlStr)
	keepPartial := resumeDownload && persistentCheckpoint && fingerprint != ""
	checkpoint, checkpointOK := loadTransferCheckpoint(checkpointPath, fingerprint)
	if !keepPartial || !checkpointOK {
		os.Remove(stagedPath)
		os.Remove(checkpointPath)
		checkpoint = transferCheckpoint{}
	}

	out, err := os.OpenFile(stagedPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return r.jsTransferError(transferFailure{
			ErrorType: "storage_error",
			Message:   fmt.Sprintf("failed to create staged file: %v", err),
		})
	}
	promoted := false
	defer func() {
		out.Close()
		if promoted {
			os.Remove(checkpointPath)
			return
		}
		if !keepPartial {
			os.Remove(stagedPath)
			os.Remove(checkpointPath)
		}
	}()

	var written int64
	var validator string
	var contentLength int64
	if checkpointOK {
		if info, statErr := out.Stat(); statErr == nil {
			written = min(checkpoint.Bytes, info.Size())
			validator = checkpoint.Validator
			contentLength = checkpoint.Total
			if truncateErr := out.Truncate(written); truncateErr != nil {
				return r.jsTransferError(transferFailure{
					ErrorType: "storage_error",
					Message:   fmt.Sprintf("failed to restore partial download: %v", truncateErr),
				})
			}
		}
	}
	if _, err := out.Seek(written, io.SeekStart); err != nil {
		return r.jsTransferError(transferFailure{
			ErrorType: "storage_error",
			Message:   fmt.Sprintf("failed to seek staged file: %v", err),
		})
	}

	activeItemID := r.getActiveDownloadItemID()
	if activeItemID != "" {
		SetItemDownloading(activeItemID)
	}
	shouldTrackItemBytes := activeItemID != "" && trackItemBytes
	itemProgressReporter := NewItemTransferProgressReporter(activeItemID, written, contentLength)
	if shouldTrackItemBytes {
		if contentLength > 0 {
			SetItemProgress(activeItemID, float64(written)/float64(contentLength), written, contentLength)
		} else if written > 0 {
			SetItemBytesReceived(activeItemID, written)
		}
	}
	if checkpointOK && written > 0 && contentLength > 0 && written == contentLength {
		// The process may have died after the last durable checkpoint but
		// before the atomic rename. Publish that already-complete staged file
		// without issuing an unsatisfiable Range request at EOF.
		if err := out.Sync(); err != nil {
			return r.jsTransferError(transferFailure{
				ErrorType: "storage_error",
				Message:   fmt.Sprintf("failed to sync restored download: %v", err),
			})
		}
		if err := out.Close(); err != nil {
			return r.jsTransferError(transferFailure{
				ErrorType: "storage_error",
				Message:   fmt.Sprintf("failed to close restored download: %v", err),
			})
		}
		if err := os.Rename(stagedPath, fullPath); err != nil {
			return r.jsTransferError(transferFailure{
				ErrorType: "storage_error",
				Message:   fmt.Sprintf("failed to publish restored download: %v", err),
			})
		}
		promoted = true
		os.Remove(checkpointPath)
		syncDir(filepath.Dir(fullPath))
		if shouldTrackItemBytes {
			SetItemProgress(activeItemID, 1, written, contentLength)
		}
		return r.jsSuccess(map[string]any{
			"path":     fullPath,
			"size":     written,
			"attempts": 0,
			"resumed":  true,
		})
	}

	config := transferRetryConfig(policy)
	retryDelay := config.InitialDelay
	var lastFailure transferFailure
	attemptsUsed := 0
	var lastProgressNotify int64
	lastCheckpointBytes := written
	lastCheckpointAt := time.Now()
	saveCheckpoint := func() {
		if !keepPartial || validator == "" || written <= 0 {
			return
		}
		// Rate-limit checkpoint attempts too: a transient storage failure must
		// not turn every subsequent 64 KiB network read into another fsync.
		lastCheckpointBytes = written
		lastCheckpointAt = time.Now()
		// Persist data before the pointer to it. After a power loss, a valid
		// checkpoint must never advertise bytes that were only in page cache.
		if syncErr := out.Sync(); syncErr != nil {
			return
		}
		_ = saveTransferCheckpoint(checkpointPath, transferCheckpoint{
			Fingerprint: fingerprint,
			Validator:   validator,
			Bytes:       written,
			Total:       contentLength,
		})
	}

	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		attemptsUsed = attempt
		rangeFrom := int64(0)
		if resumeDownload && written > 0 && validator != "" {
			rangeFrom = written
		}

		req, requestErr := http.NewRequest("GET", urlStr, nil)
		if requestErr != nil {
			return r.jsTransferError(transferFailure{
				ErrorType: "invalid_request",
				Message:   requestErr.Error(),
				Attempts:  attempt,
			})
		}
		req = r.bindDownloadCancelContext(req)
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		if req.Header.Get("User-Agent") == "" {
			req.Header.Set("User-Agent", appUserAgent())
		}
		if rangeFrom > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", rangeFrom))
			req.Header.Set("If-Range", validator)
		}

		retryContext := req.Context()
		req, watchdog := bindStallWatchdog(req, downloadStallTimeout)
		resp, requestErr := r.doResolutionTransfer(client, req, false)
		if requestErr != nil {
			stalled := watchdog.stalled.Load()
			watchdog.stop()
			if activeItemID != "" && isDownloadCancelled(activeItemID) {
				return r.jsTransferError(transferFailure{
					ErrorType: "cancelled",
					Message:   "download cancelled",
					Attempts:  attempt,
				})
			}
			message := requestErr.Error()
			if stalled {
				message = fmt.Sprintf(
					"download stalled: no data received for %ds (network timeout)",
					int(downloadStallTimeout.Seconds()),
				)
			}
			lastFailure = transferFailure{
				ErrorType: "transient_network",
				Message:   message,
				Attempts:  attempt,
			}
			if attempt == policy.MaxAttempts || retryContext.Err() != nil {
				return r.jsTransferError(lastFailure)
			}
			if written > 0 && (!resumeDownload || validator == "") {
				if err := out.Truncate(0); err != nil {
					return r.jsTransferError(transferFailure{
						ErrorType: "storage_error",
						Message:   fmt.Sprintf("failed to restart transfer: %v", err),
						Attempts:  attempt,
					})
				}
				written = 0
				if _, err := out.Seek(0, io.SeekStart); err != nil {
					return r.jsTransferError(transferFailure{
						ErrorType: "storage_error",
						Message:   fmt.Sprintf("failed to seek restarted transfer: %v", err),
						Attempts:  attempt,
					})
				}
			}
			if err := waitTransferRetry(retryContext, retryDelay); err != nil {
				return r.jsTransferError(transferFailure{
					ErrorType: "cancelled",
					Message:   "download cancelled",
					Attempts:  attempt,
				})
			}
			retryDelay = calculateNextDelay(retryDelay, config)
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			retryAfter := retryAfterSeconds(resp)
			io.Copy(io.Discard, io.LimitReader(resp.Body, 32*1024))
			resp.Body.Close()
			watchdog.stop()
			errorType := transferErrorTypeForStatus(resp.StatusCode, policy)
			lastFailure = transferFailure{
				ErrorType:         errorType,
				Message:           fmt.Sprintf("HTTP error: %d", resp.StatusCode),
				HTTPStatus:        resp.StatusCode,
				RetryAfterSeconds: retryAfter,
				Attempts:          attempt,
			}
			if !retryableTransferStatus(resp.StatusCode) || attempt == policy.MaxAttempts {
				return r.jsTransferError(lastFailure)
			}
			delay := retryDelay
			if retryAfter > 0 {
				delay = time.Duration(retryAfter) * time.Second
			}
			if err := waitTransferRetry(retryContext, delay); err != nil {
				return r.jsTransferError(transferFailure{
					ErrorType: "cancelled",
					Message:   "download cancelled",
					Attempts:  attempt,
				})
			}
			retryDelay = calculateNextDelay(retryDelay, config)
			continue
		}

		if rangeFrom > 0 && resp.StatusCode == http.StatusPartialContent &&
			!validTransferContentRange(resp, rangeFrom) {
			contentRange := resp.Header.Get("Content-Range")
			resp.Body.Close()
			watchdog.stop()
			return r.jsTransferError(transferFailure{
				ErrorType: "integrity_failed",
				Message: fmt.Sprintf(
					"resume failed: unexpected Content-Range %q at %d bytes",
					contentRange,
					rangeFrom,
				),
				HTTPStatus: resp.StatusCode,
				Attempts:   attempt,
			})
		}
		if rangeFrom > 0 && resp.StatusCode == http.StatusPartialContent {
			nextValidator := transferResponseValidator(resp.Header)
			if nextValidator != "" && validator != "" && nextValidator != validator {
				resp.Body.Close()
				watchdog.stop()
				return r.jsTransferError(transferFailure{
					ErrorType:  "integrity_failed",
					Message:    "resume failed: response validator changed",
					HTTPStatus: resp.StatusCode,
					Attempts:   attempt,
				})
			}
		}
		if rangeFrom > 0 && resp.StatusCode == http.StatusOK {
			if err := out.Truncate(0); err != nil {
				resp.Body.Close()
				watchdog.stop()
				return r.jsTransferError(transferFailure{
					ErrorType: "storage_error",
					Message:   fmt.Sprintf("failed to restart changed transfer: %v", err),
					Attempts:  attempt,
				})
			}
			if _, err := out.Seek(0, io.SeekStart); err != nil {
				resp.Body.Close()
				watchdog.stop()
				return r.jsTransferError(transferFailure{
					ErrorType: "storage_error",
					Message:   fmt.Sprintf("failed to seek restarted transfer: %v", err),
					Attempts:  attempt,
				})
			}
			written = 0
			rangeFrom = 0
		}

		if nextValidator := transferResponseValidator(resp.Header); nextValidator != "" {
			validator = nextValidator
		}
		contentLength = transferTotalLength(resp, rangeFrom)
		if callerSetRange {
			// The caller asked file.download to materialize only this range; the
			// response length, not the complete object's Content-Range total, is
			// therefore the integrity boundary for the output file.
			contentLength = resp.ContentLength
		}
		if shouldTrackItemBytes && contentLength > 0 {
			itemProgressReporter.Report(written, contentLength)
		}
		var readErr error
		buffer := make([]byte, 64*1024)
		for {
			readCount, bodyErr := resp.Body.Read(buffer)
			if readCount > 0 {
				watchdog.reset()
				writeCount, writeErr := out.Write(buffer[:readCount])
				written += int64(writeCount)
				if writeErr != nil || writeCount != readCount {
					resp.Body.Close()
					watchdog.stop()
					if writeErr == nil {
						writeErr = io.ErrShortWrite
					}
					return r.jsTransferError(transferFailure{
						ErrorType: "storage_error",
						Message:   fmt.Sprintf("failed to write staged file: %v", writeErr),
						Attempts:  attempt,
					})
				}
				if shouldTrackItemBytes {
					itemProgressReporter.Report(written, contentLength)
				}
				if onProgress != nil && contentLength > 0 &&
					(written-lastProgressNotify >= progressUpdateThreshold || written >= contentLength) {
					lastProgressNotify = written
					_, _ = onProgress(
						goja.Undefined(),
						r.vm.ToValue(written),
						r.vm.ToValue(contentLength),
					)
				}
				if keepPartial && validator != "" &&
					(written-lastCheckpointBytes >= transferCheckpointBytes ||
						time.Since(lastCheckpointAt) >= transferCheckpointPeriod) {
					saveCheckpoint()
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

		if readErr == nil && contentLength > 0 && written != contentLength {
			readErr = io.ErrUnexpectedEOF
		}
		if readErr == nil {
			break
		}

		saveCheckpoint()
		message := fmt.Sprintf("failed to read response: %v", readErr)
		if stalled {
			message = fmt.Sprintf(
				"download stalled: no data received for %ds (network timeout)",
				int(downloadStallTimeout.Seconds()),
			)
		}
		lastFailure = transferFailure{
			ErrorType: "transient_network",
			Message:   message,
			Attempts:  attempt,
		}
		if attempt == policy.MaxAttempts ||
			(activeItemID != "" && isDownloadCancelled(activeItemID)) {
			if activeItemID != "" && isDownloadCancelled(activeItemID) {
				lastFailure.ErrorType = "cancelled"
				lastFailure.Message = "download cancelled"
			}
			return r.jsTransferError(lastFailure)
		}

		if !resumeDownload || validator == "" {
			if err := out.Truncate(0); err != nil {
				return r.jsTransferError(transferFailure{
					ErrorType: "storage_error",
					Message:   fmt.Sprintf("failed to restart transfer: %v", err),
					Attempts:  attempt,
				})
			}
			if _, err := out.Seek(0, io.SeekStart); err != nil {
				return r.jsTransferError(transferFailure{
					ErrorType: "storage_error",
					Message:   fmt.Sprintf("failed to seek restarted transfer: %v", err),
					Attempts:  attempt,
				})
			}
			written = 0
			validator = ""
			os.Remove(checkpointPath)
		}
		if err := waitTransferRetry(retryContext, retryDelay); err != nil {
			return r.jsTransferError(transferFailure{
				ErrorType: "cancelled",
				Message:   "download cancelled",
				Attempts:  attempt,
			})
		}
		retryDelay = calculateNextDelay(retryDelay, config)
	}

	if written <= 0 {
		return r.jsTransferError(transferFailure{
			ErrorType: "integrity_failed",
			Message:   "download response was empty",
			Attempts:  attemptsUsed,
		})
	}
	if contentLength > 0 && written != contentLength {
		return r.jsTransferError(transferFailure{
			ErrorType: "integrity_failed",
			Message: fmt.Sprintf(
				"download size mismatch: expected %d bytes, wrote %d",
				contentLength,
				written,
			),
			Attempts: attemptsUsed,
		})
	}
	if err := out.Sync(); err != nil {
		return r.jsTransferError(transferFailure{
			ErrorType: "storage_error",
			Message:   fmt.Sprintf("failed to sync staged file: %v", err),
		})
	}
	if err := out.Close(); err != nil {
		return r.jsTransferError(transferFailure{
			ErrorType: "storage_error",
			Message:   fmt.Sprintf("failed to finalize staged file: %v", err),
		})
	}
	if err := os.Rename(stagedPath, fullPath); err != nil {
		return r.jsTransferError(transferFailure{
			ErrorType: "storage_error",
			Message:   fmt.Sprintf("failed to publish file: %v", err),
		})
	}
	promoted = true
	os.Remove(checkpointPath)
	syncDir(filepath.Dir(fullPath))
	if shouldTrackItemBytes {
		if contentLength > 0 {
			SetItemProgress(activeItemID, 1, written, contentLength)
		} else if written > 0 {
			SetItemBytesReceived(activeItemID, written)
		}
	}

	GoLog(
		"[Extension:%s] Reliable transfer downloaded %d bytes to %s\n",
		r.extensionID,
		written,
		fullPath,
	)
	return r.jsSuccess(map[string]any{
		"path":     fullPath,
		"size":     written,
		"attempts": attemptsUsed,
	})
}
