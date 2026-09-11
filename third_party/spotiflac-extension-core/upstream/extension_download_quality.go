package gobackend

import (
	"fmt"
	"strings"
)

// Quality IDs belong to their declaring provider. Keep the requested audio
// kind when translating an ID instead of treating the first option as best.
func extensionQualityKind(option QualityOption, manifest *ExtensionManifest) string {
	if kind := strings.ToLower(strings.TrimSpace(option.Kind)); kind != "" {
		switch kind {
		case "lossless", "lossy", "spatial":
			return kind
		}
	}
	// Compatibility for installed packages that predate the kind declaration.
	// Use only the ID and label: descriptions may mention fallback formats.
	token := strings.ToLower(strings.TrimSpace(option.ID))
	label := strings.ToLower(option.Label)
	text := token + " " + label
	if strings.Contains(text, "atmos") || strings.Contains(text, "dolby") ||
		strings.Contains(text, "surround") || token == "ac4" || token == "ac-4" ||
		token == "eac3" || token == "e-ac-3" || token == "ec-3" {
		return "spatial"
	}
	if strings.Contains(text, "lossless") || strings.Contains(text, "flac") ||
		strings.Contains(text, "alac") || strings.Contains(text, "24-bit") ||
		strings.Contains(text, "16-bit") || token == "hi_res" {
		return "lossless"
	}
	if token == "high" || token == "low" || strings.Contains(text, "mp3") ||
		strings.Contains(text, "aac") || strings.Contains(text, "opus") ||
		strings.Contains(text, "vorbis") {
		return "lossy"
	}
	if token == "best" || token == "default" || token == "" {
		if manifest != nil {
			switch strings.ToLower(strings.TrimSpace(fmt.Sprint(manifest.Capabilities["downloadFallbackTier"]))) {
			case "hi_res", "lossless":
				return "lossless"
			case "low_res":
				return "lossy"
			}
		}
	}
	return ""
}

func findExtensionQuality(manifest *ExtensionManifest, requested string) (QualityOption, bool) {
	if manifest != nil && requested != "" {
		for _, option := range manifest.QualityOptions {
			if strings.EqualFold(strings.TrimSpace(option.ID), requested) {
				return option, true
			}
		}
	}
	return QualityOption{}, false
}

func resolveExtensionDownloadQuality(requested string, source, target *ExtensionManifest) (string, error) {
	requested = strings.TrimSpace(requested)
	if target == nil || len(target.QualityOptions) == 0 {
		return requested, nil // Legacy providers without a quality declaration.
	}
	option, sourceRecognizes := findExtensionQuality(source, requested)
	if !sourceRecognizes {
		option = QualityOption{ID: requested}
	}
	kind := extensionQualityKind(option, source)
	if exact, ok := findExtensionQuality(target, requested); ok {
		targetKind := extensionQualityKind(exact, target)
		if source == target || (kind != "" && kind == targetKind) ||
			(kind == "" && targetKind != "spatial") {
			return strings.TrimSpace(exact.ID), nil
		}
	}
	if kind == "" {
		// An unknown foreign/default token must never opt into spatial audio.
		kind = "lossless"
	}
	allowedKinds := []string{kind}
	if kind == "spatial" || kind == "lossy" {
		allowedKinds = append(allowedKinds, "lossless")
	}
	for _, allowed := range allowedKinds {
		for _, candidate := range target.QualityOptions {
			id := strings.TrimSpace(candidate.ID)
			if id != "" && extensionQualityKind(candidate, target) == allowed {
				return id, nil
			}
		}
	}
	return "", fmt.Errorf("provider %s has no compatible %s quality for %q", target.Name, kind, requested)
}

func requestedQualityManifest(req DownloadRequest, manager *extensionManager) *ExtensionManifest {
	if manager == nil {
		return nil
	}
	for _, id := range []string{req.Service, req.Source} {
		if ext, err := manager.GetExtension(strings.TrimSpace(id)); err == nil && ext.Manifest != nil {
			if _, recognized := findExtensionQuality(ext.Manifest, strings.TrimSpace(req.Quality)); recognized {
				return ext.Manifest
			}
		}
	}
	return nil
}
