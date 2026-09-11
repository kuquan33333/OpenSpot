package gobackend

import "strings"

// classifyDownloadErrorType is retained only because the pinned provider
// aggregation code uses this name to recognize signed-session verification
// challenges. OpenSpot deliberately does not vendor SpotiFLAC's application
// download error classifier or download orchestration into Extension Core.
func classifyDownloadErrorType(message string) string {
	normalized := strings.ToLower(strings.TrimSpace(message))
	switch {
	case strings.Contains(normalized, "verification_required"),
		strings.Contains(normalized, "verification required"),
		strings.Contains(normalized, "verify_required"):
		return "verification_required"
	case strings.Contains(normalized, "cancelled"), strings.Contains(normalized, "canceled"):
		return "cancelled"
	default:
		return "extension_error"
	}
}
