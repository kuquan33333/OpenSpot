package gobackend

// Directly adapted from SpotiFLAC-Mobile go_backend/extension_signed_session.go.
// This vendored copy keeps the signed-session protocol behavior required by the
// extension runtime while remaining self-contained inside the OpenSpot snapshot.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
)

const (
	signedSessionRefreshSkew               = time.Hour
	signedSessionExchangeMaxAttempts       = 3
	signedSessionMaxRetryAfter             = 5 * time.Minute
	signedSessionMaxSessionRetries         = 1
	signedSessionMaxProviderRetries        = 2
	signedSessionProviderRetryDelay        = time.Second
	signedSessionExchangeTimeout           = DefaultJSTimeout
	signedSessionResponseBodyLimit   int64 = 8 << 20
)

var (
	pendingSignedSessionGrants    = make(map[string]string)
	pendingSignedSessionGrantsMu  sync.Mutex
	signedSessionCoordinators     sync.Map
	signedSessionRetryWaitContext = sleepRetry
	signedSessionProviderWait     = sleepRetry
	signedSessionRequestNow       = time.Now
)

var sessionHintPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

type signedSessionHints struct {
	Default string            `json:"d"`
	Values  map[string]string `json:"s"`
}

var signedSessionHintState = struct {
	sync.RWMutex
	state signedSessionHints
}{
	state: signedSessionHints{Values: map[string]string{}},
}

func SetRuntimeState(raw string) {
	next := signedSessionHints{Values: map[string]string{}}
	if err := json.Unmarshal([]byte(raw), &next); err != nil {
		next = signedSessionHints{Values: map[string]string{}}
	}
	next.Default = normalizeSessionHint(next.Default)
	values := make(map[string]string, len(next.Values))
	for key, value := range next.Values {
		key = filepath.Base(strings.TrimSpace(key))
		value = normalizeSessionHint(value)
		if key != "" && key != "." && value != "" {
			values[key] = value
		}
	}
	next.Values = values

	signedSessionHintState.Lock()
	signedSessionHintState.state = next
	signedSessionHintState.Unlock()
}

func normalizeSessionHint(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if !sessionHintPattern.MatchString(value) {
		return ""
	}
	return value
}

func signedSessionHintFor(path string) string {
	key := filepath.Base(strings.TrimSpace(path))
	signedSessionHintState.RLock()
	defer signedSessionHintState.RUnlock()
	if value := signedSessionHintState.state.Values[key]; value != "" {
		return value
	}
	return signedSessionHintState.state.Default
}

type signedSessionCoordinator struct {
	mu sync.Mutex

	authURL             string
	callbackURL         string
	callbackState       string
	challengeCreatedAt  time.Time
	pendingExtensionIDs map[string]struct{}
	completedGrantHash  string
	blockedGeneration   string
	clearGeneration     uint64

	exchangeInFlight bool
	exchangeDone     chan struct{}

	bootstrapInFlight bool
	bootstrapDone     chan struct{}
	bootstrapErr      error

	refreshInFlight bool
	refreshDone     chan struct{}
	refreshErr      error
}

