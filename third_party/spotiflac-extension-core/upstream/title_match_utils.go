package gobackend

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

func writeNormalizedArtistRune(b *strings.Builder, r rune) {
	switch r {
	case 'đ':
		b.WriteString("dj")
	case 'ß':
		b.WriteString("ss")
	case 'æ':
		b.WriteString("ae")
	case 'œ':
		b.WriteString("oe")
	default:
		b.WriteRune(r)
	}
}

func normalizeLooseTitle(title string) string {
	trimmed := strings.TrimSpace(strings.ToLower(title))
	if trimmed == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(trimmed))

	for _, r := range trimmed {
		switch {
		case unicode.IsLetter(r), unicode.IsNumber(r):
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteByte(' ')
		case r == '/', r == '\\', r == '_', r == '-', r == '|', r == '.', r == '&', r == '+':
			b.WriteByte(' ')
		default:
		}
	}

	return strings.Join(strings.Fields(b.String()), " ")
}

func normalizeLooseArtistName(name string) string {
	trimmed := strings.TrimSpace(strings.ToLower(name))
	if trimmed == "" {
		return ""
	}

	decomposed := norm.NFD.String(trimmed)

	var b strings.Builder
	b.Grow(len(decomposed))

	for _, r := range decomposed {
		switch {
		case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Mc, r), unicode.Is(unicode.Me, r):
			continue
		case unicode.IsLetter(r), unicode.IsNumber(r):
			writeNormalizedArtistRune(&b, r)
		case unicode.IsSpace(r):
			b.WriteByte(' ')
		case r == '/', r == '\\', r == '_', r == '-', r == '|', r == '.', r == '&', r == '+':
			b.WriteByte(' ')
		default:
		}
	}

	return strings.Join(strings.Fields(b.String()), " ")
}

func hasAlphaNumericRunes(value string) bool {
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return true
		}
	}
	return false
}

func normalizeSymbolOnlyTitle(title string) string {
	trimmed := strings.TrimSpace(strings.ToLower(title))
	if trimmed == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(trimmed))

	for _, r := range trimmed {
		switch {
		case unicode.IsLetter(r), unicode.IsNumber(r), unicode.IsSpace(r), unicode.IsPunct(r):
			continue
		// Drop combining marks such as emoji variation selectors.
		case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Mc, r), unicode.Is(unicode.Me, r):
			continue
		default:
			b.WriteRune(r)
		}
	}

	return b.String()
}

func artistsMatch(expectedArtist, foundArtist string) bool {
	normExpected := normalizeLooseArtistName(expectedArtist)
	normFound := normalizeLooseArtistName(foundArtist)

	if normExpected == normFound {
		return true
	}

	if strings.Contains(normExpected, normFound) ||
		strings.Contains(normFound, normExpected) {
		return true
	}

	expectedArtists := splitArtists(expectedArtist)
	foundArtists := splitArtists(foundArtist)

	for _, expected := range expectedArtists {
		for _, found := range foundArtists {
			if expected == found {
				return true
			}
			if strings.Contains(expected, found) ||
				strings.Contains(found, expected) {
				return true
			}
			if sameWordsUnordered(expected, found) {
				return true
			}
		}
	}

	return isLatinScript(expectedArtist) != isLatinScript(foundArtist)
}

