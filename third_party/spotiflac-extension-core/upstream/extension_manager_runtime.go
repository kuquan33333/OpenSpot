package gobackend

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dop251/goja"
)

// extensionLifecycleTimeout bounds extension code that runs outside a normal
// provider/action request. A lifecycle callback is still arbitrary extension
// JavaScript, so it needs the same interrupt/quarantine contract as a regular
// invocation. Tests may shorten this value; production keeps the normal JS
// request budget.
var extensionLifecycleTimeout = DefaultJSTimeout

func runExtensionLifecycleCall(
	vm *goja.Runtime,
	call func() (goja.Value, error),
) (goja.Value, error) {
	return runGojaCallWithTimeoutAndRecover(vm, call, extensionLifecycleTimeout)
}

// discardLifecycleRuntime releases a runtime whose lifecycle execution failed.
// An interrupted Go callback may still be using the VM after the timeout
// helper returns, so unsafe runtimes must remain quarantined until the helper's
// completion signal closes. Safe failures can be closed immediately.
func discardLifecycleRuntime(
	ext *loadedExtension,
	vm *goja.Runtime,
	runtime *extensionRuntime,
	err error,
) {
	if IsRuntimeUnsafeError(err) {
		if ext != nil && ext.VM == vm {
			quarantineRuntimeLocked(ext, vm, err)
		} else {
			registerQuarantinedRuntime(ext, runtime, runtimeCompletion(err))
		}
		return
	}

	if runtime != nil {
		runtime.closeStorageFlusher()
	}
	if ext != nil && ext.VM == vm {
		ext.VM = nil
		ext.runtime = nil
		ext.indexProgram = nil
		ext.initialized = false
	}
}

func initializeVMLocked(ext *loadedExtension) error {
	ext.VM = nil
	ext.runtime = nil
	ext.indexProgram = nil
	ext.initialized = false
	vm := goja.New()
	ext.VM = vm

	indexPath := filepath.Join(ext.SourceDir, "index.js")
	jsCode, err := os.ReadFile(indexPath)
	if err != nil {
		ext.VM = nil
		return fmt.Errorf("failed to read index.js: %w", err)
	}
	indexProgram, err := goja.Compile(indexPath, string(jsCode), false)
	if err != nil {
		ext.VM = nil
		return fmt.Errorf("failed to compile extension code: %w", err)
	}
	ext.indexProgram = indexProgram

	runtime := newExtensionRuntime(ext)
	ext.runtime = runtime
	runtime.RegisterAPIs(vm)
	runtime.RegisterGoBackendAPIs(vm)

	console := vm.NewObject()
	console.Set("log", func(call goja.FunctionCall) goja.Value {
		GoLog("[Extension:%s] %s\n", ext.ID, formatExtensionLogArgs(call.Arguments))
		return goja.Undefined()
	})
	vm.Set("console", console)

	var registeredExtension goja.Value
	vm.Set("registerExtension", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) > 0 {
			registeredExtension = call.Arguments[0]
			vm.Set("extension", call.Arguments[0])
		}
		return goja.Undefined()
	})

	_, err = runExtensionLifecycleCall(vm, func() (goja.Value, error) {
		return vm.RunProgram(indexProgram)
	})
	if err != nil {
		discardLifecycleRuntime(ext, vm, runtime, err)
		return fmt.Errorf("failed to execute extension code: %w", err)
	}

	if registeredExtension == nil || goja.IsUndefined(registeredExtension) {
		discardLifecycleRuntime(ext, vm, runtime, fmt.Errorf("extension did not call registerExtension()"))
		return fmt.Errorf("extension did not call registerExtension()")
	}

	return nil
}

