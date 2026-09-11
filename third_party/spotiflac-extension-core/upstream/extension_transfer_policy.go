package gobackend

import (
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	defaultTransferMaxAttempts       = 3
	defaultTransferInitialRetryDelay = 500 * time.Millisecond
	defaultTransferMaxRetryDelay     = 8 * time.Second
	defaultParallelSegments          = 3
	maxParallelSegments              = 8
	maxExtensionDownloadConcurrency  = 3
)

// DownloadTransferPolicy is the generic manifest contract used by every
// extension-backed transfer. It deliberately describes transport behavior,
// never a provider name, so new extensions can opt into the same reliability
// and concurrency features without changes in the app.
//
// Manifests declare it under capabilities.downloadTransfer:
//
//	{
//	  "maxAttempts": 4,
//	  "initialRetryDelayMs": 500,
//	  "maxRetryDelayMs": 8000,
//	  "resumePolicy": "validated",
//	  "persistentCheckpoint": true,
//	  "refreshStreamOnStatus": [401, 403],
//	  "maxParallelSegments": 4,
//	  "maxConcurrentDownloads": 2
//	}
type DownloadTransferPolicy struct {
	MaxAttempts            int
	InitialRetryDelay      time.Duration
	MaxRetryDelay          time.Duration
	ResumePolicy           string
	PersistentCheckpoint   bool
	RefreshStreamOnStatus  map[int]bool
	MaxParallelSegments    int
	MaxConcurrentDownloads int
}

func defaultDownloadTransferPolicy() DownloadTransferPolicy {
	return DownloadTransferPolicy{
		MaxAttempts:            defaultTransferMaxAttempts,
		InitialRetryDelay:      defaultTransferInitialRetryDelay,
		MaxRetryDelay:          defaultTransferMaxRetryDelay,
		ResumePolicy:           "none",
		PersistentCheckpoint:   false,
		RefreshStreamOnStatus:  map[int]bool{httpStatusUnauthorized: true, httpStatusForbidden: true},
		MaxParallelSegments:    defaultParallelSegments,
		MaxConcurrentDownloads: maxExtensionDownloadConcurrency,
	}
}

const (
	httpStatusUnauthorized = 401
	httpStatusForbidden    = 403
)

func capabilityObject(capabilities map[string]any, key string) map[string]any {
	if capabilities == nil {
		return nil
	}
	value, ok := capabilities[key]
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case map[string]any:
		return typed
	default:
		return nil
	}
}

func capabilityInt(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float32:
		return int(math.Round(float64(typed)))
	case float64:
		return int(math.Round(typed))
	default:
		return fallback
	}
}

func clampInt(value, minimum, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func parseRefreshStatuses(value any, fallback map[int]bool) map[int]bool {
	values, ok := value.([]any)
	if !ok {
		return fallback
	}
	parsed := make(map[int]bool)
	for _, raw := range values {
		status := capabilityInt(raw, 0)
		if status >= 400 && status <= 599 {
			parsed[status] = true
		}
	}
	if len(parsed) == 0 {
		return fallback
	}
	return parsed
}

func (m *ExtensionManifest) DownloadTransferPolicy() DownloadTransferPolicy {
	policy := defaultDownloadTransferPolicy()
	if m == nil {
		return policy
	}
	config := capabilityObject(m.Capabilities, "downloadTransfer")
	if config == nil {
		return policy
	}

	policy.MaxAttempts = clampInt(
		capabilityInt(config["maxAttempts"], policy.MaxAttempts),
		1,
		8,
	)
	initialDelayMs := clampInt(
		capabilityInt(config["initialRetryDelayMs"], int(policy.InitialRetryDelay/time.Millisecond)),
		100,
		30_000,
	)
	maxDelayMs := clampInt(
		capabilityInt(config["maxRetryDelayMs"], int(policy.MaxRetryDelay/time.Millisecond)),
		initialDelayMs,
		120_000,
	)
	policy.InitialRetryDelay = time.Duration(initialDelayMs) * time.Millisecond
	policy.MaxRetryDelay = time.Duration(maxDelayMs) * time.Millisecond

	if value, ok := config["resumePolicy"].(string); ok {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "validated", "none":
			policy.ResumePolicy = strings.ToLower(strings.TrimSpace(value))
		}
	}
	if value, ok := config["persistentCheckpoint"].(bool); ok {
		policy.PersistentCheckpoint = value && policy.ResumePolicy == "validated"
	}
	policy.RefreshStreamOnStatus = parseRefreshStatuses(
		config["refreshStreamOnStatus"],
		policy.RefreshStreamOnStatus,
	)
	policy.MaxParallelSegments = clampInt(
		capabilityInt(config["maxParallelSegments"], policy.MaxParallelSegments),
		1,
		maxParallelSegments,
	)
	policy.MaxConcurrentDownloads = clampInt(
		capabilityInt(config["maxConcurrentDownloads"], policy.MaxConcurrentDownloads),
		1,
		maxExtensionDownloadConcurrency,
	)
	return policy
}

func validateDownloadTransferCapability(capabilities map[string]any) error {
	if capabilities == nil {
		return nil
	}
	_, exists := capabilities["downloadTransfer"]
	if !exists {
		return nil
	}
	config := capabilityObject(capabilities, "downloadTransfer")
	if config == nil {
		return fmt.Errorf("must be an object")
	}
	if rawResume, ok := config["resumePolicy"]; ok {
		resume, ok := rawResume.(string)
		if !ok || (resume != "none" && resume != "validated") {
			return fmt.Errorf("resumePolicy must be 'none' or 'validated'")
		}
	}
	if rawCheckpoint, ok := config["persistentCheckpoint"]; ok {
		if _, ok := rawCheckpoint.(bool); !ok {
			return fmt.Errorf("persistentCheckpoint must be a boolean")
		}
	}
	for _, key := range []string{
		"maxAttempts",
		"initialRetryDelayMs",
		"maxRetryDelayMs",
		"maxParallelSegments",
		"maxConcurrentDownloads",
	} {
		if rawValue, ok := config[key]; ok && capabilityInt(rawValue, -1) < 0 {
			return fmt.Errorf("%s must be a non-negative number", key)
		}
	}
	return nil
}
