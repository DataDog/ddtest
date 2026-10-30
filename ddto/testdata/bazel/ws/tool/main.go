// Command tool imports testing outside a test (as some helpers do).
package main

import (
	"fmt"
	"os"
	"testing"
)

func main() {
	msg := fmt.Sprintf("tool testing.Testing()=%t\n", testing.Testing())
	if len(os.Args) > 1 {
		_ = os.WriteFile(os.Args[1], []byte(msg), 0o644)
	}
	fmt.Print(msg)
}
