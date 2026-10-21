//go:build !linux && !darwin && !windows

package testdrive

import "fmt"

func cloneConfigurationFile(_, _ string) (bool, error) { return false, nil }
func configurationFreeSpace(_ string) (uint64, error) {
	return 0, fmt.Errorf("configuration copy disk checks are unsupported on this platform")
}