func newIsolatedExtensionRuntime(ext *loadedExtension) (*goja.Runtime, *extensionRuntime, error) {
	vm := goja.New()

	indexProgram := ext.indexProgram
	if indexProgram == nil {
		indexPath := filepath.Join(ext.SourceDir, "index.js")
		jsCode, err := os.ReadFile(indexPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read index.js: %w", err)
		}
		indexProgram, err = goja.Compile(indexPath, string(jsCode), false)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to compile extension code: %w", err)
		}
	}

	runtime := &extensionRuntime{
		extensionID: ext.ID,
		manifest:    ext.Manifest,
		settings:    make(map[string]any),
		cookieJar:   nil,
		dataDir:     ext.DataDir,
		vm:          vm,
	}
	if ext.runtime != nil && ext.runtime.cookieJar != nil {
		runtime.cookieJar = ext.runtime.cookieJar
	} else {
		jar, _ := newSimpleCookieJar()
		runtime.cookieJar = jar
	}
	runtime.httpClient = newExtensionHTTPClient(ext, runtime.cookieJar, extensionHTTPTimeout(ext, 30*time.Second), true)
	runtime.downloadClient = newExtensionHTTPClient(ext, runtime.cookieJar, DownloadTimeout, false)
	runtime.RegisterAPIs(vm)
	runtime.RegisterGoBackendAPIs(vm)

	console := vm.NewObject()
	console.Set("log", func(call goja.FunctionCall) goja.Value {
		GoLog("[Extension:%s] %s\n", ext.ID, formatExtensionLogArgs(call.Arguments))
		return goja.Undefined()
	})
	vm.Set("console", console)

	var registeredExtension goja.Value
	vm.Set("registerExtension", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) > 0 {
			registeredExtension = call.Arguments[0]
			vm.Set("extension", call.Arguments[0])
		}
		return goja.Undefined()
	})

	if _, err := runExtensionLifecycleCall(vm, func() (goja.Value, error) {
		return vm.RunProgram(indexProgram)
	}); err != nil {
		discardLifecycleRuntime(ext, vm, runtime, err)
		return nil, nil, fmt.Errorf("failed to execute extension code: %w", err)
	}

	if registeredExtension == nil || goja.IsUndefined(registeredExtension) {
		discardLifecycleRuntime(ext, vm, runtime, fmt.Errorf("extension did not call registerExtension()"))
		return nil, nil, fmt.Errorf("extension did not call registerExtension()")
	}

	settings := getExtensionInitSettings(ext.ID)
	if len(settings) > 0 {
		if err := initializeExtensionRuntimeWithSettings(vm, ext.ID, settings); err != nil {
			discardLifecycleRuntime(ext, vm, runtime, err)
			return nil, nil, err
		}
	}

	return vm, runtime, nil
}

// A goja runtime plus an executed extension program is several MB of live
// heap; rebuilding one per download multiplies that by the number of tracks.
// Extensions already serve many calls on the persistent shared VM, so reusing
// an initialized isolated runtime for consecutive downloads is the same
// lifecycle contract.
const maxIdleIsolatedRuntimes = 1

// acquireIsolatedExtensionRuntime pops an idle pooled runtime or builds one.
func acquireIsolatedExtensionRuntime(ext *loadedExtension) (*goja.Runtime, *extensionRuntime, error) {
	if hasQuarantinedRuntime(ext) {
		return nil, nil, fmt.Errorf("extension runtime is still stopping after an unresponsive request")
	}
	ext.isolatedPoolMu.Lock()
	if n := len(ext.isolatedPool); n > 0 {
		handle := ext.isolatedPool[n-1]
		// Reslicing alone retains this handle in the backing array. If the
		// borrowed VM is retired after an error, its heap would stay alive.
		ext.isolatedPool[n-1] = nil
		ext.isolatedPool = ext.isolatedPool[:n-1]
		ext.isolatedPoolMu.Unlock()
		return handle.vm, handle.runtime, nil
	}
	ext.isolatedPoolMu.Unlock()

	ext.VMMu.Lock()
	defer ext.VMMu.Unlock()
	if hasQuarantinedRuntime(ext) {
		return nil, nil, fmt.Errorf("extension runtime is still stopping after an unresponsive request")
	}
	return newIsolatedExtensionRuntime(ext)
}

// releaseIsolatedExtensionRuntime pools a healthy runtime for reuse or tears
// it down. Pass healthy=false after an interrupt/timeout/script error, whose
// VM state can't be trusted for reuse.
func releaseIsolatedExtensionRuntime(
	ext *loadedExtension,
	vm *goja.Runtime,
	runtime *extensionRuntime,
	healthy, cleanupSafe bool,
	unsafeDone <-chan struct{},
) {
	if !cleanupSafe {
		registerQuarantinedRuntime(ext, runtime, unsafeDone)
		return
	}

	if runtime != nil {
		if err := runtime.flushStorageNow(); err != nil {
			GoLog("[Extension:%s] isolated download storage flush failed: %v\n", ext.ID, err)
		}
	}

	if healthy && vm != nil && runtime != nil && ext.Enabled {
		ext.isolatedPoolMu.Lock()
		if len(ext.isolatedPool) < maxIdleIsolatedRuntimes {
			ext.isolatedPool = append(ext.isolatedPool, &isolatedRuntimeHandle{vm: vm, runtime: runtime})
			ext.isolatedPoolMu.Unlock()
			return
		}
		ext.isolatedPoolMu.Unlock()
	}

	if cleanupSafe {
		cleanupErr := runCleanupOnVM(vm)
		if IsRuntimeUnsafeError(cleanupErr) {
			registerQuarantinedRuntime(ext, runtime, runtimeCompletion(cleanupErr))
			return
		}
		if cleanupErr != nil {
			GoLog("[Extension:%s] isolated download cleanup failed: %v\n", ext.ID, cleanupErr)
		}
	}
	if runtime != nil {
		runtime.closeStorageFlusher()
	}
}

