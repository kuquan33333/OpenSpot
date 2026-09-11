package gobackend

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
)

// allowPrivateNetworkAccess, when enabled, disables the SSRF guard that blocks
// requests resolving to private/local/loopback addresses. This is opt-in and
// intended for users who route the app's traffic through a local proxy or
// custom DNS (e.g. a local mirror of api.zarz.moe). Disabled by default.
var allowPrivateNetworkAccess atomic.Bool

// SetAllowPrivateNetwork toggles whether extensions and built-in network code
// are permitted to reach private/local network targets. Exposed to the Flutter
// layer via the platform bridge.
func SetAllowPrivateNetwork(allowed bool) {
	allowPrivateNetworkAccess.Store(allowed)
	if allowed {
		GoLog("[HTTP] Private/local network access ENABLED (SSRF guard relaxed)\n")
	} else {
		GoLog("[HTTP] Private/local network access disabled (default)\n")
	}
}

// IsPrivateNetworkAllowed reports the current state of the private-network guard.
func IsPrivateNetworkAllowed() bool {
	return allowPrivateNetworkAccess.Load()
}

const DefaultJSTimeout = 30 * time.Second

var (
	extensionAuthState   = make(map[string]*ExtensionAuthState)
	extensionAuthStateMu sync.RWMutex
)

type ExtensionAuthState struct {
	PendingAuthURL  string
	AuthCode        string
	AccessToken     string
	RefreshToken    string
	ExpiresAt       time.Time
	IsAuthenticated bool
	PKCEVerifier    string
	PKCEChallenge   string
}

type PendingAuthRequest struct {
	ExtensionID string
	AuthURL     string
	CallbackURL string
	State       string
	CreatedAt   time.Time
}

// Challenge URLs are short-lived; serving one past this age sends the user
// to an already-expired verification page.
const pendingAuthRequestTTL = 3 * time.Minute

var (
	pendingAuthRequests   = make(map[string]*PendingAuthRequest)
	pendingAuthStates     = make(map[string]string)
	pendingAuthRequestsMu sync.RWMutex
)

