package gobackend

// OpenSpot host adaptation of the SpotiFLAC download metadata enrichment
// boundary. The original application routes these calls through its full
// Deezer/MusicBrainz catalog clients. Extension Core only needs the concrete
// ISRC enrichment behavior, so the network lookup is kept here without pulling
// the rest of SpotiFLAC's catalog/search subsystem into the vendored runtime.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type AlbumExtendedMetadata struct {
	Genre     string
	Label     string
	Copyright string
}

var fetchDeezerExtendedMetadataByISRC = func(ctx context.Context, isrc string) (*AlbumExtendedMetadata, error) {
	return fetchDeezerExtendedMetadataByISRCCore(ctx, isrc)
}

var fetchMusicBrainzGenreByISRC = FetchMusicBrainzGenreByISRC
var fetchMusicBrainzAlbumArtistByISRC = FetchMusicBrainzAlbumArtistByISRC

func fetchDeezerExtendedMetadataByISRCCore(ctx context.Context, isrc string) (*AlbumExtendedMetadata, error) {
	normalizedISRC := strings.TrimSpace(isrc)
	if normalizedISRC == "" {
		return nil, fmt.Errorf("empty ISRC")
	}

	client := NewHTTPClientWithTimeout(25 * time.Second)
	trackURL := "https://api.deezer.com/2.0/track/isrc:" + url.PathEscape(normalizedISRC)
	var track struct {
		ID    int64 `json:"id"`
		Album struct {
			ID int64 `json:"id"`
		} `json:"album"`
		Error *struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}
	if err := getExtensionMetadataJSON(ctx, client, trackURL, &track); err != nil {
		return nil, fmt.Errorf("failed to find track by ISRC: %w", err)
	}
	if track.Error != nil {
		return nil, fmt.Errorf("deezer API error: %s (code %d)", track.Error.Message, track.Error.Code)
	}
	if track.ID == 0 || track.Album.ID == 0 {
		return nil, fmt.Errorf("track found but no Deezer album ID")
	}

	albumURL := fmt.Sprintf("https://api.deezer.com/2.0/album/%d", track.Album.ID)
	var album struct {
		Label     string `json:"label"`
		Copyright string `json:"copyright"`
		Genres    struct {
			Data []struct {
				Name string `json:"name"`
			} `json:"data"`
		} `json:"genres"`
		Error *struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}
	if err := getExtensionMetadataJSON(ctx, client, albumURL, &album); err != nil {
		return nil, fmt.Errorf("failed to fetch album: %w", err)
	}
	if album.Error != nil {
		return nil, fmt.Errorf("deezer API error: %s (code %d)", album.Error.Message, album.Error.Code)
	}

	genres := make([]string, 0, len(album.Genres.Data))
	for _, genre := range album.Genres.Data {
		if name := strings.TrimSpace(genre.Name); name != "" {
			genres = append(genres, name)
		}
	}
	return &AlbumExtendedMetadata{
		Genre:     strings.Join(genres, ", "),
		Label:     strings.TrimSpace(album.Label),
		Copyright: strings.TrimSpace(album.Copyright),
	}, nil
}

func getExtensionMetadataJSON(ctx context.Context, client *http.Client, endpoint string, dst any) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", getRandomUserAgent())

		resp, err := client.Do(req)
		if err == nil && resp != nil && resp.StatusCode == http.StatusOK {
			decodeErr := json.NewDecoder(resp.Body).Decode(dst)
			resp.Body.Close()
			return decodeErr
		}
		if resp != nil {
			resp.Body.Close()
			if err == nil {
				err = fmt.Errorf("metadata API returned status %d", resp.StatusCode)
			}
		}
		lastErr = err
		if attempt < 2 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(500 * time.Millisecond * time.Duration(1<<attempt)):
			}
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("metadata request failed without response")
	}
	return lastErr
}

const musicBrainzAPIBase = "https://musicbrainz.org/ws/2"

