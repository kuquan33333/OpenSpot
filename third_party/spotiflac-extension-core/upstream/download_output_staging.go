package gobackend

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// downloadPathLocks serializes writes per final output path so concurrent
// downloads that resolve to the same file cannot interleave bytes into one
// output or race the staged-promote rename. Keys are normalized case-folded
// cleaned paths; entries live for the process lifetime.
var downloadPathLocks sync.Map

// lockDownloadOutputPath locks the given final output path and returns the
// unlock function. Different paths keep downloading in parallel; a second
// download of the same path blocks until the first finishes.
func lockDownloadOutputPath(path string) func() {
	key := strings.ToLower(filepath.Clean(path))
	value, _ := downloadPathLocks.LoadOrStore(key, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// stagedDownloadPath returns the sibling name downloads are streamed into
// before being promoted to the final path with an atomic rename.
func stagedDownloadPath(finalPath string) string {
	return finalPath + ".partial"
}

// syncDir best-effort fsyncs a directory so a just-renamed entry survives
// power loss. Unsupported on some platforms/filesystems; errors are ignored.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	defer d.Close()
	_ = d.Sync()
}
