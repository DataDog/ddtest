package testdrive

import "golang.org/x/sys/windows"

func cloneConfigurationFile(_, _ string) (bool, error) { return false, nil }
func configurationFreeSpace(path string) (uint64, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var available, total, free uint64
	err = windows.GetDiskFreeSpaceEx(name, &available, &total, &free)
	return available, err
}
