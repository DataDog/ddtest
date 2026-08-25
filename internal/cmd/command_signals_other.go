//go:build js || plan9 || wasip1

package cmd

import "os"

func commandCancellationSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}
