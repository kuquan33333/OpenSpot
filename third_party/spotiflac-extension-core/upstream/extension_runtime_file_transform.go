package gobackend

import (
	"context"
	"crypto/cipher"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dop251/goja"
)

const (
	defaultPatternedTransformBufferSize = int64(1 << 20)
	maxPatternedTransformBufferSize     = int64(16 << 20)
	maxPatternedTransformSegmentSize    = int64(16 << 20)
)

// fileTransformPatternedBlocks streams one file into another while applying an
// independent block-cipher transform to selected fixed-size segments. It keeps
// provider-specific layout knowledge in the extension: the host only receives
// a generic period/offset declaration and cipher parameters.
//
// JS signature:
//
//	file.transformPatternedBlocks(inputPath, outputPath, {
//	  operation: "decrypt", algorithm: "blowfish", mode: "cbc",
//	  key: "...", keyEncoding: "hex", iv: "...", ivEncoding: "hex",
//	  segmentSize: 2048, transformEvery: 3, transformOffset: 0,
//	  bufferSize: 1048576, transformPartial: false
//	}, function(processedBytes, totalBytes) {})
func (r *extensionRuntime) fileTransformPatternedBlocks(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 3 {
		return r.jsError("input path, output path, and options are required")
	}

	inputPath, err := r.validatePath(call.Arguments[0].String())
	if err != nil {
		return r.jsError("%s", err.Error())
	}
	outputPath, err := r.validatePath(call.Arguments[1].String())
	if err != nil {
		return r.jsError("%s", err.Error())
	}
	options := parseRuntimeOptionsArgument(call, 2)
	parsedCipher, err := parseRuntimeBlockCipherOptions(options)
	if err != nil {
		return r.jsError("%s", err.Error())
	}
	if parsedCipher.Padding != "none" {
		return r.jsError("patterned file transforms only support padding: none")
	}
	if parsedCipher.Mode != "cbc" && parsedCipher.Mode != "ctr" {
		return r.jsError("unsupported block cipher mode: %s", parsedCipher.Mode)
	}
	operation := strings.ToLower(runtimeOptionString(options, "operation", "decrypt"))
	if operation != "decrypt" && operation != "encrypt" {
		return r.jsError("operation must be decrypt or encrypt")
	}

	segmentSize := runtimeOptionInt64(options, "segmentSize", 0)
	transformEvery := runtimeOptionInt64(options, "transformEvery", 1)
	transformOffset := runtimeOptionInt64(options, "transformOffset", 0)
	bufferSize := runtimeOptionInt64(options, "bufferSize", defaultPatternedTransformBufferSize)
	transformPartial := runtimeOptionBool(options, "transformPartial", false)
	if segmentSize <= 0 || segmentSize > maxPatternedTransformSegmentSize {
		return r.jsError("segmentSize must be between 1 and %d bytes", maxPatternedTransformSegmentSize)
	}
	if transformEvery <= 0 {
		return r.jsError("transformEvery must be greater than zero")
	}
	if transformOffset < 0 || transformOffset >= transformEvery {
		return r.jsError("transformOffset must be between 0 and transformEvery - 1")
	}
	if bufferSize < segmentSize {
		bufferSize = segmentSize
	}
	if bufferSize > maxPatternedTransformBufferSize {
		bufferSize = maxPatternedTransformBufferSize
	}
	bufferSize -= bufferSize % segmentSize
	if bufferSize == 0 {
		bufferSize = segmentSize
	}

	block, err := newRuntimeBlockCipher(parsedCipher)
	if err != nil {
		return r.jsError("%s", err.Error())
	}
	if len(parsedCipher.IV) != block.BlockSize() {
		return r.jsError("iv must be %d bytes for %s", block.BlockSize(), parsedCipher.Algorithm)
	}
	if parsedCipher.Mode == "cbc" && segmentSize%int64(block.BlockSize()) != 0 {
		return r.jsError("segmentSize must be a multiple of %d bytes for CBC", block.BlockSize())
	}

	var onProgress goja.Callable
	if len(call.Arguments) > 3 && !goja.IsUndefined(call.Arguments[3]) && !goja.IsNull(call.Arguments[3]) {
		callback, ok := goja.AssertFunction(call.Arguments[3])
		if !ok {
			return r.jsError("progress callback must be a function")
		}
		onProgress = callback
	}

	unlock := lockDownloadOutputPath(outputPath)
	defer unlock()

	input, err := os.Open(inputPath)
	if err != nil {
		return r.jsError("failed to open input file: %v", err)
	}
	info, err := input.Stat()
	if err != nil {
		input.Close()
		return r.jsError("failed to stat input file: %v", err)
	}
	totalSize := info.Size()

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		input.Close()
		return r.jsError("failed to create output directory: %v", err)
	}
	stagedPath := outputPath + ".transform.partial"
	if filepath.Clean(stagedPath) == filepath.Clean(inputPath) {
		input.Close()
		return r.jsError("input path conflicts with transform staging path")
	}
	_ = os.Remove(stagedPath)
	output, err := os.OpenFile(stagedPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		input.Close()
		return r.jsError("failed to create staged output: %v", err)
	}

	cleanup := func() {
		input.Close()
		output.Close()
		_ = os.Remove(stagedPath)
	}
	ctx := r.activeOperationContext(context.Background())
	buffer := make([]byte, int(bufferSize))
	processed := int64(0)
	segmentIndex := int64(0)
	segmentsTransformed := int64(0)

	for {
		if err := ctx.Err(); err != nil {
			cleanup()
			return r.jsError("patterned file transform cancelled: %v", err)
		}
		readCount, readErr := io.ReadFull(input, buffer)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			cleanup()
			return r.jsError("failed to read input file: %v", readErr)
		}
		if readCount == 0 {
			break
		}

		chunk := buffer[:readCount]
		for offset := 0; offset < readCount; offset += int(segmentSize) {
			end := min(offset+int(segmentSize), readCount)
			segment := chunk[offset:end]
			selected := segmentIndex%transformEvery == transformOffset
			fullSegment := len(segment) == int(segmentSize)
			if selected && (fullSegment || transformPartial) {
				if parsedCipher.Mode == "cbc" && len(segment)%block.BlockSize() != 0 {
					cleanup()
					return r.jsError("selected segment %d is not a multiple of %d bytes", segmentIndex, block.BlockSize())
				}
				transformPatternedSegment(block, parsedCipher, operation, segment)
				segmentsTransformed++
			}
			segmentIndex++
		}

		written, writeErr := output.Write(chunk)
		if writeErr != nil || written != len(chunk) {
			if writeErr == nil {
				writeErr = io.ErrShortWrite
			}
			cleanup()
			return r.jsError("failed to write transformed file: %v", writeErr)
		}
		processed += int64(written)
		if onProgress != nil {
			if _, callbackErr := onProgress(
				goja.Undefined(),
				r.vm.ToValue(processed),
				r.vm.ToValue(totalSize),
			); callbackErr != nil {
				cleanup()
				return r.jsError("progress callback failed: %v", callbackErr)
			}
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
	}

	if err := input.Close(); err != nil {
		output.Close()
		_ = os.Remove(stagedPath)
		return r.jsError("failed to close input file: %v", err)
	}
	if err := output.Sync(); err != nil {
		output.Close()
		_ = os.Remove(stagedPath)
		return r.jsError("failed to sync transformed file: %v", err)
	}
	if err := output.Close(); err != nil {
		_ = os.Remove(stagedPath)
		return r.jsError("failed to close transformed file: %v", err)
	}
	if err := os.Rename(stagedPath, outputPath); err != nil {
		_ = os.Remove(stagedPath)
		return r.jsError("failed to publish transformed file: %v", err)
	}

	return r.jsSuccess(map[string]any{
		"path":                 outputPath,
		"bytes_processed":      processed,
		"segments_processed":   segmentIndex,
		"segments_transformed": segmentsTransformed,
	})
}

func transformPatternedSegment(
	block cipher.Block,
	options *runtimeBlockCipherOptions,
	operation string,
	segment []byte,
) {
	if options.Mode == "ctr" {
		cipher.NewCTR(block, options.IV).XORKeyStream(segment, segment)
		return
	}
	if operation == "encrypt" {
		cipher.NewCBCEncrypter(block, options.IV).CryptBlocks(segment, segment)
		return
	}
	cipher.NewCBCDecrypter(block, options.IV).CryptBlocks(segment, segment)
}