func (c *signedSessionCoordinator) beginExchange(ctx context.Context) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		c.mu.Lock()
		if !c.exchangeInFlight {
			c.exchangeInFlight = true
			c.exchangeDone = make(chan struct{})
			done := c.exchangeDone
			c.mu.Unlock()
			return func() {
				c.mu.Lock()
				if c.exchangeInFlight && c.exchangeDone == done {
					c.exchangeInFlight = false
					c.exchangeDone = nil
					close(done)
				}
				c.mu.Unlock()
			}, nil
		}
		done := c.exchangeDone
		c.mu.Unlock()

		select {
		case <-done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (r *extensionRuntime) signedSessionCoordinator(config SignedSessionConfig) (*signedSessionCoordinator, error) {
	path, err := r.signedSessionFilePath(config)
	if err != nil {
		return nil, err
	}
	value, _ := signedSessionCoordinators.LoadOrStore(path, &signedSessionCoordinator{})
	return value.(*signedSessionCoordinator), nil
}

func (c *signedSessionCoordinator) clearChallenge() {
	for extensionID := range c.pendingExtensionIDs {
		ClearPendingAuthRequest(extensionID)
	}
	c.authURL = ""
	c.callbackURL = ""
	c.callbackState = ""
	c.challengeCreatedAt = time.Time{}
	c.pendingExtensionIDs = nil
}

func (c *signedSessionCoordinator) rememberChallenge(request *PendingAuthRequest) {
	if c.pendingExtensionIDs == nil {
		c.pendingExtensionIDs = make(map[string]struct{})
	}
	c.authURL = request.AuthURL
	c.callbackURL = request.CallbackURL
	c.callbackState = request.State
	c.challengeCreatedAt = request.CreatedAt
	c.pendingExtensionIDs[request.ExtensionID] = struct{}{}
}

func (c *signedSessionCoordinator) activeChallenge() bool {
	return strings.TrimSpace(c.authURL) != "" &&
		!c.challengeCreatedAt.IsZero() &&
		time.Since(c.challengeCreatedAt) < pendingAuthRequestTTL
}

type signedSessionRecord struct {
	InstallID     string `json:"install_id"`
	SessionID     string `json:"session_id,omitempty"`
	SessionSecret string `json:"session_secret,omitempty"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	Namespace     string `json:"namespace,omitempty"`
	BaseURL       string `json:"base_url,omitempty"`
	AppVersion    string `json:"app_version,omitempty"`
	Platform      string `json:"platform,omitempty"`
}

type signedSessionExchangeResponse struct {
	SessionID     string `json:"session_id,omitempty"`
	SessionSecret string `json:"session_secret,omitempty"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	ChallengeID   string `json:"challenge_id,omitempty"`
	ChallengeURL  string `json:"challenge_url,omitempty"`
	AuthURL       string `json:"auth_url,omitempty"`
}

type signedSessionErrorContract struct {
	Error             string `json:"error,omitempty"`
	Code              string `json:"code,omitempty"`
	Origin            string `json:"origin,omitempty"`
	Action            string `json:"action,omitempty"`
	Retryable         bool   `json:"retryable,omitempty"`
	RetryMode         string `json:"retry_mode,omitempty"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
}

func signedSessionGeneration(record *signedSessionRecord) string {
	if record == nil || record.SessionID == "" || record.SessionSecret == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(record.SessionID + "\n" + record.SessionSecret))
	return hex.EncodeToString(sum[:])
}

func (c *signedSessionCoordinator) blockGeneration(record *signedSessionRecord) {
	c.blockedGeneration = signedSessionGeneration(record)
}

func (c *signedSessionCoordinator) generationIsBlocked(record *signedSessionRecord) bool {
	generation := signedSessionGeneration(record)
	return generation != "" && generation == c.blockedGeneration
}

func (c *signedSessionCoordinator) clearBlockedGeneration() {
	c.blockedGeneration = ""
}

func signedSessionConfigWithDefaults(config *SignedSessionConfig) SignedSessionConfig {
	if config == nil {
		return SignedSessionConfig{}
	}
	resolved := *config
	if resolved.AppVersion == "" {
		resolved.AppVersion = "ext-1.0"
	}
	if resolved.Platform == "" {
		resolved.Platform = "extension"
	}
	if resolved.CallbackURL == "" {
		resolved.CallbackURL = "spotiflac://session-grant"
	}
	if resolved.SchemeLabel == "" {
		resolved.SchemeLabel = "SPOTIFLAC-HMAC-V1"
	}
	if resolved.HeaderPrefix == "" {
		resolved.HeaderPrefix = "X-Sig-"
	}
	if resolved.TimeWindowSeconds <= 0 {
		resolved.TimeWindowSeconds = 300
	}
	if resolved.Endpoints.Bootstrap == "" {
		resolved.Endpoints.Bootstrap = "/bootstrap"
	}
	if resolved.Endpoints.Challenge == "" {
		resolved.Endpoints.Challenge = "/challenge"
	}
	if resolved.Endpoints.Exchange == "" {
		resolved.Endpoints.Exchange = "/session/exchange"
	}
	return resolved
}

func (r *extensionRuntime) signedSessionFilePath(config SignedSessionConfig) (string, error) {
	namespace := sanitizeSignedSessionNamespace(config.Namespace)
	if namespace == "" {
		return "", fmt.Errorf("signed session namespace is empty")
	}
	baseDir := filepath.Dir(r.dataDir)
	if baseDir == "." || baseDir == "" {
		baseDir = r.dataDir
	}
	dir := filepath.Join(baseDir, "signed_sessions")
	scope := strings.Join([]string{
		namespace,
		strings.TrimSpace(strings.ToLower(config.BaseURL)),
		strings.TrimSpace(strings.ToLower(config.AppVersion)),
		strings.TrimSpace(strings.ToLower(config.Platform)),
	}, "\n")
	sum := sha256.Sum256([]byte(scope))
	return filepath.Join(dir, namespace+"-"+hex.EncodeToString(sum[:])[:16]+".json"), nil
}

func sanitizeSignedSessionNamespace(namespace string) string {
	namespace = strings.TrimSpace(strings.ToLower(namespace))
	var b strings.Builder
	for _, ch := range namespace {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.' {
			b.WriteRune(ch)
		}
	}
	return strings.Trim(b.String(), ".-_")
}

func (r *extensionRuntime) loadSignedSession(config SignedSessionConfig) (*signedSessionRecord, error) {
	path, err := r.signedSessionFilePath(config)
	if err != nil {
		return nil, err
	}
	record := &signedSessionRecord{}
	if data, readErr := os.ReadFile(path); readErr == nil {
		_ = json.Unmarshal(data, record)
	}
	changed := false
	if strings.TrimSpace(record.InstallID) == "" {
		record.InstallID = signedSessionHintFor(path)
		if record.InstallID == "" {
			record.InstallID = randomHex(16)
		}
		changed = true
	}
	if normalizeSignedSessionRecordScope(config, record) {
		changed = true
	}
	if changed {
		if err := r.saveSignedSession(config, record); err != nil {
			return nil, err
		}
	}
	return record, nil
}

func normalizeSignedSessionRecordScope(config SignedSessionConfig, record *signedSessionRecord) bool {
	namespace := sanitizeSignedSessionNamespace(config.Namespace)
	baseURL := strings.TrimSpace(config.BaseURL)
	appVersion := strings.TrimSpace(config.AppVersion)
	platform := strings.TrimSpace(config.Platform)
	if record.Namespace == namespace && record.BaseURL == baseURL && record.AppVersion == appVersion && record.Platform == platform {
		return false
	}
	blankScope := record.Namespace == "" && record.BaseURL == "" && record.AppVersion == "" && record.Platform == ""
	if !blankScope {
		record.SessionID = ""
		record.SessionSecret = ""
		record.ExpiresAt = ""
	}
	record.Namespace = namespace
	record.BaseURL = baseURL
	record.AppVersion = appVersion
	record.Platform = platform
	return true
}

func (r *extensionRuntime) saveSignedSession(config SignedSessionConfig, record *signedSessionRecord) error {
	path, err := r.signedSessionFilePath(config)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func randomHex(bytesLen int) string {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func parseSignedSessionTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func signedSessionRecordIsUsable(record *signedSessionRecord) bool {
	if record == nil || strings.TrimSpace(record.SessionID) == "" || strings.TrimSpace(record.SessionSecret) == "" {
		return false
	}
	if expiresAt, ok := parseSignedSessionTime(record.ExpiresAt); ok {
		return time.Now().Before(expiresAt)
	}
	return true
}

func sameSignedSession(a, b *signedSessionRecord) bool {
	return a != nil && b != nil && a.SessionID != "" && a.SessionID == b.SessionID && a.SessionSecret == b.SessionSecret
}

func parseSignedSessionErrorContract(body []byte) (signedSessionErrorContract, bool) {
	var contract signedSessionErrorContract
	if len(body) == 0 || json.Unmarshal(body, &contract) != nil {
		return signedSessionErrorContract{}, false
	}
	contract.Error = strings.TrimSpace(contract.Error)
	contract.Code = strings.ToUpper(strings.TrimSpace(contract.Code))
	contract.Origin = strings.ToLower(strings.TrimSpace(contract.Origin))
	contract.Action = strings.ToLower(strings.TrimSpace(contract.Action))
	contract.RetryMode = strings.ToLower(strings.TrimSpace(contract.RetryMode))
	if contract.RetryAfterSeconds < 0 {
		contract.RetryAfterSeconds = 0
	}
	return contract, contract.Code != "" || contract.Origin != "" || contract.Action != ""
}

func signedSessionGatewayAction(statusCode int, contract signedSessionErrorContract) string {
	if contract.Origin != "gateway" {
		return ""
	}
	switch {
	case statusCode == http.StatusUnauthorized && contract.Code == "SESSION_INVALID" && contract.Action == "bootstrap_session":
		return "bootstrap_session"
	case statusCode == http.StatusPreconditionRequired && contract.Code == "VERIFY_REQUIRED" && contract.Action == "verify":
		return "verify"
	default:
		return ""
	}
}

func signedSessionSameOperationRetry(statusCode int, contract signedSessionErrorContract) bool {
	return statusCode == http.StatusServiceUnavailable && contract.Origin == "provider" && contract.Code == "PROVIDER_UNAVAILABLE" && contract.Retryable && contract.RetryMode == "same_operation"
}

func signedSessionRequestAuthInvalid(statusCode int, contract signedSessionErrorContract) bool {
	return statusCode == http.StatusForbidden && contract.Origin == "gateway" && contract.Code == "REQUEST_AUTH_INVALID" && contract.Action == ""
}

func signedSessionProviderRetryDuration(resp *http.Response, contract signedSessionErrorContract) time.Duration {
	if retryAfter := getRetryAfterDuration(resp); retryAfter > 0 {
		return retryAfter
	}
	if contract.RetryAfterSeconds > 0 {
		maxSeconds := int(maxRetryAfterDelay / time.Second)
		return time.Duration(min(contract.RetryAfterSeconds, maxSeconds)) * time.Second
	}
	return signedSessionProviderRetryDelay
}

func (r *extensionRuntime) preflightSignedSession() (bool, error) {
	if r == nil || r.manifest == nil || r.manifest.SignedSession == nil {
		return false, nil
	}
	config := signedSessionConfigWithDefaults(r.manifest.SignedSession)
	if config.Namespace == "" || config.BaseURL == "" {
		return false, fmt.Errorf("signedSession is not configured")
	}
	coordinator, err := r.signedSessionCoordinator(config)
	if err != nil {
		return false, err
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	record, err := r.loadSignedSession(config)
	if err != nil {
		return false, err
	}
	if signedSessionRecordIsUsable(record) && !coordinator.generationIsBlocked(record) {
		return false, nil
	}
	authURL, err := r.startSignedSessionVerificationLocked(config, coordinator, "download-preflight")
	if err != nil {
		return false, err
	}
	if authURL != "" {
		return true, nil
	}
	record, err = r.loadSignedSession(config)
	if err != nil {
		return false, err
	}
	if signedSessionRecordIsUsable(record) {
		return false, nil
	}
	return false, fmt.Errorf("signed-session bootstrap did not return a session or verification challenge")
}

func (r *extensionRuntime) signedSessionStatus(call goja.FunctionCall) goja.Value {
	config := signedSessionConfigWithDefaults(r.manifest.SignedSession)
	if config.Namespace == "" || config.BaseURL == "" {
		return r.vm.ToValue(map[string]any{"authenticated": false, "error": "signedSession is not configured"})
	}
	coordinator, err := r.signedSessionCoordinator(config)
	if err != nil {
		return r.vm.ToValue(map[string]any{"authenticated": false, "error": err.Error()})
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	record, err := r.loadSignedSession(config)
	if err != nil {
		return r.vm.ToValue(map[string]any{"authenticated": false, "error": err.Error()})
	}
	blocked := coordinator.generationIsBlocked(record)
	return r.vm.ToValue(map[string]any{
		"authenticated":         signedSessionRecordIsUsable(record) && !blocked,
		"verification_required": blocked,
		"expires_at":            record.ExpiresAt,
		"install_id":            record.InstallID,
		"session_id":            record.SessionID,
		"app_version":           config.AppVersion,
		"platform":              config.Platform,
	})
}

func (r *extensionRuntime) signedSessionClear(call goja.FunctionCall) goja.Value {
	config := signedSessionConfigWithDefaults(r.manifest.SignedSession)
	coordinator, err := r.signedSessionCoordinator(config)
	if err != nil {
		return r.vm.ToValue(map[string]any{"success": false, "error": err.Error()})
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	record, err := r.loadSignedSession(config)
	if err != nil {
		return r.vm.ToValue(map[string]any{"success": false, "error": err.Error()})
	}
	record.SessionID = ""
	record.SessionSecret = ""
	record.ExpiresAt = ""
	if err := r.saveSignedSession(config, record); err != nil {
		return r.vm.ToValue(map[string]any{"success": false, "error": err.Error()})
	}
	coordinator.clearGeneration++
	coordinator.completedGrantHash = ""
	coordinator.clearBlockedGeneration()
	coordinator.clearChallenge()
	ClearPendingAuthRequest(r.extensionID)
	return r.vm.ToValue(map[string]any{"success": true})
}

func (r *extensionRuntime) signedSessionCompleteGrant(call goja.FunctionCall) goja.Value {
	grant := ""
	if len(call.Arguments) > 0 {
		grant = strings.TrimSpace(call.Arguments[0].String())
	}
	if grant != "" {
		setPendingSignedSessionGrant(r.extensionID, grant)
	}
	if grant == "" {
		pendingSignedSessionGrantsMu.Lock()
		grant = pendingSignedSessionGrants[r.extensionID]
		pendingSignedSessionGrantsMu.Unlock()
	}
	if grant == "" {
		return r.vm.ToValue(map[string]any{"success": false, "error": "no pending grant"})
	}
	ctx, cancel := r.signedSessionExchangeContext()
	defer cancel()
	if err := r.exchangeSignedSessionGrantContext(ctx, grant); err != nil {
		return r.vm.ToValue(map[string]any{"success": false, "error": err.Error()})
	}
	pendingSignedSessionGrantsMu.Lock()
	delete(pendingSignedSessionGrants, r.extensionID)
	pendingSignedSessionGrantsMu.Unlock()
	ClearPendingAuthRequest(r.extensionID)
	return r.vm.ToValue(map[string]any{"success": true})
}

func (r *extensionRuntime) exchangeSignedSessionGrant(grant string) error {
	ctx, cancel := r.signedSessionExchangeContext()
	defer cancel()
	return r.exchangeSignedSessionGrantContext(ctx, grant)
}

func (r *extensionRuntime) signedSessionExchangeContext() (context.Context, context.CancelFunc) {
	parent := context.Background()
	if r != nil {
		parent = r.activeOperationContext(parent)
	}
	return context.WithTimeout(parent, signedSessionExchangeTimeout)
}

func waitSignedSessionRetry(ctx context.Context, delay time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return signedSessionRetryWaitContext(ctx, delay)
}

func (r *extensionRuntime) exchangeSignedSessionGrantContext(ctx context.Context, grant string) error {
	if r == nil || r.manifest == nil || r.manifest.SignedSession == nil {
		return fmt.Errorf("signedSession is not configured")
	}
	if r.httpClient == nil {
		return fmt.Errorf("signed-session exchange HTTP client is unavailable")
	}
	config := signedSessionConfigWithDefaults(r.manifest.SignedSession)
	coordinator, err := r.signedSessionCoordinator(config)
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	coordinator.mu.Lock()
	clearGeneration := coordinator.clearGeneration
	coordinator.mu.Unlock()
	release, err := coordinator.beginExchange(ctx)
	if err != nil {
		return err
	}
	defer release()
	return r.exchangeSignedSessionGrantLocked(ctx, config, coordinator, clearGeneration, grant)
}

func (r *extensionRuntime) exchangeSignedSessionGrantLocked(ctx context.Context, config SignedSessionConfig, coordinator *signedSessionCoordinator, clearGeneration uint64, grant string) error {
	coordinator.mu.Lock()
	if coordinator.clearGeneration != clearGeneration {
		coordinator.mu.Unlock()
		return fmt.Errorf("signed-session exchange was superseded by session clear")
	}
	record, err := r.loadSignedSession(config)
	if err != nil {
		coordinator.mu.Unlock()
		return err
	}
	grantHashBytes := sha256.Sum256([]byte(grant))
	grantHash := hex.EncodeToString(grantHashBytes[:])
	if coordinator.completedGrantHash == grantHash && signedSessionRecordIsUsable(record) {
		coordinator.clearBlockedGeneration()
		coordinator.clearChallenge()
		coordinator.mu.Unlock()
		return nil
	}
	endpoint, err := signedSessionURL(config, config.Endpoints.Exchange)
	if err != nil {
		coordinator.mu.Unlock()
		return err
	}
	payload := map[string]any{"grant": grant, "install_id": record.InstallID, "app_version": config.AppVersion, "platform": config.Platform}
	body, _ := json.Marshal(payload)
	coordinator.mu.Unlock()

	var responseBody []byte
	for attempt := 1; attempt <= signedSessionExchangeMaxAttempts; attempt++ {
		req, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if requestErr != nil {
			return requestErr
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "SpotiFLAC-Mobile/"+config.AppVersion)
		resp, requestErr := r.httpClient.Do(req)
		if requestErr != nil {
			return requestErr
		}
		responseBody, requestErr = readSignedSessionBody(resp)
		resp.Body.Close()
		if requestErr != nil {
			return requestErr
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < signedSessionExchangeMaxAttempts {
			retryAfter := time.Duration(signedSessionRetryAfterSeconds(resp)) * time.Second
			if retryAfter <= 0 {
				retryAfter = time.Second
			}
			if retryAfter > signedSessionMaxRetryAfter {
				retryAfter = signedSessionMaxRetryAfter
			}
			if err := waitSignedSessionRetry(ctx, retryAfter); err != nil {
				return err
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("session exchange failed: HTTP %d", resp.StatusCode)
		}
		break
	}

	var exchanged signedSessionExchangeResponse
	if err := json.Unmarshal(responseBody, &exchanged); err != nil {
		return fmt.Errorf("invalid session exchange response: %w", err)
	}
	if exchanged.SessionID == "" || exchanged.SessionSecret == "" || exchanged.ExpiresAt == "" {
		return fmt.Errorf("session exchange response missing session fields")
	}

	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.clearGeneration != clearGeneration {
		return fmt.Errorf("signed-session exchange was superseded by session clear")
	}
	latest, err := r.loadSignedSession(config)
	if err != nil {
		return err
	}
	if coordinator.completedGrantHash == grantHash && signedSessionRecordIsUsable(latest) {
		coordinator.clearBlockedGeneration()
		coordinator.clearChallenge()
		return nil
	}
	if signedSessionRecordIsUsable(latest) && !sameSignedSession(latest, record) {
		coordinator.clearBlockedGeneration()
		coordinator.clearChallenge()
		return nil
	}
	latest.SessionID = exchanged.SessionID
	latest.SessionSecret = exchanged.SessionSecret
	latest.ExpiresAt = exchanged.ExpiresAt
	if err := r.saveSignedSession(config, latest); err != nil {
		return err
	}
	coordinator.completedGrantHash = grantHash
	coordinator.clearBlockedGeneration()
	coordinator.clearChallenge()
	return nil
}

func (r *extensionRuntime) signedSessionFetch(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 2 {
		return r.vm.ToValue(map[string]any{"ok": false, "error": "method and path are required"})
	}
	config := signedSessionConfigWithDefaults(r.manifest.SignedSession)
	if config.Namespace == "" || config.BaseURL == "" {
		return r.vm.ToValue(map[string]any{"ok": false, "error": "signedSession is not configured"})
	}
	method := strings.ToUpper(strings.TrimSpace(call.Arguments[0].String()))
	requestPath := call.Arguments[1].String()
	body := []byte{}
	if len(call.Arguments) > 2 && !goja.IsUndefined(call.Arguments[2]) && !goja.IsNull(call.Arguments[2]) {
		switch v := call.Arguments[2].Export().(type) {
		case string:
			body = []byte(v)
		case map[string]any, []any:
			encoded, err := json.Marshal(v)
			if err != nil {
				return r.vm.ToValue(map[string]any{"ok": false, "error": err.Error()})
			}
			body = encoded
		default:
			body = []byte(call.Arguments[2].String())
		}
	}
	extraHeaders := signedSessionHeaders(call.Argument(3))

	coordinator, err := r.signedSessionCoordinator(config)
	if err != nil {
		return r.vm.ToValue(map[string]any{"ok": false, "error": err.Error()})
	}
	coordinator.mu.Lock()
	record, err := r.ensureSignedSession(config)
	if err != nil || coordinator.generationIsBlocked(record) {
		authURL, verificationErr := r.startSignedSessionVerificationLocked(config, coordinator, "signed-fetch")
		coordinator.mu.Unlock()
		if authURL != "" {
			return r.signedSessionVerificationRequiredValue(authURL)
		}
		if verificationErr != nil {
			return r.vm.ToValue(map[string]any{"ok": false, "error": verificationErr.Error()})
		}
		return r.vm.ToValue(map[string]any{"ok": false, "error": "signed session is not authenticated"})
	}
	coordinator.mu.Unlock()

	if signedSessionRefreshDue(config, record) {
		if _, refreshErr := r.refreshSignedSessionCoalesced(config, coordinator); refreshErr != nil {
			LogWarn("SignedSession", "Session refresh failed for extension %s: %v", r.extensionID, refreshErr)
		}
		coordinator.mu.Lock()
		latest, loadErr := r.loadSignedSession(config)
		coordinator.mu.Unlock()
		if loadErr != nil {
			return r.vm.ToValue(map[string]any{"ok": false, "error": loadErr.Error()})
		}
		if signedSessionRecordIsUsable(latest) {
			record = latest
		}
	}

	sessionRetries := 0
	providerRetries := 0
	requestAuthRetryUsed := false
	providerRetryCtx := r.activeOperationContext(context.Background())
	for {
		resp, respBody, respHeaders, requestErr := r.doSignedSessionRequest(config, record, method, requestPath, body, extraHeaders)
		if requestErr != nil {
			return r.vm.ToValue(map[string]any{"ok": false, "error": requestErr.Error()})
		}
		contract, _ := parseSignedSessionErrorContract(respBody)

		if signedSessionSameOperationRetry(resp.StatusCode, contract) {
			if providerRetries >= signedSessionMaxProviderRetries {
				return r.signedSessionResponseValue(resp, respBody, respHeaders)
			}
			providerRetries++
			delay := signedSessionProviderRetryDuration(resp, contract)
			if waitErr := signedSessionProviderWait(providerRetryCtx, delay); waitErr != nil {
				return r.vm.ToValue(map[string]any{"ok": false, "error": waitErr.Error()})
			}
			continue
		}

		if signedSessionRequestAuthInvalid(resp.StatusCode, contract) {
			coordinator.mu.Lock()
			latest, loadErr := r.loadSignedSession(config)
			coordinator.mu.Unlock()
			if loadErr != nil {
				return r.vm.ToValue(map[string]any{"ok": false, "error": loadErr.Error()})
			}
			if !requestAuthRetryUsed && signedSessionRecordIsUsable(latest) && !sameSignedSession(latest, record) {
				requestAuthRetryUsed = true
				record = latest
				continue
			}
			return r.signedSessionResponseValue(resp, respBody, respHeaders)
		}

		gatewayAction := signedSessionGatewayAction(resp.StatusCode, contract)
		if gatewayAction == "" {
			return r.signedSessionResponseValue(resp, respBody, respHeaders)
		}

		coordinator.mu.Lock()
		latest, loadErr := r.loadSignedSession(config)
		if loadErr != nil {
			coordinator.mu.Unlock()
			return r.vm.ToValue(map[string]any{"ok": false, "error": loadErr.Error()})
		}
		if signedSessionRecordIsUsable(latest) && !sameSignedSession(latest, record) && sessionRetries < signedSessionMaxSessionRetries {
			sessionRetries++
			record = latest
			coordinator.mu.Unlock()
			continue
		}
		if gatewayAction == "bootstrap_session" && sameSignedSession(latest, record) {
			coordinator.clearBlockedGeneration()
			latest.SessionID = ""
			latest.SessionSecret = ""
			latest.ExpiresAt = ""
			if saveErr := r.saveSignedSession(config, latest); saveErr != nil {
				coordinator.mu.Unlock()
				return r.vm.ToValue(map[string]any{"ok": false, "error": saveErr.Error()})
			}
		} else if gatewayAction == "verify" && sameSignedSession(latest, record) {
			coordinator.blockGeneration(record)
		}
		authURL, verificationErr := r.startSignedSessionVerificationLocked(config, coordinator, "signed-fetch-"+gatewayAction)
		if authURL != "" {
			coordinator.mu.Unlock()
			return r.signedSessionVerificationRequiredValue(authURL)
		}
		if verificationErr != nil {
			coordinator.mu.Unlock()
			return r.vm.ToValue(map[string]any{"ok": false, "error": verificationErr.Error()})
		}
		bootstrapped, loadErr := r.loadSignedSession(config)
		coordinator.mu.Unlock()
		if loadErr != nil {
			return r.vm.ToValue(map[string]any{"ok": false, "error": loadErr.Error()})
		}
		if signedSessionRecordIsUsable(bootstrapped) && !sameSignedSession(bootstrapped, record) && sessionRetries < signedSessionMaxSessionRetries {
			sessionRetries++
			record = bootstrapped
			continue
		}
		return r.signedSessionResponseValue(resp, respBody, respHeaders)
	}
}

func (r *extensionRuntime) signedSessionResponseValue(resp *http.Response, respBody []byte, respHeaders map[string]any) goja.Value {
	contract, hasContract := parseSignedSessionErrorContract(respBody)
	retryAfterSeconds := signedSessionRetryAfterSeconds(resp)
	if retryAfterSeconds <= 0 && contract.RetryAfterSeconds > 0 {
		retryAfterSeconds = contract.RetryAfterSeconds
	}
	result := map[string]any{
		"statusCode":        resp.StatusCode,
		"status":            resp.StatusCode,
		"ok":                resp.StatusCode >= 200 && resp.StatusCode < 300,
		"url":               resp.Request.URL.String(),
		"body":              string(respBody),
		"headers":           respHeaders,
		"retryAfterSeconds": retryAfterSeconds,
	}
	if hasContract {
		result["error"] = contract.Error
		result["code"] = contract.Code
		result["origin"] = contract.Origin
		result["action"] = contract.Action
		result["retryable"] = contract.Retryable
		result["retryMode"] = contract.RetryMode
	}
	return r.vm.ToValue(result)
}

func (r *extensionRuntime) signedSessionVerificationRequiredValue(authURL string) goja.Value {
	r.noteVerificationRequired(authURL)
	return r.vm.ToValue(map[string]any{
		"ok":                false,
		"needsVerification": true,
		"error":             "VERIFY_REQUIRED",
		"open_auth_url":     authURL,
		"auth_url":          authURL,
	})
}

func (r *extensionRuntime) ensureSignedSession(config SignedSessionConfig) (*signedSessionRecord, error) {
	record, err := r.loadSignedSession(config)
	if err != nil {
		return nil, err
	}
	if record.SessionID == "" || record.SessionSecret == "" {
		return nil, fmt.Errorf("signed session is not authenticated")
	}
	if expiresAt, ok := parseSignedSessionTime(record.ExpiresAt); ok && time.Now().After(expiresAt) {
		record.SessionID = ""
		record.SessionSecret = ""
		record.ExpiresAt = ""
		_ = r.saveSignedSession(config, record)
		return nil, fmt.Errorf("signed session expired")
	}
	return record, nil
}

func signedSessionRefreshDue(config SignedSessionConfig, record *signedSessionRecord) bool {
	if config.Endpoints.Refresh == "" || !signedSessionRecordIsUsable(record) {
		return false
	}
	expiresAt, ok := parseSignedSessionTime(record.ExpiresAt)
	return ok && time.Now().Before(expiresAt) && time.Until(expiresAt) <= signedSessionRefreshSkew
}

func (r *extensionRuntime) fetchSignedSessionRefresh(config SignedSessionConfig, record *signedSessionRecord) (signedSessionExchangeResponse, error) {
	var refreshed signedSessionExchangeResponse
	body, _ := json.Marshal(map[string]string{"install_id": record.InstallID})
	resp, respBody, _, err := r.doSignedSessionRequest(config, record, http.MethodPost, config.Endpoints.Refresh, body, nil)
	if err != nil {
		return refreshed, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return refreshed, fmt.Errorf("session refresh failed: HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(respBody, &refreshed); err != nil {
		return refreshed, err
	}
	return refreshed, nil
}

func applySignedSessionRefresh(record *signedSessionRecord, refreshed signedSessionExchangeResponse) bool {
	changed := false
	if refreshed.SessionID != "" && refreshed.SessionID != record.SessionID {
		record.SessionID = refreshed.SessionID
		changed = true
	}
	if refreshed.SessionSecret != "" && refreshed.SessionSecret != record.SessionSecret {
		record.SessionSecret = refreshed.SessionSecret
		changed = true
	}
	if refreshed.ExpiresAt != "" && refreshed.ExpiresAt != record.ExpiresAt {
		record.ExpiresAt = refreshed.ExpiresAt
		changed = true
	}
	return changed
}

func (r *extensionRuntime) refreshSignedSessionCoalesced(config SignedSessionConfig, coordinator *signedSessionCoordinator) (*signedSessionRecord, error) {
	ctx := r.activeOperationContext(context.Background())
	for {
		coordinator.mu.Lock()
		latest, err := r.loadSignedSession(config)
		if err != nil {
			coordinator.mu.Unlock()
			return nil, err
		}
		if !signedSessionRefreshDue(config, latest) {
			coordinator.mu.Unlock()
			return latest, nil
		}
		if coordinator.refreshInFlight {
			done := coordinator.refreshDone
			coordinator.mu.Unlock()
			select {
			case <-done:
				coordinator.mu.Lock()
				sharedErr := coordinator.refreshErr
				coordinator.mu.Unlock()
				if sharedErr != nil {
					return nil, sharedErr
				}
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		coordinator.refreshInFlight = true
		coordinator.refreshDone = make(chan struct{})
		coordinator.refreshErr = nil
		done := coordinator.refreshDone
		clearGeneration := coordinator.clearGeneration
		refreshGeneration := *latest
		coordinator.mu.Unlock()

		refreshed, refreshErr := r.fetchSignedSessionRefresh(config, &refreshGeneration)

		coordinator.mu.Lock()
		var current *signedSessionRecord
		finalErr := refreshErr
		if finalErr == nil && coordinator.clearGeneration != clearGeneration {
			finalErr = fmt.Errorf("signed-session refresh was superseded by session clear")
		}
		if finalErr == nil {
			current, finalErr = r.loadSignedSession(config)
		}
		if finalErr == nil && sameSignedSession(current, &refreshGeneration) && applySignedSessionRefresh(current, refreshed) {
			finalErr = r.saveSignedSession(config, current)
		}
		if coordinator.refreshInFlight && coordinator.refreshDone == done {
			coordinator.refreshInFlight = false
			coordinator.refreshErr = finalErr
			close(done)
		}
		coordinator.mu.Unlock()
		return current, finalErr
	}
}

func (r *extensionRuntime) refreshSignedSession(config SignedSessionConfig, record *signedSessionRecord) error {
	refreshed, err := r.fetchSignedSessionRefresh(config, record)
	if err != nil {
		return err
	}
	if applySignedSessionRefresh(record, refreshed) {
		return r.saveSignedSession(config, record)
	}
	return nil
}

func (r *extensionRuntime) startSignedSessionVerificationLocked(config SignedSessionConfig, coordinator *signedSessionCoordinator, reason string) (string, error) {
	if coordinator.activeChallenge() {
		if err := registerPendingAuthRequest(&PendingAuthRequest{
			ExtensionID: r.extensionID,
			AuthURL:     coordinator.authURL,
			CallbackURL: coordinator.callbackURL,
			State:       coordinator.callbackState,
			CreatedAt:   coordinator.challengeCreatedAt,
		}); err != nil {
			return "", err
		}
		coordinator.pendingExtensionIDs[r.extensionID] = struct{}{}
		return coordinator.authURL, nil
	}
	if coordinator.authURL != "" {
		coordinator.clearChallenge()
	}
	if pending := GetPendingAuthRequest(r.extensionID); pending != nil {
		if time.Since(pending.CreatedAt) < pendingAuthRequestTTL && strings.TrimSpace(pending.AuthURL) != "" {
			coordinator.rememberChallenge(pending)
			return pending.AuthURL, nil
		}
		ClearPendingAuthRequest(r.extensionID)
	}

	record, err := r.loadSignedSession(config)
	if err != nil {
		return "", err
	}
	if signedSessionRecordIsUsable(record) && !coordinator.generationIsBlocked(record) {
		return "", nil
	}
	if coordinator.bootstrapInFlight {
		done := coordinator.bootstrapDone
		ctx := r.activeOperationContext(context.Background())
		coordinator.mu.Unlock()
		select {
		case <-done:
			coordinator.mu.Lock()
			if coordinator.bootstrapErr != nil {
				return "", coordinator.bootstrapErr
			}
			return r.startSignedSessionVerificationLocked(config, coordinator, reason)
		case <-ctx.Done():
			coordinator.mu.Lock()
			return "", ctx.Err()
		}
	}

	coordinator.bootstrapInFlight = true
	coordinator.bootstrapDone = make(chan struct{})
	coordinator.bootstrapErr = nil
	bootstrapDone := coordinator.bootstrapDone
	clearGeneration := coordinator.clearGeneration
	ctx := r.activeOperationContext(context.Background())
	coordinator.mu.Unlock()
	bootstrap, bootstrapErr := r.performSignedSessionBootstrap(ctx, config, record, reason)
	coordinator.mu.Lock()
	finalErr := bootstrapErr
	authURL := ""
	if finalErr == nil && coordinator.clearGeneration != clearGeneration {
		finalErr = fmt.Errorf("signed-session bootstrap was superseded by session clear")
	}
	var latest *signedSessionRecord
	if finalErr == nil {
		latest, finalErr = r.loadSignedSession(config)
	}
	if finalErr == nil && signedSessionRecordIsUsable(latest) && !sameSignedSession(latest, record) && !coordinator.generationIsBlocked(latest) {
		coordinator.clearChallenge()
	} else if finalErr == nil && bootstrap.SessionID != "" {
		latest.SessionID = bootstrap.SessionID
		latest.SessionSecret = bootstrap.SessionSecret
		latest.ExpiresAt = bootstrap.ExpiresAt
		if saveErr := r.saveSignedSession(config, latest); saveErr != nil {
			finalErr = saveErr
		} else {
			coordinator.clearBlockedGeneration()
			coordinator.clearChallenge()
		}
	} else if finalErr == nil {
		request := &PendingAuthRequest{
			ExtensionID: r.extensionID,
			AuthURL:     bootstrap.AuthURL,
			CallbackURL: bootstrap.CallbackURL,
			State:       bootstrap.CallbackState,
			CreatedAt:   time.Now(),
		}
		if registerErr := registerPendingAuthRequest(request); registerErr != nil {
			finalErr = registerErr
		} else {
			coordinator.rememberChallenge(request)
			authURL = bootstrap.AuthURL
		}
	}
	if coordinator.bootstrapInFlight && coordinator.bootstrapDone == bootstrapDone {
		coordinator.bootstrapInFlight = false
		coordinator.bootstrapErr = finalErr
		close(bootstrapDone)
	}
	return authURL, finalErr
}

type signedSessionBootstrapResult struct {
	SessionID     string
	SessionSecret string
	ExpiresAt     string
	AuthURL       string
	CallbackURL   string
	CallbackState string
}

func (r *extensionRuntime) performSignedSessionBootstrap(ctx context.Context, config SignedSessionConfig, record *signedSessionRecord, reason string) (signedSessionBootstrapResult, error) {
	var result signedSessionBootstrapResult
	bootstrapURL, err := signedSessionURL(config, config.Endpoints.Bootstrap)
	if err != nil {
		return result, err
	}
	parsed, err := url.Parse(bootstrapURL)
	if err != nil {
		return result, err
	}
	query := parsed.Query()
	query.Set("app_version", config.AppVersion)
	query.Set("install_id", record.InstallID)
	parsed.RawQuery = query.Encode()
	if r.httpClient == nil {
		return result, fmt.Errorf("signed-session bootstrap HTTP client is unavailable")
	}

	var resp *http.Response
	for attempt := 0; attempt < 2; attempt++ {
		req, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if requestErr != nil {
			return result, requestErr
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "SpotiFLAC-Mobile/"+config.AppVersion)
		resp, err = r.httpClient.Do(req)
		if err == nil {
			break
		}
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
			resp = nil
		}
		if attempt == 0 {
			r.httpClient.CloseIdleConnections()
		}
	}
	if err != nil {
		LogWarn("SignedSession", "Bootstrap failed for extension %s (%s): %v", r.extensionID, reason, err)
		return result, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return result, fmt.Errorf("signed-session bootstrap returned HTTP %d", resp.StatusCode)
	}
	body, err := readSignedSessionBody(resp)
	if err != nil {
		return result, err
	}
	var boot signedSessionExchangeResponse
	if err := json.Unmarshal(body, &boot); err != nil {
		return result, err
	}
	if boot.SessionID != "" && boot.SessionSecret != "" && boot.ExpiresAt != "" {
		result.SessionID = boot.SessionID
		result.SessionSecret = boot.SessionSecret
		result.ExpiresAt = boot.ExpiresAt
		return result, nil
	}
	authURL := boot.AuthURL
	if authURL == "" {
		authURL = boot.ChallengeURL
	}
	callbackState, err := newExtensionCallbackState()
	if err != nil {
		return result, err
	}
	if parsedAuthURL, parseErr := url.Parse(authURL); parseErr == nil {
		if serverState := strings.TrimSpace(parsedAuthURL.Query().Get("state")); serverState != "" {
			callbackState = serverState
		} else if authURL != "" {
			authURL, err = signedSessionSetState(authURL, callbackState)
			if err != nil {
				return result, err
			}
		}
	}
	callbackURL, err := signedSessionSetState(config.CallbackURL, callbackState)
	if err != nil {
		return result, err
	}
	if authURL == "" && boot.ChallengeID != "" {
		authURL = r.buildSignedSessionChallengeURL(config, boot.ChallengeID, callbackState)
	}
	if authURL == "" {
		return result, fmt.Errorf("signed-session bootstrap did not return a session or verification challenge")
	}
	result.AuthURL = authURL
	result.CallbackURL = callbackURL
	result.CallbackState = callbackState
	return result, nil
}

func (r *extensionRuntime) buildSignedSessionChallengeURL(config SignedSessionConfig, challengeID, callbackState string) string {
	challengeURL, err := signedSessionURL(config, config.Endpoints.Challenge)
	if err != nil {
		return ""
	}
	parsed, err := url.Parse(challengeURL)
	if err != nil {
		return ""
	}
	callback, err := url.Parse(config.CallbackURL)
	if err != nil {
		return ""
	}
	q := callback.Query()
	q.Set("cb_version", "v2grant")
	q.Set("state", callbackState)
	callback.RawQuery = q.Encode()
	query := parsed.Query()
	query.Set("id", challengeID)
	query.Set("cb", callback.String())
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func signedSessionSetState(rawURL, state string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	q := parsed.Query()
	q.Set("state", state)
	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}

func signedSessionURL(config SignedSessionConfig, endpoint string) (string, error) {
	base, err := url.Parse(strings.TrimRight(config.BaseURL, "/") + "/")
	if err != nil || base.Scheme != "https" || base.Host == "" {
		return "", fmt.Errorf("invalid signed session baseUrl")
	}
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", fmt.Errorf("signed session endpoint is empty")
	}
	if strings.HasPrefix(endpoint, "https://") {
		return endpoint, nil
	}
	ref, _ := url.Parse(strings.TrimLeft(endpoint, "/"))
	return base.ResolveReference(ref).String(), nil
}

func (r *extensionRuntime) doSignedSessionRequest(config SignedSessionConfig, record *signedSessionRecord, method, requestPath string, body []byte, extraHeaders map[string]string) (*http.Response, []byte, map[string]any, error) {
	fullURL, err := signedSessionURL(config, requestPath)
	if err != nil {
		return nil, nil, nil, err
	}
	parsed, err := url.Parse(fullURL)
	if err != nil {
		return nil, nil, nil, err
	}
	ts := signedSessionRequestNow().UTC().Format("2006-01-02T15:04:05.000Z")
	nonce := randomHex(12)
	bodyHashBytes := sha256.Sum256(body)
	bodyHash := hex.EncodeToString(bodyHashBytes[:])
	parsedTs, _ := time.Parse("2006-01-02T15:04:05.000Z", ts)
	window := parsedTs.Unix() / int64(config.TimeWindowSeconds)
	rollingInput := fmt.Sprintf("%d:%s", window, record.SessionID)
	rk := base64.RawURLEncoding.EncodeToString(hmacSHA256Bytes([]byte(record.SessionSecret), []byte(rollingInput)))
	signingInput := strings.Join([]string{
		config.SchemeLabel,
		method,
		parsed.EscapedPath(),
		"",
		bodyHash,
		ts,
		nonce,
		record.SessionID,
		config.AppVersion,
		config.Platform,
	}, "\n")
	sig := base64.RawURLEncoding.EncodeToString(hmacSHA256Bytes([]byte(rk), []byte(signingInput)))

	req, err := http.NewRequest(method, fullURL, bytes.NewReader(body))
	if err != nil {
		return nil, nil, nil, err
	}
	req = r.bindDownloadCancelContext(req)
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "SpotiFLAC-Mobile/"+config.AppVersion)
	prefix := config.HeaderPrefix
	req.Header.Set(prefix+"Session", record.SessionID)
	req.Header.Set(prefix+"Timestamp", ts)
	req.Header.Set(prefix+"Nonce", nonce)
	req.Header.Set(prefix+"Body-SHA256", bodyHash)
	req.Header.Set(prefix+"Signature", sig)
	req.Header.Set(prefix+"App-Version", config.AppVersion)
	req.Header.Set(prefix+"Platform", config.Platform)
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, nil, nil, err
	}
	defer resp.Body.Close()
	respBody, err := readSignedSessionBody(resp)
	if err != nil {
		return nil, nil, nil, err
	}
	headers := make(map[string]any)
	for k, v := range resp.Header {
		if len(v) == 1 {
			headers[k] = v[0]
		} else {
			headers[k] = v
		}
	}
	return resp, respBody, headers, nil
}

func readSignedSessionBody(resp *http.Response) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("response body is unavailable")
	}
	reader := io.LimitReader(resp.Body, signedSessionResponseBodyLimit+1)
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > signedSessionResponseBodyLimit {
		return nil, fmt.Errorf("signed-session response exceeds size limit")
	}
	return body, nil
}

func signedSessionRetryAfterSeconds(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	value := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0
		}
		return seconds
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		seconds := int(time.Until(retryAt).Seconds())
		if seconds < 0 {
			return 0
		}
		return seconds
	}
	return 0
}

func signedSessionHeaders(value goja.Value) map[string]string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil
	}
	exported, ok := value.Export().(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]string, len(exported))
	for key, raw := range exported {
		if key == "" || raw == nil {
			continue
		}
		result[key] = fmt.Sprint(raw)
	}
	return result
}

func hmacSHA256Bytes(key, message []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(message)
	return mac.Sum(nil)
}

func setPendingSignedSessionGrant(extensionID, grant string) {
	extensionID = strings.TrimSpace(extensionID)
	grant = strings.TrimSpace(grant)
	if extensionID == "" || grant == "" {
		return
	}
	pendingSignedSessionGrantsMu.Lock()
	pendingSignedSessionGrants[extensionID] = grant
	pendingSignedSessionGrantsMu.Unlock()
}

var _ = errors.Is
