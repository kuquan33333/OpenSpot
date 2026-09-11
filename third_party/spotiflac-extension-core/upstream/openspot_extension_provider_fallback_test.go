package gobackend

import (
	"reflect"
	"testing"
)

func TestOrderedExtensionDownloadProvidersUsesHintPriorityAndFallbackAllowList(t *testing.T) {
	manager := &extensionManager{
		extensions: map[string]*loadedExtension{
			"zeta": {
				ID:      "zeta",
				Enabled: true,
				Manifest: &ExtensionManifest{
					Types: []ExtensionType{ExtensionTypeDownloadProvider},
				},
			},
			"alpha": {
				ID:      "alpha",
				Enabled: true,
				Manifest: &ExtensionManifest{
					Types: []ExtensionType{ExtensionTypeDownloadProvider},
				},
			},
			"beta": {
				ID:      "beta",
				Enabled: true,
				Manifest: &ExtensionManifest{
					Types: []ExtensionType{ExtensionTypeDownloadProvider},
				},
			},
			"disabled": {
				ID:      "disabled",
				Enabled: false,
				Manifest: &ExtensionManifest{
					Types: []ExtensionType{ExtensionTypeDownloadProvider},
				},
			},
			"metadata": {
				ID:      "metadata",
				Enabled: true,
				Manifest: &ExtensionManifest{
					Types: []ExtensionType{ExtensionTypeMetadataProvider},
				},
			},
		},
	}

	previousPriority := GetProviderPriority()
	previousFallback := GetExtensionFallbackProviderIDs()
	defer func() {
		SetProviderPriority(previousPriority)
		SetExtensionFallbackProviderIDs(previousFallback)
	}()

	SetProviderPriority([]string{" beta ", "missing", "BETA"})
	SetExtensionFallbackProviderIDs([]string{"alpha", "beta"})

	ordered := manager.orderedExtensionDownloadProviders(" ZETA ", true)
	got := make([]string, len(ordered))
	for i, provider := range ordered {
		got[i] = provider.extension.ID
	}

	want := []string{"zeta", "beta", "alpha"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ordered provider IDs = %#v, want %#v", got, want)
	}

	strict := manager.orderedExtensionDownloadProviders("ZETA", false)
	if len(strict) != 1 || strict[0].extension.ID != "zeta" {
		t.Fatalf("strict provider order = %#v, want [zeta]", strict)
	}
}

func TestExtensionFallbackTrackIDPrefersAvailabilityAndProviderID(t *testing.T) {
	req := DownloadRequest{
		ProviderTrackID: "provider-track",
		SpotifyID:       "spotify-track",
		TidalID:         "tidal-track",
	}
	if got := extensionFallbackTrackID(req, &ExtAvailabilityResult{TrackID: "resolved-track"}); got != "resolved-track" {
		t.Fatalf("availability track ID = %q, want resolved-track", got)
	}
	if got := extensionFallbackTrackID(req, nil); got != "provider-track" {
		t.Fatalf("provider track ID = %q, want provider-track", got)
	}
}
