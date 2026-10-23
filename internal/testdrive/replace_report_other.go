//go:build !windows

package testdrive

import "os"

func replaceReportFile(source, destination string) error { return os.Rename(source, destination) }