func newExtensionCallbackState() (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate callback state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

func registerPendingAuthRequest(request *PendingAuthRequest) error {
	if request == nil || strings.TrimSpace(request.ExtensionID) == "" {
		return fmt.Errorf("extension id is required")
	}
	if request.State == "" {
		state, err := newExtensionCallbackState()
		if err != nil {
			return err
		}
		request.State = state
	}
	if request.CreatedAt.IsZero() {
		request.CreatedAt = time.Now()
	}

	pendingAuthRequestsMu.Lock()
	if owner := pendingAuthStates[request.State]; owner != "" && owner != request.ExtensionID {
		ownerRequest := pendingAuthRequests[owner]
		sameChallenge := ownerRequest != nil &&
			ownerRequest.State == request.State &&
			ownerRequest.AuthURL == request.AuthURL &&
			ownerRequest.CallbackURL == request.CallbackURL &&
			ownerRequest.CreatedAt.Equal(request.CreatedAt)
		if !sameChallenge {
			pendingAuthRequestsMu.Unlock()
			return fmt.Errorf("callback state is already registered")
		}
	}
	if previous := pendingAuthRequests[request.ExtensionID]; previous != nil && previous.State != request.State {
		removePendingAuthRequestLocked(request.ExtensionID)
	}
	pendingAuthRequests[request.ExtensionID] = request
	if pendingAuthStates[request.State] == "" {
		pendingAuthStates[request.State] = request.ExtensionID
	}
	pendingAuthRequestsMu.Unlock()
	return nil
}

func removePendingAuthRequestLocked(extensionID string) {
	request := pendingAuthRequests[extensionID]
	delete(pendingAuthRequests, extensionID)
	if request == nil || pendingAuthStates[request.State] != extensionID {
		return
	}
	delete(pendingAuthStates, request.State)
	for candidateID, candidate := range pendingAuthRequests {
		if candidate != nil && candidate.State == request.State &&
			time.Since(candidate.CreatedAt) < pendingAuthRequestTTL {
			pendingAuthStates[request.State] = candidateID
			return
		}
	}
}

func removePendingAuthStateLocked(state string) {
	delete(pendingAuthStates, state)
	for extensionID, request := range pendingAuthRequests {
		if request != nil && request.State == state {
			delete(pendingAuthRequests, extensionID)
		}
	}
}

func resolveExtensionCallbackStateLocked(state string) (string, error) {
	extensionID := pendingAuthStates[state]
	request := pendingAuthRequests[extensionID]
	if extensionID == "" || request == nil || request.State != state ||
		time.Since(request.CreatedAt) >= pendingAuthRequestTTL {
		removePendingAuthStateLocked(state)
		return "", fmt.Errorf("callback state is invalid, expired, or already used")
	}
	return extensionID, nil
}

// ResolveExtensionCallbackState validates a callback nonce without consuming
// it. Callback handlers use this before an exchange so a transient exchange
// failure can still be retried with the same short-lived challenge.
func ResolveExtensionCallbackState(state string) (string, error) {
	state = strings.TrimSpace(state)
	if state == "" {
		return "", fmt.Errorf("callback state is required")
	}

	pendingAuthRequestsMu.Lock()
	extensionID, err := resolveExtensionCallbackStateLocked(state)
	pendingAuthRequestsMu.Unlock()
	return extensionID, err
}

func ConsumeExtensionCallbackState(state string) (string, error) {
	state = strings.TrimSpace(state)
	if state == "" {
		return "", fmt.Errorf("callback state is required")
	}

	pendingAuthRequestsMu.Lock()
	extensionID, err := resolveExtensionCallbackStateLocked(state)
	if err != nil {
		pendingAuthRequestsMu.Unlock()
		return "", err
	}
	removePendingAuthStateLocked(state)
	pendingAuthRequestsMu.Unlock()
	return extensionID, nil
}

func GetPendingAuthRequest(extensionID string) *PendingAuthRequest {
	pendingAuthRequestsMu.RLock()
	defer pendingAuthRequestsMu.RUnlock()
	return pendingAuthRequests[extensionID]
}

func ClearPendingAuthRequest(extensionID string) {
	pendingAuthRequestsMu.Lock()
	defer pendingAuthRequestsMu.Unlock()
	removePendingAuthRequestLocked(extensionID)
}

func SetExtensionAuthCode(extensionID string, authCode string) {
	extensionAuthStateMu.Lock()
	defer extensionAuthStateMu.Unlock()

	state, exists := extensionAuthState[extensionID]
	if !exists {
		state = &ExtensionAuthState{}
		extensionAuthState[extensionID] = state
	}
	state.AuthCode = authCode
}

func SetExtensionTokens(extensionID string, accessToken, refreshToken string, expiresAt time.Time) {
	extensionAuthStateMu.Lock()
	defer extensionAuthStateMu.Unlock()

	state, exists := extensionAuthState[extensionID]
	if !exists {
		state = &ExtensionAuthState{}
		extensionAuthState[extensionID] = state
	}
	state.AccessToken = accessToken
	state.RefreshToken = refreshToken
	state.ExpiresAt = expiresAt
	state.IsAuthenticated = accessToken != ""
}

type extensionRuntime struct {
	extensionID    string
	manifest       *ExtensionManifest
	settings       map[string]any
	httpClient     *http.Client
	downloadClient *http.Client
	cookieJar      http.CookieJar
	dataDir        string
	vm             *goja.Runtime

	activeDownloadMu     sync.RWMutex
	activeDownloadItemID string

	resolutionMu     sync.RWMutex
	resolutionBudget *resolutionBudget

	activeRequestMu sync.RWMutex
	activeRequestID string

	storageMu     sync.RWMutex
	storageCache  map[string]any
	storageClosed bool

	credentialsMu    sync.RWMutex
	credentialsCache map[string]any

	// Set when a signed-session call inside the current script invocation
	// required verification. The provider wrapper consumes it after the
	// script returns, so verification surfaces even when the extension
	// script swallowed the needsVerification response (issue: fallback
	// skipped provider B's challenge and failed outright).
	verificationMu          sync.Mutex
	verificationRequiredURL string
}

func (r *extensionRuntime) noteVerificationRequired(authURL string) {
	r.verificationMu.Lock()
	if authURL == "" {
		authURL = "pending"
	}
	r.verificationRequiredURL = authURL
	r.verificationMu.Unlock()
}

// consumeVerificationRequired returns the noted auth URL (or "pending") and
// clears the flag; "" means no verification was requested since the last
// consume.
func (r *extensionRuntime) consumeVerificationRequired() string {
	r.verificationMu.Lock()
	url := r.verificationRequiredURL
	r.verificationRequiredURL = ""
	r.verificationMu.Unlock()
	return url
}

type privateIPCacheEntry struct {
	isPrivate bool
	expiresAt time.Time
}

const (
	privateIPCacheTTL      = 5 * time.Minute
	privateIPErrorCacheTTL = 30 * time.Second
	maxPrivateIPCacheSize  = 1024
)

var (
	privateIPCache   = make(map[string]privateIPCacheEntry)
	privateIPCacheMu sync.RWMutex
)

func clearPrivateIPCache() {
	privateIPCacheMu.Lock()
	privateIPCache = make(map[string]privateIPCacheEntry)
	privateIPCacheMu.Unlock()
}

func newExtensionRuntime(ext *loadedExtension) *extensionRuntime {
	jar, _ := newSimpleCookieJar()

	runtime := &extensionRuntime{
		extensionID: ext.ID,
		manifest:    ext.Manifest,
		settings:    make(map[string]any),
		cookieJar:   jar,
		dataDir:     ext.DataDir,
		vm:          ext.VM,
	}

	runtime.httpClient = newExtensionHTTPClient(ext, jar, extensionHTTPTimeout(ext, 30*time.Second), true)
	runtime.downloadClient = newExtensionHTTPClient(ext, jar, DownloadTimeout, false)

	return runtime
}

func extensionHTTPTimeout(ext *loadedExtension, fallback time.Duration) time.Duration {
	if ext == nil || ext.Manifest == nil || ext.Manifest.Capabilities == nil {
		return fallback
	}

	raw, ok := ext.Manifest.Capabilities["networkTimeoutSeconds"]
	if !ok {
		return fallback
	}

	seconds := parseExtensionTimeoutSeconds(raw)
	if seconds <= 0 {
		return fallback
	}

	if seconds < 5 {
		seconds = 5
	}
	if seconds > 300 {
		seconds = 300
	}

	return time.Duration(seconds) * time.Second
}

func parseExtensionTimeoutSeconds(raw any) int {
	switch v := raw.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float32:
		return int(v)
	case float64:
		return int(v)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0
		}
		return parsed
	default:
		return 0
	}
}

