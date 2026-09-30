package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/runner"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "test" {
		fmt.Fprintln(os.Stderr, "usage: ddtest test [go test flags] [packages]")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(runner.Run(ctx, os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
}
