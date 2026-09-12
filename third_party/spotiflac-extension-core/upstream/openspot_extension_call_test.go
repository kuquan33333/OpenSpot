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

func TestCallOpenSpotExtensionJSONEncodesRepositoryURL(t *testing.T) {
	if err := InitExtensionRepoJSON(t.TempDir()); err != nil {
		t.Fatalf("init extension repo: %v", err)
	}

	repo := getExtensionRepo()
	originalURL := repo.getRegistryURL()
	t.Cleanup(func() { repo.setRegistryURL(originalURL) })

	const registryURL = "https://raw.githubusercontent.com/example/OpenSpot/main/registry.json"
	if err := SetRepoRegistryURLJSON(registryURL); err != nil {
		t.Fatalf("set repository URL: %v", err)
	}

	result, err := CallOpenSpotExtensionJSON("GetRepoRegistryURLJSON", "[]")
	if err != nil {
		t.Fatalf("get repository URL: %v", err)
	}
	if result != `"https://raw.githubusercontent.com/example/OpenSpot/main/registry.json"` {
		t.Fatalf("repository URL result = %q", result)
	}
}
