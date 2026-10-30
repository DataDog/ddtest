// Command apibin uses Mini's Go API outside a test.
package main

import (
	"fmt"

	"github.com/DataDog/ddtest/ddto/propagation"
	"github.com/DataDog/ddtest/ddto/testopt"
)

func main() {
	_ = testopt.RunM
	_, err := propagation.New()
	fmt.Println("propagation:", err == nil)
}
