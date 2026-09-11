package gobackend

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/dop251/goja"
)

// Isolated per-download runtimes of the same extension share the storage,
// credentials, and salt files on disk, so writers must be serialized
// process-wide; the per-runtime mutexes only cover a single VM.
var extensionFileMus sync.Map // file path -> *sync.Mutex

type extensionFileIdentity struct {
	exists   bool
	size     int64
	modified int64
}

type extensionJSONCacheEntry struct {
	identity extensionFileIdentity
	snapshot map[string]any
}

// Shared by all isolated runtimes so repeated storage/credential reads avoid
// reading, decoding, and (for credentials) decrypting the complete file. The
// corresponding extensionFileMu must be held while accessing an entry.
var extensionJSONCaches sync.Map // file path -> *extensionJSONCacheEntry

func extensionFileMu(path string) *sync.Mutex {
	mu, _ := extensionFileMus.LoadOrStore(path, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// writeExtensionFileLocked writes data via a temp file + rename so a reader
// never observes a torn write. Callers must hold extensionFileMu(path).
func writeExtensionFileLocked(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func extensionFileIdentityForPath(path string) (extensionFileIdentity, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return extensionFileIdentity{}, nil
		}
		return extensionFileIdentity{}, err
	}
	return extensionFileIdentity{
		exists:   true,
		size:     info.Size(),
		modified: info.ModTime().UnixNano(),
	}, nil
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneJSONMap(typed)
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = cloneJSONValue(item)
		}
		return result
	default:
		return typed
	}
}

func cloneJSONMap(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = cloneJSONValue(value)
	}
	return result
}

// readCachedJSONMapLocked returns an isolated snapshot. The path-specific
// mutex must be held, which makes the stat/load/update sequence coherent with
// writers from all runtimes.
func readCachedJSONMapLocked(
	path string,
	load func() (map[string]any, error),
) (map[string]any, error) {
	snapshot, err := cachedJSONMapLocked(path, load)
	if err != nil {
		return nil, err
	}
	return cloneJSONMap(snapshot), nil
}

// cachedJSONMapLocked returns the shared, read-only cache entry. Never expose
// it to a VM or mutate it. Callers must hold the path-specific file mutex.
func cachedJSONMapLocked(
	path string,
	load func() (map[string]any, error),
) (map[string]any, error) {
	identity, err := extensionFileIdentityForPath(path)
	if err != nil {
		return nil, err
	}
	if cached, ok := extensionJSONCaches.Load(path); ok {
		entry := cached.(*extensionJSONCacheEntry)
		if entry.identity == identity {
			return entry.snapshot, nil
		}
	}

	snapshot, err := load()
	if err != nil {
		return nil, err
	}
	extensionJSONCaches.Store(path, &extensionJSONCacheEntry{
		identity: identity,
		snapshot: snapshot,
	})
	return snapshot, nil
}

func readCachedJSONValueLocked(path, key string, load func() (map[string]any, error)) (any, bool, error) {
	snapshot, err := cachedJSONMapLocked(path, load)
	if err != nil {
		return nil, false, err
	}
	value, exists := snapshot[key]
	return cloneJSONValue(value), exists, nil
}

func storeCachedJSONMapLocked(path string, snapshot map[string]any) error {
	identity, err := extensionFileIdentityForPath(path)
	if err != nil {
		extensionJSONCaches.Delete(path)
		return err
	}
	extensionJSONCaches.Store(path, &extensionJSONCacheEntry{
		identity: identity,
		snapshot: cloneJSONMap(snapshot),
	})
	return nil
}

func (r *extensionRuntime) getStoragePath() string {
	return filepath.Join(r.dataDir, "storage.json")
}

func readJSONMapFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]any), nil
		}
		return nil, err
	}
	result := make(map[string]any)
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if result == nil {
		result = make(map[string]any)
	}
	return result, nil
}

func (r *extensionRuntime) mutateStorage(mutate func(map[string]any) bool) error {
	r.storageMu.RLock()
	closed := r.storageClosed
	r.storageMu.RUnlock()
	if closed {
		return fmt.Errorf("storage is closed")
	}

	path := r.getStoragePath()
	fileMu := extensionFileMu(path)
	fileMu.Lock()
	snapshot, err := readCachedJSONMapLocked(path, func() (map[string]any, error) {
		return readJSONMapFile(path)
	})
	if err == nil && mutate(snapshot) {
		var data []byte
		data, err = json.Marshal(snapshot)
		if err == nil {
			err = writeExtensionFileLocked(path, data)
		}
		if err == nil {
			err = storeCachedJSONMapLocked(path, snapshot)
		}
	}
	fileMu.Unlock()
	if err != nil {
		return err
	}

	r.storageMu.Lock()
	r.storageCache = snapshot
	r.storageMu.Unlock()
	return nil
}

