package utils

import (
	"path/filepath"
	"strings"
)

// differentFilesystems reports whether a and b are known to be on different
// volumes, where renaming from one to the other fails.
func differentFilesystems(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return false
	}
	volumeA, volumeB := filepath.VolumeName(absA), filepath.VolumeName(absB)
	return volumeA != "" && volumeB != "" && !strings.EqualFold(volumeA, volumeB)
}