func hasQuarantinedRuntime(ext *loadedExtension) bool {
	if ext == nil {
		return false
	}
	ext.quarantineMu.Lock()
	defer ext.quarantineMu.Unlock()
	return ext.quarantinedRuntimes > 0
}

// registerQuarantinedRuntime keeps the extension gated until the interrupted
// goroutine actually exits. A Go goroutine cannot be killed safely, so allowing
// a replacement VM immediately would let a broken extension accumulate an
// unbounded number of runtimes. The runtime is touched only after completion.
func registerQuarantinedRuntime(ext *loadedExtension, runtime *extensionRuntime, done <-chan struct{}) {
	if ext == nil {
		return
	}
	ext.quarantineMu.Lock()
	ext.quarantinedRuntimes++
	ext.quarantineMu.Unlock()

	if done == nil {
		GoLog("[Extension:%s] quarantined runtime has no completion signal; keeping extension gated\n", ext.ID)
		return
	}
	go func() {
		<-done
		if runtime != nil {
			runtime.closeStorageFlusher()
		}
		ext.quarantineMu.Lock()
		if ext.quarantinedRuntimes > 0 {
			ext.quarantinedRuntimes--
		}
		ext.quarantineMu.Unlock()
	}()
}

// quarantineRuntimeLocked detaches a VM that remained busy after interrupt.
// The caller holds VMMu. Touching or cleaning up that VM would race its stuck
// goroutine; replacement calls remain gated until that goroutine exits.
func quarantineRuntimeLocked(ext *loadedExtension, vm *goja.Runtime, err error) {
	if ext == nil || ext.VM != vm {
		return
	}
	runtime := ext.runtime
	ext.VM = nil
	ext.runtime = nil
	ext.initialized = false
	ext.Error = "extension runtime was quarantined after an unresponsive script"
	registerQuarantinedRuntime(ext, runtime, runtimeCompletion(err))
}

// drainIsolatedRuntimePool tears down idle isolated runtimes. Called on
// extension teardown and on app-wide memory release.
func drainIsolatedRuntimePool(ext *loadedExtension) {
	ext.isolatedPoolMu.Lock()
	pool := ext.isolatedPool
	ext.isolatedPool = nil
	ext.isolatedPoolMu.Unlock()

	for _, handle := range pool {
		cleanupErr := runCleanupOnVM(handle.vm)
		if IsRuntimeUnsafeError(cleanupErr) {
			registerQuarantinedRuntime(ext, handle.runtime, runtimeCompletion(cleanupErr))
			continue
		}
		if cleanupErr != nil {
			GoLog("[Extension:%s] isolated pool cleanup failed: %v\n", ext.ID, cleanupErr)
		}
		if handle.runtime != nil {
			if err := handle.runtime.flushStorageNow(); err != nil {
				GoLog("[Extension:%s] isolated pool storage flush failed: %v\n", ext.ID, err)
			}
			handle.runtime.closeStorageFlusher()
		}
	}
}

// drainAllIsolatedRuntimePools releases every extension's idle isolated
// runtimes (memory-pressure hook).
func drainAllIsolatedRuntimePools() {
	m := getExtensionManager()
	m.mu.RLock()
	exts := make([]*loadedExtension, 0, len(m.extensions))
	for _, ext := range m.extensions {
		exts = append(exts, ext)
	}
	m.mu.RUnlock()

	for _, ext := range exts {
		drainIsolatedRuntimePool(ext)
	}
}

func (m *extensionManager) initializeVM(ext *loadedExtension) error {
	ext.VMMu.Lock()
	defer ext.VMMu.Unlock()
	return initializeVMLocked(ext)
}