func (r *extensionRuntime) flushStorageNow() error {
	// Mutations are persisted synchronously under the process-wide file lock.
	return nil
}

func (r *extensionRuntime) closeStorageFlusher() {
	r.storageMu.Lock()
	r.storageClosed = true
	r.storageMu.Unlock()
}

func (r *extensionRuntime) storageGet(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		return goja.Undefined()
	}

	key := call.Arguments[0].String()

	path := r.getStoragePath()
	fileMu := extensionFileMu(path)
	fileMu.Lock()
	value, exists, err := readCachedJSONValueLocked(path, key, func() (map[string]any, error) {
		return readJSONMapFile(path)
	})
	fileMu.Unlock()
	if err != nil {
		GoLog("[Extension:%s] Storage load error: %v\n", r.extensionID, err)
		return goja.Undefined()
	}

	if !exists {
		if len(call.Arguments) > 1 {
			return call.Arguments[1]
		}
		return goja.Undefined()
	}

	return r.vm.ToValue(value)
}

func (r *extensionRuntime) storageSet(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 2 {
		return r.vm.ToValue(false)
	}

	key := call.Arguments[0].String()
	value := call.Arguments[1].Export()

	if err := r.mutateStorage(func(storage map[string]any) bool {
		storage[key] = value
		return true
	}); err != nil {
		GoLog("[Extension:%s] Storage save error: %v\n", r.extensionID, err)
		return r.vm.ToValue(false)
	}

	return r.vm.ToValue(true)
}

func (r *extensionRuntime) storageRemove(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		return r.vm.ToValue(false)
	}

	key := call.Arguments[0].String()

	if err := r.mutateStorage(func(storage map[string]any) bool {
		if _, exists := storage[key]; !exists {
			return false
		}
		delete(storage, key)
		return true
	}); err != nil {
		GoLog("[Extension:%s] Storage save error: %v\n", r.extensionID, err)
		return r.vm.ToValue(false)
	}

	return r.vm.ToValue(true)
}

func (r *extensionRuntime) getCredentialsPath() string {
	return filepath.Join(r.dataDir, ".credentials.enc")
}

func (r *extensionRuntime) getSaltPath() string {
	return filepath.Join(r.dataDir, ".cred_salt")
}

func (r *extensionRuntime) getOrCreateSalt() ([]byte, error) {
	saltPath := r.getSaltPath()

	// Serialize concurrent runtimes: if two generated different salts, the
	// loser's credentials would become undecryptable.
	mu := extensionFileMu(saltPath)
	mu.Lock()
	defer mu.Unlock()

	salt, err := os.ReadFile(saltPath)
	if err == nil && len(salt) == 32 {
		return salt, nil
	}

	salt = make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("failed to generate salt: %w", err)
	}

	if err := writeExtensionFileLocked(saltPath, salt); err != nil {
		return nil, fmt.Errorf("failed to save salt: %w", err)
	}

	return salt, nil
}

func (r *extensionRuntime) getEncryptionKey() ([]byte, error) {
	return deriveExtensionStorageKey(r.extensionID, "credentials")
}

func (r *extensionRuntime) getLegacyEncryptionKey() ([]byte, error) {
	salt, err := r.getOrCreateSalt()
	if err != nil {
		return nil, err
	}

	combined := append([]byte(r.extensionID), salt...)
	hash := sha256.Sum256(combined)
	return hash[:], nil
}

