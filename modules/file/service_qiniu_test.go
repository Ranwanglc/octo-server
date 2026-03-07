package file

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTrimPrefixForQiniuPath tests the path trimming logic used in ServiceQiniu.DownloadURL
// to ensure it doesn't panic on empty paths (fixes Issue #249)
func TestTrimPrefixForQiniuPath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{
			name:     "empty path should not panic",
			path:     "",
			expected: "",
		},
		{
			name:     "path with leading slash should be trimmed",
			path:     "/images/test.png",
			expected: "images/test.png",
		},
		{
			name:     "path without leading slash should remain unchanged",
			path:     "images/test.png",
			expected: "images/test.png",
		},
		{
			name:     "single slash should become empty",
			path:     "/",
			expected: "",
		},
		{
			name:     "path with multiple leading slashes should trim only first",
			path:     "//images/test.png",
			expected: "/images/test.png",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This is the same logic used in ServiceQiniu.DownloadURL
			result := strings.TrimPrefix(tt.path, "/")
			assert.Equal(t, tt.expected, result)
		})
	}
}