func splitArtists(artists string) []string {
	normalized := strings.ToLower(artists)
	normalized = strings.ReplaceAll(normalized, " feat. ", "|")
	normalized = strings.ReplaceAll(normalized, " feat ", "|")
	normalized = strings.ReplaceAll(normalized, " ft. ", "|")
	normalized = strings.ReplaceAll(normalized, " ft ", "|")
	normalized = strings.ReplaceAll(normalized, " & ", "|")
	normalized = strings.ReplaceAll(normalized, " and ", "|")
	normalized = strings.ReplaceAll(normalized, ",", "|")
	normalized = strings.ReplaceAll(normalized, ";", "|")
	normalized = strings.ReplaceAll(normalized, " x ", "|")

	parts := strings.Split(normalized, "|")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := normalizeLooseArtistName(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func sameWordsUnordered(a, b string) bool {
	wordsA := strings.Fields(a)
	wordsB := strings.Fields(b)
	if len(wordsA) != len(wordsB) || len(wordsA) == 0 {
		return false
	}

	sortedA := make([]string, len(wordsA))
	sortedB := make([]string, len(wordsB))
	copy(sortedA, wordsA)
	copy(sortedB, wordsB)

	for i := 0; i < len(sortedA)-1; i++ {
		for j := i + 1; j < len(sortedA); j++ {
			if sortedA[i] > sortedA[j] {
				sortedA[i], sortedA[j] = sortedA[j], sortedA[i]
			}
			if sortedB[i] > sortedB[j] {
				sortedB[i], sortedB[j] = sortedB[j], sortedB[i]
			}
		}
	}

	for i := range sortedA {
		if sortedA[i] != sortedB[i] {
			return false
		}
	}
	return true
}

func titlesMatch(expectedTitle, foundTitle string) bool {
	normExpected := strings.ToLower(strings.TrimSpace(expectedTitle))
	normFound := strings.ToLower(strings.TrimSpace(foundTitle))

	if normExpected == normFound {
		return true
	}

	if strings.Contains(normExpected, normFound) ||
		strings.Contains(normFound, normExpected) {
		return true
	}

	cleanExpected := cleanTitle(normExpected)
	cleanFound := cleanTitle(normFound)
	if cleanExpected == cleanFound {
		return true
	}

	if cleanExpected != "" && cleanFound != "" {
		if strings.Contains(cleanExpected, cleanFound) ||
			strings.Contains(cleanFound, cleanExpected) {
			return true
		}
	}

	coreExpected := extractCoreTitle(normExpected)
	coreFound := extractCoreTitle(normFound)
	if coreExpected != "" && coreFound != "" && coreExpected == coreFound {
		return true
	}

	looseExpected := normalizeLooseTitle(normExpected)
	looseFound := normalizeLooseTitle(normFound)
	if looseExpected != "" && looseFound != "" {
		if looseExpected == looseFound {
			return true
		}
		if strings.Contains(looseExpected, looseFound) ||
			strings.Contains(looseFound, looseExpected) {
			return true
		}
	}

	if (!hasAlphaNumericRunes(expectedTitle) || !hasAlphaNumericRunes(foundTitle)) &&
		strings.TrimSpace(expectedTitle) != "" &&
		strings.TrimSpace(foundTitle) != "" {
		expectedSymbols := normalizeSymbolOnlyTitle(expectedTitle)
		foundSymbols := normalizeSymbolOnlyTitle(foundTitle)
		if expectedSymbols != "" &&
			foundSymbols != "" &&
			expectedSymbols == foundSymbols {
			return true
		}
	}

	return false
}

var trackTitleAnnotationPattern = regexp.MustCompile(`(?i)[(\[]\s*(?:(?:feat\.?|ft\.?|featuring)\s+[^)\]]+|from\s+["“][^)\]]+["”]\s*)[)\]]`)

func normalizeTrackIdentityTitle(title string) string {
	return normalizeLooseTitle(trackTitleAnnotationPattern.ReplaceAllString(title, " "))
}

func trackTitlesMatch(expectedTitle, foundTitle string) bool {
	expectedTitle = trackTitleAnnotationPattern.ReplaceAllString(expectedTitle, " ")
	foundTitle = trackTitleAnnotationPattern.ReplaceAllString(foundTitle, " ")
	expected := normalizeLooseTitle(expectedTitle)
	found := normalizeLooseTitle(foundTitle)
	if expected != "" && expected == found {
		return true
	}

	// Version words identify recordings; punctuation around them does not.
	for _, title := range []string{expected, found} {
		for _, word := range strings.Fields(title) {
			switch word {
			case "mix", "remix", "live", "acoustic", "demo", "instrumental",
				"karaoke", "edit", "extended", "slowed", "sped":
				return false
			}
		}
	}
	return titlesMatch(expectedTitle, foundTitle)
}

func extractCoreTitle(title string) string {
	parenIdx := strings.Index(title, "(")
	bracketIdx := strings.Index(title, "[")
	dashIdx := strings.Index(title, " - ")

	cutIdx := len(title)
	if parenIdx > 0 && parenIdx < cutIdx {
		cutIdx = parenIdx
	}
	if bracketIdx > 0 && bracketIdx < cutIdx {
		cutIdx = bracketIdx
	}
	if dashIdx > 0 && dashIdx < cutIdx {
		cutIdx = dashIdx
	}

	return strings.TrimSpace(title[:cutIdx])
}

func cleanTitle(title string) string {
	cleaned := title

	versionPatterns := []string{
		"remaster", "remastered", "deluxe", "bonus", "single",
		"album version", "radio edit", "original mix", "extended",
		"club mix", "remix", "live", "acoustic", "demo",
	}

	for {
		startParen := strings.LastIndex(cleaned, "(")
		endParen := strings.LastIndex(cleaned, ")")
		if startParen >= 0 && endParen > startParen {
			content := strings.ToLower(cleaned[startParen+1 : endParen])
			isVersionIndicator := false
			for _, pattern := range versionPatterns {
				if strings.Contains(content, pattern) {
					isVersionIndicator = true
					break
				}
			}
			if isVersionIndicator {
				cleaned = strings.TrimSpace(cleaned[:startParen]) + cleaned[endParen+1:]
				continue
			}
		}
		break
	}

	for {
		startBracket := strings.LastIndex(cleaned, "[")
		endBracket := strings.LastIndex(cleaned, "]")
		if startBracket >= 0 && endBracket > startBracket {
			content := strings.ToLower(cleaned[startBracket+1 : endBracket])
			isVersionIndicator := false
			for _, pattern := range versionPatterns {
				if strings.Contains(content, pattern) {
					isVersionIndicator = true
					break
				}
			}
			if isVersionIndicator {
				cleaned = strings.TrimSpace(cleaned[:startBracket]) + cleaned[endBracket+1:]
				continue
			}
		}
		break
	}

	dashPatterns := []string{
		" - remaster", " - remastered", " - single version", " - radio edit",
		" - live", " - acoustic", " - demo", " - remix",
	}
	for _, pattern := range dashPatterns {
		if strings.HasSuffix(strings.ToLower(cleaned), pattern) {
			cleaned = cleaned[:len(cleaned)-len(pattern)]
		}
	}

	for strings.Contains(cleaned, "  ") {
		cleaned = strings.ReplaceAll(cleaned, "  ", " ")
	}

	return strings.TrimSpace(cleaned)
}

func isLatinScript(value string) bool {
	for _, r := range value {
		if r < 128 {
			continue
		}
		if (r >= 0x0100 && r <= 0x024F) ||
			(r >= 0x1E00 && r <= 0x1EFF) ||
			(r >= 0x00C0 && r <= 0x00FF) {
			continue
		}
		if (r >= 0x4E00 && r <= 0x9FFF) ||
			(r >= 0x3040 && r <= 0x309F) ||
			(r >= 0x30A0 && r <= 0x30FF) ||
			(r >= 0xAC00 && r <= 0xD7AF) ||
			(r >= 0x0600 && r <= 0x06FF) ||
			(r >= 0x0400 && r <= 0x04FF) {
			return false
		}
	}
	return true
}

type resolvedTrackInfo struct {
	Title                string
	ArtistName           string
	AlbumName            string
	ISRC                 string
	Duration             int
	SkipNameVerification bool
}

func exactLooseIdentityMatch(expected, found string, normalize func(string) string) bool {
	normExpected := normalize(expected)
	normFound := normalize(found)
	if normExpected != "" && normFound != "" {
		return normExpected == normFound
	}
	return strings.EqualFold(strings.TrimSpace(expected), strings.TrimSpace(found))
}

func durationMatchesRequest(req DownloadRequest, resolved resolvedTrackInfo) bool {
	expectedDurationSec := req.DurationMS / 1000
	if expectedDurationSec <= 0 || resolved.Duration <= 0 {
		return false
	}
	diff := expectedDurationSec - resolved.Duration
	if diff < 0 {
		diff = -diff
	}
	return diff <= 10
}

func hasStrongTrackIdentity(req DownloadRequest, resolved resolvedTrackInfo) bool {
	if req.TrackName == "" || resolved.Title == "" ||
		req.ArtistName == "" || resolved.ArtistName == "" {
		return false
	}

	titleExact := exactLooseIdentityMatch(req.TrackName, resolved.Title, normalizeTrackIdentityTitle)
	if !titleExact {
		return false
	}

	artistExact := exactLooseIdentityMatch(
		req.ArtistName,
		resolved.ArtistName,
		normalizeLooseArtistName,
	)
	return artistExact || (artistsMatch(req.ArtistName, resolved.ArtistName) &&
		durationMatchesRequest(req, resolved))
}

func trackMatchesRequest(req DownloadRequest, resolved resolvedTrackInfo, logPrefix string) bool {
	exactISRCMatch := req.ISRC != "" &&
		resolved.ISRC != "" &&
		strings.EqualFold(strings.TrimSpace(req.ISRC), strings.TrimSpace(resolved.ISRC))
	conflictingISRC := req.ISRC != "" && resolved.ISRC != "" && !exactISRCMatch

	if !exactISRCMatch && !resolved.SkipNameVerification {
		if req.ArtistName != "" && resolved.ArtistName != "" &&
			!artistsMatch(req.ArtistName, resolved.ArtistName) {
			GoLog("[%s] Verification failed: artist mismatch — expected '%s', got '%s'\n",
				logPrefix, req.ArtistName, resolved.ArtistName)
			return false
		}

		if req.TrackName != "" && resolved.Title != "" &&
			!trackTitlesMatch(req.TrackName, resolved.Title) {
			GoLog("[%s] Verification failed: title mismatch — expected '%s', got '%s'\n",
				logPrefix, req.TrackName, resolved.Title)
			return false
		}

		if req.AlbumName != "" && resolved.AlbumName != "" &&
			!titlesMatch(req.AlbumName, resolved.AlbumName) {
			if conflictingISRC || !hasStrongTrackIdentity(req, resolved) {
				GoLog("[%s] Verification failed: album mismatch — expected '%s', got '%s'\n",
					logPrefix, req.AlbumName, resolved.AlbumName)
				return false
			}
			GoLog("[%s] Verification accepted album mismatch for matching track identity — expected '%s', got '%s'\n",
				logPrefix, req.AlbumName, resolved.AlbumName)
		}
	}

	expectedDurationSec := req.DurationMS / 1000
	if expectedDurationSec > 0 && resolved.Duration > 0 {
		diff := expectedDurationSec - resolved.Duration
		if diff < 0 {
			diff = -diff
		}
		if diff > 10 {
			// Catalog durations can disagree even for the same recording. Require
			// both its ISRC and matching names; a preview still cannot qualify.
			if exactISRCMatch && req.TrackName != "" && resolved.Title != "" &&
				exactLooseIdentityMatch(req.TrackName, resolved.Title, normalizeTrackIdentityTitle) &&
				req.ArtistName != "" && resolved.ArtistName != "" &&
				artistsMatch(req.ArtistName, resolved.ArtistName) &&
				!(resolved.Duration <= 35 && expectedDurationSec > 45) {
				GoLog("[%s] Accepted catalog duration difference for matching ISRC and recording names: expected %ds, got %ds\n",
					logPrefix, expectedDurationSec, resolved.Duration)
				return true
			}
			GoLog("[%s] Verification failed: duration mismatch — expected %ds, got %ds\n",
				logPrefix, expectedDurationSec, resolved.Duration)
			return false
		}
	}

	return true
}

// selectBestMetadataEnrichmentTrack only returns a provider result when it is
// safe to copy missing tags into a download request. Search ordering alone is
// not evidence of identity: providers can put covers, remixes, or unrelated
// same-title recordings first.
func selectBestMetadataEnrichmentTrack(req DownloadRequest, tracks []ExtTrackMetadata) *ExtTrackMetadata {
	var best *ExtTrackMetadata
	bestScore := -1 << 30
	expectedISRC := strings.TrimSpace(req.ISRC)

	for i := range tracks {
		track := &tracks[i]
		candidateISRC := strings.TrimSpace(track.ISRC)
		exactISRCMatch := expectedISRC != "" && candidateISRC != "" &&
			strings.EqualFold(expectedISRC, candidateISRC)
		if expectedISRC != "" && candidateISRC != "" && !exactISRCMatch {
			GoLog("[MetadataEnrichment] Rejected %s result with conflicting ISRC %s\n", track.ProviderID, candidateISRC)
			continue
		}

		resolved := resolvedTrackInfo{
			Title:      track.Name,
			ArtistName: track.Artists,
			AlbumName:  track.AlbumName,
			ISRC:       track.ISRC,
			Duration:   track.DurationMS / 1000,
		}
		if !trackMatchesRequest(req, resolved, "MetadataEnrichment") {
			continue
		}
		if !exactISRCMatch && !hasStrongTrackIdentity(req, resolved) {
			GoLog("[MetadataEnrichment] Rejected low-confidence result: %s - %s\n", track.Name, track.Artists)
			continue
		}

		score := 2000
		if exactISRCMatch {
			score += 10000
		}
		if exactLooseIdentityMatch(req.TrackName, track.Name, normalizeLooseTitle) {
			score += 400
		}
		if exactLooseIdentityMatch(req.ArtistName, track.Artists, normalizeLooseArtistName) {
			score += 320
		}
		if req.AlbumName != "" && track.AlbumName != "" && titlesMatch(req.AlbumName, track.AlbumName) {
			score += 120
		}
		if durationMatchesRequest(req, resolved) {
			score += 80
		}
		if track.ISRC != "" {
			score += 40
		}
		if track.AlbumName != "" {
			score += 30
		}
		if track.ReleaseDate != "" {
			score += 30
		}
		if track.TrackNumber > 0 {
			score += 10
		}

		if best == nil || score > bestScore {
			best = track
			bestScore = score
		}
	}

	return best
}
