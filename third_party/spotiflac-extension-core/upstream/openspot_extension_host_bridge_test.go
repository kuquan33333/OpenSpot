package gobackend

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestOpenSpotHostBridgeAuthJSONDoesNotExposeTokens(t *testing.T) {
	const extensionID = "bridge-auth-test"
	pendingAuthRequestsMu.Lock()
	pendingAuthRequests[extensionID] = &PendingAuthRequest{
		ExtensionID: extensionID,
		AuthURL:     "https://auth.example.test/start",
		CallbackURL: "openspot://extension-callback",
		State:       "private-state",
		CreatedAt:   time.Now(),
	}
	pendingAuthRequestsMu.Unlock()
	t.Cleanup(func() {
		pendingAuthRequestsMu.Lock()
		delete(pendingAuthRequests, extensionID)
		pendingAuthRequestsMu.Unlock()
	})

	payload, err := GetAllPendingAuthRequestsJSON()
	if err != nil {
		t.Fatalf("GetAllPendingAuthRequestsJSON: %v", err)
	}
	if strings.Contains(payload, "private-state") || strings.Contains(payload, "access_token") || strings.Contains(payload, "refresh_token") {
		t.Fatalf("auth JSON exposed private state: %s", payload)
	}

	var requests []map[string]any
	if err := json.Unmarshal([]byte(payload), &requests); err != nil {
		t.Fatalf("decode auth JSON: %v", err)
	}
	found := false
	for _, request := range requests {
		if request["extension_id"] == extensionID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("auth request missing from JSON: %#v", requests)
	}
}

func TestOpenSpotHostBridgeFFmpegQueueClaimsOnce(t *testing.T) {
	const commandID = "bridge-ffmpeg-test"
	command := &FFmpegCommand{
		ExtensionID: "bridge-extension",
		Arguments:   []string{"-version"},
		InputPath:   "input.flac",
		OutputPath:  "output.flac",
		done:        make(chan struct{}),
	}
	ffmpegCommandsMu.Lock()
	ffmpegCommands[commandID] = command
	ffmpegCommandsMu.Unlock()
	t.Cleanup(func() { ClearFFmpegCommand(commandID) })

	first, err := WaitForPendingFFmpegCommandsJSON(50)
	if err != nil || !strings.Contains(first, commandID) {
		t.Fatalf("first FFmpeg wait = %q/%v", first, err)
	}
	second, err := WaitForPendingFFmpegCommandsJSON(1)
	if err != nil || second != "[]" {
		t.Fatalf("claimed FFmpeg command returned twice: %q/%v", second, err)
	}

	commandJSON, err := GetPendingFFmpegCommandJSON(commandID)
	if err != nil || !strings.Contains(commandJSON, "input.flac") || !strings.Contains(commandJSON, "output.flac") {
		t.Fatalf("pending FFmpeg JSON = %q/%v", commandJSON, err)
	}

	SetFFmpegCommandResultByID(commandID, true, "ok", "")
	select {
	case <-command.done:
	default:
		t.Fatal("FFmpeg completion did not signal waiter")
	}
}
