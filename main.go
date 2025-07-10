package main

import (
	"fmt"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func main() {
	tracer.Start(tracer.WithService("datadog-test-runner"))
	defer tracer.Stop()

	span := tracer.StartSpan("greeting")
	defer span.Finish()

	span.SetTag("greeting", "Hello")

	fmt.Println("Hello, World!")
}