func (r *extensionRuntime) setActiveDownloadItemID(itemID string) {
	r.activeDownloadMu.Lock()
	defer r.activeDownloadMu.Unlock()
	r.activeDownloadItemID = strings.TrimSpace(itemID)
}

func (r *extensionRuntime) clearActiveDownloadItemID() {
	r.activeDownloadMu.Lock()
	defer r.activeDownloadMu.Unlock()
	r.activeDownloadItemID = ""
}

func (r *extensionRuntime) getActiveDownloadItemID() string {
	r.activeDownloadMu.RLock()
	defer r.activeDownloadMu.RUnlock()
	return r.activeDownloadItemID
}

func (r *extensionRuntime) setActiveRequestID(requestID string) {
	r.activeRequestMu.Lock()
	defer r.activeRequestMu.Unlock()
	r.activeRequestID = strings.TrimSpace(requestID)
}

func (r *extensionRuntime) clearActiveRequestID() {
	r.activeRequestMu.Lock()
	defer r.activeRequestMu.Unlock()
	r.activeRequestID = ""
}

func (r *extensionRuntime) getActiveRequestID() string {
	r.activeRequestMu.RLock()
	defer r.activeRequestMu.RUnlock()
	return r.activeRequestID
}

func (r *extensionRuntime) bindDownloadCancelContext(req *http.Request) *http.Request {
	if req == nil {
		return nil
	}
	return req.WithContext(r.activeOperationContext(req.Context()))
}

// activeOperationContext is stable for the full extension operation. An
// http.Client with a finite Timeout derives a per-request child context and
// cancels it when that response body closes, so that request context must not
// be reused for provider retry delays between requests.
func (r *extensionRuntime) activeOperationContext(fallback context.Context) context.Context {
	if budget := r.currentResolutionBudget(); budget != nil {
		return budget.ctx
	}
	itemID := r.getActiveDownloadItemID()
	if itemID == "" {
		requestID := r.getActiveRequestID()
		if requestID == "" {
			if fallback != nil {
				return fallback
			}
			return context.Background()
		}
		return extensionRequestCancelContext(requestID)
	}
	return downloadCancelContext(itemID)
}

// downloadStallTimeout is how long a download may go without receiving a single
// byte before the stall watchdog aborts it. A dead radio mid-transfer otherwise
// blocks on Body.Read until the 24h client timeout with no error and no retry.
const downloadStallTimeout = 60 * time.Second

