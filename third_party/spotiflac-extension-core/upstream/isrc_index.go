package gobackend

import (
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ISRCIndex is the Extension Core-scoped form of SpotiFLAC's duplicate index.
// Entries added after successful downloads are write-maintained for every
// supported format; cold directory rebuilds parse FLAC Vorbis comments directly
// without importing the full application metadata/tag subsystem.
type isrcFileEntry struct {
	size    int64
	modTime int64
	isrc    string
}

type ISRCIndex struct {
	index     map[string]string
	files     map[string]isrcFileEntry
	outputDir string
	buildTime atomic.Int64
	mu        sync.RWMutex
}

var (
	isrcIndexCache   = make(map[string]*ISRCIndex)
	isrcIndexCacheMu sync.RWMutex
	isrcBuildingMu   sync.Map
	isrcIndexTTL     = 5 * time.Minute
)

func (idx *ISRCIndex) isFresh() bool {
	built := idx.buildTime.Load()
	return built != 0 && time.Since(time.Unix(0, built)) < isrcIndexTTL
}

func GetISRCIndex(outputDir string) *ISRCIndex {
	outputDir = filepath.Clean(strings.TrimSpace(outputDir))
	isrcIndexCacheMu.RLock()
	idx, exists := isrcIndexCache[outputDir]
	isrcIndexCacheMu.RUnlock()
	if exists && idx.isFresh() {
		return idx
	}

	buildLock, _ := isrcBuildingMu.LoadOrStore(outputDir, &sync.Mutex{})
	mu := buildLock.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	isrcIndexCacheMu.RLock()
	idx, exists = isrcIndexCache[outputDir]
	isrcIndexCacheMu.RUnlock()
	if exists && idx.isFresh() {
		return idx
	}
	return buildISRCIndex(outputDir)
}

func buildISRCIndex(outputDir string) *ISRCIndex {
	idx := &ISRCIndex{
		index:     make(map[string]string),
		files:     make(map[string]isrcFileEntry),
		outputDir: outputDir,
	}
	idx.buildTime.Store(time.Now().UnixNano())
	if outputDir == "" || outputDir == "." {
		return idx
	}

	// Preserve write-maintained entries from the previous generation first.
	isrcIndexCacheMu.RLock()
	previous := isrcIndexCache[outputDir]
	if previous != nil {
		previous.mu.RLock()
		for key, path := range previous.index {
			if checkFileExists(path) {
				idx.index[key] = path
			}
		}
		for path, entry := range previous.files {
			if checkFileExists(path) {
				idx.files[path] = entry
			}
		}
		previous.mu.RUnlock()
	}
	isrcIndexCacheMu.RUnlock()

	// FLAC can be rebuilt natively from the Vorbis comment block without the
	// app's larger MP3/M4A/Ogg metadata subsystem. Other formats remain fully
	// indexed when AddToISRCIndex is called after a successful download.
	_ = filepath.Walk(outputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() || !strings.EqualFold(filepath.Ext(path), ".flac") {
			return nil
		}
		isrc := strings.ToUpper(strings.TrimSpace(readFLACISRC(path)))
		entry := isrcFileEntry{size: info.Size(), modTime: info.ModTime().UnixNano(), isrc: isrc}
		idx.files[path] = entry
		if isrc != "" {
			idx.index[isrc] = path
		}
		return nil
	})

	isrcIndexCacheMu.Lock()
	isrcIndexCache[outputDir] = idx
	isrcIndexCacheMu.Unlock()
	return idx
}

func (idx *ISRCIndex) lookup(isrc string) (string, bool) {
	isrc = strings.ToUpper(strings.TrimSpace(isrc))
	if isrc == "" {
		return "", false
	}
	idx.mu.RLock()
	path, exists := idx.index[isrc]
	idx.mu.RUnlock()
	return path, exists
}

