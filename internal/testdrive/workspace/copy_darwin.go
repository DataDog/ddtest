package workspace

import "golang.org/x/sys/unix"

func cloneFile(source, target string) (bool, error) {
	if err := unix.Clonefile(source, target, 0); err == nil {
		return true, nil
	}
	// Unsupported/cross-device clones fall back to an independent byte copy.
	return false, nil
}

// FreeSpace reports the bytes available to the current user at path.
func FreeSpace(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}