const (
	musicBrainzCachePositiveTTL = 6 * time.Hour
	musicBrainzCacheNegativeTTL = 10 * time.Minute
	musicBrainzCacheMaxEntries  = 256
)

type musicBrainzCacheEntry struct {
	value     string
	err       error
	expiresAt time.Time
}

var (
	musicBrainzCacheMu sync.Mutex
	musicBrainzCache   = make(map[string]musicBrainzCacheEntry)
	musicBrainzFlight  singleflight.Group
)

func musicBrainzCached(key string, fetch func() (string, error)) (string, error) {
	musicBrainzCacheMu.Lock()
	if entry, ok := musicBrainzCache[key]; ok && time.Now().Before(entry.expiresAt) {
		musicBrainzCacheMu.Unlock()
		return entry.value, entry.err
	}
	musicBrainzCacheMu.Unlock()

	result, err, _ := musicBrainzFlight.Do(key, func() (any, error) {
		musicBrainzCacheMu.Lock()
		if entry, ok := musicBrainzCache[key]; ok && time.Now().Before(entry.expiresAt) {
			musicBrainzCacheMu.Unlock()
			return entry.value, entry.err
		}
		musicBrainzCacheMu.Unlock()

		value, fetchErr := fetch()
		ttl := musicBrainzCachePositiveTTL
		if fetchErr != nil || value == "" {
			ttl = musicBrainzCacheNegativeTTL
		}
		musicBrainzCacheMu.Lock()
		if len(musicBrainzCache) >= musicBrainzCacheMaxEntries {
			now := time.Now()
			for cacheKey, entry := range musicBrainzCache {
				if now.After(entry.expiresAt) {
					delete(musicBrainzCache, cacheKey)
				}
			}
			if len(musicBrainzCache) >= musicBrainzCacheMaxEntries {
				musicBrainzCache = make(map[string]musicBrainzCacheEntry)
			}
		}
		musicBrainzCache[key] = musicBrainzCacheEntry{value: value, err: fetchErr, expiresAt: time.Now().Add(ttl)}
		musicBrainzCacheMu.Unlock()
		return value, fetchErr
	})
	if err != nil {
		return "", err
	}
	value, _ := result.(string)
	return value, nil
}

type musicBrainzTag struct {
	Count int    `json:"count"`
	Name  string `json:"name"`
}

type musicBrainzArtistCredit struct {
	Name       string `json:"name"`
	JoinPhrase string `json:"joinphrase"`
}

type musicBrainzRelease struct {
	Title        string                     `json:"title"`
	ArtistCredit []musicBrainzArtistCredit `json:"artist-credit"`
}

type musicBrainzCombinedResponse struct {
	Recordings []struct {
		Tags     []musicBrainzTag     `json:"tags"`
		Releases []musicBrainzRelease `json:"releases"`
	} `json:"recordings"`
}

func fetchMusicBrainzCombinedByISRC(isrc string) (*musicBrainzCombinedResponse, string, error) {
	normalizedISRC := strings.ToUpper(strings.TrimSpace(isrc))
	key := "recording\x00" + normalizedISRC
	encoded, err := musicBrainzCached(key, func() (string, error) {
		var payload musicBrainzCombinedResponse
		normalized, fetchErr := fetchMusicBrainzRecordingByISRC(isrc, "tags+releases+artist-credits", &payload)
		if fetchErr != nil {
			return "", fetchErr
		}
		data, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return "", marshalErr
		}
		if normalizedISRC == "" {
			normalizedISRC = normalized
		}
		return string(data), nil
	})
	if err != nil {
		return nil, normalizedISRC, err
	}
	var payload musicBrainzCombinedResponse
	if err := json.Unmarshal([]byte(encoded), &payload); err != nil {
		return nil, normalizedISRC, err
	}
	return &payload, normalizedISRC, nil
}

