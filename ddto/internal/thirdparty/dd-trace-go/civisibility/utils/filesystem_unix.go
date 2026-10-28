//go:build unix

package utils

import (
	"os"
	"syscall"
)

// differentFilesystems reports whether a and b are known to be on different
// filesystems, where renaming from one to the other fails.
func differentFilesystems(a, b string) bool {
	infoA, errA := os.Stat(a)
	infoB, errB := os.Stat(b)
	if errA != nil || errB != nil {
		return false
	}
	statA, okA := infoA.Sys().(*syscall.Stat_t)
	statB, okB := infoB.Sys().(*syscall.Stat_t)
	return okA && okB && statA.Dev != statB.Dev
}
