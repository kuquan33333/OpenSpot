package gobackend

import (
	"strings"
	"sync"
)

var (
	appVersionMu sync.RWMutex
	appVersion   string
)

func SetAppVersion(version string) {
	normalized := strings.TrimSpace(version)

	appVersionMu.Lock()
	defer appVersionMu.Unlock()
	appVersion = normalized
}

func GetAppVersion() string {
	appVersionMu.RLock()
	defer appVersionMu.RUnlock()
	return appVersion
}

func appUserAgent() string {
	version := GetAppVersion()
	if version == "" {
		return "SpotiFLAC-Mobile"
	}
	return "SpotiFLAC-Mobile/" + version
}
