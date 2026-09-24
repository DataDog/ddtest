//go:build !darwin && !linux && !windows

package cmd

import "os"

func fileIsTerminal(_ *os.File) bool { return false }