// stallWatchdog cancels an in-flight download when no data arrives within
// timeout. It wraps the request context in a child cancel so firing it does NOT
// set the user-cancel flag (isDownloadCancelled stays false) — a stall is a
// distinct, retryable condition. Call reset() after every successful Read and
// stop() when the transfer ends.
type stallWatchdog struct {
	cancel  context.CancelFunc
	timer   *time.Timer
	timeout time.Duration
	stalled atomic.Bool
}

func bindStallWatchdog(req *http.Request, timeout time.Duration) (*http.Request, *stallWatchdog) {
	ctx, cancel := context.WithCancel(req.Context())
	w := &stallWatchdog{cancel: cancel, timeout: timeout}
	w.timer = time.AfterFunc(timeout, func() {
		w.stalled.Store(true)
		cancel()
	})
	return req.WithContext(ctx), w
}

func (w *stallWatchdog) reset() { w.timer.Reset(w.timeout) }

// stop halts the timer and releases the child context so a completed download
// leaks neither a pending timer nor a live cancel func.
func (w *stallWatchdog) stop() {
	w.timer.Stop()
	w.cancel()
}

func newExtensionHTTPClient(ext *loadedExtension, jar http.CookieJar, timeout time.Duration, compressResponses bool) *http.Client {
	// Extension sandbox enforces HTTPS-only domains. Do not apply global
	// allow_http scheme downgrade here, because some extension APIs (e.g.
	// spotify-web) will redirect http -> https and can end up in 301 loops.
	// API calls can use response compression for faster metadata/search loads,
	// while media downloads keep identity transfer semantics for progress/streaming.
	transport := sharedTransport
	if compressResponses {
		transport = extensionAPITransport
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		Jar:       jar,
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" &&
			!(req.URL.Scheme == "http" && ext.Manifest.Permissions.AllowHTTP) {
			GoLog("[Extension:%s] Redirect blocked: non-https scheme '%s'\n", ext.ID, req.URL.Scheme)
			return fmt.Errorf("redirect blocked: only https is allowed")
		}

		domain := req.URL.Hostname()
		if domain == "" {
			GoLog("[Extension:%s] Redirect blocked: missing hostname\n", ext.ID)
			return fmt.Errorf("redirect blocked: hostname is required")
		}
		if !ext.Manifest.IsDomainAllowed(domain) {
			GoLog("[Extension:%s] Redirect blocked: domain '%s' not in allowed list\n", ext.ID, domain)
			return &RedirectBlockedError{Domain: domain}
		}
		// The transport resolves and pins every redirect target before dialing.
		// Reject literals/local aliases here without doing a second, uncancellable
		// DNS lookup on the redirect path.
		if isPrivateIPLiteralOrLocal(domain) {
			GoLog("[Extension:%s] Redirect blocked: private IP '%s'\n", ext.ID, domain)
			return &RedirectBlockedError{Domain: domain, IsPrivate: true}
		}
		if len(via) >= 10 {
			return http.ErrUseLastResponse
		}
		return nil
	}
	return client
}

type RedirectBlockedError struct {
	Domain    string
	IsPrivate bool
}

func (e *RedirectBlockedError) Error() string {
	if e.IsPrivate {
		return "redirect blocked: private/local network access denied"
	}
	return "redirect blocked: domain '" + e.Domain + "' not in allowed list"
}

func isPrivateIP(host string) bool {
	// Opt-in escape hatch: when the user has enabled private/local network
	// access, treat every host as public so local proxies / custom DNS work.
	if allowPrivateNetworkAccess.Load() {
		return false
	}

	hostLower := strings.ToLower(strings.TrimSpace(host))
	if hostLower == "" {
		return false
	}
	if isPrivateIPLiteralOrLocal(hostLower) {
		return true
	}

	if cached, ok := getPrivateIPCache(hostLower); ok {
		return cached
	}

	ips, err := net.LookupIP(hostLower)
	if err != nil {
		// Defer the final decision to dialWithDoHFallback. It resolves and filters
		// every concrete address (including DoH answers) before opening a socket.
		setPrivateIPCache(hostLower, false, privateIPErrorCacheTTL)
		return false
	}

	isPrivate := slices.ContainsFunc(ips, isPrivateIPAddr)

	setPrivateIPCache(hostLower, isPrivate, privateIPCacheTTL)
	return isPrivate
}

