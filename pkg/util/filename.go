package util

import (
	"net/url"
	"strings"
)

// ExtractFilenameFromPath extracts the original filename from a storage path.
// It handles two path formats:
//   - Legacy: "chat/timestamp/uuid_filename" → strips 32-char hex UUID prefix + underscore
//   - New:    "chat/timestamp/uuid/filename" → returns the last path segment
//
// Percent-encoded filenames are decoded. If the extracted filename is empty
// (e.g. legacy path ending in "uuid_"), the full last segment is returned as fallback.
func ExtractFilenameFromPath(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) == 0 {
		return ""
	}
	lastPart := parts[len(parts)-1]

	// Try to strip legacy UUID prefix: 32 hex chars followed by underscore
	if idx := strings.Index(lastPart, "_"); idx == 32 && IsHexString(lastPart[:32]) {
		after := lastPart[idx+1:]
		if after != "" {
			unescaped, err := url.PathUnescape(after)
			if err == nil {
				return unescaped
			}
			return after
		}
		// Empty filename after UUID prefix — fall back to full last segment
	}

	unescaped, err := url.PathUnescape(lastPart)
	if err == nil {
		return unescaped
	}
	return lastPart
}
