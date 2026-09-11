package gobackend

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dop251/goja"
)

var (
	allowedDownloadDirs   []string
	allowedDownloadDirsMu sync.RWMutex
)

func AddAllowedDownloadDir(dir string) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return
	}
	absDir = filepath.Clean(absDir)

	allowedDownloadDirsMu.Lock()
	defer allowedDownloadDirsMu.Unlock()
	for _, existing := range allowedDownloadDirs {
		if existing == absDir {
			return
		}
	}
	allowedDownloadDirs = append(allowedDownloadDirs, absDir)
}

// SetAllowedDownloadDirs replaces the whole allow-list in one call (passing nil
// clears it). Used by tests to reset the sandbox between cases; production code
// appends via AddAllowedDownloadDir.
func SetAllowedDownloadDirs(dirs []string) {
	allowedDownloadDirsMu.Lock()
	defer allowedDownloadDirsMu.Unlock()
	allowedDownloadDirs = nil
	seen := make(map[string]struct{}, len(dirs))
	for _, dir := range dirs {
		absDir, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		absDir = filepath.Clean(absDir)
		if _, duplicate := seen[absDir]; duplicate {
			continue
		}
		seen[absDir] = struct{}{}
		allowedDownloadDirs = append(allowedDownloadDirs, absDir)
	}
}

func isPathInAllowedDirs(absPath string) bool {
	allowedDownloadDirsMu.RLock()
	defer allowedDownloadDirsMu.RUnlock()

	for _, allowedDir := range allowedDownloadDirs {
		if isPathWithinBase(allowedDir, absPath) {
			return true
		}
	}
	return false
}

func isPathWithinBase(baseDir, targetPath string) bool {
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return false
	}
	targetAbs, err := filepath.Abs(targetPath)
	if err != nil {
		return false
	}

	rel, err := filepath.Rel(baseAbs, targetAbs)
	if err != nil {
		return false
	}
	rel = filepath.Clean(rel)
	if rel == "." {
		return true
	}

	prefix := ".." + string(filepath.Separator)
	if rel == ".." || strings.HasPrefix(rel, prefix) {
		return false
	}
	return true
}

func (r *extensionRuntime) validatePath(path string) (string, error) {
	if !r.manifest.Permissions.File {
		return "", fmt.Errorf("file access denied: extension does not have 'file' permission")
	}

	cleanPath := filepath.Clean(path)

	if filepath.IsAbs(cleanPath) {
		absPath, err := filepath.Abs(cleanPath)
		if err != nil {
			return "", fmt.Errorf("invalid path: %w", err)
		}

		if isPathInAllowedDirs(absPath) {
			return absPath, nil
		}

		return "", fmt.Errorf("file access denied: absolute paths are not allowed. Use relative paths within extension sandbox")
	}

	fullPath := filepath.Join(r.dataDir, cleanPath)

	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}

	absDataDir, _ := filepath.Abs(r.dataDir)
	if !isPathWithinBase(absDataDir, absPath) {
		return "", fmt.Errorf("file access denied: path '%s' is outside sandbox", path)
	}

	return absPath, nil
}

