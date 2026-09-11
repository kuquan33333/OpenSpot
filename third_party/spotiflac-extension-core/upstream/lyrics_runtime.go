package gobackend

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// This file is the Extension Core-scoped host implementation of SpotiFLAC's
// lyrics utility. The full SpotiFLAC app has additional built-in providers;
// the vendored extension runtime keeps the source LRCLIB path and LRC semantics
// without importing the whole application lyrics subsystem.

type LRCLibResponse struct {
	ID           int     `json:"id"`
	Name         string  `json:"name"`
	TrackName    string  `json:"trackName"`
	ArtistName   string  `json:"artistName"`
	AlbumName    string  `json:"albumName"`
	Duration     float64 `json:"duration"`
	Instrumental bool    `json:"instrumental"`
	PlainLyrics  string  `json:"plainLyrics"`
	SyncedLyrics string  `json:"syncedLyrics"`
}

type LyricsLine struct {
	StartTimeMs int64  `json:"startTimeMs"`
	Words       string `json:"words"`
	EndTimeMs   int64  `json:"endTimeMs"`
}

type LyricsResponse struct {
	Lines        []LyricsLine `json:"lines"`
	SyncType     string       `json:"syncType"`
	Instrumental bool         `json:"instrumental"`
	PlainLyrics  string       `json:"plainLyrics"`
	Provider     string       `json:"provider"`
	Source       string       `json:"source"`
}

var (
	lrcLinePattern               = regexp.MustCompile(`\[(\d{1,3}):(\d{1,2})\.(\d{2,3})\](.*)`)
	rawLyricsMetadataLinePattern = regexp.MustCompile(`(?i)^\[[a-z][a-z0-9_]*:.*\]$`)
	rawLyricsTimestampPattern    = regexp.MustCompile(`^\[\d{1,3}:\d{1,2}(?:[.:]\d{1,3})?\]`)
)

func GetLyricsLRC(_ string, trackName, artistName, filePath string, durationMs int64) (string, error) {
	if strings.TrimSpace(filePath) != "" {
		lyrics, err := readLocalLyrics(filePath)
		if err == nil && rawLyricsHasUsableContent(lyrics) {
			return lyrics, nil
		}
		return "", nil
	}

	trackName = strings.TrimSpace(trackName)
	artistName = strings.TrimSpace(artistName)
	if trackName == "" || artistName == "" {
		return "", fmt.Errorf("track name and artist name are required")
	}

	client := &http.Client{
		Transport: extensionAPITransport,
		Timeout:   15 * time.Second,
	}
	durationSec := float64(durationMs) / 1000.0

	lyrics, err := fetchLRCLibExact(client, trackName, artistName, durationSec)
	if err != nil {
		lyrics, err = fetchLRCLibSearch(client, trackName, artistName, durationSec)
	}
	if err != nil {
		return "", err
	}
	if lyrics.Instrumental {
		return "[instrumental:true]", nil
	}
	return convertToLRCWithMetadata(lyrics, trackName, artistName), nil
}

