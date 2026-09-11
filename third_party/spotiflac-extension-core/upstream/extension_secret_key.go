package gobackend

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"sync"
)

const extensionStorageMasterKeyBytes = 32

var extensionStorageKeyState struct {
	sync.RWMutex
	key []byte
}

// SetExtensionStorageMasterKey installs the platform-keystore-backed key used
// to encrypt extension settings and credentials. The key itself is never
// persisted by Go; Flutter stores it in Keychain/Android Keystore.
func SetExtensionStorageMasterKey(encodedKey string) error {
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil || len(key) != extensionStorageMasterKeyBytes {
		return fmt.Errorf("extension storage master key must be 32 base64-encoded bytes")
	}
	extensionStorageKeyState.Lock()
	extensionStorageKeyState.key = append(extensionStorageKeyState.key[:0], key...)
	extensionStorageKeyState.Unlock()
	return nil
}

func extensionStorageKeyConfigured() bool {
	extensionStorageKeyState.RLock()
	configured := len(extensionStorageKeyState.key) == extensionStorageMasterKeyBytes
	extensionStorageKeyState.RUnlock()
	return configured
}

func deriveExtensionStorageKey(extensionID, purpose string) ([]byte, error) {
	extensionStorageKeyState.RLock()
	if len(extensionStorageKeyState.key) != extensionStorageMasterKeyBytes {
		extensionStorageKeyState.RUnlock()
		return nil, fmt.Errorf("extension storage master key is not configured")
	}
	masterKey := append([]byte(nil), extensionStorageKeyState.key...)
	extensionStorageKeyState.RUnlock()

	mac := hmac.New(sha256.New, masterKey)
	_, _ = mac.Write([]byte("SpotiFLAC Mobile extension storage v2\x00"))
	_, _ = mac.Write([]byte(purpose))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(extensionID))
	return mac.Sum(nil), nil
}
