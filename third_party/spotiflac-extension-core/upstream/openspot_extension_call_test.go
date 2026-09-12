package gobackend

import "testing"

func TestCallOpenSpotExtensionJSONDispatchesTypedBridge(t *testing.T) {
	original := GetProviderPriority()
	t.Cleanup(func() { SetProviderPriority(original) })

	if result, err := CallOpenSpotExtensionJSON("SetProviderPriorityJSON", `["[\"provider-a\",\"provider-b\"]"]`); err != nil {
		t.Fatalf("set provider priority: %v", err)
	} else if result != "null" {
		t.Fatalf("set provider priority result = %q, want null", result)
	}

	result, err := CallOpenSpotExtensionJSON("GetProviderPriorityJSON", "[]")
	if err != nil {
		t.Fatalf("get provider priority: %v", err)
	}
	if result != `["provider-a","provider-b"]` {
		t.Fatalf("get provider priority result = %q", result)
	}
}

func TestCallOpenSpotExtensionJSONRejectsUnknownOperation(t *testing.T) {
	if _, err := CallOpenSpotExtensionJSON("NotAnExtensionOperation", "[]"); err == nil {
		t.Fatal("unknown operation unexpectedly succeeded")
	}
}

func TestCallOpenSpotExtensionJSONValidatesPackageManagementArguments(t *testing.T) {
	for _, operation := range []string{
		"LoadExtensionFromPath",
		"RemoveExtensionByID",
		"UpgradeExtensionFromPath",
		"CheckExtensionUpgradeFromPath",
	} {
		if _, err := CallOpenSpotExtensionJSON(operation, "[]"); err == nil {
			t.Fatalf("%s unexpectedly accepted a missing path/id", operation)
		}
	}
}