func (r *extensionRuntime) fileDownload(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 2 {
		return r.jsError("URL and output path are required")
	}

	urlStr := call.Arguments[0].String()
	outputPath := call.Arguments[1].String()

	if err := r.validateDomain(urlStr); err != nil {
		return r.jsError("%s", err.Error())
	}

	fullPath, err := r.validatePath(outputPath)
	if err != nil {
		return r.jsError("%s", err.Error())
	}

	var onProgress goja.Callable
	var headers map[string]string
	var chunkedDownload bool
	var resumeDownload bool
	var resumeOptionSet bool
	var persistentCheckpointOption *bool
	var maxAttemptsOption int
	trackItemBytes := true
	var chunkSize int64
	if len(call.Arguments) > 2 && !goja.IsUndefined(call.Arguments[2]) && !goja.IsNull(call.Arguments[2]) {
		optionsObj := call.Arguments[2].Export()
		if opts, ok := optionsObj.(map[string]any); ok {
			if h, ok := opts["headers"].(map[string]any); ok {
				headers = make(map[string]string)
				for k, v := range h {
					headers[k] = fmt.Sprintf("%v", v)
				}
			}
			if progressVal, ok := opts["onProgress"]; ok {
				if callable, ok := goja.AssertFunction(r.vm.ToValue(progressVal)); ok {
					onProgress = callable
				}
			}
			if trackBytes, ok := opts["trackItemBytes"]; ok {
				if v, ok := trackBytes.(bool); ok {
					trackItemBytes = v
				}
			} else if trackBytes, ok := opts["track_item_bytes"]; ok {
				if v, ok := trackBytes.(bool); ok {
					trackItemBytes = v
				}
			}
			if chunked, ok := opts["chunked"]; ok {
				switch v := chunked.(type) {
				case bool:
					chunkedDownload = v
				case int64:
					if v > 0 {
						chunkedDownload = true
						chunkSize = v
					}
				case float64:
					if v > 0 {
						chunkedDownload = true
						chunkSize = int64(v)
					}
				}
			}
			if resume, ok := opts["resume"]; ok {
				if v, ok := resume.(bool); ok {
					resumeDownload = v
					resumeOptionSet = true
				}
			}
			if checkpoint, ok := opts["persistentCheckpoint"]; ok {
				if v, ok := checkpoint.(bool); ok {
					persistentCheckpointOption = &v
				}
			}
			if attempts, ok := opts["maxAttempts"]; ok {
				switch v := attempts.(type) {
				case int64:
					maxAttemptsOption = int(v)
				case float64:
					maxAttemptsOption = int(v)
				}
			}
		}
	}

	// Default chunk size: 1MB (YouTube CDN max without poToken)
	if chunkedDownload && chunkSize <= 0 {
		chunkSize = 1024 * 1024
	}

	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return r.jsError("failed to create directory: %v", err)
	}

	client := r.downloadClient
	if client == nil {
		client = r.httpClient
	}

	ua := appUserAgent()
	if h, ok := headers["User-Agent"]; ok && h != "" {
		ua = h
	}

	policy := r.manifest.DownloadTransferPolicy()
	if !resumeOptionSet {
		resumeDownload = policy.ResumePolicy == "validated"
	}
	persistentCheckpoint := policy.PersistentCheckpoint
	if persistentCheckpointOption != nil {
		persistentCheckpoint = *persistentCheckpointOption && resumeDownload
	}
	if maxAttemptsOption > 0 {
		policy.MaxAttempts = clampInt(maxAttemptsOption, 1, 8)
	}
	if chunkedDownload {
		return r.fileDownloadChunked(
			client,
			urlStr,
			fullPath,
			headers,
			ua,
			chunkSize,
			onProgress,
			trackItemBytes,
			persistentCheckpoint,
			policy,
		)
	}
	return r.reliableFileDownload(
		client,
		urlStr,
		fullPath,
		headers,
		onProgress,
		trackItemBytes,
		resumeDownload,
		persistentCheckpoint,
		policy,
	)
}

func (r *extensionRuntime) fileExists(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		return r.vm.ToValue(false)
	}

	path := call.Arguments[0].String()
	fullPath, err := r.validatePath(path)
	if err != nil {
		return r.vm.ToValue(false)
	}

	_, err = os.Stat(fullPath)
	return r.vm.ToValue(err == nil)
}

func (r *extensionRuntime) fileDelete(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		return r.jsError("path is required")
	}

	path := call.Arguments[0].String()
	fullPath, err := r.validatePath(path)
	if err != nil {
		return r.jsError("%s", err.Error())
	}

	if err := os.Remove(fullPath); err != nil {
		return r.jsError("%s", err.Error())
	}

	return r.jsSuccess(nil)
}

