package gobackend

import (
	"context"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/dop251/goja"
)

// FFmpegCommand holds a pending FFmpeg command for the host to execute.
type FFmpegCommand struct {
	ExtensionID string
	Arguments   []string
	InputPath   string
	OutputPath  string
	Completed   bool
	Claimed     bool
	Success     bool
	Error       string
	Output      string
	done        chan struct{}
}

var (
	ffmpegCommands      = make(map[string]*FFmpegCommand)
	ffmpegCommandsMu    sync.RWMutex
	ffmpegCommandID     int64
	ffmpegCommandQueued = make(chan struct{}, 1)
)

func notifyFFmpegCommandQueued() {
	select {
	case ffmpegCommandQueued <- struct{}{}:
	default:
	}
}

func GetPendingFFmpegCommand(commandID string) *FFmpegCommand {
	ffmpegCommandsMu.RLock()
	defer ffmpegCommandsMu.RUnlock()
	return ffmpegCommands[commandID]
}

func SetFFmpegCommandResult(commandID string, success bool, output, errorMsg string) {
	ffmpegCommandsMu.Lock()
	defer ffmpegCommandsMu.Unlock()
	if cmd, exists := ffmpegCommands[commandID]; exists {
		if cmd.Completed {
			return
		}
		cmd.Completed = true
		cmd.Success = success
		cmd.Output = output
		cmd.Error = errorMsg
		if cmd.done != nil {
			close(cmd.done)
		}
	}
}

func ClearFFmpegCommand(commandID string) {
	ffmpegCommandsMu.Lock()
	defer ffmpegCommandsMu.Unlock()
	delete(ffmpegCommands, commandID)
}

func (r *extensionRuntime) ffmpegExecute(call goja.FunctionCall) goja.Value {
	// A raw command can introduce additional file inputs and network protocols,
	// bypassing both validatePath and the extension network allow-list. Keep the
	// API stub for compatibility, but never forward an unstructured command to
	// the native FFmpeg process.
	return r.jsError("raw FFmpeg execution is disabled; use ffmpeg.convert")
}

func (r *extensionRuntime) executeFFmpegCommand(arguments []string, inputPath, outputPath string) goja.Value {
	ctx := r.activeOperationContext(context.Background())
	if budget := r.currentResolutionBudget(); budget != nil {
		defer budget.pause()()
	}
	if ctx.Err() != nil {
		return r.jsError("FFmpeg command cancelled: %v", context.Cause(ctx))
	}

	ffmpegCommandsMu.Lock()
	ffmpegCommandID++
	cmdID := fmt.Sprintf("%s_%d", r.extensionID, ffmpegCommandID)
	queuedCommand := &FFmpegCommand{
		ExtensionID: r.extensionID,
		Arguments:   append([]string(nil), arguments...),
		InputPath:   inputPath,
		OutputPath:  outputPath,
		Completed:   false,
		done:        make(chan struct{}),
	}
	ffmpegCommands[cmdID] = queuedCommand
	ffmpegCommandsMu.Unlock()
	notifyFFmpegCommandQueued()

	GoLog("[Extension:%s] FFmpeg command queued: %s\n", r.extensionID, cmdID)

	select {
	case <-queuedCommand.done:
		ffmpegCommandsMu.Lock()
		result := map[string]any{
			"success": queuedCommand.Success,
			"output":  queuedCommand.Output,
		}
		if queuedCommand.Error != "" {
			result["error"] = queuedCommand.Error
		}
		delete(ffmpegCommands, cmdID)
		ffmpegCommandsMu.Unlock()
		return r.vm.ToValue(result)
	case <-ctx.Done():
		ClearFFmpegCommand(cmdID)
		return r.jsError("FFmpeg command cancelled: %v", context.Cause(ctx))
	case <-time.After(5 * time.Minute):
		ClearFFmpegCommand(cmdID)
		return r.jsError("FFmpeg command timed out")
	}
}

var ffmpegBitratePattern = regexp.MustCompile(`^[1-9][0-9]{0,7}[kKmM]?$`)

var allowedFFmpegAudioCodecs = map[string]struct{}{
	"aac":        {},
	"alac":       {},
	"copy":       {},
	"flac":       {},
	"libmp3lame": {},
	"libopus":    {},
	"opus":       {},
	"pcm_s16le":  {},
	"pcm_s24le":  {},
}

func (r *extensionRuntime) ffmpegGetInfo(call goja.FunctionCall) goja.Value {
	if r.manifest == nil || !r.manifest.Permissions.File {
		return r.jsError("file permission denied")
	}
	if len(call.Arguments) < 1 {
		return r.jsError("file path is required")
	}

	filePath, err := r.validatePath(call.Arguments[0].String())
	if err != nil {
		return r.jsError("%s", err.Error())
	}

	quality, err := GetAudioQuality(filePath)
	if err != nil {
		return r.jsError("%s", err.Error())
	}

	return r.jsSuccess(map[string]any{
		"bit_depth":     quality.BitDepth,
		"sample_rate":   quality.SampleRate,
		"total_samples": quality.TotalSamples,
		"duration":      float64(quality.TotalSamples) / float64(quality.SampleRate),
		"codec":         quality.Codec,
	})
}

func (r *extensionRuntime) ffmpegConvert(call goja.FunctionCall) goja.Value {
	if r.manifest == nil || !r.manifest.Permissions.File {
		return r.jsError("file permission denied")
	}
	if len(call.Arguments) < 2 {
		return r.jsError("input and output paths are required")
	}

	inputPath, err := r.validatePath(call.Arguments[0].String())
	if err != nil {
		return r.jsError("invalid input path: %v", err)
	}
	outputPath, err := r.validatePath(call.Arguments[1].String())
	if err != nil {
		return r.jsError("invalid output path: %v", err)
	}

	options := map[string]any{}
	if len(call.Arguments) > 2 && !goja.IsUndefined(call.Arguments[2]) && !goja.IsNull(call.Arguments[2]) {
		if opts, ok := call.Arguments[2].Export().(map[string]any); ok {
			options = opts
		}
	}

	arguments := []string{"-hide_banner", "-nostdin", "-i", inputPath}

	if codec, ok := options["codec"].(string); ok {
		if _, allowed := allowedFFmpegAudioCodecs[codec]; !allowed {
			return r.jsError("unsupported audio codec")
		}
		arguments = append(arguments, "-c:a", codec)
	}

	if bitrate, ok := options["bitrate"].(string); ok {
		if !ffmpegBitratePattern.MatchString(bitrate) {
			return r.jsError("invalid audio bitrate")
		}
		arguments = append(arguments, "-b:a", bitrate)
	}

	if sampleRate, ok := options["sample_rate"].(float64); ok {
		if sampleRate < 8_000 || sampleRate > 768_000 || sampleRate != float64(int(sampleRate)) {
			return r.jsError("invalid sample rate")
		}
		arguments = append(arguments, "-ar", fmt.Sprintf("%d", int(sampleRate)))
	}

	if channels, ok := options["channels"].(float64); ok {
		if channels < 1 || channels > 32 || channels != float64(int(channels)) {
			return r.jsError("invalid channel count")
		}
		arguments = append(arguments, "-ac", fmt.Sprintf("%d", int(channels)))
	}

	arguments = append(arguments, "-y", outputPath)

	return r.executeFFmpegCommand(arguments, inputPath, outputPath)
}