func readLocalLyrics(filePath string) (string, error) {
	clean := filepath.Clean(filePath)
	if strings.EqualFold(filepath.Ext(clean), ".lrc") {
		data, err := os.ReadFile(clean)
		return string(data), err
	}
	sidecar := strings.TrimSuffix(clean, filepath.Ext(clean)) + ".lrc"
	data, err := os.ReadFile(sidecar)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func fetchLRCLibExact(client *http.Client, trackName, artistName string, durationSec float64) (*LyricsResponse, error) {
	params := url.Values{}
	params.Set("artist_name", artistName)
	params.Set("track_name", trackName)
	if durationSec > 0 {
		params.Set("duration", fmt.Sprintf("%.0f", durationSec))
	}

	var response LRCLibResponse
	if err := lrclibGET(client, "/api/get", params, &response); err != nil {
		return nil, err
	}
	if !lyricsMetadataMatches(&response, trackName, artistName, durationSec) {
		return nil, fmt.Errorf("LRCLIB returned mismatched track metadata")
	}
	return parseLRCLibResponse(&response), nil
}

func fetchLRCLibSearch(client *http.Client, trackName, artistName string, durationSec float64) (*LyricsResponse, error) {
	params := url.Values{}
	params.Set("q", strings.TrimSpace(artistName+" "+trackName))
	var results []LRCLibResponse
	if err := lrclibGET(client, "/api/search", params, &results); err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("lyrics not found")
	}

	var best *LRCLibResponse
	bestScore := -1
	for i := range results {
		candidate := &results[i]
		if !lyricsMetadataMatches(candidate, trackName, artistName, durationSec) {
			continue
		}
		score := 0
		if strings.TrimSpace(candidate.SyncedLyrics) != "" {
			score += 10
		}
		if strings.TrimSpace(candidate.PlainLyrics) != "" {
			score += 3
		}
		if durationSec > 0 && candidate.Duration > 0 {
			score += max(0, 5-int(math.Abs(candidate.Duration-durationSec)))
		}
		if score > bestScore {
			best = candidate
			bestScore = score
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no matching lyrics found")
	}
	return parseLRCLibResponse(best), nil
}

func lrclibGET(client *http.Client, path string, params url.Values, dst any) error {
	req, err := http.NewRequest(http.MethodGet, "https://lrclib.net"+path+"?"+params.Encode(), nil)
	if err != nil {
		return fmt.Errorf("failed to create lyrics request: %w", err)
	}
	req.Header.Set("User-Agent", getRandomUserAgent())
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch lyrics: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("lyrics not found")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("lyrics request returned HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("failed to decode lyrics response: %w", err)
	}
	return nil
}

func lyricsMetadataMatches(response *LRCLibResponse, trackName, artistName string, durationSec float64) bool {
	if response == nil {
		return false
	}
	candidateTrack := strings.TrimSpace(response.TrackName)
	if candidateTrack == "" {
		candidateTrack = strings.TrimSpace(response.Name)
	}
	if !looseLyricsTextMatch(candidateTrack, trackName) || !looseLyricsTextMatch(response.ArtistName, artistName) {
		return false
	}
	return durationSec <= 0 || response.Duration <= 0 || math.Abs(response.Duration-durationSec) <= 10
}

func looseLyricsTextMatch(a, b string) bool {
	normalize := func(value string) string {
		value = strings.ToLower(strings.TrimSpace(value))
		value = strings.NewReplacer("-", " ", "_", " ", "&", " and ").Replace(value)
		return strings.Join(strings.Fields(value), " ")
	}
	a = normalize(a)
	b = normalize(b)
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.Contains(a, b) || strings.Contains(b, a)
}

func parseLRCLibResponse(response *LRCLibResponse) *LyricsResponse {
	result := &LyricsResponse{
		Instrumental: response.Instrumental,
		PlainLyrics:  response.PlainLyrics,
		Provider:     "LRCLIB",
		Source:       "LRCLIB",
	}
	if response.Instrumental {
		return result
	}
	if strings.TrimSpace(response.SyncedLyrics) != "" {
		result.SyncType = "LINE_SYNCED"
		result.Lines = parseSyncedLyrics(response.SyncedLyrics)
		return result
	}
	result.SyncType = "UNSYNCED"
	result.Lines = plainTextLyricsLines(response.PlainLyrics)
	return result
}

func rawLyricsHasUsableContent(raw string) bool {
	if strings.EqualFold(strings.TrimSpace(raw), "[instrumental:true]") {
		return true
	}
	for _, line := range strings.Split(raw, "\n") {
		cleaned := strings.TrimSpace(line)
		if cleaned == "" || rawLyricsMetadataLinePattern.MatchString(cleaned) {
			continue
		}
		for rawLyricsTimestampPattern.MatchString(cleaned) {
			cleaned = strings.TrimSpace(rawLyricsTimestampPattern.ReplaceAllString(cleaned, ""))
		}
		if cleaned != "" {
			return true
		}
	}
	return false
}

func parseSyncedLyrics(raw string) []LyricsLine {
	lines := make([]LyricsLine, 0)
	for _, line := range strings.Split(raw, "\n") {
		match := lrcLinePattern.FindStringSubmatch(strings.TrimSpace(line))
		if len(match) != 5 {
			continue
		}
		startMs := lrcTimestampToMS(match[1], match[2], match[3])
		words := strings.TrimSpace(match[4])
		if words == "" {
			continue
		}
		lines = append(lines, LyricsLine{StartTimeMs: startMs, Words: words})
	}
	for i := 0; i+1 < len(lines); i++ {
		lines[i].EndTimeMs = lines[i+1].StartTimeMs
	}
	if len(lines) > 0 {
		lines[len(lines)-1].EndTimeMs = lines[len(lines)-1].StartTimeMs + 5000
	}
	return lines
}

func plainTextLyricsLines(raw string) []LyricsLine {
	lines := make([]LyricsLine, 0)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, LyricsLine{Words: line})
		}
	}
	return lines
}

func lrcTimestampToMS(minutes, seconds, fraction string) int64 {
	var min, sec, frac int64
	_, _ = fmt.Sscan(minutes, &min)
	_, _ = fmt.Sscan(seconds, &sec)
	_, _ = fmt.Sscan(fraction, &frac)
	if len(fraction) == 2 {
		frac *= 10
	}
	return min*60*1000 + sec*1000 + frac
}

func msToLRCTimestamp(ms int64) string {
	totalSeconds := ms / 1000
	minutes := totalSeconds / 60
	seconds := totalSeconds % 60
	centiseconds := (ms % 1000) / 10
	return fmt.Sprintf("[%02d:%02d.%02d]", minutes, seconds, centiseconds)
}

func convertToLRCWithMetadata(lyrics *LyricsResponse, trackName, artistName string) string {
	if lyrics == nil || len(lyrics.Lines) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("[ti:%s]\n", trackName))
	builder.WriteString(fmt.Sprintf("[ar:%s]\n", artistName))
	builder.WriteString(fmt.Sprintf("[by:SpotiFLAC-Mobile (source: %s)]\n\n", lyrics.Source))
	if lyrics.SyncType == "LINE_SYNCED" {
		for _, line := range lyrics.Lines {
			if strings.TrimSpace(line.Words) == "" {
				continue
			}
			builder.WriteString(msToLRCTimestamp(line.StartTimeMs))
			builder.WriteString(line.Words)
			builder.WriteByte('\n')
		}
	} else {
		for _, line := range lyrics.Lines {
			if strings.TrimSpace(line.Words) != "" {
				builder.WriteString(line.Words)
				builder.WriteByte('\n')
			}
		}
	}
	return builder.String()
}
