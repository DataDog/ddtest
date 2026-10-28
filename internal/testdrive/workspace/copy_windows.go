package workspace

import "golang.org/x/sys/windows"

func cloneFile(_, _ string) (bool, error) { return false, nil }

// FreeSpace reports the bytes available to the current user at path.
func FreeSpace(path string) (uint64, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var available, total, free uint64
	err = windows.GetDiskFreeSpaceEx(name, &available, &total, &free)
	return available, err
}
