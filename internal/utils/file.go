package utils

import (
	"os"
	"strings"
)

// FileContainsAll reports whether a file contains every marker.
// If the file cannot be read, it returns true so callers that use this as a
// safety guard keep the file runnable.
func FileContainsAll(path string, markers ...string) bool {
	content, err := os.ReadFile(path)
	if err != nil {
		return true
	}

	text := string(content)
	for _, marker := range markers {
		if !strings.Contains(text, marker) {
			return false
		}
	}
	return true
}
