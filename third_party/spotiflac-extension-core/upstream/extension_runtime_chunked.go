package gobackend

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dop251/goja"
)

func chunkedTransferTotal(resp *http.Response) int64 {
	if resp == nil {
		return 0
	}
	if contentRange := resp.Header.Get("Content-Range"); contentRange != "" {
		if slash := strings.LastIndex(contentRange, "/"); slash >= 0 {
			var total int64
			if _, err := fmt.Sscanf(contentRange[slash+1:], "%d", &total); err == nil {
				return total
			}
		}
	}
	if resp.StatusCode == http.StatusOK {
		return resp.ContentLength
	}
	return 0
}

func (r *extensionRuntime) chunkedTransferCancelled(activeItemID string) bool {
	return activeItemID != "" && isDownloadCancelled(activeItemID)
}

// fileDownloadChunked downloads sequential byte ranges. The same transfer
// policy used by ordinary and segmented downloads controls retries and
// checkpoints here, so specialized CDN downloads do not lose the reliability
// guarantees of the generic file API.
func (r *extensionRuntime) fileDownloadChunked(
	client *http.Client,
	urlStr, fullPath string,
	headers map[string]string,
	userAgent string,
	chunkSize int64,
	onProgress goja.Callable,
	trackItemBytes bool,
	persistentCheckpoint bool,
	policy DownloadTransferPolicy,
) goja.Value {
	unlock := lockDownloadOutputPath(fullPath)
	defer unlock()

	activeItemID := r.getActiveDownloadItemID()
	if activeItemID != "" {
		SetItemDownloading(activeItemID)
	}
	config := transferRetryConfig(policy)
	var probeResp *http.Response
	var probeFailure transferFailure
	probeDelay := config.InitialDelay
	probeAttempts := 0
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		probeAttempts = attempt
		request, err := http.NewRequest("GET", urlStr, nil)
		if err != nil {
			return r.jsTransferError(transferFailure{
				ErrorType: "invalid_request",
				Message:   fmt.Sprintf("chunked probe request: %v", err),
				Attempts:  attempt,
			})
		}
		request = r.bindDownloadCancelContext(request)
		retryContext := request.Context()
		for key, value := range headers {
			if !strings.EqualFold(key, "Range") {
				request.Header.Set(key, value)
			}
		}
		request.Header.Set("User-Agent", userAgent)
		request.Header.Set("Range", "bytes=0-1")
		request, watchdog := bindStallWatchdog(request, downloadStallTimeout)
		response, requestErr := client.Do(request)
		if requestErr != nil {
			stalled := watchdog.stalled.Load()
			watchdog.stop()
			if r.chunkedTransferCancelled(activeItemID) {
				return r.jsTransferError(transferFailure{
					ErrorType: "cancelled",
					Message:   "download cancelled",
					Attempts:  attempt,
				})
			}
			message := fmt.Sprintf("chunked probe failed: %v", requestErr)
			if stalled {
				message = fmt.Sprintf(
					"chunked probe stalled for %ds",
					int(downloadStallTimeout.Seconds()),
				)
			}
			probeFailure = transferFailure{
				ErrorType: "transient_network",
				Message:   message,
				Attempts:  attempt,
			}
			if attempt == policy.MaxAttempts {
				return r.jsTransferError(probeFailure)
			}
			if waitTransferRetry(retryContext, probeDelay) != nil {
				return r.jsTransferError(transferFailure{
					ErrorType: "cancelled",
					Message:   "download cancelled",
					Attempts:  attempt,
				})
			}
			probeDelay = calculateNextDelay(probeDelay, config)
			continue
		}
		watchdog.stop()
		if response.StatusCode == http.StatusPartialContent ||
			response.StatusCode == http.StatusOK {
			io.Copy(io.Discard, io.LimitReader(response.Body, 32*1024))
			response.Body.Close()
			probeResp = response
			break
		}

		retryAfter := retryAfterSeconds(response)
		io.Copy(io.Discard, io.LimitReader(response.Body, 32*1024))
		response.Body.Close()
		probeFailure = transferFailure{
			ErrorType:         transferErrorTypeForStatus(response.StatusCode, policy),
			Message:           fmt.Sprintf("chunked probe HTTP %d", response.StatusCode),
			HTTPStatus:        response.StatusCode,
			RetryAfterSeconds: retryAfter,
			Attempts:          attempt,
		}
		if !retryableTransferStatus(response.StatusCode) || attempt == policy.MaxAttempts {
			return r.jsTransferError(probeFailure)
		}
		delay := probeDelay
		if retryAfter > 0 {
			delay = time.Duration(retryAfter) * time.Second
		}
		if waitTransferRetry(retryContext, delay) != nil {
			return r.jsTransferError(transferFailure{
				ErrorType: "cancelled",
				Message:   "download cancelled",
				Attempts:  attempt,
			})
		}
		probeDelay = calculateNextDelay(probeDelay, config)
	}
	if probeResp == nil {
		return r.jsTransferError(probeFailure)
	}

	totalSize := chunkedTransferTotal(probeResp)
	validator := transferResponseValidator(probeResp.Header)
	fingerprint := transferURLFingerprint(urlStr)
	stagedPath := stagedDownloadPath(fullPath)
	checkpointPath := transferCheckpointPath(stagedPath)
	keepPartial := persistentCheckpoint && validator != "" && fingerprint != ""
	checkpoint, checkpointOK := loadTransferCheckpoint(checkpointPath, fingerprint)
	if checkpointOK && (checkpoint.Validator != validator ||
		(checkpoint.Total > 0 && totalSize > 0 && checkpoint.Total != totalSize)) {
		checkpointOK = false
	}
	if !keepPartial || !checkpointOK {
		os.Remove(stagedPath)
		os.Remove(checkpointPath)
		checkpoint = transferCheckpoint{}
	}

	output, err := os.OpenFile(stagedPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return r.jsTransferError(transferFailure{
			ErrorType: "storage_error",
			Message:   fmt.Sprintf("failed to create chunked staged file: %v", err),
		})
	}
	promoted := false
	defer func() {
		output.Close()
		if promoted {
			os.Remove(checkpointPath)
		} else if !keepPartial {
			os.Remove(stagedPath)
			os.Remove(checkpointPath)
		}
	}()

	var totalWritten int64
	if checkpointOK {
		if info, statErr := output.Stat(); statErr == nil {
			totalWritten = min(checkpoint.Bytes, info.Size())
		}
	}
	if err := output.Truncate(totalWritten); err != nil {
		return r.jsTransferError(transferFailure{
			ErrorType: "storage_error",
			Message:   fmt.Sprintf("failed to restore chunked partial: %v", err),
		})
	}
	if _, err := output.Seek(totalWritten, io.SeekStart); err != nil {
		return r.jsTransferError(transferFailure{
			ErrorType: "storage_error",
			Message:   fmt.Sprintf("failed to seek chunked partial: %v", err),
		})
	}

	shouldTrackBytes := activeItemID != "" && trackItemBytes
	itemProgressReporter := NewItemTransferProgressReporter(activeItemID, totalWritten, totalSize)
	if shouldTrackBytes {
		if totalSize > 0 {
			SetItemProgress(
				activeItemID,
				float64(totalWritten)/float64(totalSize),
				totalWritten,
				totalSize,
			)
		} else if totalWritten > 0 {
			SetItemBytesReceived(activeItemID, totalWritten)
		}
	}

	lastProgressNotify := totalWritten
	lastCheckpointBytes := totalWritten
	lastCheckpointAt := time.Now()
	attemptsUsed := probeAttempts
	fullResponse := false
	completedChunk := false
	buffer := make([]byte, 64*1024)
	for totalSize <= 0 || totalWritten < totalSize {
		chunkStart := totalWritten
		chunkEnd := chunkStart + chunkSize - 1
		if totalSize > 0 && chunkEnd >= totalSize {
			chunkEnd = totalSize - 1
		}
		retryDelay := config.InitialDelay
		var chunkComplete bool
		var lastFailure transferFailure

		for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
			attemptsUsed++
			request, requestErr := http.NewRequest("GET", urlStr, nil)
			if requestErr != nil {
				return r.jsTransferError(transferFailure{
					ErrorType: "invalid_request",
					Message:   fmt.Sprintf("chunked request at %d: %v", chunkStart, requestErr),
					Attempts:  attemptsUsed,
				})
			}
			request = r.bindDownloadCancelContext(request)
			retryContext := request.Context()
			for key, value := range headers {
				if !strings.EqualFold(key, "Range") {
					request.Header.Set(key, value)
				}
			}
			request.Header.Set("User-Agent", userAgent)
			request.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", chunkStart, chunkEnd))
			if validator != "" {
				request.Header.Set("If-Range", validator)
			}
			request, watchdog := bindStallWatchdog(request, downloadStallTimeout)
			response, responseErr := r.doResolutionTransfer(client, request, attempt == 1 && completedChunk)
			if responseErr != nil {
				stalled := watchdog.stalled.Load()
				watchdog.stop()
				if r.chunkedTransferCancelled(activeItemID) {
					lastFailure = transferFailure{
						ErrorType: "cancelled",
						Message:   "download cancelled",
						Attempts:  attemptsUsed,
					}
					return r.jsTransferError(lastFailure)
				}
				message := fmt.Sprintf("chunked request at %d failed: %v", chunkStart, responseErr)
				if stalled {
					message = fmt.Sprintf(
						"chunked request at %d stalled for %ds",
						chunkStart,
						int(downloadStallTimeout.Seconds()),
					)
				}
				lastFailure = transferFailure{
					ErrorType: "transient_network",
					Message:   message,
					Attempts:  attemptsUsed,
				}
				if attempt == policy.MaxAttempts {
					break
				}
				if waitTransferRetry(retryContext, retryDelay) != nil {
					lastFailure.ErrorType = "cancelled"
					lastFailure.Message = "download cancelled"
					return r.jsTransferError(lastFailure)
				}
				retryDelay = calculateNextDelay(retryDelay, config)
				continue
			}

			if response.StatusCode != http.StatusPartialContent &&
				response.StatusCode != http.StatusOK {
				retryAfter := retryAfterSeconds(response)
				io.Copy(io.Discard, io.LimitReader(response.Body, 32*1024))
				response.Body.Close()
				watchdog.stop()
				lastFailure = transferFailure{
					ErrorType:         transferErrorTypeForStatus(response.StatusCode, policy),
					Message:           fmt.Sprintf("chunked HTTP %d at offset %d", response.StatusCode, chunkStart),
					HTTPStatus:        response.StatusCode,
					RetryAfterSeconds: retryAfter,
					Attempts:          attemptsUsed,
				}
				if !retryableTransferStatus(response.StatusCode) || attempt == policy.MaxAttempts {
					break
				}
				delay := retryDelay
				if retryAfter > 0 {
					delay = time.Duration(retryAfter) * time.Second
				}
				if waitTransferRetry(retryContext, delay) != nil {
					lastFailure.ErrorType = "cancelled"
					lastFailure.Message = "download cancelled"
					return r.jsTransferError(lastFailure)
				}
				retryDelay = calculateNextDelay(retryDelay, config)
				continue
			}

			if response.StatusCode == http.StatusPartialContent &&
				!strings.HasPrefix(
					response.Header.Get("Content-Range"),
					fmt.Sprintf("bytes %d-", chunkStart),
				) {
				contentRange := response.Header.Get("Content-Range")
				response.Body.Close()
				watchdog.stop()
				return r.jsTransferError(transferFailure{
					ErrorType: "integrity_failed",
					Message: fmt.Sprintf(
						"chunked response has unexpected Content-Range %q at %d",
						contentRange,
						chunkStart,
					),
					HTTPStatus: response.StatusCode,
					Attempts:   attemptsUsed,
				})
			}
			if nextValidator := transferResponseValidator(response.Header); nextValidator != "" &&
				validator != "" && nextValidator != validator {
				response.Body.Close()
				watchdog.stop()
				return r.jsTransferError(transferFailure{
					ErrorType: "integrity_failed",
					Message:   "chunked response validator changed during transfer",
					Attempts:  attemptsUsed,
				})
			}

			if response.StatusCode == http.StatusOK && chunkStart > 0 {
				if err := output.Truncate(0); err != nil {
					response.Body.Close()
					watchdog.stop()
					return r.jsTransferError(transferFailure{
						ErrorType: "storage_error",
						Message:   fmt.Sprintf("failed to restart ignored range: %v", err),
						Attempts:  attemptsUsed,
					})
				}
				if _, err := output.Seek(0, io.SeekStart); err != nil {
					response.Body.Close()
					watchdog.stop()
					return r.jsTransferError(transferFailure{
						ErrorType: "storage_error",
						Message:   fmt.Sprintf("failed to seek restarted range: %v", err),
						Attempts:  attemptsUsed,
					})
				}
				chunkStart = 0
				totalWritten = 0
				lastCheckpointBytes = 0
				os.Remove(checkpointPath)
				if response.ContentLength > 0 {
					totalSize = response.ContentLength
				}
			}

			chunkWritten := int64(0)
			var readErr error
			for {
				readCount, bodyErr := response.Body.Read(buffer)
				if readCount > 0 {
					watchdog.reset()
					if r.chunkedTransferCancelled(activeItemID) {
						readErr = ErrDownloadCancelled
						break
					}
					writeCount, writeErr := output.Write(buffer[:readCount])
					chunkWritten += int64(writeCount)
					totalWritten += int64(writeCount)
					if writeErr != nil || writeCount != readCount {
						response.Body.Close()
						watchdog.stop()
						if writeErr == nil {
							writeErr = io.ErrShortWrite
						}
						return r.jsTransferError(transferFailure{
							ErrorType: "storage_error",
							Message:   fmt.Sprintf("failed to write chunked output: %v", writeErr),
							Attempts:  attemptsUsed,
						})
					}
					if shouldTrackBytes {
						itemProgressReporter.Report(totalWritten, totalSize)
					}
					if onProgress != nil && totalSize > 0 &&
						(totalWritten-lastProgressNotify >= progressUpdateThreshold || totalWritten >= totalSize) {
						lastProgressNotify = totalWritten
						_, _ = onProgress(
							goja.Undefined(),
							r.vm.ToValue(totalWritten),
							r.vm.ToValue(totalSize),
						)
					}
				}
				if bodyErr != nil {
					if bodyErr != io.EOF {
						readErr = bodyErr
					}
					break
				}
			}
			response.Body.Close()
			stalled := watchdog.stalled.Load()
			watchdog.stop()
			expectedBytes := response.ContentLength
			if response.StatusCode == http.StatusPartialContent && expectedBytes <= 0 {
				expectedBytes = chunkEnd - chunkStart + 1
			}
			if readErr == nil && expectedBytes > 0 && chunkWritten != expectedBytes {
				readErr = io.ErrUnexpectedEOF
			}
			if readErr == nil && chunkWritten > 0 {
				chunkComplete = true
				completedChunk = true
				fullResponse = response.StatusCode == http.StatusOK
				break
			}

			if err := output.Truncate(chunkStart); err != nil {
				return r.jsTransferError(transferFailure{
					ErrorType: "storage_error",
					Message:   fmt.Sprintf("failed to roll back incomplete chunk: %v", err),
					Attempts:  attemptsUsed,
				})
			}
			if _, err := output.Seek(chunkStart, io.SeekStart); err != nil {
				return r.jsTransferError(transferFailure{
					ErrorType: "storage_error",
					Message:   fmt.Sprintf("failed to seek rolled-back chunk: %v", err),
					Attempts:  attemptsUsed,
				})
			}
			totalWritten = chunkStart
			if shouldTrackBytes && totalSize > 0 {
				SetItemProgress(
					activeItemID,
					float64(totalWritten)/float64(totalSize),
					totalWritten,
					totalSize,
				)
			}
			message := fmt.Sprintf("failed to read chunk at %d: %v", chunkStart, readErr)
			if chunkWritten == 0 && readErr == nil {
				message = fmt.Sprintf("chunk at %d was empty", chunkStart)
			}
			if stalled {
				message = fmt.Sprintf(
					"chunk at %d stalled for %ds",
					chunkStart,
					int(downloadStallTimeout.Seconds()),
				)
			}
			lastFailure = transferFailure{
				ErrorType: "transient_network",
				Message:   message,
				Attempts:  attemptsUsed,
			}
			if attempt == policy.MaxAttempts || r.chunkedTransferCancelled(activeItemID) {
				if r.chunkedTransferCancelled(activeItemID) {
					lastFailure.ErrorType = "cancelled"
					lastFailure.Message = "download cancelled"
				}
				break
			}
			if waitTransferRetry(retryContext, retryDelay) != nil {
				lastFailure.ErrorType = "cancelled"
				lastFailure.Message = "download cancelled"
				return r.jsTransferError(lastFailure)
			}
			retryDelay = calculateNextDelay(retryDelay, config)
		}

		if !chunkComplete {
			if keepPartial && totalWritten > 0 {
				_ = output.Sync()
				_ = saveTransferCheckpoint(checkpointPath, transferCheckpoint{
					Fingerprint: fingerprint,
					Validator:   validator,
					Bytes:       totalWritten,
					Total:       totalSize,
				})
			}
			return r.jsTransferError(lastFailure)
		}
		if keepPartial && validator != "" &&
			(totalWritten-lastCheckpointBytes >= transferCheckpointBytes ||
				time.Since(lastCheckpointAt) >= transferCheckpointPeriod) {
			if syncErr := output.Sync(); syncErr == nil {
				if saveTransferCheckpoint(checkpointPath, transferCheckpoint{
					Fingerprint: fingerprint,
					Validator:   validator,
					Bytes:       totalWritten,
					Total:       totalSize,
				}) == nil {
					lastCheckpointBytes = totalWritten
					lastCheckpointAt = time.Now()
				}
			}
		}
		if fullResponse {
			break
		}
		if totalSize <= 0 && totalWritten-chunkStart < chunkSize {
			break
		}
	}

	if totalWritten <= 0 || (totalSize > 0 && totalWritten != totalSize) {
		return r.jsTransferError(transferFailure{
			ErrorType: "integrity_failed",
			Message: fmt.Sprintf(
				"chunked transfer size mismatch: expected %d bytes, wrote %d",
				totalSize,
				totalWritten,
			),
			Attempts: attemptsUsed,
		})
	}
	if err := output.Sync(); err != nil {
		return r.jsTransferError(transferFailure{
			ErrorType: "storage_error",
			Message:   fmt.Sprintf("failed to sync chunked output: %v", err),
			Attempts:  attemptsUsed,
		})
	}
	if err := output.Close(); err != nil {
		return r.jsTransferError(transferFailure{
			ErrorType: "storage_error",
			Message:   fmt.Sprintf("failed to close chunked output: %v", err),
			Attempts:  attemptsUsed,
		})
	}
	if err := os.Rename(stagedPath, fullPath); err != nil {
		return r.jsTransferError(transferFailure{
			ErrorType: "storage_error",
			Message:   fmt.Sprintf("failed to publish chunked output: %v", err),
			Attempts:  attemptsUsed,
		})
	}
	promoted = true
	os.Remove(checkpointPath)
	syncDir(filepath.Dir(fullPath))
	if shouldTrackBytes {
		SetItemProgress(activeItemID, 1, totalWritten, totalWritten)
	}
	return r.jsSuccess(map[string]any{
		"path":     fullPath,
		"size":     totalWritten,
		"attempts": attemptsUsed,
	})
}