// isPrivateIPLiteralOrLocal performs the validation that does not require DNS.
// Extension HTTP requests use this before dispatch; dialWithDoHFallback remains
// the authoritative hostname check because it filters and pins the exact DNS
// answers used by the socket, closing the rebinding window without a duplicate
// lookup.
func isPrivateIPLiteralOrLocal(host string) bool {
	if allowPrivateNetworkAccess.Load() {
		return false
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	if host == "localhost" || strings.HasSuffix(host, ".local") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return isPrivateIPAddr(ip)
	}
	return false
}

func getPrivateIPCache(host string) (bool, bool) {
	now := time.Now()

	privateIPCacheMu.RLock()
	entry, exists := privateIPCache[host]
	privateIPCacheMu.RUnlock()
	if !exists {
		return false, false
	}

	if now.Before(entry.expiresAt) {
		return entry.isPrivate, true
	}

	privateIPCacheMu.Lock()
	delete(privateIPCache, host)
	privateIPCacheMu.Unlock()
	return false, false
}

func setPrivateIPCache(host string, isPrivate bool, ttl time.Duration) {
	expiresAt := time.Now().Add(ttl)

	privateIPCacheMu.Lock()
	if len(privateIPCache) >= maxPrivateIPCacheSize {
		now := time.Now()
		for key, entry := range privateIPCache {
			if now.After(entry.expiresAt) {
				delete(privateIPCache, key)
			}
		}
		if len(privateIPCache) >= maxPrivateIPCacheSize {
			privateIPCache = make(map[string]privateIPCacheEntry)
		}
	}
	privateIPCache[host] = privateIPCacheEntry{
		isPrivate: isPrivate,
		expiresAt: expiresAt,
	}
	privateIPCacheMu.Unlock()
}

func isPrivateIPAddr(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() {
		return true
	}
	if !ip.IsGlobalUnicast() {
		return true
	}
	return false
}

type simpleCookieJar struct {
	mu  sync.RWMutex
	jar *cookiejar.Jar
}

func newSimpleCookieJar() (*simpleCookieJar, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &simpleCookieJar{jar: jar}, nil
}

func (j *simpleCookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.RLock()
	defer j.mu.RUnlock()
	j.jar.SetCookies(u, cookies)
}

func (j *simpleCookieJar) Cookies(u *url.URL) []*http.Cookie {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.jar.Cookies(u)

}

func (j *simpleCookieJar) Clear() {
	jar, _ := cookiejar.New(nil)
	j.mu.Lock()
	j.jar = jar
	j.mu.Unlock()
}

func (r *extensionRuntime) SetSettings(settings map[string]any) {
	r.settings = settings
}