func (idx *ISRCIndex) remove(isrc string) {
	isrc = strings.ToUpper(strings.TrimSpace(isrc))
	if isrc == "" {
		return
	}
	idx.mu.Lock()
	path := idx.index[isrc]
	delete(idx.index, isrc)
	if path != "" {
		delete(idx.files, path)
	}
	idx.mu.Unlock()
}

func (idx *ISRCIndex) Add(isrc, filePath string) {
	upper := strings.ToUpper(strings.TrimSpace(isrc))
	filePath = filepath.Clean(strings.TrimSpace(filePath))
	if upper == "" || filePath == "" || filePath == "." {
		return
	}

	var entry *isrcFileEntry
	if info, err := os.Stat(filePath); err == nil && !info.IsDir() {
		entry = &isrcFileEntry{size: info.Size(), modTime: info.ModTime().UnixNano(), isrc: upper}
	}

	idx.mu.Lock()
	idx.index[upper] = filePath
	if entry != nil {
		idx.files[filePath] = *entry
	}
	idx.mu.Unlock()
	idx.buildTime.Store(time.Now().UnixNano())
}

func checkISRCExistsInternal(outputDir, isrc string) (string, bool) {
	if strings.TrimSpace(outputDir) == "" || strings.TrimSpace(isrc) == "" {
		return "", false
	}
	idx := GetISRCIndex(outputDir)
	filePath, exists := idx.lookup(isrc)
	if !exists {
		return "", false
	}
	if !checkFileExists(filePath) {
		idx.remove(isrc)
		return "", false
	}
	return filePath, true
}

func AddToISRCIndex(outputDir, isrc, filePath string) {
	if strings.TrimSpace(outputDir) == "" || strings.TrimSpace(isrc) == "" || strings.TrimSpace(filePath) == "" {
		return
	}
	// Ensure the cache exists so an add performed before the first lookup is
	// never silently discarded.
	idx := GetISRCIndex(outputDir)
	idx.Add(isrc, filePath)
}

func checkFileExists(filePath string) bool {
	info, err := os.Stat(filePath)
	return err == nil && info != nil && !info.IsDir() && info.Size() > 0
}

func readFLACISRC(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	magic := make([]byte, 4)
	if _, err := io.ReadFull(f, magic); err != nil || string(magic) != "fLaC" {
		return ""
	}

	header := make([]byte, 4)
	for {
		if _, err := io.ReadFull(f, header); err != nil {
			return ""
		}
		last := header[0]&0x80 != 0
		blockType := header[0] & 0x7F
		length := int64(header[1])<<16 | int64(header[2])<<8 | int64(header[3])
		if blockType == 4 {
			if length < 0 || length > 16<<20 {
				return ""
			}
			payload := make([]byte, length)
			if _, err := io.ReadFull(f, payload); err != nil {
				return ""
			}
			return vorbisCommentISRC(payload)
		}
		if last {
			return ""
		}
		if _, err := f.Seek(length, io.SeekCurrent); err != nil {
			return ""
		}
	}
}

func vorbisCommentISRC(payload []byte) string {
	if len(payload) < 8 {
		return ""
	}
	offset := int(binary.LittleEndian.Uint32(payload[0:4])) + 4
	if offset < 4 || offset+4 > len(payload) {
		return ""
	}
	count := int(binary.LittleEndian.Uint32(payload[offset : offset+4]))
	offset += 4
	for i := 0; i < count; i++ {
		if offset+4 > len(payload) {
			return ""
		}
		commentLen := int(binary.LittleEndian.Uint32(payload[offset : offset+4]))
		offset += 4
		if commentLen < 0 || offset+commentLen > len(payload) {
			return ""
		}
		comment := payload[offset : offset+commentLen]
		offset += commentLen
		eq := strings.IndexByte(string(comment), '=')
		if eq > 0 && strings.EqualFold(string(comment[:eq]), "ISRC") {
			return strings.TrimSpace(string(comment[eq+1:]))
		}
	}
	return ""
}
