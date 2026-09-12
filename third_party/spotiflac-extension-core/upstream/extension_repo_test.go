package gobackend

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchRegistryRejectsHTMLResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = writer.Write([]byte("<!doctype html><html><body>Not found</body></html>"))
	}))
	t.Cleanup(server.Close)

	repo := &extensionRepo{
		registryURL: server.URL,
		cacheTTL:    time.Minute,
	}
	_, err := repo.fetchRegistryUncoalesced(server.URL, true)
	if err == nil || !strings.Contains(err.Error(), "HTML instead of JSON") {
		t.Fatalf("fetch HTML registry error = %v, want an actionable HTML response error", err)
	}
}

func TestParseRegistryBodyRequiresExtensionsArray(t *testing.T) {
	if _, err := parseRegistryBody([]byte(`{"version":1}`)); err == nil || !strings.Contains(err.Error(), "missing extensions array") {
		t.Fatalf("parse malformed registry error = %v, want missing extensions array", err)
	}
}