func (r *extensionRuntime) RegisterAPIs(vm *goja.Runtime) {
	r.vm = vm

	httpObj := vm.NewObject()
	httpObj.Set("get", r.httpGet)
	httpObj.Set("post", r.httpPost)
	httpObj.Set("put", r.httpPut)
	httpObj.Set("delete", r.httpDelete)
	httpObj.Set("patch", r.httpPatch)
	httpObj.Set("request", r.httpRequest)
	httpObj.Set("clearCookies", r.httpClearCookies)
	vm.Set("http", httpObj)

	if r.manifest != nil && r.manifest.Permissions.Storage {
		storageObj := vm.NewObject()
		storageObj.Set("get", r.storageGet)
		storageObj.Set("set", r.storageSet)
		storageObj.Set("remove", r.storageRemove)
		vm.Set("storage", storageObj)

		credentialsObj := vm.NewObject()
		credentialsObj.Set("store", r.credentialsStore)
		credentialsObj.Set("get", r.credentialsGet)
		credentialsObj.Set("remove", r.credentialsRemove)
		credentialsObj.Set("has", r.credentialsHas)
		vm.Set("credentials", credentialsObj)
	}

	if r.manifest != nil && r.manifest.Permissions.Storage {
		authObj := vm.NewObject()
		authObj.Set("openAuthUrl", r.authOpenUrl)
		authObj.Set("getAuthCode", r.authGetCode)
		authObj.Set("setAuthCode", r.authSetCode)
		authObj.Set("clearAuth", r.authClear)
		authObj.Set("isAuthenticated", r.authIsAuthenticated)
		authObj.Set("getTokens", r.authGetTokens)
		authObj.Set("generatePKCE", r.authGeneratePKCE)
		authObj.Set("getPKCE", r.authGetPKCE)
		authObj.Set("startOAuthWithPKCE", r.authStartOAuthWithPKCE)
		authObj.Set("exchangeCodeWithPKCE", r.authExchangeCodeWithPKCE)
		vm.Set("auth", authObj)

		if r.manifest.SignedSession != nil {
			sessionObj := vm.NewObject()
			sessionObj.Set("signedFetch", r.signedSessionFetch)
			sessionObj.Set("completeGrant", r.signedSessionCompleteGrant)
			sessionObj.Set("status", r.signedSessionStatus)
			sessionObj.Set("clear", r.signedSessionClear)
			vm.Set("session", sessionObj)
		}
	}

	if r.manifest != nil && r.manifest.Permissions.File {
		fileObj := vm.NewObject()
		fileObj.Set("download", r.fileDownload)
		fileObj.Set("downloadSegments", r.fileDownloadSegments)
		fileObj.Set("exists", r.fileExists)
		fileObj.Set("delete", r.fileDelete)
		fileObj.Set("read", r.fileRead)
		fileObj.Set("readBytes", r.fileReadBytes)
		fileObj.Set("write", r.fileWrite)
		fileObj.Set("writeBytes", r.fileWriteBytes)
		fileObj.Set("copy", r.fileCopy)
		fileObj.Set("move", r.fileMove)
		fileObj.Set("getSize", r.fileGetSize)
		fileObj.Set("transformPatternedBlocks", r.fileTransformPatternedBlocks)
		vm.Set("file", fileObj)

		ffmpegObj := vm.NewObject()
		if r.manifest.HasCapability("rawFfmpeg") {
			ffmpegObj.Set("execute", r.ffmpegExecute)
		}
		ffmpegObj.Set("getInfo", r.ffmpegGetInfo)
		ffmpegObj.Set("convert", r.ffmpegConvert)
		vm.Set("ffmpeg", ffmpegObj)
	}

	matchingObj := vm.NewObject()
	matchingObj.Set("compareStrings", r.matchingCompareStrings)
	matchingObj.Set("compareDuration", r.matchingCompareDuration)
	matchingObj.Set("normalizeString", r.matchingNormalizeString)
	vm.Set("matching", matchingObj)

	utilsObj := vm.NewObject()
	utilsObj.Set("base64Encode", r.base64Encode)
	utilsObj.Set("base64Decode", r.base64Decode)
	utilsObj.Set("md5", r.md5Hash)
	utilsObj.Set("sha256", r.sha256Hash)
	utilsObj.Set("hmacSHA256", r.hmacSHA256)
	utilsObj.Set("hmacSHA256Base64", r.hmacSHA256Base64)
	utilsObj.Set("hmacSHA1", r.hmacSHA1)
	utilsObj.Set("parseJSON", r.parseJSON)
	utilsObj.Set("stringifyJSON", r.stringifyJSON)
	utilsObj.Set("encrypt", r.cryptoEncrypt)
	utilsObj.Set("decrypt", r.cryptoDecrypt)
	utilsObj.Set("encryptBlockCipher", r.encryptBlockCipher)
	utilsObj.Set("decryptBlockCipher", r.decryptBlockCipher)
	utilsObj.Set("decryptCTRSegments", r.decryptCTRSegments)
	utilsObj.Set("generateKey", r.cryptoGenerateKey)
	utilsObj.Set("randomUserAgent", r.randomUserAgent)
	utilsObj.Set("appVersion", r.appVersion)
	utilsObj.Set("appUserAgent", r.appUserAgent)
	utilsObj.Set("sleep", r.sleep)
	utilsObj.Set("getResolutionRemainingMs", r.getResolutionRemainingMs)
	utilsObj.Set("isDownloadCancelled", r.isDownloadCancelled)
	utilsObj.Set("isRequestCancelled", r.isRequestCancelled)
	utilsObj.Set("setDownloadStatus", r.setDownloadStatus)
	vm.Set("utils", utilsObj)

	logObj := vm.NewObject()
	logObj.Set("debug", r.logDebug)
	logObj.Set("info", r.logInfo)
	logObj.Set("warn", r.logWarn)
	logObj.Set("error", r.logError)
	vm.Set("log", logObj)

	vm.Set("fetch", r.fetchPolyfill)

	vm.Set("atob", r.atobPolyfill)
	vm.Set("btoa", r.btoaPolyfill)

	r.registerTextEncoderDecoder(vm)

	r.registerURLClass(vm)

	r.registerJSONGlobal(vm)
}
