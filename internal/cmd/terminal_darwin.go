//go:build darwin

package cmd

import (
	"os"

	"golang.org/x/sys/unix"
)

func fileIsTerminal(file *os.File) bool {
	_, err := unix.IoctlGetTermios(int(file.Fd()), unix.TIOCGETA)
	return err == nil
}
