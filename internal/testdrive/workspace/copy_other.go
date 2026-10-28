//go:build !linux && !darwin && !windows

package workspace

import "fmt"

func cloneFile(_, _ string) (bool, error) { return false, nil }

// FreeSpace reports the bytes available to the current user at path.
func FreeSpace(_ string) (uint64, error) {
	return 0, fmt.Errorf("workspace disk checks are unsupported on this platform")
}