func formatMusicBrainzGenre(tags []musicBrainzTag) string {
	if len(tags) == 0 {
		return ""
	}
	caser := cases.Title(language.English)
	seen := make(map[string]struct{}, len(tags))
	maxCount := -1
	bestTag := ""
	for _, tag := range tags {
		name := strings.TrimSpace(tag.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		formatted := caser.String(name)
		if tag.Count > maxCount {
			maxCount = tag.Count
			bestTag = formatted
		}
	}
	return bestTag
}

func formatMusicBrainzArtistCredit(credits []musicBrainzArtistCredit) string {
	var builder strings.Builder
	for _, credit := range credits {
		name := strings.TrimSpace(credit.Name)
		if name == "" {
			continue
		}
		builder.WriteString(name)
		builder.WriteString(credit.JoinPhrase)
	}
	return strings.TrimSpace(builder.String())
}

func selectMusicBrainzAlbumArtist(releases []musicBrainzRelease, albumName string) string {
	if len(releases) == 0 {
		return ""
	}
	normalizedAlbum := strings.ToLower(strings.TrimSpace(albumName))
	if normalizedAlbum != "" {
		for _, release := range releases {
			if strings.ToLower(strings.TrimSpace(release.Title)) != normalizedAlbum {
				continue
			}
			if albumArtist := formatMusicBrainzArtistCredit(release.ArtistCredit); albumArtist != "" {
				return albumArtist
			}
		}
	}
	for _, release := range releases {
		if albumArtist := formatMusicBrainzArtistCredit(release.ArtistCredit); albumArtist != "" {
			return albumArtist
		}
	}
	return ""
}

func fetchMusicBrainzRecordingByISRC(isrc string, inc string, payload any) (string, error) {
	normalizedISRC := strings.ToUpper(strings.TrimSpace(isrc))
	if normalizedISRC == "" {
		return "", fmt.Errorf("no ISRC provided")
	}
	client := NewHTTPClientWithTimeout(10 * time.Second)
	query := fmt.Sprintf("isrc:%s", normalizedISRC)
	reqURL := fmt.Sprintf("%s/recording?query=%s&fmt=json&inc=%s", musicBrainzAPIBase, url.QueryEscape(query), inc)
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", getRandomUserAgent())

	var resp *http.Response
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		resp, lastErr = client.Do(req)
		if lastErr == nil && resp.StatusCode == http.StatusOK {
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		if attempt < 2 {
			time.Sleep(2 * time.Second)
		}
	}
	if lastErr != nil {
		return "", lastErr
	}
	if resp == nil {
		return "", fmt.Errorf("MusicBrainz request failed without response")
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return "", fmt.Errorf("MusicBrainz API returned status: %d", resp.StatusCode)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(payload); err != nil {
		return "", err
	}
	return normalizedISRC, nil
}

func FetchMusicBrainzAlbumArtistByISRC(isrc string, albumName string) (string, error) {
	payload, normalizedISRC, err := fetchMusicBrainzCombinedByISRC(isrc)
	if err != nil {
		return "", err
	}
	for _, recording := range payload.Recordings {
		if albumArtist := selectMusicBrainzAlbumArtist(recording.Releases, albumName); albumArtist != "" {
			return albumArtist, nil
		}
	}
	return "", fmt.Errorf("no MusicBrainz album artist found for ISRC: %s", normalizedISRC)
}

func FetchMusicBrainzGenreByISRC(isrc string) (string, error) {
	payload, normalizedISRC, err := fetchMusicBrainzCombinedByISRC(isrc)
	if err != nil {
		return "", err
	}
	if len(payload.Recordings) == 0 {
		return "", fmt.Errorf("no recordings found for ISRC: %s", normalizedISRC)
	}
	genre := formatMusicBrainzGenre(payload.Recordings[0].Tags)
	if genre == "" {
		return "", fmt.Errorf("no MusicBrainz genre tags found for ISRC: %s", normalizedISRC)
	}
	return genre, nil
}
