//go:build !unix && !windows

package utils

// differentFilesystems cannot tell filesystems apart here; callers keep their
// first choice.
func differentFilesystems(_, _ string) bool { return false }