func (r *extensionRuntime) readCredentialsFileLocked() (map[string]any, error) {
	data, err := os.ReadFile(r.getCredentialsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]any), nil
		}
		return nil, err
	}
	key, err := r.getEncryptionKey()
	if err != nil {
		return nil, fmt.Errorf("failed to get encryption key: %w", err)
	}
	decrypted, err := decryptAES(data, key)
	if err != nil {
		legacyKey, legacyKeyErr := r.getLegacyEncryptionKey()
		if legacyKeyErr != nil {
			return nil, fmt.Errorf("failed to decrypt credentials: %w", err)
		}
		decrypted, legacyKeyErr = decryptAES(data, legacyKey)
		if legacyKeyErr != nil {
			return nil, fmt.Errorf("failed to decrypt credentials: %w", err)
		}
		// Transparently replace the legacy extension-id-derived ciphertext while
		// the caller holds the per-file lock.
		migrated, migrateErr := encryptAES(decrypted, key)
		if migrateErr != nil {
			return nil, fmt.Errorf("failed to migrate credentials: %w", migrateErr)
		}
		if migrateErr = writeExtensionFileLocked(r.getCredentialsPath(), migrated); migrateErr != nil {
			return nil, fmt.Errorf("failed to migrate credentials: %w", migrateErr)
		}
	}
	creds := make(map[string]any)
	if err := json.Unmarshal(decrypted, &creds); err != nil {
		return nil, err
	}
	if creds == nil {
		creds = make(map[string]any)
	}
	return creds, nil
}

func (r *extensionRuntime) refreshCredentials() error {
	path := r.getCredentialsPath()
	fileMu := extensionFileMu(path)
	fileMu.Lock()
	snapshot, err := readCachedJSONMapLocked(path, r.readCredentialsFileLocked)
	fileMu.Unlock()
	if err != nil {
		return err
	}
	r.credentialsMu.Lock()
	r.credentialsCache = snapshot
	r.credentialsMu.Unlock()
	return nil
}

func (r *extensionRuntime) mutateCredentials(mutate func(map[string]any)) error {
	path := r.getCredentialsPath()
	fileMu := extensionFileMu(path)
	fileMu.Lock()
	snapshot, err := readCachedJSONMapLocked(path, r.readCredentialsFileLocked)
	if err == nil {
		mutate(snapshot)
		var data []byte
		data, err = json.Marshal(snapshot)
		if err == nil {
			var key []byte
			key, err = r.getEncryptionKey()
			if err == nil {
				data, err = encryptAES(data, key)
			}
		}
		if err == nil {
			err = writeExtensionFileLocked(path, data)
		}
		if err == nil {
			err = storeCachedJSONMapLocked(path, snapshot)
		}
	}
	fileMu.Unlock()
	if err != nil {
		return err
	}
	r.credentialsMu.Lock()
	r.credentialsCache = snapshot
	r.credentialsMu.Unlock()
	return nil
}

func (r *extensionRuntime) credentialsStore(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 2 {
		return r.jsError("key and value are required")
	}

	key := call.Arguments[0].String()
	value := call.Arguments[1].Export()

	if err := r.mutateCredentials(func(credentials map[string]any) {
		credentials[key] = value
	}); err != nil {
		GoLog("[Extension:%s] Credentials save error: %v\n", r.extensionID, err)
		return r.jsError("%s", err.Error())
	}

	return r.jsSuccess(nil)
}

func (r *extensionRuntime) credentialsGet(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		return goja.Undefined()
	}

	key := call.Arguments[0].String()

	if err := r.refreshCredentials(); err != nil {
		GoLog("[Extension:%s] Credentials load error: %v\n", r.extensionID, err)
		return goja.Undefined()
	}

	r.credentialsMu.RLock()
	value, exists := r.credentialsCache[key]
	r.credentialsMu.RUnlock()
	if !exists {
		if len(call.Arguments) > 1 {
			return call.Arguments[1]
		}
		return goja.Undefined()
	}

	return r.vm.ToValue(value)
}

func (r *extensionRuntime) credentialsRemove(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		return r.vm.ToValue(false)
	}

	key := call.Arguments[0].String()

	if err := r.mutateCredentials(func(credentials map[string]any) {
		delete(credentials, key)
	}); err != nil {
		GoLog("[Extension:%s] Credentials save error: %v\n", r.extensionID, err)
		return r.vm.ToValue(false)
	}

	return r.vm.ToValue(true)
}

func (r *extensionRuntime) credentialsHas(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		return r.vm.ToValue(false)
	}

	key := call.Arguments[0].String()

	if err := r.refreshCredentials(); err != nil {
		return r.vm.ToValue(false)
	}

	r.credentialsMu.RLock()
	_, exists := r.credentialsCache[key]
	r.credentialsMu.RUnlock()
	return r.vm.ToValue(exists)
}

func encryptAES(plaintext []byte, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

func decryptAES(ciphertext []byte, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}

	return plaintext, nil
}