func initializeExtensionRuntimeWithSettings(
	vm *goja.Runtime,
	extensionID string,
	settings map[string]any,
) error {
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("failed to save settings")
	}

	script := fmt.Sprintf(`
		(function() {
			var settings = %s;
			if (typeof extension !== 'undefined' && typeof extension.initialize === 'function') {
				try {
					extension.initialize(settings);
					return { success: true };
				} catch (e) {
					return { success: false, error: e.toString() };
				}
			}
			return { success: true, message: 'no initialize function' };
		})()
	`, string(settingsJSON))

	result, err := runExtensionLifecycleCall(vm, func() (goja.Value, error) {
		return vm.RunString(script)
	})
	if err != nil {
		GoLog("[Extension] Initialize error for %s: %v\n", extensionID, err)
		return err
	}

	if result != nil && !goja.IsUndefined(result) {
		exported := result.Export()
		if resultMap, ok := exported.(map[string]any); ok {
			if success, ok := resultMap["success"].(bool); ok && !success {
				errMsg := "unknown error"
				if e, ok := resultMap["error"].(string); ok {
					errMsg = e
				}
				GoLog("[Extension] Initialize failed for %s: %s\n", extensionID, errMsg)
				return fmt.Errorf("initialize failed: %s", errMsg)
			}
		}
	}

	return nil
}

func initializeExtensionWithSettingsLocked(
	ext *loadedExtension,
	settings map[string]any,
) error {
	if ext.VM == nil {
		return fmt.Errorf("extension failed to load: please reinstall the extension")
	}

	if err := initializeExtensionRuntimeWithSettings(ext.VM, ext.ID, settings); err != nil {
		ext.Error = err.Error()
		ext.Enabled = false
		if IsRuntimeUnsafeError(err) {
			quarantineRuntimeLocked(ext, ext.VM, err)
		}
		return err
	}

	ext.initialized = true
	GoLog("[Extension] Initialized %s\n", ext.ID)
	return nil
}

func runCleanupLocked(ext *loadedExtension) error {
	if ext.VM != nil {
		if err := runCleanupOnVM(ext.VM); err != nil {
			return err
		}
		if ext.VM.Get("extension") != nil {
			GoLog("[Extension] Cleanup called for %s\n", ext.ID)
		}
	}
	return nil
}

func runCleanupOnVM(vm *goja.Runtime) error {
	if vm == nil {
		return nil
	}

	script := `
		(function() {
			if (typeof extension !== 'undefined' && typeof extension.cleanup === 'function') {
				try {
					extension.cleanup();
					return { success: true };
				} catch (e) {
					return { success: false, error: e.toString() };
				}
			}
			return { success: true, message: 'no cleanup function' };
		})()
	`

	result, err := runExtensionLifecycleCall(vm, func() (goja.Value, error) {
		return vm.RunString(script)
	})
	if err != nil {
		return err
	}

	if result != nil && !goja.IsUndefined(result) {
		exported := result.Export()
		if resultMap, ok := exported.(map[string]any); ok {
			if success, ok := resultMap["success"].(bool); ok && !success {
				errMsg := "unknown error"
				if e, ok := resultMap["error"].(string); ok {
					errMsg = e
				}
				return fmt.Errorf("cleanup failed: %s", errMsg)
			}
		}
	}

	return nil
}

func teardownVMLocked(ext *loadedExtension) {
	drainIsolatedRuntimePool(ext)
	// Preserve writes made before cleanup even when the cleanup callback becomes
	// unresponsive and its VM has to remain quarantined.
	if ext.runtime != nil {
		if err := ext.runtime.flushStorageNow(); err != nil {
			GoLog("[Extension] Failed to flush storage before cleanup for %s: %v\n", ext.ID, err)
		}
	}
	vm := ext.VM
	if err := runCleanupLocked(ext); err != nil {
		GoLog("[Extension] Error calling cleanup for %s: %v\n", ext.ID, err)
		if IsRuntimeUnsafeError(err) {
			quarantineRuntimeLocked(ext, vm, err)
			return
		}
	}
	if ext.runtime != nil {
		if err := ext.runtime.flushStorageNow(); err != nil {
			GoLog("[Extension] Failed to flush storage for %s: %v\n", ext.ID, err)
		}
		ext.runtime.closeStorageFlusher()
	}
	ext.runtime = nil
	ext.VM = nil
	ext.initialized = false
}
