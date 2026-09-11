package gobackend

import (
	"strings"
	"sync"
)

var providerPriority []string
var providerPriorityMu sync.RWMutex

var extensionFallbackProviderIDs []string
var extensionFallbackProviderIDsMu sync.RWMutex

var metadataProviderPriority []string
var metadataProviderPriorityMu sync.RWMutex

func sanitizeProviderIDs(providerIDs []string) []string {
	sanitized := make([]string, 0, len(providerIDs))
	seen := map[string]struct{}{}
	for _, providerID := range providerIDs {
		providerID = strings.TrimSpace(providerID)
		if providerID == "" {
			continue
		}
		key := strings.ToLower(providerID)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		sanitized = append(sanitized, providerID)
	}
	return sanitized
}

func SetProviderPriority(providerIDs []string) {
	providerPriorityMu.Lock()
	defer providerPriorityMu.Unlock()
	providerPriority = sanitizeProviderIDs(providerIDs)
	GoLog("[Extension] Download provider priority set: %v\n", providerPriority)
}

func GetProviderPriority() []string {
	providerPriorityMu.RLock()
	defer providerPriorityMu.RUnlock()
	result := make([]string, len(providerPriority))
	copy(result, providerPriority)
	return result
}

func SetExtensionFallbackProviderIDs(providerIDs []string) {
	extensionFallbackProviderIDsMu.Lock()
	defer extensionFallbackProviderIDsMu.Unlock()

	if providerIDs == nil {
		extensionFallbackProviderIDs = nil
		GoLog("[Extension] Extension fallback providers reset to default (all enabled download extensions)\n")
		return
	}

	extensionFallbackProviderIDs = sanitizeProviderIDs(providerIDs)
	GoLog("[Extension] Extension fallback providers set: %v\n", extensionFallbackProviderIDs)
}

func GetExtensionFallbackProviderIDs() []string {
	extensionFallbackProviderIDsMu.RLock()
	defer extensionFallbackProviderIDsMu.RUnlock()
	if extensionFallbackProviderIDs == nil {
		return nil
	}
	result := make([]string, len(extensionFallbackProviderIDs))
	copy(result, extensionFallbackProviderIDs)
	return result
}

func isExtensionFallbackAllowed(providerID string) bool {
	allowed := GetExtensionFallbackProviderIDs()
	if allowed == nil {
		return true
	}
	for _, allowedProviderID := range allowed {
		if strings.EqualFold(strings.TrimSpace(allowedProviderID), strings.TrimSpace(providerID)) {
			return true
		}
	}
	return false
}

func SetMetadataProviderPriority(providerIDs []string) {
	metadataProviderPriorityMu.Lock()
	defer metadataProviderPriorityMu.Unlock()
	metadataProviderPriority = sanitizeProviderIDs(providerIDs)
	GoLog("[Extension] Metadata provider priority set: %v\n", metadataProviderPriority)
}

func GetMetadataProviderPriority() []string {
	metadataProviderPriorityMu.RLock()
	defer metadataProviderPriorityMu.RUnlock()
	result := make([]string, len(metadataProviderPriority))
	copy(result, metadataProviderPriority)
	return result
}
