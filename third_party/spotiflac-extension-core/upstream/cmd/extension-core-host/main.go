package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

import backend "github.com/zarz/spotiflac_android/go_backend"

type request struct {
	Operation string          `json:"operation"`
	Payload   json.RawMessage `json:"payload"`
}

type response struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func storageKey(path string) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("OPENSPOT_EXTENSION_CORE_KEY_FILE is not set")
	}
	if existing, err := os.ReadFile(path); err == nil {
		if len(existing) != 32 {
			return nil, fmt.Errorf("extension storage key must contain 32 bytes")
		}
		return existing, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read extension storage key: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create extension storage directory: %w", err)
	}
	generated := make([]byte, 32)
	if _, err := rand.Read(generated); err != nil {
		return nil, fmt.Errorf("generate extension storage key: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		if _, writeErr := file.Write(generated); writeErr != nil {
			_ = file.Close()
			return nil, fmt.Errorf("write extension storage key: %w", writeErr)
		}
		if closeErr := file.Close(); closeErr != nil {
			return nil, fmt.Errorf("close extension storage key: %w", closeErr)
		}
		return generated, nil
	}
	if !os.IsExist(err) {
		return nil, fmt.Errorf("create extension storage key: %w", err)
	}
	existing, readErr := os.ReadFile(path)
	if readErr != nil || len(existing) != 32 {
		if readErr != nil {
			return nil, fmt.Errorf("read concurrently-created extension storage key: %w", readErr)
		}
		return nil, fmt.Errorf("concurrently-created extension storage key must contain 32 bytes")
	}
	return existing, nil
}

func emit(writer *bufio.Writer, result string, err error) error {
	message := response{}
	if err != nil {
		message.Error = err.Error()
	} else if result == "" {
		message.Result = json.RawMessage("null")
	} else if !json.Valid([]byte(result)) {
		message.Error = fmt.Sprintf("extension core returned invalid JSON: %s", result)
	} else {
		message.Result = json.RawMessage(result)
	}
	encoded, marshalErr := json.Marshal(message)
	if marshalErr != nil {
		return marshalErr
	}
	if _, writeErr := writer.Write(append(encoded, '\n')); writeErr != nil {
		return writeErr
	}
	return writer.Flush()
}

func main() {
	key, err := storageKey(os.Getenv("OPENSPOT_EXTENSION_CORE_KEY_FILE"))
	if err == nil {
		err = backend.SetExtensionStorageMasterKey(base64.StdEncoding.EncodeToString(key))
	}
	startupErr := err

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	writer := bufio.NewWriter(os.Stdout)
	for scanner.Scan() {
		if startupErr != nil {
			if err := emit(writer, "", startupErr); err != nil {
				return
			}
			continue
		}
		var call request
		if err := json.Unmarshal(scanner.Bytes(), &call); err != nil {
			if emitErr := emit(writer, "", fmt.Errorf("invalid request JSON: %w", err)); emitErr != nil {
				return
			}
			continue
		}
		result, callErr := backend.CallOpenSpotExtensionJSON(call.Operation, string(call.Payload))
		if emitErr := emit(writer, result, callErr); emitErr != nil {
			return
		}
	}
}
