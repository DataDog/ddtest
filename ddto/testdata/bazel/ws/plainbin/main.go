// Command plainbin imports dd-trace-go v2 and testify outside tests.
package main

import (
	"fmt"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/stretchr/testify/assert"
)

type printer struct{}

func (printer) Errorf(format string, args ...interface{}) { fmt.Printf(format+"\n", args...) }

func main() {
	tracer.Start(tracer.WithLogStartup(false))
	defer tracer.Stop()
	span := tracer.StartSpan("plain")
	span.Finish()
	fmt.Println("equal:", assert.Equal(printer{}, 1, 1))
}