func (r *extensionRuntime) fileRead(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		return r.jsError("path is required")
	}

	path := call.Arguments[0].String()
	fullPath, err := r.validatePath(path)
	if err != nil {
		return r.jsError("%s", err.Error())
	}

	file, err := os.Open(fullPath)
	if err != nil {
		return r.jsError("%s", err.Error())
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxExtensionFileReadBytes+1))
	if err != nil {
		return r.jsError("%s", err.Error())
	}
	if int64(len(data)) > maxExtensionFileReadBytes {
		return r.jsError(extensionFileReadLimitError)
	}

	return r.jsSuccess(map[string]any{
		"data": string(data),
	})
}

const (
	maxExtensionFileReadBytes   = int64(16 << 20)
	extensionFileReadLimitError = "file read exceeds 16 MiB limit; use file.readBytes with offset and length to read it in chunks"
)

func extensionFileReadLength(size, offset, requested int64) (int64, error) {
	remaining := size - offset
	if remaining < 0 {
		remaining = 0
	}

	if requested < 0 {
		if remaining > maxExtensionFileReadBytes {
			return 0, fmt.Errorf("%s", extensionFileReadLimitError)
		}
		return remaining, nil
	}
	if requested > maxExtensionFileReadBytes {
		return 0, fmt.Errorf("%s", extensionFileReadLimitError)
	}
	return min(requested, remaining), nil
}

func (r *extensionRuntime) fileReadBytes(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		return r.jsError("path is required")
	}

	path := call.Arguments[0].String()
	fullPath, err := r.validatePath(path)
	if err != nil {
		return r.jsError("%s", err.Error())
	}

	options := parseRuntimeOptionsArgument(call, 1)
	offset := runtimeOptionInt64(options, "offset", 0)
	length := runtimeOptionInt64(options, "length", -1)
	encoding := runtimeOptionString(options, "encoding", "base64")
	if offset < 0 {
		return r.jsError("offset must be >= 0")
	}
	file, err := os.Open(fullPath)
	if err != nil {
		return r.jsError("%s", err.Error())
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return r.jsError("%s", err.Error())
	}

	size := info.Size()
	if offset > size {
		offset = size
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return r.jsError("failed to seek file: %v", err)
	}

	readLength, err := extensionFileReadLength(size, offset, length)
	if err != nil {
		return r.jsError("%s", err.Error())
	}
	data := make([]byte, int(readLength))
	if readLength > 0 {
		n, readErr := io.ReadFull(file, data)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return r.jsError("failed to read file: %v", readErr)
		}
		data = data[:n]
	}

	if strings.EqualFold(strings.TrimSpace(encoding), "bytes") ||
		strings.EqualFold(strings.TrimSpace(encoding), "raw") {
		// Return raw bytes as an ArrayBuffer to avoid base64 encode/decode of
		// large payloads under the goja interpreter.
		return r.jsSuccess(map[string]any{
			"data":       r.vm.NewArrayBuffer(data),
			"bytes_read": len(data),
			"offset":     offset,
			"size":       size,
			"eof":        offset+int64(len(data)) >= size,
		})
	}

	encoded, err := encodeRuntimeBytes(data, encoding)
	if err != nil {
		return r.jsError("%s", err.Error())
	}

	return r.jsSuccess(map[string]any{
		"data":       encoded,
		"bytes_read": len(data),
		"offset":     offset,
		"size":       size,
		"eof":        offset+int64(len(data)) >= size,
	})
}
func (r *extensionRuntime) fileWrite(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 2 {
		return r.jsError("path and data are required")
	}

	path := call.Arguments[0].String()
	data := call.Arguments[1].String()

	fullPath, err := r.validatePath(path)
	if err != nil {
		return r.jsError("%s", err.Error())
	}

	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return r.jsError("failed to create directory: %v", err)
	}

	// Full-content write: stage and rename so a kill mid-write cannot leave
	// a truncated file under the final name.
	stagedPath := stagedDownloadPath(fullPath)
	if err := os.WriteFile(stagedPath, []byte(data), 0644); err != nil {
		os.Remove(stagedPath)
		return r.jsError("%s", err.Error())
	}
	if err := os.Rename(stagedPath, fullPath); err != nil {
		os.Remove(stagedPath)
		return r.jsError("%s", err.Error())
	}

	return r.jsSuccess(map[string]any{
		"path": fullPath,
	})
}

