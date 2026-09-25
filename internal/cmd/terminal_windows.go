//go:build windows

package cmd

import (
	"os"

	"golang.org/x/sys/windows"
)

func fileIsTerminal(file *os.File) bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(file.Fd()), &mode) == nil
}
