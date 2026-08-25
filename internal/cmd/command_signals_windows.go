//go:build windows

package cmd

import "os"

func commandCancellationSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}