func (r *extensionRuntime) fileWriteBytes(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 2 {
		return r.jsError("path and data are required")
	}

	path := call.Arguments[0].String()
	fullPath, err := r.validatePath(path)
	if err != nil {
		return r.jsError("%s", err.Error())
	}

	options := parseRuntimeOptionsArgument(call, 2)
	appendMode := runtimeOptionBool(options, "append", false)
	truncate := runtimeOptionBool(options, "truncate", false)
	hasOffset := runtimeOptionHasKey(options, "offset")
	offset := runtimeOptionInt64(options, "offset", 0)
	encoding := runtimeOptionString(options, "encoding", "base64")

	if appendMode && hasOffset {
		return r.jsError("append and offset cannot be used together")
	}
	if offset < 0 {
		return r.jsError("offset must be >= 0")
	}

	data, err := decodeRuntimeBytesValue(call.Arguments[1].Export(), encoding)
	if err != nil {
		return r.jsError("%s", err.Error())
	}

	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return r.jsError("failed to create directory: %v", err)
	}

	flags := os.O_CREATE | os.O_WRONLY
	if appendMode {
		flags |= os.O_APPEND
	}
	if truncate {
		flags |= os.O_TRUNC
	}

	file, err := os.OpenFile(fullPath, flags, 0644)
	if err != nil {
		return r.jsError("%s", err.Error())
	}
	defer file.Close()

	if hasOffset && !appendMode {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return r.jsError("failed to seek file: %v", err)
		}
	}

	written, err := file.Write(data)
	if err != nil {
		return r.jsError("%s", err.Error())
	}

	info, statErr := file.Stat()
	size := int64(0)
	if statErr == nil {
		size = info.Size()
	}

	return r.jsSuccess(map[string]any{
		"path":          fullPath,
		"bytes_written": written,
		"size":          size,
	})
}

func (r *extensionRuntime) fileCopy(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 2 {
		return r.jsError("source and destination paths are required")
	}

	srcPath := call.Arguments[0].String()
	dstPath := call.Arguments[1].String()

	fullSrc, err := r.validatePath(srcPath)
	if err != nil {
		return r.jsError("%s", err.Error())
	}

	fullDst, err := r.validatePath(dstPath)
	if err != nil {
		return r.jsError("%s", err.Error())
	}

	srcFile, err := os.Open(fullSrc)
	if err != nil {
		return r.jsError("failed to read source: %v", err)
	}
	defer srcFile.Close()

	dir := filepath.Dir(fullDst)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return r.jsError("failed to create directory: %v", err)
	}

	dstFile, err := os.OpenFile(fullDst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return r.jsError("failed to open destination: %v", err)
	}

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		_ = dstFile.Close()
		return r.jsError("failed to copy file: %v", err)
	}

	if err := dstFile.Close(); err != nil {
		return r.jsError("failed to finalize destination: %v", err)
	}

	return r.jsSuccess(map[string]any{
		"path": fullDst,
	})
}

func (r *extensionRuntime) fileMove(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 2 {
		return r.jsError("source and destination paths are required")
	}

	srcPath := call.Arguments[0].String()
	dstPath := call.Arguments[1].String()

	fullSrc, err := r.validatePath(srcPath)
	if err != nil {
		return r.jsError("%s", err.Error())
	}

	fullDst, err := r.validatePath(dstPath)
	if err != nil {
		return r.jsError("%s", err.Error())
	}

	dir := filepath.Dir(fullDst)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return r.jsError("failed to create directory: %v", err)
	}

	if err := os.Rename(fullSrc, fullDst); err != nil {
		return r.jsError("failed to move file: %v", err)
	}

	return r.jsSuccess(map[string]any{
		"path": fullDst,
	})
}

func (r *extensionRuntime) fileGetSize(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		return r.jsError("path is required")
	}

	path := call.Arguments[0].String()
	fullPath, err := r.validatePath(path)
	if err != nil {
		return r.jsError("%s", err.Error())
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		return r.jsError("%s", err.Error())
	}

	return r.jsSuccess(map[string]any{
		"size": info.Size(),
	})
}
